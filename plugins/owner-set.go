package plugins

import (
	"context"
	"fmt"
	"strings"
	"template-go/config"
)

var statusInfo = map[string]string{
	"public": "groups & private chats can use the bot",
	"ponly":  "private chats only",
	"gonly":  "groups only",
	"self":   "owner and bot only in private chat",
}

func init() {
	Register(&Plugin{
		Command:     []string{"set"},
		Description: "Change bot mode: public, ponly, gonly, or self",
		Category:    "owner",
		Handler:     ownerSetHandler,
	})
}

func ownerSetHandler(_ context.Context, c *Ctx) error {
	if !c.IsOwner {
		return nil
	}
	req := strings.ToLower(strings.TrimSpace(c.Q))
	if req == "" {
		return c.Reply(fmt.Sprintf(
			"Enter a status mode.\nExample: %sset public\n\nOptions:\n- public\n- ponly\n- gonly\n- self",
			c.Prefix))
	}
	valid := map[string]string{
		"public": "public", "pconly": "ponly", "ponly": "ponly",
		"gconly": "gonly", "gonly": "gonly", "self": "self", "selfonly": "self",
	}
	next, ok := valid[req]
	if !ok {
		return c.Reply("Invalid status. Choose: public, ponly, gonly, or self.")
	}
	cfg := config.Get()
	if next == cfg.Status {
		return c.Replyf("Bot status is already \"%s\" (%s).", next, statusInfo[next])
	}
	config.Update(func(conf *config.Config) { conf.Status = next })
	return c.Replyf("Bot status changed to \"%s\" (%s).", next, statusInfo[next])
}
