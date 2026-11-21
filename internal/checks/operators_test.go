package checks

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/operatorrefs"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
)

const refsFile = `
apiVersion: bulkhead.io/v1alpha1
kind: OperatorReferences
references:
  - name: secret-ref
    apiVersion: probe.bulkhead.io/v1
    kind: Reference
    resource: references
    namespacePath: spec.secretRef.namespace
    body: {spec: {secretRef: {name: credentials}}}
`

// refCluster answers dry-run creates of the custom resource like a cluster with, or without, an
// admission rule against cross-namespace references.
type refCluster struct {
	defended   bool // a ValidatingAdmissionPolicy refuses references to another namespace
	noRights   bool // the tenant admin may not create the resource at all
	badBody    bool // the resource's schema refuses the body
	dryRuns    []bool
	requesters []string
}

func (rc *refCluster) env(t *testing.T, mode sandbox.Mode) *runner.Env {
	t.Helper()
	refs, err := operatorrefs.Parse([]byte(refsFile))
	if err != nil {
		t.Fatal(err)
	}
	gvr := schema.GroupVersionResource{Group: "probe.bulkhead.io", Version: "v1", Resource: "references"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "ReferenceList"})
	dyn.PrependReactor("create", "references", func(a k8stesting.Action) (bool, runtime.Object, error) {
		ca := a.(k8stesting.CreateActionImpl)
		rc.dryRuns = append(rc.dryRuns, len(ca.CreateOptions.DryRun) == 1 && ca.CreateOptions.DryRun[0] == metav1.DryRunAll)
		u := ca.GetObject().(*unstructured.Unstructured)
		target, _, _ := unstructured.NestedString(u.Object, "spec", "secretRef", "namespace")
		gr := gvr.GroupResource()
		switch {
		case rc.noRights:
			return true, nil, apierrors.NewForbidden(gr, u.GetName(), fmt.Errorf(`User "bulkhead-check" cannot create resource "references" in API group "probe.bulkhead.io"`))
		case rc.badBody:
			return true, nil, apierrors.NewInvalid(schema.GroupKind{Group: "probe.bulkhead.io", Kind: "Reference"}, u.GetName(), nil)
		case rc.defended && target != u.GetNamespace():
			return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: 422, Reason: metav1.StatusReasonInvalid,
				Message: `references.probe.bulkhead.io "` + u.GetName() + `" is forbidden: ValidatingAdmissionPolicy 'no-cross-namespace-refs' with binding 'no-cross-namespace-refs' denied request: the reference must stay in the namespace`}}
		}
		return true, u, nil
	})
	old := DynamicFor
	t.Cleanup(func() { DynamicFor = old })
	DynamicFor = func(c *kube.Cluster) (dynamic.Interface, error) {
		rc.requesters = append(rc.requesters, c.As.String())
		return dyn, nil
	}
	c := fake.NewClientset()
	return &runner.Env{
		Cluster: &kube.Cluster{Client: c},
		Tenants: tenants,
		Sandbox: sandbox.New(mode),
		Impersonate: func(id kube.Identity) (*kube.Cluster, error) {
			return &kube.Cluster{Client: c, As: &id}, nil
		},
		OperatorRefs: refs,
	}
}

func runXNS(t *testing.T, env *runner.Env) map[string][]runner.Result {
	t.Helper()
	t.Cleanup(func() { ForgetRun(env.Sandbox) })
	var cs []catalog.Check
	for _, id := range []string{"XNS-01", "XNS-02"} {
		c, _ := catalog.Get(id)
		cs = append(cs, c)
	}
	out := map[string][]runner.Result{}
	for _, r := range runner.Run(context.Background(), env, runner.Options{Checks: cs, CheckTimeout: 5 * time.Second}) {
		out[r.Check] = append(out[r.Check], r)
	}
	return out
}

func TestADefendedClusterRefusesCrossTenantReferences(t *testing.T) {
	rc := &refCluster{defended: true}
	got := runXNS(t, rc.env(t, sandbox.Live))
	if len(got["XNS-01"]) != 2 || len(got["XNS-02"]) != 2 {
		t.Fatalf("results: %+v", got)
	}
	for _, r := range got["XNS-01"] {
		if r.Status != runner.Pass {
			t.Errorf("XNS-01: %+v", r)
		}
	}
	for _, r := range got["XNS-02"] {
		if r.Status != runner.Pass || !strings.HasPrefix(r.Subject, "secret-ref, ") || r.Layer != LayerAdmission || !strings.Contains(r.Message, "no-cross-namespace-refs") {
			t.Errorf("XNS-02: %+v", r)
		}
	}
	// Every request was a dry run, sent as a tenant admin; the own reference was sent once per tenant.
	for i, d := range rc.dryRuns {
		if !d {
			t.Errorf("request %d was not a dry run", i)
		}
	}
	if len(rc.dryRuns) != 4 {
		t.Errorf("sent %d requests, want 4 (2 own, 2 cross)", len(rc.dryRuns))
	}
	for _, who := range rc.requesters {
		if !strings.Contains(who, "-admins") {
			t.Errorf("a request was not sent as a tenant admin: %s", who)
		}
	}
}

func TestAnUndefendedClusterFailsXNS02(t *testing.T) {
	rc := &refCluster{}
	got := runXNS(t, rc.env(t, sandbox.Live))
	for _, r := range got["XNS-02"] {
		if r.Status != runner.Fail || !strings.Contains(r.Message, "accepted") {
			t.Errorf("XNS-02 must fail when the reference is accepted: %+v", r)
		}
	}
}

// TestARefusalOfEverythingProvesNothing: when the tenant may not create the resource, or its body
// is invalid, the cross reference is refused too, but that is not the defence XNS-02 is about.
func TestARefusalOfEverythingProvesNothing(t *testing.T) {
	for name, rc := range map[string]*refCluster{"no rights": {noRights: true}, "bad body": {badBody: true}} {
		got := runXNS(t, rc.env(t, sandbox.Live))
		for _, r := range got["XNS-01"] {
			if r.Status != runner.Fail {
				t.Errorf("%s: XNS-01 must fail: %+v", name, r)
			}
		}
		for _, r := range got["XNS-02"] {
			if r.Status != runner.Inconclusive || !strings.Contains(r.Message, "was not accepted either") {
				t.Errorf("%s: XNS-02 must be inconclusive: %+v", name, r)
			}
		}
	}
}

func TestReferenceChecksRunInReadOnlyMode(t *testing.T) {
	rc := &refCluster{defended: true}
	got := runXNS(t, rc.env(t, sandbox.ReadOnly))
	if r := got["XNS-02"][0]; r.Status != runner.Pass {
		t.Errorf("a dry run stores nothing, so it runs in read-only mode: %+v", r)
	}
}

func TestReferenceChecksInADryRun(t *testing.T) {
	rc := &refCluster{defended: true}
	got := runXNS(t, rc.env(t, sandbox.DryRun))
	for _, r := range append(got["XNS-01"], got["XNS-02"]...) {
		if r.Status != runner.Planned {
			t.Errorf("dry run: %+v", r)
		}
	}
	if len(rc.dryRuns) != 0 {
		t.Error("a dry run of the checker sent a request")
	}
}

func TestReferenceChecksWithoutAFile(t *testing.T) {
	rc := &refCluster{}
	env := rc.env(t, sandbox.Live)
	env.OperatorRefs = nil
	got := runXNS(t, env)
	if r := got["XNS-01"][0]; r.Status != runner.Skipped {
		t.Errorf("without a file: %+v", r)
	}
}
