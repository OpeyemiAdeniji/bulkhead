package checks

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

// sim is a simulated cluster that enforces tenant isolation like Bulkhead's charts do.
// Each field switches one protection off, for the break tests.
type sim struct {
	crossTenantSecrets bool // RBAC lets a tenant read another tenant's secrets
	viewerReadsSecrets bool
	deleteGuardrails   bool // RBAC lets a tenant delete its network policies, quota and limit range
	privilegedAllowed  bool // no Pod Security
	nodePortsAllowed   bool // no services.nodeports in the quota
	noLimitDefaults    bool
	crossTenantExec    bool
}

var tenants = []tenant.Tenant{
	{Name: "a", Namespace: "tenant-a", Admin: kube.Identity{User: tenant.TesterUser, Groups: []string{"a-admins"}}, Viewer: &kube.Identity{User: tenant.TesterUser, Groups: []string{"a-viewers"}}},
	{Name: "b", Namespace: "tenant-b", Admin: kube.Identity{User: tenant.TesterUser, Groups: []string{"b-admins"}}},
}

func forbidden(resource, msg string) error {
	return apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "x", fmt.Errorf("%s", msg))
}

func rbacDenied(a k8stesting.Action) error {
	return forbidden(a.GetResource().Resource, fmt.Sprintf(`User "bulkhead-check" cannot %s resource "%s" in API group "" in the namespace "%s"`, a.GetVerb(), a.GetResource().Resource, a.GetNamespace()))
}

// client returns a fake client that answers as the given identity would be answered.
func (s sim) client(id kube.Identity) *fake.Clientset {
	own := ""
	viewer := false
	for _, t := range tenants {
		if t.Admin.Groups[0] == id.Groups[0] {
			own = t.Namespace
		}
		if t.Viewer != nil && t.Viewer.Groups[0] == id.Groups[0] {
			own, viewer = t.Namespace, true
		}
	}
	c := fake.NewClientset()
	c.PrependReactor("*", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		ns, res, verb := a.GetNamespace(), a.GetResource().Resource, a.GetVerb()
		switch {
		case res == "selfsubjectaccessreviews":
			attrs := a.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview).Spec.ResourceAttributes
			r := &authorizationv1.SelfSubjectAccessReview{}
			r.Status.Allowed = attrs.Namespace == own || s.crossTenantExec
			return true, r, nil
		case res == "namespaces" && verb == "patch":
			return true, nil, rbacDenied(a)
		case res == "secrets" && ns != own && !s.crossTenantSecrets:
			return true, nil, rbacDenied(a)
		case res == "secrets" && ns != own:
			return false, nil, nil // the broken cluster lets it through to the store
		case res == "secrets" && viewer && !s.viewerReadsSecrets:
			return true, nil, rbacDenied(a)
		case ns != own:
			return true, nil, rbacDenied(a)
		case verb == "delete" && !s.deleteGuardrails:
			return true, nil, rbacDenied(a)
		case verb == "delete":
			return true, nil, nil // a dry-run delete that is allowed
		case res == "rolebindings" || res == "endpoints" && verb == "create":
			return true, nil, rbacDenied(a)
		case res == "pods" && verb == "create":
			return s.admitPod(a.(k8stesting.CreateAction).GetObject().(*corev1.Pod))
		case res == "services" && verb == "create":
			svc := a.(k8stesting.CreateAction).GetObject().(*corev1.Service)
			if svc.Spec.Type == corev1.ServiceTypeLoadBalancer || (svc.Spec.Type == corev1.ServiceTypeNodePort && !s.nodePortsAllowed) {
				return true, nil, forbidden("services", "exceeded quota: tenant-quota, requested: services.nodeports=1")
			}
			return true, svc, nil
		}
		return false, nil, nil // fall through to the object tracker: reads succeed, gets of absent names are "not found"
	})
	return c
}

func (s sim) admitPod(p *corev1.Pod) (bool, runtime.Object, error) {
	sc := p.Spec.Containers[0].SecurityContext
	if sc != nil && sc.Privileged != nil && *sc.Privileged && !s.privilegedAllowed {
		return true, nil, forbidden("pods", `violates PodSecurity "restricted:latest": privileged`)
	}
	if l, ok := p.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU]; ok && l.Cmp(resource.MustParse("1")) > 0 {
		return true, nil, forbidden("pods", "maximum cpu usage per Container is 1, but limit is "+l.String())
	}
	out := p.DeepCopy()
	if !s.noLimitDefaults && len(out.Spec.Containers[0].Resources.Limits) == 0 {
		out.Spec.Containers[0].Resources.Limits = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("100Mi")}
	}
	return true, out, nil
}

