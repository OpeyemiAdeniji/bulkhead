package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/exitcode"
)

type checkOptions struct {
	list       bool
	output     string
	categories []string
	levels     []string
}

func newCheck() *cobra.Command {
	o := &checkOptions{}
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Test tenant isolation on a cluster",
		Long: "check tests the isolation between tenants on a running cluster: access, guardrails,\n" +
			"admission, network, resources and quotas. Every check that must be blocked is paired\n" +
			"with a check that must work, so a broken test cannot pass by accident.\n\n" +
			"This development version lists the catalogue of checks (--list). Running the checks\n" +
			"against a cluster is being built (docs/phase-2-checklist.md).",
		Example: "  bulkhead check --list\n" +
			"  bulkhead check --list --category network --level basic,hardened\n" +
			"  bulkhead check --list -o json",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return o.run(cmd.OutOrStdout()) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.list, "list", false, "list the checks instead of running them")
	f.StringVarP(&o.output, "output", "o", "table", "output format: table or json")
	f.StringSliceVar(&o.categories, "category", nil, "only these categories (repeat or separate with commas)")
	f.StringSliceVar(&o.levels, "level", nil, "only these levels: basic, hardened, research (default: all for --list)")
	return cmd
}

func (o *checkOptions) run(w io.Writer) error {
	if o.output != "table" && o.output != "json" {
		return exitcode.Usagef("unknown output %q (known: table, json)", o.output)
	}
	selected, err := o.selected()
	if err != nil {
		return err
	}
	if !o.list {
		return exitcode.Usagef("running the checks is not available in this development version yet; use --list to see the catalogue")
	}
	if o.output == "json" {
		return writeJSON(w, selected)
	}
	return writeTable(w, selected)
}

func (o *checkOptions) selected() ([]catalog.Check, error) {
	cats := map[catalog.Category]bool{}
	for _, s := range o.categories {
		c, err := catalog.ParseCategory(s)
		if err != nil {
			return nil, exitcode.Usagef("%v", err)
		}
		cats[c] = true
	}
	levels := map[catalog.Level]bool{}
	for _, s := range o.levels {
		l, err := catalog.ParseLevel(s)
		if err != nil {
			return nil, exitcode.Usagef("%v", err)
		}
		levels[l] = true
	}
	var out []catalog.Check
	for _, c := range catalog.All() {
		if len(cats) > 0 && !cats[c.Category] {
			continue
		}
		if len(levels) > 0 && !levels[c.Level] {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func writeTable(w io.Writer, checks []catalog.Check) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tLEVEL\tMODE\tEXPECT\tPAIR\tTITLE"); err != nil {
		return err
	}
	for _, c := range checks {
		pair := c.Pair
		if pair == "" {
			pair = "-"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", c.ID, c.Level, c.Mode, c.Expect, pair, c.Title); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%d checks. Details of one check: bulkhead check --list -o json | jq '.[] | select(.id == \"NET-04\")'\n", len(checks))
	return err
}
