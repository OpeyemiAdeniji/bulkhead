package checks

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/kube"
	"github.com/OpeyemiAdeniji/bulkhead/internal/operatorrefs"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
	"github.com/OpeyemiAdeniji/bulkhead/internal/tenant"
)

// The cross-namespace reference checks (ADR 0021): for each reference in --operator-refs, a
// custom resource is sent as a server-side dry run, as the tenant admin. Nothing is stored, so
// no operator ever sees it.

func init() {
	runner.Register("XNS-01", xns01)
	runner.Register("XNS-02", xns02)
}

// DynamicFor returns a dynamic client for a connection. Tests replace it.
var DynamicFor = func(c *kube.Cluster) (dynamic.Interface, error) { return dynamic.NewForConfig(c.Config) }

// sendReference sends the custom resource of ref in tenant a as a dry run, with its namespace
// field pointing to target.
func sendReference(ctx context.Context, env *runner.Env, c catalog.Check, ref operatorrefs.Reference, a tenant.Tenant, target string) error {
	name := env.Sandbox.Name("xns")
	obj, err := ref.Object(a.Namespace, name, env.Sandbox.Labels(), target)
	if err != nil {
		return err
	}
	req := sandbox.Request{Verb: "create", Kind: ref.ResKind, Namespace: a.Namespace, Name: name + " (" + ref.NamespacePath + "=" + target + ")", As: a.Admin.String(), Check: c.ID}
	return do(ctx, env, req, func(ctx context.Context) error {
		cl, err := env.Impersonate(a.Admin)
		if err != nil {
			return err
		}
		dyn, err := DynamicFor(cl)
		if err != nil {
			return err
		}
		_, err = dyn.Resource(ref.GVR()).Namespace(a.Namespace).Create(ctx, obj, metav1.CreateOptions{DryRun: dryRun})
		return err
	})
}

// ownReference sends the reference of a tenant to its own namespace once per run, for XNS-01
// and as the pair of XNS-02, and remembers the answer.
func ownReference(ctx context.Context, env *runner.Env, c catalog.Check, ref operatorrefs.Reference, a tenant.Tenant) error {
	s := stateOf(env)
	key := ref.Name + " " + a.Namespace
	s.mu.Lock()
	if err, ok := s.xnsOwn[key]; ok {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	err := sendReference(ctx, env, c, ref, a, a.Namespace)
	s.mu.Lock()
	if s.xnsOwn == nil {
		s.xnsOwn = map[string]error{}
	}
	s.xnsOwn[key] = err
	s.mu.Unlock()
	return err
}

func noReferences(env *runner.Env) []runner.Result {
	if env.OperatorRefs == nil {
		return []runner.Result{{Status: runner.Skipped, Message: "no references file was given (--operator-refs)"}}
	}
	return nil
}

func xns01(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
	if r := noReferences(env); r != nil {
		return r
	}
	var out []runner.Result
	for _, ref := range env.OperatorRefs.References {
		for _, a := range env.Tenants {
			out = append(out, allowed(ref.Name+", "+a.Namespace, ownReference(ctx, env, c, ref, a)))
		}
	}
	return out
}

func xns02(ctx context.Context, env *runner.Env, c catalog.Check) []runner.Result {
	if r := noReferences(env); r != nil {
		return r
	}
	if len(env.Tenants) < 2 {
		return needTwo
	}
	var out []runner.Result
	for _, ref := range env.OperatorRefs.References {
		for _, p := range pairs(env.Tenants) {
			a, b := p[0], p[1]
			subject := ref.Name + ", " + between(a, b)
			r := blocked(subject, sendReference(ctx, env, c, ref, a, b.Namespace), LayerAdmission)
			// The pair is the same reference from the same tenant to its own namespace.
			r.PairApplied = true
			if r.Status == runner.Pass {
				if err := ownReference(ctx, env, c, ref, a); err != nil {
					r.Status = runner.Inconclusive
					r.Message = fmt.Sprintf("refused, but the same resource pointing to %s was not accepted either (XNS-01), so this result does not count: %s", a.Namespace, short(err))
				}
			}
			out = append(out, r)
		}
	}
	return out
}