func (s sim) env(mode sandbox.Mode) *runner.Env {
	var objects []runtime.Object
	for _, t := range tenants {
		objects = append(objects,
			&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "default-deny-all", Namespace: t.Namespace}},
			&corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "tenant-quota", Namespace: t.Namespace}},
			&corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: "tenant-limits", Namespace: t.Namespace}, Spec: corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{
				Type: corev1.LimitTypeContainer, Max: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
			}}}},
		)
	}
	admin := fake.NewClientset(objects...)
	return &runner.Env{
		Cluster: &kube.Cluster{Client: admin},
		Tenants: tenants,
		Sandbox: sandbox.New(mode),
		Impersonate: func(id kube.Identity) (*kube.Cluster, error) {
			return &kube.Cluster{Client: s.client(id), As: &id}, nil
		},
	}
}

func readOnlyChecks() []catalog.Check {
	var out []catalog.Check
	for _, c := range catalog.All() {
		if c.Mode == catalog.ReadOnly && runner.Implemented(c.ID) {
			out = append(out, c)
		}
	}
	return out
}

// failing runs every read-only check on the simulated cluster and returns the IDs that did not pass.
func failing(t *testing.T, s sim) []string {
	t.Helper()
	rs := runner.Run(context.Background(), s.env(sandbox.Live), runner.Options{Checks: readOnlyChecks(), CheckTimeout: 5 * time.Second})
	bad := map[string]bool{}
	for _, r := range rs {
		switch r.Status {
		case runner.Pass:
		case runner.Skipped:
		default:
			bad[r.Check] = true
			t.Logf("%s %s %s: %s", r.Check, r.Status, r.Subject, r.Message)
		}
	}
	var out []string
	for id := range bad {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func TestEveryReadOnlyCheckIsImplemented(t *testing.T) {
	for _, c := range catalog.All() {
		if c.Mode == catalog.ReadOnly && c.Level != catalog.Research && !runner.Implemented(c.ID) {
			t.Errorf("%s is read-only but has no implementation", c.ID)
		}
	}
}

func TestAnIsolatedClusterPassesEveryReadOnlyCheck(t *testing.T) {
	if got := failing(t, sim{}); len(got) != 0 {
		t.Fatalf("checks that did not pass: %v", got)
	}
}

// Each broken protection must fail exactly the checks that test it (and, through the pair
// rule, nothing else).
func TestBreakingOneProtectionFailsExactlyItsChecks(t *testing.T) {
	cases := []struct {
		name string
		sim  sim
		want []string
	}{
		{"cross-tenant secrets", sim{crossTenantSecrets: true}, []string{"ACC-02", "ACC-03"}},
		{"viewer reads secrets", sim{viewerReadsSecrets: true}, []string{"ACC-04"}},
		{"tenant may delete guardrails", sim{deleteGuardrails: true}, []string{"GRD-02", "GRD-03", "GRD-04"}},
		{"no Pod Security", sim{privilegedAllowed: true}, []string{"ADM-02"}},
		{"node ports allowed", sim{nodePortsAllowed: true}, []string{"QTA-04"}},
		{"no default limits", sim{noLimitDefaults: true}, []string{"QTA-07", "QTA-06"}},
		{"exec into other tenants", sim{crossTenantExec: true}, []string{"GRD-10"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sort.Strings(c.want)
			got := failing(t, c.sim)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("failed %v, want exactly %v", got, c.want)
			}
		})
	}
}

func TestDryRunSendsNothing(t *testing.T) {
	env := sim{}.env(sandbox.DryRun)
	rs := runner.Run(context.Background(), env, runner.Options{Checks: readOnlyChecks(), CheckTimeout: time.Second})
	for _, r := range rs {
		if r.Status != runner.Planned && r.Status != runner.Skipped {
			t.Errorf("%s: %s in a dry run", r.Check, r.Status)
		}
	}
	if len(env.Sandbox.Requests()) == 0 {
		t.Error("a dry run must record the requests it would send")
	}
	for _, q := range env.Sandbox.Requests() {
		if q.Stores {
			t.Errorf("a read-only check would store an object: %s", q)
		}
	}
}

func TestOneTenantSkipsTheChecksBetweenTenants(t *testing.T) {
	env := sim{}.env(sandbox.Live)
	env.Tenants = tenants[:1]
	c, _ := catalog.Get("ACC-02")
	rs := runner.Run(context.Background(), env, runner.Options{Checks: []catalog.Check{c}, CheckTimeout: time.Second})
	if rs[0].Status != runner.Skipped {
		t.Errorf("got %+v", rs[0])
	}
}
