package checks

import (
	"context"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
)

// workloadPod is a running, ready pod of a tenant's own workload.
func workloadPod(ns, name string, labels map[string]string, change ...func(*corev1.Pod)) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "example/orders:1.0"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.9.9.9",
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}},
	}
	for _, c := range change {
		c(p)
	}
	return p
}

// workloadSim extends flowEnv with workload pods. Ephemeral containers start at once (or wait
// with startError), and probes in them answer from the network table, keyed like flowEnv's.
type workloadSim struct {
	updates    int
	startError string
	execs      [][]string // ns, pod, container, command...
}

func (w *workloadSim) install(t *testing.T, env *runner.Env, network map[string]string, pods ...*corev1.Pod) {
	t.Helper()
	c := env.Cluster.Client.(*fake.Clientset)
	for _, p := range pods {
		if err := c.Tracker().Add(p); err != nil {
			t.Fatal(err)
		}
	}
	podsGVR := corev1.SchemeGroupVersion.WithResource("pods")
	isWorkload := func(ns, name string) bool {
		for _, p := range pods {
			if p.Namespace == ns && p.Name == name {
				return true
			}
		}
		return false
	}
	c.PrependReactor("update", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "ephemeralcontainers" {
			return false, nil, nil
		}
		w.updates++
		p := a.(k8stesting.UpdateAction).GetObject().(*corev1.Pod)
		return true, p, c.Tracker().Update(podsGVR, p, p.Namespace)
	})
	c.PrependReactor("get", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		ga := a.(k8stesting.GetAction)
		if !isWorkload(ga.GetNamespace(), ga.GetName()) {
			return false, nil, nil // the checker's own test pods: netSim answers
		}
		obj, err := c.Tracker().Get(podsGVR, ga.GetNamespace(), ga.GetName())
		if err != nil {
			return true, nil, err
		}
		p := obj.(*corev1.Pod).DeepCopy()
		p.Status.EphemeralContainerStatuses = nil
		for _, e := range p.Spec.EphemeralContainers {
			st := corev1.ContainerStatus{Name: e.Name, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}
			if w.startError != "" {
				st.State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: w.startError, Message: "no such image"}}
			}
			p.Status.EphemeralContainerStatuses = append(p.Status.EphemeralContainerStatuses, st)
		}
		return true, p, nil
	})
	old := ExecContainer
	t.Cleanup(func() { ExecContainer = old })
	ExecContainer = func(_ context.Context, _ *runner.Env, ns, pod, container string, cmd []string) (string, error) {
		w.execs = append(w.execs, append([]string{ns, pod, container}, cmd...))
		if cmd[0] != "sh" {
			return "", nil // touch doneFile
		}
		outcome, ok := network[ns+"/"+pod+" "+cmd[4]+":"+cmd[5]]
		if !ok {
			outcome = timedOut
		}
		return probeOutputs[outcome], nil
	}
}

const ordersFlows = `
apiVersion: bulkhead.io/v1alpha1
kind: Flows
flows:
  - {name: orders-to-db, from: tenant-a, via: {app: orders}, to: {service: db, namespace: shared, port: 5432}, expect: allowed}
  - {name: rest-of-a-not-to-db, from: tenant-a, to: {service: db, namespace: shared, port: 5432}, expect: blocked}
`

