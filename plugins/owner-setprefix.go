package plugins

import (
	"context"
	"strings"
	"template-go/config"
)

func init() {
	Register(&Plugin{
		Command:     []string{"setp", "setprefix"},
		Description: "Change the command prefix",
		Category:    "owner",
		Handler:     ownerSetprefixHandler,
	})
}

func ownerSetprefixHandler(_ context.Context, c *Ctx) error {
	if !c.IsOwner {
		return nil
	}
	next := strings.TrimSpace(c.Q)
	if next == "" {
		return c.Replyf("Enter a new prefix.\nExample: %ssetp !", c.Prefix)
	}
	if strings.ContainsAny(next, " \t\n") {
		return c.Reply("Prefix cannot contain spaces.")
	}
	cfg := config.Get()
	if next == cfg.Prefix {
		return c.Replyf("Prefix is already \"%s\".", next)
	}
	prev := cfg.Prefix
	config.Update(func(conf *config.Config) { conf.Prefix = next })
	return c.Replyf("Prefix changed from \"%s\" to \"%s\".", prev, next)
}
