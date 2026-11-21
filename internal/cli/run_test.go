package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/OpeyemiAdeniji/bulkhead/internal/exitcode"
	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/policyreport"
	"github.com/OpeyemiAdeniji/bulkhead/internal/report"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
)

// whoami makes a fake client answer SelfSubjectReviews with the given identity.
func whoami(c *fake.Clientset, id kube.Identity) {
	c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		r := &authenticationv1.SelfSubjectReview{}
		r.Status.UserInfo.Username = id.User
		r.Status.UserInfo.Groups = slices.Concat(id.Groups, []string{"system:authenticated"})
		return true, r, nil
	})
}

// fakeCluster installs a fake cluster with two Bulkhead tenants. lie makes impersonated
// identities answer as someone else.
func fakeCluster(t *testing.T, lie bool, objects ...runtime.Object) *fake.Clientset {
	t.Helper()
	objects = append(objects,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant-alpha"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant-bravo"}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant-alpha", Name: "tenant-admins"}, Subjects: []rbacv1.Subject{{Kind: "Group", Name: "alpha-admins"}}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant-bravo", Name: "tenant-admins"}, Subjects: []rbacv1.Subject{{Kind: "Group", Name: "bravo-admins"}}},
	)
	admin := fake.NewClientset(objects...)
	whoami(admin, kube.Identity{User: "kubernetes-admin", Groups: []string{"kubeadm:cluster-admins"}})
	oldConnect, oldImp := connect, impersonate
	t.Cleanup(func() { connect, impersonate = oldConnect, oldImp })
	connect = func(kube.Options) (*kube.Cluster, error) {
		return &kube.Cluster{Context: "fake", Server: "https://fake", Client: admin}, nil
	}
	impersonate = func(*kube.Cluster) func(kube.Identity) (*kube.Cluster, error) {
		return func(id kube.Identity) (*kube.Cluster, error) {
			c := fake.NewClientset()
			if lie {
				id = kube.Identity{User: "someone-else"}
			}
			whoami(c, id)
			return &kube.Cluster{Client: c, As: &id}, nil
		}
	}
	return admin
}

func TestCheckRunsAgainstAFakeCluster(t *testing.T) {
	fakeCluster(t, false)
	// Read-only: the fake cluster's pods never start, so the active checks are left to their own tests.
	code, out, errOut := run(t, "check", "--tenant", "tenant-alpha", "--tenant", "tenant-bravo", "--read-only")
	// The fake identities may do anything, so the checks between tenants fail: exit code 1.
	if code != exitcode.ChecksFailed {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"context fake", "(read-only)", "tenant-alpha: admin bulkhead-check (groups alpha-admins), no viewer", "confirmed with the API server",
		"ACC-02  fail", "active check, not run in read-only mode"} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
}

func TestCheckStopsWhenAnIdentityDoesNotMatch(t *testing.T) {
	fakeCluster(t, true)
	code, _, errOut := run(t, "check", "--tenant", "tenant-alpha")
	if code != exitcode.Error || !strings.Contains(errOut, "confirm the identity of tenant tenant-alpha") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}

func TestDryRunSendsNothingAndListsTheRequests(t *testing.T) {
	fakeCluster(t, true) // would fail the identity check if anything were sent
	code, out, errOut := run(t, "check", "--tenant", "tenant-alpha", "--tenant", "tenant-bravo", "--dry-run", "-o", "json")
	if code != exitcode.OK {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	var rep report.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Run.Mode != "dry-run" || len(rep.Requests) < 3 || rep.Requests[0].Kind != "SelfSubjectReview" || rep.Requests[1].Kind != "SelfSubjectReview" || rep.Cleanup != nil {
		t.Fatalf("unexpected report: mode %s, %d requests, cleanup %v", rep.Run.Mode, len(rep.Requests), rep.Cleanup)
	}
	for _, r := range rep.Results {
		if r.Status != "planned" && r.Status != "not-implemented" {
			t.Errorf("%s: %s in a dry run", r.Check, r.Status)
		}
	}
}

func TestCleanupRemovesObjectsOfAnInterruptedRun(t *testing.T) {
	left := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "bhc-1-server", Namespace: "tenant-alpha",
		Labels: map[string]string{sandbox.LabelManagedBy: sandbox.ManagedBy, sandbox.LabelRunID: "r1"}}}
	admin := fakeCluster(t, false, left)
	code, out, errOut := run(t, "cleanup", "--tenant", "tenant-alpha", "--run-id", "r1")
	if code != exitcode.OK || !strings.Contains(out, "Removed 1 test objects") {
		t.Fatalf("code %d, out %q, err %q", code, out, errOut)
	}
	if l, _ := admin.CoreV1().Pods("tenant-alpha").List(context.Background(), metav1.ListOptions{}); len(l.Items) != 0 {
		t.Error("the pod is still there")
	}
	for _, args := range [][]string{
		{"cleanup", "--tenant", "tenant-alpha"},
		{"cleanup", "--tenant", "tenant-alpha", "--run-id", "r1", "--all-runs"},
		{"cleanup", "--tenant", "kube-system", "--all-runs"},
	} {
		if code, _, _ := run(t, args...); code != exitcode.Usage {
			t.Errorf("%v: code %d, want a usage error", args, code)
		}
	}
}

func TestInterruptedCheckCleansUp(t *testing.T) {
	left := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "bhc-x", Namespace: "tenant-alpha"}}
	fakeCluster(t, false, left)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // as if Ctrl+C came at once
	var out, errOut bytes.Buffer
	code := RunContext(ctx, []string{"check", "--tenant", "tenant-alpha"}, &out, &errOut)
	if code != exitcode.Error {
		t.Fatalf("code %d, stderr %q", code, errOut.String())
	}
}

