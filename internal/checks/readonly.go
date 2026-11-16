package checks

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

// The read-only checks: reads, access reviews and server-side dry runs. Nothing is stored.
func init() {
	runner.Register("ACC-01", acc01)
	runner.Register("ACC-02", crossTenant(getAbsentSecret))
	runner.Register("ACC-03", crossTenant(listSecrets))
	runner.Register("ACC-04", acc04)
	runner.Register("ACC-05", acc05)
	runner.Register("GRD-01", perTenant(createDeployment))
	runner.Register("GRD-02", deleteEach("networkpolicy", listNetworkPolicies, deleteNetworkPolicy))
	runner.Register("GRD-03", deleteEach("resourcequota", listQuotas, deleteQuota))
	runner.Register("GRD-04", deleteEach("limitrange", listLimitRanges, deleteLimitRange))
	runner.Register("GRD-05", perTenant(createAdminBinding))
	runner.Register("GRD-06", perTenant(loosenPodSecurity))
	runner.Register("GRD-07", perTenant(createEndpoints))
	runner.Register("GRD-08", perTenant(listEndpoints))
	runner.Register("GRD-09", perTenant(func(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
		return execReview(ctx, env, c, t, t)
	}))
	runner.Register("GRD-10", crossTenant(execReview))
	runner.Register("ADM-01", perTenant(createPod(restrictedPod)))
	runner.Register("ADM-02", perTenant(createPod(privilegedPod)))
	runner.Register("QTA-03", perTenant(createService(corev1.ServiceTypeClusterIP)))
	runner.Register("QTA-04", perTenant(createService(corev1.ServiceTypeNodePort)))
	runner.Register("QTA-05", perTenant(createService(corev1.ServiceTypeLoadBalancer)))
	runner.Register("QTA-06", perTenant(podAboveMaximum))
	runner.Register("QTA-07", perTenant(podGetsDefaults))
}

