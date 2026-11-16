package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/checks" // also registers the implemented checks
	"github.com/OpeyemiAdeniji/bulkhead/internal/exitcode"
	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

// connect and impersonate are replaced in tests.
var (
	connect     = kube.Connect
	impersonate = func(c *kube.Cluster) func(kube.Identity) (*kube.Cluster, error) { return c.Impersonate }
)

type checkOptions struct {
	list       bool
	output     string
	categories []string
	levels     []string
	ids        []string

	kube         kube.Options
	tenants      tenant.Options
	readOnly     bool
	dryRun       bool
	timeout      time.Duration
	checkTimeout time.Duration
}

func newCheck() *cobra.Command {
	o := &checkOptions{}
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Test tenant isolation on a cluster",
		Long: "check tests the isolation between tenants on a running cluster: access, guardrails,\n" +
			"admission, network, resources and quotas. Every check that must be blocked is paired\n" +
			"with a check that must work, and its result counts only when the pair passed.\n\n" +
			"The checker acts as each tenant through impersonation, so your own identity needs the\n" +
			"impersonate permission (cluster admins have it). Before any check, it confirms each\n" +
			"tenant identity with the API server. Test objects carry the labels\n" +
			"app.kubernetes.io/managed-by=bulkhead-check and bulkhead.io/run-id, and are removed at\n" +
			"the end, also after Ctrl+C. --read-only stores nothing; --dry-run sends nothing.\n\n" +
			"The checks are being built (docs/phase-2-checklist.md). Checks without an\n" +
			"implementation are reported as not-implemented.",
		Example: "  bulkhead check --list\n" +
			"  bulkhead check --tenant tenant-alpha --tenant tenant-bravo --dry-run\n" +
			"  bulkhead check --context prod --tenant team-a --tenant team-b --read-only\n" +
			"  bulkhead check --tenant tenant-alpha --tenant tenant-bravo --category network -o json",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return o.run(cmd.Context(), cmd.OutOrStdout()) },
	}
	f := cmd.Flags()
	f.BoolVar(&o.list, "list", false, "list the checks instead of running them")
	f.StringVarP(&o.output, "output", "o", "table", "output format: table or json")
	f.StringSliceVar(&o.categories, "category", nil, "only these categories (repeat or separate with commas)")
	f.StringSliceVar(&o.levels, "level", nil, "only these levels: basic, hardened, research (default: basic for a run, all for --list)")
	f.StringSliceVar(&o.ids, "id", nil, "only these checks, for example NET-04 (their pairs are added)")
	addKubeFlags(f, &o.kube)
	f.StringSliceVar(&o.tenants.Namespaces, "tenant", nil, "a tenant namespace to test (repeat, two or more for the checks between tenants)")
	f.StringVar(&o.tenants.AdminBinding, "admin-binding", tenant.DefaultAdminBinding, "role binding in each tenant namespace whose subject is the tenant admin")
	f.StringVar(&o.tenants.ViewerBinding, "viewer-binding", tenant.DefaultViewerBinding, "role binding whose subject is the tenant viewer (empty: no viewer checks)")
	f.BoolVar(&o.tenants.Force, "force-namespaces", false, "allow system namespaces such as kube-system as tenants")
	f.BoolVar(&o.readOnly, "read-only", false, "run only checks that store nothing (safe on production clusters)")
	f.BoolVar(&o.dryRun, "dry-run", false, "send nothing; print every request the run would send")
	f.DurationVar(&o.timeout, "timeout", 15*time.Minute, "time limit of the whole run")
	f.DurationVar(&o.checkTimeout, "check-timeout", 3*time.Minute, "time limit of each check")
	return cmd
}

func (o *checkOptions) run(ctx context.Context, w io.Writer) error {
	if o.output != "table" && o.output != "json" {
		return exitcode.Usagef("unknown output %q (known: table, json)", o.output)
	}
	if o.list {
		selected, err := o.selected(false)
		if err != nil {
			return err
		}
		if o.output == "json" {
			return writeJSON(w, selected)
		}
		return writeTable(w, selected)
	}
	if o.readOnly && o.dryRun {
		return exitcode.Usagef("--read-only and --dry-run cannot be combined: a dry run sends nothing at all")
	}
	if o.timeout <= 0 || o.checkTimeout <= 0 {
		return exitcode.Usagef("--timeout and --check-timeout must be positive")
	}
	if err := o.tenants.Validate(); err != nil {
		return exitcode.Usagef("%v", err)
	}
	selected, err := o.selected(true)
	if err != nil {
		return err
	}
	return o.execute(ctx, w, runner.WithPairs(selected))
}

