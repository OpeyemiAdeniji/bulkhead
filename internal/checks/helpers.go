package checks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

// ProbeImage is the image of the checker's test pods. Its tag is pinned, because many platforms
// refuse images without one.
const ProbeImage = "busybox:1.36"

// absentName is a name that no real object has. A read of it tells allowed ("not found")
// from refused ("forbidden") without reading any real data.
const absentName = sandbox.NamePrefix + "absent-probe"

var (
	dryRun = []string{metav1.DryRunAll}
	// You is how requests made with the user's own identity are labelled.
	you = "you"
)

// client returns a client that acts as the identity.
func client(env *runner.Env, id kube.Identity) (kubernetes.Interface, error) {
	c, err := env.Impersonate(id)
	if err != nil {
		return nil, err
	}
	return c.Client, nil
}

// pairs returns every ordered pair of different tenants.
func pairs(ts []tenant.Tenant) [][2]tenant.Tenant {
	var out [][2]tenant.Tenant
	for _, a := range ts {
		for _, b := range ts {
			if a.Namespace != b.Namespace {
				out = append(out, [2]tenant.Tenant{a, b})
			}
		}
	}
	return out
}

func between(a, b tenant.Tenant) string { return a.Namespace + " -> " + b.Namespace }

var needTwo = []runner.Result{{Status: runner.Skipped, Message: "needs two or more tenants (--tenant)"}}

// allowed judges a request that must work.
func allowed(subject string, err error) runner.Result {
	if err == nil {
		return runner.Result{Status: runner.Pass, Subject: subject}
	}
	if r, ok := Refused(err); ok {
		return runner.Result{Status: runner.Fail, Subject: subject, Layer: r.Layer,
			Message: fmt.Sprintf("refused by %s, but it must work: %s", r, short(err))}
	}
	return errResult(subject, err)
}

// blocked judges a request that must be refused. expected lists the layers that should refuse it;
// a refusal by another layer still passes, and the report says so.
func blocked(subject string, err error, expected ...string) runner.Result {
	if err == nil {
		return runner.Result{Status: runner.Fail, Subject: subject, Message: "the request was accepted, but it must be refused"}
	}
	r, ok := Refused(err)
	if !ok {
		return errResult(subject, err)
	}
	res := runner.Result{Status: runner.Pass, Subject: subject, Layer: r.Layer, Message: "refused by " + r.String()}
	if len(expected) > 0 && !slices.Contains(expected, r.Layer) {
		res.Message += fmt.Sprintf("; expected %s, so check that this is the protection you rely on", strings.Join(expected, " or "))
	}
	return res
}

func errResult(subject string, err error) runner.Result {
	if errors.Is(err, sandbox.ErrNotSent) {
		return runner.Result{Status: runner.Planned, Subject: subject}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return runner.Result{Status: runner.Error, Subject: subject, Message: "timed out: " + short(err)}
	}
	return runner.Result{Status: runner.Error, Subject: subject, Message: short(err)}
}

// short keeps the first line of an error, without the long admission details.
func short(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 240 {
		s = s[:240] + "..."
	}
	return s
}

func isNotFound(err error) bool { return apierrors.IsNotFound(err) }

// restrictedPod returns a pod that meets the restricted Pod Security level, without resources,
// so the namespace's limit range fills them in.
func restrictedPod(ns, name string) *corev1.Pod {
	no, yes := false, true
	uid := int64(65534)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken: &no,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   &yes,
				RunAsUser:      &uid,
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:    "probe",
				Image:   ProbeImage,
				Command: []string{"sleep", "3600"},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &no,
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
			}},
		},
	}
}

// do sends a request through the sandbox.
func do(ctx context.Context, env *runner.Env, r sandbox.Request, fn func(context.Context) error) error {
	return env.Sandbox.Do(ctx, r, fn)
}

func quantity(s string) resource.Quantity { return resource.MustParse(s) }
