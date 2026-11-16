package report_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/checks"
	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/report"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

const schemaPath = "../../schemas/check-report.schema.json"

// sample returns a report with every status, a tenant with and one without a viewer, a dry-run
// request and a clean-up.
func sample() *report.Report {
	var cs []catalog.Check
	for _, id := range []string{"ACC-01", "ACC-02", "NET-01", "NET-04", "RES-03", "QTA-04", "QTA-08", "QTA-09"} {
		c, _ := catalog.Get(id)
		cs = append(cs, c)
	}
	r := report.New(cs)
	start := time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC)
	r.Run = report.Run{ID: "20261116t130000-1a2b3c4d", Mode: "live", StartedAt: start, FinishedAt: start.Add(80 * time.Second),
		Context: "kind-bulkhead", Server: "https://127.0.0.1:6443", Kubernetes: "v1.37.0", User: "kubernetes-admin", IdentitiesConfirmed: true}
	r.Tenants = []tenant.Tenant{
		{Name: "tenant-alpha", Namespace: "tenant-alpha", Admin: kube.Identity{User: "bulkhead-check", Groups: []string{"alpha-admins"}}, Viewer: &kube.Identity{User: "bulkhead-check", Groups: []string{"alpha-viewers"}}},
		{Name: "tenant-bravo", Namespace: "tenant-bravo", Admin: kube.Identity{User: "bulkhead-check", Groups: []string{"bravo-admins"}}},
	}
	r.Results = []runner.Result{
		{Check: "ACC-01", Status: runner.Pass, Subject: "tenant-alpha", Duration: "120ms"},
		{Check: "ACC-02", Status: runner.Fail, Subject: "tenant-alpha -> tenant-bravo", Message: "the request reached the secret store", Duration: "80ms"},
		{Check: "NET-01", Status: runner.Pass, Subject: "tenant-alpha, n1 -> n1", Duration: "30s"},
		{Check: "NET-04", Status: runner.Inconclusive, Subject: "tenant-alpha -> tenant-bravo, n1 -> n1", Message: "its pair did not pass", Layer: "network-policy"},
		{Check: "RES-03", Status: runner.Error, Subject: "tenant-alpha, bhc-1a2b3c4d-memory-hog", Message: "timed out"},
		{Check: "QTA-04", Status: runner.Skipped, Subject: "tenant-bravo", Message: "active check, not run in read-only mode"},
		{Check: "QTA-08", Status: runner.NotImplemented, Message: "not implemented"},
		{Check: "QTA-09", Status: runner.Planned, Subject: "tenant-alpha", Layer: "quota"},
	}
	r.Requests = []sandbox.Request{{Verb: "create", Kind: "pod", Namespace: "tenant-alpha", Name: "bhc-x", As: "bulkhead-check (groups alpha-admins)", Stores: true, Check: "QTA-08"}}
	r.Cleanup = &report.Cleanup{Deleted: 20}
	r.Summarize()
	r.ExitCode = 1
	return r
}

func schema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	s, err := c.Compile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validate(t *testing.T, s *jsonschema.Schema, data []byte) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return s.Validate(inst)
}

func TestTheJSONReportMatchesThePublishedSchema(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Write(&buf, "json", sample()); err != nil {
		t.Fatal(err)
	}
	if err := validate(t, schema(t), buf.Bytes()); err != nil {
		t.Fatalf("the report does not match the schema: %v\n%s", err, buf.String())
	}
}

