# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **The start of `bulkhead check`**, the tenant isolation checker (ADR 0012), in this repository as one Go module (ADR 0018). `bulkhead version` prints the version, and `bulkhead check --list` prints the catalogue of 39 checks as a table or as JSON, filtered by category and level. Every check has an ID, a level, a mode (read-only or active), the expected result, the "must work" check it is paired with, what it proves and does not prove, how it is run, and the fix, and unit tests enforce those rules. Running the checks against a cluster is not built yet. The work is tracked in `docs/phase-2-checklist.md`.
- CI job `checker`: `gofmt`, `go vet`, tests with the race detector, `govulncheck`, `golangci-lint`, and a build that runs the binary. Dependabot watches the Go modules. `make checker-build` and `make checker-test` run the same locally.
- `docs/checks.md`, generated from the catalogue by `make checks-doc`. A unit test fails when the page and the catalogue differ.
- **The safety rules of the checker** (work package 2). `bulkhead check --tenant <namespace>...` connects with your kubeconfig (`--context`, `--kubeconfig`), prints the cluster, its version and your identity, and reads each tenant's admin and viewer from role bindings in its namespace. It confirms every tenant identity with a `SelfSubjectReview` through impersonation before any check, and stops when one does not match. All requests go through one sandbox, which:
  - refuses to create an object without the run's labels (`app.kubernetes.io/managed-by: bulkhead-check`, `bulkhead.io/run-id`) and the name prefix `bhc-`;
  - sends nothing in `--dry-run`, and prints every request the run would send instead;
  - refuses every request that would store an object in `--read-only`.

  Clean-up runs at the end, also after Ctrl+C, SIGTERM or the time limit (`--timeout`, `--check-timeout`), and `bulkhead cleanup --run-id` (or `--all-runs`) removes what a killed run left. System namespaces are refused as tenants unless `--force-namespaces` is given. A "blocked" result counts only when its pair passed; otherwise it is inconclusive and fails the run. No check is implemented yet, so a run reports every check as not implemented and exits with 3.
