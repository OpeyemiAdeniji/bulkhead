package checks

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
)

// activeSim is a cluster for the noisy-neighbour and quota checks: pods start at once, the CPU hog
// burns at hogCPUs, the memory hog ends with memEnd, and a quota of 500m CPU requests and podLimit
// pods is enforced per namespace.
type activeSim struct {
	hogCPUs    float64
	throttled  bool
	memEnd     string // "OOMKilled", "Error" or "" (still running)
	podLimit   int64
	defaultCPU string // the limit range default request; "" means no defaults

	mu       sync.Mutex
	start    time.Time
	deleted  map[string]bool
	restarts int32
}

func (s *activeSim) env(t *testing.T) *runner.Env {
	t.Helper()
	old, oldWarm, oldMeasure, oldOOM, oldRelease := Exec, HogWarmUp, HogMeasure, OOMWaitTimeout, ReleaseTimeout
	t.Cleanup(func() {
		Exec, HogWarmUp, HogMeasure, OOMWaitTimeout, ReleaseTimeout = old, oldWarm, oldMeasure, oldOOM, oldRelease
	})
	HogWarmUp, HogMeasure, OOMWaitTimeout, ReleaseTimeout = time.Millisecond, 200*time.Millisecond, 3*time.Second, 5*time.Second
	s.deleted = map[string]bool{}
	var objects []runtime.Object
	objects = append(objects, node("worker-1", true, false), node("worker-2", true, false))
	c := fake.NewClientset(objects...)

	usage := func(ns string) (cpu int64, pods int64) {
		l, _ := c.Tracker().List(corev1.SchemeGroupVersion.WithResource("pods"), corev1.SchemeGroupVersion.WithKind("Pod"), ns)
		for _, p := range l.(*corev1.PodList).Items {
			r := p.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
			cpu += r.MilliValue()
			pods++
		}
		return
	}
	c.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		p := a.(k8stesting.CreateAction).GetObject().(*corev1.Pod).DeepCopy()
		if len(p.Spec.Containers[0].Resources.Requests) == 0 && s.defaultCPU != "" {
			p.Spec.Containers[0].Resources.Requests = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(s.defaultCPU)}
		}
		cpu, pods := usage(p.Namespace)
		req := p.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
		if pods+1 > s.podLimit || cpu+req.MilliValue() > 500 {
			return true, nil, forbidden("pods", fmt.Sprintf("exceeded quota: tenant-quota, requested: pods=1,requests.cpu=%s", req.String()))
		}
		return true, p, c.Tracker().Create(corev1.SchemeGroupVersion.WithResource("pods"), p, p.Namespace)
	})
	c.PrependReactor("list", "resourcequotas", func(a k8stesting.Action) (bool, runtime.Object, error) {
		cpu, pods := usage(a.GetNamespace())
		q := corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "tenant-quota", Namespace: a.GetNamespace()}}
		q.Status.Hard = corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("500m"), corev1.ResourcePods: *resource.NewQuantity(s.podLimit, resource.DecimalSI)}
		q.Status.Used = corev1.ResourceList{corev1.ResourceRequestsCPU: *resource.NewMilliQuantity(cpu, resource.DecimalSI), corev1.ResourcePods: *resource.NewQuantity(pods, resource.DecimalSI)}
		return true, &corev1.ResourceQuotaList{Items: []corev1.ResourceQuota{q}}, nil
	})
	c.PrependReactor("get", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		ga := a.(k8stesting.GetAction)
		obj, err := c.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), ga.GetNamespace(), ga.GetName())
		if err != nil {
			return true, nil, err
		}
		p := obj.(*corev1.Pod).DeepCopy()
		p.Spec.NodeName = p.Spec.NodeSelector[corev1.LabelHostname]
		p.Status.PodIP = "10.1.0.1"
		p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
		cs := corev1.ContainerStatus{RestartCount: 0}
		if strings.HasSuffix(p.Name, "neighbour-server") {
			s.mu.Lock()
			cs.RestartCount = s.restarts
			s.mu.Unlock()
		}
		if strings.HasSuffix(p.Name, "memory-hog") && s.memEnd != "" {
			cs.State.Terminated = &corev1.ContainerStateTerminated{Reason: s.memEnd, ExitCode: 137}
		}
		p.Status.ContainerStatuses = []corev1.ContainerStatus{cs}
		return true, p, nil
	})
	s.start = time.Now()
	Exec = func(_ context.Context, _ *runner.Env, _, _ string, cmd []string) (string, error) {
		if cmd[0] == "cat" {
			used := int64(time.Since(s.start).Seconds() * s.hogCPUs * 1e6)
			throttled := int64(0)
			if s.throttled {
				throttled = used / 1000
			}
			return fmt.Sprintf("usage_usec %d\nuser_usec %d\nnr_throttled %d\n", used, used, throttled), nil
		}
		return "ok", nil
	}
	return &runner.Env{
		Cluster: &kube.Cluster{Client: c},
		Tenants: tenants,
		Sandbox: sandbox.New(sandbox.Live),
		Impersonate: func(id kube.Identity) (*kube.Cluster, error) {
			return &kube.Cluster{Client: c, As: &id}, nil
		},
	}
}

