package checks

import (
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestClassify(t *testing.T) {
	cases := []struct{ msg, layer, detail string }{
		{`secrets "x" is forbidden: User "bulkhead-check" cannot get resource "secrets" in API group "" in the namespace "tenant-bravo"`, LayerRBAC, ""},
		{`pods "p" is forbidden: violates PodSecurity "restricted:latest": privileged (container "c" must not set securityContext.privileged=true)`, LayerPodSecurity, ""},
		{`services "s" is forbidden: exceeded quota: tenant-quota, requested: services.nodeports=1, used: services.nodeports=0, limited: services.nodeports=0`, LayerQuota, "tenant-quota"},
		{`pods "p" is forbidden: maximum cpu usage per Container is 1, but limit is 4`, LayerLimitRange, ""},
		{`admission webhook "vpol.validate.kyverno.svc-fail" denied the request: Policy protect-platform-objects failed: only platform admins may change platform objects`, LayerAdmission, "protect-platform-objects"},
		{`networkpolicies.networking.k8s.io "x" is forbidden: ValidatingAdmissionPolicy 'no-delete' with binding 'b' denied request: nope`, LayerAdmission, "no-delete"},
		{`admission webhook "validate.example.com" denied the request: not today`, LayerAdmission, "validate.example.com"},
		{`something else entirely`, LayerUnknown, ""},
	}
	for _, c := range cases {
		got := Classify(c.msg)
		if got.Layer != c.layer || got.Detail != c.detail {
			t.Errorf("%q: got %+v, want %s (%s)", c.msg, got, c.layer, c.detail)
		}
	}
}

func TestRefused(t *testing.T) {
	gr := schema.GroupResource{Resource: "secrets"}
	if r, ok := Refused(apierrors.NewForbidden(gr, "x", errors.New(`User "u" cannot get resource "secrets"`))); !ok || r.Layer != LayerRBAC {
		t.Errorf("forbidden: %+v %v", r, ok)
	}
	// Kyverno answers a denial with 400 Bad Request (seen on kind with Kyverno 1.19.2).
	kyverno := apierrors.NewBadRequest(`admission webhook "vpol.validate.kyverno.svc-fail" denied the request: Policy protect-platform-objects failed: NetworkPolicy allow-dns-egress in tenant-alpha is managed by the platform`)
	if r, ok := Refused(kyverno); !ok || r.Layer != LayerAdmission || r.Detail != "protect-platform-objects" {
		t.Errorf("a webhook denial with 400: %+v %v", r, ok)
	}
	if _, ok := Refused(apierrors.NewBadRequest("the server could not understand the request")); ok {
		t.Error("a plain bad request is not a refusal")
	}
	if _, ok := Refused(apierrors.NewNotFound(gr, "x")); ok {
		t.Error("not found is not a refusal")
	}
	if _, ok := Refused(errors.New("connection refused")); ok {
		t.Error("a network error is not a refusal")
	}
	if _, ok := Refused(nil); ok {
		t.Error("nil is not a refusal")
	}
}
