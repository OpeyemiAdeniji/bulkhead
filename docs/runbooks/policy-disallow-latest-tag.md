# Runbook: a pod was rejected because of its image tag

## What you see

    Error from server: error when creating "pod.yaml": admission webhook "vpol.validate.kyverno.svc-fail" denied the request: Policy disallow-latest-image-tag failed: Image busybox:latest uses the tag latest or has no tag. Fix: pin the image to a version, for example busybox:1.36, or to a digest. Runbook: <this page>

The first part of the text comes from Kubernetes and Kyverno. The part after `failed:` is the message of the policy.

## Why

The policy `disallow-latest-image-tag` applies to pods in tenant namespaces. It rejects an image that has the tag `latest`, or no tag, because both mean "whatever is current". Such an image can change under a running workload, and the same manifest then runs different code on different days. It also checks init containers, and a second policy, `disallow-latest-image-tag-ephemeral`, applies the same rule to ephemeral containers added to a running pod (`kubectl debug`), with the same message.

## How to fix it

Pin every image to a version or to a digest:

| Not accepted | Accepted |
|---|---|
| `busybox` | `busybox:1.36` |
| `busybox:latest` | `busybox@sha256:<digest>` |
| `registry.example.com:5000/team/app` (the `:5000` is a port, not a tag) | `registry.example.com:5000/team/app:1.2.3` |

A Deployment or CronJob that uses `latest` is rejected when it is created or updated, with the same message. A Deployment that already ran `latest` before the policy is accepted, and its new pods are rejected when the controller creates them. Look at the events of the ReplicaSet (`kubectl describe replicaset`) to see this message.

## If you need an exception

An exception is a file in `exceptions/`, added in a pull request: a `PolicyException` in the namespace `platform-exceptions`, with an absolute expiry date at most 90 days ahead, an owner, a reason and the tenant it is for. Continuous integration refuses a file that breaks these rules, and Kyverno deletes the exception at its date. The rules and an example are in [`exceptions/README.md`](../../exceptions/README.md), and the reasoning is in ADR 0008 and ADR 0014.

## Not covered

- The policy is written for pods. Kyverno generates the same rule for Deployments, ReplicaSets, StatefulSets, DaemonSets, Jobs and CronJobs. The live tests cover pods, Deployments and CronJobs.
- While Kyverno is stopped on purpose (scaled to zero), it removes its admission webhooks, so this policy does not apply and a `latest` image is admitted. A crash of the container is different: the webhooks stay, and pod creation in tenant namespaces is refused for about 10 seconds until the container is back. A rolling restart did not open a window in one test. A tenant admin cannot stop Kyverno (four requests were refused). The `kyverno` and `kube-system` namespaces are excluded by the namespace selector of the webhook. A Helm upgrade, a node drain and two replicas are not tested. See `docs/adr/0011-kyverno-outage-behaviour.md`.
- Ephemeral containers are checked by their own policy, `disallow-latest-image-tag-ephemeral`, because they are added through the `pods/ephemeralcontainers` subresource. Matching that subresource in `disallow-latest-image-tag` itself was tried on kind: Kyverno then generated no rules for Deployments and the other controllers (a Deployment with `latest` was accepted), and it marked the policy not ready, for lack of permissions to list a subresource that cannot be listed. The ephemeral policy judges admission only, with background scans off, and only the ephemeral containers a request adds, because an ephemeral container cannot be removed. The live tests add one with `busybox:latest` (refused) and one with `busybox:1.36` (accepted) as the platform admin; tenant admins may not add ephemeral containers at all.

## Workloads that already run latest

The policy applies to updates as well as creations, but it only blocks an image that was not in the
object before the update. A workload that already runs `latest` keeps running and accepts unrelated
edits, such as a label or an annotation.

A new pod is a creation, so it is still denied. Scaling up, a node drain, or a restart that recreates
a pod fails until the image is pinned. A Deployment shows this as a ReplicaFailure on its ReplicaSet.
Fix: pin the image to a version or a digest.
