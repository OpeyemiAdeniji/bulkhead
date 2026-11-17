// Package policyreport writes the results of a run into the cluster as PolicyReport objects of
// the Policy Report API (wgpolicyk8s.io/v1alpha2), so that dashboards and tools that already
// show those reports, for example the reports of Kyverno, also show the isolation of each tenant.
//
// There is one report per tenant namespace, named bulkhead-check. Each run replaces it, so it
// always shows the latest run. The reports are the output of a run, not test objects: the
// clean-up at the end of a run and bulkhead cleanup leave them alone.
package policyreport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/report"
	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
)

// The API and the names of the reports.
const (
	Group        = "wgpolicyk8s.io"
	Version      = "v1alpha2"
	GroupVersion = Group + "/" + Version
	Kind         = "PolicyReport"
	// Name is the name of the report in each tenant namespace.
	Name = "bulkhead-check"
	// Source names Bulkhead as the tool that wrote a result.
	Source = "bulkhead"
)

// Resource is the PolicyReport resource.
var Resource = schema.GroupVersionResource{Group: Group, Version: Version, Resource: "policyreports"}

// verbs are what writing a report needs: read the current one, create it the first time and
// replace it afterwards.
var verbs = []string{"get", "create", "update"}

// Preflight checks, before any check runs, that the cluster serves the PolicyReport API and that
// the person running the checker may write reports into every tenant namespace. A run that would
// fail only at its end, after minutes of checks, fails at once instead.
func Preflight(ctx context.Context, client kubernetes.Interface, namespaces []string) error {
	list, err := client.Discovery().ServerResourcesForGroupVersion(GroupVersion)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("ask the cluster whether it serves %s: %w", GroupVersion, err)
	}
	served := false
	if list != nil {
		for _, r := range list.APIResources {
			served = served || r.Name == Resource.Resource
		}
	}
	if !served {
		return fmt.Errorf("the cluster does not serve the PolicyReport API (%s). Kyverno installs it; "+
			"otherwise install its CRD from https://github.com/kubernetes-sigs/wg-policy-prototypes, or run without --policy-reports", GroupVersion)
	}
	var denied []string
	for _, ns := range namespaces {
		for _, verb := range verbs {
			review := &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: ns, Verb: verb, Group: Group, Resource: Resource.Resource},
			}}
			got, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("ask the API server whether you may %s policyreports in %s: %w", verb, ns, err)
			}
			if !got.Status.Allowed {
				denied = append(denied, verb+" in "+ns)
			}
		}
	}
	if len(denied) > 0 {
		return fmt.Errorf("you may not write PolicyReports (%s), so --policy-reports cannot store the results; "+
			"a cluster admin can, or run without --policy-reports", strings.Join(denied, ", "))
	}
	return nil
}

// Build returns the report of each tenant namespace of the run, in the order of the tenants, and
// the number of results that belong to no tenant and are therefore in no report. Every check of
// the catalogue names the tenant it tested, so that number is 0 unless a check breaks that rule.
func Build(r *report.Report) ([]*unstructured.Unstructured, int) {
	namespaces := make([]string, 0, len(r.Tenants))
	for _, t := range r.Tenants {
		namespaces = append(namespaces, t.Namespace)
	}
	byTenant := map[string][]any{}
	summaries := map[string]map[string]any{}
	leftOut := 0
	for _, res := range r.Results {
		ns := TenantOf(res, namespaces)
		if ns == "" {
			leftOut++
			continue
		}
		e := entry(r, res)
		byTenant[ns] = append(byTenant[ns], e)
		if summaries[ns] == nil {
			summaries[ns] = map[string]any{"pass": int64(0), "fail": int64(0), "warn": int64(0), "error": int64(0), "skip": int64(0)}
		}
		k := e["result"].(string)
		summaries[ns][k] = summaries[ns][k].(int64) + 1
	}
	var out []*unstructured.Unstructured
	for _, ns := range namespaces {
		if len(byTenant[ns]) == 0 {
			continue
		}
		out = append(out, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": GroupVersion,
			"kind":       Kind,
			"metadata": map[string]any{
				"name":      Name,
				"namespace": ns,
				"labels": map[string]any{
					sandbox.LabelManagedBy: sandbox.ManagedBy,
					sandbox.LabelRunID:     r.Run.ID,
				},
			},
			"scope":   map[string]any{"apiVersion": "v1", "kind": "Namespace", "name": ns},
			"summary": summaries[ns],
			"results": byTenant[ns],
		}})
	}
	return out, leftOut
}

