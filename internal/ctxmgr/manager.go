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
	Large     []map[string]any `json:"large"`
	Smalls    []map[string]any `json:"smalls"`
	Facts     []map[string]any `json:"facts"`
	Window    []map[string]any `json:"recent_window"`
	Retrieval []map[string]any `json:"retrieval"`
	Runtime   []map[string]any `json:"runtime"`
}

// PrepareIn 是一次 ask 前的输入。
type PrepareIn struct {
	SessionID    string
	RunID        string
	Workspace    string
	Phase        string
	PromptHash   string
	Runtime      string
	PendingTool  string
	WantRetrieve bool
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

// Prepare 可能 compact（成功才写事件），再组装 messages。
func (m *Manager) Prepare(ctx context.Context, in PrepareIn) (Assembly, error) {
	if m == nil || m.Events == nil {
		return Assembly{}, fmt.Errorf("ctxmgr_unconfigured")
	}
	st := m.Settings.withDefaults()
	events, err := m.Events.ListBySession(ctx, in.SessionID)
	if err != nil {
		return Assembly{}, err
	}
	reason := ""
	if compacted, why, err := m.maybeCompact(ctx, in.SessionID, in.RunID, events, in.PendingTool, st); err != nil {
		return Assembly{}, err
	} else if compacted {
		reason = why
		events, err = m.Events.ListBySession(ctx, in.SessionID)
		if err != nil {
			return Assembly{}, err
		}
	}
	layers := parseLayers(events, in.RunID, in.PendingTool)
	kept, _ := splitWindow(layers.window, st.WindowTokens)
	hits := m.retrieve(ctx, in)
	asm := m.assemble(ctx, in, layers, kept, hits, st, reason)
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

func (m *Manager) maybeCompact(ctx context.Context, sessionID, runID string, events []event.Event, pending string, st Settings) (bool, string, error) {
	if m.Compactor == nil || st.PromptsDir == "" {
		return false, "", nil
	}
	layers := parseLayers(events, runID, pending)
	_, evicted := splitWindow(layers.window, st.WindowTokens)
	if EstimateTokens(joinItems(evicted)) < st.SmallTriggerTok {
		return m.maybeLarge(ctx, runID, events, st)
	}
	if err := m.runCompact(ctx, runID, "small", evicted, layers); err != nil {
		return false, "", nil
	}
	events, err := m.Events.ListBySession(ctx, sessionID)
	if err != nil {
		return true, "small_compact", err
	}
	_, why, _ := m.maybeLarge(ctx, runID, events, st)
	if why == "" {
		why = "small_compact"
	}
	return true, why, nil
}

func (m *Manager) maybeLarge(ctx context.Context, runID string, events []event.Event, st Settings) (bool, string, error) {
	layers := parseLayers(events, runID, "")
	if len(layers.smalls) < st.LargeSmallCount && EstimateTokens(smallsText(layers.smalls)) < st.LargeTriggerTok {
		return false, "", nil
	}
	if len(layers.smalls) == 0 {
		return false, "", nil
	}
	if err := m.runLarge(ctx, runID, layers); err != nil {
		return false, "", nil
	}
	return true, "large_compact", nil
}

func (m *Manager) runCompact(ctx context.Context, runID, kind string, items []windowItem, layers layer) error {
	system, err := LoadPrompt(m.Settings.PromptsDir, kind)
	if err != nil {
		return err
	}
	user, refs, inTok := evictedJSON(items)
	raw, err := m.Compactor.Compact(ctx, kind, system, user)
	if err != nil {
		return err
	}
	res, err := ValidateResult(raw, allowedSet(refs), inTok)
	if err != nil {
		return err
	}
	payload := CompactPayload{
		Summary: res.Summary, Facts: res.Facts,
		OpenItems: res.OpenItems, Decisions: res.Decisions,
		BasedOn: refs,
	}
	typ := event.TypeContextSmallCompact
	if kind == "large" {
		typ = event.TypeContextLargeCompact
	}
	_ = layers
	return m.appendDerived(ctx, runID, typ, payload)
}

func (m *Manager) runLarge(ctx context.Context, runID string, layers layer) error {
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
		"allowed_seqs":   seqsOf(refs),
		"previous_large": payloadMap(layers.large),
		"smalls":         smallPayloads(layers.smalls),
	})
	inTok := EstimateTokens(string(body))
	raw, err := m.Compactor.Compact(ctx, "large", system, string(body))
	if err != nil {
		return err
	}
	res, err := ValidateResult(raw, allowedSet(refs), inTok)
	if err != nil {
		return err
	}
	return m.appendDerived(ctx, runID, event.TypeContextLargeCompact, CompactPayload{
		Summary: res.Summary, Facts: res.Facts,
		OpenItems: res.OpenItems, Decisions: res.Decisions,
		BasedOn: refs, Absorbs: absorbs,
	})
}

