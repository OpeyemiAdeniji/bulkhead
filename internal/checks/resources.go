package checks

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

// LayerResourceLimits names the kernel enforcing a container's limits (cgroups).
const LayerResourceLimits = "resource-limits"

// Timing of the noisy-neighbour checks. Tests shorten them.
var (
	HogWarmUp      = 5 * time.Second
	HogMeasure     = 10 * time.Second
	OOMWaitTimeout = time.Minute
)

// The hogs. The CPU hog spins on two threads under a limit of 500m; the memory hog allocates
// without end under a limit of 64Mi.
var (
	cpuHogCommand = []string{"sh", "-c", "for i in 1 2; do (while :; do :; done) & done; wait"}
	memHogCommand = []string{"sh", "-c", "tail /dev/zero"}
	cpuHogLimit   = quantity("500m")
	cpuHogRes     = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: quantity("100m"), corev1.ResourceMemory: quantity("32Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: cpuHogLimit, corev1.ResourceMemory: quantity("64Mi")},
	}
)

func init() {
	runner.Register("RES-01", resCheck(res01))
	runner.Register("RES-02", resCheck(res02))
	runner.Register("RES-03", resCheck(res03))
	runner.Register("RES-04", resCheck(res04))
	runner.Register("RES-05", resCheck(res05))
}

// resFixture: in the first tenant, a CPU hog and a memory hog on one node; in the second tenant,
// a server on the same node and a client. The CPU hog is measured once, for RES-01 and RES-02.
type resFixture struct {
	hogs, neighbour  tenant.Tenant
	hasNeighbour     bool
	cpuHog, memHog   testPod
	server, client   testPod
	restartsBefore   int32
	usedCPUs         float64
	throttledPeriods int64
}

func resCheck(f func(ctx context.Context, env *runner.Env, c catalog.Check, x *resFixture) runner.Result) runner.Func {
	return func(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
		x, err := resources(ctx, env, c.ID)
		if err != nil {
			return fixtureFailed(err)
		}
		return []runner.Result{f(ctx, env, c, x)}
	}
}

// resources returns the noisy-neighbour fixture of the run, and starts it on first use.
func resources(ctx context.Context, env *runner.Env, checkID string) (*resFixture, error) {
	s := stateOf(env)
	s.mu.Lock()
	if s.res != nil || s.resErr != nil {
		defer s.mu.Unlock()
		return s.res, s.resErr
	}
	s.mu.Unlock()
	// The network pods would take room on the node and in the quota.
	if err := releasePods(ctx, env, checkID); err != nil && !errors.Is(err, sandbox.ErrNotSent) {
		return nil, err
	}
	x, err := startResources(ctx, env, checkID)
	s.mu.Lock()
	s.res, s.resErr = x, err
	s.mu.Unlock()
	return x, err
}

