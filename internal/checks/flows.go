package checks

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/flows"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
)

// FlowTimeout is how long a probe waits for a connection, in seconds.
const FlowTimeout = 3

// The outcomes of a probe.
const (
	connected = "connected"
	timedOut  = "timed out"
	refused   = "refused"
	noName    = "no such name"
	planned   = "planned"
	probeErr  = "error"
)

type probeOutcome struct {
	kind   string
	detail string
}

func init() {
	runner.Register("FLW-01", flowCheck(flows.Allowed))
	runner.Register("FLW-02", flowCheck(flows.Blocked))
}

// probeScript opens a TCP connection and sends nothing (nc -z). BusyBox nc prints nothing
// when a connection fails, so the time tells a drop (the full timeout) from a refusal (at
// once). The destination and port are arguments, never part of the script.
const probeScript = `s=$(date +%s); nc -z -w "$3" "$1" "$2" 2>&1; rc=$?; e=$(date +%s); echo "bulkhead-probe rc=$rc seconds=$((e - s))"`

var (
	probeLine   = regexp.MustCompile(`bulkhead-probe rc=(\d+) seconds=(\d+)`)
	unresolving = regexp.MustCompile(`bad address|can't resolve`)
)

// classify turns the output of probeScript into an outcome.
func classify(out string, err error) probeOutcome {
	if errors.Is(err, sandbox.ErrNotSent) {
		return probeOutcome{kind: planned}
	}
	m := probeLine.FindStringSubmatch(out)
	if m == nil {
		return probeOutcome{kind: probeErr, detail: "the probe did not run: " + firstLine(out, err)}
	}
	rc, _ := strconv.Atoi(m[1])
	secs, _ := strconv.Atoi(m[2])
	switch {
	case rc == 0:
		return probeOutcome{kind: connected}
	case unresolving.MatchString(out):
		return probeOutcome{kind: noName}
	case secs >= FlowTimeout-1:
		return probeOutcome{kind: timedOut}
	default:
		return probeOutcome{kind: refused}
	}
}

// probeFlow probes a declared flow once per run, and remembers the outcome.
func probeFlow(ctx context.Context, env *runner.Env, checkID string, f *netFixture, fl flows.Flow) probeOutcome {
	s := stateOf(env)
	s.mu.Lock()
	if o, ok := s.probes[fl.Name]; ok {
		s.mu.Unlock()
		return o
	}
	s.mu.Unlock()

	var o probeOutcome
	clients := f.clients[fl.From]
	if len(clients) == 0 {
		o = probeOutcome{kind: probeErr, detail: fl.From + " is not a tenant under test, so it has no client pod"}
	} else {
		out, err := execIn(ctx, env, checkID, fl.From, clients[0].Name,
			"sh", "-c", probeScript, "probe", fl.To.Address(), strconv.Itoa(fl.To.Port), strconv.Itoa(FlowTimeout))
		o = classify(out, err)
	}
	s.mu.Lock()
	if s.probes == nil {
		s.probes = map[string]probeOutcome{}
	}
	s.probes[fl.Name] = o
	s.mu.Unlock()
	return o
}

func flowCheck(expect string) runner.Func {
	return func(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
		if env.Flows == nil {
			return []runner.Result{{Status: runner.Skipped, Message: "no flows file was given (--flows)"}}
		}
		var mine []flows.Flow
		for _, fl := range env.Flows.Flows {
			if fl.Expect == expect {
				mine = append(mine, fl)
			}
		}
		if len(mine) == 0 {
			return []runner.Result{{Status: runner.Skipped, Message: "the flows file declares no " + expect + " flow"}}
		}
		f, err := network(ctx, env, c.ID)
		if err != nil {
			var out []runner.Result
			for _, r := range fixtureFailed(err) {
				for _, fl := range mine {
					r.Subject, r.From, r.To = fl.Name, fl.From, fl.To.Label()
					out = append(out, r)
				}
			}
			return out
		}
		var out []runner.Result
		for _, fl := range mine {
			o := probeFlow(ctx, env, c.ID, f, fl)
			var r runner.Result
			if expect == flows.Allowed {
				r = judgeAllowed(fl, o)
			} else {
				r = judgeBlocked(ctx, env, c.ID, f, fl, o)
			}
			r.Subject, r.From, r.To = fl.Name, fl.From, fl.To.Label()
			out = append(out, r)
		}
		return out
	}
}

func judgeAllowed(fl flows.Flow, o probeOutcome) runner.Result {
	dst := fl.To.String()
	switch o.kind {
	case connected:
		return runner.Result{Status: runner.Pass, Message: "connected to " + dst}
	case timedOut:
		return runner.Result{Status: runner.Fail, Message: fmt.Sprintf("no connection to %s within %d seconds: a network policy drops it, or the service has no such port", dst, FlowTimeout)}
	case refused:
		return runner.Result{Status: runner.Fail, Message: "the connection to " + dst + " was refused: nothing listens on that port, or the network rejects it"}
	case noName:
		return runner.Result{Status: runner.Fail, Message: "the name " + fl.To.Address() + " does not resolve: check the service and namespace in the flows file"}
	case planned:
		return runner.Result{Status: runner.Planned}
	}
	return runner.Result{Status: runner.Error, Message: o.detail}
}

// judgeBlocked passes a flow that did not connect, but only when an allowed flow to the same
// destination and port connected: otherwise the destination may simply be down.
func judgeBlocked(ctx context.Context, env *runner.Env, checkID string, f *netFixture, fl flows.Flow, o probeOutcome) runner.Result {
	dst := fl.To.String()
	r := runner.Result{PairApplied: true}
	switch o.kind {
	case connected:
		r.Status, r.Message = runner.Fail, "connected to "+dst+", but this flow must be blocked"
		return r
	case noName:
		r.Status, r.Message = runner.Error, "the name "+fl.To.Address()+" does not resolve, so the flow was not tested"
		return r
	case planned:
		r.Status = runner.Planned
		return r
	case probeErr:
		r.Status, r.Message = runner.Error, o.detail
		return r
	}
	var witness string
	for _, p := range env.Flows.Pairs(fl) {
		if probeFlow(ctx, env, checkID, f, p).kind == connected {
			witness = p.Name
			break
		}
	}
	how := "the connection timed out"
	if o.kind == refused {
		how = "the connection was refused"
	}
	if witness == "" {
		r.Status = runner.Inconclusive
		r.Message = fmt.Sprintf("blocked (%s), but no allowed flow to %s connected, so the destination may be down and this result does not count", how, dst)
		return r
	}
	r.Status, r.Layer = runner.Pass, LayerNetworkPolicy
	r.Message = fmt.Sprintf("blocked: %s, while the allowed flow %s connected to the same destination", how, witness)
	return r
}