type tenantFunc func(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result
type pairFunc func(ctx context.Context, env *runner.Env, c catalog.Check, a, b tenant.Tenant) runner.Result

// perTenant runs f once for every tenant.
func perTenant(f tenantFunc) runner.Func {
	return func(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
		var out []runner.Result
		for _, t := range env.Tenants {
			out = append(out, f(ctx, env, c, t))
		}
		return out
	}
}

// crossTenant runs f for every ordered pair of different tenants.
func crossTenant(f pairFunc) runner.Func {
	return func(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
		ps := pairs(env.Tenants)
		if len(ps) == 0 {
			return needTwo
		}
		var out []runner.Result
		for _, p := range ps {
			out = append(out, f(ctx, env, c, p[0], p[1]))
		}
		return out
	}
}

// asAdmin sends a request as the tenant's admin.
func asAdmin(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant, req sandbox.Request, fn func(context.Context, kubernetes.Interface) error) error {
	req.As, req.Check = t.Admin.String(), c.ID
	return do(ctx, env, req, func(ctx context.Context) error {
		cl, err := client(env, t.Admin)
		if err != nil {
			return err
		}
		return fn(ctx, cl)
	})
}

// ---------------------------------------------------------------- access

func acc01(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
	var out []runner.Result
	for _, t := range env.Tenants {
		err := asAdmin(ctx, env, c, t, sandbox.Request{Verb: "list", Kind: "secrets", Namespace: t.Namespace}, func(ctx context.Context, cl kubernetes.Interface) error {
			_, err := cl.CoreV1().Secrets(t.Namespace).List(ctx, metav1.ListOptions{Limit: 1})
			return err
		})
		if err != nil {
			out = append(out, allowed(t.Namespace, err))
			continue
		}
		// Reading one by name must reach the store: "not found" means allowed.
		err = asAdmin(ctx, env, c, t, sandbox.Request{Verb: "get", Kind: "secret", Namespace: t.Namespace, Name: absentName}, func(ctx context.Context, cl kubernetes.Interface) error {
			_, err := cl.CoreV1().Secrets(t.Namespace).Get(ctx, absentName, metav1.GetOptions{})
			return err
		})
		if isNotFound(err) {
			err = nil
		}
		out = append(out, allowed(t.Namespace, err))
	}
	return out
}

func getAbsentSecret(ctx context.Context, env *runner.Env, c catalog.Check, a, b tenant.Tenant) runner.Result {
	err := asAdmin(ctx, env, c, a, sandbox.Request{Verb: "get", Kind: "secret", Namespace: b.Namespace, Name: absentName}, func(ctx context.Context, cl kubernetes.Interface) error {
		_, err := cl.CoreV1().Secrets(b.Namespace).Get(ctx, absentName, metav1.GetOptions{})
		return err
	})
	if isNotFound(err) {
		return runner.Result{Status: runner.Fail, Subject: between(a, b), Message: "the request reached the secret store (\"not found\" instead of \"forbidden\"): a secret of that name would have been returned"}
	}
	return blocked(between(a, b), err, LayerRBAC)
}

func listSecrets(ctx context.Context, env *runner.Env, c catalog.Check, a, b tenant.Tenant) runner.Result {
	err := asAdmin(ctx, env, c, a, sandbox.Request{Verb: "list", Kind: "secrets", Namespace: b.Namespace}, func(ctx context.Context, cl kubernetes.Interface) error {
		_, err := cl.CoreV1().Secrets(b.Namespace).List(ctx, metav1.ListOptions{Limit: 1})
		return err
	})
	return blocked(between(a, b), err, LayerRBAC)
}

// asViewer sends a request as the tenant's viewer. Callers use perViewer, which skips tenants without one.
func asViewer(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant, req sandbox.Request, fn func(context.Context, kubernetes.Interface) error) error {
	req.As, req.Check = t.Viewer.String(), c.ID
	return do(ctx, env, req, func(ctx context.Context) error {
		cl, err := client(env, *t.Viewer)
		if err != nil {
			return err
		}
		return fn(ctx, cl)
	})
}

func acc04(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
	return perViewer(env, func(t tenant.Tenant) runner.Result {
		err := asViewer(ctx, env, c, t, sandbox.Request{Verb: "get", Kind: "secret", Namespace: t.Namespace, Name: absentName}, func(ctx context.Context, cl kubernetes.Interface) error {
			_, err := cl.CoreV1().Secrets(t.Namespace).Get(ctx, absentName, metav1.GetOptions{})
			return err
		})
		if isNotFound(err) {
			return runner.Result{Status: runner.Fail, Subject: t.Namespace, Message: "the viewer's request reached the secret store (\"not found\" instead of \"forbidden\")"}
		}
		return blocked(t.Namespace, err, LayerRBAC)
	})
}

func acc05(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
	return perViewer(env, func(t tenant.Tenant) runner.Result {
		err := asViewer(ctx, env, c, t, sandbox.Request{Verb: "list", Kind: "pods", Namespace: t.Namespace}, func(ctx context.Context, cl kubernetes.Interface) error {
			_, err := cl.CoreV1().Pods(t.Namespace).List(ctx, metav1.ListOptions{Limit: 1})
			return err
		})
		return allowed(t.Namespace, err)
	})
}

// perViewer runs f for every tenant with a viewer, and skips the rest.
func perViewer(env *runner.Env, f func(tenant.Tenant) runner.Result) []runner.Result {
	var out []runner.Result
	for _, t := range env.Tenants {
		if t.Viewer == nil {
			out = append(out, runner.Result{Status: runner.Skipped, Subject: t.Namespace, Message: "the tenant has no viewer (--viewer-binding)"})
			continue
		}
		out = append(out, f(t))
	}
	return out
}

// ---------------------------------------------------------------- guardrails

func createDeployment(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
	pod := restrictedPod(t.Namespace, "")
	labels := map[string]string{"app": "bhc-probe"}
	one := int32(1)
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: env.Sandbox.Name("deploy"), Namespace: t.Namespace, Labels: env.Sandbox.Labels()},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels}, Spec: pod.Spec},
		},
	}
	err := asAdmin(ctx, env, c, t, sandbox.Request{Verb: "create", Kind: "deployment", Namespace: t.Namespace, Name: d.Name}, func(ctx context.Context, cl kubernetes.Interface) error {
		_, err := cl.AppsV1().Deployments(t.Namespace).Create(ctx, d, metav1.CreateOptions{DryRun: dryRun})
		return err
	})
	return allowed(t.Namespace, err)
}

type lister func(ctx context.Context, cl kubernetes.Interface, ns string) ([]string, error)
type deleter func(ctx context.Context, cl kubernetes.Interface, ns, name string) error