- `tests/checker/e2e.sh` (`make test-checker-e2e`) runs the checker against the kind cluster in the `isolation` CI job: dry run, live and read-only runs with identity confirmation, refused and missing namespaces, and clean-up of a killed run's objects only.
- **The 21 read-only checks of `bulkhead check`** (work package 3): access between tenants (ACC-01 to ACC-05), guardrails the tenant cannot change (GRD-01 to GRD-10), Pod Security (ADM-01, ADM-02), and service types, the per-container maximum and default limits (QTA-03 to QTA-07). They use reads, access reviews and server-side dry runs only, so they also run with `--read-only`. Every refusal names the layer that decided it: access rules, Pod Security, an admission policy or webhook by name, the quota by name, or the limit range. On the local kind cluster all 42 results passed. Two break tests on the real cluster now run in CI: cross-tenant secret access fails exactly ACC-02 and ACC-03 for that direction, and the built-in `admin` role fails GRD-05 (a tenant could create role bindings). With that role, GRD-02 still passed, refused by the Kyverno policy `protect-platform-objects` instead of access rules, and the report says so. Unit tests break each protection of a simulated cluster in turn and expect exactly the matching checks to fail.
- **The network checks of `bulkhead check`** (NET-01 to NET-06, the first active checks). In every tenant, the checker starts a server and a client on each of two nodes, as the tenant admin, and fetches by pod IP from inside the clients: within a tenant on the same node and across nodes (must work), DNS (must work), between every pair of tenants in both directions on the same node and across nodes (must be blocked), and to the internet (must be blocked, hardened level). On a cluster with one schedulable node, the checks across nodes are skipped. A dry run plans the pods without sending anything. On kind all 16 results passed; with the network policies of both tenants deleted, exactly the 10 blocked results failed, and both tests now run in CI, together with an interrupt test: Ctrl+C while the test pods exist ends the run with exit code 3 and leaves nothing behind.
- **The noisy-neighbour and quota checks of `bulkhead check`**, which complete the catalogue of the bash isolation suite. RES-01 to RES-05: in the first tenant a CPU hog (limit 500m) and a memory hog (limit 64Mi) run on one node, next to a server of the second tenant; the checker reads the hog's `cpu.stat` (cgroup v2) twice over ten seconds, waits for the memory hog to be OOMKilled, and checks that the neighbour still answers and did not restart. QTA-01, QTA-02, QTA-08 and QTA-09: from each tenant's own quota, a pod with half the free CPU requests is accepted and a second that would go over is refused, and as many default-sized pods as the pod count allows are accepted and one more is refused (at most 25 pods per tenant). The test pods of earlier categories are removed before, and the run waits until the quota stops counting them. On kind, a full run gave 69 passes and 2 skips (bravo has no viewer) in 80 seconds and removed its 20 test pods; without tenant alpha's quota, exactly alpha's quota checks failed. A blocked result now counts when its pair passed for the same tenant, so one tenant's failure no longer makes another tenant's results inconclusive.
- **Reports of `bulkhead check`** (`docs/check-reports.md`): `-o table|json|junit|sarif` on stdout, and `--report FORMAT=PATH` (repeatable) to write other formats from the same run. The JSON report (`apiVersion: bulkhead.io/v1alpha1`, `kind: CheckReport`) has a published schema, `schemas/check-report.schema.json`, and carries the run, the tenants, the full text of every check of the run, the results, the summary and the exit code. JUnit has one suite per category; SARIF 2.1.0 has one rule per check and a result for every failure, pointing to `kubernetes/<context>/<namespace>`. The table now ends with how to fix each check that did not pass. A unit test validates a report with every status against the schema and checks that the schema refuses wrong ones; the end-to-end test validates the JSON report of a real failing run on kind, and checks that JUnit and SARIF report the same failures.
- **An HTML report** (`-o html`, `--report html=PATH`): one self-contained page to keep as evidence, with no scripts and nothing loaded from elsewhere. It shows the verdict, the run, the tenants, a **network matrix** (source tenant by destination, including DNS and the internet, with what each cell must be and what was observed), how to fix each failure, every result with the layer that decided it, and the text of every check. Network results now carry `from` and `to` in every format. On kind, with tenant bravo's network policies removed, the matrix showed traffic between the tenants still blocked in both directions (tenant alpha's own policies hold) and bravo reaching the internet, and the run failed exactly NET-06 and GRD-02 for bravo.
- **`PolicyReport` objects** (`--policy-reports`): the run also stores its results in the cluster as a `PolicyReport` (`wgpolicyk8s.io/v1alpha2`) named `bulkhead-check` in each tenant namespace, so dashboards that read these reports show the isolation of each tenant. It is off by default and refused with `--read-only` and `--dry-run`. Before any check runs, the checker makes sure the cluster serves the API and that you may write the reports, and stops with exit code 3 otherwise. Each run replaces the reports; an interrupted run writes none. The table, JSON (`policyReports`) and HTML reports name the reports written. On kind, a full run stored all 71 results in the two reports, a later run with a broken protection replaced them and showed its two failures, and tenant admins may not change or delete it. When the cluster does not serve the API, the end-to-end test checks that the run stops before any check, and then installs only the CRD.
- **Benchmark mapping** (`docs/benchmarks.md`, generated by `make checks-doc`): each check names the items of the archived Kubernetes multi-tenancy benchmarks, of section 5 of the CIS Kubernetes Benchmark v2.0, and of the NSA and CISA Kubernetes Hardening Guide 1.2 that it tests, partly tests, or deliberately differs from, after reading every item. 9 of the 19 multi-tenancy items, 7 of the 34 CIS items and 10 of the 18 NSA and CISA recommendations that tenants reach have a check; the page lists the rest as not tested. The references are in `docs/checks.md`, in the JSON and HTML reports, and as tags of the SARIF rules (for example `CIS 5.2.2`). A unit test fails when a reference names an item that is not in the documents read.
- **Declared flows** (`--flows FILE`, `docs/declared-flows.md`, ADR 0019): teams declare the flows they expect, for example "tenant alpha may reach the shared database, tenant bravo may not", in a file with a published schema (`schemas/flows.schema.json`, example in `examples/checker/flows.yaml`). The checker probes each flow with a TCP connection from its client pod in the source tenant: FLW-01 for allowed flows and FLW-02 for blocked ones, in a new `flows` category. Every blocked flow needs an allowed flow to the same destination and port, and counts only when that one connected, because on a real cluster a port that a Service does not have times out exactly like a dropped connection. The file is checked before the cluster is contacted. On kind, a shared web service was first unreachable for both tenants (FLW-01 failed, FLW-02 inconclusive), then reachable for tenant alpha only after an egress policy (both passed), and FLW-02 failed when tenant bravo was allowed to send anywhere.

