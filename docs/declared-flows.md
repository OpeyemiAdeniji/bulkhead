# Declared flows

The built-in network checks of `bulkhead check` test what every tenant needs: a tenant reaches its own pods and DNS, and nothing else. Your platform probably has flows of its own: a tenant may reach a shared database, and the others may not. Declare them in a file, and the checker tests the cluster against what you declared ([ADR 0019](adr/0019-declared-flows-file.md)).

```bash
bulkhead check --tenant tenant-alpha --tenant tenant-bravo --flows flows.yaml
```

## The file

```yaml
apiVersion: bulkhead.io/v1alpha1
kind: Flows
flows:
  - name: alpha-to-shared-db
    description: The orders service of tenant alpha writes to the shared database.
    from: tenant-alpha
    to:
      service: postgres
      namespace: shared-db
      port: 5432
    expect: allowed

  - name: bravo-not-to-shared-db
    from: tenant-bravo
    to:
      service: postgres
      namespace: shared-db
      port: 5432
    expect: blocked
```

The same example is in [`examples/checker/flows.yaml`](../examples/checker/flows.yaml), and the format is [`schemas/flows.schema.json`](../schemas/flows.schema.json), which editors with YAML support can use for completion and errors.

| Field | Meaning |
|---|---|
| `name` | Names the flow in the reports. Lower-case letters, digits and hyphens, unique in the file. |
| `description` | Optional, up to 500 characters. |
| `from` | The tenant namespace the probe starts from. It must be one of the `--tenant` namespaces of the run. |
| `via` | Optional: labels of your own pods in `from`, for example `{app: orders}`. The probe then runs inside one of them (see below). |
| `to` | Either a Service (`service`, `namespace`, `port`) or a host (`host`, a DNS name or IP address, and `port`). |
| `expect` | `allowed` (FLW-01) or `blocked` (FLW-02). |

The checker reads the file before it contacts the cluster, and refuses it with exit code 2 and a message per mistake: unknown fields (a typo such as `expected:`), a destination with both a service and a host, a port out of range, a flow from a namespace that is not under test.

YAML reads a bare `no`, `yes`, `on`, `off`, `y` or `n` as true or false. If a namespace or service has such a name, put it in quotes (`namespace: "no"`); the checker says so when it happens.

## Every blocked flow needs an allowed flow to the same destination

A blocked flow passes when the connection does not succeed. On its own, that proves nothing: the destination may be down, or the port may be wrong. On a real cluster, a connection to a port that a Service does not have simply times out, exactly like a connection that a network policy drops.

So every `blocked` flow needs an `allowed` flow to the same destination and port in the same file, and its result counts only when that allowed flow connected in the same run. The checker refuses a file without such a pair. When the allowed flow does not connect, the blocked flow is **inconclusive**, and the run fails (exit code 1).

## How a flow is probed

- From the checker's client pod in the `from` namespace (a test pod that the tenant admin starts), the checker opens a TCP connection to the destination with `nc -z`, waiting at most 3 seconds. It sends no data. A flow with `via` is probed from inside one of your own pods instead (see below).
- A Service is reached by its name, `service.namespace.svc`, so DNS is part of the test.
- Results:

| What happened | Allowed flow (FLW-01) | Blocked flow (FLW-02) |
|---|---|---|
| Connected | pass | fail |
| Timed out (dropped) | fail | pass, if its allowed pair connected; otherwise inconclusive |
| Refused at once | fail | pass, if its allowed pair connected; otherwise inconclusive |
| The name does not resolve | fail | error: not tested |

- Each flow is one result, with its name as the subject and `from` and `to` set, in every report format. The HTML report lists declared flows in their own section, not in the network matrix, because the matrix assumes that tenants reach only themselves and DNS.
- The declared flows run after the built-in network checks, and reuse their client pods. In `--read-only` mode they are skipped, like every active check; in `--dry-run` they are planned and nothing is sent.

## Probing from inside a workload

