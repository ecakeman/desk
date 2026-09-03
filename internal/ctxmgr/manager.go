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

func New(events *event.Store, idx *memory.Index, st Settings) *Manager {
	return &Manager{
		Events:   events,
		Index:    idx,
		Settings: st.withDefaults(),
		last:     map[string]Assembly{},
		version:  map[string]int{},
	}
}

// Prepare 推进 durable window/compact 生命周期，再组装发给 Worker 的 snapshot。
func (m *Manager) Prepare(ctx context.Context, in PrepareIn) (Assembly, error) {
	if m == nil || m.Events == nil {
		return Assembly{}, fmt.Errorf("ctxmgr_unconfigured")
	}
	st := m.Settings.withDefaults()
	reason := ""
	changed := false
	if !in.SkipCompact {
		why, ch, err := m.advanceLifecycle(ctx, in, st)
		if err != nil {
			return Assembly{}, err
		}
		reason, changed = why, ch
	}
	events, err := m.Events.ListBySession(ctx, in.SessionID)
	if err != nil {
		return Assembly{}, err
	}
	layers := parseLayers(events, in.RunID, in.PendingTool)
	hits := in.FrozenHits
	if hits == nil {
		hits = m.retrieve(ctx, in)
	}
	asm, err := m.assemble(ctx, in, layers, events, hits, st, reason, changed)
	if err != nil {
		return Assembly{}, err
	}
	if in.SkipCompact {
		return asm, nil
	}
	m.mu.Lock()
	m.version[in.RunID]++
	asm.Applied.Version = m.version[in.RunID]
	m.last[in.RunID] = asm
	m.mu.Unlock()
	return asm, nil
}

