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

func sourceKey(r SourceRef) string {
	return r.RunID + "\x00" + itoaSeq(r.Seq)
}

func itoaSeq(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	v := n
	if v < 0 {
		v = -v
	}
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if n < 0 {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func allowedMap(refs []SourceRef) map[string]SourceRef {
	out := map[string]SourceRef{}
	for _, r := range refs {
		out[sourceKey(r)] = r
	}
	return out
}

func sameRun(refs []SourceRef) (string, bool) {
	if len(refs) == 0 {
		return "", false
	}
	run := refs[0].RunID
	for _, r := range refs {
		if r.RunID != run {
			return "", false
		}
	}
	return run, run != ""
}

func factRefs(f Fact, allowed []SourceRef) []SourceRef {
	if len(f.SourceRefs) > 0 {
		return f.SourceRefs
	}
	run, ok := sameRun(allowed)
	if !ok || len(f.SourceEventSeqs) == 0 {
		return nil
	}
	var out []SourceRef
	for _, seq := range f.SourceEventSeqs {
		out = append(out, SourceRef{RunID: run, Seq: seq})
	}
	return out
}

// ValidateResult 校验 compact JSON：schema、provenance=(run_id,seq)、非空、体积。
func ValidateResult(raw []byte, allowed []SourceRef, inputTokens int) (Result, error) {
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
	allow := allowedMap(allowed)
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
		refs := factRefs(*f, allowed)
		if len(refs) == 0 {
			return Result{}, fmt.Errorf("compact_provenance")
		}
		for _, r := range refs {
			if _, ok := allow[sourceKey(r)]; !ok {
				return Result{}, fmt.Errorf("compact_provenance")
			}
		}
		f.SourceRefs = refs
		f.SourceEventSeqs = nil
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
