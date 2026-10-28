package plugins

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"template-go/config"
)

var sectionDefs = []struct {
	title      string
	key        string
	categories []string
	order      []string
}{
	{"Information", "information", []string{"info", "utility"}, []string{"profile", "menu", "ping", "hello"}},
}

var ownerOrder = []string{"$", "addprem", "ban", "set", "setp"}

func init() {
	Register(&Plugin{
		Command:     []string{"menu", "help"},
		Description: "Show the command list",
		Category:    "info",
		Handler:     infoMenuHandler,
	})
}

func infoMenuHandler(_ context.Context, c *Ctx) error {
	arg := ""
	if len(c.Args) > 0 {
		arg = strings.ToLower(strings.TrimSpace(c.Args[0]))
	}

	validArgs := map[string]bool{
		"":            true,
		"all":         true,
		"info":        true,
		"information": true,
		"owner":       true,
	}

	if !validArgs[arg] {
		return nil
	}

	cfg := config.Get()
	all := All()

	var visible []*Plugin
	for _, p := range all {
		if p.Category != "hidden" && len(p.Command) > 0 {
			visible = append(visible, p)
		}
	}

	used := make(map[*Plugin]bool)
	var allSections []struct {
		title   string
		key     string
		plugins []*Plugin
	}

	for _, sec := range sectionDefs {
		var sps []*Plugin
		for _, p := range visible {
			if inSlice(p.Category, sec.categories) && !used[p] {
				sps = append(sps, p)
			}
		}
		if len(sps) > 0 {
			sort.Slice(sps, func(i, j int) bool {
				leftPrimary := sps[i].Command[0]
				rightPrimary := sps[j].Command[0]
				leftIndex := orderIndex(leftPrimary, sec.order)
				rightIndex := orderIndex(rightPrimary, sec.order)
				if leftIndex != rightIndex {
					return leftIndex < rightIndex
				}
				return leftPrimary < rightPrimary
			})
			for _, p := range sps {
				used[p] = true
			}
			allSections = append(allSections, struct {
				title   string
				key     string
				plugins []*Plugin
			}{sec.title, sec.key, sps})
		}
	}

	extraCats := map[string]bool{}
	for _, p := range visible {
		if !used[p] && p.Category != "owner" {
			extraCats[p.Category] = true
		}
	}
	var sortedCats []string
	for cat := range extraCats {
		sortedCats = append(sortedCats, cat)
	}
	sort.Strings(sortedCats)
	for _, cat := range sortedCats {
		var sps []*Plugin
		for _, p := range visible {
			if !used[p] && p.Category == cat {
				sps = append(sps, p)
				used[p] = true
			}
		}
		if len(sps) > 0 {
			sort.Slice(sps, func(i, j int) bool { return sps[i].Command[0] < sps[j].Command[0] })
			allSections = append(allSections, struct {
				title   string
				key     string
				plugins []*Plugin
			}{capitalize(cat), strings.ToLower(cat), sps})
		}
	}

	if c.IsOwner {
		var ops []*Plugin
		for _, p := range visible {
			if p.Category == "owner" {
				ops = append(ops, p)
			}
		}
		if len(ops) > 0 {
			sort.Slice(ops, func(i, j int) bool {
				leftPrimary := ops[i].Command[0]
				rightPrimary := ops[j].Command[0]
				leftIndex := orderIndex(leftPrimary, ownerOrder)
				rightIndex := orderIndex(rightPrimary, ownerOrder)
				if leftIndex != rightIndex {
					return leftIndex < rightIndex
				}
				return leftPrimary < rightPrimary
			})
			allSections = append(allSections, struct {
				title   string
				key     string
				plugins []*Plugin
			}{"Owner", "owner", ops})
		}
	}

	var sectionsToShow []struct {
		title   string
		key     string
		plugins []*Plugin
	}

	if arg == "" || arg == "all" {
		sectionsToShow = allSections
	} else {
		targetKey := arg
		if arg == "info" {
			targetKey = "information"
		}
		for _, s := range allSections {
			if s.key == targetKey {
				sectionsToShow = append(sectionsToShow, s)
			}
		}
	}

	if len(sectionsToShow) == 0 {
		return nil
	}

	lines := []string{
		fmt.Sprintf("╭─── %s ───", cfg.Name),
		"│",
		fmt.Sprintf("│ Status: %s", cfg.Status),
		fmt.Sprintf("│ Prefix: %s", c.Prefix),
	}

	for _, sec := range sectionsToShow {
		lines = append(lines, "│", fmt.Sprintf("│ *%s*", sec.title))
		for _, p := range sec.plugins {
			formatted := formatCmds(p.Command, c.Prefix)
			lines = append(lines, "│ ▸ "+formatted)
		}
	}

	lines = append(lines, "│", fmt.Sprintf("│ _%s_", cfg.Name), "╰────────────────")
	return c.Reply(strings.Join(lines, "\n"))
}

func formatCmds(cmds []string, prefix string) string {
	parts := make([]string, len(cmds))
	for i, cmd := range cmds {
		if cmd == "$" {
			parts[i] = cmd
		} else {
			parts[i] = prefix + cmd
		}
	}
	return strings.Join(parts, ", ")
}

func orderIndex(cmd string, order []string) int {
	for i, o := range order {
		if o == cmd {
			return i
		}
	}
	return len(order) + 999
}

func inSlice(s string, sl []string) bool {
	for _, v := range sl {
		if v == s {
			return true
		}
	}
	return false
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
