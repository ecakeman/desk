// Command fs 在 cwd（插件 jail）上 read / write；write 由控制面 Ask。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"desk/internal/plugin"
)

type req struct {
	ID   string         `json:"id"`
	Op   string         `json:"op"`
	Args map[string]any `json:"args"`
}

type resp struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func main() {
	var in req
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		write(resp{OK: false, Error: err.Error()})
		os.Exit(1)
	}
	switch in.Op {
	case "read":
		path, _ := in.Args["path"].(string)
		rel, err := plugin.ResolveInWorkspace(".", path)
		if err != nil {
			write(resp{ID: in.ID, OK: false, Error: err.Error()})
			os.Exit(1)
		}
		b, err := os.ReadFile(rel)
		if err != nil {
			write(resp{ID: in.ID, OK: false, Error: err.Error()})
			os.Exit(1)
		}
		write(resp{ID: in.ID, OK: true, Data: map[string]string{"content": string(b)}})
	case "write":
		path, _ := in.Args["path"].(string)
		content, _ := in.Args["content"].(string)
		rel, err := plugin.ResolveInWorkspace(".", path)
		if err != nil {
			write(resp{ID: in.ID, OK: false, Error: err.Error()})
			os.Exit(1)
		}
		if err := os.MkdirAll(filepath.Dir(rel), 0755); err != nil {
			write(resp{ID: in.ID, OK: false, Error: err.Error()})
			os.Exit(1)
		}
		if err := os.WriteFile(rel, []byte(content), 0644); err != nil {
			write(resp{ID: in.ID, OK: false, Error: err.Error()})
			os.Exit(1)
		}
		write(resp{ID: in.ID, OK: true, Data: map[string]string{"path": rel}})
	case "sleep":
		time.Sleep(10 * time.Second)
		write(resp{ID: in.ID, OK: true})
	default:
		write(resp{ID: in.ID, OK: false, Error: "unknown_op: " + in.Op})
		os.Exit(1)
	}
}

func write(r resp) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(r)
}
