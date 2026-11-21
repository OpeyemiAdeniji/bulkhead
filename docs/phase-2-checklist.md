# Phase 2 checklist: the checker (v0.4)

This list holds every piece of work that Phase 2 of the [roadmap](roadmap.md) and [ADR 0012](adr/0012-active-tenant-isolation-checker.md) promise, in the order it is built. A box is ticked in the pull request that does the work, and Phase 2 ends when every box is ticked. Work that is found along the way is added here, not done on the side.

Phase 2 is done when the platform's own CI runs the checker instead of `tests/isolation/run.sh`, and the checker gives the expected results on one cloud cluster that is not kind (roadmap).

## 1. Foundation

- [x] Decide where the checker lives and how it is laid out ([ADR 0018](adr/0018-checker-in-this-repository.md)).
- [x] Go module, `cmd/bulkhead`, `bulkhead version` with the version, commit and build date.
- [x] Exit codes 0, 1, 2 and 3 (ADR 0018).
- [x] The catalogue in Go: every check of the isolation suite has an ID, level, mode, expected result, pair, what it proves and does not prove, how it is run, and the fix. Pod Security, egress to the internet and a shell into another tenant's pods are added. Done when a unit test enforces those fields and pairs, and `bulkhead check --list` prints the catalogue as a table or as JSON (39 checks).
- [x] CI job `checker`: `gofmt`, `go vet`, tests with the race detector, `govulncheck`, `golangci-lint`, and a build that runs the binary. Dependabot watches the Go modules.
- [x] A documentation page of the checks, `docs/checks.md`, generated from the catalogue, with a CI check that it is up to date.

## 2. Safety, before any check talks to a cluster

One item of this package waits for the first active check, because only then does a run create objects: an end-to-end test that interrupts a run on kind and finds nothing left (it is listed under work package 4).

