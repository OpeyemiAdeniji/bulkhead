package checks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
)

func node(name string, ready bool, taint bool) *corev1.Node {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	st := corev1.ConditionFalse
	if ready {
		st = corev1.ConditionTrue
	}
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: st}}
	if taint {
		n.Spec.Taints = []corev1.Taint{{Key: "node-role.kubernetes.io/control-plane", Effect: corev1.TaintEffectNoSchedule}}
	}
	return n
}

// netSim is a cluster whose pods start at once, and whose network lets traffic through within a
// tenant, and between tenants only when open is true.
type netSim struct {
	open       bool
	badImage   bool
	nodes      []runtime.Object
	mu         sync.Mutex
	ips        map[string]string // IP -> namespace
	nextIP     int
	internetUp bool
}

func (s *netSim) env(t *testing.T, mode sandbox.Mode) *runner.Env {
	t.Helper()
	s.ips = map[string]string{}
	c := fake.NewClientset(s.nodes...)
	// Pods become ready at once, on the node of their node selector, with an IP of their own.
	c.PrependReactor("get", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		ga := a.(k8stesting.GetAction)
		obj, err := c.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), ga.GetNamespace(), ga.GetName())
		if err != nil {
			return true, nil, err
		}
		p := obj.(*corev1.Pod).DeepCopy()
		if s.badImage {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "no such image"}}}}
			return true, p, nil
		}
		s.mu.Lock()
		s.nextIP++
		ip := fmt.Sprintf("10.0.0.%d", s.nextIP)
		s.ips[ip] = p.Namespace
		s.mu.Unlock()
		p.Spec.NodeName = p.Spec.NodeSelector[corev1.LabelHostname]
		p.Status.PodIP = ip
		p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
		return true, p, nil
	})
	old := Exec
	t.Cleanup(func() { Exec = old })
	Exec = func(_ context.Context, _ *runner.Env, ns, _ string, cmd []string) (string, error) {
		url := cmd[len(cmd)-1]
		switch {
		case cmd[0] == "nslookup":
			return "Name: kubernetes.default.svc.cluster.local", nil
		case url == InternetProbe:
			if s.internetUp {
				return "", nil
			}
			return "wget: download timed out", errors.New("command terminated with exit code 1")
		}
		ip := strings.TrimSuffix(strings.TrimPrefix(url, "http://"), ":8080")
		s.mu.Lock()
		dst := s.ips[ip]
		s.mu.Unlock()
		if dst == ns || s.open {
			return "ok", nil
		}
		return "wget: download timed out", errors.New("command terminated with exit code 1")
	}
	return &runner.Env{
		Cluster: &kube.Cluster{Client: c},
		Tenants: tenants,
		Sandbox: sandbox.New(mode),
		Impersonate: func(id kube.Identity) (*kube.Cluster, error) {
			return &kube.Cluster{Client: c, As: &id}, nil
		},
	}
}

func twoWorkers() []runtime.Object {
	return []runtime.Object{node("worker-1", true, false), node("worker-2", true, false), node("control-plane", true, true), node("broken", false, false)}
}

func runNet(t *testing.T, env *runner.Env, ids ...string) map[string][]runner.Result {
	t.Helper()
	t.Cleanup(func() { ForgetRun(env.Sandbox) })
	var cs []catalog.Check
	for _, id := range ids {
		c, _ := catalog.Get(id)
		cs = append(cs, c)
	}
	rs := runner.Run(context.Background(), env, runner.Options{Checks: cs, CheckTimeout: 10 * time.Second})
	out := map[string][]runner.Result{}
	for _, r := range rs {
		out[r.Check] = append(out[r.Check], r)
	}
	return out
}

var allNet = []string{"NET-01", "NET-02", "NET-03", "NET-04", "NET-05", "NET-06"}

