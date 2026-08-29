package cli

import "fmt"

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
