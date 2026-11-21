# 0020. Declared flows can be probed from inside a running workload

- Status: accepted
- Date: 2026-11-21

## Context

Declared flows (ADR 0019) are probed from the checker's own client pod in the source tenant. Network policies often select pods by their labels: "pods with `app: orders` may reach the shared database, the rest of the namespace may not". The checker's client pod does not have those labels, so such a policy is not exercised: the probe tests the namespace, not the workload.

A probe that runs inside the network namespace of a real workload pod has its IP address and labels, and so meets the same policies. Kubernetes offers that through ephemeral containers: a container added to a running pod, sharing its network.

These facts were measured on the kind cluster (Kubernetes 1.37) before deciding:

- An ephemeral container **cannot be removed**. A patch that removes it from the list is ignored; it stays in the pod's spec, terminated, until the pod is replaced. A name cannot be used twice in a pod.
- Pod Security applies: in a namespace with the restricted level, an ephemeral container without a restricted security context is refused. A restricted one runs, as a user other than root.
- Bulkhead's tenant admin role cannot add ephemeral containers (`pods/ephemeralcontainers` is not in it). Cluster admins can.
- `kubectl auth can-i update pods/ephemeralcontainers` answers "yes" for the tenant admin, because kubectl reads `ephemeralcontainers` as a pod name there; the request itself is refused. The subresource must be named explicitly (`--subresource`, or the `subresource` field of an access review).
- The platform's latest-tag policy does not look at ephemeral containers. That is a gap of the platform chart, handled separately.

## Options considered

1. **Exec into an existing container of the workload.** Nothing is added to the pod. But the workload's image may have no `nc` or shell, and running commands in the application's own container is more intrusive than a separate, short-lived one.
2. **An ephemeral container, added by the person running the checker** (chosen). It works for any image, shares the pod's network, and its security context is the checker's choice. It changes the pod's spec for good, which must be explicit.
3. **An ephemeral container added as the tenant admin.** Closest to the tenant's own rights, but Bulkhead's tenant role does not allow it, and widening tenant rights for a test is the wrong trade.
4. **Start a copy of the workload's pod with its labels.** No change to the real pod, but a copy is a different pod: its own controller, volumes and service account are not those of the workload, and a copy with production labels can receive real traffic from a Service.

## Decision

- **In the file:** a flow may have `via`, a label selector for pods in its `from` namespace, for example `via: {app: orders}`. The probe then runs inside one running pod that matches.
- **Opt in:** flows with `via` run only with `--probe-workloads`, because they change running pods. Without it, a file with `via` is refused before the run, with exit code 2. It cannot be combined with `--read-only`. In `--dry-run`, the probes are planned and nothing is sent.
- **Who:** the ephemeral container is added, and the probe run in it, with the identity of the person running the checker, like `--policy-reports`. Before any check runs, access reviews make sure that identity may list pods, add ephemeral containers and exec in each source namespace, with the subresource named explicitly.
- **Which pod:** the first pod by name that matches the selector, is running and ready, is not terminating, is not a test pod of the checker, and does not carry the annotation `bulkhead.io/probe: "false"`, which lets a team exclude a workload. When none matches, the flow is an error, not a pass.
- **The container:**
  - It is named `bhc-<run id>`, with the probe image of the checker (`busybox:1.36`).
  - It has the restricted security context: not root (user 65534), no privilege escalation, all capabilities dropped, the runtime's default seccomp profile.
  - It waits for the checker, at most ten minutes. When the run's flows are done, the checker tells it to exit.
- **On record:** every pod that received a probe container is listed in the report (`workloadProbes`), because the container stays in its spec until the pod is replaced.
- **Pairs:** unchanged. A blocked flow needs an allowed flow to the same destination, from a workload or from the checker's pod. This allows the case that motivated it: `orders` may reach the database, while the checker's plain pod in the same namespace may not.

## Consequences

- A label-based network policy can be tested from the pod it is written for.
- Each run with workload probes leaves one terminated ephemeral container per probed pod, until the pod is replaced. The report names them; the documentation says so before the first run.
- If the run is killed, the probe container exits on its own after ten minutes at most.
- A workload whose image or node cannot pull `busybox:1.36` cannot be probed; the flow is an error with the reason.
