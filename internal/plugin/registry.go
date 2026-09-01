// Package plugin 按 manifest 注册工具；控制面不按插件名分支。
// path 参数才走 jail；cwd 是 manifest.root 对应的目录；执行超时 5s。
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Registry 持有已注册插件；Workspace 是 workspace 根的绝对路径。
type Registry struct {
	Workspace string
	byName    map[string]Plugin
}

// Load 扫 plugins/*/plugin.json。root 为 workspace 或 host；缺对应根则跳过该插件。
func Load(pluginDir, workspace, hostRoot string) (*Registry, error) {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return nil, err
	}
	workAbs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	roots := map[string]string{"workspace": workAbs}
	hostAbs, err := optionalRoot(hostRoot)
	if err != nil {
		return nil, err
	}
	if hostAbs != "" {
		roots["host"] = hostAbs
	}
	r := &Registry{
		Workspace: workAbs,
		byName:    map[string]Plugin{},
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(pluginDir, e.Name())
		b, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
		if err != nil {
			continue
		}
		var man Manifest
		if err := json.Unmarshal(b, &man); err != nil {
			return nil, err
		}
		key := man.Root
		if key == "" {
			key = "workspace"
		}
		jail, ok := roots[key]
		if !ok || jail == "" {
			continue
		}
		r.byName[man.Name] = &Process{dir: dir, man: man, jail: jail}
	}
	return r, nil
}

func optionalRoot(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if JailTooBroad(raw) {
		return "", fmt.Errorf("host_root_too_broad")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("host_root: %w", err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("host_root_not_dir")
	}
	return abs, nil
}

// Put 注册进程内插件（memory.search、task.update）。
func (r *Registry) Put(p Plugin) {
	r.byName[p.Manifest().Name] = p
}

// Tools 列出非 internal 的 name.op；write 操作强制 risk=write。
func (r *Registry) Tools() []Tool {
	var out []Tool
	for _, p := range r.byName {
		m := p.Manifest()
		for _, op := range m.Ops {
			if op.Internal {
				continue
			}
			risk := m.Risk
			out = append(out, Tool{
				Name:        m.Name + "." + op.Name,
				Description: op.Description,
				Risk:        risk,
				Parameters:  op.Parameters,
			})
			if op.Name == "write" {
				out[len(out)-1].Risk = "write"
			}
		}
	}
	return out
}

// Exec 按插件名调用；未知名报错。
func (r *Registry) Exec(ctx context.Context, name, op string, args map[string]any) (json.RawMessage, error) {
	p, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("unknown_plugin: %s", name)
	}
	return p.Exec(ctx, op, args)
}
