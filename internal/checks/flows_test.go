package checks

import (
	"context"
	"strings"
	"testing"

	"github.com/OpeyemiAdeniji/bulkhead/internal/flows"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
)

// What a probe from a namespace to a destination sees, as the probe script prints it.
var probeOutputs = map[string]string{
	connected: "bulkhead-probe rc=0 seconds=0",
	timedOut:  "bulkhead-probe rc=1 seconds=3",
	refused:   "bulkhead-probe rc=1 seconds=0",
	noName:    "nc: bad address 'nowhere.ns.svc'\nbulkhead-probe rc=1 seconds=0",
}

// flowEnv is a simulated cluster whose network answers each probe from the table, keyed by
// "namespace destination:port". It records the commands it was asked to run.
func flowEnv(t *testing.T, mode sandbox.Mode, file string, network map[string]string) (*runner.Env, *[][]string) {
	t.Helper()
	s := &netSim{nodes: twoWorkers()}
	env := s.env(t, mode)
	f, err := flows.Parse([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	env.Flows = f
	var commands [][]string
	inner := Exec
	Exec = func(ctx context.Context, e *runner.Env, ns, pod string, cmd []string) (string, error) {
		if cmd[0] != "sh" {
			return inner(ctx, e, ns, pod, cmd)
		}
		commands = append(commands, cmd)
		outcome, ok := network[ns+" "+cmd[4]+":"+cmd[5]]
		if !ok {
			outcome = timedOut
		}
		return probeOutputs[outcome], nil
	}
	return env, &commands
}

const dbFlows = `
apiVersion: bulkhead.io/v1alpha1
kind: Flows
flows:
  - {name: a-to-db, from: tenant-a, to: {service: db, namespace: shared, port: 5432}, expect: allowed}
  - {name: b-not-to-db, from: tenant-b, to: {service: db, namespace: shared, port: 5432}, expect: blocked}
`

const db = "db.shared.svc:5432"

func TestDeclaredFlowsThatHold(t *testing.T) {
	env, commands := flowEnv(t, sandbox.Live, dbFlows, map[string]string{"tenant-a " + db: connected, "tenant-b " + db: timedOut})
	got := runNet(t, env, "FLW-01", "FLW-02")
	a, b := got["FLW-01"][0], got["FLW-02"][0]
	if a.Status != runner.Pass || a.Subject != "a-to-db" || a.From != "tenant-a" || a.To != "shared" {
		t.Errorf("allowed flow: %+v", a)
	}
	if b.Status != runner.Pass || b.Layer != LayerNetworkPolicy || !strings.Contains(b.Message, "timed out, while the allowed flow a-to-db connected") {
		t.Errorf("blocked flow: %+v", b)
	}
	// Each flow is probed once, although FLW-02 also needs the outcome of its pair.
	if len(*commands) != 2 {
		t.Errorf("probed %d times, want 2: %v", len(*commands), *commands)
	}
	// The destination is an argument of the script, never part of it.
	for _, c := range *commands {
		if c[2] != probeScript || c[4] != "db.shared.svc" || c[5] != "5432" {
			t.Errorf("unexpected probe command %q", c)
		}
	}
}

func TestADeclaredFlowThatLeaks(t *testing.T) {
	env, _ := flowEnv(t, sandbox.Live, dbFlows, map[string]string{"tenant-a " + db: connected, "tenant-b " + db: connected})
	got := runNet(t, env, "FLW-01", "FLW-02")
	if b := got["FLW-02"][0]; b.Status != runner.Fail || !strings.Contains(b.Message, "must be blocked") {
		t.Errorf("a blocked flow that connects must fail: %+v", b)
	}
}

// TestADestinationThatIsDownProvesNothing: when the allowed flow does not connect either, the
// blocked flow is inconclusive, not a pass.
func TestADestinationThatIsDownProvesNothing(t *testing.T) {
	env, _ := flowEnv(t, sandbox.Live, dbFlows, map[string]string{"tenant-a " + db: timedOut, "tenant-b " + db: timedOut})
	got := runNet(t, env, "FLW-01", "FLW-02")
	if a := got["FLW-01"][0]; a.Status != runner.Fail || !strings.Contains(a.Message, "no connection") {
		t.Errorf("allowed flow: %+v", a)
	}
	if b := got["FLW-02"][0]; b.Status != runner.Inconclusive || !strings.Contains(b.Message, "may be down") {
		t.Errorf("blocked flow: %+v", b)
	}
	if code := runner.ExitCode(append(got["FLW-01"], got["FLW-02"]...)); code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
}

func TestARefusalCountsWhenThePairConnected(t *testing.T) {
	env, _ := flowEnv(t, sandbox.Live, dbFlows, map[string]string{"tenant-a " + db: connected, "tenant-b " + db: refused})
	got := runNet(t, env, "FLW-01", "FLW-02")
	if b := got["FLW-02"][0]; b.Status != runner.Pass || !strings.Contains(b.Message, "refused") {
		t.Errorf("blocked flow: %+v", b)
	}
}

// TestTheFlowPairIsPerDestination: a failing allowed flow to another destination does not make
// the blocked flows of this one inconclusive, as the pair rule for whole checks would.
func TestTheFlowPairIsPerDestination(t *testing.T) {
	file := dbFlows + "  - {name: a-to-api, from: tenant-a, to: {host: api.example.com, port: 443}, expect: allowed}\n"
	env, _ := flowEnv(t, sandbox.Live, file, map[string]string{"tenant-a " + db: connected, "tenant-b " + db: timedOut, "tenant-a api.example.com:443": timedOut})
	got := runNet(t, env, "FLW-01", "FLW-02")
	if len(got["FLW-01"]) != 2 || got["FLW-01"][1].Status != runner.Fail {
		t.Fatalf("the allowed flow to the API must fail: %+v", got["FLW-01"])
	}
	if b := got["FLW-02"][0]; b.Status != runner.Pass {
		t.Errorf("the blocked flow to the database must still pass: %+v", b)
	}
}

func TestANameThatDoesNotResolve(t *testing.T) {
	env, _ := flowEnv(t, sandbox.Live, dbFlows, map[string]string{"tenant-a " + db: noName, "tenant-b " + db: noName})
	got := runNet(t, env, "FLW-01", "FLW-02")
	if a := got["FLW-01"][0]; a.Status != runner.Fail || !strings.Contains(a.Message, "does not resolve") {
		t.Errorf("allowed flow: %+v", a)
	}
	if b := got["FLW-02"][0]; b.Status != runner.Error || !strings.Contains(b.Message, "not tested") {
		t.Errorf("a blocked flow to a name that does not resolve is not a pass: %+v", b)
	}
}

func TestFlowsWithoutAFileAreSkipped(t *testing.T) {
	s := &netSim{nodes: twoWorkers()}
	env := s.env(t, sandbox.Live)
	got := runNet(t, env, "FLW-01")
	if r := got["FLW-01"][0]; r.Status != runner.Skipped {
		t.Errorf("without a flows file: %+v", r)
	}
}

func TestFlowsInADryRunArePlanned(t *testing.T) {
	env, commands := flowEnv(t, sandbox.DryRun, dbFlows, nil)
	got := runNet(t, env, "FLW-01", "FLW-02")
	for _, r := range append(got["FLW-01"], got["FLW-02"]...) {
		if r.Status != runner.Planned || r.Subject == "" {
			t.Errorf("dry run: %+v", r)
		}
	}
	if len(*commands) != 0 {
		t.Errorf("a dry run probed: %v", *commands)
	}
}

func TestClassifyAProbe(t *testing.T) {
	for out, want := range map[string]string{
		probeOutputs[connected]:                              connected,
		probeOutputs[timedOut]:                               timedOut,
		"bulkhead-probe rc=1 seconds=4":                      timedOut,
		probeOutputs[refused]:                                refused,
		"bulkhead-probe rc=1 seconds=1":                      refused,
		probeOutputs[noName]:                                 noName,
		"nc: can't resolve x\nbulkhead-probe rc=1 seconds=0": noName,
		"sh: nc: not found":                                  probeErr,
	} {
		if got := classify(out, nil); got.kind != want {
			t.Errorf("classify(%q) = %s, want %s", out, got.kind, want)
		}
	}
	if got := classify("", sandbox.ErrNotSent); got.kind != planned {
		t.Errorf("not sent: %s", got.kind)
	}
}
