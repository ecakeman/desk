package plugin

import (
	"context"
	"encoding/json"
)

type OpSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Manifest struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Ops     []OpSpec `json:"ops"`
	Risk    string   `json:"risk"`
}

type Plugin interface {
	Manifest() Manifest
	Exec(ctx context.Context, op string, args map[string]any) (json.RawMessage, error)
}

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Risk        string `json:"risk"`
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