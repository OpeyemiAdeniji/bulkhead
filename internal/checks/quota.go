package checks

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

// MaxQuotaPods caps how many pods QTA-08 creates in one tenant. A namespace with a larger free
// pod count is skipped, so the checker never floods a large tenant.
var MaxQuotaPods = 25

func init() {
	runner.Register("QTA-01", quotaPerTenant(qta01))
	runner.Register("QTA-02", quotaPerTenant(qta02))
	runner.Register("QTA-08", quotaPerTenant(qta08))
	runner.Register("QTA-09", quotaPerTenant(qta09))
}

// quotaState remembers, per tenant, what the earlier quota checks found.
type quotaState struct {
	released  bool
	firstCPU  map[string]resource.Quantity // QTA-01: the request of the accepted pod
	filled    map[string]int               // QTA-08: how many pods filled the pod count
	firstDone map[string]bool              // QTA-02 ran, so QTA-08 releases the pods of QTA-01 and 02
}

func quotaOf(env *runner.Env) *quotaState {
	s := stateOf(env)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.quota == nil {
		s.quota = &quotaState{firstCPU: map[string]resource.Quantity{}, filled: map[string]int{}, firstDone: map[string]bool{}}
	}
	return s.quota
}

func quotaPerTenant(f tenantFunc) runner.Func {
	return func(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
		q := quotaOf(env)
		if !q.released {
			// The pods of the network and noisy-neighbour checks would count against the quota.
			if err := releasePods(ctx, env, c.ID); err != nil && !errors.Is(err, sandbox.ErrNotSent) {
				return fixtureFailed(err)
			}
			q.released = true
		}
		var out []runner.Result
		for _, t := range env.Tenants {
			out = append(out, f(ctx, env, c, t))
		}
		return out
	}
}

