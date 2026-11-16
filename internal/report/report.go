// Package report holds the result of one run of bulkhead check, and writes it in every format:
// a table for people, JSON with a published schema (schemas/check-report.schema.json), JUnit XML
// for CI systems and SARIF for code scanning.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
	"github.com/OpeyemiAdeniji/bulkhead/internal/version"
)

// APIVersion and Kind identify the JSON form. v1alpha1 may change before v1.0 of Bulkhead; a
// change is announced in the changelog, and the schema in schemas/ always matches the code.
const (
	APIVersion = "bulkhead.io/v1alpha1"
	Kind       = "CheckReport"
)

// Formats are the output formats, in the order they are listed in help texts.
var Formats = []string{"table", "json", "junit", "sarif"}

// Report is the result of one run.
type Report struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Tool       Tool              `json:"tool"`
	Run        Run               `json:"run"`
	Tenants    []tenant.Tenant   `json:"tenants"`
	Checks     []catalog.Check   `json:"checks"`
	Results    []runner.Result   `json:"results"`
	Requests   []sandbox.Request `json:"requests,omitempty"`
	Cleanup    *Cleanup          `json:"cleanup,omitempty"`
	Summary    Summary           `json:"summary"`
	// ExitCode is the exit code of the run (docs/adr/0018-checker-in-this-repository.md), so a
	// CI system that keeps only the file still knows the outcome.
	ExitCode int `json:"exitCode"`
}

// Tool names the program that wrote the report.
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// Run describes the run.
type Run struct {
	ID                  string    `json:"id"`
	Mode                string    `json:"mode"`
	StartedAt           time.Time `json:"startedAt"`
	FinishedAt          time.Time `json:"finishedAt"`
	Context             string    `json:"context"`
	Server              string    `json:"server"`
	Kubernetes          string    `json:"kubernetes"`
	User                string    `json:"user"`
	IdentitiesConfirmed bool      `json:"identitiesConfirmed"`
	// Interrupted is "interrupted" (Ctrl+C or SIGTERM), "timeout" or empty.
	Interrupted string `json:"interrupted,omitempty"`
}

// Cleanup says what the run removed at its end.
type Cleanup struct {
	Deleted int    `json:"deleted"`
	Error   string `json:"error,omitempty"`
}

// Summary counts the results by status.
type Summary struct {
	Total          int `json:"total"`
	Pass           int `json:"pass"`
	Fail           int `json:"fail"`
	Inconclusive   int `json:"inconclusive"`
	Error          int `json:"error"`
	Skipped        int `json:"skipped"`
	NotImplemented int `json:"notImplemented"`
	Planned        int `json:"planned"`
}

// New returns a report with the tool and the checks of the run filled in.
func New(checks []catalog.Check) *Report {
	v := version.Get()
	return &Report{
		APIVersion: APIVersion,
		Kind:       Kind,
		Tool:       Tool{Name: "bulkhead", Version: v.Version, Commit: v.Commit},
		Checks:     checks,
	}
}

// Summarise fills in the summary from the results.
func (r *Report) Summarise() {
	n := runner.Count(r.Results)
	r.Summary = Summary{
		Total: len(r.Results), Pass: n[runner.Pass], Fail: n[runner.Fail], Inconclusive: n[runner.Inconclusive],
		Error: n[runner.Error], Skipped: n[runner.Skipped], NotImplemented: n[runner.NotImplemented], Planned: n[runner.Planned],
	}
}

// Line returns the summary as one line, for example "71 results: 69 pass, 2 skipped".
func (s Summary) Line() string {
	var parts []string
	for _, p := range []struct {
		n    int
		name string
	}{
		{s.Pass, "pass"}, {s.Fail, "fail"}, {s.Inconclusive, "inconclusive"}, {s.Error, "error"},
		{s.Skipped, "skipped"}, {s.NotImplemented, "not-implemented"}, {s.Planned, "planned"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.name))
		}
	}
	return fmt.Sprintf("%d results: %s", s.Total, strings.Join(parts, ", "))
}

// check returns the catalogue entry of a result.
func (r *Report) check(id string) catalog.Check {
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	c, _ := catalog.Get(id)
	return c
}

// ValidFormat reports whether f is one of Formats.
func ValidFormat(f string) bool { return slices.Contains(Formats, f) }

// Write writes the report in the given format.
func Write(w io.Writer, format string, r *Report) error {
	switch format {
	case "table":
		return writeTable(w, r)
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	case "junit":
		return writeJUnit(w, r)
	case "sarif":
		return writeSARIF(w, r)
	}
	return fmt.Errorf("unknown format %q (known: %s)", format, strings.Join(Formats, ", "))
}
