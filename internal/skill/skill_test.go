package skill

import (
	"testing"

	"desk/internal/event"
	"desk/internal/memory"
)

func TestPathsFromHitsNoFilenamePad(t *testing.T) {
	hits := []memory.Hit{
		{Kind: event.TypeMessageUser, RunID: "a", Seq: 1},
		{Kind: event.TypeSkillRevised, RunID: "a", Seq: 2},
		{Kind: event.TypeSkillRevised, RunID: "a", Seq: 3},
		{Kind: event.TypeSkillRevised, RunID: "a", Seq: 4},
	}
	n := 0
	paths := PathsFromHits(hits, func(h memory.Hit) string {
		n++
		if h.Seq == 2 {
			return "memory/skills/a.md"
		}
		if h.Seq == 3 {
			return "memory/skills/a.md"
		}
		return "memory/skills/b.md"
	})
	if len(paths) != 2 || paths[0] != "memory/skills/a.md" || paths[1] != "memory/skills/b.md" {
		t.Fatalf("%v", paths)
	}
}

func TestParseRef(t *testing.T) {
	path, version, err := ParseRef(" memory/skills/event-index.md@141FF90F ")
	if err != nil || path != "memory/skills/event-index.md" || version != "141ff90f" {
		t.Fatalf("got %s %s %v", path, version, err)
	}
	for _, ref := range []string{
		"",
		"memory/skills/event-index.md",
		"memory/skills/event-index.md@",
		"@141ff90f",
		"../secret.md@141ff90f",
		"memory/skills/event-index.md@zzzzzzzz",
		"memory/skills/event-index.md@141ff90",
		"notes.md@141ff90f",
		"memory/skills/a.md@141ff90f@deadbeef",
	} {
		if _, _, err := ParseRef(ref); err == nil {
			t.Fatalf("accepted %q", ref)
		}
	}
}

func TestPathsFromHitsFewerThanTwo(t *testing.T) {
	hits := []memory.Hit{{Kind: event.TypeSkillRevised, Seq: 1}}
	paths := PathsFromHits(hits, func(memory.Hit) string { return "memory/skills/only.md" })
	if len(paths) != 1 {
		t.Fatalf("%v", paths)
	}
}
