package plugin

import (
	"context"
	"encoding/json"
)

// OpSpec 是 plugin.json 里的一个操作。
type OpSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Internal    bool            `json:"internal,omitempty"`
}

// Manifest 描述一个插件。Root 为 workspace 或 host。
type Manifest struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Ops     []OpSpec `json:"ops"`
	Risk    string   `json:"risk"`
	Root    string   `json:"root,omitempty"`
}

// Plugin 是进程插件或宿主内建工具。
type Plugin interface {
	Manifest() Manifest
	Exec(ctx context.Context, op string, args map[string]any) (json.RawMessage, error)
}

// Tool 是暴露给模型的一条 name.op。
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Risk        string          `json:"risk"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type request struct {
	ID   string         `json:"id"`
	Op   string         `json:"op"`
	Args map[string]any `json:"args"`
}

type response struct {
	ID    string          `json:"id"`
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}
