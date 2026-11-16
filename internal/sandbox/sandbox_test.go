package sandbox

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var labelValue = regexp.MustCompile(`^[a-z0-9]([-a-z0-9_.]*[a-z0-9])?$`)

func TestRunIDIsAValidLabelValue(t *testing.T) {
	id := NewRunID(time.Date(2026, 11, 16, 9, 10, 10, 0, time.UTC))
	if !labelValue.MatchString(id) || len(id) > 63 {
		t.Fatalf("run ID %q is not a valid label value", id)
	}
	if !strings.HasPrefix(id, "20261116t091010-") {
		t.Errorf("run ID %q should start with the UTC time", id)
	}
	if NewRunID(time.Now()) == NewRunID(time.Now()) {
		t.Error("two run IDs in the same second must differ")
	}
}

func TestNamesArePrefixedAndShort(t *testing.T) {
	s := New(Live)
	n := s.Name(strings.Repeat("very-long-purpose-", 10))
	if !strings.HasPrefix(n, NamePrefix) || len(n) > 63 || strings.HasSuffix(n, "-") {
		t.Errorf("bad name %q", n)
	}
}

func TestModes(t *testing.T) {
	ctx := context.Background()
	read := Request{Verb: "get", Kind: "secret", As: "a"}
	store := Request{Verb: "create", Kind: "pod", As: "a", Stores: true}
	dryRunCreate := Request{Verb: "create", Kind: "pod", As: "a"}

	for _, tc := range []struct {
		mode          Mode
		req           Request
		wantCalled    bool
		wantErrTarget error
	}{
		{Live, read, true, nil},
		{Live, store, true, nil},
		{ReadOnly, read, true, nil},
		{ReadOnly, dryRunCreate, true, nil},
		{ReadOnly, store, false, ErrReadOnly},
		{DryRun, read, false, ErrNotSent},
		{DryRun, store, false, ErrNotSent},
	} {
		s := New(tc.mode)
		called := false
		err := s.Do(ctx, tc.req, func(context.Context) error { called = true; return nil })
		if called != tc.wantCalled || !errors.Is(err, tc.wantErrTarget) {
			t.Errorf("%s %s: called=%v err=%v", tc.mode, tc.req, called, err)
		}
		if len(s.Requests()) != 1 {
			t.Errorf("%s: every request must be recorded", tc.mode)
		}
	}
}

func TestCreateRefusesObjectsWithoutTheRunLabels(t *testing.T) {
	s := New(Live)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "bhc-x", Namespace: "a"}}
	if err := s.Create(context.Background(), pod, "pod", "a", "", func(context.Context) error { return nil }); err == nil {
		t.Fatal("an object without labels was accepted")
	}
	s.Prepare(&pod.ObjectMeta, "server")
	called := false
	if err := s.Create(context.Background(), pod, "pod", "a", "", func(context.Context) error { called = true; return nil }); err != nil || !called {
		t.Fatalf("a prepared object was refused: %v", err)
	}
	if s.Created() != 1 {
		t.Errorf("Created() = %d, want 1", s.Created())
	}
	other := New(Live)
	if err := other.Create(context.Background(), pod, "pod", "a", "", func(context.Context) error { return nil }); err == nil {
		t.Error("an object of another run was accepted")
	}
}

func TestCleanupRemovesOnlyTheRunsObjects(t *testing.T) {
	ctx := context.Background()
	s := New(Live)
	mine := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant-a"}}
	s.Prepare(&mine.ObjectMeta, "server")
	otherRun := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "bhc-old", Namespace: "tenant-a", Labels: map[string]string{LabelManagedBy: ManagedBy, LabelRunID: "older-run"}}}
	tenants := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "tenant-a", Labels: map[string]string{"app": "web"}}}
	cm := corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant-b"}}
	s.Prepare(&cm.ObjectMeta, "cfg")
	elsewhere := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "bhc-x", Namespace: "not-named", Labels: s.Labels()}}
	client := fake.NewClientset(&mine, &otherRun, &tenants, &cm, &elsewhere)

	deleted, err := Cleanup(ctx, client, []string{"tenant-a", "tenant-b"}, s.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Total() != 2 || deleted["pod"] != 1 || deleted["configmap"] != 1 {
		t.Fatalf("deleted %v, want one pod and one configmap", deleted)
	}
	left, _ := client.CoreV1().Pods("tenant-a").List(ctx, metav1.ListOptions{})
	if len(left.Items) != 2 {
		t.Errorf("%d pods left in tenant-a, want the other run's and the tenant's own", len(left.Items))
	}
	if _, err := client.CoreV1().Pods("not-named").Get(ctx, "bhc-x", metav1.GetOptions{}); err != nil {
		t.Error("a namespace that was not named must not be touched")
	}

	// Every run: the older run's pod goes too, the tenant's own pod stays.
	deleted, err = Cleanup(ctx, client, []string{"tenant-a"}, "")
	if err != nil || deleted.Total() != 1 {
		t.Fatalf("all runs: deleted %v, err %v", deleted, err)
	}
	if _, err := client.CoreV1().Pods("tenant-a").Get(ctx, "app", metav1.GetOptions{}); err != nil {
		t.Error("the tenant's own pod must never be deleted")
	}
}