// servePolicyReports makes the fake cluster serve the PolicyReport API, allow every access
// review, and store reports in a fake dynamic client, which it returns.
func servePolicyReports(t *testing.T, admin *fake.Clientset) *dynamicfake.FakeDynamicClient {
	t.Helper()
	admin.Discovery().(*fakediscovery.FakeDiscovery).Resources = []*metav1.APIResourceList{{
		GroupVersion: policyreport.GroupVersion,
		APIResources: []metav1.APIResource{{Name: "policyreports", Namespaced: true, Kind: policyreport.Kind}},
	}}
	admin.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		r := a.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		r.Status.Allowed = true
		return true, r, nil
	})
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{policyreport.Resource: "PolicyReportList"})
	old := newDynamic
	t.Cleanup(func() { newDynamic = old })
	newDynamic = func(*kube.Cluster) (dynamic.Interface, error) { return dyn, nil }
	return dyn
}

func TestPolicyReportsAreWrittenForEveryTenant(t *testing.T) {
	dyn := servePolicyReports(t, fakeCluster(t, false))
	code, out, errOut := run(t, "check", "--tenant", "tenant-alpha", "--tenant", "tenant-bravo", "--id", "ACC-02", "--policy-reports", "-o", "json")
	// The fake identities may do anything, so ACC-02 fails: exit code 1, and the reports say so.
	if code != exitcode.ChecksFailed {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	var rep report.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.PolicyReports == nil || strings.Join(rep.PolicyReports.Written, ",") != "tenant-alpha/bulkhead-check,tenant-bravo/bulkhead-check" || rep.PolicyReports.LeftOut != 0 {
		t.Fatalf("policyReports: %+v", rep.PolicyReports)
	}
	total := 0
	for _, ns := range []string{"tenant-alpha", "tenant-bravo"} {
		got, err := dyn.Resource(policyreport.Resource).Namespace(ns).Get(context.Background(), policyreport.Name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		results, _, _ := unstructured.NestedSlice(got.Object, "results")
		total += len(results)
		if fails, _, _ := unstructured.NestedInt64(got.Object, "summary", "fail"); fails == 0 {
			t.Errorf("%s: the report shows no failure", ns)
		}
	}
	if total != rep.Summary.Total {
		t.Errorf("the reports hold %d results, the run has %d", total, rep.Summary.Total)
	}
}

func TestPolicyReportsNeedTheAPIBeforeAnyCheck(t *testing.T) {
	admin := fakeCluster(t, false)
	servePolicyReports(t, admin)
	admin.Discovery().(*fakediscovery.FakeDiscovery).Resources = nil
	code, out, errOut := run(t, "check", "--tenant", "tenant-alpha", "--policy-reports")
	if code != exitcode.Error || !strings.Contains(errOut, "does not serve the PolicyReport API") || out != "" {
		t.Fatalf("code %d, out %q, stderr %q", code, out, errOut)
	}
}

func TestRunUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"check", "--tenant", "kube-system"},
		{"check", "--tenant", "a", "--read-only", "--dry-run"},
		{"check", "--tenant", "a", "--id", "NOPE-01"},
		{"check", "--tenant", "a", "--timeout", "0s"},
		{"check", "--tenant", "a", "--policy-reports", "--read-only"},
		{"check", "--tenant", "a", "--policy-reports", "--dry-run"},
	} {
		if code, _, errOut := run(t, args...); code != exitcode.Usage {
			t.Errorf("%v: code %d (%s), want a usage error", args, code, errOut)
		}
	}
}

// TestFlowsFileErrorsStopBeforeTheCluster: a wrong flows file is a usage error, found before
// anything is sent, with a message that says what is wrong.
func TestFlowsFileErrorsStopBeforeTheCluster(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	head := "apiVersion: bulkhead.io/v1alpha1\nkind: Flows\nflows:\n"
	good := write("good.yaml", head+"  - {name: x, from: tenant-alpha, to: {service: db, namespace: shared, port: 5432}, expect: allowed}\n")
	unpaired := write("unpaired.yaml", head+"  - {name: x, from: tenant-alpha, to: {service: db, namespace: shared, port: 5432}, expect: blocked}\n")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--tenant", "tenant-alpha", "--flows", filepath.Join(dir, "missing.yaml")}, "no such file"},
		{[]string{"--tenant", "tenant-alpha", "--flows", unpaired}, "needs an allowed flow to the same destination"},
		{[]string{"--tenant", "tenant-bravo", "--flows", good}, "flows must start in a tenant under test"},
		{[]string{"--tenant", "tenant-alpha", "--id", "FLW-01"}, "need a flows file"},
		{[]string{"--tenant", "tenant-alpha", "--category", "flows"}, "need a flows file"},
	} {
		code, out, errOut := run(t, append([]string{"check"}, c.args...)...)
		if code != exitcode.Usage || !strings.Contains(errOut, c.want) || out != "" {
			t.Errorf("%v: code %d, stderr %q, want a usage error with %q", c.args, code, errOut, c.want)
		}
	}
}

// TestTheFlowChecksRunOnlyWithAFile: without --flows a run does not list FLW results, and
// --list shows the checks anyway.
func TestTheFlowChecksRunOnlyWithAFile(t *testing.T) {
	fakeCluster(t, false)
	_, out, _ := run(t, "check", "--tenant", "tenant-alpha", "--tenant", "tenant-bravo", "--read-only", "-o", "json")
	if strings.Contains(out, "FLW-0") {
		t.Error("a run without --flows reports the declared flow checks")
	}
	_, list, _ := run(t, "check", "--list", "--category", "flows")
	if !strings.Contains(list, "FLW-01") || !strings.Contains(list, "FLW-02") {
		t.Errorf("--list must show the declared flow checks:\n%s", list)
	}
}