// deleteEach lists the objects of a kind in each tenant namespace with the user's own identity,
// and tries to delete each one as the tenant admin, as a server-side dry run.
func deleteEach(kind string, list lister, del deleter) runner.Func {
	return perTenant(func(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
		var names []string
		err := do(ctx, env, sandbox.Request{Verb: "list", Kind: kind, Namespace: t.Namespace, As: you, Check: c.ID}, func(ctx context.Context) error {
			var err error
			names, err = list(ctx, env.Cluster.Client, t.Namespace)
			return err
		})
		if err != nil {
			return errResult(t.Namespace, err)
		}
		if len(names) == 0 {
			return runner.Result{Status: runner.Fail, Subject: t.Namespace, Message: fmt.Sprintf("the namespace has no %s to protect", kind)}
		}
		var res runner.Result
		for _, n := range names {
			err := asAdmin(ctx, env, c, t, sandbox.Request{Verb: "delete", Kind: kind, Namespace: t.Namespace, Name: n}, func(ctx context.Context, cl kubernetes.Interface) error {
				return del(ctx, cl, t.Namespace, n)
			})
			res = blocked(t.Namespace+" "+kind+"/"+n, err, LayerRBAC, LayerAdmission)
			if res.Status != runner.Pass {
				return res
			}
		}
		if len(names) > 1 {
			res.Subject = fmt.Sprintf("%s (%d %s objects)", t.Namespace, len(names), kind)
		}
		return res
	})
}

var dryDelete = metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}}

func listNetworkPolicies(ctx context.Context, cl kubernetes.Interface, ns string) ([]string, error) {
	l, err := cl.NetworkingV1().NetworkPolicies(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, o := range l.Items {
		out = append(out, o.Name)
	}
	return out, nil
}

func deleteNetworkPolicy(ctx context.Context, cl kubernetes.Interface, ns, name string) error {
	return cl.NetworkingV1().NetworkPolicies(ns).Delete(ctx, name, dryDelete)
}

func listQuotas(ctx context.Context, cl kubernetes.Interface, ns string) ([]string, error) {
	l, err := cl.CoreV1().ResourceQuotas(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, o := range l.Items {
		out = append(out, o.Name)
	}
	return out, nil
}

func deleteQuota(ctx context.Context, cl kubernetes.Interface, ns, name string) error {
	return cl.CoreV1().ResourceQuotas(ns).Delete(ctx, name, dryDelete)
}

func listLimitRanges(ctx context.Context, cl kubernetes.Interface, ns string) ([]string, error) {
	l, err := cl.CoreV1().LimitRanges(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, o := range l.Items {
		out = append(out, o.Name)
	}
	return out, nil
}

func deleteLimitRange(ctx context.Context, cl kubernetes.Interface, ns, name string) error {
	return cl.CoreV1().LimitRanges(ns).Delete(ctx, name, dryDelete)
}

func createAdminBinding(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: env.Sandbox.Name("escalate"), Namespace: t.Namespace, Labels: env.Sandbox.Labels()},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "admin"},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: "bulkhead-check-escalation"}},
	}
	err := asAdmin(ctx, env, c, t, sandbox.Request{Verb: "create", Kind: "rolebinding", Namespace: t.Namespace, Name: rb.Name}, func(ctx context.Context, cl kubernetes.Interface) error {
		_, err := cl.RbacV1().RoleBindings(t.Namespace).Create(ctx, rb, metav1.CreateOptions{DryRun: dryRun})
		return err
	})
	return blocked(t.Namespace, err, LayerRBAC, LayerAdmission)
}

func loosenPodSecurity(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
	patch := []byte(`{"metadata":{"labels":{"pod-security.kubernetes.io/enforce":"privileged"}}}`)
	err := asAdmin(ctx, env, c, t, sandbox.Request{Verb: "update", Kind: "namespace", Name: t.Namespace}, func(ctx context.Context, cl kubernetes.Interface) error {
		_, err := cl.CoreV1().Namespaces().Patch(ctx, t.Namespace, types.MergePatchType, patch, metav1.PatchOptions{DryRun: dryRun})
		return err
	})
	return blocked(t.Namespace, err, LayerRBAC, LayerAdmission)
}