### Fixed
- **A random failure of the quota test in `tests/isolation/run.sh`** (seen once in CI, on Kubernetes 1.37 with Cilium): the eleventh pod was accepted although the quota allows ten. After the test pods of the previous step were deleted, the quota controller recounted from a cache that lagged behind and lowered the count. The suite now waits until those pods are gone and the quota counts no pod and no CPU, and waits until the quota counts the ten new pods before it tries the eleventh. A quota that does not settle is now reported as a failure instead of being ignored.

## [0.3.0] - 2026-11-16

### Added
- **First cloud run, on GKE Standard** (Kubernetes 1.36, Dataplane V2). With the signed `0.3.0-rc.2` platform chart from the registry and the DNS fix above: 41 of 41 isolation checks and 29 of 29 live policy checks. With the network policies of both tenants deleted, the eight checks that tenants cannot reach each other failed. Results and the problems of the test machine are in `docs/measurements.md`.
- **The README quick start runs in CI as written.** `scripts/readme-quickstart.sh` takes the bash block under the Quick start heading and runs it line by line on a fresh runner (job `quickstart`). The block now starts with the clone and puts `~/.local/bin` on the `PATH`, so it works on a machine that has nothing set up.
- **Tests on three Kubernetes versions and two network plugins.** `make cluster-up` takes `K8S_VERSION` (1.37.0, 1.36.4 or 1.35.8, node images pinned by digest in `examples/kind/node-images`) and `CNI` (`calico` or `cilium`, Cilium pinned in `examples/kind/cilium-version`). A new `compatibility` CI job runs the isolation and live policy suites on 1.36 with Calico, and on 1.35 and 1.37 with Cilium. Calico 3.33.0 does not install on Kubernetes 1.35 (its manifests create `MutatingAdmissionPolicy` objects of `admissionregistration.k8s.io/v1`), so `make cluster-up` refuses that combination with a clear message. Locally on Cilium 1.20.4: 41 of 41 isolation checks, 29 of 29 live policy checks, and with the network policies deleted exactly the eight network checks that must be blocked failed.
- **Signed chart releases** (ADR 0017). A version tag runs `.github/workflows/release.yml`, which packages both charts, pushes them to `oci://ghcr.io/opeyemiadeniji/charts`, signs them with cosign without a key, attaches a build provenance attestation, verifies the signatures, and creates the GitHub release from the changelog. A tag with a suffix makes a pre-release. `scripts/package-charts.sh` and `scripts/release-notes.sh` do the packaging and the notes, and the `chart` CI job packages both charts on every pull request. `docs/install.md` shows how to install from the registry and how to verify a chart.
- `docs/install.md`: installing Bulkhead on Amazon EKS, Google Kubernetes Engine, Azure Kubernetes Service and other clusters. Followed on GKE Standard; the EKS and AKS parts are written from the providers' documentation and not yet run.

### Changed
- The kind node image is now pinned by digest.
- **Bulkhead is two Helm charts that can be installed on any cluster** (ADR 0016). `charts/bulkhead-tenant` renders one tenant, as before, and adds values for the namespace prefix, the Pod Security level and where DNS runs (including extra CIDRs such as NodeLocal DNSCache). `charts/bulkhead-platform` holds the two policies, the exceptions namespace and the cleanup permission, with values for the platform admins, the exceptions namespace, Kyverno's namespace and the runbook links. It requires Kyverno and does not install it. Both charts have a values schema. With default values, the rendered tenant is identical to v0.2.0.
- **Layout:** `platform/` is gone. The charts are in `charts/`, the local demo (kind configuration, Calico checksums, pinned Kyverno, kind values) in `examples/kind/`, and policy exceptions in `exceptions/`.
- The kind admin group `kubeadm:cluster-admins` is no longer built into the policy. The demo sets it in `examples/kind/bulkhead-platform-values.yaml`. **A cluster set up with v0.2.0 must be rebuilt** (`make cluster-down` and the quick start), because the policies are now owned by a Helm release.
- `make policy-test` tests the rendered platform chart, and `make chart-test` also lints, renders and schema-checks the platform chart (Kyverno kinds through the CRDs catalog) and checks that the values schemas refuse wrong values.
- `make tenants-apply` takes cluster-wide tenant values from `TENANT_VALUES`.

### Fixed
- **Tenant DNS failed on GKE with NodeLocal DNSCache**, found in the first cloud run. Queries to the DNS service are answered there by pods labelled `k8s-app: node-local-dns`, which the tenant's DNS policy did not allow. The tenant chart has a new value `dns.extraPodLabels`, with that label by default. On clusters without those pods, the entry matches nothing. The install guide's earlier advice for GKE (`extraCIDRs: ["169.254.20.10/32"]`) was wrong for this setup and is removed.
- `make tenants-apply` honours `KUBE_CONTEXT`, as the test suites do. It always applied to the kind cluster, although `docs/install.md` told people to use it on their own cluster.

