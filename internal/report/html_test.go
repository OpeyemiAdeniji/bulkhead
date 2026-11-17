package report_test

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/report"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
)

// withFlows returns the sample report with network results between two tenants, DNS and the internet.
func withFlows() *report.Report {
	r := sample()
	for _, id := range []string{"NET-03", "NET-06"} {
		c, _ := catalog.Get(id)
		r.Checks = append(r.Checks, c)
	}
	r.Results = append(r.Results,
		runner.Result{Check: "NET-01", Status: runner.Pass, Subject: "tenant-alpha, n1 -> n1", From: "tenant-alpha", To: "tenant-alpha"},
		runner.Result{Check: "NET-01", Status: runner.Fail, Subject: "tenant-bravo, n1 -> n1", From: "tenant-bravo", To: "tenant-bravo", Message: "no answer"},
		runner.Result{Check: "NET-04", Status: runner.Pass, Subject: "tenant-alpha -> tenant-bravo, n1 -> n1", From: "tenant-alpha", To: "tenant-bravo", Layer: "network-policy"},
		runner.Result{Check: "NET-04", Status: runner.Fail, Subject: "tenant-bravo -> tenant-alpha, n1 -> n1", From: "tenant-bravo", To: "tenant-alpha", Message: "the server answered"},
		runner.Result{Check: "NET-03", Status: runner.Pass, Subject: "tenant-alpha", From: "tenant-alpha", To: "dns"},
		runner.Result{Check: "NET-06", Status: runner.Inconclusive, Subject: "tenant-alpha -> http://1.1.1.1", From: "tenant-alpha", To: "internet"},
	)
	r.Summarise()
	return r
}

func html(t *testing.T, r *report.Report) string {
	t.Helper()
	var buf bytes.Buffer
	if err := report.Write(&buf, "html", r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestHTMLNetworkMatrix(t *testing.T) {
	out := html(t, withFlows())
	cell := regexp.MustCompile(`<div class="cell (\w+)" title="[^"]*">([^<]+)<small>must be (\w+)</small>`)
	var got []string
	for _, m := range cell.FindAllStringSubmatch(out, -1) {
		got = append(got, m[1]+":"+m[2]+":"+m[3])
	}
	// Rows alpha and bravo; columns alpha, bravo, dns, internet.
	want := []string{
		"pass:allowed:allowed", "pass:blocked:blocked", "pass:allowed:allowed", "warn:inconclusive:blocked",
		"fail:reached, must be blocked:blocked", "fail:no answer, must work:allowed", "none:not tested:allowed", "none:not tested:blocked",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("matrix cells\n got %v\nwant %v", got, want)
	}
}

func TestNetworkResultsMatchTheSchema(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Write(&buf, "json", withFlows()); err != nil {
		t.Fatal(err)
	}
	if err := validate(t, schema(t), buf.Bytes()); err != nil {
		t.Fatalf("a report with from and to does not match the schema: %v", err)
	}
}

func TestHTMLEscapesWhatComesFromTheCluster(t *testing.T) {
	r := withFlows()
	r.Run.Context = `<script>alert("context")</script>`
	r.Results[1].Message = `<img src=x onerror=alert(1)>`
	out := html(t, r)
	for _, raw := range []string{`<script>alert`, `<img src=x`} {
		if strings.Contains(out, raw) {
			t.Errorf("%q is not escaped", raw)
		}
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("the context should appear escaped")
	}
}

func TestHTMLIsSelfContained(t *testing.T) {
	out := html(t, withFlows())
	for _, external := range []string{"<script", "<link", " src=", "@import", "url("} {
		if strings.Contains(out, external) {
			t.Errorf("the report loads something: %q", external)
		}
	}
	for _, want := range []string{"Tenant isolation report", "3 results failed, and 2 results were inconclusive. Exit code 1", "Network matrix", "How to fix",
		`id="acc-02"`, "Does not prove", "It does not prove that the cluster is secure"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report misses %q", want)
		}
	}
}

func TestHTMLWithoutNetworkResultsHasNoMatrix(t *testing.T) {
	if out := html(t, sample()); strings.Contains(out, "Network matrix") {
		t.Error("a run without network results must not draw a matrix")
	}
}

func TestHTMLVerdicts(t *testing.T) {
	r := sample()
	r.ExitCode = 0
	if !strings.Contains(html(t, r), "Every selected check passed") {
		t.Error("exit code 0")
	}
	r.Run.Mode = "dry-run"
	if !strings.Contains(html(t, r), "Dry run: nothing was sent") {
		t.Error("dry run")
	}
	r.Run.Mode, r.ExitCode = "live", 3
	if !strings.Contains(html(t, r), "could not run completely") {
		t.Error("exit code 3")
	}
}

func TestHTMLNamesThePolicyReports(t *testing.T) {
	page := html(t, sample())
	for _, want := range []string{"<dt>PolicyReports</dt>", "<code>tenant-alpha/bulkhead-check</code>",
		"results that name no tenant, so are in no report: 1", "errors: write PolicyReport tenant-bravo/bulkhead-check: forbidden"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page misses %q", want)
		}
	}
}
