package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"desk/internal/event"
	"desk/internal/ids"
	"desk/internal/plugin"
)

type Host struct {
	DB     *sql.DB
	Events *event.Store
}

func NewHost(db *sql.DB, events *event.Store) *Host {
	return &Host{DB: db, Events: events}
}

func (h *Host) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name: "task",
		Risk: "read",
		Ops: []plugin.OpSpec{{
			Name:        "update",
			Description: "Create or update a task on this Run. status=failed does not fail the Run.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"additionalProperties":false,
				"required":["title","status"],
				"properties":{
					"id":{"type":"string"},
					"title":{"type":"string"},
					"status":{"type":"string","enum":["open","done","failed","skipped"]},
					"skill_ref":{"type":"string"}
				}
			}`),
		}},
	}
}

func (h *Host) Exec(ctx context.Context, op string, args map[string]any) (json.RawMessage, error) {
	if op != "update" {
		return nil, fmt.Errorf("unknown_op: %s", op)
	}
	runID := plugin.RunID(ctx)
	if runID == "" {
		return nil, fmt.Errorf("no_run")
	}
	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("title_required")
	}
	status, _ := args["status"].(string)
	switch status {
	case "open", "done", "failed", "skipped":
	default:
		return nil, fmt.Errorf("bad_status")
	}
	id, _ := args["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		id = ids.New()
	}
	payload := map[string]any{
		"id":     id,
		"status": status,
		"title":  title,
	}
	if ref, _ := args["skill_ref"].(string); strings.TrimSpace(ref) != "" {
		payload["skill_ref"] = strings.TrimSpace(ref)
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := h.Events.Append(ctx, tx, runID, event.TypeTaskUpdated, payload); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}
