package worker

import "encoding/json"

type In struct {
	T        string           `json:"t"`
	RunID    string           `json:"run_id,omitempty"`
	Messages []map[string]any `json:"messages,omitempty"`
	Tools    []any            `json:"tools,omitempty"`
	ID       string           `json:"id,omitempty"`
	OK       bool             `json:"ok"`
	Data     json.RawMessage  `json:"data,omitempty"`
	Error    string           `json:"error,omitempty"`
	Model    string `json:"model,omitempty"`
	Phase    string `json:"phase,omitempty"`
	APIModel string `json:"api_model,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
}

type Out struct {
	T     string         `json:"t"`
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Args  map[string]any `json:"args,omitempty"`
	Text  string         `json:"text,omitempty"`
	Error string         `json:"error,omitempty"`
}