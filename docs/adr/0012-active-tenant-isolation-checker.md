# 0012. Build an active tenant isolation checker that runs on any cluster

- Status: proposed
- Date: 2026-11-14

## Context

The project is moving from a portfolio project to an open source project that other engineers install and use (`docs/roadmap.md`). The platform itself, a namespace per tenant with quotas, access rules, default-deny network policies and admission policies, is a known pattern. Several maintained tools already create tenants, so a new way to create them is not a reason for anyone to adopt this project.

The part of this project that is less common is the isolation suite (`tests/isolation/run.sh`). It does not read the configuration and infer what a tenant could do. It acts as a tenant and tries: it sends traffic between tenants on the same node and across nodes, reads another tenant's secrets, deletes its own guardrails, and runs CPU and memory hogs next to a quiet tenant. Every check that must be blocked has a matching check that must work, so a cluster that blocks everything cannot pass by accident. Today the suite only runs against this project's own kind cluster and tenants.

What exists today, read on 2026-11-14 (repository data from the GitHub API on that date; nothing below was installed or tried):

| Project | What it does | Status |
|---|---|---|
| Kubernetes multi-tenancy benchmarks (`kubectl-mtb`) | Benchmark of multi-tenant configuration, checked by a kubectl plugin | Archived on 2023-06-26 in `kubernetes-retired/multi-tenancy`. Its README said it was not ready for use. |
| NetAssert (Control Plane) | Active network tests between workloads, declared in a file | Maintained, about 390 stars. Network only. |
| Cyclonus | Network policy conformance tests | Last push 2024-07. Points to the static Policy Assistant in `kubernetes-sigs/network-policy-api`. |
| illuminatio | Network policy validator | Archived, recommends Cyclonus. |
| Kubescape, kube-bench | Security posture and CIS checks of the configuration | Maintained and widely used. They read configuration from the admin's side and are not about tenants. |
| Capsule, vCluster | Create and manage tenants | Maintained and widely used. They create isolation and do not test it end to end. |
| `kubectl-audit-tenant` | Checks from the tenant's side whether a namespace is fenced, with permission checks and server-side dry runs. It never changes the cluster. | First release 2026-10-26, v0.1.2, no stars yet. Read-only, so no traffic and no resource tests. |

The gap, as far as this reading shows: no maintained tool runs active, end-to-end isolation tests across network, access, resources and admission in one run, against any cluster, with a report that a platform team or an auditor can keep. The closest projects either cover the network only (NetAssert), read configuration (Kubescape, kube-bench, `kubectl-audit-tenant`), or are archived (the benchmarks).

Who needs it: platform teams that share a cluster between teams and must show, not assert, that teams are separated. That evidence is asked for after a cluster upgrade, after a network plugin change, when a new tenancy tool is introduced, and in audits that require segregation of environments or customers.

## Options considered

1. **Keep the suite inside this project only.**
   - Pros: no new work.
   - Cons: nobody outside this project can use it, and the platform alone repeats what Capsule and others already do.
2. **Contribute active checks to an existing tool** (NetAssert for traffic, or `kubectl-audit-tenant` for the tenant view).
   - Pros: no new project to maintain, and existing users get the checks.
   - Cons: NetAssert is about network flows and has no concept of tenants, access or quotas. `kubectl-audit-tenant` is read-only by design ("it never changes the cluster"), and active tests would break that promise. Both owners would have to want it.
3. **Build a separate checker, the `bulkhead check` command (ADR 0013), that runs active tests against any cluster.**
   - Pros: it fills the gap above, and it is useful to people who never use this platform. The isolation suite is already its specification.
   - Cons: a second deliverable to build, release and maintain. Active tests create pods and send traffic, so they need clear safety rules, clean-up and a dry-run mode. It must work with tenancy models it was not built with.

## Decision

Build the checker (option 3), in Go, as a CLI that is also usable as a kubectl plugin and a GitHub Action. The name is decided separately (`docs/roadmap.md`).

