// Package checks holds the implementations of the checks in the catalogue. Each file registers
// its checks with the runner when the package is imported.
package checks

import (
	"errors"
	"regexp"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// The layers that can refuse a request. The report names the one that decided.
const (
	LayerRBAC        = "rbac"
	LayerPodSecurity = "pod-security"
	LayerAdmission   = "admission"
	LayerQuota       = "quota"
	LayerLimitRange  = "limit-range"
	LayerUnknown     = "unknown"
)

var (
	policyName   = regexp.MustCompile(`(?i)\bpolicy[: ]+"?([a-z0-9][-a-z0-9.]*)"?`)
	webhookName  = regexp.MustCompile(`admission webhook "([^"]+)" denied the request`)
	vapName      = regexp.MustCompile(`ValidatingAdmissionPolicy '([^']+)'`)
	rbacMessage  = regexp.MustCompile(`cannot [a-z]+ resource "`)
	limitMessage = regexp.MustCompile(`(?i)(maximum|minimum) (cpu|memory|ephemeral-storage) usage per (Container|Pod)`)
)

// Refusal describes why the API server refused a request.
type Refusal struct {
	Layer string
	// Detail names the policy or object that refused, when the message says so.
	Detail string
}

// String returns the layer and the detail, for example "admission (protect-platform-objects)".
func (r Refusal) String() string {
	if r.Detail == "" {
		return r.Layer
	}
	return r.Layer + " (" + r.Detail + ")"
}

// Refused reports whether err is a refusal of the request by the API server (403 Forbidden,
// or 422 Invalid from admission), and which layer refused it. Other errors, such as a
// timeout or "not found", are not refusals.
func Refused(err error) (Refusal, bool) {
	if err == nil {
		return Refusal{}, false
	}
	var status apierrors.APIStatus
	if !errors.As(err, &status) {
		return Refusal{}, false
	}
	msg := status.Status().Message
	// Admission webhooks choose their own status code: Kyverno, for example, answers 400.
	// A denial by a webhook or an admission policy is a refusal, whatever the code.
	if webhookName.MatchString(msg) || vapName.MatchString(msg) {
		return Classify(msg), true
	}
	if !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err) {
		return Refusal{}, false
	}
	return Classify(msg), true
}

// Classify finds the layer in a refusal message of the API server.
func Classify(msg string) Refusal {
	switch {
	case strings.Contains(msg, "violates PodSecurity"):
		return Refusal{Layer: LayerPodSecurity}
	case strings.Contains(msg, "exceeded quota"):
		r := Refusal{Layer: LayerQuota}
		if i := strings.Index(msg, "exceeded quota: "); i >= 0 {
			r.Detail = strings.SplitN(msg[i+len("exceeded quota: "):], ",", 2)[0]
		}
		return r
	case limitMessage.MatchString(msg):
		return Refusal{Layer: LayerLimitRange}
	case vapName.MatchString(msg):
		return Refusal{Layer: LayerAdmission, Detail: vapName.FindStringSubmatch(msg)[1]}
	case webhookName.MatchString(msg):
		r := Refusal{Layer: LayerAdmission, Detail: webhookName.FindStringSubmatch(msg)[1]}
		// Kyverno names the policy after the webhook: "... denied the request: Policy protect-platform-objects failed".
		rest := msg[strings.Index(msg, "denied the request")+len("denied the request"):]
		if m := policyName.FindStringSubmatch(rest); m != nil {
			r.Detail = m[1]
		}
		return r
	case rbacMessage.MatchString(msg):
		return Refusal{Layer: LayerRBAC}
	}
	return Refusal{Layer: LayerUnknown}
}
