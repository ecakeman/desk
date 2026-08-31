package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"desk/internal/event"
)

const maxFiles = 2
const maxRunes = 2000
const headRunes = 1000

func IsRel(p string) bool {
	p = filepath.ToSlash(filepath.Clean(p))
	dir, name := pathSplit(p)
	return dir == "memory/skills/" && strings.HasSuffix(name, ".md") && name != ".md"
}

func pathSplit(p string) (string, string) {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "", p
	}
	return p[:i+1], p[i+1:]
}

func DiffHead(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:8]
}

func Inject(work string) []map[string]any {
	if work == "" {
		work = "."
	}
	dir := filepath.Join(work, "memory", "skills")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("memory/skills", e.Name()))
		if !IsRel(rel) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) > maxFiles {
		names = names[:maxFiles]
	}
	var out []map[string]any
	for _, name := range names {
		rel := "memory/skills/" + name
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		text := clip(string(b))
		out = append(out, map[string]any{
			"role":    "user",
			"content": event.Redact("[skill " + rel + "]\n" + text),
		})
	}
	return out
}

func clip(s string) string {
	n := utf8.RuneCountInString(s)
	if n <= maxRunes {
		return s
	}
	title, rest, _ := strings.Cut(s, "\n")
	rs := []rune(rest)
	if len(rs) > headRunes {
		rs = rs[:headRunes]
	}
	return strings.TrimSpace(title) + "\n" + string(rs)
}