## [0.2.0] - 2026-11-15

### Security
- **The `tenant-admin` role can no longer write endpoints.** Writing endpoints lets a tenant point a service at addresses outside its namespace (CVE-2021-25740), which is why Kubernetes removed the right from its built-in roles. Endpoints and endpoint slices are now read-only, `pods/log` is read-only, and `pods/exec` and `pods/portforward` get only the verbs they use. Three new isolation checks (41 in total): a tenant admin cannot create endpoints, can still read them, and can still exec into its own pods.

### Added
- **The policy `protect-platform-objects`** (ADR 0015): the network policies, quota, limit range, role and role bindings that the platform puts in a tenant namespace cannot be changed or deleted except by platform admins and Kubernetes itself, even when someone has too many rights in the namespace. Its message names the fix and the runbook `docs/runbooks/policy-protect-platform-objects.md`. Seven new live checks (29 in total).
- **Policy exceptions are checked before they reach the cluster** (ADR 0014). Exception files live in `platform/exceptions/`. `make validate-exceptions` refuses a file without an absolute expiry date in the future and at most 90 days ahead, without an owner, a reason or a known tenant, with an unknown policy, or without a match condition. `make test-exceptions` runs 13 files that must fail for a stated reason and one that must pass, and both run in the `contract` CI job. `make kyverno-up` validates and applies the exceptions. The live policy suite checks that the example exception is accepted by the cluster.
- `docs/roadmap.md`: the plan to turn the project into an open source project that other engineers install, with what "done" means for each phase and what "solid" means for the whole project. It replaces the release plan in the problem statement.
- ADR 0012 (proposed): an active tenant isolation checker that runs on any cluster, with a review of the tools that exist today and the checker's design features, including a research category for cross-namespace references through operators.
- ADR 0004: Kyverno as the admission policy engine, compared with OPA Gatekeeper and the built-in ValidatingAdmissionPolicy.
- Supported platforms section in the README, with the parts that are tied to Linux on x86-64, and an issue for macOS and ARM.
- A third cluster start time in the measurements and in the README.
- `make kyverno-up`, which installs Kyverno with Helm at the chart version that is pinned in `platform/kyverno/chart-version`, and the measured memory cost of the stock install.
- Kyverno policy exceptions, enabled in one namespace (`platform-exceptions`), that expire through a `cleanup.kyverno.io/ttl` label with an absolute date, and the one permission that the cleanup controller needs to delete them.
- ADR 0008: CEL-based Kyverno policies instead of the deprecated `ClusterPolicy`, and exceptions that expire through a TTL label.
- First policy, `disallow-latest-image-tag` (a CEL `ValidatingPolicy` scoped to tenant namespaces), with its offline tests, a message that names the fix and links to a runbook, and the runbook `docs/runbooks/policy-disallow-latest-tag.md`.
- `make policy-test` and the `policy` CI job, which run the offline tests and check that the suite cannot give a false green.
- The Kyverno CLI in `scripts/install-tools.sh` with its checksum, pinned in `platform/kyverno/app-version`, and a check in `make kyverno-up` that the chart installs that version.
- ADR 0009: scope policies to tenant namespaces by label, and test the scope offline with a values file.
- The policy `disallow-latest-image-tag` also judges updates, but only for images that the old object did not have. A workload that already runs `latest` accepts unrelated edits, and a swap to another `latest` image is denied.
- `make test-policy-live` (`tests/policy-live/run.sh`): 21 live checks as a tenant admin on the local cluster, and a `policy-live` CI job that is not a required check yet.
- ADR 0010: the policy judges updates, but only for images that the old object did not have.
- ADR 0011: what the guardrail does while Kyverno is stopped, crashes or restarts, and the chosen route (accept and document). ADR 0009 is amended.
- `make chart-test` checks every rendered object against the Kubernetes API schema with kubeconform, in strict mode. kubeconform is pinned in `scripts/install-tools.sh` with its checksum.
- shellcheck runs on every script as a pre-commit hook, and so in the `lint` CI job.
- `make cluster-up` checks the Calico manifests against the hashes in `platform/calico/manifests.sha256` before it applies them.
- `SECURITY.md`, and Dependabot for GitHub Actions and Python dependencies.

