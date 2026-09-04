// Package httpapi 把 /v1 接到 Store 和 run.Service。Gin 不编排模型；SSE 只读 events。
package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"desk/internal/event"
	"desk/internal/run"
	"desk/internal/session"

	"github.com/gin-gonic/gin"
)

// Deps 是 serve 注入的只读依赖。
type Deps struct {
	DB        *sql.DB
	Workspace string
	WebDir    string
	Sessions  *session.Store
	Runs      *run.Store
	Messages  *run.Service
	Events    *event.Store
}

// NewMux 挂 healthz、/v1 和可选静态 Dashboard。
func NewMux(deps Deps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	router.GET("/healthz", func(c *gin.Context) {
		if err := deps.DB.PingContext(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "db"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	v1 := router.Group("/v1")
	{
		v1.GET("/sessions", func(c *gin.Context) {
			items, err := deps.Sessions.List(c.Request.Context())
			if err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, items)
		})
		v1.DELETE("/sessions", func(c *gin.Context) {
			if err := deps.Sessions.DeleteAll(c.Request.Context()); err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
		v1.GET("/workspace", func(c *gin.Context) {
			abs, err := filepath.Abs(deps.Workspace)
			if err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"path": abs})
		})
		v1.POST("/workspace/open", func(c *gin.Context) {
			if err := openWorkspace(deps.Workspace); err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
		v1.POST("/sessions", func(c *gin.Context) {
			item, err := deps.Sessions.Create(c.Request.Context())
			if err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, item)
		})
		v1.GET("/sessions/:id", func(c *gin.Context) {
			item, err := deps.Sessions.Get(c.Request.Context(), c.Param("id"))
			if err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, item)
		})
		v1.DELETE("/sessions/:id", func(c *gin.Context) {
			err := deps.Sessions.Delete(c.Request.Context(), c.Param("id"))
			if err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
		v1.GET("/sessions/:id/runs", func(c *gin.Context) {
			if _, err := deps.Sessions.Get(c.Request.Context(), c.Param("id")); err != nil {
				writeError(c, err)
				return
			}
			items, err := deps.Runs.ListBySession(c.Request.Context(), c.Param("id"))
			if err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, items)
		})
		v1.GET("/sessions/:id/events", func(c *gin.Context) {
			if _, err := deps.Sessions.Get(c.Request.Context(), c.Param("id")); err != nil {
				writeError(c, err)
				return
			}
			items, err := deps.Events.ListBySession(c.Request.Context(), c.Param("id"))
			if err != nil {
				writeError(c, err)
				return
			}
			if items == nil {
				items = []event.Event{}
			}
			c.JSON(http.StatusOK, items)
		})
		v1.POST("/sessions/:id/messages", func(c *gin.Context) {
			var body struct {
				Text string `json:"text"`
			}
			if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Text) == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "text_required"})
				return
			}
			runID, err := deps.Messages.PostUserMessage(
				c.Request.Context(), c.Param("id"), body.Text, deps.Workspace,
			)
			if err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"run_id": runID})
		})

		v1.GET("/runs/:id", func(c *gin.Context) {
			item, err := deps.Runs.Get(c.Request.Context(), c.Param("id"))
			if err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, item)
		})
		v1.DELETE("/runs/:id", func(c *gin.Context) {
			err := deps.Runs.Delete(c.Request.Context(), c.Param("id"))
			if err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
		v1.GET("/runs/:id/stm", func(c *gin.Context) {
			item, err := deps.Runs.Get(c.Request.Context(), c.Param("id"))
			if err != nil {
				writeError(c, err)
				return
			}
			messages, err := deps.Events.Messages(c.Request.Context(), item.SessionID, item.ID)
			if err != nil {
				writeError(c, err)
				return
			}
			if messages == nil {
				messages = []map[string]any{}
			}
			c.JSON(http.StatusOK, gin.H{"kind": "event_projection", "messages": messages})
		})
		v1.GET("/runs/:id/context", func(c *gin.Context) {
			item, err := deps.Runs.Get(c.Request.Context(), c.Param("id"))
			if err != nil {
				writeError(c, err)
				return
			}
			contextAssembly, src, ok := deps.Messages.InspectContext(c.Request.Context(), item.SessionID, item.ID)
			if !ok {
				c.JSON(http.StatusNotFound, gin.H{"error": "no_context"})
				return
			}
			out := gin.H{
				"kind":     "context",
				"source":   src,
				"applied":  contextAssembly.Applied,
				"layers":   contextAssembly.Layers,
				"messages": contextAssembly.Messages,
			}
			if src == "reconstructable" {
				out["reconstructable_note"] = "not a byte-for-byte prompt replay; prompt body is not snapshotted"
			}
			c.JSON(http.StatusOK, out)
		})
		v1.GET("/runs/:id/events/:seq", func(c *gin.Context) {
			seq, err := strconv.Atoi(c.Param("seq"))
			if err != nil || seq < 1 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "bad_seq"})
				return
			}
			item, err := deps.Events.Get(c.Request.Context(), c.Param("id"), seq)
			if err != nil {
				writeError(c, err)
				return
			}
			c.JSON(http.StatusOK, item)
		})
		v1.POST("/runs/:id/decisions", func(c *gin.Context) {
			var body struct {
				Seq   int  `json:"seq"`
				Allow bool `json:"allow"`
			}
			if err := c.ShouldBindJSON(&body); err != nil || body.Seq < 1 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "bad_seq"})
				return
			}
			err := deps.Messages.Decide(c.Request.Context(), c.Param("id"), body.Seq, body.Allow)
			switch {
			case errors.Is(err, run.ErrBadSeq):
				c.JSON(http.StatusBadRequest, gin.H{"error": "bad_seq"})
			case errors.Is(err, run.ErrNotWaiting), errors.Is(err, run.ErrConflict):
				c.JSON(http.StatusConflict, gin.H{"error": "conflict"})
			case err != nil:
				writeError(c, err)
			default:
				c.JSON(http.StatusOK, gin.H{"ok": true})
			}
		})
		v1.POST("/runs/:id/cancel", func(c *gin.Context) {
			err := deps.Messages.Cancel(c.Param("id"))
			switch {
			case errors.Is(err, run.ErrNotWaiting):
				c.JSON(http.StatusConflict, gin.H{"error": "conflict"})
			case err != nil:
				writeError(c, err)
			default:
				c.JSON(http.StatusOK, gin.H{"ok": true})
			}
		})
		v1.GET("/runs/:id/events", func(c *gin.Context) {
			streamRunEvents(c, deps, c.Param("id"))
		})
	}

	mountWeb(router, deps.WebDir)
	return router
}

