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
		if len(c.References) > 0 {
			parts := make([]string, 0, len(c.References))
			for _, r := range c.References {
				parts = append(parts, fmt.Sprintf("%s %s %s (%s)", sourceLabel[r.Source], r.ID, r.Title, r.Relation))
			}
			fmt.Fprintf(&b, "- **Benchmarks** ([mapping](benchmarks.md)): %s\n", strings.Join(parts, "; "))
		}
	}
	return b.String()
}

// sourceLabel is the short name of each benchmark in the pages.
var sourceLabel = map[Source]string{MTB: "Multi-tenancy benchmark", CIS: "CIS"}

// BenchmarksMarkdown renders the page docs/benchmarks.md: for each benchmark, every item and
// the checks that test it.
func BenchmarksMarkdown() string {
	var b strings.Builder
	b.WriteString("<!-- Generated from internal/catalog by `make checks-doc`. Do not edit by hand. -->\n\n")
	b.WriteString("# The checks and published benchmarks\n\n")
	b.WriteString("This page links the checks of `bulkhead check` ([checks.md](checks.md)) to the items of published benchmarks, for readers who know them. ")
	b.WriteString("It was made by reading each item and each check, and it maps only where they overlap.\n\n")
	b.WriteString("- **tests:** the check tests the behaviour the item asks for, on the tenants under test.\n")
	b.WriteString("- **partly:** the check tests a part of it. For example, a check that a tenant cannot read another tenant's secrets is a part of an item about all of another tenant's objects.\n")
	b.WriteString("- **differs:** Bulkhead deliberately blocks what the item asks to allow.\n\n")
	b.WriteString("A pass is evidence for an item on the cluster and the tenants tested, not a certification. ")
	b.WriteString("Most CIS items are about configuration that a person reviews (\"Manual\" in the benchmark), and the checks test the behaviour that such configuration should produce, from the tenant's side. ")
	b.WriteString("An item without a check is not tested by Bulkhead.\n")

	all := All()
	for _, src := range Sources() {
		fmt.Fprintf(&b, "\n## %s\n\nSource: %s\n\n", src.Name, src.URL)
		b.WriteString(sourceNotes[src.Source])
		b.WriteString("| Item | Title | Checks |\n|---|---|---|\n")
		tested := 0
		for _, item := range src.Items {
			var hits []string
			for _, c := range all {
				for _, r := range c.References {
					if r.Source == src.Source && r.ID == item.ID && r.Title == item.Title {
						hits = append(hits, fmt.Sprintf("[%s](checks.md#%s) (%s)", c.ID, anchor(c.ID), r.Relation))
					}
				}
			}
			cell := "not tested"
			if len(hits) > 0 {
				cell = strings.Join(hits, ", ")
				tested++
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", item.ID, escape(item.Title), cell)
		}
		fmt.Fprintf(&b, "\n%d of %d items have at least one check.\n", tested, len(src.Items))
	}
	return b.String()
}

// sourceNotes say how each benchmark was read, and what to know about it.
var sourceNotes = map[Source]string{
	MTB: "The benchmarks were archived in 2023, and their README said they were not ready for use. The items are taken from the `config.yaml` of each test of `kubectl-mtb`. " +
		"The source gives the ID MTB-PL1-BC-HI-1 to two tests (host path volumes and NodePort services), so the title tells them apart. " +
		"Two level 2 items ask that tenants may create their own role bindings and network policies. Bulkhead's tenant admin may not create role bindings, by design (GRD-05), because a binding to the cluster role `admin` is a way to escalate.\n\n",
	CIS: "The IDs and titles are those of section 5 (Policies) of the CIS Kubernetes Benchmark v2.0, as listed in the `cis-2.0` configuration of kube-bench, which uses that edition for Kubernetes 1.34 and 1.35. " +
		"Sections 1 to 4 are about the control plane and the nodes, which tenants do not reach, and are not mapped. The full benchmark is available from the Center for Internet Security.\n\n",
}

func anchor(id string) string { return strings.ToLower(id) }

func escape(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
