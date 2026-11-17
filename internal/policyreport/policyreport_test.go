package policyreport

import (
	"context"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/report"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

var tenants = []string{"tenant-a", "tenant-ab"}

func TestTenantOf(t *testing.T) {
	for _, c := range []struct {
		res  runner.Result
		want string
	}{
		{runner.Result{Subject: "tenant-a"}, "tenant-a"},
		{runner.Result{Subject: "tenant-ab"}, "tenant-ab"},
		{runner.Result{Subject: "tenant-ab -> tenant-a"}, "tenant-ab"},
		{runner.Result{Subject: "tenant-a, bhc-1234-cpu-hog"}, "tenant-a"},
		{runner.Result{Subject: "tenant-a (3 networkpolicy objects)"}, "tenant-a"},
		{runner.Result{Subject: "tenant-a resourcequota/tenant-quota"}, "tenant-a"},
		// A network result belongs to its source, whatever the subject says.
		{runner.Result{Subject: "tenant-a -> tenant-ab, n1 -> n2", From: "tenant-ab", To: "tenant-a"}, "tenant-ab"},
		{runner.Result{Subject: "tenant-abc"}, ""},
		{runner.Result{Subject: "kube-system"}, ""},
		{runner.Result{}, ""},
	} {
		if got := TenantOf(c.res, tenants); got != c.want {
			t.Errorf("TenantOf(%+v) = %q, want %q", c.res, got, c.want)
		}
	}
}

func testReport() *report.Report {
	acc02, _ := catalog.Get("ACC-02")
	net06, _ := catalog.Get("NET-06")
	xns, _ := catalog.Get("XNS-01")
	r := report.New([]catalog.Check{acc02, net06, xns})
	r.Run.ID = "run1"
	r.Run.FinishedAt = time.Unix(1_800_000_000, 0).UTC()
	r.Tenants = []tenant.Tenant{{Name: "tenant-a", Namespace: "tenant-a"}, {Name: "tenant-ab", Namespace: "tenant-ab"}}
	r.Results = []runner.Result{
		{Check: "ACC-02", Status: runner.Pass, Subject: "tenant-a -> tenant-ab", Layer: "rbac", Message: "refused by rbac"},
		{Check: "ACC-02", Status: runner.Inconclusive, Subject: "tenant-ab -> tenant-a"},
		{Check: "NET-06", Status: runner.Fail, Subject: "tenant-ab -> http://1.1.1.1", From: "tenant-ab", To: "internet", Message: "reached"},
		{Check: "NET-06", Status: runner.Error, Subject: "tenant-a -> http://1.1.1.1", From: "tenant-a", To: "internet"},
		{Check: "XNS-01", Status: runner.NotImplemented, Subject: "tenant-a"},
		{Check: "XNS-01", Status: runner.Skipped, Subject: "somewhere else"},
	}
	return r
}

func TestBuild(t *testing.T) {
	objs, leftOut := Build(testReport())
	if leftOut != 1 {
		t.Errorf("left out %d results, want 1", leftOut)
	}
	if len(objs) != 2 || objs[0].GetNamespace() != "tenant-a" || objs[1].GetNamespace() != "tenant-ab" {
		t.Fatalf("want a report in tenant-a and tenant-ab, got %d", len(objs))
	}
	a := objs[0]
	if a.GetAPIVersion() != "wgpolicyk8s.io/v1alpha2" || a.GetKind() != "PolicyReport" || a.GetName() != "bulkhead-check" {
		t.Errorf("wrong identity: %s %s %s", a.GetAPIVersion(), a.GetKind(), a.GetName())
	}
	if l := a.GetLabels(); l[sandbox.LabelManagedBy] != "bulkhead-check" || l[sandbox.LabelRunID] != "run1" {
		t.Errorf("wrong labels: %v", l)
	}
	if scope, _, _ := unstructured.NestedStringMap(a.Object, "scope"); scope["kind"] != "Namespace" || scope["name"] != "tenant-a" {
		t.Errorf("wrong scope: %v", scope)
	}
	summary, _, _ := unstructured.NestedMap(a.Object, "summary")
	if summary["pass"] != int64(1) || summary["error"] != int64(1) || summary["skip"] != int64(1) || summary["fail"] != int64(0) {
		t.Errorf("wrong summary of tenant-a: %v", summary)
	}
	results, _, _ := unstructured.NestedSlice(a.Object, "results")
	first := results[0].(map[string]any)
	for k, want := range map[string]any{
		"source": "bulkhead", "policy": "ACC-02", "rule": "tenant-a -> tenant-ab", "category": "access",
		"severity": "high", "result": "pass", "scored": true, "message": "refused by rbac",
	} {
		if first[k] != want {
			t.Errorf("result field %s = %v, want %v", k, first[k], want)
		}
	}
	if ts := first["timestamp"].(map[string]any); ts["seconds"] != int64(1_800_000_000) {
		t.Errorf("wrong timestamp: %v", ts)
	}
	props := first["properties"].(map[string]any)
	if props["layer"] != "rbac" || props["runId"] != "run1" || props["status"] != "pass" || !strings.HasSuffix(props["docs"].(string), "checks.md#acc-02") {
		t.Errorf("wrong properties: %v", props)
	}
	if _, ok := props["from"]; ok {
		t.Errorf("a result without a source must have no from property: %v", props)
	}

	b, _, _ := unstructured.NestedSlice(objs[1].Object, "results")
	if len(b) != 2 {
		t.Fatalf("tenant-ab: want 2 results, got %d", len(b))
	}
	inconclusive, network := b[0].(map[string]any), b[1].(map[string]any)
	if inconclusive["result"] != "warn" || inconclusive["scored"] != false || inconclusive["message"] == "" {
		t.Errorf("an inconclusive result must be an unscored warn with a message: %v", inconclusive)
	}
	if p := network["properties"].(map[string]any); network["result"] != "fail" || p["from"] != "tenant-ab" || p["to"] != "internet" {
		t.Errorf("wrong network result: %v", network)
	}
	// Every object must survive a deep copy, which the client does before it sends one.
	_ = a.DeepCopy()
}

func TestResultAndSeverity(t *testing.T) {
	for s, want := range map[runner.Status]string{
		runner.Pass: "pass", runner.Fail: "fail", runner.Inconclusive: "warn", runner.Error: "error",
		runner.Skipped: "skip", runner.NotImplemented: "skip",
	} {
		if got := Result(s); got != want {
			t.Errorf("Result(%s) = %s, want %s", s, got, want)
		}
	}
	for l, want := range map[catalog.Level]string{catalog.Basic: "high", catalog.Hardened: "medium", catalog.Research: "low"} {
		if got := Severity(l); got != want {
			t.Errorf("Severity(%s) = %s, want %s", l, got, want)
		}
	}
}

func TestWriteCreatesAndThenReplaces(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{Resource: "PolicyReportList"})
	ctx := context.Background()
	objs, _ := Build(testReport())
	written, err := Write(ctx, client, objs)
	if err != nil || strings.Join(written, ",") != "tenant-a/bulkhead-check,tenant-ab/bulkhead-check" {
		t.Fatalf("first write: %v, %v", written, err)
	}
	// A second run replaces the report of the first.
	r := testReport()
	r.Run.ID = "run2"
	r.Results = r.Results[:1]
	objs, _ = Build(r)
	if written, err = Write(ctx, client, objs); err != nil || len(written) != 1 {
		t.Fatalf("second write: %v, %v", written, err)
	}
	got, err := client.Resource(Resource).Namespace("tenant-a").Get(ctx, Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	results, _, _ := unstructured.NestedSlice(got.Object, "results")
	if got.GetLabels()[sandbox.LabelRunID] != "run2" || len(results) != 1 {
		t.Errorf("the report was not replaced: run %s, %d results", got.GetLabels()[sandbox.LabelRunID], len(results))
	}
}

func TestWriteReportsEveryFailure(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{Resource: "PolicyReportList"})
	client.PrependReactor("create", "policyreports", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() == "tenant-a" {
			return true, nil, context.DeadlineExceeded
		}
		return false, nil, nil
	})
	objs, _ := Build(testReport())
	written, err := Write(context.Background(), client, objs)
	if len(written) != 1 || written[0] != "tenant-ab/bulkhead-check" || err == nil || !strings.Contains(err.Error(), "tenant-a/bulkhead-check") {
		t.Errorf("one failed write must not stop the others: %v, %v", written, err)
	}
}

