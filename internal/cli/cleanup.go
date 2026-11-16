package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/OpeyemiAdeniji/bulkhead/internal/exitcode"
	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

func newCleanup() *cobra.Command {
	var (
		k       kube.Options
		ns      []string
		runID   string
		allRuns bool
		force   bool
	)
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove test objects that a run of bulkhead check left behind",
		Long: "check removes its test objects at the end of every run, also after Ctrl+C. If a run\n" +
			"was killed (for example with kill -9, or when the machine stopped), its objects remain.\n" +
			"cleanup removes them: only objects with the label app.kubernetes.io/managed-by=bulkhead-check,\n" +
			"of one run (--run-id) or of every run (--all-runs), in the namespaces you name.",
		Example: "  bulkhead cleanup --tenant tenant-alpha --tenant tenant-bravo --run-id 20261116t091010-1a2b3c4d\n" +
			"  bulkhead cleanup --tenant tenant-alpha --all-runs",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (runID == "") == !allRuns {
				return exitcode.Usagef("give either --run-id or --all-runs")
			}
			if err := (tenant.Options{Namespaces: ns, AdminBinding: "-", Force: force}).Validate(); err != nil {
				return exitcode.Usagef("%v", err)
			}
			cluster, err := connect(k)
			if err != nil {
				return exitcode.Errorf("%v", err)
			}
			deleted, err := sandbox.Cleanup(cmd.Context(), cluster.Client, ns, runID)
			kinds := make([]string, 0, len(deleted))
			for kind := range deleted {
				kinds = append(kinds, kind)
			}
			sort.Strings(kinds)
			w := cmd.OutOrStdout()
			if _, perr := fmt.Fprintf(w, "Removed %d test objects from context %s.\n", deleted.Total(), cluster.Context); perr != nil {
				return perr
			}
			for _, kind := range kinds {
				if _, perr := fmt.Fprintf(w, "  %s: %d\n", kind, deleted[kind]); perr != nil {
					return perr
				}
			}
			if err != nil {
				return exitcode.Errorf("some objects were not removed: %v", err)
			}
			return nil
		},
	}
	f := cmd.Flags()
	addKubeFlags(f, &k)
	f.StringSliceVar(&ns, "tenant", nil, "a namespace to clean (repeat)")
	f.StringVar(&runID, "run-id", "", "remove the objects of this run (printed by bulkhead check)")
	f.BoolVar(&allRuns, "all-runs", false, "remove the objects of every run of the checker")
	f.BoolVar(&force, "force-namespaces", false, "allow system namespaces")
	return cmd
}
