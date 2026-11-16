package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/pflag"

	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

// report is the result of one run. Its JSON form gets a published schema in work package 5
// of docs/phase-2-checklist.md; until then it may change.
type report struct {
	Run      runInfo               `json:"run"`
	Tenants  []tenant.Tenant       `json:"tenants"`
	Results  []runner.Result       `json:"results"`
	Requests []sandbox.Request     `json:"requests,omitempty"`
	Cleanup  *cleanupInfo          `json:"cleanup,omitempty"`
	Summary  map[runner.Status]int `json:"summary,omitempty"`
}

type runInfo struct {
	ID                  string `json:"id"`
	Mode                string `json:"mode"`
	Context             string `json:"context"`
	Server              string `json:"server"`
	Kubernetes          string `json:"kubernetes"`
	User                string `json:"user"`
	IdentitiesConfirmed bool   `json:"identitiesConfirmed"`
}

type cleanupInfo struct {
	Deleted int    `json:"deleted"`
	Error   string `json:"error,omitempty"`
}

func addKubeFlags(f *pflag.FlagSet, o *kube.Options) {
	f.StringVar(&o.Kubeconfig, "kubeconfig", "", "path to the kubeconfig (default: $KUBECONFIG or ~/.kube/config)")
	f.StringVar(&o.Context, "context", "", "kubeconfig context to use (default: the current context)")
}

func writeRunTable(w io.Writer, r *report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Cluster:  context %s, %s, Kubernetes %s\n", r.Run.Context, r.Run.Server, r.Run.Kubernetes)
	fmt.Fprintf(&b, "You:      %s\n", r.Run.User)
	fmt.Fprintf(&b, "Run:      %s (%s)\n", r.Run.ID, r.Run.Mode)
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
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}

	if len(r.Requests) > 0 {
		if _, err := fmt.Fprintf(w, "Dry run: nothing was sent. The run would send these %d requests:\n", len(r.Requests)); err != nil {
			return err
		}
		for _, q := range r.Requests {
			if _, err := fmt.Fprintf(w, "  %s\n", q); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
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
	if _, err := fmt.Fprintf(w, "\n%s\n", summaryLine(r.Results)); err != nil {
		return err
	}
	if r.Cleanup != nil {
		msg := fmt.Sprintf("Clean-up: removed %d test objects of run %s.", r.Cleanup.Deleted, r.Run.ID)
		if r.Cleanup.Error != "" {
			msg += " Errors: " + r.Cleanup.Error
		}
		if _, err := fmt.Fprintln(w, msg); err != nil {
			return err
		}
	}
	return nil
}

// summaryLine counts the results by status, in a fixed order.
func summaryLine(results []runner.Result) string {
	n := runner.Count(results)
	order := []runner.Status{runner.Pass, runner.Fail, runner.Inconclusive, runner.Error, runner.Skipped, runner.NotImplemented, runner.Planned}
	var parts []string
	for _, s := range order {
		if n[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n[s], s))
		}
	}
	// Statuses not in the order above, if any are added later.
	var rest []string
	for s, c := range n {
		known := false
		for _, o := range order {
			known = known || o == s
		}
		if !known {
			rest = append(rest, fmt.Sprintf("%d %s", c, s))
		}
	}
	sort.Strings(rest)
	parts = append(parts, rest...)
	return fmt.Sprintf("%d results: %s", len(results), strings.Join(parts, ", "))
}
