// Package prompt 读 prompts/ 目录，算出本 Run 固定的 Snapshot 与 hash。
package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"desk/internal/plugin"
)

// Snapshot 是一次 Drive 钉死的 Prompt；hash 进 prompt.applied，不是 API 前缀缓存键。
type Snapshot struct {
	base   string
	phases map[string]string
	tools  map[string]string
	hash   string
	files  []string
}

// Load 读 base、phases、tools/*.md；缺文件失败。
func Load(dir string) (*Snapshot, error) {
	snapshot := &Snapshot{
		phases: make(map[string]string),
		tools:  make(map[string]string),
	}
	var parts []string
	read := func(rel string) (string, error) {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", fmt.Errorf("prompt %s: %w", rel, err)
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			return "", fmt.Errorf("prompt %s: empty", rel)
		}
		snapshot.files = append(snapshot.files, rel)
		parts = append(parts, rel+"\x00"+text)
		return text, nil
	}

	var err error
	snapshot.base, err = read("system/base.md")
	if err != nil {
		return nil, err
	}
	for _, phase := range []string{"plan", "act", "review"} {
		snapshot.phases[phase], err = read("phases/" + phase + ".md")
		if err != nil {
			return nil, err
		}
	}
	toolDir := filepath.Join(dir, "tools")
	toolFiles, err := os.ReadDir(toolDir)
	if err != nil {
		return nil, fmt.Errorf("prompt tools: %w", err)
	}
	for _, file := range toolFiles {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(file.Name(), ".md")
		snapshot.tools[name], err = read("tools/" + file.Name())
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	snapshot.hash = hex.EncodeToString(sum[:])
	sort.Strings(snapshot.files)
	return snapshot, nil
}

// Hash 是目录内容的 SHA-256。
func (s *Snapshot) Hash() string {
	if s == nil {
		return ""
	}
	return s.hash
}

// Files 返回参与 hash 的相对路径。
func (s *Snapshot) Files() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.files...)
}

// System 返回所有回合共享的稳定行为契约。
func (s *Snapshot) System() string {
	if s == nil {
		return ""
	}
	return s.base
}

// Runtime 返回当前 phase 的动态策略，供 Worker 追加到消息尾部。
func (s *Snapshot) Runtime(phase string) string {
	if s == nil {
		return ""
	}
	phaseText := s.phases[phase]
	if phaseText == "" {
		phaseText = s.phases["act"]
	}
	return "[RUNTIME: PHASE]\n" + phaseText + "\n[/RUNTIME]"
}

// ApplyTools 用 prompts/tools/<name>.md 覆盖工具 description。
func (s *Snapshot) ApplyTools(tools []plugin.Tool) []plugin.Tool {
	out := make([]plugin.Tool, len(tools))
	copy(out, tools)
	for i := range out {
		if description := s.tools[out[i].Name]; description != "" {
			out[i].Description = description
		}
	}
	return out
}
