// Demo plugin — demonstrates how to write a Go plugin for this template.
//
// Key concepts shown here:
//   1. init() + Register() — self-registration at compile time
//   2. Plugin struct fields — Command, Description, Category, Handler
//   3. Ctx methods — Reply, ReplyMention, Replyf
//   4. Ctx fields — PushName, SenderPhone, IsGroup, Prefix
//
// To add your own plugin, just create a new .go file in the plugins/ directory
// with an init() that calls Register(), then rebuild with `go build`.
//
// Plugin categories: ai | info | utility | downloader | owner | hidden
//   - "hidden" plugins with a Before hook run on every message

package plugins

import (
	"context"
	"fmt"
)

func init() {
	Register(&Plugin{
		Command:     []string{"hello", "hi"},
		Description: "Greet the bot with your name",
		Category:    "info",
		Handler:     demoHelloHandler,
	})
}

func demoHelloHandler(_ context.Context, c *Ctx) error {
	name := c.PushName
	if name == "" {
		name = c.SenderPhone
	}

	if c.IsGroup {
		return c.ReplyMention(
			fmt.Sprintf("Hello @%s! 👋 Type %smenu to see all commands.", c.SenderPhone, c.Prefix),
			[]string{c.SenderPhone},
		)
	}
	return c.Reply(fmt.Sprintf("Hello %s! 👋 Type %smenu to see all commands.", name, c.Prefix))
}
