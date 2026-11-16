package checks

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/OpeyemiAdeniji/bulkhead/internal/runner"
	"github.com/OpeyemiAdeniji/bulkhead/internal/sandbox"
)

// ReleaseTimeout is how long removing a category's test pods, and the quota catching up, may take.
var ReleaseTimeout = 2 * time.Minute

// releasePods removes the run's test pods from every tenant namespace, waits until they are gone
// and until each quota counts them no more, and forgets the fixtures that used them. The next
// category starts from the tenant's own usage, so its numbers are not distorted by earlier checks.
func releasePods(ctx context.Context, env *runner.Env, checkID string) error {
	s := stateOf(env)
	s.mu.Lock()
	s.net, s.netErr = nil, nil
	s.res, s.resErr = nil, nil
	s.mu.Unlock()

	r := sandbox.Request{Verb: "delete", Kind: "pods", Name: "test pods of earlier checks", As: you, Stores: true, Check: checkID}
	return do(ctx, env, r, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, ReleaseTimeout)
		defer cancel()
		sel := metav1.ListOptions{LabelSelector: sandbox.Selector(env.Sandbox.RunID)}
		zero := int64(0)
		for _, t := range env.Tenants {
			l, err := env.Cluster.Client.CoreV1().Pods(t.Namespace).List(ctx, sel)
			if err != nil {
				return err
			}
			for _, p := range l.Items {
				if err := env.Cluster.Client.CoreV1().Pods(t.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{GracePeriodSeconds: &zero}); err != nil && !isNotFound(err) {
					return err
				}
			}
		}
		for {
			left, err := podsLeft(ctx, env, sel)
			if err != nil {
				return err
			}
			settled, err := quotasSettled(ctx, env)
			if err != nil {
				return err
			}
			if left == 0 && settled {
				return nil
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("test pods of earlier checks were not gone, or the quota did not catch up, after %s (%d pods left)", ReleaseTimeout, left)
			case <-time.After(time.Second):
			}
		}
	})
}

func podsLeft(ctx context.Context, env *runner.Env, sel metav1.ListOptions) (int, error) {
	n := 0
	for _, t := range env.Tenants {
		l, err := env.Cluster.Client.CoreV1().Pods(t.Namespace).List(ctx, sel)
		if err != nil {
			return 0, err
		}
		n += len(l.Items)
	}
	return n, nil
}

// quotasSettled reports whether every quota of the tenants counts exactly the pods that exist and
// are not finished. The quota controller updates its counts a little after pods are deleted.
func quotasSettled(ctx context.Context, env *runner.Env) (bool, error) {
	for _, t := range env.Tenants {
		pods, err := env.Cluster.Client.CoreV1().Pods(t.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		active := 0
		for _, p := range pods.Items {
			if p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
				active++
			}
		}
		qs, err := env.Cluster.Client.CoreV1().ResourceQuotas(t.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		for _, q := range qs.Items {
			used, ok := q.Status.Used[corev1.ResourcePods]
			if ok && used.Value() != int64(active) {
				return false, nil
			}
		}
	}
	return true, nil
}