### Changed
- The README describes v0.2: the guardrails, exceptions, outage behaviour, every suite and break test, the new make targets, and CI job times measured over all runs (`isolation` 126 s to 195 s in 47 runs, `policy-live` 126 s to 299 s in 28 runs), which replace the range from five runs.
- **The project is renamed to Bulkhead** (ADR 0013). The repository is `OpeyemiAdeniji/bulkhead`, and GitHub redirects the old URLs. The label on platform-managed objects, which the policies select on, is now `app.kubernetes.io/managed-by: bulkhead`, so a cluster set up before the rename must apply its tenants again. The local kind cluster is named `bulkhead` (context `kind-bulkhead`).
- The roadmap adds AWS Emulator: for testing the EKS infrastructure code in continuous integration, and as an optional step in the tenant CI template. It is never used as evidence for an isolation claim.
- GitHub Actions are pinned to commit SHAs, and `actions/checkout` and `actions/setup-python` move to their current major versions (the old ones ran on the deprecated Node 20).
- The `chart` CI job installs Helm through `scripts/install-tools.sh`, so the Helm version is pinned in one place. The `lint` job uses Python 3.14, like the `contract` job.
- The pre-commit hooks move to their current versions.
- The README lists every ADR, and its roadmap follows `docs/roadmap.md`.

### Fixed
- **A small tenant now fits its 10 pods when they set no resources.** The default container limit was 500m CPU, half of the tier's CPU limit, so only two such pods fitted. The defaults are now requests of 50m CPU and 50Mi and limits of 100m CPU and 100Mi, one tenth of the small tier. The isolation suite has two new checks (38 in total): 10 pods without resources fit, and an eleventh is rejected by the pod count.
- `make kyverno-up` waits until the Kyverno service has a ready address and retries the policy apply, because the webhook refused the connection in the first `policy-live` run on `main`.
- The architecture diagram in the README did not render on GitHub (an error in the label of a dotted arrow). It is removed for now and replaced by a numbered list, until a hand-drawn diagram is added. The v0.1.0 tag still contains the old diagram.

## [0.1.0] - 2026-10-26

### Added
- Initial project structure from the repo template.
- Problem statement with goals, non-goals, constraints, definition of done and how success is measured.
- ADR 0001: namespace per tenant on a shared cluster.
- Tenant contract: JSON Schema, validation script and sample tenants `alpha` and `bravo`.
- Contract tests with valid and invalid examples, where each invalid file declares the message it must produce.
- `contract` CI job, plus `make validate` and `make test-contract`.
- ADR 0005: tenant contract as plain YAML with a JSON Schema.
- ADR 0002: pull request as the self-service interface, not a web portal.
- ADR 0003: network plugin for NetworkPolicy enforcement, with a measured test of the default kind plugin.
- Local kind cluster with one control-plane node and two workers, with Calico v3.33.0 as the network plugin.
- `make cluster-up` and `make cluster-down`, which build and remove the cluster in one command each.
- Measurements document with the setup and the method behind each number.
- Tenant Helm chart (`platform/charts/tenant`): namespace, quota, limit range, access rules and default-deny network policies for every tenant file.
- `make chart-test` and the `chart` CI job, which lint and render the chart for every tenant and check that every object carries the cost label.
- `make tenants-apply`, which renders every tenant file and applies it to the local cluster.
- ADR 0006: Helm chart as the renderer of the tenant file.
- ADR 0007: custom tenant admin role instead of the built-in roles.
- Isolation test suite (`make test-isolation`) with 36 checks, in which the test pods are placed on named nodes so that same-node and cross-node traffic are both covered: secrets across tenants, platform controls, network traffic between tenants, noisy neighbour limits and quotas.
- `scripts/install-tools.sh`, which installs pinned versions of kind, kubectl and Helm and checks each download against its checksum.
- `isolation` CI job, which builds the cluster on a GitHub runner and runs the isolation tests.
- Runbook for a failed isolation job, with the first real failure recorded.
- README with the idea, a quick start, the checks and measured results, the known limits and the roadmap.

### Changed
- `make setup` and `CONTRIBUTING.md` now use pre-commit installed with pipx, not a virtualenv.
- The YAML hook skips Helm templates, which are not valid YAML until they are rendered.

[Unreleased]: https://github.com/OpeyemiAdeniji/bulkhead/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/OpeyemiAdeniji/bulkhead/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/OpeyemiAdeniji/bulkhead/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/OpeyemiAdeniji/bulkhead/releases/tag/v0.1.0