// cpuRequests returns the hard limit and the use of requests.cpu of the namespace's quota.
// ok is false when no quota limits CPU requests.
func cpuRequests(ctx context.Context, env *runner.Env, checkID, ns string) (hard, used resource.Quantity, name string, ok bool, err error) {
	err = do(ctx, env, sandbox.Request{Verb: "list", Kind: "resourcequotas", Namespace: ns, As: you, Check: checkID}, func(ctx context.Context) error {
		l, err := env.Cluster.Client.CoreV1().ResourceQuotas(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for _, q := range l.Items {
			if h, found := q.Status.Hard[corev1.ResourceRequestsCPU]; found {
				hard, used, name, ok = h, q.Status.Used[corev1.ResourceRequestsCPU], q.Name, true
				return nil
			}
		}
		return nil
	})
	return hard, used, name, ok, err
}

// podCount returns the hard limit and the use of the pod count of the namespace's quota.
func podCount(ctx context.Context, env *runner.Env, checkID, ns string) (hard, used int64, ok bool, err error) {
	err = do(ctx, env, sandbox.Request{Verb: "list", Kind: "resourcequotas", Namespace: ns, As: you, Check: checkID}, func(ctx context.Context) error {
		l, err := env.Cluster.Client.CoreV1().ResourceQuotas(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for _, q := range l.Items {
			if h, found := q.Status.Hard[corev1.ResourcePods]; found {
				u := q.Status.Used[corev1.ResourcePods]
				hard, used, ok = h.Value(), u.Value(), true
				return nil
			}
		}
		return nil
	})
	return hard, used, ok, err
}

// cpuPod returns a test pod that requests (and is limited to) the given CPU.
func cpuPod(env *runner.Env, ns, purpose string, cpu resource.Quantity) *corev1.Pod {
	p := restrictedPod(ns, "")
	env.Sandbox.Prepare(&p.ObjectMeta, purpose)
	p.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: quantity("32Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: quantity("64Mi")},
	}
	return p
}

// createAs creates a test pod as the tenant admin, through the sandbox.
func createAs(ctx context.Context, env *runner.Env, checkID string, t tenant.Tenant, p *corev1.Pod) error {
	return env.Sandbox.Create(ctx, p, "pod", t.Admin.String(), checkID, func(ctx context.Context) error {
		cl, err := client(env, t.Admin)
		if err != nil {
			return err
		}
		_, err = cl.CoreV1().Pods(t.Namespace).Create(ctx, p, metav1.CreateOptions{})
		return err
	})
}

func qta01(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
	hard, used, name, ok, err := cpuRequests(ctx, env, c.ID, t.Namespace)
	if err != nil {
		return errResult(t.Namespace, err)
	}
	if !ok && env.Sandbox.Mode != sandbox.DryRun {
		return runner.Result{Status: runner.Fail, Subject: t.Namespace, Message: "no resource quota limits requests.cpu"}
	}
	free := hard.DeepCopy()
	free.Sub(used)
	half := resource.NewMilliQuantity(free.MilliValue()/2, resource.DecimalSI)
	if half.MilliValue() < 10 && env.Sandbox.Mode != sandbox.DryRun {
		return runner.Result{Status: runner.Error, Subject: t.Namespace, Message: fmt.Sprintf("quota %s has only %s of CPU requests free, too little to test", name, free.String())}
	}
	err = createAs(ctx, env, c.ID, t, cpuPod(env, t.Namespace, "quota-first", *half))
	res := allowed(t.Namespace, err)
	if res.Status == runner.Pass {
		quotaOf(env).firstCPU[t.Namespace] = *half
		res.Message = fmt.Sprintf("a pod requesting %s of the %s free in quota %s was accepted", half.String(), free.String(), name)
	}
	return res
}

func qta02(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
	q := quotaOf(env)
	q.firstDone[t.Namespace] = true
	first, ok := q.firstCPU[t.Namespace]
	if !ok {
		if env.Sandbox.Mode == sandbox.DryRun {
			first = quantity("100m")
		} else {
			return runner.Result{Status: runner.Skipped, Subject: t.Namespace, Message: "QTA-01 did not create its pod"}
		}
	}
	// What is left after the first pod is about the same as the first pod's request; ask for more.
	more := first.DeepCopy()
	more.Add(quantity("50m"))
	err := createAs(ctx, env, c.ID, t, cpuPod(env, t.Namespace, "quota-second", more))
	res := blocked(t.Namespace, err, LayerQuota)
	if res.Status == runner.Pass {
		res.Message += fmt.Sprintf(" (a second pod requesting %s)", more.String())
	}
	return res
}

func qta08(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
	q := quotaOf(env)
	if q.firstDone[t.Namespace] || len(q.firstCPU) > 0 {
		// The pods of QTA-01 and QTA-02 must not take places of the pod count.
		if err := releasePods(ctx, env, c.ID); err != nil && !errors.Is(err, sandbox.ErrNotSent) {
			return errResult(t.Namespace, err)
		}
		q.firstCPU = map[string]resource.Quantity{}
		q.firstDone = map[string]bool{}
	}
	hard, used, ok, err := podCount(ctx, env, c.ID, t.Namespace)
	if err != nil {
		return errResult(t.Namespace, err)
	}
	if !ok && env.Sandbox.Mode != sandbox.DryRun {
		return runner.Result{Status: runner.Fail, Subject: t.Namespace, Message: "no resource quota limits the pod count"}
	}
	n := int(hard - used)
	if env.Sandbox.Mode == sandbox.DryRun {
		n = 1 // the plan shows the pattern; the real count comes from the quota
	}
	if n > MaxQuotaPods {
		return runner.Result{Status: runner.Skipped, Subject: t.Namespace, Message: fmt.Sprintf("the quota has room for %d more pods; the checker creates at most %d", n, MaxQuotaPods)}
	}
	if n <= 0 {
		return runner.Result{Status: runner.Error, Subject: t.Namespace, Message: "the pod count of the quota is already used up by the tenant's own pods"}
	}
	for i := 1; i <= n; i++ {
		p := restrictedPod(t.Namespace, "")
		env.Sandbox.Prepare(&p.ObjectMeta, fmt.Sprintf("quota-fill-%d", i))
		if err := createAs(ctx, env, c.ID, t, p); err != nil {
			res := allowed(t.Namespace, err)
			if res.Status == runner.Fail {
				res.Message = fmt.Sprintf("pod %d of the %d the quota allows was refused (the limit range defaults do not fit the quota): %s", i, n, res.Message)
			}
			return res
		}
	}
	q.filled[t.Namespace] = n
	return runner.Result{Status: runner.Pass, Subject: t.Namespace, Message: fmt.Sprintf("%d default-sized pods filled the pod count (%d of %d)", n, hard, hard)}
}

func qta09(ctx context.Context, env *runner.Env, c catalog.Check, t tenant.Tenant) runner.Result {
	if _, ok := quotaOf(env).filled[t.Namespace]; !ok && env.Sandbox.Mode != sandbox.DryRun {
		return runner.Result{Status: runner.Skipped, Subject: t.Namespace, Message: "QTA-08 did not fill the pod count"}
	}
	p := restrictedPod(t.Namespace, "")
	env.Sandbox.Prepare(&p.ObjectMeta, "quota-one-more")
	err := createAs(ctx, env, c.ID, t, p)
	return blocked(t.Namespace, err, LayerQuota)
}
