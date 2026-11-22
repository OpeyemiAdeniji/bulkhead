package tenant

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func binding(ns, name string, subjects ...rbacv1.Subject) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}, Subjects: subjects}
}

func ns(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func group(name string) rbacv1.Subject { return rbacv1.Subject{Kind: rbacv1.GroupKind, Name: name} }

func TestValidateRefusesSystemNamespaces(t *testing.T) {
	for _, n := range []string{"kube-system", "default", "kube-anything"} {
		err := Options{Namespaces: []string{n}, AdminBinding: "x"}.Validate()
		if !errors.Is(err, ErrNotATenant) {
			t.Errorf("%s: got %v, want a refusal", n, err)
		}
		if err := (Options{Namespaces: []string{n}, AdminBinding: "x", Force: true}).Validate(); err != nil {
			t.Errorf("%s with force: %v", n, err)
		}
	}
	if err := (Options{AdminBinding: "x"}).Validate(); err == nil {
		t.Error("no namespaces must be refused")
	}
	if err := (Options{Namespaces: []string{"a", "a"}, AdminBinding: "x"}).Validate(); err == nil {
		t.Error("a namespace given twice must be refused")
	}
}

func TestDiscover(t *testing.T) {
	client := fake.NewClientset(
		ns("tenant-alpha"), ns("tenant-bravo"),
		binding("tenant-alpha", DefaultAdminBinding, group("alpha-admins")),
		binding("tenant-alpha", DefaultViewerBinding, group("alpha-viewers")),
		binding("tenant-bravo", DefaultAdminBinding, rbacv1.Subject{Kind: rbacv1.UserKind, Name: "bob"}, group("bravo-admins")),
	)
	o := Options{Namespaces: []string{"tenant-alpha", "tenant-bravo"}, AdminBinding: DefaultAdminBinding, ViewerBinding: DefaultViewerBinding}
	ts, err := Discover(context.Background(), client, nil, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 {
		t.Fatalf("got %d tenants", len(ts))
	}
	if a := ts[0]; a.Admin.User != TesterUser || a.Admin.Groups[0] != "alpha-admins" || a.Viewer == nil || a.Viewer.Groups[0] != "alpha-viewers" {
		t.Errorf("alpha: %+v", a)
	}
	if b := ts[1]; b.Admin.Groups[0] != "bravo-admins" || b.Viewer != nil {
		t.Errorf("bravo: a group must win over a user, and no viewer binding means no viewer: %+v", b)
	}

	_, err = Discover(context.Background(), client, nil, Options{Namespaces: []string{"missing"}, AdminBinding: DefaultAdminBinding})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing namespace: %v", err)
	}
	_, err = Discover(context.Background(), client, nil, Options{Namespaces: []string{"tenant-bravo"}, AdminBinding: "other"})
	if err == nil || !strings.Contains(err.Error(), "--admin-binding") {
		t.Errorf("missing binding: %v", err)
	}
}

func TestIdentityOf(t *testing.T) {
	sa := rbacv1.Subject{Kind: rbacv1.ServiceAccountKind, Name: "ci", Namespace: "tenant-a"}
	id, err := IdentityOf([]rbacv1.Subject{{Kind: rbacv1.UserKind, Name: "u"}, sa})
	if err != nil || id.User != "system:serviceaccount:tenant-a:ci" {
		t.Errorf("a service account must win over a user: %+v %v", id, err)
	}
	if id, _ := IdentityOf([]rbacv1.Subject{{Kind: rbacv1.UserKind, Name: "u"}}); id.User != "u" || len(id.Groups) != 0 {
		t.Errorf("user: %+v", id)
	}
	if _, err := IdentityOf(nil); err == nil {
		t.Error("no subjects must be an error")
	}
}