- **Active by default, with a read-only mode.** The active mode creates short-lived test pods in the namespaces that the user names, with labels that identify them, and removes them at the end, also after an interruption. The read-only mode runs only permission checks and server-side dry runs.
- **Paired checks.** Every "must be blocked" check has a "must work" check. A result is valid only when its pair passed.
- **A numbered catalogue** (for example `NET-01`), with for each check: what it proves, what it does not prove, how it is run, and the fix when it fails.
- **Tenancy profiles.** The first profile is a plain namespace per tenant. Profiles for Capsule and vCluster follow, each tested against that tool in continuous integration.
- **Reports:** a terminal table, JSON, JUnit for continuous integration, and an HTML report that can be kept as evidence. The exit code says whether every selected check passed.
- **Go,** because it is the language of the Kubernetes ecosystem: `client-go` is the reference client, a single static binary is easy to install on every platform, and most contributors in this field read it.
- The bash suite stays until the checker covers all 36 checks. Then the platform's own continuous integration runs the checker instead.

## Design features

| Feature | What the checker does |
|---|---|
| Levels, categories and stable IDs | The catalogue has levels (basic, hardened) and categories. Each check also lists the ID it covers in the archived Kubernetes multi-tenancy benchmarks, for users who know them. |
| The deciding layer | A blocked request reports whether access rules, admission, a network policy, a quota or a limit range stopped it. A pass that came from the wrong layer is reported. |
| The tenant's own identity | Requests impersonate the tenant's group or service account, and a `SelfSubjectReview` confirms the identity before any check runs. |
| Stated limits | Every check states what a pass does not prove, as the ADRs of this project already do. |
| Team-declared expectations | Besides the built-in checks, a file can list extra expectations ("alpha may reach the shared database, nothing else may"). |
| Probes from inside real workloads | An option runs network probes from an ephemeral container in a running pod, so the probe has the pod's real labels and policies. |
| Network matrix | The HTML report shows a source by destination grid for every network check, with the expected and the observed result. |
| Standard formats | Checks map to the CIS Kubernetes Benchmark and the NSA and CISA hardening guidance where they overlap. Results are available as SARIF for GitHub code scanning, and as `PolicyReport` objects (`wgpolicyk8s.io`) for dashboards that already read them. |
| Pull request summary | The GitHub Action posts a short comment with the failed checks and their fixes. |
| Cross-namespace references through operators | A research category, off by default: for operators installed in the cluster, the tenant creates its own namespaced custom resources that point at another tenant's objects, and the checker looks for whether the operator follows the reference. |

The last row matters for the problem statement. The NDSS 2026 paper "Breaking the Bulkhead" (Chen et al.) analysed 2,268 operators on GitHub, found more than 14 percent potentially vulnerable to cross-namespace references that break namespace isolation, and reports 7 CVEs. A namespace with perfect access rules and network policies can still leak through an operator that acts on another namespace for the tenant. No tool listed above tests that on a live cluster. It is also the hardest category to do safely, which is why it is a research item and opt-in.

## Consequences

- The project gets a reason to exist for people who already have a tenancy tool.
- The checker has to be safe to run on clusters that are not ours. Safety rules, clean-up and the read-only mode come before new checks.
- Results depend on the network plugin, the node layout and the tenancy tool. Every report states the versions and the profile that it ran with, and the documentation says what a pass does not prove.
- A second release stream: versioned binaries, a container image and a GitHub Action, all signed.
- Not verified yet: that the gap is still open when the checker ships, and that platform teams want active tests on their clusters. The roadmap includes asking users before v1.0.
- Revisit this decision if a maintained project starts to run active tenant isolation tests, in which case contributing to it is better than competing, or if the safety cost of active tests turns out to be too high for the intended users.

## Sources read

- Archived benchmarks: https://github.com/kubernetes-retired/multi-tenancy/tree/master/benchmarks
- NetAssert: https://github.com/controlplaneio/netassert
- illuminatio: https://github.com/inovex/illuminatio
- Cyclonus: https://github.com/mattfenwick/cyclonus
- Network Policy API (Policy Assistant): https://github.com/kubernetes-sigs/network-policy-api
- `kubectl-audit-tenant`: https://github.com/BodhiSatvik/kubectl-audit-tenant
- Capsule: https://github.com/projectcapsule/capsule
- Chen, Guo, Jin, Li and Chen, "Breaking the Bulkhead: Demystifying Cross-Namespace Reference Vulnerabilities in Kubernetes Operators", NDSS 2026: https://www.ndss-symposium.org/ndss-paper/breaking-the-bulkhead-demystifying-cross-namespace-reference-vulnerabilities-in-kubernetes-operators/ (preprint: https://arxiv.org/abs/2507.03387)
- Policy Report API: https://github.com/kubernetes-sigs/wg-policy-prototypes
