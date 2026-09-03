package ctxmgr

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

// Applied 是一次 LLM Call 的可审查元数据。
type Applied struct {
	Version          int            `json:"version"`
	PromptHash       string         `json:"prompt_hash"`
	WindowTokens     int            `json:"window_tokens"`
	WindowEstimate   int            `json:"window_estimate"`
	TotalTokens      int            `json:"total_tokens"`
	TotalEstimate    int            `json:"total_estimate"`
	InputEstimate    int            `json:"estimated_input_tokens"`
	LargeSeq         int            `json:"large_seq,omitempty"`
	LargeRunID       string         `json:"large_run_id,omitempty"`
	SmallRefs        []SourceRef    `json:"small_refs,omitempty"`
	Retrieval        []RetrievalHit `json:"retrieval,omitempty"`
	RebuildReason    string         `json:"rebuild_reason,omitempty"`
	CompactionReason string         `json:"compaction_reason,omitempty"`
	SkipRuntime      bool           `json:"skip_runtime,omitempty"`
}

// RetrievalHit 冻结当时注入的检索，供确定性 Reconstruct。
type RetrievalHit struct {
	RunID string `json:"run_id"`
	Seq   int    `json:"seq"`
	Kind  string `json:"kind"`
	Text  string `json:"text"`
}

// Settings 是窗口与触发阈值（估算单位）。
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
