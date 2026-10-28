package plugins

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

func init() {
	Register(&Plugin{
		Command:     []string{"$"},
		Description: "Execute terminal commands (owner only)",
		Category:    "owner",
		Handler:     ownerExecHandler,
	})
}

func ownerExecHandler(_ context.Context, c *Ctx) error {
	if !c.IsOwner {
		return nil
	}
	if c.Q == "" {
		return c.Reply("Enter a command to run!\nExample: $ ls")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", c.Q)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return c.Reply("Command timed out after 30 seconds.")
		}
		result := strings.TrimSpace(out.String())
		if result == "" {
			result = "Error: " + err.Error()
		}
		return c.Reply(result)
	}
	result := strings.TrimSpace(out.String())
	if result == "" {
		result = "Command executed successfully (no output)."
	}
	return c.Reply(result)
}
