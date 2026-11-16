package checks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

// LayerNetworkPolicy names the network plugin enforcing network policies.
const LayerNetworkPolicy = "network-policy"

// InternetProbe is the address NET-06 tries to reach. It is fetched by IP address, so DNS is not involved.
var InternetProbe = "http://1.1.1.1"

func init() {
	runner.Register("NET-01", netPerTenant(func(f *netFixture, t tenant.Tenant) (testPod, testPod, error) {
		return f.client(t, 0), f.server(t, 0), nil
	}))
	runner.Register("NET-02", netPerTenant(func(f *netFixture, t tenant.Tenant) (testPod, testPod, error) {
		if len(f.nodes) < 2 {
			return testPod{}, testPod{}, errNeedsTwoNodes
		}
		return f.client(t, 1), f.server(t, 0), nil
	}))
	runner.Register("NET-03", net03)
	runner.Register("NET-04", netBetween(false))
	runner.Register("NET-05", netBetween(true))
	runner.Register("NET-06", net06)
}

// netFixture is the set of network test pods: in every tenant, a server and a client on each
// of up to two nodes. Pods are reached by IP address, so DNS is not involved in NET-01, 02, 04, 05.
type netFixture struct {
	nodes   []string
	servers map[string][]testPod // namespace -> one per node
	clients map[string][]testPod
}

func (f *netFixture) server(t tenant.Tenant, node int) testPod { return f.servers[t.Namespace][node] }
func (f *netFixture) client(t tenant.Tenant, node int) testPod { return f.clients[t.Namespace][node] }

// network returns the network fixture of the run, and starts it on first use.
func network(ctx context.Context, env *runner.Env, checkID string) (*netFixture, error) {
	s := stateOf(env)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.net != nil || s.netErr != nil {
		return s.net, s.netErr
	}
	f, err := startNetwork(ctx, env, checkID)
	// The outcome is kept for the run: the other network checks neither retry nor plan twice.
	s.net, s.netErr = f, err
	return f, err
}

func startNetwork(ctx context.Context, env *runner.Env, checkID string) (*netFixture, error) {
	notSent := func(err error) bool { return errors.Is(err, sandbox.ErrNotSent) }
	nodes, err := schedulableNodes(ctx, env, checkID)
	if err != nil && !notSent(err) {
		return nil, err
	}
	if len(nodes) == 0 && env.Sandbox.Mode != sandbox.DryRun {
		return nil, errors.New("no ready node accepts ordinary pods")
	}
	if len(nodes) > 2 {
		nodes = nodes[:2]
	}
	if len(nodes) == 0 { // dry run: plan the pods without placement
		nodes = []string{""}
	}
	f := &netFixture{nodes: nodes, servers: map[string][]testPod{}, clients: map[string][]testPod{}}
	var all []*testPod
	for _, t := range env.Tenants {
		for i, node := range nodes {
			srv, err := startPod(ctx, env, checkID, t, fmt.Sprintf("net-server-%d", i+1), node, serverCommand, smallResources, corev1.RestartPolicyAlways, 8080)
			if err != nil && !notSent(err) {
				return nil, err
			}
			cli, err := startPod(ctx, env, checkID, t, fmt.Sprintf("net-client-%d", i+1), node, sleepCommand, smallResources, corev1.RestartPolicyAlways, 0)
			if err != nil && !notSent(err) {
				return nil, err
			}
			f.servers[t.Namespace] = append(f.servers[t.Namespace], srv)
			f.clients[t.Namespace] = append(f.clients[t.Namespace], cli)
		}
	}
	for ns := range f.servers {
		for i := range f.servers[ns] {
			all = append(all, &f.servers[ns][i], &f.clients[ns][i])
		}
	}
	if err := waitReady(ctx, env, checkID, all); err != nil {
		return nil, err // in a dry run: ErrNotSent, after every pod was planned
	}
	for ns := range f.servers {
		for i, node := range nodes {
			for _, p := range []testPod{f.servers[ns][i], f.clients[ns][i]} {
				if node != "" && p.Node != node {
					return nil, fmt.Errorf("test pod %s/%s runs on %s instead of %s", p.Namespace, p.Name, p.Node, node)
				}
			}
		}
	}
	return f, nil
}

// fetch asks the client pod to fetch from the server pod by IP, with a timeout of three seconds.
func fetch(ctx context.Context, env *runner.Env, checkID string, from, to testPod) (string, error) {
	return execIn(ctx, env, checkID, from.Namespace, from.Name, "wget", "-q", "-O-", "-T", "3", "http://"+to.IP+":8080")
}

