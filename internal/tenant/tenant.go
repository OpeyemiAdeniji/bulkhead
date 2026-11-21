// Package tenant finds the tenants under test and the identities the checker acts as.
package tenant

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
)

// TesterUser is the user name the checker impersonates together with a tenant's group.
// Impersonating a group needs a user name; this one makes the checker's requests easy to find in audit logs.
const TesterUser = "bulkhead-check"

// Tenant is one tenant under test.
type Tenant struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// Admin is the identity of the tenant's admins. Every tenant has one.
	Admin kube.Identity `json:"admin"`
	// Viewer is the identity of the tenant's read-only users, or nil when the tenant has none.
	Viewer *kube.Identity `json:"viewer,omitempty"`
}

// Options says how the identities of a tenant are found: from the subjects of two role
// bindings in its namespace. The defaults are the bindings of Bulkhead's tenant chart.
type Options struct {
	// Profile is how the platform models tenants (profile.go); "" means ProfileNamespaces.
	Profile       string
	Namespaces    []string
	AdminBinding  string
	ViewerBinding string
	// Force allows namespaces that look like system namespaces.
	Force bool
}

// Defaults of Options, matching charts/bulkhead-tenant.
const (
	DefaultAdminBinding  = "tenant-admins"
	DefaultViewerBinding = "tenant-viewers"
)

// systemNamespaces are never tested without Force: the checker creates pods in tenant namespaces.
var systemNamespaces = []string{"default", "kube-system", "kube-public", "kube-node-lease"}

// ErrNotATenant marks a namespace that the checker refuses to test.
var ErrNotATenant = errors.New("not a tenant namespace")

// Validate checks the options without a cluster.
func (o Options) Validate() error {
	if o.Profile != "" {
		if err := CheckProfile(o.Profile); err != nil {
			return err
		}
	}
	if len(o.Namespaces) == 0 {
		return fmt.Errorf("name the tenant namespaces with --tenant (at least one, two or more for the checks between tenants)")
	}
	seen := map[string]bool{}
	for _, ns := range o.Namespaces {
		if ns == "" {
			return fmt.Errorf("an empty tenant namespace was given")
		}
		if seen[ns] {
			return fmt.Errorf("tenant namespace %q is given twice", ns)
		}
		seen[ns] = true
		if !o.Force && (slices.Contains(systemNamespaces, ns) || strings.HasPrefix(ns, "kube-")) {
			return fmt.Errorf("%w: %q is a system namespace; the checker creates test pods in tenant namespaces, so it refuses unless --force-namespaces is given", ErrNotATenant, ns)
		}
	}
	if o.AdminBinding == "" {
		return fmt.Errorf("the admin role binding name is empty")
	}
	return nil
}

// Discover reads each namespace and its role bindings, and returns the tenants.
func Discover(ctx context.Context, client kubernetes.Interface, o Options) ([]Tenant, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	var out []Tenant
	for _, ns := range o.Namespaces {
		if _, err := client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("tenant namespace %q does not exist", ns)
			}
			return nil, fmt.Errorf("read namespace %q: %w", ns, err)
		}
		admin, err := identityFrom(ctx, client, ns, o.AdminBinding)
		if err != nil {
			return nil, err
		}
		if admin == nil {
			return nil, fmt.Errorf("namespace %q has no role binding %q; name the binding of its admins with --admin-binding", ns, o.AdminBinding)
		}
		t := Tenant{Name: ns, Namespace: ns, Admin: *admin}
		if o.ViewerBinding != "" {
			if t.Viewer, err = identityFrom(ctx, client, ns, o.ViewerBinding); err != nil {
				return nil, err
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// identityFrom returns the identity of the first subject of a role binding, or nil when the
// binding does not exist. Groups are preferred, because platforms bind teams as groups.
func identityFrom(ctx context.Context, client kubernetes.Interface, ns, name string) (*kube.Identity, error) {
	rb, err := client.RbacV1().RoleBindings(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read role binding %s/%s: %w", ns, name, err)
	}
	id, err := IdentityOf(rb.Subjects)
	if err != nil {
		return nil, fmt.Errorf("role binding %s/%s: %w", ns, name, err)
	}
	return &id, nil
}

// IdentityOf picks the identity to act as from the subjects of a role binding:
// the first group, else the first service account, else the first user.
func IdentityOf(subjects []rbacv1.Subject) (kube.Identity, error) {
	for _, s := range subjects {
		if s.Kind == rbacv1.GroupKind {
			return kube.Identity{User: TesterUser, Groups: []string{s.Name}}, nil
		}
	}
	for _, s := range subjects {
		if s.Kind == rbacv1.ServiceAccountKind {
			return kube.Identity{
				User:   fmt.Sprintf("system:serviceaccount:%s:%s", s.Namespace, s.Name),
				Groups: []string{"system:serviceaccounts", "system:serviceaccounts:" + s.Namespace},
			}, nil
		}
	}
	for _, s := range subjects {
		if s.Kind == rbacv1.UserKind {
			return kube.Identity{User: s.Name}, nil
		}
	}
	return kube.Identity{}, fmt.Errorf("it has no group, service account or user subject")
}
