package kube

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestConfirm(t *testing.T) {
	want := Identity{User: "bulkhead-check", Groups: []string{"alpha-admins"}}
	if err := Confirm(want, Identity{User: "bulkhead-check", Groups: []string{"alpha-admins", "system:authenticated"}}); err != nil {
		t.Errorf("extra groups must be fine: %v", err)
	}
	if err := Confirm(want, Identity{User: "someone-else", Groups: []string{"alpha-admins"}}); err == nil {
		t.Error("another user must be refused")
	}
	if err := Confirm(want, Identity{User: "bulkhead-check", Groups: []string{"system:authenticated"}}); err == nil {
		t.Error("a missing group must be refused")
	}
}

func TestWhoAmI(t *testing.T) {
	client := fake.NewClientset()
	client.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		r := &authenticationv1.SelfSubjectReview{}
		r.Status.UserInfo.Username = "alice"
		r.Status.UserInfo.Groups = []string{"devs"}
		return true, r, nil
	})
	id, err := WhoAmI(context.Background(), client)
	if err != nil || id.User != "alice" || id.Groups[0] != "devs" {
		t.Fatalf("got %+v, %v", id, err)
	}
}

func TestConnectWithAKubeconfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	cfg := `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster: {server: "https://127.0.0.1:6443"}
users:
- name: u
  user: {token: "t"}
contexts:
- name: one
  context: {cluster: c, user: u}
- name: two
  context: {cluster: c, user: u}
current-context: one
`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Connect(Options{Kubeconfig: path})
	if err != nil || c.Context != "one" || c.Server != "https://127.0.0.1:6443" {
		t.Fatalf("current context: %+v, %v", c, err)
	}
	if c, err = Connect(Options{Kubeconfig: path, Context: "two"}); err != nil || c.Context != "two" {
		t.Fatalf("named context: %v", err)
	}
	if _, err = Connect(Options{Kubeconfig: path, Context: "three"}); err == nil {
		t.Error("an unknown context must be an error")
	}
	imp, err := c.Impersonate(Identity{User: "bulkhead-check", Groups: []string{"g"}})
	if err != nil || imp.Config.Impersonate.UserName != "bulkhead-check" || imp.Config.Impersonate.Groups[0] != "g" || c.Config.Impersonate.UserName != "" {
		t.Errorf("impersonation must apply to a copy only: %v", err)
	}
}
