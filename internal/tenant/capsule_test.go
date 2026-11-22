package tenant

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// capsuleTenantObject is a Tenant as Capsule 0.15.2 reports it.
func capsuleTenantObject(name string, namespaces []any, specOwners, statusOwners []any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "capsule.clastix.io/v1beta2",
		"kind":       "Tenant",
		"metadata":   map[string]any{"name": name},
		"spec":       map[string]any{"owners": specOwners},
		"status":     map[string]any{"namespaces": namespaces},
	}}
	if statusOwners != nil {
		u.Object["status"].(map[string]any)["owners"] = statusOwners
	}
	return u
}

func capsuleClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{CapsuleTenants: "TenantList"}, objs...)
}

func TestDiscoverCapsule(t *testing.T) {
	group := func(n string) map[string]any { return map[string]any{"kind": "Group", "name": n} }
	dyn := capsuleClient(
		capsuleTenantObject("alpha", []any{"alpha-2", "alpha-1"}, []any{map[string]any{"kind": "User", "name": "alice"}},
			[]any{map[string]any{"kind": "User", "name": "alice"}, group("alpha-admins")}),
		capsuleTenantObject("bravo", []any{"bravo-1"}, []any{map[string]any{"kind": "ServiceAccount", "name": "system:serviceaccount:ops:robot"}}, nil),
	)
	ts, err := Discover(context.Background(), nil, dyn, Options{Profile: ProfileCapsule, Namespaces: []string{"alpha", "bravo"}, AdminBinding: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	a, b := ts[0], ts[1]
	// The first namespace by name; a group owner first, from status.owners.
	if a.Name != "alpha" || a.Namespace != "alpha-1" || strings.Join(a.Namespaces, ",") != "alpha-1,alpha-2" ||
		a.Admin.User != TesterUser || a.Admin.Groups[0] != "alpha-admins" || a.Viewer != nil {
		t.Errorf("alpha: %+v", a)
	}
	if b.Admin.User != "system:serviceaccount:ops:robot" || b.Namespace != "bravo-1" {
		t.Errorf("bravo, a service account owner from spec.owners: %+v", b)
	}
}

func TestDiscoverCapsuleErrors(t *testing.T) {
	user := []any{map[string]any{"kind": "User", "name": "u"}}
	for name, c := range map[string]struct {
		dyn     *dynamicfake.FakeDynamicClient
		tenants []string
		want    string
	}{
		"missing tenant": {capsuleClient(), []string{"nope"}, `capsule tenant "nope" does not exist`},
		"no namespace":   {capsuleClient(capsuleTenantObject("empty", nil, user, nil)), []string{"empty"}, "has no namespace yet"},
		"no owner":       {capsuleClient(capsuleTenantObject("lonely", []any{"l-1"}, nil, nil)), []string{"lonely"}, "its owners"},
		"bad service account": {capsuleClient(capsuleTenantObject("sa", []any{"s-1"},
			[]any{map[string]any{"kind": "ServiceAccount", "name": "robot"}}, nil)), []string{"sa"}, "is not system:serviceaccount"},
		"shared namespace": {capsuleClient(capsuleTenantObject("x", []any{"same"}, user, nil), capsuleTenantObject("y", []any{"same"}, user, nil)),
			[]string{"x", "y"}, `both use namespace "same"`},
	} {
		_, err := Discover(context.Background(), nil, c.dyn, Options{Profile: ProfileCapsule, Namespaces: c.tenants, AdminBinding: "unused"})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", name, c.want, err)
		}
	}
}