// TestAReportFileMatchesTheSchema validates a report written by a real run, named in
// BULKHEAD_REPORT_FILE. tests/checker/e2e.sh runs it on a report from the kind cluster.
func TestAReportFileMatchesTheSchema(t *testing.T) {
	path := os.Getenv("BULKHEAD_REPORT_FILE")
	if path == "" {
		t.Skip("BULKHEAD_REPORT_FILE is not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(t, schema(t), data); err != nil {
		t.Fatalf("%s does not match the schema: %v", path, err)
	}
}

func TestTheSchemaRefusesWrongReports(t *testing.T) {
	s := schema(t)
	var good map[string]any
	var buf bytes.Buffer
	_ = report.Write(&buf, "json", sample())
	_ = json.Unmarshal(buf.Bytes(), &good)
	for name, change := range map[string]func(m map[string]any){
		"unknown status":    func(m map[string]any) { m["results"].([]any)[0].(map[string]any)["status"] = "maybe" },
		"unknown top field": func(m map[string]any) { m["extra"] = true },
		"missing exit code": func(m map[string]any) { delete(m, "exitCode") },
		"wrong api version": func(m map[string]any) { m["apiVersion"] = "bulkhead.io/v2" },
		"bad check ID":      func(m map[string]any) { m["results"].([]any)[0].(map[string]any)["check"] = "net-4" },
		"bad date":          func(m map[string]any) { m["run"].(map[string]any)["startedAt"] = "yesterday" },
	} {
		data, _ := json.Marshal(good)
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		change(m)
		data, _ = json.Marshal(m)
		if validate(t, s, data) == nil {
			t.Errorf("%s: the schema accepted it", name)
		}
	}
}

func TestTheSchemaKnowsEveryLayerTheChecksReport(t *testing.T) {
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, layer := range []string{checks.LayerRBAC, checks.LayerPodSecurity, checks.LayerAdmission, checks.LayerQuota,
		checks.LayerLimitRange, checks.LayerUnknown, checks.LayerNetworkPolicy, checks.LayerResourceLimits} {
		if !strings.Contains(string(raw), `"`+layer+`"`) {
			t.Errorf("the schema does not allow the layer %q", layer)
		}
	}
}

func TestJUnit(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Write(&buf, "junit", sample()); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
		Errors   int `xml:"errors,attr"`
		Skipped  int `xml:"skipped,attr"`
		Suites   []struct {
			Name  string `xml:"name,attr"`
			Cases []struct {
				Name      string `xml:"name,attr"`
				ClassName string `xml:"classname,attr"`
			} `xml:"testcase"`
		} `xml:"testsuite"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not valid XML: %v\n%s", err, buf.String())
	}
	// 8 results: 1 fail + 1 inconclusive are failures, 1 error, 3 skipped (skipped, not implemented, planned).
	if got.Tests != 8 || got.Failures != 2 || got.Errors != 1 || got.Skipped != 3 {
		t.Errorf("tests %d, failures %d, errors %d, skipped %d", got.Tests, got.Failures, got.Errors, got.Skipped)
	}
	if len(got.Suites) != 4 || got.Suites[0].Name != "access" {
		t.Errorf("suites: %+v", got.Suites)
	}
	if c := got.Suites[0].Cases[1]; c.Name != "ACC-02 tenant-alpha -> tenant-bravo" || c.ClassName != "bulkhead.access.ACC-02" {
		t.Errorf("case: %+v", c)
	}
	if !strings.Contains(buf.String(), "Fix: ") {
		t.Error("a failure should carry the fix")
	}
}

func TestSARIF(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Write(&buf, "sarif", sample()); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name  string `json:"name"`
					Rules []struct {
						ID      string `json:"id"`
						Name    string `json:"name"`
						HelpURI string `json:"helpUri"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID    string `json:"ruleId"`
				RuleIndex int    `json:"ruleIndex"`
				Level     string `json:"level"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
					} `json:"physicalLocation"`
					LogicalLocations []struct {
						Name string `json:"name"`
					} `json:"logicalLocations"`
				} `json:"locations"`
				PartialFingerprints map[string]string `json:"partialFingerprints"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != "2.1.0" || len(got.Runs) != 1 {
		t.Fatalf("version %q, %d runs", got.Version, len(got.Runs))
	}
	run := got.Runs[0]
	if len(run.Tool.Driver.Rules) != 8 {
		t.Errorf("%d rules, want one per check of the run (8)", len(run.Tool.Driver.Rules))
	}
	ident := regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	for _, rule := range run.Tool.Driver.Rules {
		if !ident.MatchString(rule.Name) || !strings.HasSuffix(rule.HelpURI, "#"+strings.ToLower(rule.ID)) {
			t.Errorf("rule %+v", rule)
		}
	}
	// Only fail, inconclusive and error become results.
	if len(run.Results) != 3 {
		t.Fatalf("%d results, want 3", len(run.Results))
	}
	levels := map[string]string{}
	for _, res := range run.Results {
		levels[res.RuleID] = res.Level
		if run.Tool.Driver.Rules[res.RuleIndex].ID != res.RuleID {
			t.Errorf("%s: ruleIndex %d points at %s", res.RuleID, res.RuleIndex, run.Tool.Driver.Rules[res.RuleIndex].ID)
		}
		if uri := res.Locations[0].PhysicalLocation.ArtifactLocation.URI; uri != "kubernetes/kind-bulkhead/tenant-alpha" {
			t.Errorf("%s: location %q", res.RuleID, uri)
		}
		if res.Locations[0].LogicalLocations[0].Name != "tenant-alpha" || len(res.PartialFingerprints) != 1 {
			t.Errorf("%s: logical location or fingerprint missing", res.RuleID)
		}
	}
	if levels["ACC-02"] != "error" || levels["NET-04"] != "warning" || levels["RES-03"] != "warning" {
		t.Errorf("levels %v", levels)
	}
}

func TestTable(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Write(&buf, "table", sample()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"context kind-bulkhead", "tenant-bravo: admin bulkhead-check (groups bravo-admins), no viewer",
		"Dry run: nothing was sent", "ACC-02  fail", "8 results: 2 pass, 1 fail, 1 inconclusive, 1 error, 1 skipped, 1 not-implemented, 1 planned",
		"Clean-up: removed 20 test objects", "How to fix:", "ACC-02 A tenant admin cannot read secrets in another tenant's namespace",
		"Remove role bindings in tenant B"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the table misses %q", want)
		}
	}
}

func TestUnknownFormat(t *testing.T) {
	if err := report.Write(&bytes.Buffer{}, "pdf", sample()); err == nil {
		t.Error("an unknown format must be an error")
	}
	if report.ValidFormat("pdf") || !report.ValidFormat("sarif") {
		t.Error("ValidFormat")
	}
}