func (m *Manager) appendDerived(ctx context.Context, runID, typ string, payload CompactPayload) error {
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

func (m *Manager) assemble(ctx context.Context, in PrepareIn, layers layer, window []windowItem, hits []RetrievalHit, st Settings, compactWhy string) Assembly {
	var msgs []map[string]any
	var ly InspectLayers
	if layers.large != nil {
		b := compactBlock("LARGE", *layers.large)
		msgs = append(msgs, b)
		ly.Large = []map[string]any{b}
	}
	for _, s := range layers.smalls {
		b := compactBlock("SMALL", s)
		msgs = append(msgs, b)
		ly.Smalls = append(ly.Smalls, b)
	}
	if fb := factsBlock(layers.facts); fb != nil {
		msgs = append(msgs, fb)
		ly.Facts = []map[string]any{fb}
	}
	userText := firstUser(ctx, m.Events, in.RunID)
	for _, sk := range skill.InjectPaths(in.Workspace, skillPaths(ctx, m, in, userText)) {
		msgs = append(msgs, sk)
	}
	for _, w := range window {
		msgs = append(msgs, w.Msg)
		ly.Window = append(ly.Window, w.Msg)
	}
	if rb := retrievalBlock(hits); rb != nil {
		msgs = append(msgs, rb)
		ly.Retrieval = []map[string]any{rb}
	}
	if in.Runtime != "" {
		rt := map[string]any{"role": "user", "content": in.Runtime}
		ly.Runtime = []map[string]any{rt}
	}
	applied := Applied{
		PromptHash:       in.PromptHash,
		WindowTokens:     st.WindowTokens,
		WindowEstimate:   EstimateMessages(ly.Window),
		InputEstimate:    EstimateMessages(msgs),
		Retrieval:        hits,
		CompactionReason: compactWhy,
	}
	if layers.large != nil {
		applied.LargeSeq = layers.large.Seq
		applied.LargeRunID = layers.large.RunID
	}
	for _, s := range layers.smalls {
		applied.SmallRefs = append(applied.SmallRefs, SourceRef{RunID: s.RunID, Seq: s.Seq})
	}
	rebuild := compactWhy != ""
	if rebuild {
		applied.RebuildReason = compactWhy
	}
	if msgs == nil {
		msgs = []map[string]any{}
	}
	return Assembly{Messages: msgs, Applied: applied, Rebuild: rebuild, CompactionReason: compactWhy, Layers: ly}
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

func splitWindow(items []windowItem, budget int) (kept, evicted []windowItem) {
	total := 0
	for _, it := range items {
		total += EstimateTokens(messageText(it.Msg))
	}
	if total <= budget {
		return items, nil
	}
	i := 0
	for i < len(items) && total > budget {
		total -= EstimateTokens(messageText(items[i].Msg))
		i++
	}
	if i >= len(items) {
		i = len(items) - 1
		if i < 0 {
			return nil, items
		}
	}
	return items[i:], items[:i]
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

func seqsOf(refs []SourceRef) []int {
	var out []int
	for _, r := range refs {
		out = append(out, r.Seq)
	}
	return out
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
