package catalog

import (
	"fmt"
	"strings"
)

var categoryTitles = map[Category]string{
	Access:     "Access between tenants",
	Guardrails: "Guardrails the tenant cannot change",
	Admission:  "Admission of pods",
	Network:    "Network isolation",
	Resources:  "Noisy neighbours",
	Quota:      "Quota and limits",
	Operators:  "Operators (research, off by default)",
}

// Markdown renders the catalogue as the page docs/checks.md.
func Markdown() string {
	var b strings.Builder
	all := All()
	b.WriteString("<!-- Generated from internal/catalog by `make checks-doc`. Do not edit by hand. -->\n\n")
	b.WriteString("# The checks of bulkhead check\n\n")
	fmt.Fprintf(&b, "The catalogue has %d checks. Every check that must be blocked is paired with a check that must work, ", len(all))
	b.WriteString("and its result counts only when that pair passed, so a broken test cannot pass by accident ")
	b.WriteString("([ADR 0012](adr/0012-active-tenant-isolation-checker.md)).\n\n")
	b.WriteString("- **Level:** basic checks run by default; hardened checks are selected on request, because some platforms allow what they block on purpose; research checks are experimental.\n")
	b.WriteString("- **Mode:** read-only checks only read, ask for access reviews or send server-side dry runs, so they are safe on production clusters (`--read-only`). Active checks create short-lived, labelled test objects and remove them.\n")
	b.WriteString("- **Tenant A and tenant B** stand for every ordered pair of the tenants under test.\n\n")
	b.WriteString("The same list is printed by `bulkhead check --list`, and as JSON by `bulkhead check --list -o json`.\n\n")

	b.WriteString("| ID | Level | Mode | Expect | Pair | Title |\n|---|---|---|---|---|---|\n")
	for _, c := range all {
		pair := "-"
		if c.Pair != "" {
			pair = fmt.Sprintf("[%s](#%s)", c.Pair, anchor(c.Pair))
		}
		fmt.Fprintf(&b, "| [%s](#%s) | %s | %s | %s | %s | %s |\n", c.ID, anchor(c.ID), c.Level, c.Mode, c.Expect, pair, escape(c.Title))
	}

	var current Category
	for _, c := range all {
		if c.Category != current {
			current = c.Category
			fmt.Fprintf(&b, "\n## %s\n", categoryTitles[current])
		}
		fmt.Fprintf(&b, "\n### %s\n\n**%s**\n\n", c.ID, c.Title)
		pair := "none, this check makes other results trustworthy"
		if c.Pair != "" {
			pair = fmt.Sprintf("[%s](#%s)", c.Pair, anchor(c.Pair))
		}
		fmt.Fprintf(&b, "Level %s, %s, expected result: %s. Pair: %s.\n\n", c.Level, c.Mode, c.Expect, pair)
		fmt.Fprintf(&b, "- **Proves:** %s\n", c.Proves)
		fmt.Fprintf(&b, "- **Does not prove:** %s\n", c.DoesNotProve)
		fmt.Fprintf(&b, "- **How:** %s\n", c.How)
		fmt.Fprintf(&b, "- **Fix when it fails:** %s\n", c.Fix)
	}
	return b.String()
}

func anchor(id string) string { return strings.ToLower(id) }

func escape(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
