package plugins

import (
	"context"
	"fmt"
	"template-go/store"
)

func init() {
	Register(&Plugin{
		Command:     []string{"profile"},
		Description: "View user profile & premium status",
		Category:    "info",
		Handler:     infoProfileHandler,
	})
}

func infoProfileHandler(_ context.Context, c *Ctx) error {
	targetPhone := c.SenderPhone
	targetLid := ""

	if c.IsOwner {
		if t, lid, err := ResolveTargetPhone(c); err == nil && t != "" {
			targetPhone = t
			targetLid = lid
		}
	}

	profile := store.GetPremiumProfile(targetPhone, targetLid)
	display := targetPhone
	if display == "" {
		display = c.SenderPhone
	}
	text := fmt.Sprintf(
		"Profile @%s\n\nPremium: %s\nCredits: %d",
		display,
		map[bool]string{true: "Yes", false: "No"}[profile.IsPremium],
		profile.Credits,
	)
	return c.ReplyMention(text, []string{display})
}
