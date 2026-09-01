package memory

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

func tokenize(s string) []string {
	s = strings.ToLower(s)
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			flush()
			out = append(out, string(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

func hitID(h Hit) string {
	return h.RunID + ":" + strconv.Itoa(h.Seq)
}

func likeContainment(q string) string {
	q = strings.ReplaceAll(q, `\`, `\\`)
	q = strings.ReplaceAll(q, `%`, `\%`)
	q = strings.ReplaceAll(q, `_`, `\_`)
	return q
}

func containsFold(text, q string) bool {
	return q != "" && strings.Contains(strings.ToLower(text), strings.ToLower(q))
}

// bm25Rank 在已加载的候选上算 BM25；不访问数据库。
func bm25Rank(docs []Hit, query string) []Hit {
	qtoks := uniqueTokens(tokenize(query))
	if len(docs) == 0 || len(qtoks) == 0 {
		return []Hit{}
	}
	type bag struct {
		tf  map[string]int
		len int
		hit Hit
	}
	df := map[string]int{}
	bags := make([]bag, 0, len(docs))
	totalLen := 0
	for _, h := range docs {
		tf := map[string]int{}
		toks := tokenize(h.Text)
		for _, tok := range toks {
			tf[tok]++
		}
		for tok := range tf {
			df[tok]++
		}
		n := len(toks)
		if n == 0 {
			n = 1
		}
		totalLen += n
		bags = append(bags, bag{tf: tf, len: n, hit: h})
	}
	avg := float64(totalLen) / float64(len(bags))
	nDocs := float64(len(bags))
	out := make([]Hit, 0, len(bags))
	for _, item := range bags {
		score := 0.0
		for _, tok := range qtoks {
			tf := float64(item.tf[tok])
			if tf == 0 {
				continue
			}
			idf := math.Log((nDocs-float64(df[tok])+0.5)/(float64(df[tok])+0.5) + 1)
			score += idf * (tf * (bm25K1 + 1)) / (tf + bm25K1*(1-bm25B+bm25B*float64(item.len)/avg))
		}
		h := item.hit
		h.Score = score
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			if out[i].RunID == out[j].RunID {
				return out[i].Seq < out[j].Seq
			}
			return out[i].RunID < out[j].RunID
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func uniqueTokens(toks []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(toks))
	for _, tok := range toks {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

func clipHits(hits []Hit, n int) []Hit {
	if hits == nil {
		return []Hit{}
	}
	if n > 0 && len(hits) > n {
		return hits[:n]
	}
	return hits
}
