# Bulkhead

> Self-service tenants on a shared Kubernetes cluster, with isolation that tests prove. A team asks for a space by adding one file in a pull request, the platform builds it, and admission policies keep it the way it was built.

[![CI](https://github.com/OpeyemiAdeniji/bulkhead/actions/workflows/ci.yml/badge.svg)](https://github.com/OpeyemiAdeniji/bulkhead/actions/workflows/ci.yml)

A bulkhead is the sealed wall inside a ship that keeps one flooded compartment from sinking the rest. The project was called `k8s-tenant-platform` until 2026-11-14.

**Status: v0.2.0.** A local reference implementation on kind. It is not a production platform, it has no real tenants, and no real team has used it yet. The section "Known limits" says what is missing, and [the roadmap](docs/roadmap.md) says what comes next.

## The idea

In ticket-based operations, a team that needs a place to run workloads opens a ticket and waits for someone to build a namespace by hand. Hand-built namespaces drift apart, and nobody tests whether they are really isolated.

Here, a team adds one file, `tenants/<name>.yaml`, in a pull request. Continuous integration checks the file against a JSON Schema and renders what it would create. The merge is the approval. The platform then creates a namespace with a resource quota, default limits, access rules and default-deny network policies. Admission policies reject unsafe workloads with a message that says how to fix them, and they stop anyone from removing the platform's own controls. Every isolation claim is backed by a test that performs the real action, in continuous integration.

## What this proves

- A tenant contract as a JSON Schema, with tests that show that bad files fail for the right reason, and not just that they fail.
- A Helm chart that turns the contract into the same baseline for every tenant, checked against the Kubernetes API schema without a cluster.
- Isolation tests that perform real actions. Every "must be blocked" check has a matching "must work" check, and break tests show that each suite fails when the protection is removed.
- Admission policies with developer-friendly messages and runbooks, tested offline and live.
- Policy exceptions that always have an owner, a reason and an end date, checked before they reach the cluster.
- A second layer that keeps a tenant isolated even when someone gives it too many rights.
- Measured numbers only, each with its method and its limits, and architecture decision records for every choice.

## How it works

1. A team adds `tenants/<name>.yaml` in a pull request.
2. Continuous integration checks the file against the JSON Schema, renders the chart for it, and checks every rendered object against the Kubernetes API schema.
3. The merge to `main` is the approval.
4. `make tenants-apply` renders the file with `helm template` and applies it. This step is manual until Argo CD arrives (v0.5 in [the roadmap](docs/roadmap.md)).
5. The cluster now has a namespace `tenant-<name>` with its quota, limits, access rules and network policies. Kyverno judges every workload in it against the platform's policies.

The cluster is a kind cluster with one control-plane node and two workers, with Calico as the network plugin ([ADR 0003](docs/adr/0003-network-plugin-calico.md)) and Kyverno as the policy engine ([ADR 0004](docs/adr/0004-kyverno-admission-policy-engine.md)).

## What a tenant file looks like

```yaml
schemaVersion: 1
name: alpha
owner:
  team: alpha
  email: team-alpha@example.com
costCentre: cc-1001
environment: dev
size: small
access:
  admins:
    - alpha-admins
  viewers:
    - alpha-viewers
```

The size selects the quota. These tiers are design choices, not measurements: medium is twice small, and large is four times small.

| Size | requests (CPU, memory) | limits (CPU, memory) | pods | services | volume claims |
|---|---|---|---|---|---|
| small | 500m, 512Mi | 1, 1Gi | 10 | 5 | 2 |
| medium | 1, 1Gi | 2, 2Gi | 20 | 10 | 4 |
| large | 2, 2Gi | 4, 4Gi | 40 | 20 | 8 |

A container that sets no resources gets requests of 50m CPU and 50Mi, and limits of 100m CPU and 100Mi. That is one tenth of the small tier, so a tenant can run all the pods that its size allows even when none of them sets resources. Workloads that need more set their own requests and limits.

## What every tenant gets

- A namespace `tenant-<name>` with the labels `tenant`, `team`, `cost-centre` and `environment`, an owner annotation, and the Pod Security `restricted` level.
- A `ResourceQuota` for the size, with zero NodePort and LoadBalancer services, and a `LimitRange` with the defaults above.
- A custom `tenant-admin` role for the admin groups ([ADR 0007](docs/adr/0007-custom-tenant-admin-role.md)). It can run workloads, but it cannot change the quota, the limit range or the network policies, it cannot grant access, and it can only read endpoints, so it cannot redirect traffic (CVE-2021-25740). Viewer groups get the built-in `view` role, which does not include secrets.
- Three network policies that deny all traffic, allow DNS lookups, and allow traffic inside the namespace. They use only the standard NetworkPolicy API.
- Every object carries `app.kubernetes.io/managed-by: bulkhead`, which the policies below use.

## Guardrails

Two Kyverno policies apply to tenant namespaces only (selected by the label above, [ADR 0009](docs/adr/0009-scope-policies-by-namespace-label.md)). Each message states the problem, the fix and a runbook.

| Policy | What it refuses | Runbook |
|---|---|---|
| `disallow-latest-image-tag` | A pod with an image tagged `latest` or with no tag, on creation, and on an update that brings in such an image ([ADR 0010](docs/adr/0010-policy-judges-updates-for-new-images-only.md)). Deployments, CronJobs and other workload kinds are judged too. | [policy-disallow-latest-tag](docs/runbooks/policy-disallow-latest-tag.md) |
| `protect-platform-objects` | A change or deletion of the network policies, quota, limit range, role or role bindings that the platform created, unless it comes from a platform admin or from Kubernetes itself. It holds even when someone gives the tenant too many rights ([ADR 0015](docs/adr/0015-protect-platform-objects.md)). | [policy-protect-platform-objects](docs/runbooks/policy-protect-platform-objects.md) |

What a developer sees:

```text
Error from server: admission webhook "vpol.validate.kyverno.svc-fail" denied the request:
Policy disallow-latest-image-tag failed: Image busybox:latest uses the tag latest or has no tag.
Fix: pin the image to a version, for example busybox:1.36, or to a digest. Runbook: https://...
```

**Exceptions.** When a workload really needs to skip a policy, the team adds a file to [`exceptions/`](exceptions/README.md) in a pull request. Continuous integration refuses the file unless it has an absolute end date at most 90 days ahead, an owner, a reason, a known tenant and policy, and a condition that narrows it to the workloads that need it ([ADR 0014](docs/adr/0014-exception-files-checked-in-ci.md)). Kyverno deletes the exception at its date ([ADR 0008](docs/adr/0008-kyverno-cel-policies-and-expiring-exceptions.md)).

**When Kyverno is down.** The guardrails protect against mistakes, not against someone who stops Kyverno on purpose. When Kyverno is scaled to zero, it removes its webhooks and the policies do not apply. A crash of the container refused pod creation in tenant namespaces for about 8 seconds, and a rolling restart opened no gap in one test. A tenant admin cannot stop Kyverno. The details and their limits are in [ADR 0011](docs/adr/0011-kyverno-outage-behaviour.md).

## Install on your own cluster

Bulkhead is two Helm charts: `charts/bulkhead-platform`, installed once per cluster next to Kyverno, and `charts/bulkhead-tenant`, installed once per tenant file. Everything that differs between clusters is a value: who your platform admins are, where DNS runs, the namespace prefix and the Pod Security level ([ADR 0016](docs/adr/0016-two-charts-configurable-per-cluster.md)). From the next release on, both charts are published to `oci://ghcr.io/opeyemiadeniji/charts`, signed, with a record of how they were built ([ADR 0017](docs/adr/0017-signed-chart-releases.md)).

[docs/install.md](docs/install.md) walks through it for Amazon EKS, Google Kubernetes Engine, Azure Kubernetes Service and other clusters, including the one thing Bulkhead cannot do for you: a network plugin that enforces network policies. Besides kind, the guide has been followed on GKE Standard with Dataplane V2, where all isolation and live policy checks passed after a DNS fix ([measurements](docs/measurements.md#first-cloud-run-gke-standard-phase-1)). EKS and AKS have not been run yet.

## Quick start (local demo on kind)

You need Docker, bash, make, curl, git, pipx and Python 3. The tools are pinned to the versions that were measured, and every download is checked against its checksum. Continuous integration runs this block as written on a fresh machine on every pull request (`scripts/readme-quickstart.sh`), so it cannot go stale.

```bash
git clone https://github.com/OpeyemiAdeniji/bulkhead.git && cd bulkhead
bash scripts/install-tools.sh        # kind, kubectl, Helm, kubeconform and the Kyverno CLI in ~/.local/bin
export PATH="$HOME/.local/bin:$PATH"    # if ~/.local/bin is not on your PATH yet
python3 -m venv .venv && source .venv/bin/activate && pip install -r requirements.txt
pipx install pre-commit && make setup   # git hooks, once per clone

make help                  # list all targets

# Offline, no cluster needed
make validate              # check the tenant files against the contract
make test-contract         # valid tenant files pass, invalid ones fail for the right reason
make chart-test            # lint and render the chart for every tenant, and check it against the API schema
make policy-test           # Kyverno tests, and a check that they can fail
make validate-exceptions   # check the policy exception files
make test-exceptions       # valid exception files pass, invalid ones fail for the right reason

# On a local cluster
make cluster-up            # build the kind cluster and install Calico (about 3 to 4 minutes)
make tenants-apply         # render every tenant file and apply it
make test-isolation        # 41 isolation checks
make kyverno-up            # install Kyverno, the bulkhead-platform chart and the exceptions
make test-policy-live      # 31 live policy checks
make cluster-down          # remove the cluster
```

## Supported platforms

Tested:

- Ubuntu under WSL2 on Windows 11, x86-64, with Docker Desktop.
- The `ubuntu-latest` runner of GitHub Actions, x86-64.

Every pull request runs the isolation and live policy suites on these combinations, all on kind:

| Kubernetes | Network plugin | CI job |
|---|---|---|
| 1.37.0 | Calico 3.33.0 | `isolation` and `policy-live` (required) |
| 1.36.4 | Calico 3.33.0 | `compatibility` |
| 1.35.8 | Cilium 1.20.4 | `compatibility` |
| 1.37.0 | Cilium 1.20.4 | `compatibility` |

`K8S_VERSION=1.35.8 CNI=cilium make cluster-up` builds any of them locally. Calico 3.33.0 needs Kubernetes 1.36 or newer, because its install creates `MutatingAdmissionPolicy` objects of an API version that 1.35 does not serve, so 1.35 is tested with Cilium. The kubectl client is 1.37, which is two minor versions newer than a 1.35 cluster, one more than Kubernetes supports. The suites passed with it, but that is the reason if a command behaves differently there.

The cluster used about 1.8 GiB of memory and Kyverno about 0.5 GiB more, on a machine with 7.7 GiB. The minimum has not been determined.

Not tested: macOS, Windows without WSL2, and ARM processors. The quick start has also not been followed from a clean copy of the repository yet. `scripts/install-tools.sh` downloads `linux-amd64` binaries, and `tests/isolation/run.sh` uses `mapfile` and `date +%s%N`, which need bash 4 and GNU date. Tracked in [issue 14](https://github.com/OpeyemiAdeniji/bulkhead/issues/14).

## What is proven, and how

Every suite below runs in continuous integration on every pull request.

**Isolation** (`make test-isolation`, 41 checks against a running cluster):

| Property | Checks |
|---|---|
| A tenant cannot read another tenant's secrets, and viewers cannot read secrets | 6 |
| A tenant cannot delete its default-deny policy, quota or limit range, cannot create role bindings, cannot loosen the Pod Security level, and cannot create endpoints. It can still read endpoints and exec into its own pods | 8 |
| Traffic between pods of one tenant works, on the same node and across nodes. DNS works. Traffic between tenants is blocked, on the same node and across nodes | 14 |
| A CPU hog is held at its limit, a memory hog is killed at its limit, and a quiet tenant on the same node keeps answering | 5 |
| A second pod above the quota, NodePort and LoadBalancer services, and a container above the tier maximum are rejected. A pod without resources gets the default limit. Ten pods without resources fit in the small tier, and an eleventh is rejected by the pod count | 8 |

**Policies, live** (`make test-policy-live`, 29 checks as a tenant admin on a running cluster with Kyverno): `latest` images are denied on pods, Deployments and CronJobs, at creation and on image changes; workloads that already ran `latest` accept unrelated edits; a tenant admin cannot create policies or exceptions; the example exception is accepted by the cluster; and with the built-in `admin` role bound by mistake, the platform's network policy and role binding still cannot be deleted, unlabelled or loosened, while the platform admin can still change them and a tenant namespace can still be removed.

**Policies and exceptions, offline:** `make policy-test` runs `kyverno test` and checks that the suite fails when the `latest` check is removed. `make test-exceptions` checks 13 exception files that must fail for a stated reason, and one that must pass.

**Break tests** remove a protection on purpose and expect exactly the matching checks to fail. Results so far: deleting all six network policies failed exactly the eight network checks that must be blocked (when the suite had 36 checks); the old default limits failed exactly the three default-limit checks; the old tenant role failed exactly the endpoints check; and deleting `protect-platform-objects` failed exactly its four "must be blocked" checks.

## Measured results

One machine (Windows 11 with WSL2, 7.7 GiB of memory, 8 CPUs) and GitHub's `ubuntu-latest` runners. The setup and the method of each number are in [docs/measurements.md](docs/measurements.md).

| What | Result |
|---|---|
| Cluster start, `make cluster-up` | 173 s, 204 s and 211 s in three runs, and 235 s on 2026-11-14. `kind create cluster` took between 35 s and 45 s of that |
| Memory cost of the cluster | about 1.8 GiB, as the difference in `free -h` before and after. The precision is about 0.1 GiB |
| Memory cost of Kyverno (four controllers, one replica each) | about 0.5 GiB by `free -h` and 0.55 GiB by `docker stats` |
| `isolation` CI job | 126 s to 195 s, median 156 s, in 47 successful runs |
| `policy-live` CI job | 126 s to 299 s, median 142 s, in 28 successful runs. The 299 s run is not explained |

## Known limits

- It is a local reference implementation. It has no high availability, no real tenants and no on-call process.
- The kind nodes are containers on one machine and share its CPU and memory. The noisy neighbour checks show that pod limits are enforced, not how a full node behaves.
- The network checks show that traffic between two tenants is blocked. They do not show whether the ingress rules or the egress rules did it, because the policies deny both directions at once. Traffic through a Service or through the host network is not covered.
- No check yet tries to run a privileged pod (Pod Security is set to `restricted` on every namespace), or to reach the internet (the baseline denies egress).
- Removing a tenant file does not remove the tenant until Argo CD arrives. For now, a tenant is removed by deleting its namespace.
- The policies are not tested on StatefulSets, DaemonSets and Jobs, during a Helm upgrade of Kyverno or a node drain, or with two Kyverno replicas. The Kyverno CLI cannot test updates and deletions offline, so those parts are tested live only.
- The charts have been run on one cloud cluster, GKE Standard, once. The EKS and AKS parts of [docs/install.md](docs/install.md) are written from the providers' documentation.
- Not built yet: the pull request preview comment, the Trivy gate, cost estimates and the platform SLO.
- The size tiers and the default limits have not been compared with real workloads.
- Everything was tested with kind v0.33.0, Kubernetes v1.37.0, Calico v3.33.0 and Kyverno v1.19.2 only.
- The sample tenants use `example.com` addresses and are not real teams.
- The CI jobs have failed for real reasons, each written down: the isolation job twice at the start, because a check asked for an exact label that the runner reports differently ([runbook](docs/runbooks/isolation-job-failed.md)); and the `policy-live` job twice, once because Kyverno's webhook was not answering yet, and once because `make kyverno-up` could not apply two policies. Both are fixed.

## Design decisions

| ADR | Decision |
|---|---|
| [0001](docs/adr/0001-namespace-per-tenant.md) | One namespace per tenant on a shared cluster |
| [0002](docs/adr/0002-pull-request-not-web-portal.md) | A pull request as the interface, and no web portal |
| [0003](docs/adr/0003-network-plugin-calico.md) | Calico as the network plugin, with a measured test of the default plugin |
| [0004](docs/adr/0004-kyverno-admission-policy-engine.md) | Kyverno as the admission policy engine |
| [0005](docs/adr/0005-tenant-contract-yaml-json-schema.md) | The tenant contract as plain YAML with a JSON Schema |
| [0006](docs/adr/0006-helm-chart-renders-tenant-file.md) | A Helm chart renders the tenant file |
| [0007](docs/adr/0007-custom-tenant-admin-role.md) | A custom role for tenant admins |
| [0008](docs/adr/0008-kyverno-cel-policies-and-expiring-exceptions.md) | CEL-based Kyverno policies, and exceptions that expire through a TTL label |
| [0009](docs/adr/0009-scope-policies-by-namespace-label.md) | Scope policies to tenant namespaces by label |
| [0010](docs/adr/0010-policy-judges-updates-for-new-images-only.md) | The image policy judges updates, but only for new images |
| [0011](docs/adr/0011-kyverno-outage-behaviour.md) | What the guardrail does while Kyverno is down |
| [0012](docs/adr/0012-active-tenant-isolation-checker.md) | An active tenant isolation checker that runs on any cluster (proposed) |
| [0013](docs/adr/0013-rename-to-bulkhead.md) | Rename the project to Bulkhead |
| [0014](docs/adr/0014-exception-files-checked-in-ci.md) | Exception files are checked in CI, with an owner, a reason and at most 90 days |
| [0015](docs/adr/0015-protect-platform-objects.md) | A policy protects the platform's objects in tenant namespaces, as a second layer |
| [0016](docs/adr/0016-two-charts-configurable-per-cluster.md) | Two Helm charts, configurable per cluster, with the kind demo as one example |
| [0017](docs/adr/0017-signed-chart-releases.md) | Charts are released to ghcr.io by a workflow, signed without a key, with build provenance |

## Roadmap

The project is becoming an open source project for other engineers to install and use. The full plan, with what "done" means for each phase, is in [docs/roadmap.md](docs/roadmap.md).

| Version | Scope |
|---|---|
| v0.1 | Tenant contract, namespace, quota, access, network policies, isolation tests. Released. |
| v0.2 | Kyverno guardrails with friendly messages, expiring exceptions checked in CI, protection of platform objects, read-only endpoints. Released. |
| v0.3 | Installable by others: configurable chart, signed releases on `ghcr.io`, tests on three Kubernetes versions |
| v0.4 | `bulkhead check`: a CLI that tests tenant isolation on any cluster ([ADR 0012](docs/adr/0012-active-tenant-isolation-checker.md)) |
| v0.5 | Self-service with Argo CD, pull request preview, platform SLO, cost showback, Trivy gate |
| v1.0 | Public website, stable interfaces, launch |
| later | Amazon EKS |

## Repository layout

```
tenants/               one file per tenant, the only place a team edits
exceptions/            policy exceptions, one per file, checked in CI
schemas/               the JSON Schema of the tenant contract
charts/bulkhead-tenant/    the Helm chart that renders one tenant
charts/bulkhead-platform/  the Kyverno policies and their offline tests, once per cluster
examples/kind/         the local demo: kind configuration, Calico checksums, pinned Kyverno, kind values
scripts/               cluster, chart, install and validation scripts
tests/contract/        tenant files that must pass and files that must fail
tests/exceptions/      exception files that must pass and files that must fail
tests/isolation/       the isolation suite
tests/policy-live/     the live policy suite
docs/adr/              architecture decision records
docs/runbooks/         runbooks for each policy and for a real CI failure
docs/measurements.md   setup, methods and numbers
docs/roadmap.md        the plan to v1.0
```

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md). To report a way around one of the claims above, see [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
