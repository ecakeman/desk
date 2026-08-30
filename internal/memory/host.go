package memory

import (
	"fmt"
	"context"
	"encoding/json"

	"desk/internal/plugin"
)

type Host struct {
	idx *Index
}

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
	hits, err := h.idx.Search(ctx, q, 8)
	if err != nil {
		return nil, err
	}
	if hits == nil {
		hits = []Hit{}
	}
	return json.Marshal(map[string]any{"hits": hits})
}