package httpapi

import (
	"database/sql"
	"errors"
	"net/http"

	"desk/internal/run"
	"desk/internal/session"

	"github.com/gin-gonic/gin"
)

type Deps struct {
	DB        *sql.DB
	Workspace string
	Sessions  *session.Store
	Runs      *run.Store
	Messages  *run.Service
}

func NewMux(d Deps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		if err := d.DB.PingContext(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "db"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	v1 := r.Group("/v1")
	{
		v1.POST("/sessions", func(c *gin.Context) {
			s, err := d.Sessions.Create(c.Request.Context())
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, s)
		})
		v1.GET("/sessions/:id", func(c *gin.Context) {
			s, err := d.Sessions.Get(c.Request.Context(), c.Param("id"))
			if errors.Is(err, sql.ErrNoRows) {
				c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
				return
			}
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, s)
		})
		v1.POST("/sessions/:id/messages", func(c *gin.Context) {
			var body struct {
				Text string `json:"text"`
			}
			if err := c.ShouldBindJSON(&body); err != nil || body.Text == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "text_required"})
				return
			}
			runID, err := d.Messages.PostUserMessage(
				c.Request.Context(), c.Param("id"), body.Text, d.Workspace,
			)
			if errors.Is(err, sql.ErrNoRows) {
				c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
				return
			}
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"run_id": runID})
		})
		v1.GET("/runs/:id", func(c *gin.Context) {
			out, err := d.Runs.Get(c.Request.Context(), c.Param("id"))
			if errors.Is(err, sql.ErrNoRows) {
				c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
				return
			}
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, out)
		})
	}
	return r
}