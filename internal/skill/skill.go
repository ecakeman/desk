// Package skill 判定 Workspace 里 memory/skills/*.md：路径、注入、修订事件。
package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"desk/internal/event"
	"desk/internal/memory"
)

const maxFiles = 2
const maxRunes = 2000
const headRunes = 1000

// Revision 是 skill.revised 的 payload。
type Revision struct {
	Path     string `json:"path"`
	BasedOn  []int  `json:"based_on"`
	DiffHead string `json:"diff_head"`
	Text     string `json:"text"`
}

// IsRel 判断相对路径是否是一篇 skill 文件。
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

// DiffHead 是内容 SHA-256 的前 8 位，用作 skill_ref 版本。
func DiffHead(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:8]
}

// ParseRef 校验 task.skill_ref：必须是 memory/skills/*.md@<8位内容 hash>。
func ParseRef(ref string) (path, version string, err error) {
	ref = strings.TrimSpace(ref)
	path, version, ok := strings.Cut(ref, "@")
	if !ok || path == "" || version == "" || strings.Contains(version, "@") {
		return "", "", fmt.Errorf("bad_skill_ref")
	}
	if !IsRel(path) || !isDiffHead(version) {
		return "", "", fmt.Errorf("bad_skill_ref")
	}
	return path, strings.ToLower(version), nil
}

func isDiffHead(v string) bool {
	if len(v) != 8 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// NewRevision 仅对 skill 路径生成修订记录。
func NewRevision(path, content string, basedOn int) (Revision, bool) {
	if !IsRel(path) {
		return Revision{}, false
	}
	return Revision{
		Path:     path,
		BasedOn:  []int{basedOn},
		DiffHead: DiffHead(content),
		Text:     Clip(content),
	}, true
}

// Clip 截到注入上限：保留标题行加正文前 headRunes。
func Clip(s string) string {
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

// PathsFromHits 从检索命中里选出最多两篇 skill 路径。
func PathsFromHits(hits []memory.Hit, pathOf func(memory.Hit) string) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range hits {
		if h.Kind != event.TypeSkillRevised {
			continue
		}
		p := pathOf(h)
		if p == "" || !IsRel(p) || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) >= maxFiles {
			break
		}
	}
	return out
}

// InjectPaths 读 Workspace 当前文本，打成 STM 里的 user 块。
func InjectPaths(work string, paths []string) []map[string]any {
	if work == "" {
		work = "."
	}
	var out []map[string]any
	for _, rel := range paths {
		if !IsRel(rel) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		out = append(out, map[string]any{
			"role":    "user",
			"content": event.Redact("[skill " + rel + "@" + DiffHead(string(b)) + "]\n" + Clip(string(b))),
		})
	}
	return out
}