func TestAnIsolatedNetworkPassesEveryNetworkCheck(t *testing.T) {
	s := &netSim{nodes: twoWorkers()}
	env := s.env(t, sandbox.Live)
	got := runNet(t, env, allNet...)
	for _, id := range allNet {
		for _, r := range got[id] {
			if r.Status != runner.Pass {
				t.Errorf("%s %s: %s %s", id, r.Subject, r.Status, r.Message)
			}
		}
	}
	// Two tenants, two nodes: four pairs on the same node and four across nodes, as the bash suite.
	if len(got["NET-04"]) != 4 || len(got["NET-05"]) != 4 {
		t.Errorf("NET-04 %d results, NET-05 %d, want 4 each", len(got["NET-04"]), len(got["NET-05"]))
	}
	if !strings.Contains(got["NET-05"][0].Subject, "worker-1 -> worker-2") {
		t.Errorf("subject %q should name both nodes", got["NET-05"][0].Subject)
	}
	// 2 tenants x 2 nodes x (server + client), created once for all six checks.
	if n := env.Sandbox.Created(); n != 8 {
		t.Errorf("%d pods created, want 8", n)
	}
}

func TestAnOpenNetworkFailsExactlyTheChecksBetweenTenants(t *testing.T) {
	s := &netSim{nodes: twoWorkers(), open: true, internetUp: true}
	got := runNet(t, s.env(t, sandbox.Live), allNet...)
	for _, id := range allNet {
		want := runner.Pass
		if id == "NET-04" || id == "NET-05" || id == "NET-06" {
			want = runner.Fail
		}
		for _, r := range got[id] {
			if r.Status != want {
				t.Errorf("%s %s: %s, want %s", id, r.Subject, r.Status, want)
			}
		}
	}
}

func TestOneNodeSkipsTheChecksAcrossNodes(t *testing.T) {
	s := &netSim{nodes: []runtime.Object{node("only", true, false)}}
	got := runNet(t, s.env(t, sandbox.Live), allNet...)
	for _, id := range []string{"NET-02", "NET-05"} {
		if got[id][0].Status != runner.Skipped {
			t.Errorf("%s: %+v", id, got[id][0])
		}
	}
	if got["NET-04"][0].Status != runner.Pass {
		t.Errorf("NET-04 must still run on one node: %+v", got["NET-04"][0])
	}
}

func TestATestPodThatCannotStartIsAnError(t *testing.T) {
	s := &netSim{nodes: twoWorkers(), badImage: true}
	env := s.env(t, sandbox.Live)
	got := runNet(t, env, "NET-01", "NET-04")
	if got["NET-01"][0].Status != runner.Error || !strings.Contains(got["NET-01"][0].Message, "ImagePullBackOff") {
		t.Errorf("got %+v", got["NET-01"][0])
	}
	// The second check reuses the outcome instead of creating the pods again.
	if env.Sandbox.Created() != 8 {
		t.Errorf("%d pods created, want 8 (once)", env.Sandbox.Created())
	}
}

func TestNetworkDryRunPlansEveryPod(t *testing.T) {
	s := &netSim{nodes: twoWorkers()}
	env := s.env(t, sandbox.DryRun)
	got := runNet(t, env, allNet...)
	for _, id := range allNet {
		if got[id][0].Status != runner.Planned {
			t.Errorf("%s: %+v", id, got[id][0])
		}
	}
	creates := 0
	for _, q := range env.Sandbox.Requests() {
		if q.Verb == "create" && q.Stores {
			creates++
		}
	}
	// A dry run sends nothing, not even the list of nodes, so it plans one server and one client
	// per tenant without placement. A real run places them on two nodes (8 pods).
	if creates != 4 {
		t.Errorf("the plan has %d pod creations, want 4", creates)
	}
}

func TestUsableNodes(t *testing.T) {
	cordoned := node("c", true, false)
	cordoned.Spec.Unschedulable = true
	for _, c := range []struct {
		n    *corev1.Node
		want bool
	}{
		{node("ok", true, false), true},
		{node("tainted", true, true), false},
		{node("not-ready", false, false), false},
		{cordoned, false},
	} {
		if usable(*c.n) != c.want {
			t.Errorf("%s: usable=%v", c.n.Name, !c.want)
		}
	}
}
