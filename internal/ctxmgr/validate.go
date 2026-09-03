package ctxmgr

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

var emptySummaries = []string{
	"everything important is preserved",
	"everything important is retained",
	"all important information is preserved",
	"一切重要信息均已保留",
	"没有需要保留的内容",
}

// ValidateResult 校验 compact JSON：schema、provenance、非空、体积。
func ValidateResult(raw []byte, allowed map[int]bool, inputTokens int) (Result, error) {
	var out Result
	if err := json.Unmarshal(raw, &out); err != nil {
		return Result{}, fmt.Errorf("compact_json: %w", err)
	}
	if out.Facts == nil {
		out.Facts = []Fact{}
	}
	if out.OpenItems == nil {
		out.OpenItems = []string{}
	}
	if out.Decisions == nil {
		out.Decisions = []string{}
	}
	sum := strings.TrimSpace(out.Summary)
	if !hasSignal(sum, out) {
		return Result{}, fmt.Errorf("compact_empty")
	}
	if isBoilerplate(sum) && len(out.Facts) == 0 && len(out.OpenItems) == 0 && len(out.Decisions) == 0 {
		return Result{}, fmt.Errorf("compact_empty")
	}
	for i := range out.Facts {
		f := &out.Facts[i]
		f.Key = strings.TrimSpace(f.Key)
		f.Value = strings.TrimSpace(f.Value)
		f.Status = strings.TrimSpace(f.Status)
		if f.Key == "" || f.Value == "" {
			return Result{}, fmt.Errorf("compact_fact")
		}
		if f.Status == "" {
			f.Status = "active"
		}
		if f.Status != "active" && f.Status != "superseded" && f.Status != "dropped" {
			return Result{}, fmt.Errorf("compact_status")
		}
		if f.Confidence < 0 || f.Confidence > 1 {
			return Result{}, fmt.Errorf("compact_confidence")
		}
		for _, seq := range f.SourceEventSeqs {
			if allowed != nil && !allowed[seq] {
				return Result{}, fmt.Errorf("compact_provenance")
			}
		}
	}
	outTokens := EstimateTokens(sum) + EstimateTokens(factsText(out.Facts))
	if inputTokens > 0 && outTokens >= inputTokens {
		return Result{}, fmt.Errorf("compact_oversized")
	}
	if utf8.RuneCountInString(sum) > 2000 {
		return Result{}, fmt.Errorf("compact_oversized")
	}
	out.Summary = sum
	return out, nil
}

func hasSignal(summary string, out Result) bool {
	if utf8.RuneCountInString(summary) >= 8 && !isBoilerplate(summary) {
		return true
	}
	return len(out.Facts) > 0 || len(out.OpenItems) > 0 || len(out.Decisions) > 0
}

func isBoilerplate(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	low = strings.Trim(low, ".!。")
	for _, p := range emptySummaries {
		if low == p {
			return true
		}
	}
	return false
}

func factsText(facts []Fact) string {
	var b strings.Builder
	for _, f := range facts {
		b.WriteString(f.Key)
		b.WriteByte('=')
		b.WriteString(f.Value)
		b.WriteByte('\n')
	}
	return b.String()
}

func allowedSet(refs []SourceRef) map[int]bool {
	out := map[int]bool{}
	for _, r := range refs {
		out[r.Seq] = true
	}
	return out
}
