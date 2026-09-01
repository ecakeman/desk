// Package plugin 按 manifest 注册工具；控制面不按插件名分支。
// path 参数才走 jail；cwd 是 Workspace；执行超时 5s。
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Registry 持有已注册插件；Workspace 是 workspace 根的绝对路径。
type Registry struct {
	Workspace string
	byName    map[string]Plugin
}

// Load 扫 plugins/*/plugin.json。只注册 jail=workspace 的进程插件；其它 root 跳过。
func Load(pluginDir, workspace string) (*Registry, error) {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return nil, err
	}
	workAbs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if JailTooBroad(workAbs) {
		return nil, fmt.Errorf("workspace_too_broad")
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
		if man.Root != "" && man.Root != "workspace" {
			continue
		}
		r.byName[man.Name] = &Process{dir: dir, man: man, jail: workAbs}
	}
	return r, nil
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