func (o *checkOptions) execute(ctx context.Context, w io.Writer, selected []catalog.Check) error {
	cluster, err := connect(o.kube)
	if err != nil {
		return exitcode.Errorf("%v", err)
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	rep := report{Run: runInfo{Context: cluster.Context, Server: cluster.Server}}
	mode := sandbox.Live
	switch {
	case o.dryRun:
		mode = sandbox.DryRun
	case o.readOnly:
		mode = sandbox.ReadOnly
	}
	sb := sandbox.New(mode)
	rep.Run.ID, rep.Run.Mode = sb.RunID, string(mode)
	if rep.Run.Kubernetes, err = cluster.ServerVersion(); err != nil {
		return exitcode.Errorf("reach the cluster of context %q at %s: %v", cluster.Context, cluster.Server, err)
	}
	me, err := cluster.WhoAmI(ctx)
	if err != nil {
		return exitcode.Errorf("ask the API server who you are (SelfSubjectReview): %v", err)
	}
	rep.Run.User = me.String()
	tenants, err := tenant.Discover(ctx, cluster.Client, o.tenants)
	if err != nil {
		return exitcode.Errorf("%v", err)
	}
	rep.Tenants = tenants
	env := &runner.Env{Cluster: cluster, Tenants: tenants, Sandbox: sb, Impersonate: impersonate(cluster)}

	// From here on, objects may be created. Clean-up runs once: before the report is printed,
	// and on every early return. It uses a fresh context, so it also runs after Ctrl+C or the timeout.
	var cleaned bool
	cleanup := func() {
		if cleaned || mode != sandbox.Live {
			return
		}
		cleaned = true
		cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer ccancel()
		deleted, cerr := sandbox.Cleanup(cctx, cluster.Client, o.tenants.Namespaces, sb.RunID)
		rep.Cleanup = &cleanupInfo{Deleted: deleted.Total()}
		if cerr != nil {
			rep.Cleanup.Error = cerr.Error()
		}
	}
	defer cleanup()

	if err := runner.ConfirmIdentities(ctx, env); err != nil {
		return err
	}
	rep.Run.IdentitiesConfirmed = mode != sandbox.DryRun
	rep.Results = runner.Run(ctx, env, runner.Options{Checks: selected, CheckTimeout: o.checkTimeout})
	checks.ForgetRun(sb)
	if mode == sandbox.DryRun {
		rep.Requests = sb.Requests()
	}
	rep.Summary = runner.Count(rep.Results)
	interrupted := ctx.Err()
	cleanup()

	var perr error
	if o.output == "json" {
		perr = writeJSON(w, &rep)
	} else {
		perr = writeRunTable(w, &rep)
	}
	switch {
	case perr != nil:
		return perr
	case rep.Cleanup != nil && rep.Cleanup.Error != "":
		return exitcode.Errorf("clean-up of run %s was not complete: %s; remove the rest with: bulkhead cleanup --run-id %s", sb.RunID, rep.Cleanup.Error, sb.RunID)
	case errors.Is(interrupted, context.Canceled):
		return exitcode.Errorf("the run was interrupted; its test objects were removed")
	case errors.Is(interrupted, context.DeadlineExceeded):
		return exitcode.Errorf("the run reached its time limit of %s", o.timeout)
	case mode == sandbox.DryRun:
		return nil
	}
	if code := runner.ExitCode(rep.Results); code != exitcode.OK {
		return &exitcode.Err{Code: code, Err: errors.New(summaryLine(rep.Results))}
	}
	return nil
}

func (o *checkOptions) selected(run bool) ([]catalog.Check, error) {
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
	if run && len(levels) == 0 && len(o.ids) == 0 {
		levels[catalog.Basic] = true
	}
	ids := map[string]bool{}
	for _, id := range o.ids {
		c, ok := catalog.Get(strings.TrimSpace(id))
		if !ok {
			return nil, exitcode.Usagef("unknown check %q (bulkhead check --list shows them)", id)
		}
		ids[c.ID] = true
	}
	var out []catalog.Check
	for _, c := range catalog.All() {
		if len(cats) > 0 && !cats[c.Category] {
			continue
		}
		if len(levels) > 0 && !levels[c.Level] {
			continue
		}
		if len(ids) > 0 && !ids[c.ID] {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, exitcode.Usagef("no check matches the selection")
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
	_, err := fmt.Fprintf(w, "\n%d checks. Details of each check: docs/checks.md, or bulkhead check --list -o json\n", len(checks))
	return err
}
