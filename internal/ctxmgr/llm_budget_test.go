package ctxmgr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"desk/internal/event"
	"desk/internal/ids"
)

func TestEstimateToolsIncludesSchemaNotJustDescription(t *testing.T) {
	short := EstimateTools([]ToolSpec{{Name: "fs.read", Description: "d", Parameters: json.RawMessage(`{}`)}})
	wide := EstimateTools([]ToolSpec{{
		Name:        "fs.read",
		Description: "d",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"` + strings.Repeat("schema-field-", 40) + `"}}}`),
	}})
	if wide <= short {
		t.Fatalf("schema must count wide=%d short=%d", wide, short)
	}
}

func TestRealLLMBudgetSmallSystemLargeMessages(t *testing.T) {
	m, ev, sessionID, runID := testMgr(t, 80, &StubCompactor{Err: context.Canceled})
	m.Settings.TotalTokens = 200
	m.Settings.SmallTriggerTok = 1_000_000
	for i := 0; i < 12; i++ {
		appendUser(t, ev, runID, strings.Repeat("msg-pad-", 10)+ids.New())
	}
	sys := "tiny-system"
	tools := []ToolSpec{{Name: "ping.ok", Description: "noop", Parameters: json.RawMessage(`{"type":"object"}`)}}
	contextAssembly, err := m.Prepare(context.Background(), PrepareIn{
		SessionID: sessionID, RunID: runID, System: sys, Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := EstimateLLMInput(sys, tools, contextAssembly.Messages, "")
	if got > m.Settings.TotalTokens {
		t.Fatalf("real input %d > total %d", got, m.Settings.TotalTokens)
	}
	if contextAssembly.Applied.TotalEstimate != got {
		t.Fatalf("applied total_estimate %d got %d", contextAssembly.Applied.TotalEstimate, got)
	}
	if contextAssembly.Applied.SystemEstimate != EstimateSystem(sys) || contextAssembly.Applied.ToolsEstimate != EstimateTools(tools) {
		t.Fatalf("system/tools estimates %+v", contextAssembly.Applied)
	}
	if got <= EstimateMessages(contextAssembly.Messages) {
		t.Fatal("reserved system/tools must add to total_estimate")
	}
}

func TestRealLLMBudgetLargeSystemToolsSmallMessages(t *testing.T) {
	m, ev, sessionID, runID := testMgr(t, 100000, &StubCompactor{Err: context.Canceled})
	appendUser(t, ev, runID, "hi")
	sys := strings.Repeat("SYSTEM-BLOCK-", 80)
	tools := []ToolSpec{{
		Name:        "fs.write",
		Description: strings.Repeat("tool-desc-", 40),
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"text":{"type":"string"}}}`),
	}}
	m.Settings.TotalTokens = EstimateSystem(sys) + EstimateTools(tools) + 40
	m.Settings.SmallTriggerTok = 1_000_000
	contextAssembly, err := m.Prepare(context.Background(), PrepareIn{
		SessionID: sessionID, RunID: runID, System: sys, Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := EstimateLLMInput(sys, tools, contextAssembly.Messages, "")
	if got > m.Settings.TotalTokens && contextAssembly.Applied.OverBudget != "pending_tool" {
		t.Fatalf("real input %d > total %d", got, m.Settings.TotalTokens)
	}
}

func TestRealLLMBudgetEverythingLargeNoLargeTruncate(t *testing.T) {
	m, ev, sessionID, runID := testMgr(t, 100000, &StubCompactor{Err: context.Canceled})
	ctx := context.Background()
	tx, err := ev.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Append(ctx, tx, runID, event.TypeContextLargeCompact, CompactPayload{
		Summary: "完整长期状态基线不可截断标记 UNIQUE-LARGE-BODY",
		Facts:   []Fact{{Key: "k", Value: "v", Status: "active", Confidence: 1, SourceRefs: []SourceRef{{RunID: runID, Seq: 1}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		appendUser(t, ev, runID, strings.Repeat("window-big-", 12)+ids.New())
	}
	sys := strings.Repeat("SYS-", 30)
	tools := []ToolSpec{{Name: "fs.read", Description: strings.Repeat("d", 80), Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}}
	hits := []RetrievalHit{{RunID: runID, Seq: 1, Kind: event.TypeMessageUser, Text: strings.Repeat("retrieval-huge ", 50)}}
	m.Settings.TotalTokens = 180
	m.Settings.SmallTriggerTok = 1_000_000
	contextAssembly, err := m.Prepare(ctx, PrepareIn{
		SessionID: sessionID, RunID: runID, System: sys, Tools: tools, FrozenHits: hits,
	})
	if err != nil && err.Error() != "context_over_budget" {
		t.Fatal(err)
	}
	if err == nil {
		got := EstimateLLMInput(sys, tools, contextAssembly.Messages, "")
		if got > m.Settings.TotalTokens && contextAssembly.Applied.OverBudget != "pending_tool" {
			t.Fatalf("real input %d > total %d", got, m.Settings.TotalTokens)
		}
		joined := ""
		for _, msg := range contextAssembly.Layers.Large {
			joined += fmtString(msg["content"])
		}
		if !strings.Contains(joined, "UNIQUE-LARGE-BODY") {
			t.Fatal("large truncated")
		}
		if len(contextAssembly.Applied.Retrieval) != 0 {
			t.Fatal("retrieval should drop before slicing large")
		}
	}
}

func TestRealLLMBudgetPendingToolException(t *testing.T) {
	m, ev, sessionID, runID := testMgr(t, 100000, &StubCompactor{Err: context.Canceled})
	ctx := context.Background()
	tx, err := ev.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.Append(ctx, tx, runID, event.TypeToolRequested, map[string]any{
		"id": "pend", "name": "ping.ok", "args": map[string]any{"blob": strings.Repeat("x", 80)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	sys := strings.Repeat("SYSTEM-BLOCK-", 80)
	tools := []ToolSpec{{
		Name:        "ping.ok",
		Description: strings.Repeat("tool-desc-", 40),
		Parameters:  json.RawMessage(`{"type":"object","properties":{"blob":{"type":"string"}}}`),
	}}
	reserved := EstimateSystem(sys) + EstimateTools(tools)
	m.Settings.TotalTokens = reserved - 1
	m.Settings.SmallTriggerTok = 1_000_000
	contextAssembly, err := m.Prepare(ctx, PrepareIn{
		SessionID: sessionID, RunID: runID, System: sys, Tools: tools, PendingTool: "pend",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := EstimateLLMInput(sys, tools, contextAssembly.Messages, "")
	if got <= m.Settings.TotalTokens {
		t.Fatalf("expected over real budget got=%d total=%d", got, m.Settings.TotalTokens)
	}
	if contextAssembly.Applied.OverBudget != "pending_tool" {
		t.Fatalf("over=%q", contextAssembly.Applied.OverBudget)
	}
}

func TestCompactorNilDoesNotEvict(t *testing.T) {
	m, ev, sessionID, runID := testMgr(t, 30, &StubCompactor{Raw: []byte(`{}`)})
	m.Compactor = nil
	m.Settings.TotalTokens = 80
	first := "keep-raw-" + ids.New()
	appendUser(t, ev, runID, first+strings.Repeat(" z", 20))
	for i := 0; i < 6; i++ {
		appendUser(t, ev, runID, strings.Repeat("later-", 12)+ids.New())
	}
	_, err := m.Prepare(context.Background(), PrepareIn{SessionID: sessionID, RunID: runID})
	if err != nil && err.Error() != "context_over_budget" {
		t.Fatal(err)
	}
	if countType(t, ev, runID, event.TypeContextEvicted) != 0 {
		t.Fatal("evicted without compactor")
	}
	events, err := ev.ListBySession(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	layers := parseLayers(events, runID, "")
	found := false
	for _, u := range layers.window {
		for _, it := range u.Items {
			if strings.Contains(fmtString(it.Msg["content"]), first) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("raw history disappeared without compact")
	}
}
