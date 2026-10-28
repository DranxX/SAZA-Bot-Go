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
		Command:     []string{"ban", "unban", "listban", "banlist"},
		Description: "Ban/unban users from using the bot",
		Category:    "owner",
		Handler:     ownerBanHandler,
	})
}

func ownerBanHandler(_ context.Context, c *Ctx) error {
	if !c.IsOwner {
		return nil
	}

	switch c.CommandName {
	case "listban", "banlist":
		list := store.ListBanned()
		if len(list) == 0 {
			return c.Reply("No banned users.")
		}
		now := time.Now().UnixMilli()
		var sb strings.Builder
		sb.WriteString("*Banned Users:*\n\n")
		phones := make([]string, 0, len(list))
		for i, e := range list {
			num := e.Number
			if num == "" {
				num = PhoneFromJID(e.JID)
			}
			phones = append(phones, num)
			var remaining string
			if e.Expiry == nil || *e.Expiry == 0 {
				remaining = "Permanent"
			} else {
				secs := (*e.Expiry - now) / 1000
				if secs < 0 {
					secs = 0
				}
				remaining = FormatDuration(secs)
			}
			sb.WriteString(fmt.Sprintf("%d. @%s\n   Remaining: %s\n\n", i+1, num, remaining))
		}
		return c.ReplyMention(strings.TrimRight(sb.String(), "\n"), phones)

	case "ban":
		phone, lid, err := ResolveTargetPhone(c)
		if err != nil {
			return c.Reply("Tag/reply/enter a user number!")
		}
		if IsTargetOwnerOrBot(phone, c) {
			return c.ReplyMention(fmt.Sprintf("@%s cannot be banned (owner/bot).", phone), []string{phone})
		}
		durSec := ParseDuration(findDurationArg(c.Args))
		durMs := durSec * 1000
		store.AddBan(phone, lid, "banned by owner", durMs)
		durText := FormatDuration(durSec)
		if durSec == 0 {
			durText = "permanent"
		}
		return c.ReplyMention(fmt.Sprintf("@%s has been banned (%s).", phone, durText), []string{phone})

	case "unban":
		phone, _, err := ResolveTargetPhone(c)
		if err != nil {
			return c.Reply("Tag/reply/enter a user number!")
		}
		if !store.RemoveBan(phone) {
			return c.ReplyMention(fmt.Sprintf("@%s is not in the ban list.", phone), []string{phone})
		}
		return c.ReplyMention(fmt.Sprintf("@%s has been unbanned.", phone), []string{phone})
	}
	return nil
}

func findDurationArg(args []string) string {
	for _, a := range args {
		for _, r := range a {
			if r == 's' || r == 'm' || r == 'h' || r == 'd' {
				return a
			}
		}
	}
	return ""
}
