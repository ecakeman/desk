package event

import (
	"context"
	"encoding/json"
	"unicode/utf8"
)

// MaxSTMChars 是投影字符上限；超过则 EnsureCompact 写 episode.compacted。
const MaxSTMChars = 8000

type compactPayload struct {
	Text    string `json:"text"`
	BasedOn []int  `json:"based_on"`
}

// EnsureCompact 超长时把一批旧 tool.completed 收成一条 compacted 事件；仍是事实。
func (s *Store) EnsureCompact(ctx context.Context, sessionID, currentRunID string) error {
	rows, err := s.sessionRows(ctx, sessionID)
	if err != nil {
		return err
	}
	skip := compactedSkip(rows)
	if stmChars(rows, skip) <= MaxSTMChars {
		return nil
	}
	var targetRun string
	var based []int
	var dumped []byte
	for _, e := range rows {
		if e.Type != TypeToolCompleted {
			continue
		}
		if skip[e.RunID][e.Seq] {
			continue
		}
		if targetRun == "" {
			targetRun = e.RunID
		}
		if e.RunID != targetRun {
			continue
		}
		based = append(based, e.Seq)
		if len(dumped) < 400 {
			dumped = append(dumped, e.Raw...)
		}
	}
	if targetRun == "" || len(based) == 0 {
		return nil
	}
	text := string(dumped)
	if utf8.RuneCountInString(text) > 400 {
		text = string([]rune(text)[:400])
	}
	text = Redact(text) + "\n(compacted)"
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := s.Append(ctx, tx, targetRun, TypeEpisodeCompacted, compactPayload{Text: text, BasedOn: based}); err != nil {
		return err
	}
	return tx.Commit()
}

func compactedSkip(rows []sessRow) map[string]map[int]bool {
	skip := map[string]map[int]bool{}
	for _, e := range rows {
		if e.Type != TypeEpisodeCompacted {
			continue
		}
		var p compactPayload
		if json.Unmarshal(e.Raw, &p) != nil {
			continue
		}
		if skip[e.RunID] == nil {
			skip[e.RunID] = map[int]bool{}
		}
		for _, seq := range p.BasedOn {
			skip[e.RunID][seq] = true
		}
	}
	return skip
}

func stmChars(rows []sessRow, skip map[string]map[int]bool) int {
	n := 0
	for _, e := range rows {
		switch e.Type {
		case TypeMessageUser, TypeMessageCompleted:
			n += utf8.RuneCount(e.Raw)
		case TypeTaskUpdated:
			n += utf8.RuneCount(e.Raw)
		case TypeToolCompleted:
			if skip[e.RunID][e.Seq] {
				continue
			}
			n += utf8.RuneCount(e.Raw)
		case TypeEpisodeCompacted:
			n += utf8.RuneCount(e.Raw)
		}
	}
	return n
}
