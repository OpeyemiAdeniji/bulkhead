package checks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	"github.com/OpeyemiAdeniji/bulkhead/internal/flows"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
)

// Probes from inside workloads (ADR 0020): an ephemeral container is added to a running pod
// that matches the flow's via labels, and the probe runs in it, with the pod's network and
// labels. Kubernetes cannot remove an ephemeral container, so every probed pod is reported.

// NoProbeAnnotation lets a team exclude a workload from probes: bulkhead.io/probe: "false".
const NoProbeAnnotation = "bulkhead.io/probe"

// WorkloadProbeLife is the longest an ephemeral probe container waits before it exits by
// itself, in seconds, in case the run is killed before it can tell it to stop.
const WorkloadProbeLife = 600

// doneFile is created in the probe container when the run is done with it.
const doneFile = "/tmp/bhc-done"

// waitCommand keeps the probe container alive until the run is done, or for its life at most.
var waitCommand = []string{"sh", "-c", fmt.Sprintf(`i=0; while [ ! -f %s ] && [ "$i" -lt %d ]; do sleep 1; i=$((i + 1)); done`, doneFile, WorkloadProbeLife)}

// workloadTarget is a pod that runs a probe container for the flows of one selector.
type workloadTarget struct {
	ns, pod, container string
	// added is true once the container is in the pod's spec, where it stays.
	added bool
	err   error
}

// workload returns the probe container for a namespace and selector, and adds it on first use.
func workload(ctx context.Context, env *runner.Env, checkID, ns, selector string) *workloadTarget {
	s := stateOf(env)
	key := ns + " " + selector
	s.mu.Lock()
	if w, ok := s.workloads[key]; ok {
		s.mu.Unlock()
		return w
	}
	s.mu.Unlock()
	w := startWorkloadProbe(ctx, env, checkID, ns, selector)
	s.mu.Lock()
	if s.workloads == nil {
		s.workloads = map[string]*workloadTarget{}
	}
	s.workloads[key] = w
	s.mu.Unlock()
	return w
}

func startWorkloadProbe(ctx context.Context, env *runner.Env, checkID, ns, selector string) *workloadTarget {
	pod, err := pickWorkload(ctx, env, checkID, ns, selector)
	if err != nil {
		return &workloadTarget{err: err}
	}
	w := &workloadTarget{ns: ns, pod: pod, container: sandbox.NamePrefix + env.Sandbox.RunID}
	req := sandbox.Request{Verb: "add ephemeral container", Kind: "pod", Namespace: ns, Name: pod + "/" + w.container, As: you, Stores: true, Check: checkID}
	err = do(ctx, env, req, func(ctx context.Context) error {
		pods := env.Cluster.Client.CoreV1().Pods(ns)
		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			p, err := pods.Get(ctx, pod, metav1.GetOptions{})
			if err != nil {
				return err
			}
			p.Spec.EphemeralContainers = append(p.Spec.EphemeralContainers, probeContainer(w.container))
			_, err = pods.UpdateEphemeralContainers(ctx, pod, p, metav1.UpdateOptions{FieldManager: sandbox.ManagedBy})
			return err
		})
	})
	if err != nil {
		w.err = fmt.Errorf("add a probe container to %s/%s: %w", ns, pod, err)
		if errors.Is(err, sandbox.ErrNotSent) {
			w.err = err
		}
		return w
	}
	w.added = true
	if err := waitProbeRunning(ctx, env, ns, pod, w.container); err != nil {
		w.err = err
	}
	return w
}

// probeContainer is the ephemeral container: the checker's probe image, with the restricted
// security context of Pod Security, so it is allowed in any tenant namespace.
func probeContainer(name string) corev1.EphemeralContainer {
	no, yes, nobody := false, true, int64(65534)
	return corev1.EphemeralContainer{EphemeralContainerCommon: corev1.EphemeralContainerCommon{
		Name:            name,
		Image:           ProbeImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         waitCommand,
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:             &yes,
			RunAsUser:                &nobody,
			RunAsGroup:               &nobody,
			AllowPrivilegeEscalation: &no,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}}
}