// Last 返回本 Run 最近一次 Prepare 的组装。
func (m *Manager) Last(runID string) (Assembly, bool) {
	if m == nil {
		return Assembly{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.last[runID]
	return a, ok
}

// Forget 丢掉进程内 last，供 durable inspect 测试。
func (m *Manager) Forget(runID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.last, runID)
	m.mu.Unlock()
}

// Inspect 优先进程内 assembled；否则从最后一条 context.applied 重建。
// reconstructable 不是 byte-for-byte 历史 Prompt 回放：只有 hash 与 applied 元数据，没有 prompt 正文快照。
func (m *Manager) Inspect(ctx context.Context, sessionID, runID string) (Assembly, string, bool) {
	if m == nil {
		return Assembly{}, "", false
	}
	if a, ok := m.Last(runID); ok {
		return a, "assembled", true
	}
	if m.Events == nil {
		return Assembly{}, "", false
	}
	events, err := m.Events.ListBySession(ctx, sessionID)
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
	hits := applied.Retrieval
	if hits == nil {
		hits = []RetrievalHit{}
	}
	asm, err := m.Prepare(ctx, PrepareIn{
		SessionID:   sessionID,
		RunID:       runID,
		SkipCompact: true,
		FrozenHits:  hits,
		PromptHash:  applied.PromptHash,
	})
	if err != nil {
		return Assembly{}, "", false
	}
	asm.Applied = *applied
	return asm, "reconstructable", true
}

func (m *Manager) advanceLifecycle(ctx context.Context, in PrepareIn, st Settings) (string, bool, error) {
	events, err := m.Events.ListBySession(ctx, in.SessionID)
	if err != nil {
		return "", false, err
	}
	changed := false
	why := ""
	hits := in.FrozenHits
	if hits == nil {
		hits = m.retrieve(ctx, in)
	}
	userText := firstUser(ctx, m.Events, in.RunID)
	skills := skill.InjectPaths(in.Workspace, skillPaths(ctx, m, in, userText))
	ret := []map[string]any{}
	if rb := retrievalBlock(hits); rb != nil {
		ret = []map[string]any{rb}
	}
	for pass := 0; pass < 6; pass++ {
		layers := parseLayers(events, in.RunID, in.PendingTool)
		stable := stableMsgs(layers, true)
		reserved := reservedTokens(in)
		dyn := EstimateMessages(skills) + EstimateMessages(ret) + EstimateTokens(in.Runtime)
		pendingMin := 0
		if n := len(layers.window); n > 0 && layers.window[n-1].Pending {
			pendingMin = layers.window[n-1].tokens()
		}
		if reserved+EstimateMessages(stable)+pendingMin+dyn > st.TotalTokens {
			ok, _, err := m.maybeLarge(ctx, in.RunID, events, st, true)
			if err != nil {
				return "", changed, err
			}
			if ok {
				changed = true
				why = "large_compact"
				events, err = m.Events.ListBySession(ctx, in.SessionID)
				if err != nil {
					return why, changed, err
				}
				continue
			}
			stable = stableMsgs(layers, false)
			if len(ret) > 0 {
				ret = nil
			} else if len(skills) > 0 {
				skills = nil
			}
			dyn = EstimateMessages(skills) + EstimateMessages(ret) + EstimateTokens(in.Runtime)
		}
		remaining := st.TotalTokens - reserved - EstimateMessages(stable) - dyn
		if remaining < 0 {
			remaining = 0
		}
		windowBudget := st.WindowTokens
		if remaining < windowBudget {
			windowBudget = remaining
		}
		progress := false
		_, newly := splitUnits(layers.window, windowBudget)
		if len(newly) > 0 {
			if !m.compactReady(st) {
				break
			}
			if err := m.appendJSON(ctx, in.RunID, event.TypeContextEvicted, EvictPayload{BasedOn: unitRefs(newly)}); err != nil {
				return why, changed, err
			}
			changed = true
			progress = true
			if why == "" {
				why = "window_evict"
			}
			events, err = m.Events.ListBySession(ctx, in.SessionID)
			if err != nil {
				return why, changed, err
			}
		}
		if try, units := m.pendingSmallUnits(events, in.RunID, in.PendingTool, st); try {
			if err := m.runCompact(ctx, in.RunID, units, 0); err != nil {
				_ = m.appendJSON(ctx, in.RunID, event.TypeContextCompactFailed, CompactFailPayload{
					Kind: "small", BasedOn: unitRefs(units), Error: err.Error(),
				})
				changed = true
			} else {
				why = "small_compact"
				changed = true
				progress = true
				events, err = m.Events.ListBySession(ctx, in.SessionID)
				if err != nil {
					return why, changed, err
				}
			}
		}
		ok, lwhy, err := m.maybeLarge(ctx, in.RunID, events, st, false)
		if err != nil {
			return why, changed, err
		}
		if ok {
			changed = true
			progress = true
			why = lwhy
			events, err = m.Events.ListBySession(ctx, in.SessionID)
			if err != nil {
				return why, changed, err
			}
		}
		if !progress {
			break
		}
	}
	return why, changed, nil
}

func (m *Manager) compactReady(st Settings) bool {
	return m != nil && m.Compactor != nil && st.PromptsDir != ""
}

func (m *Manager) pendingSmallUnits(events []event.Event, runID, pending string, st Settings) (bool, []contextUnit) {
	refs := pendingEvictedRefs(events)
	if len(refs) == 0 {
		return false, nil
	}
	want := allowedMap(refs)
	unitsAll := buildUnits(events, runID, pending, skipCompacted(events))
	var picked []contextUnit
	for _, u := range unitsAll {
		for _, it := range u.Items {
			if _, ok := want[sourceKey(it.Ref)]; ok {
				picked = append(picked, u)
				break
			}
		}
	}
	if EstimateTokens(joinUnits(picked)) < st.SmallTriggerTok {
		return false, picked
	}
	if blockedByFail(events, "small") {
		return false, picked
	}
	if m.Compactor == nil || st.PromptsDir == "" {
		return false, picked
	}
	return true, picked
}

func (m *Manager) maybeLarge(ctx context.Context, runID string, events []event.Event, st Settings, force bool) (bool, string, error) {
	if m.Compactor == nil || st.PromptsDir == "" {
		return false, "", nil
	}
	if blockedByFail(events, "large") {
		return false, "", nil
	}
	layers := parseLayers(events, runID, "")
	if len(layers.smalls) == 0 {
		return false, "", nil
	}
	if !force && len(layers.smalls) < st.LargeSmallCount && EstimateTokens(smallsText(layers.smalls)) < st.LargeTriggerTok {
		return false, "", nil
	}
	maxOut := st.TotalTokens / 3
	if err := m.runLarge(ctx, runID, layers, maxOut); err != nil {
		var absorbs []SourceRef
		for _, s := range layers.smalls {
			absorbs = append(absorbs, SourceRef{RunID: s.RunID, Seq: s.Seq})
		}
		_ = m.appendJSON(ctx, runID, event.TypeContextCompactFailed, CompactFailPayload{
			Kind: "large", BasedOn: absorbs, Error: err.Error(),
		})
		return false, "", nil
	}
	return true, "large_compact", nil
}

func (m *Manager) runCompact(ctx context.Context, runID string, units []contextUnit, maxOut int) error {
	system, err := LoadPrompt(m.Settings.PromptsDir, "small")
	if err != nil {
		return err
	}
	user, refs, inTok := evictedJSON(units)
	raw, err := m.Compactor.Compact(ctx, "small", system, user)
	if err != nil {
		return err
	}
	res, err := ValidateResult(raw, refs, inTok, maxOut)
	if err != nil {
		return err
	}
	return m.appendJSON(ctx, runID, event.TypeContextSmallCompact, CompactPayload{
		Summary: res.Summary, Facts: res.Facts,
		OpenItems: res.OpenItems, Decisions: res.Decisions,
		BasedOn: refs,
	})
}

func (m *Manager) runLarge(ctx context.Context, runID string, layers layer, maxOut int) error {
	system, err := LoadPrompt(m.Settings.PromptsDir, "large")
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
	inTok := EstimateTokens(string(body))
	raw, err := m.Compactor.Compact(ctx, "large", system, string(body))
	if err != nil {
		return err
	}
	res, err := ValidateResult(raw, refs, inTok, maxOut)
	if err != nil {
		return err
	}
	return m.appendJSON(ctx, runID, event.TypeContextLargeCompact, CompactPayload{
		Summary: res.Summary, Facts: res.Facts,
		OpenItems: res.OpenItems, Decisions: res.Decisions,
		BasedOn: refs, Absorbs: absorbs,
	})
}

func (m *Manager) appendJSON(ctx context.Context, runID, typ string, payload any) error {
	tx, err := m.Events.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := m.Events.Append(ctx, tx, runID, typ, payload); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) retrieve(ctx context.Context, in PrepareIn) []RetrievalHit {
	if !in.WantRetrieve || m.Index == nil {
		return nil
	}
	q := strings.TrimSpace(firstUser(ctx, m.Events, in.RunID) + " " + lastTask(ctx, m.Events, in.RunID))
	if q == "" {
		return nil
	}
	k := m.Settings.withDefaults().RetrievalK
	hits, err := m.Index.Search(ctx, q, k)
	if err != nil {
		return nil
	}
	out := make([]RetrievalHit, 0, len(hits))
	for _, h := range hits {
		text := h.Text
		if utf8.RuneCountInString(text) > 200 {
			text = string([]rune(text)[:200])
		}
		out = append(out, RetrievalHit{RunID: h.RunID, Seq: h.Seq, Kind: h.Kind, Text: text})
	}
	return out
}

func firstUser(ctx context.Context, ev *event.Store, runID string) string {
	t, err := ev.FirstUserText(ctx, runID)
	if err != nil {
		return ""
	}
	return t
}

func lastTask(ctx context.Context, ev *event.Store, runID string) string {
	t, err := ev.LastTaskTitle(ctx, runID)
	if err != nil {
		return ""
	}
	return t
}

func (m *Manager) assemble(ctx context.Context, in PrepareIn, layers layer, events []event.Event, hits []RetrievalHit, st Settings, compactWhy string, lifecycleChanged bool) (Assembly, error) {
	omitFacts := false
	stable := stableMsgs(layers, true)
	userText := firstUser(ctx, m.Events, in.RunID)
	skills := skill.InjectPaths(in.Workspace, skillPaths(ctx, m, in, userText))
	ret := []map[string]any{}
	if rb := retrievalBlock(hits); rb != nil {
		ret = []map[string]any{rb}
	}
	window := layers.window
	reserved := reservedTokens(in)
	dyn := func() int {
		return EstimateMessages(skills) + EstimateMessages(ret) + EstimateTokens(in.Runtime)
	}
	pendingMin := 0
	if n := len(window); n > 0 && window[n-1].Pending {
		pendingMin = window[n-1].tokens()
	}
	if reserved+EstimateMessages(stable)+pendingMin+dyn() > st.TotalTokens {
		omitFacts = true
		stable = stableMsgs(layers, false)
	}
	for reserved+EstimateMessages(stable)+EstimateMessages(unitMsgs(window))+dyn() > st.TotalTokens {
		if len(ret) > 0 {
			ret = nil
			continue
		}
		if len(skills) > 0 {
			skills = nil
			continue
		}
		break
	}
	var ly InspectLayers
	if layers.large != nil {
		ly.Large = []map[string]any{compactBlock("LARGE", *layers.large)}
	}
	for _, s := range layers.smalls {
		ly.Smalls = append(ly.Smalls, compactBlock("SMALL", s))
	}
	if !omitFacts {
		if fb := factsBlock(layers.facts); fb != nil {
			ly.Facts = []map[string]any{fb}
		}
	}
	winMsgs := unitMsgs(window)
	ly.Window = winMsgs
	ly.Skill = skills
	ly.Retrieval = ret
	ly.PendingEvicted = pendingEvictedRefs(events)
	if in.Runtime != "" {
		ly.Runtime = []map[string]any{{"role": "user", "content": in.Runtime}}
	}
	var msgs []map[string]any
	msgs = append(msgs, stable...)
	msgs = append(msgs, winMsgs...)
	msgs = append(msgs, skills...)
	msgs = append(msgs, ret...)
	if msgs == nil {
		msgs = []map[string]any{}
	}
	remaining := st.TotalTokens - reserved - EstimateMessages(stable) - dyn()
	if remaining < 0 {
		remaining = 0
	}
	windowBudget := st.WindowTokens
	if remaining < windowBudget {
		windowBudget = remaining
	}
	sysEst := EstimateSystem(in.System)
	toolEst := EstimateTools(in.Tools)
	totalEst := EstimateLLMInput(in.System, in.Tools, msgs, in.Runtime)
	over := ""
	if totalEst > st.TotalTokens {
		if n := len(window); n > 0 && window[n-1].Pending {
			over = "pending_tool"
		} else if in.SkipCompact {
			over = "reconstruct"
		} else {
			return Assembly{}, fmt.Errorf("context_over_budget")
		}
	}
	applied := Applied{
		PromptHash:       in.PromptHash,
		WindowTokens:     st.WindowTokens,
		WindowBudget:     windowBudget,
		WindowEstimate:   EstimateMessages(winMsgs),
		TotalTokens:      st.TotalTokens,
		TotalEstimate:    totalEst,
		InputEstimate:    EstimateMessages(msgs),
		SystemEstimate:   sysEst,
		ToolsEstimate:    toolEst,
		FactCount:        len(layers.facts),
		SkillCount:       len(skills),
		PendingEvicted:   ly.PendingEvicted,
		Retrieval:        hits,
		CompactionReason: compactWhy,
		OverBudget:       over,
	}
	if omitFacts {
		applied.FactCount = 0
	}
	if len(ret) == 0 {
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
	return Assembly{Messages: msgs, Applied: applied, Rebuild: rebuild, CompactionReason: compactWhy, Layers: ly}, nil
}

func skillPaths(ctx context.Context, m *Manager, in PrepareIn, query string) []string {
	if m.Index == nil || query == "" {
		return nil
	}
	hits, err := m.Index.Search(ctx, query, 16)
	if err != nil {
		return nil
	}
	return skill.PathsFromHits(hits, func(hit memory.Hit) string {
		ev, err := m.Events.Get(ctx, hit.RunID, hit.Seq)
		if err != nil {
			return ""
		}
		var payload struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(ev.Payload, &payload) != nil {
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
	var m any
	_ = json.Unmarshal(e.Payload, &m)
	return m
}

func smallPayloads(smalls []event.Event) []any {
	var out []any
	for i := range smalls {
		out = append(out, payloadMap(&smalls[i]))
	}
	return out
}
