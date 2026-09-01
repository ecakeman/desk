package memory

import (
	"context"
	"encoding/json"
	"fmt"

	"desk/internal/plugin"
)

// Host 是进程内插件 memory.search，走 Index.Search。
type Host struct {
	idx *Index
}

// NewHost 包装 Index。
func NewHost(idx *Index) *Host {
	return &Host{idx: idx}
}

func (h *Host) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name: "memory",
		Risk: "read",
		Ops: []plugin.OpSpec{{
			Name:        "search",
			Description: "Search past events by substring/keywords. Returns run_id and seq.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"additionalProperties":false,
				"required":["query"],
				"properties":{"query":{"type":"string"}}
			}`),
		}},
	}
}

func (h *Host) Exec(ctx context.Context, op string, args map[string]any) (json.RawMessage, error) {
	if op != "search" {
		return nil, fmt.Errorf("unknown_op: %s", op)
	}
	q, _ := args["query"].(string)
	hits, trace, err := h.idx.SearchWithTrace(ctx, q, 8)
	if err != nil {
		return nil, err
	}
	if hits == nil {
		hits = []Hit{}
	}
	return json.Marshal(map[string]any{"hits": hits, "trace": trace})
}
