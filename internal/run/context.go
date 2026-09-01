package run

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"desk/internal/event"
	"desk/internal/memory"
	"desk/internal/skill"
)

// skillInject 按用户文本检索，最多注入两篇 Workspace 里的 skill 文件。
func (s *Service) skillInject(ctx context.Context, runID, work, query string) []map[string]any {
	if s.Index == nil || query == "" {
		return nil
	}
	hits, err := s.Index.Search(ctx, query, 16)
	if err != nil {
		return nil
	}
	paths := skill.PathsFromHits(hits, func(hit memory.Hit) string {
		ev, err := s.Events.Get(ctx, hit.RunID, hit.Seq)
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
	return skill.InjectPaths(work, paths)
}

// attachReview 在 review 回合把检索结果附在 history 尾部，并写 memory.retrieved。
func (s *Service) attachReview(ctx context.Context, runID string) []map[string]any {
	if s.Index == nil {
		return nil
	}
	query := strings.TrimSpace(s.runUserText(ctx, runID) + " " + s.lastTaskTitle(ctx, runID))
	if query == "" {
		return nil
	}
	hits, err := s.Index.Search(ctx, query, 8)
	if err != nil {
		return nil
	}
	brief := make([]map[string]any, 0, len(hits))
	var text strings.Builder
	text.WriteString("[memory.retrieved]\n")
	for _, hit := range hits {
		brief = append(brief, map[string]any{
			"run_id": hit.RunID, "seq": hit.Seq, "kind": hit.Kind,
		})
		hitText := hit.Text
		if utf8.RuneCountInString(hitText) > 200 {
			hitText = string([]rune(hitText)[:200])
		}
		fmt.Fprintf(&text, "%s %d %s\n%s\n", hit.Kind, hit.Seq, hit.RunID, hitText)
	}
	_, _ = s.appendOne(ctx, runID, event.TypeMemoryRetrieved, map[string]any{
		"phase": "review",
		"query": query,
		"hits":  brief,
	})
	if len(hits) == 0 {
		return nil
	}
	return []map[string]any{{
		"role":    "user",
		"content": event.Redact(text.String()),
	}}
}

func (s *Service) runUserText(ctx context.Context, runID string) string {
	text, err := s.Events.FirstUserText(ctx, runID)
	if err != nil {
		return ""
	}
	return text
}

func (s *Service) lastTaskTitle(ctx context.Context, runID string) string {
	title, err := s.Events.LastTaskTitle(ctx, runID)
	if err != nil {
		return ""
	}
	return title
}