// TestALabelPolicyIsTestedFromItsWorkload: the orders pod may reach the database, the rest of the
// same namespace may not. Only a probe from inside the orders pod can show both.
func TestALabelPolicyIsTestedFromItsWorkload(t *testing.T) {
	env, clientProbes := flowEnv(t, sandbox.Live, ordersFlows, map[string]string{"tenant-a " + db: timedOut})
	env.ProbeWorkloads = true
	w := &workloadSim{}
	w.install(t, env, map[string]string{"tenant-a/orders-1 " + db: connected},
		workloadPod("tenant-a", "orders-2", map[string]string{"app": "orders"}),
		workloadPod("tenant-a", "orders-1", map[string]string{"app": "orders"}))
	got := runNet(t, env, "FLW-01", "FLW-02")
	a, b := got["FLW-01"][0], got["FLW-02"][0]
	if a.Status != runner.Pass || !strings.Contains(a.Message, "from inside tenant-a/orders-1") {
		t.Errorf("the flow from the orders pod: %+v", a)
	}
	if b.Status != runner.Pass || !strings.Contains(b.Message, "while the allowed flow orders-to-db connected") {
		t.Errorf("the flow from the rest of the namespace: %+v", b)
	}
	if len(*clientProbes) != 1 {
		t.Errorf("the flow without via must be probed from the client pod: %v", *clientProbes)
	}

	// The first matching pod by name received one restricted ephemeral container.
	p, err := env.Cluster.Client.CoreV1().Pods("tenant-a").Get(context.Background(), "orders-1", metav1.GetOptions{})
	if err != nil || len(p.Spec.EphemeralContainers) != 1 {
		t.Fatalf("orders-1 has %d ephemeral containers (%v)", len(p.Spec.EphemeralContainers), err)
	}
	e := p.Spec.EphemeralContainers[0]
	sc := e.SecurityContext
	if e.Name != sandbox.NamePrefix+env.Sandbox.RunID || e.Image != ProbeImage || sc == nil || !*sc.RunAsNonRoot || *sc.RunAsUser == 0 ||
		*sc.AllowPrivilegeEscalation || sc.Capabilities.Drop[0] != "ALL" || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("the probe container is not restricted: %+v", e)
	}

	// The run tells the container to exit, and reports the pod.
	probed := FinishWorkloadProbes(context.Background(), env)
	want := "tenant-a/orders-1 (" + e.Name + ")"
	if len(probed) != 1 || probed[0] != want {
		t.Errorf("reported %v, want %s", probed, want)
	}
	last := w.execs[len(w.execs)-1]
	if strings.Join(last, " ") != "tenant-a orders-1 "+e.Name+" touch "+doneFile {
		t.Errorf("the container was not told to exit: %v", last)
	}
}

func TestOneProbeContainerPerWorkload(t *testing.T) {
	file := ordersFlows + "  - {name: orders-to-api, from: tenant-a, via: {app: orders}, to: {host: api.example.com, port: 443}, expect: allowed}\n"
	env, _ := flowEnv(t, sandbox.Live, file, map[string]string{"tenant-a " + db: timedOut})
	env.ProbeWorkloads = true
	w := &workloadSim{}
	w.install(t, env, map[string]string{"tenant-a/orders-1 " + db: connected, "tenant-a/orders-1 api.example.com:443": connected},
		workloadPod("tenant-a", "orders-1", map[string]string{"app": "orders"}))
	got := runNet(t, env, "FLW-01")
	if len(got["FLW-01"]) != 2 || got["FLW-01"][0].Status != runner.Pass || got["FLW-01"][1].Status != runner.Pass {
		t.Fatalf("results: %+v", got["FLW-01"])
	}
	if w.updates != 1 {
		t.Errorf("two flows with the same selector added %d containers, want 1", w.updates)
	}
}

// TestWhichPodsMayBeProbed: terminating pods, the checker's own pods, opted-out pods, pods on
// the host network and pods that are not ready are never used.
func TestWhichPodsMayBeProbed(t *testing.T) {
	sel := map[string]string{"app": "orders"}
	now := metav1.Now()
	env, _ := flowEnv(t, sandbox.Live, ordersFlows, nil)
	env.ProbeWorkloads = true
	w := &workloadSim{}
	w.install(t, env, nil,
		workloadPod("tenant-a", "a-terminating", sel, func(p *corev1.Pod) { p.DeletionTimestamp = &now; p.Finalizers = []string{"x"} }),
		workloadPod("tenant-a", "b-test-pod", map[string]string{"app": "orders", sandbox.LabelManagedBy: sandbox.ManagedBy}),
		workloadPod("tenant-a", "c-opted-out", sel, func(p *corev1.Pod) { p.Annotations = map[string]string{NoProbeAnnotation: "false"} }),
		workloadPod("tenant-a", "d-host-network", sel, func(p *corev1.Pod) { p.Spec.HostNetwork = true }),
		workloadPod("tenant-a", "e-not-ready", sel, func(p *corev1.Pod) { p.Status.Conditions = nil }),
	)
	got := runNet(t, env, "FLW-01")
	r := got["FLW-01"][0]
	for _, want := range []string{"no running, ready pod in tenant-a matches app=orders", "a-terminating (terminating)", "b-test-pod (a test pod of the checker)",
		"c-opted-out (bulkhead.io/probe: \"false\")", "d-host-network (uses the host network)", "e-not-ready (not running and ready)"} {
		if r.Status != runner.Error || !strings.Contains(r.Message, want) {
			t.Errorf("the result misses %q: %+v", want, r)
		}
	}
	if w.updates != 0 || len(FinishWorkloadProbes(context.Background(), env)) != 0 {
		t.Error("no pod may receive a probe container")
	}
}

