package ctxmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"desk/internal/event"
	"desk/internal/memory"
	"desk/internal/skill"
)

// Manager 是每次 LLM Call 的上下文决策者。
type Manager struct {
	Events    *event.Store
	Index     *memory.Index
	Compactor Compactor
	Settings  Settings

	mu      sync.Mutex
	last    map[string]Assembly
	version map[string]int
}

// Assembly 是发给 Worker 的权威 snapshot（不含 system；由 Worker 安装）。
type Assembly struct {
	Messages         []map[string]any
	Applied          Applied
	Rebuild          bool
	CompactionReason string
	Layers           InspectLayers
}

// InspectLayers 供 GET /context 分层展示。
type InspectLayers struct {
	Large          []map[string]any `json:"large"`
	Smalls         []map[string]any `json:"smalls"`
	Facts          []map[string]any `json:"facts"`
	Window         []map[string]any `json:"recent_window"`
	Skill          []map[string]any `json:"skill"`
	Retrieval      []map[string]any `json:"retrieval"`
	Runtime        []map[string]any `json:"runtime"`
	PendingEvicted []SourceRef      `json:"pending_evicted,omitempty"`
}

// PrepareIn 是一次 ask 前的输入。
type PrepareIn struct {
	SessionID    string
	RunID        string
	Workspace    string
	Phase        string
	PromptHash   string
	Runtime      string
	System       string
	Tools        []ToolSpec
	PendingTool  string
	WantRetrieve bool
	SkipCompact  bool
	FrozenHits   []RetrievalHit
}

func New(events *event.Store, memoryIndex *memory.Index, settings Settings) *Manager {
	return &Manager{
		Events:   events,
		Index:    memoryIndex,
		Settings: settings.withDefaults(),
		last:     map[string]Assembly{},
		version:  map[string]int{},
	}
}

// Prepare 推进 durable window/compact 生命周期，再组装发给 Worker 的 snapshot。
func (manager *Manager) Prepare(requestContext context.Context, prepareInput PrepareIn) (Assembly, error) {
	if manager == nil || manager.Events == nil {
		return Assembly{}, fmt.Errorf("ctxmgr_unconfigured")
	}
	settings := manager.Settings.withDefaults()
	reason := ""
	changed := false
	if !prepareInput.SkipCompact {
		compactionReason, lifecycleChanged, err := manager.advanceLifecycle(requestContext, prepareInput, settings)
		if err != nil {
			return Assembly{}, err
		}
		reason, changed = compactionReason, lifecycleChanged
	}
	events, err := manager.Events.ListBySession(requestContext, prepareInput.SessionID)
	if err != nil {
		return Assembly{}, err
	}
	layers := parseLayers(events, prepareInput.RunID, prepareInput.PendingTool)
	retrievalHits := prepareInput.FrozenHits
	if retrievalHits == nil {
		retrievalHits = manager.retrieve(requestContext, prepareInput)
	}
	contextAssembly, err := manager.assemble(requestContext, prepareInput, layers, events, retrievalHits, settings, reason, changed)
	if err != nil {
		return Assembly{}, err
	}
	if prepareInput.SkipCompact {
		return contextAssembly, nil
	}
	manager.mu.Lock()
	manager.version[prepareInput.RunID]++
	contextAssembly.Applied.Version = manager.version[prepareInput.RunID]
	manager.last[prepareInput.RunID] = contextAssembly
	manager.mu.Unlock()
	return contextAssembly, nil
}