// TenantOf returns the tenant namespace a result belongs to: the source of a network result, or
// else the tenant its subject starts with ("tenant-alpha", "tenant-alpha -> tenant-bravo",
// "tenant-alpha, a-pod"). It returns "" for a result that names no tenant.
func TenantOf(res runner.Result, namespaces []string) string {
	for _, ns := range namespaces {
		if res.From == ns {
			return ns
		}
	}
	// The longest name first, so that tenant-a does not claim the results of tenant-ab.
	sorted := append([]string(nil), namespaces...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, ns := range sorted {
		rest, ok := strings.CutPrefix(res.Subject, ns)
		if ok && (rest == "" || rest[0] == ' ' || rest[0] == ',') {
			return ns
		}
	}
	return ""
}

// entry turns one result into one result of a PolicyReport. The policy is the check ID and the
// rule is the subject, so every result of a report has its own name.
func entry(r *report.Report, res runner.Result) map[string]any {
	c := r.Check(res.Check)
	result := Result(res.Status)
	rule := res.Subject
	if rule == "" {
		rule = c.ID
	}
	message := res.Message
	if message == "" {
		message = c.Title
	}
	props := map[string]any{
		"title":  c.Title,
		"status": string(res.Status),
		"expect": string(c.Expect),
		"level":  string(c.Level),
		"runId":  r.Run.ID,
		"docs":   report.CheckDocURL(c.ID),
	}
	for k, v := range map[string]string{"layer": res.Layer, "from": res.From, "to": res.To} {
		if v != "" {
			props[k] = v
		}
	}
	return map[string]any{
		"source":     Source,
		"policy":     c.ID,
		"rule":       rule,
		"category":   string(c.Category),
		"severity":   Severity(c.Level),
		"result":     result,
		"scored":     result == "pass" || result == "fail",
		"message":    message,
		"timestamp":  map[string]any{"seconds": r.Run.FinishedAt.Unix(), "nanos": int64(0)},
		"properties": props,
	}
}

// Result maps a status of Bulkhead to a result of the Policy Report API. Inconclusive becomes
// warn: the check could not decide, because the request that must work did not work either.
func Result(s runner.Status) string {
	switch s {
	case runner.Pass:
		return "pass"
	case runner.Fail:
		return "fail"
	case runner.Inconclusive:
		return "warn"
	case runner.Error:
		return "error"
	default: // skipped and not-implemented; a dry run writes no reports
		return "skip"
	}
}

// Severity maps the level of a check to a severity: a basic check guards the isolation every
// tenant relies on, a hardened check adds to it, and a research check explores.
func Severity(l catalog.Level) string {
	switch l {
	case catalog.Basic:
		return "high"
	case catalog.Hardened:
		return "medium"
	default:
		return "low"
	}
}

// Write creates the reports, or replaces the reports of an earlier run. It returns the
// namespace/name of each report it wrote.
func Write(ctx context.Context, client dynamic.Interface, reports []*unstructured.Unstructured) ([]string, error) {
	var written []string
	var errs []error
	for _, u := range reports {
		ri := client.Resource(Resource).Namespace(u.GetNamespace())
		ref := u.GetNamespace() + "/" + u.GetName()
		old, err := ri.Get(ctx, u.GetName(), metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			_, err = ri.Create(ctx, u, metav1.CreateOptions{FieldManager: sandbox.ManagedBy})
		case err == nil:
			u.SetResourceVersion(old.GetResourceVersion())
			_, err = ri.Update(ctx, u, metav1.UpdateOptions{FieldManager: sandbox.ManagedBy})
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("write PolicyReport %s: %w", ref, err))
			continue
		}
		written = append(written, ref)
	}
	return written, errors.Join(errs...)
}
