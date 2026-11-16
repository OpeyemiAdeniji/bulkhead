// Package cli holds the commands of bulkhead.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/OpeyemiAdeniji/bulkhead/internal/exitcode"
	"github.com/OpeyemiAdeniji/bulkhead/internal/version"
)

// Run runs bulkhead with the given arguments and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	root := newRoot(stdout, stderr)
	root.SetArgs(args)
	err := root.Execute()
	if err != nil {
		var e *exitcode.Err
		// Cobra reports an unknown command as a plain error; it is a usage error.
		if !errors.As(err, &e) && strings.HasPrefix(err.Error(), "unknown command") {
			err = exitcode.Usagef("%v", err)
		}
		fmt.Fprintln(stderr, "Error:", err)
	}
	return exitcode.Of(err)
}

func newRoot(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "bulkhead",
		Short: "Bulkhead: tenant isolation for Kubernetes, and a check that proves it",
		Long: "Bulkhead gives each tenant of a shared Kubernetes cluster its own namespace with\n" +
			"quotas, access rules, default-deny network policies and admission policies.\n" +
			"bulkhead check tests that isolation on a running cluster.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	// Wrong flags and arguments are usage errors (exit code 2).
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return exitcode.Usagef("%v", err) })
	root.AddCommand(newVersion(), newCheck())
	return root
}

func newVersion() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version of bulkhead",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := version.Get()
			switch output {
			case "text":
				fmt.Fprintln(cmd.OutOrStdout(), info)
				return nil
			case "json":
				return writeJSON(cmd.OutOrStdout(), info)
			}
			return exitcode.Usagef("unknown output %q (known: text, json)", output)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "text", "output format: text or json")
	return cmd
}

func noArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return exitcode.Usagef("unexpected argument %q", args[0])
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
