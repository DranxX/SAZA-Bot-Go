package plugins

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"template-go/store"
)

func init() {
	Register(&Plugin{
		Command:     []string{"addcredit", "decredit", "setcredit"},
		Description: "Manage premium credits (add/decrease/set)",
		Category:    "owner",
		Handler:     ownerCreditHandler,
	})
}

const maxCreditAmount = 1_000_000

func ownerCreditHandler(_ context.Context, c *Ctx) error {
	if !c.IsOwner {
		return nil
	}

	phone, _, err := ResolveTargetPhone(c)
	if err != nil {
		return c.Reply("Target required — reply/tag/number.")
	}
	amount, ok := findCreditAmount(c.Args)
	if !ok {
		return c.Replyf("Amount required, e.g. %s%s @user 10", c.Prefix, c.CommandName)
	}

	var result store.PremiumResult
	var verb string
	switch c.CommandName {
	case "addcredit":
		result = store.AddPremiumCredits(phone, amount)
		verb = fmt.Sprintf("+%d credits", amount)
	case "decredit":
		result = store.RemovePremiumCredits(phone, amount)
		verb = fmt.Sprintf("-%d credits", amount)
	case "setcredit":
		result = store.SetPremiumCredits(phone, amount)
		verb = fmt.Sprintf("credits set to %d", amount)
	}
	if !result.OK {
		return c.ReplyMention(fmt.Sprintf("@%s %s", phone, result.Error), []string{phone})
	}
	return c.ReplyMention(
		fmt.Sprintf("@%s %s.\nCredits: %d.", phone, verb, result.Entry.Credits),
		[]string{phone},
	)
}

// findCreditAmount returns the first plain integer argument, capped at maxCreditAmount.
func findCreditAmount(args []string) (int, bool) {
	for _, a := range args {
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		if n, err := strconv.Atoi(a); err == nil {
			return min(n, maxCreditAmount), true
		}
	}
	return 0, false
}