func startResources(ctx context.Context, env *runner.Env, checkID string) (*resFixture, error) {
	notSent := func(err error) bool { return errors.Is(err, sandbox.ErrNotSent) }
	nodes, err := schedulableNodes(ctx, env, checkID)
	if err != nil && !notSent(err) {
		return nil, err
	}
	node := ""
	if len(nodes) > 0 {
		node = nodes[0]
	}
	x := &resFixture{hogs: env.Tenants[0]}
	if len(env.Tenants) > 1 {
		x.neighbour, x.hasNeighbour = env.Tenants[1], true
	}
	if x.cpuHog, err = startPod(ctx, env, checkID, x.hogs, "cpu-hog", node, cpuHogCommand, cpuHogRes, corev1.RestartPolicyAlways, 0); err != nil && !notSent(err) {
		return nil, err
	}
	if x.memHog, err = startPod(ctx, env, checkID, x.hogs, "memory-hog", node, memHogCommand, smallResources, corev1.RestartPolicyNever, 0); err != nil && !notSent(err) {
		return nil, err
	}
	wait := []*testPod{&x.cpuHog}
	if x.hasNeighbour {
		if x.server, err = startPod(ctx, env, checkID, x.neighbour, "neighbour-server", node, serverCommand, smallResources, corev1.RestartPolicyAlways, 8080); err != nil && !notSent(err) {
			return nil, err
		}
		if x.client, err = startPod(ctx, env, checkID, x.neighbour, "neighbour-client", "", sleepCommand, smallResources, corev1.RestartPolicyAlways, 0); err != nil && !notSent(err) {
			return nil, err
		}
		wait = append(wait, &x.server, &x.client)
	}
	// The memory hog is not waited for: it may be killed before it ever counts as ready.
	if err := waitReady(ctx, env, checkID, wait); err != nil {
		return nil, err
	}
	if x.hasNeighbour {
		if x.restartsBefore, err = restarts(ctx, env, checkID, x.server); err != nil {
			return nil, err
		}
	}
	if err := sleep(ctx, HogWarmUp); err != nil {
		return nil, err
	}
	s0, err := execIn(ctx, env, checkID, x.cpuHog.Namespace, x.cpuHog.Name, "cat", "/sys/fs/cgroup/cpu.stat")
	if err != nil {
		return nil, fmt.Errorf("read the CPU hog's cpu.stat (cgroup v2 is needed): %s", firstLine(s0, err))
	}
	t0 := time.Now()
	if err := sleep(ctx, HogMeasure); err != nil {
		return nil, err
	}
	s1, err := execIn(ctx, env, checkID, x.cpuHog.Namespace, x.cpuHog.Name, "cat", "/sys/fs/cgroup/cpu.stat")
	if err != nil {
		return nil, fmt.Errorf("read the CPU hog's cpu.stat: %s", firstLine(s1, err))
	}
	elapsed := time.Since(t0).Seconds()
	u0, u1 := cpuStat(s0, "usage_usec"), cpuStat(s1, "usage_usec")
	if u0 < 0 || u1 < 0 {
		return nil, errors.New("cpu.stat of the CPU hog has no usage_usec (cgroup v2 is needed)")
	}
	x.usedCPUs = float64(u1-u0) / 1e6 / elapsed
	x.throttledPeriods = cpuStat(s1, "nr_throttled") - cpuStat(s0, "nr_throttled")
	return x, nil
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// cpuStat returns a counter of cpu.stat, or -1 when it is missing.
func cpuStat(stat, key string) int64 {
	for _, line := range strings.Split(stat, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == key {
			if v, err := strconv.ParseInt(f[1], 10, 64); err == nil {
				return v
			}
		}
	}
	return -1
}

func restarts(ctx context.Context, env *runner.Env, checkID string, tp testPod) (int32, error) {
	var n int32
	err := do(ctx, env, sandbox.Request{Verb: "get", Kind: "pod", Namespace: tp.Namespace, Name: tp.Name, As: you, Check: checkID}, func(ctx context.Context) error {
		p, err := env.Cluster.Client.CoreV1().Pods(tp.Namespace).Get(ctx, tp.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		for _, s := range p.Status.ContainerStatuses {
			n += s.RestartCount
		}
		return nil
	})
	return n, err
}

func res01(_ context.Context, _ *runner.Env, _ catalog.Check, x *resFixture) runner.Result {
	limit := float64(cpuHogLimit.MilliValue()) / 1000
	subject := fmt.Sprintf("%s, %s", x.hogs.Namespace, x.cpuHog.Name)
	msg := fmt.Sprintf("the hog used %.2f CPUs over %s, against a limit of %.2f", x.usedCPUs, HogMeasure, limit)
	switch {
	case x.usedCPUs > limit*1.15:
		return runner.Result{Status: runner.Fail, Subject: subject, Message: msg + ": above its limit"}
	case x.usedCPUs < limit*0.6:
		return runner.Result{Status: runner.Error, Subject: subject, Message: msg + ": too little to test the limit, the node may be too busy"}
	}
	return runner.Result{Status: runner.Pass, Subject: subject, Layer: LayerResourceLimits, Message: msg}
}

func res02(_ context.Context, _ *runner.Env, _ catalog.Check, x *resFixture) runner.Result {
	subject := fmt.Sprintf("%s, %s", x.hogs.Namespace, x.cpuHog.Name)
	if x.throttledPeriods > 0 {
		return runner.Result{Status: runner.Pass, Subject: subject, Layer: LayerResourceLimits, Message: fmt.Sprintf("%d throttled periods while measured", x.throttledPeriods)}
	}
	return runner.Result{Status: runner.Fail, Subject: subject, Message: "the kernel did not throttle the hog (nr_throttled did not grow)"}
}

func res03(ctx context.Context, env *runner.Env, c catalog.Check, x *resFixture) runner.Result {
	subject := fmt.Sprintf("%s, %s", x.hogs.Namespace, x.memHog.Name)
	var reason string
	var code int32
	err := do(ctx, env, sandbox.Request{Verb: "watch", Kind: "pod", Namespace: x.memHog.Namespace, Name: x.memHog.Name, As: you, Check: c.ID}, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, OOMWaitTimeout)
		defer cancel()
		for {
			p, err := env.Cluster.Client.CoreV1().Pods(x.memHog.Namespace).Get(ctx, x.memHog.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			for _, s := range p.Status.ContainerStatuses {
				for _, t := range []*corev1.ContainerStateTerminated{s.State.Terminated, s.LastTerminationState.Terminated} {
					if t != nil {
						reason, code = t.Reason, t.ExitCode
						return nil
					}
				}
			}
			if err := sleep(ctx, time.Second); err != nil {
				return fmt.Errorf("the memory hog was still running after %s", OOMWaitTimeout)
			}
		}
	})
	switch {
	case err != nil:
		return errResult(subject, err)
	case reason == "OOMKilled":
		return runner.Result{Status: runner.Pass, Subject: subject, Layer: LayerResourceLimits, Message: fmt.Sprintf("killed at its memory limit (OOMKilled, exit code %d)", code)}
	}
	return runner.Result{Status: runner.Fail, Subject: subject, Message: fmt.Sprintf("it ended with %q (exit code %d) instead of being killed at its limit", reason, code)}
}

func res04(ctx context.Context, env *runner.Env, c catalog.Check, x *resFixture) runner.Result {
	if !x.hasNeighbour {
		return needTwo[0]
	}
	subject := fmt.Sprintf("%s, %s next to the hogs", x.neighbour.Namespace, x.server.Name)
	out, err := fetch(ctx, env, c.ID, x.client, x.server)
	return netResult(c, subject, out, err)
}

func res05(ctx context.Context, env *runner.Env, c catalog.Check, x *resFixture) runner.Result {
	if !x.hasNeighbour {
		return needTwo[0]
	}
	subject := fmt.Sprintf("%s, %s next to the hogs", x.neighbour.Namespace, x.server.Name)
	n, err := restarts(ctx, env, c.ID, x.server)
	switch {
	case err != nil:
		return errResult(subject, err)
	case n != x.restartsBefore:
		return runner.Result{Status: runner.Fail, Subject: subject, Message: fmt.Sprintf("it restarted %d times while the hogs ran", n-x.restartsBefore)}
	}
	return runner.Result{Status: runner.Pass, Subject: subject, Message: "no restart while the hogs ran"}
}
