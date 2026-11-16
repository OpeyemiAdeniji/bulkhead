package sandbox

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Deleted counts the objects clean-up removed, by kind.
type Deleted map[string]int

// Total returns the number of objects removed.
func (d Deleted) Total() int {
	n := 0
	for _, v := range d {
		n += v
	}
	return n
}

// Selector returns the label selector of one run, or of every run of the checker when runID is empty.
func Selector(runID string) string {
	s := LabelManagedBy + "=" + ManagedBy
	if runID != "" {
		s += "," + LabelRunID + "=" + runID
	}
	return s
}

// Cleanup deletes the objects of one run (or of every run, when runID is empty) in the given
// namespaces. It only deletes objects that carry the checker's labels, and it keeps going after
// an error so that as much as possible is removed.
func Cleanup(ctx context.Context, client kubernetes.Interface, namespaces []string, runID string) (Deleted, error) {
	deleted := Deleted{}
	sel := metav1.ListOptions{LabelSelector: Selector(runID)}
	now := int64(0)
	bg := metav1.DeletePropagationBackground
	var errs []error
	del := func(kind, ns string, names []string, fn func(string) error) {
		for _, n := range names {
			if err := fn(n); err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("delete %s %s/%s: %w", kind, ns, n, err))
				continue
			}
			deleted[kind]++
		}
	}
	for _, ns := range namespaces {
		opts := metav1.DeleteOptions{PropagationPolicy: &bg}
		podOpts := metav1.DeleteOptions{PropagationPolicy: &bg, GracePeriodSeconds: &now}

		if l, err := client.AppsV1().Deployments(ns).List(ctx, sel); err != nil {
			errs = append(errs, err)
		} else {
			del("deployment", ns, names(l.Items, func(i int) string { return l.Items[i].Name }), func(n string) error {
				return client.AppsV1().Deployments(ns).Delete(ctx, n, opts)
			})
		}
		if l, err := client.CoreV1().Pods(ns).List(ctx, sel); err != nil {
			errs = append(errs, err)
		} else {
			del("pod", ns, names(l.Items, func(i int) string { return l.Items[i].Name }), func(n string) error {
				return client.CoreV1().Pods(ns).Delete(ctx, n, podOpts)
			})
		}
		if l, err := client.CoreV1().Services(ns).List(ctx, sel); err != nil {
			errs = append(errs, err)
		} else {
			del("service", ns, names(l.Items, func(i int) string { return l.Items[i].Name }), func(n string) error {
				return client.CoreV1().Services(ns).Delete(ctx, n, opts)
			})
		}
		if l, err := client.CoreV1().Secrets(ns).List(ctx, sel); err != nil {
			errs = append(errs, err)
		} else {
			del("secret", ns, names(l.Items, func(i int) string { return l.Items[i].Name }), func(n string) error {
				return client.CoreV1().Secrets(ns).Delete(ctx, n, opts)
			})
		}
		if l, err := client.CoreV1().ConfigMaps(ns).List(ctx, sel); err != nil {
			errs = append(errs, err)
		} else {
			del("configmap", ns, names(l.Items, func(i int) string { return l.Items[i].Name }), func(n string) error {
				return client.CoreV1().ConfigMaps(ns).Delete(ctx, n, opts)
			})
		}
	}
	return deleted, errors.Join(errs...)
}

func names[T any](items []T, name func(int) string) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = name(i)
	}
	return out
}