Network policies often select pods by their labels: pods with `app: orders` may reach the database, the rest of the namespace may not. The checker's own client pod does not have those labels, so a flow without `via` tests the namespace, not your workload. Give the flow `via`, the labels of your pods, and the probe runs inside one of them ([ADR 0020](adr/0020-probes-from-workloads.md)):

```yaml
flows:
  - name: orders-to-shared-db
    from: tenant-alpha
    via: {app: orders}
    to: {service: postgres, namespace: shared-db, port: 5432}
    expect: allowed
  - name: rest-of-alpha-not-to-shared-db
    from: tenant-alpha
    to: {service: postgres, namespace: shared-db, port: 5432}
    expect: blocked
```

```bash
bulkhead check --tenant tenant-alpha --tenant tenant-bravo --flows flows.yaml --probe-workloads
```

**Read this before the first run:** the probe runs in an *ephemeral container* added to one of your running pods, and **Kubernetes cannot remove an ephemeral container**. It stays in the pod's spec, stopped, until the pod is replaced (for example by the next rollout). That is why it needs `--probe-workloads`: without it, a file with `via` is refused before the run.

- **Which pod:** the first by name that matches the labels, is running and ready, and is not being deleted. These are never used:
  - the checker's own test pods;
  - pods on the host network, whose traffic is the node's;
  - pods with the annotation `bulkhead.io/probe: "false"`, which is how a team excludes a workload.

  If no pod qualifies, the flow is an error, with the list of pods that were not used and why.
- **The container:**
  - It is named `bhc-<run id>` and uses the image `busybox:1.36`.
  - It has the restricted security context of Pod Security: user 65534, no privilege escalation, all capabilities dropped, the default seccomp profile.
  - It has the pod's network, IP address and labels. It cannot change the pod's other containers.
- **How long:** one container per pod and set of labels in a run, shared by all its flows. When the run's flows are done, the checker tells it to exit. If the run is killed, it exits by itself after ten minutes.
- **Who adds it:** you, with your own identity, not the tenant admin, whose role does not allow ephemeral containers. Before any check runs, the checker makes sure you may list pods, add ephemeral containers (`pods/ephemeralcontainers`) and exec (`pods/exec`) in each namespace, and stops with exit code 3 otherwise.

  Do not trust `kubectl auth can-i update pods/ephemeralcontainers` for that: it reads `ephemeralcontainers` as a pod name and can answer "yes" for a request that is refused. Use `kubectl auth can-i update pods --subresource=ephemeralcontainers`.
- **On record:** the table, JSON (`workloadProbes`) and HTML reports name every pod that received a probe container.
- **Modes:** `--probe-workloads` cannot be combined with `--read-only`. In `--dry-run`, the probes are planned and nothing is sent.

The pair rule does not change: a blocked flow needs an allowed flow to the same destination, from a workload or from the checker's pod. The example above uses exactly that: the allowed flow from the orders pod shows that the database answers, so the blocked flow from the rest of the namespace counts.

## What a declared flow does not prove

- That the application accepts the tenant: the probe only opens a connection.
- Without `via`: that a policy which selects pods by their own labels works. The probe then comes from the checker's client pod, which has the checker's labels, not those of your workload (for example `app: orders`). Use `via` for such flows.
- That other routes are closed, for example through an ingress, a proxy, or a namespace that is not under test.

## Tested

Unit tests cover every outcome in the table above, the pair rule, the per-destination pairing (a failing allowed flow to another destination does not affect a blocked flow), dry runs, and that the destination is passed to the probe as an argument, never as part of a shell command. The end-to-end test on kind creates a shared web service and declares that tenant alpha may reach it and tenant bravo may not:

1. With only the tenants' default policies, the allowed flow fails and the blocked flow is inconclusive.
2. With an egress policy for alpha, both flows hold.
3. When bravo may send anywhere, the blocked flow fails.

A second end-to-end test gives only the pods with `app: orders` in alpha access to the service:

1. The plain flow from the checker's pod now fails, because that pod does not have the label.
2. A file with `via` is refused without `--probe-workloads`.
3. With it, the flow from inside the orders pod passes, and the blocked flow from the rest of the namespace passes.
4. The probe container has exited after the run, and is still listed in the pod's spec.