func createEndpoints(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
	//nolint:staticcheck // Endpoints are deprecated, but writing them is the risk this check is about (CVE-2021-25740).
	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: env.Sandbox.Name("redirect"), Namespace: t.Namespace, Labels: env.Sandbox.Labels()},
		//nolint:staticcheck // see above
		Subsets: []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.96.0.1"}}, Ports: []corev1.EndpointPort{{Port: 443}}}},
	}
	err := asAdmin(ctx, env, c, t, sandbox.Request{Verb: "create", Kind: "endpoints", Namespace: t.Namespace, Name: ep.Name}, func(ctx context.Context, cl kubernetes.Interface) error {
		_, err := cl.CoreV1().Endpoints(t.Namespace).Create(ctx, ep, metav1.CreateOptions{DryRun: dryRun}) //nolint:staticcheck // see above
		return err
	})
	return blocked(t.Namespace, err, LayerRBAC, LayerAdmission)
}

func listEndpoints(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
	err := asAdmin(ctx, env, c, t, sandbox.Request{Verb: "list", Kind: "endpoints", Namespace: t.Namespace}, func(ctx context.Context, cl kubernetes.Interface) error {
		_, err := cl.CoreV1().Endpoints(t.Namespace).List(ctx, metav1.ListOptions{Limit: 1}) //nolint:staticcheck // reading endpoints is what is checked
		return err
	})
	return allowed(t.Namespace, err)
}

// execReview asks, as the admin of a, whether it may open a shell in pods of b.
func execReview(ctx context.Context, env *runner.Env, c catalog.Check, a, b tenant.Tenant) runner.Result {
	subject := b.Namespace
	if a.Namespace != b.Namespace {
		subject = between(a, b)
	}
	var review *authorizationv1.SelfSubjectAccessReview
	err := asAdmin(ctx, env, c, a, sandbox.Request{Verb: "review", Kind: "SelfSubjectAccessReview", Namespace: b.Namespace, Name: "create pods/exec"}, func(ctx context.Context, cl kubernetes.Interface) error {
		var err error
		review, err = cl.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: b.Namespace, Verb: "create", Resource: "pods", Subresource: "exec",
			}},
		}, metav1.CreateOptions{})
		return err
	})
	if err != nil {
		return errResult(subject, err)
	}
	ok := review.Status.Allowed
	switch {
	case c.Expect == catalog.Allowed && ok:
		return runner.Result{Status: runner.Pass, Subject: subject}
	case c.Expect == catalog.Allowed:
		return runner.Result{Status: runner.Fail, Subject: subject, Layer: LayerRBAC, Message: "the access review says no: " + review.Status.Reason}
	case ok:
		return runner.Result{Status: runner.Fail, Subject: subject, Message: "the access review allows it: " + review.Status.Reason}
	}
	return runner.Result{Status: runner.Pass, Subject: subject, Layer: LayerRBAC, Message: "refused by rbac (access review)"}
}

// ---------------------------------------------------------------- admission and quota

func privilegedPod(ns, name string) *corev1.Pod {
	p := restrictedPod(ns, name)
	yes := true
	// Kubernetes refuses privileged together with allowPrivilegeEscalation=false as invalid, so it is unset.
	p.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{Privileged: &yes}
	return p
}

func createPod(newPod func(ns, name string) *corev1.Pod) tenantFunc {
	return func(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
		p := newPod(t.Namespace, "")
		env.Sandbox.Prepare(&p.ObjectMeta, "pod")
		err := asAdmin(ctx, env, c, t, sandbox.Request{Verb: "create", Kind: "pod", Namespace: t.Namespace, Name: p.Name}, func(ctx context.Context, cl kubernetes.Interface) error {
			_, err := cl.CoreV1().Pods(t.Namespace).Create(ctx, p, metav1.CreateOptions{DryRun: dryRun})
			return err
		})
		if c.Expect == catalog.Allowed {
			return allowed(t.Namespace, err)
		}
		return blocked(t.Namespace, err, LayerPodSecurity, LayerAdmission)
	}
}

