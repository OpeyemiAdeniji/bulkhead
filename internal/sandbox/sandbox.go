// Package sandbox is the only way checks send requests to a cluster. It enforces the safety
// rules of the checker in one place: test objects carry the run's labels, --dry-run sends
// nothing, --read-only stores nothing, and clean-up finds every object by its labels.
package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Labels on every object the checker creates.
const (
	LabelManagedBy = "app.kubernetes.io/managed-by"
	ManagedBy      = "bulkhead-check"
	LabelRunID     = "bulkhead.io/run-id"
	// NamePrefix starts the name of every object the checker creates.
	NamePrefix = "bhc-"
)

// Mode is how requests are handled.
type Mode string

const (
	// Live sends every request.
	Live Mode = "live"
	// ReadOnly sends only requests that store nothing: reads, access reviews and server-side dry runs.
	ReadOnly Mode = "read-only"
	// DryRun sends nothing and records every request that would be sent.
	DryRun Mode = "dry-run"
)

// ErrReadOnly is returned for a request that would store an object in read-only mode.
var ErrReadOnly = errors.New("refused in read-only mode: this request would store an object")

// ErrNotSent is returned in dry-run mode, where requests are recorded and not sent.
var ErrNotSent = errors.New("not sent: dry run")

// Request describes one request a check sends.
type Request struct {
	Verb      string `json:"verb"` // get, list, create, delete, review
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	As        string `json:"as"`
	// Stores is true when the request stores an object, which read-only mode refuses.
	// Server-side dry runs, reads and access reviews store nothing.
	Stores bool   `json:"stores"`
	Check  string `json:"check,omitempty"`
}

// String returns the request in the form used in messages and the dry-run plan.
func (r Request) String() string {
	target := r.Kind
	if r.Name != "" {
		target += "/" + r.Name
	}
	if r.Namespace != "" {
		target += " in " + r.Namespace
	}
	s := fmt.Sprintf("%s %s as %s", r.Verb, target, r.As)
	if !r.Stores && (r.Verb == "create" || r.Verb == "delete" || r.Verb == "update") {
		s += " (server-side dry run)"
	}
	return s
}

// Sandbox belongs to one run of the checker.
type Sandbox struct {
	RunID string
	Mode  Mode

	mu       sync.Mutex
	requests []Request
	created  int
}

// New returns a sandbox with a new run ID.
func New(mode Mode) *Sandbox {
	return &Sandbox{RunID: NewRunID(time.Now()), Mode: mode}
}

// NewRunID returns an ID that is unique enough for one cluster and valid as a label value:
// the UTC time and four random bytes, for example 20261116t091010-1a2b3c4d.
func NewRunID(now time.Time) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b) // crypto/rand does not fail on supported platforms
	return strings.ToLower(now.UTC().Format("20060102T150405")) + "-" + hex.EncodeToString(b)
}

// Labels returns the labels of every object of this run.
func (s *Sandbox) Labels() map[string]string {
	return map[string]string{LabelManagedBy: ManagedBy, LabelRunID: s.RunID}
}

// Name returns the name of a test object: the prefix, the random part of the run ID and the
// purpose, cut to 63 characters so it is valid for every kind.
func (s *Sandbox) Name(purpose string) string {
	short := s.RunID
	if i := strings.LastIndex(short, "-"); i >= 0 {
		short = short[i+1:]
	}
	n := NamePrefix + short + "-" + purpose
	if len(n) > 63 {
		n = strings.TrimRight(n[:63], "-.")
	}
	return n
}

// Prepare sets the name and the run's labels on an object the checker is about to create.
func (s *Sandbox) Prepare(meta *metav1.ObjectMeta, purpose string) {
	meta.Name = s.Name(purpose)
	if meta.Labels == nil {
		meta.Labels = map[string]string{}
	}
	for k, v := range s.Labels() {
		meta.Labels[k] = v
	}
}

// Do sends one request through the sandbox. fn sends it; it is called only when the mode allows.
func (s *Sandbox) Do(ctx context.Context, r Request, fn func(context.Context) error) error {
	s.mu.Lock()
	s.requests = append(s.requests, r)
	mode := s.Mode
	s.mu.Unlock()
	switch {
	case mode == DryRun:
		return ErrNotSent
	case mode == ReadOnly && r.Stores:
		return ErrReadOnly
	}
	err := fn(ctx)
	if err == nil && r.Stores && r.Verb == "create" {
		s.mu.Lock()
		s.created++
		s.mu.Unlock()
	}
	return err
}

// Create stores an object of this run. It refuses an object without the run's labels, so that
// clean-up can always find what the checker made.
func (s *Sandbox) Create(ctx context.Context, meta metav1.Object, kind, as, check string, fn func(context.Context) error) error {
	if meta.GetLabels()[LabelRunID] != s.RunID || meta.GetLabels()[LabelManagedBy] != ManagedBy {
		return fmt.Errorf("refusing to create %s %s: it lacks the labels of run %s (use Prepare)", kind, meta.GetName(), s.RunID)
	}
	if !strings.HasPrefix(meta.GetName(), NamePrefix) {
		return fmt.Errorf("refusing to create %s %s: its name must start with %q", kind, meta.GetName(), NamePrefix)
	}
	r := Request{Verb: "create", Kind: kind, Namespace: meta.GetNamespace(), Name: meta.GetName(), As: as, Stores: true, Check: check}
	return s.Do(ctx, r, fn)
}

// Requests returns every request sent or recorded so far.
func (s *Sandbox) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Created returns how many objects this run stored.
func (s *Sandbox) Created() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.created
}
