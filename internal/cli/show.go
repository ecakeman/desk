package cli

import "fmt"

// Show 打印 Session 时间线。
func Show(c *Client, sessionID string) error {
	evs, err := c.ListSessionEvents(sessionID)
	if err != nil {
		return err
	}
	for _, e := range evs {
		fmt.Printf("%s %d %s %s\n", e.RunID, e.Seq, e.Type, e.Payload)
	}
	return nil
}
