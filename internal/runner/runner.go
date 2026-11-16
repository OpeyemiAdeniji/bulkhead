// Package runner runs the selected checks with their timeouts, applies the pair rule and
// works out the exit code.
package runner

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/exitcode"
	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

// Status is the outcome of one check for one subject.
type Status string

// The statuses.
const (
	Pass Status = "pass"
	Fail Status = "fail"
	// Inconclusive: a blocked check passed, but its pair did not, so the result does not count.
	Inconclusive   Status = "inconclusive"
	Skipped        Status = "skipped"
	Error          Status = "error"
	NotImplemented Status = "not-implemented"
	// Planned: the check was not sent, because the run is a dry run.
	Planned Status = "planned"
)

// Result is the outcome of one check for one subject, such as one pair of tenants.
type Result struct {
	Check   string `json:"check"`
	Status  Status `json:"status"`
	Subject string `json:"subject,omitempty"`
	Message string `json:"message,omitempty"`
	// Layer names what decided a blocked request: rbac, admission, network-policy, quota, limit-range.
	Layer    string `json:"layer,omitempty"`
	Duration string `json:"duration,omitempty"`
}

// Env is what a check gets to work with.
type Env struct {
	Cluster *kube.Cluster
	Tenants []tenant.Tenant
	Sandbox *sandbox.Sandbox
	// Impersonate returns a connection that acts as the given identity.
	Impersonate func(kube.Identity) (*kube.Cluster, error)
}

// Func runs one check and returns one result per subject.
type Func func(ctx context.Context, env *Env, c catalog.Check) []Result

var registry = map[string]Func{}

// Register adds the implementation of a check. It panics on an unknown or repeated ID,
// so a mistake is found when the program starts.
func Register(id string, f Func) {
	if _, ok := catalog.Get(id); !ok {
		panic("runner: no check " + id + " in the catalogue")
	}
	if _, dup := registry[id]; dup {
		panic("runner: check " + id + " is registered twice")
	}
	registry[id] = f
}

// Implemented reports whether a check has an implementation.
func Implemented(id string) bool { _, ok := registry[id]; return ok }

// Options of a run.
type Options struct {
	Checks       []catalog.Check
	CheckTimeout time.Duration
}

// WithPairs adds the pair of every selected blocked check, so that its result can count.
func WithPairs(selected []catalog.Check) []catalog.Check {
	have := map[string]bool{}
	for _, c := range selected {
		have[c.ID] = true
	}
	out := append([]catalog.Check(nil), selected...)
	for _, c := range selected {
		if c.Pair != "" && !have[c.Pair] {
			if p, ok := catalog.Get(c.Pair); ok {
				out = append(out, p)
				have[p.ID] = true
			}
		}
	}
	// Keep the catalogue order.
	order := map[string]int{}
	for i, c := range catalog.All() {
		order[c.ID] = i
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].ID] < order[out[j].ID] })
	return out
}

// ConfirmIdentities asks the API server, as each tenant identity, who it is
// (SelfSubjectReview), and stops the run when an answer does not match.
func ConfirmIdentities(ctx context.Context, env *Env) error {
	for _, t := range env.Tenants {
		ids := []kube.Identity{t.Admin}
		if t.Viewer != nil {
			ids = append(ids, *t.Viewer)
		}
		for _, id := range ids {
			r := sandbox.Request{Verb: "review", Kind: "SelfSubjectReview", As: id.String()}
			err := env.Sandbox.Do(ctx, r, func(ctx context.Context) error {
				c, err := env.Impersonate(id)
				if err != nil {
					return err
				}
				got, err := c.WhoAmI(ctx)
				if apierrors.IsForbidden(err) {
					return fmt.Errorf("your identity may not impersonate %s; the checker acts as each tenant through impersonation, which needs the impersonate permission on users and groups: %w", id, err)
				}
				if err != nil {
					return err
				}
				return kube.Confirm(id, got)
			})
			if errors.Is(err, sandbox.ErrNotSent) {
				continue
			}
			if err != nil {
				return exitcode.Errorf("confirm the identity of tenant %s (%s): %v", t.Namespace, id, err)
			}
		}
	}
	return nil
}

// Run runs the checks in order. It stops starting new checks when ctx ends, and marks them skipped.
func Run(ctx context.Context, env *Env, o Options) []Result {
	var results []Result
	for _, c := range o.Checks {
		if ctx.Err() != nil {
			results = append(results, Result{Check: c.ID, Status: Skipped, Message: "the run was interrupted or reached its timeout"})
			continue
		}
		if c.Mode == catalog.Active && env.Sandbox.Mode == sandbox.ReadOnly {
			results = append(results, Result{Check: c.ID, Status: Skipped, Message: "active check, not run in read-only mode"})
			continue
		}
		f, ok := registry[c.ID]
		if !ok {
			results = append(results, Result{Check: c.ID, Status: NotImplemented, Message: "not implemented in this version yet"})
			continue
		}
		results = append(results, runOne(ctx, env, c, f, o.CheckTimeout)...)
	}
	return ApplyPairs(results)
}

func runOne(ctx context.Context, env *Env, c catalog.Check, f Func, timeout time.Duration) []Result {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	rs := f(cctx, env, c)
	d := time.Since(start).Round(time.Millisecond).String()
	if len(rs) == 0 {
		rs = []Result{{Status: Error, Message: "the check returned no result"}}
		if errors.Is(cctx.Err(), context.DeadlineExceeded) {
			rs[0].Message = "timed out after " + timeout.String()
		}
	}
	for i := range rs {
		rs[i].Check = c.ID
		rs[i].Duration = d
		if env.Sandbox.Mode == sandbox.DryRun {
			rs[i].Status, rs[i].Message, rs[i].Layer = Planned, "not sent: dry run", ""
		}
	}
	return rs
}

// ApplyPairs marks a passed blocked check inconclusive unless its pair passed: at least one
// result of the pair is a pass, and none is anything but a pass or skipped. A subject the pair
// skipped (for example a tenant without a viewer) does not spoil the others, but a pair that
// passed nowhere makes the blocked result inconclusive.
func ApplyPairs(results []Result) []Result {
	passed := map[string]bool{}
	spoiled := map[string]bool{}
	for _, r := range results {
		switch r.Status {
		case Pass:
			passed[r.Check] = true
		case Skipped:
		default:
			spoiled[r.Check] = true
		}
	}
	pairOK := map[string]bool{}
	for id := range passed {
		pairOK[id] = !spoiled[id]
	}
	for i, r := range results {
		c, ok := catalog.Get(r.Check)
		if !ok || c.Expect != catalog.Blocked || r.Status != Pass {
			continue
		}
		if !pairOK[c.Pair] {
			results[i].Status = Inconclusive
			results[i].Message = fmt.Sprintf("blocked as expected, but its pair %s did not pass, so this result does not count", c.Pair)
		}
	}
	return results
}

// Count returns how many results have each status.
func Count(results []Result) map[Status]int {
	n := map[Status]int{}
	for _, r := range results {
		n[r.Status]++
	}
	return n
}

// ExitCode follows ADR 0018: 1 when a check failed or was inconclusive, 3 when a check could
// not run or nothing ran, and 0 otherwise.
func ExitCode(results []Result) int {
	n := Count(results)
	switch {
	case n[Fail] > 0 || n[Inconclusive] > 0:
		return exitcode.ChecksFailed
	case n[Error] > 0:
		return exitcode.Error
	case n[Pass] == 0 && n[Planned] == 0:
		return exitcode.Error
	}
	return exitcode.OK
}
