package runner

import (
	"context"
	"testing"
	"time"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/exitcode"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
)

// register adds a fake implementation for the length of one test.
func register(t *testing.T, id string, f Func) {
	t.Helper()
	Register(id, f)
	t.Cleanup(func() { delete(registry, id) })
}

func fixed(s Status) Func {
	return func(context.Context, *Env, catalog.Check) []Result { return []Result{{Status: s, Subject: "a -> b"}} }
}

func checks(ids ...string) []catalog.Check {
	var out []catalog.Check
	for _, id := range ids {
		c, _ := catalog.Get(id)
		out = append(out, c)
	}
	return out
}

func TestPairRule(t *testing.T) {
	// NET-04 is blocked as expected, but its pair NET-01 failed: the pass does not count.
	register(t, "NET-01", fixed(Fail))
	register(t, "NET-04", fixed(Pass))
	env := &Env{Sandbox: sandbox.New(sandbox.Live)}
	rs := Run(context.Background(), env, Options{Checks: checks("NET-01", "NET-04"), CheckTimeout: time.Second})
	if rs[1].Check != "NET-04" || rs[1].Status != Inconclusive {
		t.Fatalf("got %+v", rs)
	}
	if ExitCode(rs) != exitcode.ChecksFailed {
		t.Errorf("exit code %d", ExitCode(rs))
	}
}

func TestPairThatPassedKeepsThePass(t *testing.T) {
	register(t, "NET-01", fixed(Pass))
	register(t, "NET-04", fixed(Pass))
	rs := Run(context.Background(), &Env{Sandbox: sandbox.New(sandbox.Live)}, Options{Checks: checks("NET-01", "NET-04"), CheckTimeout: time.Second})
	if rs[1].Status != Pass || ExitCode(rs) != exitcode.OK {
		t.Fatalf("got %+v", rs)
	}
}

func TestASkippedSubjectOfThePairDoesNotSpoilIt(t *testing.T) {
	rs := ApplyPairs([]Result{
		{Check: "ACC-05", Status: Pass, Subject: "a"},
		{Check: "ACC-05", Status: Skipped, Subject: "b"},
		{Check: "ACC-04", Status: Pass, Subject: "a"},
	})
	if rs[2].Status != Pass {
		t.Errorf("got %+v", rs[2])
	}
	rs = ApplyPairs([]Result{
		{Check: "ACC-05", Status: Skipped, Subject: "a"},
		{Check: "ACC-04", Status: Pass, Subject: "a"},
	})
	if rs[1].Status != Inconclusive {
		t.Errorf("a pair that passed nowhere must make it inconclusive: %+v", rs[1])
	}
}

func TestReadOnlySkipsActiveChecks(t *testing.T) {
	called := false
	register(t, "NET-01", func(context.Context, *Env, catalog.Check) []Result { called = true; return nil })
	rs := Run(context.Background(), &Env{Sandbox: sandbox.New(sandbox.ReadOnly)}, Options{Checks: checks("NET-01"), CheckTimeout: time.Second})
	if called || rs[0].Status != Skipped {
		t.Fatalf("an active check ran in read-only mode: %+v", rs)
	}
}

func TestTimeoutAndNotImplemented(t *testing.T) {
	register(t, "ACC-01", func(ctx context.Context, _ *Env, _ catalog.Check) []Result {
		<-ctx.Done()
		return nil
	})
	rs := Run(context.Background(), &Env{Sandbox: sandbox.New(sandbox.Live)}, Options{Checks: checks("ACC-01", "ACC-02"), CheckTimeout: 20 * time.Millisecond})
	if rs[0].Status != Error || rs[0].Message != "timed out after 20ms" {
		t.Errorf("timeout: %+v", rs[0])
	}
	if rs[1].Status != NotImplemented {
		t.Errorf("not implemented: %+v", rs[1])
	}
	if ExitCode(rs) != exitcode.Error {
		t.Errorf("exit code %d", ExitCode(rs))
	}
}

func TestInterruptedRunSkipsTheRest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	register(t, "ACC-01", func(context.Context, *Env, catalog.Check) []Result {
		cancel()
		return []Result{{Status: Pass}}
	})
	register(t, "ACC-02", fixed(Pass))
	rs := Run(ctx, &Env{Sandbox: sandbox.New(sandbox.Live)}, Options{Checks: checks("ACC-01", "ACC-02"), CheckTimeout: time.Second})
	if rs[1].Status != Skipped {
		t.Errorf("got %+v", rs[1])
	}
}

func TestDryRunMarksResultsPlanned(t *testing.T) {
	register(t, "ACC-01", fixed(Fail))
	rs := Run(context.Background(), &Env{Sandbox: sandbox.New(sandbox.DryRun)}, Options{Checks: checks("ACC-01"), CheckTimeout: time.Second})
	if rs[0].Status != Planned {
		t.Errorf("got %+v", rs[0])
	}
}

func TestWithPairsAddsMissingPairsInCatalogueOrder(t *testing.T) {
	got := WithPairs(checks("NET-05", "ACC-02"))
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	want := []string{"ACC-01", "ACC-02", "NET-02", "NET-05"}
	if len(ids) != len(want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("got %v, want %v", ids, want)
		}
	}
}

func TestRegisterPanicsOnMistakes(t *testing.T) {
	for _, id := range []string{"NOPE-01"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Register(%q) did not panic", id)
				}
			}()
			Register(id, fixed(Pass))
		}()
	}
}
