package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"desk/internal/event"
	"desk/internal/run"
)

type Client struct {
	Base string
	HTTP *http.Client
}

func New(addr string) *Client {
	base := addr
	if strings.HasPrefix(base, ":") {
		base = "http://127.0.0.1" + base
	} else if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{}}
}

func (c *Client) doJSON(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.Base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: %s %s", method, path, resp.Status, string(b))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) CreateSession() (string, error) {
	var s struct {
		ID string `json:"id"`
	}
	if err := c.doJSON(http.MethodPost, "/v1/sessions", nil, &s); err != nil {
		return "", err
	}
	return s.ID, nil
}

func (c *Client) PostMessage(sessionID, text string) (string, error) {
	var out struct {
		RunID string `json:"run_id"`
	}
	err := c.doJSON(http.MethodPost, "/v1/sessions/"+sessionID+"/messages", map[string]string{"text": text}, &out)
	if err != nil {
		return "", err
	}
	return out.RunID, nil
}

func (c *Client) GetRunStatus(runID string) (string, error) {
	var r run.Run
	if err := c.doJSON(http.MethodGet, "/v1/runs/"+runID, nil, &r); err != nil {
		return "", err
	}
	return r.Status, nil
}

func (c *Client) Decide(runID string, seq int, allow bool) error {
	return c.doJSON(http.MethodPost, "/v1/runs/"+runID+"/decisions", map[string]any{
		"seq": seq, "allow": allow,
	}, nil)
}

func (c *Client) Cancel(runID string) error {
	return c.doJSON(http.MethodPost, "/v1/runs/"+runID+"/cancel", map[string]any{}, nil)
}

func (c *Client) ListSessionEvents(sessionID string) ([]event.Event, error) {
	var evs []event.Event
	if err := c.doJSON(http.MethodGet, "/v1/sessions/"+sessionID+"/events", nil, &evs); err != nil {
		return nil, err
	}
	return evs, nil
}