// netResult judges a fetch: allowed checks need the server's answer, blocked checks need a timeout.
func netResult(c catalog.Check, subject, out string, err error) runner.Result {
	if errors.Is(err, sandbox.ErrNotSent) {
		return runner.Result{Status: runner.Planned, Subject: subject}
	}
	reached := err == nil && strings.Contains(out, "ok")
	if c.Expect == catalog.Allowed {
		if reached {
			return runner.Result{Status: runner.Pass, Subject: subject}
		}
		return runner.Result{Status: runner.Fail, Subject: subject, Message: "no answer, but this traffic must work: " + firstLine(out, err)}
	}
	switch {
	case reached:
		return runner.Result{Status: runner.Fail, Subject: subject, Message: "the server answered, but this traffic must be blocked"}
	case strings.Contains(out, "timed out"):
		return runner.Result{Status: runner.Pass, Subject: subject, Layer: LayerNetworkPolicy, Message: "blocked: the connection timed out"}
	case strings.Contains(out, "refused") || strings.Contains(out, "unreachable"):
		return runner.Result{Status: runner.Pass, Subject: subject, Layer: LayerNetworkPolicy, Message: "blocked: " + firstLine(out, nil) + " (the network plugin rejects instead of dropping)"}
	}
	return runner.Result{Status: runner.Error, Subject: subject, Message: "the probe failed for another reason: " + firstLine(out, err)}
}

func firstLine(out string, err error) string {
	s := strings.TrimSpace(out)
	if s == "" && err != nil {
		s = err.Error()
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// fixtureFailed turns an error of the fixture into results.
func fixtureFailed(err error) []runner.Result {
	switch {
	case errors.Is(err, sandbox.ErrNotSent):
		return []runner.Result{{Status: runner.Planned}}
	case errors.Is(err, errNeedsTwoNodes):
		return []runner.Result{{Status: runner.Skipped, Message: "needs two schedulable nodes"}}
	}
	return []runner.Result{errResult("", err)}
}

func netPerTenant(pick func(*netFixture, tenant.Tenant) (testPod, testPod, error)) runner.Func {
	return func(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
		f, err := network(ctx, env, c.ID)
		if err != nil {
			return fixtureFailed(err)
		}
		var out []runner.Result
		for _, t := range env.Tenants {
			from, to, err := pick(f, t)
			if err != nil {
				return fixtureFailed(err)
			}
			subject := fmt.Sprintf("%s, %s -> %s", t.Namespace, nodeLabel(from.Node), nodeLabel(to.Node))
			o, err := fetch(ctx, env, c.ID, from, to)
			out = append(out, netResult(c, subject, o, err))
		}
		return out
	}
}

func nodeLabel(n string) string {
	if n == "" {
		return "any node"
	}
	return n
}

func net03(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
	f, err := network(ctx, env, c.ID)
	if err != nil {
		return fixtureFailed(err)
	}
	var out []runner.Result
	for _, t := range env.Tenants {
		cli := f.client(t, 0)
		o, err := execIn(ctx, env, c.ID, cli.Namespace, cli.Name, "nslookup", "kubernetes.default.svc.cluster.local")
		switch {
		case errors.Is(err, sandbox.ErrNotSent):
			out = append(out, runner.Result{Status: runner.Planned, Subject: t.Namespace})
		case err == nil:
			out = append(out, runner.Result{Status: runner.Pass, Subject: t.Namespace})
		default:
			out = append(out, runner.Result{Status: runner.Fail, Subject: t.Namespace, Message: "the lookup failed: " + firstLine(o, err)})
		}
	}
	return out
}

// netBetween tests traffic from every tenant to every other tenant, on the same node or across nodes.
func netBetween(across bool) runner.Func {
	return func(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
		ps := pairs(env.Tenants)
		if len(ps) == 0 {
			return needTwo
		}
		f, err := network(ctx, env, c.ID)
		if err != nil {
			return fixtureFailed(err)
		}
		if across && len(f.nodes) < 2 {
			return fixtureFailed(errNeedsTwoNodes)
		}
		var out []runner.Result
		for _, p := range ps {
			for i := range f.nodes {
				j := i
				if across {
					j = 1 - i
				}
				from, to := f.client(p[0], i), f.server(p[1], j)
				subject := fmt.Sprintf("%s, %s -> %s", between(p[0], p[1]), nodeLabel(from.Node), nodeLabel(to.Node))
				o, err := fetch(ctx, env, c.ID, from, to)
				out = append(out, netResult(c, subject, o, err))
			}
		}
		return out
	}
}

func net06(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
	f, err := network(ctx, env, c.ID)
	if err != nil {
		return fixtureFailed(err)
	}
	var out []runner.Result
	for _, t := range env.Tenants {
		cli := f.client(t, 0)
		o, err := execIn(ctx, env, c.ID, cli.Namespace, cli.Name, "wget", "-q", "-O-", "-T", "3", InternetProbe)
		subject := t.Namespace + " -> " + InternetProbe
		switch {
		case errors.Is(err, sandbox.ErrNotSent):
			out = append(out, runner.Result{Status: runner.Planned, Subject: subject})
		case err == nil || strings.Contains(o, "HTTP/") || strings.Contains(o, "server returned error"):
			out = append(out, runner.Result{Status: runner.Fail, Subject: subject, Message: "the internet answered, but egress must be blocked"})
		case strings.Contains(o, "timed out"):
			out = append(out, runner.Result{Status: runner.Pass, Subject: subject, Layer: LayerNetworkPolicy, Message: "blocked: the connection timed out"})
		default:
			out = append(out, runner.Result{Status: runner.Error, Subject: subject, Message: "the probe failed for another reason: " + firstLine(o, err)})
		}
	}
	return out
}
