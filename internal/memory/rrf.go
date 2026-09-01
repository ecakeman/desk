package memory

import "sort"

// rrfMerge 把词法与向量两路按 Reciprocal Rank Fusion 合成一条序。
func rrfMerge(lex, vec []Hit, topK int) []Hit {
	const k = 60.0
	type id struct {
		run string
		seq int
	}
	score := map[id]float64{}
	meta := map[id]Hit{}
	add := func(list []Hit) {
		for i, h := range list {
			key := id{h.RunID, h.Seq}
			score[key] += 1.0 / (k + float64(i+1))
			if _, ok := meta[key]; !ok {
				meta[key] = h
			}
		}
	}
	add(lex)
	add(vec)
	out := make([]Hit, 0, len(score))
	for key, s := range score {
		h := meta[key]
		h.Score = s
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			if out[i].RunID == out[j].RunID {
				return out[i].Seq < out[j].Seq
			}
			return out[i].RunID < out[j].RunID
		}
		return out[i].Score > out[j].Score
	})
	if topK > 0 && len(out) > topK {
		out = out[:topK]
	}
	return out
}