// pickWorkload returns the first pod by name that matches the selector and may be probed.
func pickWorkload(ctx context.Context, env *runner.Env, checkID, ns, selector string) (string, error) {
	var names []string
	var excluded []string
	err := do(ctx, env, sandbox.Request{Verb: "list", Kind: "pods", Namespace: ns, Name: selector, As: you, Check: checkID}, func(ctx context.Context) error {
		l, err := env.Cluster.Client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return err
		}
		for _, p := range l.Items {
			if why := notProbeable(&p); why != "" {
				excluded = append(excluded, p.Name+" ("+why+")")
				continue
			}
			names = append(names, p.Name)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		msg := fmt.Sprintf("no running, ready pod in %s matches %s", ns, selector)
		if len(excluded) > 0 {
			msg += "; not used: " + strings.Join(excluded, ", ")
		}
		return "", errors.New(msg)
	}
	slices.Sort(names)
	return names[0], nil
}

// notProbeable says why a pod must not receive a probe container, or "".
func notProbeable(p *corev1.Pod) string {
	switch {
	case p.DeletionTimestamp != nil:
		return "terminating"
	case p.Labels[sandbox.LabelManagedBy] == sandbox.ManagedBy:
		return "a test pod of the checker"
	case p.Annotations[NoProbeAnnotation] == "false":
		return NoProbeAnnotation + ": \"false\""
	case p.Spec.HostNetwork:
		// Its traffic is the node's, which pod network policies do not cover.
		return "uses the host network"
	case p.Status.Phase != corev1.PodRunning || !podReady(p):
		return "not running and ready"
	}
	return ""
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// waitProbeRunning waits until the probe container runs, or fails with the reason it does not.
func waitProbeRunning(ctx context.Context, env *runner.Env, ns, pod, container string) error {
	ctx, cancel := context.WithTimeout(ctx, ReadyTimeout)
	defer cancel()
	for {
		p, err := env.Cluster.Client.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("wait for the probe container in %s/%s: %w", ns, pod, err)
		}
		for _, st := range p.Status.EphemeralContainerStatuses {
			if st.Name != container {
				continue
			}
			switch {
			case st.State.Running != nil:
				return nil
			case st.State.Terminated != nil:
				return fmt.Errorf("the probe container in %s/%s stopped: %s", ns, pod, st.State.Terminated.Reason)
			case st.State.Waiting != nil && st.State.Waiting.Reason != "" && st.State.Waiting.Reason != "ContainerCreating" && st.State.Waiting.Reason != "PodInitializing":
				return fmt.Errorf("the probe container in %s/%s does not start: %s %s", ns, pod, st.State.Waiting.Reason, st.State.Waiting.Message)
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the probe container in %s/%s did not start within %s", ns, pod, ReadyTimeout)
		case <-time.After(time.Second):
		}
	}
}

// probeWorkload probes a flow from inside the workload that its via labels select.
func probeWorkload(ctx context.Context, env *runner.Env, checkID string, fl flows.Flow) probeOutcome {
	w := workload(ctx, env, checkID, fl.From, fl.Selector())
	if errors.Is(w.err, sandbox.ErrNotSent) {
		return probeOutcome{kind: planned}
	}
	if w.err != nil {
		return probeOutcome{kind: probeErr, detail: w.err.Error()}
	}
	var out string
	err := do(ctx, env, sandbox.Request{Verb: "exec", Kind: "pod", Namespace: fl.From, Name: w.pod + "/" + w.container, As: you, Check: checkID}, func(ctx context.Context) error {
		var err error
		out, err = ExecContainer(ctx, env, fl.From, w.pod, w.container, probeCommand(fl))
		return err
	})
	o := classify(out, err)
	o.from = fl.From + "/" + w.pod
	return o
}

// FinishWorkloadProbes tells every probe container of the run to exit, and returns the pods
// that received one, as namespace/pod (container), sorted. The CLI calls it when the run ends,
// with a fresh context, so it also runs after Ctrl+C.
func FinishWorkloadProbes(ctx context.Context, env *runner.Env) []string {
	s := stateOf(env)
	s.mu.Lock()
	var added []*workloadTarget
	for _, w := range s.workloads {
		if w.added {
			added = append(added, w)
		}
	}
	s.mu.Unlock()
	var out []string
	for _, w := range added {
		// Best effort: if it fails, the container exits by itself after WorkloadProbeLife.
		if w.err == nil {
			_, _ = ExecContainer(ctx, env, w.ns, w.pod, w.container, []string{"touch", doneFile})
		}
		out = append(out, fmt.Sprintf("%s/%s (%s)", w.ns, w.pod, w.container))
	}
	slices.Sort(out)
	return out
}

// PreflightWorkloadProbes checks, before any check runs, that the person running the checker
// may list pods, add ephemeral containers and exec in each namespace. The subresources are
// named explicitly: "kubectl auth can-i update pods/ephemeralcontainers" reads
// "ephemeralcontainers" as a pod name and can answer yes for a request that is refused.
func PreflightWorkloadProbes(ctx context.Context, client kubernetes.Interface, namespaces []string) error {
	var denied []string
	for _, ns := range namespaces {
		for _, a := range []struct{ verb, sub string }{{"list", ""}, {"update", "ephemeralcontainers"}, {"create", "exec"}} {
			review := &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: ns, Verb: a.verb, Resource: "pods", Subresource: a.sub},
			}}
			got, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("ask the API server whether you may %s pods in %s: %w", a.verb, ns, err)
			}
			if !got.Status.Allowed {
				what := "pods"
				if a.sub != "" {
					what += "/" + a.sub
				}
				denied = append(denied, fmt.Sprintf("%s %s in %s", a.verb, what, ns))
			}
		}
	}
	if len(denied) > 0 {
		return fmt.Errorf("--probe-workloads needs these permissions, which you do not have: %s", strings.Join(denied, ", "))
	}
	return nil
}