// streamRunEvents 按 after= / Last-Event-ID 推已写入的事件；Run 终态后停。
func streamRunEvents(c *gin.Context, deps Deps, runID string) {
	if _, err := deps.Runs.Get(c.Request.Context(), runID); err != nil {
		writeError(c, err)
		return
	}
	after := 0
	value := c.Query("after")
	if value == "" {
		value = c.GetHeader("Last-Event-ID")
	}
	if value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad_after"})
			return
		}
		after = n
	}
	writer := c.Writer
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	writer.Flush()

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	last := after
	idle := 0
	for {
		events, err := deps.Events.ListAfter(c.Request.Context(), runID, last)
		if err != nil {
			return
		}
		ended := false
		for _, item := range events {
			raw, err := json.Marshal(item)
			if err != nil {
				return
			}
			_, _ = fmt.Fprintf(writer, "id: %d\ndata: %s\n\n", item.Seq, raw)
			writer.Flush()
			last = item.Seq
			if terminalEvent(item.Type) {
				ended = true
			}
		}
		if ended {
			return
		}
		item, err := deps.Runs.Get(c.Request.Context(), runID)
		if err != nil {
			return
		}
		if run.Terminal(item.Status) {
			return
		}
		if len(events) == 0 {
			idle++
			if idle%50 == 0 {
				_, _ = fmt.Fprint(writer, ": ping\n\n")
				writer.Flush()
			}
		} else {
			idle = 0
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func terminalEvent(typ string) bool {
	return typ == event.TypeRunCompleted || typ == event.TypeRunFailed || typ == event.TypeRunInterrupted
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
	case errors.Is(err, run.ErrBadSeq):
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad_seq"})
	case errors.Is(err, run.ErrConflict), errors.Is(err, run.ErrNotWaiting):
		c.JSON(http.StatusConflict, gin.H{"error": "conflict"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal"})
	}
}

func openWorkspace(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if out, err := exec.Command("wslpath", "-w", abs).Output(); err == nil {
		cmd := exec.Command("explorer.exe", strings.TrimSpace(string(out)))
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() { _, _ = cmd.Process.Wait() }()
		return nil
	}
	cmd := exec.Command("xdg-open", abs)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _, _ = cmd.Process.Wait() }()
	return nil
}

func mountWeb(router *gin.Engine, dir string) {
	index := filepath.Join(dir, "index.html")
	if dir == "" {
		return
	}
	if info, err := os.Stat(index); err != nil || info.IsDir() {
		return
	}
	router.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet || strings.HasPrefix(c.Request.URL.Path, "/v1/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		rel := filepath.Clean(strings.TrimPrefix(c.Request.URL.Path, "/"))
		if filepath.IsLocal(rel) {
			candidate := filepath.Join(dir, rel)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				c.File(candidate)
				return
			}
		}
		c.File(index)
	})
}