func TestAProbeContainerThatDoesNotStart(t *testing.T) {
	old := ReadyTimeout
	ReadyTimeout = 3 * time.Second
	t.Cleanup(func() { ReadyTimeout = old })
	env, _ := flowEnv(t, sandbox.Live, ordersFlows, nil)
	env.ProbeWorkloads = true
	w := &workloadSim{startError: "ImagePullBackOff"}
	w.install(t, env, nil, workloadPod("tenant-a", "orders-1", map[string]string{"app": "orders"}))
	got := runNet(t, env, "FLW-01")
	if r := got["FLW-01"][0]; r.Status != runner.Error || !strings.Contains(r.Message, "does not start: ImagePullBackOff") {
		t.Errorf("result: %+v", r)
	}
	// It was added, so it is in the pod's spec for good, and must be reported.
	if probed := FinishWorkloadProbes(context.Background(), env); len(probed) != 1 {
		t.Errorf("an added container must be reported even if it did not start: %v", probed)
	}
}

func TestWorkloadFlowsNeedTheFlag(t *testing.T) {
	env, _ := flowEnv(t, sandbox.Live, ordersFlows, map[string]string{"tenant-a " + db: timedOut})
	w := &workloadSim{}
	w.install(t, env, nil, workloadPod("tenant-a", "orders-1", map[string]string{"app": "orders"}))
	got := runNet(t, env, "FLW-01")
	if r := got["FLW-01"][0]; r.Status != runner.Error || !strings.Contains(r.Message, "needs --probe-workloads") {
		t.Errorf("result: %+v", r)
	}
	if w.updates != 0 {
		t.Error("a container was added without --probe-workloads")
	}
}

func TestWorkloadProbesInADryRun(t *testing.T) {
	env, _ := flowEnv(t, sandbox.DryRun, ordersFlows, nil)
	env.ProbeWorkloads = true
	w := &workloadSim{}
	w.install(t, env, nil, workloadPod("tenant-a", "orders-1", map[string]string{"app": "orders"}))
	got := runNet(t, env, "FLW-01")
	if r := got["FLW-01"][0]; r.Status != runner.Planned {
		t.Errorf("result: %+v", r)
	}
	if w.updates != 0 || len(w.execs) != 0 {
		t.Error("a dry run changed a workload")
	}
}

func TestPreflightWorkloadProbes(t *testing.T) {
	c := fake.NewClientset()
	var asked []string
	c.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		r := a.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		ra := r.Spec.ResourceAttributes
		asked = append(asked, ra.Verb+" "+ra.Resource+"/"+ra.Subresource)
		r.Status.Allowed = ra.Subresource != "ephemeralcontainers"
		return true, r, nil
	})
	err := PreflightWorkloadProbes(context.Background(), c, []string{"tenant-a"})
	if err == nil || !strings.Contains(err.Error(), "update pods/ephemeralcontainers in tenant-a") {
		t.Errorf("error: %v", err)
	}
	if strings.Join(asked, ", ") != "list pods/, update pods/ephemeralcontainers, create pods/exec" {
		t.Errorf("the reviews must name the subresources: %v", asked)
	}
}
