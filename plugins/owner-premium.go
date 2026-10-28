package plugins

import (
	"context"
	"fmt"
	"strings"
	"template-go/store"
	"time"
)

func init() {
	Register(&Plugin{
		Command:     []string{"addprem", "delprem", "listprem"},
		Description: "Manage premium users",
		Category:    "owner",
		Handler:     ownerPremiumHandler,
	})
}

func ownerPremiumHandler(_ context.Context, c *Ctx) error {
	if !c.IsOwner {
		return nil
	}
	switch c.CommandName {
	case "listprem":
		list := store.ListPremiumUsers()
		if len(list) == 0 {
			return c.Reply("No premium users.")
		}
		phones := make([]string, 0, len(list))
		var sb strings.Builder
		sb.WriteString("*Premium Users:*\n\n")
		now := time.Now().UnixMilli()
		for i, e := range list {
			num := e.Number
			if num == "" {
				num = PhoneFromJID(e.JID)
			}
			phones = append(phones, num)
			remaining := "Permanent"
			if e.Expiry != nil && *e.Expiry > 0 {
				secs := (*e.Expiry - now) / 1000
				if secs < 0 {
					secs = 0
				}
				remaining = FormatDuration(secs)
			}
			sb.WriteString(fmt.Sprintf("%d. @%s\n   Remaining: %s\n   Credits: %d\n\n",
				i+1, num, remaining, e.Credits))
		}
		return c.ReplyMention(strings.TrimRight(sb.String(), "\n"), phones)

	case "addprem":
		phone, lid, err := ResolveTargetPhone(c)
		if err != nil {
			return c.Reply("Target required — reply/tag/number.")
		}
		durSec := ParseDuration(findDurationArgPrem(c.Args))
		result := store.AddPremiumUser(phone, lid, durSec)
		durText := FormatDuration(durSec)
		if durSec == 0 {
			durText = "permanent"
		}
		action := "added to premium"
		if !result.Created {
			action = "premium duration updated"
		}
		return c.ReplyMention(
			fmt.Sprintf("@%s %s (%s).\nCredits: %d.", phone, action, durText, result.Entry.Credits),
			[]string{phone},
		)

	case "delprem":
		phone, _, err := ResolveTargetPhone(c)
		if err != nil {
			return c.Reply("Target required — reply/tag/number.")
		}
		result := store.DeletePremiumUser(phone)
		if !result.OK {
			return c.ReplyMention(fmt.Sprintf("@%s %s", phone, result.Error), []string{phone})
		}
		return c.ReplyMention(fmt.Sprintf("@%s removed from premium.", phone), []string{phone})
	}
	return nil
}

func findDurationArgPrem(args []string) string {
	for _, a := range args {
		for _, r := range a {
			if r == 's' || r == 'm' || r == 'h' || r == 'd' {
				return a
			}
		}
	}
	return ""
}
