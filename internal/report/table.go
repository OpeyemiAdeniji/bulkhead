package report

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
)

// writeTable writes the report for people: the run, the tenants, the requests of a dry run, one
// line per result, the summary and the clean-up.
func writeTable(w io.Writer, r *Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Cluster:  context %s, %s, Kubernetes %s\n", r.Run.Context, r.Run.Server, r.Run.Kubernetes)
	fmt.Fprintf(&b, "You:      %s\n", r.Run.User)
	fmt.Fprintf(&b, "Run:      %s (%s), profile %s\n", r.Run.ID, r.Run.Mode, r.Run.Profile)
	for i, t := range r.Tenants {
		label := "Tenants:"
		if i > 0 {
			label = ""
		}
		viewer := "no viewer"
		if t.Viewer != nil {
			viewer = "viewer " + t.Viewer.String()
		}
		fmt.Fprintf(&b, "%-9s %s: admin %s, %s\n", label, t.Namespace, t.Admin, viewer)
	}
	if r.Run.IdentitiesConfirmed {
		b.WriteString("          Every identity was confirmed with the API server (SelfSubjectReview).\n")
	}
	b.WriteString("\n")
	if len(r.Requests) > 0 {
		fmt.Fprintf(&b, "Dry run: nothing was sent. The run would send these %d requests:\n", len(r.Requests))
		for _, q := range r.Requests {
			fmt.Fprintf(&b, "  %s\n", q)
		}
		b.WriteString("\n")
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "CHECK\tSTATUS\tSUBJECT\tMESSAGE"); err != nil {
		return err
	}
	for _, res := range r.Results {
		subject := res.Subject
		if subject == "" {
			subject = "-"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", res.Check, res.Status, subject, res.Message); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	var tail strings.Builder
	fmt.Fprintf(&tail, "\n%s\n", r.Summary.Line())
	// One fix per check that did not pass, in the order of the results.
	seen := map[string]bool{}
	for _, res := range r.Results {
		if seen[res.Check] || (res.Status != runner.Fail && res.Status != runner.Inconclusive && res.Status != runner.Error) {
			continue
		}
		if !seen["header"] {
			tail.WriteString("\nHow to fix:\n")
			seen["header"] = true
		}
		seen[res.Check] = true
		c := r.Check(res.Check)
		fmt.Fprintf(&tail, "  %s %s\n      %s\n", c.ID, c.Title, c.Fix)
	}
	if seen["header"] {
		tail.WriteString("  Every check is described in docs/checks.md.\n\n")
	}
	if r.Cleanup != nil {
		fmt.Fprintf(&tail, "Clean-up: removed %d test objects of run %s.", r.Cleanup.Deleted, r.Run.ID)
		if r.Cleanup.Error != "" {
			tail.WriteString(" Errors: " + r.Cleanup.Error)
		}
		tail.WriteString("\n")
	}
	if len(r.WorkloadProbes) > 0 {
		fmt.Fprintf(&tail, "Probe containers were added to %s. Kubernetes keeps them in the pod's spec, stopped, until the pod is replaced.\n",
			strings.Join(r.WorkloadProbes, ", "))
	}
	if p := r.PolicyReports; p != nil {
		if len(p.Written) > 0 {
			fmt.Fprintf(&tail, "PolicyReports: wrote %s.", strings.Join(p.Written, ", "))
		} else {
			tail.WriteString("PolicyReports: none written.")
		}
		if p.LeftOut > 0 {
			fmt.Fprintf(&tail, " Results that name no tenant, so are in no report: %d.", p.LeftOut)
		}
		if p.Error != "" {
			tail.WriteString(" Errors: " + p.Error)
		}
		tail.WriteString("\n")
	}
	_, err := io.WriteString(w, tail.String())
	return err
}
