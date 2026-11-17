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
| `to` | Either a Service (`service`, `namespace`, `port`) or a host (`host`, a DNS name or IP address, and `port`). |
| `expect` | `allowed` (FLW-01) or `blocked` (FLW-02). |

The checker reads the file before it contacts the cluster, and refuses it with exit code 2 and a message per mistake: unknown fields (a typo such as `expected:`), a destination with both a service and a host, a port out of range, a flow from a namespace that is not under test.

YAML reads a bare `no`, `yes`, `on`, `off`, `y` or `n` as true or false. If a namespace or service has such a name, put it in quotes (`namespace: "no"`); the checker says so when it happens.

## Every blocked flow needs an allowed flow to the same destination

A blocked flow passes when the connection does not succeed. On its own, that proves nothing: the destination may be down, or the port may be wrong. On a real cluster, a connection to a port that a Service does not have simply times out, exactly like a connection that a network policy drops.

So every `blocked` flow needs an `allowed` flow to the same destination and port in the same file, and its result counts only when that allowed flow connected in the same run. The checker refuses a file without such a pair. When the allowed flow does not connect, the blocked flow is **inconclusive**, and the run fails (exit code 1).

## How a flow is probed

- From the checker's client pod in the `from` namespace, acting as the tenant admin, the checker opens a TCP connection to the destination with `nc -z`, waiting at most 3 seconds. It sends no data.
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

## What a declared flow does not prove

- That the application accepts the tenant: the probe only opens a connection.
- That a policy which selects pods by their own labels works. The probe comes from the checker's client pod, which has the checker's labels, not those of your workload (for example `app: orders`). Probes from inside your own workloads are planned ([phase-2-checklist.md](phase-2-checklist.md), work package 6).
- That other routes are closed, for example through an ingress, a proxy, or a namespace that is not under test.

## Tested

Unit tests cover every outcome in the table above, the pair rule, the per-destination pairing (a failing allowed flow to another destination does not affect a blocked flow), dry runs, and that the destination is passed to the probe as an argument, never as part of a shell command. The end-to-end test on kind creates a shared web service and declares that tenant alpha may reach it and tenant bravo may not:

1. With only the tenants' default policies, the allowed flow fails and the blocked flow is inconclusive.
2. With an egress policy for alpha, both flows hold.
3. When bravo may send anywhere, the blocked flow fails.