func statuses(t *testing.T, env *runner.Env, ids ...string) map[string]runner.Status {
	t.Helper()
	got := runNet(t, env, ids...)
	out := map[string]runner.Status{}
	for id, rs := range got {
		out[id] = runner.Pass
		for _, r := range rs {
			if r.Status != runner.Pass {
				out[id] = r.Status
				t.Logf("%s %s %s: %s", id, r.Subject, r.Status, r.Message)
			}
		}
	}
	return out
}

var resIDs = []string{"RES-01", "RES-02", "RES-03", "RES-04", "RES-05"}

func TestHeldHogsPassEveryNoisyNeighbourCheck(t *testing.T) {
	s := &activeSim{hogCPUs: 0.5, throttled: true, memEnd: "OOMKilled", podLimit: 10, defaultCPU: "50m"}
	for id, st := range statuses(t, s.env(t), resIDs...) {
		if st != runner.Pass {
			t.Errorf("%s: %s", id, st)
		}
	}
}

func TestBrokenLimitsFailTheirChecks(t *testing.T) {
	cases := []struct {
		name string
		sim  *activeSim
		want map[string]runner.Status
	}{
		{"no CPU limit", &activeSim{hogCPUs: 2, memEnd: "OOMKilled", podLimit: 10}, map[string]runner.Status{"RES-01": runner.Fail, "RES-02": runner.Fail, "RES-03": runner.Pass}},
		{"hog starved by a busy node", &activeSim{hogCPUs: 0.1, throttled: true, memEnd: "OOMKilled", podLimit: 10}, map[string]runner.Status{"RES-01": runner.Error}},
		{"memory hog not killed at its limit", &activeSim{hogCPUs: 0.5, throttled: true, memEnd: "Error", podLimit: 10}, map[string]runner.Status{"RES-03": runner.Fail, "RES-01": runner.Pass}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := statuses(t, c.sim.env(t), resIDs...)
			for id, want := range c.want {
				if got[id] != want {
					t.Errorf("%s: %s, want %s", id, got[id], want)
				}
			}
		})
	}
}

func TestQuotaChecksWithRealPods(t *testing.T) {
	s := &activeSim{podLimit: 10, defaultCPU: "50m"}
	got := statuses(t, s.env(t), "QTA-01", "QTA-02", "QTA-08", "QTA-09")
	for _, id := range []string{"QTA-01", "QTA-02", "QTA-08", "QTA-09"} {
		if got[id] != runner.Pass {
			t.Errorf("%s: %s", id, got[id])
		}
	}
}

func TestDefaultsThatDoNotFitTheQuotaFailQTA08(t *testing.T) {
	// 10 pods of 100m do not fit in 500m of CPU requests.
	s := &activeSim{podLimit: 10, defaultCPU: "100m"}
	got := statuses(t, s.env(t), "QTA-08", "QTA-09")
	if got["QTA-08"] != runner.Fail {
		t.Errorf("QTA-08: %s, want fail", got["QTA-08"])
	}
	if got["QTA-09"] != runner.Skipped && got["QTA-09"] != runner.Inconclusive {
		t.Errorf("QTA-09: %s, want skipped or inconclusive", got["QTA-09"])
	}
}

func TestALargeFreePodCountIsSkipped(t *testing.T) {
	s := &activeSim{podLimit: int64(MaxQuotaPods + 5), defaultCPU: "1m"}
	if got := statuses(t, s.env(t), "QTA-08")["QTA-08"]; got != runner.Skipped {
		t.Errorf("QTA-08: %s, want skipped", got)
	}
}

func TestCPUStat(t *testing.T) {
	stat := "usage_usec 1500\nuser_usec 1000\nnr_throttled 7\n"
	if cpuStat(stat, "usage_usec") != 1500 || cpuStat(stat, "nr_throttled") != 7 || cpuStat(stat, "missing") != -1 {
		t.Error("cpuStat")
	}
}