// preflightClient serves the PolicyReport API when serve is true, and allows every request
// except the denied verb.
func preflightClient(serve bool, denied string) *fake.Clientset {
	c := fake.NewClientset()
	if serve {
		c.Discovery().(*fakediscovery.FakeDiscovery).Resources = []*metav1.APIResourceList{{
			GroupVersion: GroupVersion,
			APIResources: []metav1.APIResource{{Name: "policyreports", Namespaced: true, Kind: Kind}},
		}}
	}
	c.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		r := a.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		r.Status.Allowed = r.Spec.ResourceAttributes.Verb != denied
		return true, r, nil
	})
	return c
}

func TestPreflight(t *testing.T) {
	ctx := context.Background()
	if err := Preflight(ctx, preflightClient(true, ""), tenants); err != nil {
		t.Errorf("allowed: %v", err)
	}
	if err := Preflight(ctx, preflightClient(false, ""), tenants); err == nil || !strings.Contains(err.Error(), "does not serve the PolicyReport API") {
		t.Errorf("no API: %v", err)
	}
	err := Preflight(ctx, preflightClient(true, "update"), tenants)
	if err == nil || !strings.Contains(err.Error(), "update in tenant-a, update in tenant-ab") {
		t.Errorf("no update permission: %v", err)
	}
}
