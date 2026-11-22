package tenant

import (
	"context"
	"fmt"
	"slices"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// ProfileCapsule: a tenant is a Capsule Tenant (capsule.clastix.io/v1beta2) with owners and
// namespaces. Measured on kind with Capsule 0.15.2: the owners get the cluster roles admin and
// capsule-namespace-deleter through role bindings with hashed names (capsule:managed:<hash>),
// so the profile reads the owners and namespaces from the Tenant, not from role bindings.
const ProfileCapsule = "capsule"

// CapsuleTenants is the resource of Capsule's tenants.
var CapsuleTenants = schema.GroupVersionResource{Group: "capsule.clastix.io", Version: "v1beta2", Resource: "tenants"}

// discoverCapsule reads each Capsule Tenant. The checks use its first namespace by name.
func discoverCapsule(ctx context.Context, dyn dynamic.Interface, o Options) ([]Tenant, error) {
	if dyn == nil {
		return nil, fmt.Errorf("the capsule profile needs a dynamic client")
	}
	var out []Tenant
	for _, name := range o.Namespaces {
		u, err := dyn.Resource(CapsuleTenants).Get(ctx, name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			return nil, fmt.Errorf("capsule tenant %q does not exist (kubectl get tenants lists them)", name)
		case err != nil:
			return nil, fmt.Errorf("read capsule tenant %q: %w (is Capsule installed?)", name, err)
		}
		t, err := capsuleTenant(u)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	// Two tenants must not share the namespace the checks use, or the checks between them test nothing.
	seen := map[string]string{}
	for _, t := range out {
		if other, ok := seen[t.Namespace]; ok {
			return nil, fmt.Errorf("capsule tenants %q and %q both use namespace %q", other, t.Name, t.Namespace)
		}
		seen[t.Namespace] = t.Name
	}
	return out, nil
}

// capsuleTenant turns a Tenant object into a tenant under test.
func capsuleTenant(u *unstructured.Unstructured) (Tenant, error) {
	name := u.GetName()
	namespaces, _, _ := unstructured.NestedStringSlice(u.Object, "status", "namespaces")
	if len(namespaces) == 0 {
		return Tenant{}, fmt.Errorf("capsule tenant %q has no namespace yet; an owner creates one with kubectl create namespace", name)
	}
	namespaces = slices.Clone(namespaces)
	slices.Sort(namespaces)
	// status.owners includes owners from TenantOwner objects; spec.owners is the fallback.
	owners, _, _ := unstructured.NestedSlice(u.Object, "status", "owners")
	if len(owners) == 0 {
		owners, _, _ = unstructured.NestedSlice(u.Object, "spec", "owners")
	}
	var subjects []rbacv1.Subject
	for _, o := range owners {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := m["kind"].(string)
		n, _ := m["name"].(string)
		if n == "" {
			continue
		}
		s := rbacv1.Subject{Kind: kind, Name: n}
		if kind == rbacv1.ServiceAccountKind {
			// Capsule names a service account owner system:serviceaccount:<namespace>:<name>.
			rest, ok := strings.CutPrefix(n, "system:serviceaccount:")
			ns, sa, found := strings.Cut(rest, ":")
			if !ok || !found {
				return Tenant{}, fmt.Errorf("capsule tenant %q: owner %q is not system:serviceaccount:<namespace>:<name>", name, n)
			}
			s.Namespace, s.Name = ns, sa
		}
		subjects = append(subjects, s)
	}
	admin, err := IdentityOf(subjects)
	if err != nil {
		return Tenant{}, fmt.Errorf("capsule tenant %q: its owners: %w", name, err)
	}
	return Tenant{Name: name, Namespace: namespaces[0], Namespaces: namespaces, Admin: admin}, nil
}
