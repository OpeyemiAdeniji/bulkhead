package checks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

// ReadyTimeout is how long a test pod may take to become ready.
var ReadyTimeout = 3 * time.Minute

// smallResources are the resources of the checker's network test pods.
var smallResources = corev1.ResourceRequirements{
	Requests: corev1.ResourceList{corev1.ResourceCPU: quantity("50m"), corev1.ResourceMemory: quantity("32Mi")},
	Limits:   corev1.ResourceList{corev1.ResourceCPU: quantity("100m"), corev1.ResourceMemory: quantity("64Mi")},
}

// Commands of the test pods. The server answers "ok" on port 8080.
var (
	serverCommand = []string{"sh", "-c", "echo ok > /tmp/index.html && exec httpd -f -p 8080 -h /tmp"}
	sleepCommand  = []string{"sleep", "3600"}
)

// Exec runs a command in a pod with the user's own identity and returns its output (stdout and
// stderr together). A command that exits with a non-zero code returns an error. Tests replace it.
var Exec = func(ctx context.Context, env *runner.Env, ns, pod string, command []string) (string, error) {
	req := env.Cluster.Client.CoreV1().RESTClient().Post().Resource("pods").Namespace(ns).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{Command: command, Stdout: true, Stderr: true}, scheme.ParameterCodec)
	spdy, err := remotecommand.NewSPDYExecutor(env.Cluster.Config, "POST", req.URL())
	if err != nil {
		return "", err
	}
	ws, err := remotecommand.NewWebSocketExecutor(env.Cluster.Config, "GET", req.URL().String())
	if err != nil {
		return "", err
	}
	exec, err := remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return "", err
	}
	// stdout and stderr arrive on two streams at the same time, so the shared buffer is locked.
	out := &lockedBuffer{}
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: out, Stderr: out})
	return strings.TrimSpace(out.String()), err
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// execIn runs a command in a test pod through the sandbox.
func execIn(ctx context.Context, env *runner.Env, checkID, ns, pod string, command ...string) (string, error) {
	var out string
	err := do(ctx, env, sandbox.Request{Verb: "exec", Kind: "pod", Namespace: ns, Name: pod, As: you, Check: checkID}, func(ctx context.Context) error {
		var err error
		out, err = Exec(ctx, env, ns, pod, command)
		return err
	})
	return out, err
}

// schedulableNodes returns the names of ready nodes that accept ordinary pods, sorted.
func schedulableNodes(ctx context.Context, env *runner.Env, checkID string) ([]string, error) {
	var names []string
	err := do(ctx, env, sandbox.Request{Verb: "list", Kind: "nodes", As: you, Check: checkID}, func(ctx context.Context) error {
		l, err := env.Cluster.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for _, n := range l.Items {
			if usable(n) {
				names = append(names, n.Name)
			}
		}
		return nil
	})
	sort.Strings(names)
	return names, err
}

func usable(n corev1.Node) bool {
	if n.Spec.Unschedulable {
		return false
	}
	for _, t := range n.Spec.Taints {
		if t.Effect == corev1.TaintEffectNoSchedule || t.Effect == corev1.TaintEffectNoExecute {
			return false
		}
	}
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// testPod is a pod the checker started, once it is ready.
type testPod struct {
	Name, Namespace, Node, IP string
}

// startPod creates a test pod as the tenant admin, on the given node (any node when empty).
// readyPort, when not 0, is a TCP port that must accept connections before the pod counts as ready.
// waitReady waits for it.
func startPod(ctx context.Context, env *runner.Env, checkID string, t tenant.Tenant, purpose, node string, command []string, res corev1.ResourceRequirements, restart corev1.RestartPolicy, readyPort int32) (testPod, error) {
	p := restrictedPod(t.Namespace, "")
	env.Sandbox.Prepare(&p.ObjectMeta, purpose)
	p.Spec.Containers[0].Command = command
	p.Spec.Containers[0].Resources = res
	p.Spec.RestartPolicy = restart
	if readyPort > 0 {
		// Ready must mean listening, or the first probes can fail before the server has started.
		p.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
			ProbeHandler:  corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(readyPort)}},
			PeriodSeconds: 1,
		}
	}
	if node != "" {
		p.Spec.NodeSelector = map[string]string{corev1.LabelHostname: node}
	}
	tp := testPod{Name: p.Name, Namespace: t.Namespace, Node: node}
	err := env.Sandbox.Create(ctx, p, "pod", t.Admin.String(), checkID, func(ctx context.Context) error {
		cl, err := client(env, t.Admin)
		if err != nil {
			return err
		}
		_, err = cl.CoreV1().Pods(t.Namespace).Create(ctx, p, metav1.CreateOptions{})
		return err
	})
	if err != nil {
		return tp, fmt.Errorf("create test pod %s in %s: %w", p.Name, t.Namespace, err)
	}
	return tp, nil
}

// waitReady waits until every pod is ready, and fills in their IP addresses and nodes.
func waitReady(ctx context.Context, env *runner.Env, checkID string, pods []*testPod) error {
	if len(pods) == 0 {
		return nil
	}
	r := sandbox.Request{Verb: "watch", Kind: "pods", Namespace: pods[0].Namespace, Name: fmt.Sprintf("%d test pods until ready", len(pods)), As: you, Check: checkID}
	return do(ctx, env, r, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, ReadyTimeout)
		defer cancel()
		for {
			pending := 0
			for _, tp := range pods {
				if tp.IP != "" {
					continue
				}
				p, err := env.Cluster.Client.CoreV1().Pods(tp.Namespace).Get(ctx, tp.Name, metav1.GetOptions{})
				if err != nil && !apierrors.IsNotFound(err) {
					return err
				}
				if err == nil && ready(p) {
					tp.IP, tp.Node = p.Status.PodIP, p.Spec.NodeName
					continue
				}
				if err == nil {
					if why := stuck(p); why != "" {
						return fmt.Errorf("test pod %s/%s cannot start: %s", tp.Namespace, tp.Name, why)
					}
				}
				pending++
			}
			if pending == 0 {
				return nil
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("%d test pods were not ready after %s: %w", pending, ReadyTimeout, ctx.Err())
			case <-time.After(time.Second):
			}
		}
	})
}

func ready(p *corev1.Pod) bool {
	if p.Status.PodIP == "" {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// stuck returns why a pod will not start by itself, or "" while it may still start.
func stuck(p *corev1.Pod) string {
	for _, s := range p.Status.ContainerStatuses {
		if w := s.State.Waiting; w != nil {
			switch w.Reason {
			case "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "CreateContainerConfigError":
				return w.Reason + ": " + w.Message
			}
		}
	}
	return ""
}

// state holds the test pods of one run, shared by the checks of a category.
type state struct {
	mu     sync.Mutex
	net    *netFixture
	netErr error
}

var states sync.Map // *sandbox.Sandbox -> *state

func stateOf(env *runner.Env) *state {
	s, _ := states.LoadOrStore(env.Sandbox, &state{})
	return s.(*state)
}

// ForgetRun drops the shared state of a run. The CLI calls it when the run ends.
func ForgetRun(sb *sandbox.Sandbox) { states.Delete(sb) }

// errNeedsTwoNodes is returned by a fixture when a check needs two schedulable nodes.
var errNeedsTwoNodes = errors.New("needs two schedulable nodes")