func createService(typ corev1.ServiceType) tenantFunc {
	return func(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
		s := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: t.Namespace},
			Spec: corev1.ServiceSpec{
				Type:     typ,
				Selector: map[string]string{"app": "bhc-probe"},
				Ports:    []corev1.ServicePort{{Port: 8080, TargetPort: intstr.FromInt32(8080)}},
			},
		}
		env.Sandbox.Prepare(&s.ObjectMeta, "svc")
		err := asAdmin(ctx, env, c, t, sandbox.Request{Verb: "create", Kind: "service", Namespace: t.Namespace, Name: s.Name}, func(ctx context.Context, cl kubernetes.Interface) error {
			_, err := cl.CoreV1().Services(t.Namespace).Create(ctx, s, metav1.CreateOptions{DryRun: dryRun})
			return err
		})
		if c.Expect == catalog.Allowed {
			return allowed(t.Namespace, err)
		}
		return blocked(t.Namespace, err, LayerQuota, LayerAdmission)
	}
}

// containerMax returns the smallest per-container CPU maximum of the namespace's limit ranges.
func containerMax(ctx context.Context, env *runner.Env, c catalog.Check, ns string) (*resource.Quantity, error) {
	var maximum *resource.Quantity
	err := do(ctx, env, sandbox.Request{Verb: "list", Kind: "limitranges", Namespace: ns, As: you, Check: c.ID}, func(ctx context.Context) error {
		l, err := env.Cluster.Client.CoreV1().LimitRanges(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for _, lr := range l.Items {
			for _, item := range lr.Spec.Limits {
				if item.Type != corev1.LimitTypeContainer {
					continue
				}
				if q, ok := item.Max[corev1.ResourceCPU]; ok && (maximum == nil || q.Cmp(*maximum) < 0) {
					q := q.DeepCopy()
					maximum = &q
				}
			}
		}
		return nil
	})
	return maximum, err
}

func podAboveMaximum(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
	maximum, err := containerMax(ctx, env, c, t.Namespace)
	if err != nil {
		return errResult(t.Namespace, err)
	}
	if maximum == nil && env.Sandbox.Mode != sandbox.DryRun {
		return runner.Result{Status: runner.Fail, Subject: t.Namespace, Message: "no limit range sets a per-container CPU maximum"}
	}
	limit := quantity("64")
	if maximum != nil {
		limit = *maximum
		limit.Add(*maximum) // twice the maximum
	}
	p := restrictedPod(t.Namespace, "")
	env.Sandbox.Prepare(&p.ObjectMeta, "too-big")
	p.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: quantity("10m"), corev1.ResourceMemory: quantity("16Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: limit, corev1.ResourceMemory: quantity("32Mi")},
	}
	err = asAdmin(ctx, env, c, t, sandbox.Request{Verb: "create", Kind: "pod", Namespace: t.Namespace, Name: p.Name}, func(ctx context.Context, cl kubernetes.Interface) error {
		_, err := cl.CoreV1().Pods(t.Namespace).Create(ctx, p, metav1.CreateOptions{DryRun: dryRun})
		return err
	})
	res := blocked(t.Namespace, err, LayerLimitRange)
	if res.Status == runner.Pass && maximum != nil {
		res.Message += fmt.Sprintf(" (CPU limit %s, maximum %s)", limit.String(), maximum.String())
	}
	return res
}

func podGetsDefaults(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
	p := restrictedPod(t.Namespace, "")
	env.Sandbox.Prepare(&p.ObjectMeta, "defaults")
	var got *corev1.Pod
	err := asAdmin(ctx, env, c, t, sandbox.Request{Verb: "create", Kind: "pod", Namespace: t.Namespace, Name: p.Name}, func(ctx context.Context, cl kubernetes.Interface) error {
		var err error
		got, err = cl.CoreV1().Pods(t.Namespace).Create(ctx, p, metav1.CreateOptions{DryRun: dryRun})
		return err
	})
	if err != nil {
		return allowed(t.Namespace, err)
	}
	limits := got.Spec.Containers[0].Resources.Limits
	cpu, hasCPU := limits[corev1.ResourceCPU]
	mem, hasMem := limits[corev1.ResourceMemory]
	if !hasCPU || !hasMem {
		return runner.Result{Status: runner.Fail, Subject: t.Namespace, Layer: LayerLimitRange, Message: fmt.Sprintf("a container without resources got limits %v; the limit range must default both CPU and memory", limits)}
	}
	return runner.Result{Status: runner.Pass, Subject: t.Namespace, Message: fmt.Sprintf("defaults filled in: CPU %s, memory %s", cpu.String(), mem.String())}
}
