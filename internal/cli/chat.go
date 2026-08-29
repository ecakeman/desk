package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"desk/internal/event"
)

func Chat(c *Client, sessionID string) error {
	var err error
	if sessionID == "" {
		sessionID, err = c.CreateSession()
		if err != nil {
			return err
		}
	}
	fmt.Println("session", sessionID)
	stdin := bufio.NewScanner(os.Stdin)
	stdin.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var lastRun string
	for {
		fmt.Print("> ")
		if !stdin.Scan() {
			return stdin.Err()
		}
		line := strings.TrimSpace(stdin.Text())
		if line == "" {
			continue
		}
		if line == "/quit" {
			return nil
		}
		if line == "/stop" {
			if lastRun == "" {
				fmt.Println("no run")
				continue
			}
			if err := c.Cancel(lastRun); err != nil {
				fmt.Println(err)
			}
			continue
		}
		runID, err := c.PostMessage(sessionID, line)
		if err != nil {
			return err
		}
		lastRun = runID
		if err := follow(c, runID, stdin); err != nil {
			return err
		}
	}
}

func follow(c *Client, runID string, stdin *bufio.Scanner) error {
	resp, err := c.HTTP.Get(c.Base + "/v1/runs/" + runID + "/events?after=0")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var seq int
	var name string
	var args json.RawMessage
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e event.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
			return err
		}
		switch e.Type {
		case "message.delta":
			var p struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(e.Payload, &p)
			fmt.Print(p.Text)
		case "tool.requested":
			seq = e.Seq
			var p struct {
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			}
			_ = json.Unmarshal(e.Payload, &p)
			name, args = p.Name, p.Args
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				st, err := c.GetRunStatus(runID)
				if err != nil {
					return err
				}
				if st == "waiting_approval" {
					fmt.Printf("\nallow %s %s? [y/n] ", name, args)
					if !stdin.Scan() {
						return stdin.Err()
					}
					allow := strings.EqualFold(strings.TrimSpace(stdin.Text()), "y")
					if err := c.Decide(runID, seq, allow); err != nil {
						return err
					}
					break
				}
				if st != "running" {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
		case "run.completed", "run.failed", "run.interrupted":
			fmt.Println()
			fmt.Println(e.Type)
			return nil
		}
	}
	return sc.Err()
}