// Last 返回本 Run 最近一次 Prepare 的组装。
func (manager *Manager) Last(runID string) (Assembly, bool) {
	if manager == nil {
		return Assembly{}, false
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	lastAssembly, ok := manager.last[runID]
	return lastAssembly, ok
}

// Forget 丢掉进程内 last，供 durable inspect 测试。
func (manager *Manager) Forget(runID string) {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	delete(manager.last, runID)
	manager.mu.Unlock()
}

// Inspect 优先进程内 assembled；否则从最后一条 context.applied 重建。
// reconstructable 不是 byte-for-byte 历史 Prompt 回放：只有 hash 与 applied 元数据，没有 prompt 正文快照。
func (manager *Manager) Inspect(requestContext context.Context, sessionID, runID string) (Assembly, string, bool) {
	if manager == nil {
		return Assembly{}, "", false
	}
	if lastAssembly, ok := manager.Last(runID); ok {
		return lastAssembly, "assembled", true
	}
	if manager.Events == nil {
		return Assembly{}, "", false
	}
	events, err := manager.Events.ListBySession(requestContext, sessionID)
	if err != nil {
		return Assembly{}, "", false
	}
	var applied *Applied
	for i := range events {
		e := events[i]
		if e.RunID != runID || e.Type != event.TypeContextApplied {
			continue
		}
		var p Applied
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		cp := p
		applied = &cp
	}
	if applied == nil {
		return Assembly{}, "", false
	}
	retrievalHits := applied.Retrieval
	if retrievalHits == nil {
		retrievalHits = []RetrievalHit{}
	}
	contextAssembly, err := manager.Prepare(requestContext, PrepareIn{
		SessionID:   sessionID,
		RunID:       runID,
		SkipCompact: true,
		FrozenHits:  retrievalHits,
		PromptHash:  applied.PromptHash,
	})
	if err != nil {
		return Assembly{}, "", false
	}
	contextAssembly.Applied = *applied
	return contextAssembly, "reconstructable", true
}

func (manager *Manager) advanceLifecycle(requestContext context.Context, prepareInput PrepareIn, settings Settings) (string, bool, error) {
	events, err := manager.Events.ListBySession(requestContext, prepareInput.SessionID)
	if err != nil {
		return "", false, err
	}
	changed := false
	compactionReason := ""
	retrievalHits := prepareInput.FrozenHits
	if retrievalHits == nil {
		retrievalHits = manager.retrieve(requestContext, prepareInput)
	}
	userText := firstUser(requestContext, manager.Events, prepareInput.RunID)
	skills := skill.InjectPaths(prepareInput.Workspace, skillPaths(requestContext, manager, prepareInput, userText))
	retrievalMessages := []map[string]any{}
	if retrievalMessage := retrievalBlock(retrievalHits); retrievalMessage != nil {
		retrievalMessages = []map[string]any{retrievalMessage}
	}
	for pass := 0; pass < 6; pass++ {
		layers := parseLayers(events, prepareInput.RunID, prepareInput.PendingTool)
		stable := stableMsgs(layers, true)
		reserved := reservedTokens(prepareInput)
		dynamicTailTokens := EstimateMessages(skills) + EstimateMessages(retrievalMessages) + EstimateTokens(prepareInput.Runtime)
		pendingToolMinTokens := 0
		if n := len(layers.window); n > 0 && layers.window[n-1].Pending {
			pendingToolMinTokens = layers.window[n-1].tokens()
		}
		if reserved+EstimateMessages(stable)+pendingToolMinTokens+dynamicTailTokens > settings.TotalTokens {
			ok, _, err := manager.maybeLarge(requestContext, prepareInput.RunID, events, settings, true)
			if err != nil {
				return "", changed, err
			}
			if ok {
				changed = true
				compactionReason = "large_compact"
				events, err = manager.Events.ListBySession(requestContext, prepareInput.SessionID)
				if err != nil {
					return compactionReason, changed, err
				}
				continue
			}
			stable = stableMsgs(layers, false)
			if len(retrievalMessages) > 0 {
				retrievalMessages = nil
			} else if len(skills) > 0 {
				skills = nil
			}
			dynamicTailTokens = EstimateMessages(skills) + EstimateMessages(retrievalMessages) + EstimateTokens(prepareInput.Runtime)
		}
		remaining := settings.TotalTokens - reserved - EstimateMessages(stable) - dynamicTailTokens
		if remaining < 0 {
			remaining = 0
		}
		windowBudget := settings.WindowTokens
		if remaining < windowBudget {
			windowBudget = remaining
		}
		progress := false
		_, newlyEvicted := splitUnits(layers.window, windowBudget)
		if len(newlyEvicted) > 0 {
			if !manager.compactReady(settings) {
				break
			}
			if err := manager.appendJSON(requestContext, prepareInput.RunID, event.TypeContextEvicted, EvictPayload{BasedOn: unitRefs(newlyEvicted)}); err != nil {
				return compactionReason, changed, err
			}
			changed = true
			progress = true
			if compactionReason == "" {
				compactionReason = "window_evict"
			}
			events, err = manager.Events.ListBySession(requestContext, prepareInput.SessionID)
			if err != nil {
				return compactionReason, changed, err
			}
		}
		if shouldSmallCompact, units := manager.pendingSmallUnits(events, prepareInput.RunID, prepareInput.PendingTool, settings); shouldSmallCompact {
			if err := manager.runCompact(requestContext, prepareInput.RunID, units, 0); err != nil {
				_ = manager.appendJSON(requestContext, prepareInput.RunID, event.TypeContextCompactFailed, CompactFailPayload{
					Kind: "small", BasedOn: unitRefs(units), Error: err.Error(),
				})
				changed = true
			} else {
				compactionReason = "small_compact"
				changed = true
				progress = true
				events, err = manager.Events.ListBySession(requestContext, prepareInput.SessionID)
				if err != nil {
					return compactionReason, changed, err
				}
			}
		}
		ok, largeCompactReason, err := manager.maybeLarge(requestContext, prepareInput.RunID, events, settings, false)
		if err != nil {
			return compactionReason, changed, err
		}
		if ok {
			changed = true
			progress = true
			compactionReason = largeCompactReason
			events, err = manager.Events.ListBySession(requestContext, prepareInput.SessionID)
			if err != nil {
				return compactionReason, changed, err
			}
		}
		if !progress {
			break
		}
	}
	return compactionReason, changed, nil
}

func (manager *Manager) compactReady(settings Settings) bool {
	return manager != nil && manager.Compactor != nil && settings.PromptsDir != ""
}

func (manager *Manager) pendingSmallUnits(events []event.Event, runID, pendingToolID string, settings Settings) (bool, []contextUnit) {
	refs := pendingEvictedRefs(events)
	if len(refs) == 0 {
		return false, nil
	}
	allowedSources := allowedMap(refs)
	allWindowUnits := buildUnits(events, runID, pendingToolID, skipCompacted(events))
	var smallCompactUnits []contextUnit
	for _, u := range allWindowUnits {
		for _, it := range u.Items {
			if _, ok := allowedSources[sourceKey(it.Ref)]; ok {
				smallCompactUnits = append(smallCompactUnits, u)
				break
			}
		}
	}
	if EstimateTokens(joinUnits(smallCompactUnits)) < settings.SmallTriggerTok {
		return false, smallCompactUnits
	}
	if blockedByFail(events, "small") {
		return false, smallCompactUnits
	}
	if manager.Compactor == nil || settings.PromptsDir == "" {
		return false, smallCompactUnits
	}
	return true, smallCompactUnits
}

func (manager *Manager) maybeLarge(requestContext context.Context, runID string, events []event.Event, settings Settings, force bool) (bool, string, error) {
	if manager.Compactor == nil || settings.PromptsDir == "" {
		return false, "", nil
	}
	if blockedByFail(events, "large") {
		return false, "", nil
	}
	layers := parseLayers(events, runID, "")
	if len(layers.smalls) == 0 {
		return false, "", nil
	}
	if !force && len(layers.smalls) < settings.LargeSmallCount && EstimateTokens(smallsText(layers.smalls)) < settings.LargeTriggerTok {
		return false, "", nil
	}
	maxOutputTokens := settings.TotalTokens / 3
	if err := manager.runLarge(requestContext, runID, layers, maxOutputTokens); err != nil {
		var absorbs []SourceRef
		for _, s := range layers.smalls {
			absorbs = append(absorbs, SourceRef{RunID: s.RunID, Seq: s.Seq})
		}
		_ = manager.appendJSON(requestContext, runID, event.TypeContextCompactFailed, CompactFailPayload{
			Kind: "large", BasedOn: absorbs, Error: err.Error(),
		})
		return false, "", nil
	}
	return true, "large_compact", nil
}

func (manager *Manager) runCompact(requestContext context.Context, runID string, units []contextUnit, maxOutputTokens int) error {
	compactSystemPrompt, err := LoadPrompt(manager.Settings.PromptsDir, "small")
	if err != nil {
		return err
	}
	compactUserPrompt, refs, inputTokens := evictedJSON(units)
	compactJSON, err := manager.Compactor.Compact(requestContext, "small", compactSystemPrompt, compactUserPrompt)
	if err != nil {
		return err
	}
	compactResult, err := ValidateResult(compactJSON, refs, inputTokens, maxOutputTokens)
	if err != nil {
		return err
	}
	return manager.appendJSON(requestContext, runID, event.TypeContextSmallCompact, CompactPayload{
		Summary: compactResult.Summary, Facts: compactResult.Facts,
		OpenItems: compactResult.OpenItems, Decisions: compactResult.Decisions,
		BasedOn: refs,
	})
}

func (manager *Manager) runLarge(requestContext context.Context, runID string, layers layer, maxOutputTokens int) error {
	compactSystemPrompt, err := LoadPrompt(manager.Settings.PromptsDir, "large")
	if err != nil {
		return err
	}
	var refs []SourceRef
	if layers.large != nil {
		refs = append(refs, SourceRef{RunID: layers.large.RunID, Seq: layers.large.Seq})
	}
	var absorbs []SourceRef
	for _, s := range layers.smalls {
		r := SourceRef{RunID: s.RunID, Seq: s.Seq}
		refs = append(refs, r)
		absorbs = append(absorbs, r)
	}
	body, _ := json.Marshal(map[string]any{
		"allowed_sources": refs,
		"previous_large":  payloadMap(layers.large),
		"smalls":          smallPayloads(layers.smalls),
	})
	inputTokens := EstimateTokens(string(body))
	compactJSON, err := manager.Compactor.Compact(requestContext, "large", compactSystemPrompt, string(body))
	if err != nil {
		return err
	}
	compactResult, err := ValidateResult(compactJSON, refs, inputTokens, maxOutputTokens)
	if err != nil {
		return err
	}
	return manager.appendJSON(requestContext, runID, event.TypeContextLargeCompact, CompactPayload{
		Summary: compactResult.Summary, Facts: compactResult.Facts,
		OpenItems: compactResult.OpenItems, Decisions: compactResult.Decisions,
		BasedOn: refs, Absorbs: absorbs,
	})
}

func (manager *Manager) appendJSON(requestContext context.Context, runID, typ string, payload any) error {
	tx, err := manager.Events.DB.BeginTx(requestContext, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := manager.Events.Append(requestContext, tx, runID, typ, payload); err != nil {
		return err
	}
	return tx.Commit()
}

func (manager *Manager) retrieve(requestContext context.Context, prepareInput PrepareIn) []RetrievalHit {
	if !prepareInput.WantRetrieve || manager.Index == nil {
		return nil
	}
	searchQuery := strings.TrimSpace(firstUser(requestContext, manager.Events, prepareInput.RunID) + " " + lastTask(requestContext, manager.Events, prepareInput.RunID))
	if searchQuery == "" {
		return nil
	}
	retrievalK := manager.Settings.withDefaults().RetrievalK
	retrievalHits, err := manager.Index.Search(requestContext, searchQuery, retrievalK)
	if err != nil {
		return nil
	}
	clippedHits := make([]RetrievalHit, 0, len(retrievalHits))
	for _, memoryHit := range retrievalHits {
		text := memoryHit.Text
		if utf8.RuneCountInString(text) > 200 {
			text = string([]rune(text)[:200])
		}
		clippedHits = append(clippedHits, RetrievalHit{RunID: memoryHit.RunID, Seq: memoryHit.Seq, Kind: memoryHit.Kind, Text: text})
	}
	return clippedHits
}

func firstUser(requestContext context.Context, eventStore *event.Store, runID string) string {
	userText, err := eventStore.FirstUserText(requestContext, runID)
	if err != nil {
		return ""
	}
	return userText
}

func lastTask(requestContext context.Context, eventStore *event.Store, runID string) string {
	taskTitle, err := eventStore.LastTaskTitle(requestContext, runID)
	if err != nil {
		return ""
	}
	return taskTitle
}

func (manager *Manager) assemble(requestContext context.Context, prepareInput PrepareIn, layers layer, events []event.Event, retrievalHits []RetrievalHit, settings Settings, compactWhy string, lifecycleChanged bool) (Assembly, error) {
	omitFacts := false
	stable := stableMsgs(layers, true)
	userText := firstUser(requestContext, manager.Events, prepareInput.RunID)
	skills := skill.InjectPaths(prepareInput.Workspace, skillPaths(requestContext, manager, prepareInput, userText))
	retrievalMessages := []map[string]any{}
	if retrievalMessage := retrievalBlock(retrievalHits); retrievalMessage != nil {
		retrievalMessages = []map[string]any{retrievalMessage}
	}
	window := layers.window
	reserved := reservedTokens(prepareInput)
	dynamicTailTokens := func() int {
		return EstimateMessages(skills) + EstimateMessages(retrievalMessages) + EstimateTokens(prepareInput.Runtime)
	}
	pendingToolMinTokens := 0
	if n := len(window); n > 0 && window[n-1].Pending {
		pendingToolMinTokens = window[n-1].tokens()
	}
	if reserved+EstimateMessages(stable)+pendingToolMinTokens+dynamicTailTokens() > settings.TotalTokens {
		omitFacts = true
		stable = stableMsgs(layers, false)
	}
	for reserved+EstimateMessages(stable)+EstimateMessages(unitMsgs(window))+dynamicTailTokens() > settings.TotalTokens {
		if len(retrievalMessages) > 0 {
			retrievalMessages = nil
			continue
		}
		if len(skills) > 0 {
			skills = nil
			continue
		}
		break
	}
	var inspectLayers InspectLayers
	if layers.large != nil {
		inspectLayers.Large = []map[string]any{compactBlock("LARGE", *layers.large)}
	}
	for _, s := range layers.smalls {
		inspectLayers.Smalls = append(inspectLayers.Smalls, compactBlock("SMALL", s))
	}
	if !omitFacts {
		if factsMessage := factsBlock(layers.facts); factsMessage != nil {
			inspectLayers.Facts = []map[string]any{factsMessage}
		}
	}
	windowMessages := unitMsgs(window)
	inspectLayers.Window = windowMessages
	inspectLayers.Skill = skills
	inspectLayers.Retrieval = retrievalMessages
	inspectLayers.PendingEvicted = pendingEvictedRefs(events)
	if prepareInput.Runtime != "" {
		inspectLayers.Runtime = []map[string]any{{"role": "user", "content": prepareInput.Runtime}}
	}
	var assembledMessages []map[string]any
	assembledMessages = append(assembledMessages, stable...)
	assembledMessages = append(assembledMessages, windowMessages...)
	assembledMessages = append(assembledMessages, skills...)
	assembledMessages = append(assembledMessages, retrievalMessages...)
	if assembledMessages == nil {
		assembledMessages = []map[string]any{}
	}
	remaining := settings.TotalTokens - reserved - EstimateMessages(stable) - dynamicTailTokens()
	if remaining < 0 {
		remaining = 0
	}
	windowBudget := settings.WindowTokens
	if remaining < windowBudget {
		windowBudget = remaining
	}
	systemEstimate := EstimateSystem(prepareInput.System)
	toolsEstimate := EstimateTools(prepareInput.Tools)
	totalEstimate := EstimateLLMInput(prepareInput.System, prepareInput.Tools, assembledMessages, prepareInput.Runtime)
	overBudgetReason := ""
	if totalEstimate > settings.TotalTokens {
		if n := len(window); n > 0 && window[n-1].Pending {
			overBudgetReason = "pending_tool"
		} else if prepareInput.SkipCompact {
			overBudgetReason = "reconstruct"
		} else {
			return Assembly{}, fmt.Errorf("context_over_budget")
		}
	}
	applied := Applied{
		PromptHash:       prepareInput.PromptHash,
		WindowTokens:     settings.WindowTokens,
		WindowBudget:     windowBudget,
		WindowEstimate:   EstimateMessages(windowMessages),
		TotalTokens:      settings.TotalTokens,
		TotalEstimate:    totalEstimate,
		InputEstimate:    EstimateMessages(assembledMessages),
		SystemEstimate:   systemEstimate,
		ToolsEstimate:    toolsEstimate,
		FactCount:        len(layers.facts),
		SkillCount:       len(skills),
		PendingEvicted:   inspectLayers.PendingEvicted,
		Retrieval:        retrievalHits,
		CompactionReason: compactWhy,
		OverBudget:       overBudgetReason,
	}
	if omitFacts {
		applied.FactCount = 0
	}
	if len(retrievalMessages) == 0 {
		applied.Retrieval = nil
	}
	if layers.large != nil {
		applied.LargeSeq = layers.large.Seq
		applied.LargeRunID = layers.large.RunID
	}
	for _, s := range layers.smalls {
		applied.SmallRefs = append(applied.SmallRefs, SourceRef{RunID: s.RunID, Seq: s.Seq})
	}
	rebuild := compactWhy != "" || lifecycleChanged
	if compactWhy != "" {
		applied.RebuildReason = compactWhy
	} else if lifecycleChanged {
		applied.RebuildReason = "window_evict"
	}
	return Assembly{Messages: assembledMessages, Applied: applied, Rebuild: rebuild, CompactionReason: compactWhy, Layers: inspectLayers}, nil
}

func skillPaths(requestContext context.Context, manager *Manager, prepareInput PrepareIn, query string) []string {
	if manager.Index == nil || query == "" {
		return nil
	}
	retrievalHits, err := manager.Index.Search(requestContext, query, 16)
	if err != nil {
		return nil
	}
	return skill.PathsFromHits(retrievalHits, func(hit memory.Hit) string {
		runEvent, err := manager.Events.Get(requestContext, hit.RunID, hit.Seq)
		if err != nil {
			return ""
		}
		var payload struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(runEvent.Payload, &payload) != nil {
			return ""
		}
		return payload.Path
	})
}

func splitUnits(units []contextUnit, budget int) (kept, evicted []contextUnit) {
	total := 0
	for _, u := range units {
		total += u.tokens()
	}
	if total <= budget {
		return units, nil
	}
	i := 0
	for i < len(units) && total > budget {
		if i == len(units)-1 && units[i].Pending {
			break
		}
		total -= units[i].tokens()
		i++
	}
	if i >= len(units) {
		return nil, units
	}
	return units[i:], units[:i]
}

func joinUnits(units []contextUnit) string {
	return joinItems(flattenUnits(units))
}

func joinItems(items []windowItem) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString(messageText(it.Msg))
	}
	return b.String()
}

func smallsText(smalls []event.Event) string {
	var b strings.Builder
	for _, s := range smalls {
		b.Write(s.Payload)
	}
	return b.String()
}

func payloadMap(e *event.Event) any {
	if e == nil {
		return nil
	}
	var payloadValue any
	_ = json.Unmarshal(e.Payload, &payloadValue)
	return payloadValue
}

func smallPayloads(smalls []event.Event) []any {
	var payloads []any
	for i := range smalls {
		payloads = append(payloads, payloadMap(&smalls[i]))
	}
	return payloads
}
