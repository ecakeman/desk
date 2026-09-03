package ctxmgr

import "encoding/json"

// SourceRef 指向一条原始 event。
type SourceRef struct {
	RunID string `json:"run_id"`
	Seq   int    `json:"seq"`
}

// Fact 是 compact 派生的结构化事实，不是 Event Store 真相。
type Fact struct {
	Key        string      `json:"key"`
	Value      string      `json:"value"`
	Status     string      `json:"status"`
	Confidence float64     `json:"confidence"`
	SourceRefs []SourceRef `json:"source_refs,omitempty"`
	// SourceEventSeqs 仅兼容旧测例；校验时只有全部 allowed 同 run 才接受。
	SourceEventSeqs []int `json:"source_event_seqs,omitempty"`
}

// Result 是 Small/Large Compact 的可解析输出。
type Result struct {
	Summary   string   `json:"summary"`
	Facts     []Fact   `json:"facts"`
	OpenItems []string `json:"open_items"`
	Decisions []string `json:"decisions"`
}

// CompactPayload 写入 context.small_compact / context.large_compact。
type CompactPayload struct {
	Summary   string      `json:"summary"`
	Facts     []Fact      `json:"facts"`
	OpenItems []string    `json:"open_items"`
	Decisions []string    `json:"decisions"`
	BasedOn   []SourceRef `json:"based_on"`
	Absorbs   []SourceRef `json:"absorbs,omitempty"`
}

// EvictPayload 是 durable Active Window 边界：这些源事件不再作为 raw window。
type EvictPayload struct {
	BasedOn []SourceRef `json:"based_on"`
}

// CompactFailPayload 记录 compact LLM/校验失败，避免同一批每 Call 重试。
type CompactFailPayload struct {
	Kind    string      `json:"kind"`
	BasedOn []SourceRef `json:"based_on"`
	Error   string      `json:"error"`
}

// Applied 是「Worker 已成功采用」的 Context 元数据，不是 Prepare 成功。
// PromptHash 不能还原 prompt 正文；reconstructable 不是 byte-for-byte replay。
type Applied struct {
	Version          int            `json:"version"`
	PromptHash       string         `json:"prompt_hash"`
	WindowTokens     int            `json:"window_tokens"`
	WindowBudget     int            `json:"window_budget"`
	WindowEstimate   int            `json:"window_estimate"`
	TotalTokens      int            `json:"total_tokens"`
	TotalEstimate    int            `json:"total_estimate"`
	InputEstimate    int            `json:"estimated_input_tokens"`
	SystemEstimate   int            `json:"system_estimate"`
	ToolsEstimate    int            `json:"tools_estimate"`
	FactCount        int            `json:"fact_count"`
	SkillCount       int            `json:"skill_count"`
	LargeSeq         int            `json:"large_seq,omitempty"`
	LargeRunID       string         `json:"large_run_id,omitempty"`
	SmallRefs        []SourceRef    `json:"small_refs,omitempty"`
	PendingEvicted   []SourceRef    `json:"pending_evicted,omitempty"`
	Retrieval        []RetrievalHit `json:"retrieval,omitempty"`
	RebuildReason    string         `json:"rebuild_reason,omitempty"`
	CompactionReason string         `json:"compaction_reason,omitempty"`
	OverBudget       string         `json:"over_budget,omitempty"`
	SkipRuntime      bool           `json:"skip_runtime,omitempty"`
}

// RetrievalHit 冻结当时注入的检索，供确定性 Reconstruct。
type RetrievalHit struct {
	RunID string `json:"run_id"`
	Seq   int    `json:"seq"`
	Kind  string `json:"kind"`
	Text  string `json:"text"`
}

// ToolSpec 只供预算估算，与 Worker 发送的工具条目对应；ctxmgr 不发 HTTP。
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Settings 是窗口与触发阈值（估算单位）。
// TotalTokens 是硬上限；WindowTokens 是 raw recent 的 preferred max。
// windowBudget = min(Window, max(0, Total-system-tools-stable-dynamic))。
type Settings struct {
	WindowTokens    int
	TotalTokens     int
	SmallTriggerTok int
	LargeTriggerTok int
	LargeSmallCount int
	RetrievalK      int
	PromptsDir      string
}

func (s Settings) withDefaults() Settings {
	out := s
	if out.WindowTokens <= 0 {
		out.WindowTokens = 4000
	}
	if out.TotalTokens <= 0 {
		out.TotalTokens = out.WindowTokens * 3
	}
	if out.SmallTriggerTok <= 0 {
		out.SmallTriggerTok = 400
	}
	if out.LargeTriggerTok <= 0 {
		out.LargeTriggerTok = 1200
	}
	if out.LargeSmallCount <= 0 {
		out.LargeSmallCount = 3
	}
	if out.RetrievalK <= 0 {
		out.RetrievalK = 8
	}
	return out
}