- [x] Connect with the user's kubeconfig and context (`--context`, `--kubeconfig`), and print the cluster, the server version and the user before anything runs.
- [x] Tenant selection: `--tenant` names the namespaces to test. The identities are the subjects of a role binding in each namespace (`--admin-binding`, `--viewer-binding`, by default those of Bulkhead's tenant chart); a group wins over a service account, which wins over a user. Nothing outside the named namespaces is touched. Profiles for other tenancy tools are in work package 7.
- [x] Every object the checker creates has the labels `app.kubernetes.io/managed-by: bulkhead-check` and `bulkhead.io/run-id: <id>`, and the name prefix `bhc-`. The sandbox refuses to create an object without them.
- [x] Clean-up at the end, on Ctrl+C and on SIGTERM, and `bulkhead cleanup --run-id` (or `--all-runs`) for objects left after a crash. Clean-up deletes only objects with the checker's labels, in the named namespaces.
- [x] A timeout for every check and for the whole run.
- [x] `--dry-run` lists every object that would be created and every request that would be sent, without sending them.
- [x] `--read-only` skips active checks, and the sandbox refuses every request that would store an object, so a mistake in a check cannot store one either.
- [x] The tenant identity is confirmed with a `SelfSubjectReview` before any check runs, and the run stops when it does not match.
- [x] The checker refuses system namespaces (`default`, `kube-*`) as tenants, unless `--force-namespaces` is given.

## 3. Read-only checks

- [x] ACC-01 to ACC-05 (access between tenants). A read of a name that does not exist tells "allowed" (not found) from "refused" (forbidden) without reading any real secret.
- [x] GRD-01 to GRD-10 (the tenant cannot change its guardrails or open a shell in another tenant's pods), with server-side dry runs and access reviews.
- [x] ADM-01 and ADM-02 (Pod Security), with server-side dry runs.
- [x] QTA-03 to QTA-07 (service types, the per-container maximum, default limits), with server-side dry runs.
- [x] Each blocked result names the layer that decided it: access rules, Pod Security, admission (which policy or webhook), quota or limit range. A pass from an unexpected layer is reported. On kind, with the built-in admin role given to a tenant, GRD-02 was reported as refused by the admission policy protect-platform-objects instead of access rules.

## 4. Active checks

- [x] An end-to-end test that interrupts a run on kind while its test pods exist, and finds nothing left (from work package 2). The run stops with exit code 3.
- [x] Test pods with placement on two nodes, readiness waits (a TCP probe for servers, so ready means listening), clear errors for pods that cannot start (for example ImagePullBackOff), and checks across nodes skipped when the cluster has only one schedulable node. The pods are created as the tenant admin, through the sandbox.
- [x] NET-01 to NET-06 (network), in both directions between every pair of tenants, on the same node and across nodes. On kind: 16 of 16. Without network policies, exactly the 10 blocked results fail.
- [x] RES-01 to RES-05 (noisy neighbour), measured from cgroup v2. On kind: the CPU hog used 0.50 CPUs against its limit of 0.50 and was throttled; the memory hog was OOMKilled; the other tenant's server on the same node kept answering without a restart.
- [x] QTA-01, QTA-02, QTA-08 and QTA-09 (the quota with real pods), reading the tenant's quota instead of fixed numbers. The test pods of earlier categories are removed first, and the run waits until the quota no longer counts them. QTA-08 creates at most 25 pods per tenant and skips a larger free pod count.
- [x] Results of a "blocked" check are only valid when its pair passed: otherwise they are reported as inconclusive and fail the run. When the pair has a result for the same tenant, that result decides. The pairs of selected checks are added to the run.

## 5. Reports and exit codes

- [x] A terminal table with the result, the deciding layer and the fix for each failure.
- [x] JSON with a published JSON Schema (`schemas/check-report.schema.json`, `apiVersion: bulkhead.io/v1alpha1`), including the versions, mode, tenants, the text of every check, the requests of a dry run and the exit code. A unit test validates a report with every status, and the end-to-end test validates the report of a real run on kind. The tenancy profile is in `run.profile` since work package 7.
- [x] JUnit XML for CI systems.
- [x] SARIF 2.1.0 for GitHub code scanning (`docs/check-reports.md`).
- [x] An HTML report that can be kept as evidence, with a source by destination matrix for the network checks (including DNS and the internet). One self-contained file, escaped with `html/template`, light and dark, printable.
- [x] `PolicyReport` objects (`wgpolicyk8s.io`) in the cluster, optional. `--policy-reports` writes one report per tenant namespace, checked before the run; on kind a full run stored all 71 results, a later run replaced them, and without the API the run stops before any check ([check-reports.md](check-reports.md)).
- [x] Every check maps to the archived Kubernetes multi-tenancy benchmark IDs, the CIS Kubernetes Benchmark and the NSA and CISA hardening guidance where they overlap, after reading those documents. [benchmarks.md](benchmarks.md): 9 of 19 multi-tenancy items, 7 of 34 CIS items (section 5) and 10 of 18 NSA and CISA recommendations have a check; the rest are listed as not tested.

## 6. Beyond the built-in checks

- [x] A file in which teams declare their own expected flows ("alpha may reach the shared database, nothing else may"), with a JSON Schema. `--flows` ([declared-flows.md](declared-flows.md), [ADR 0019](adr/0019-declared-flows-file.md)): FLW-01 and FLW-02, with a pair per destination; tested on kind against a shared service.
- [x] Probes from inside a running workload with an ephemeral container, as an option. `via` in the flows file with `--probe-workloads` ([ADR 0020](adr/0020-probes-from-workloads.md)); tested on kind with a policy that only the `app: orders` pods may use.
- [x] Research category, off by default: XNS-01 and XNS-02 (cross-namespace references through installed operators), with its own safety rules and an ADR. [ADR 0021](adr/0021-operator-reference-checks.md): after reading the paper, they test at admission with dry runs instead of making an operator act; [operator-references.md](operator-references.md).

## 7. Tenancy profiles

- [x] Profile "namespaces": a plain namespace per tenant, as Bulkhead's charts make. Tested in CI. `--profile namespaces` (the default, [ADR 0022](adr/0022-tenancy-profiles.md)); the end-to-end test also runs it on two namespaces that Bulkhead did not make, with their own role binding (`--admin-binding`).
- [ ] Profile "capsule", tested in CI against Capsule.
- [ ] Profile "vcluster", tested in CI against vCluster.

## 8. Tests

- [ ] Unit tests for every check, with a fake cluster where possible.
- [ ] End-to-end tests on kind with both network plugins.
- [ ] The automatic break test: remove each protection on purpose and expect exactly the matching checks to fail.

## 9. The platform uses its own checker

- [ ] The `isolation` and `compatibility` CI jobs run the checker. `tests/isolation/run.sh` is removed once the checker covers all its checks, and the measurements say when that happened.
- [ ] One run on a cloud cluster (GKE, which is kept for this; EKS when its run is done) with the expected results, recorded in `docs/measurements.md`.

## 10. Distribution

- [ ] An ADR for the release tooling.
- [ ] Release binaries for Linux, macOS and Windows on amd64 and arm64, with checksums, signatures, provenance and an SBOM.
- [ ] A signed container image on ghcr.io.
- [ ] A kubectl plugin (`kubectl bulkhead`) and a krew manifest.
- [ ] A GitHub Action, which posts a short pull request comment with the failed checks and their fixes.

## 11. Documentation and release

- [ ] A user guide for the checker: install, first run, read-only mode on production, reading the report, and what a pass does not prove.
- [ ] README, roadmap, install guide and changelog updated.
- [ ] Release v0.4.0.
