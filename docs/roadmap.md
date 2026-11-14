# Roadmap

This roadmap takes the project from a portfolio project to an open source project that other engineers install, run and contribute to. It replaces the release plan in `docs/problem-statement.md` from v0.2 on. Dates are not promised. A phase is done when its "done when" list is true and shown by a test or a measurement.

## What the project offers

Two things, which can be used apart:

1. **The platform:** a reference way to give each team a namespace on a shared cluster from one file in a pull request, with quotas, access rules, default-deny network policies and admission policies with developer-friendly messages and expiring exceptions. It is installed with Helm on any conformant cluster, and kind is the way to try it locally.
2. **The checker** (working name `tenantcheck`): a CLI that acts as a tenant on any cluster and actively tries to break isolation between tenants: network, secrets, access, guardrails, quotas, noisy neighbours and admission. It reports what held, with evidence. ADR 0012 explains why it is built and what already exists.

The checker is the part that solves an open problem: platform teams that share a cluster are asked to show that teams are separated, and no maintained tool tests that end to end. The platform is the reference setup that the checker is developed and tested against.

## Principles

- **Evidence over claims.** Every claim in the documentation or on the website links to the test that proves it, and every number to its measurement and method.
- **Safe on other people's clusters.** The checker never touches anything that it did not create, cleans up after itself, and has a read-only mode.
- **Standard tools and conventions,** so that a Kubernetes engineer feels at home: Helm, Go, kubectl plugin conventions, Semantic Versioning, Conventional Commits, Keep a Changelog.
- **Small, reviewed changes:** a pull request for everything, green checks before merge, an ADR for every decision that someone could reasonably make differently.

## Phases

### Phase 0: finish v0.2 (guardrails)

- Merge the CI hardening (shellcheck, kubeconform, pinned actions, Calico checksums, Dependabot, `SECURITY.md`).
- Fix the small tier: the default container limit (500m CPU) is half of the tier's CPU limit, so only two default pods fit although the quota allows 10. Add an isolation check that 10 default pods fit.
- A check in continuous integration that refuses a policy exception without an owner, a reason and a future expiry date (promised in ADR 0008), and a folder for exceptions.
- A policy that stops changes to platform-owned objects (the second layer that ADR 0007 promised).
- README for v0.2 (Kyverno, the new make targets, exceptions, outage behaviour) and the corrected CI timings.
- A short ADR for the separate `policy-live` job, then make it a required check after several green runs on `main`.

Done when: v0.2.0 is released with a signed tag and release notes, and every v0.2 goal in the problem statement is either built and tested or moved to a later phase with a reason. The Trivy gate moves to Phase 3, together with the tenant CI template that it belongs to.

### Phase 1: installable by others (v0.3)

- **Layout:** separate the product (`charts/`, `policies/`) from the local demo (`examples/kind/`).
- **Configuration:** values for everything that differs between clusters: the DNS selector, the tenant label, the size tiers, the Pod Security level, and an optional ingress allowance.
- **Packaging:** publish the chart to `ghcr.io` as an OCI artifact, signed with cosign, with an SBOM and build provenance. List it on Artifact Hub. Publish the policies as a versioned bundle.
- **Releases:** releases are made from signed tags by a workflow, with generated release notes from the changelog. Semantic Versioning, and an upgrade note for every minor version.
- **Compatibility:** continuous integration runs the isolation suite on the three newest Kubernetes minor versions. A published compatibility table lists Kubernetes, Calico and Kyverno versions that were tested.
- **Quick start from a clean clone,** run in continuous integration, so that it cannot break silently.
- **Contributor setup:** a dev container with the pinned tools, which also answers part of issue #14 (macOS and ARM).

Done when: a person who has never seen the project installs it on a cluster of their own with `helm install` and the documentation alone, and the result passes the isolation suite.

### Phase 2: the checker (v0.4)

- Go module, CLI and kubectl plugin, built with `client-go`.
- The check catalogue, ported from the 36 checks of the isolation suite and extended with Pod Security (a privileged pod is refused) and egress to the internet. Every check has an ID, a pair, a statement of what it proves and does not prove, and a fix.
- Safety: labelled test objects, clean-up on exit and on interruption, a read-only mode, a dry-run that lists what would be created, and a timeout for every check.
- Reports: table, JSON with a published schema, JUnit and HTML. Exit codes that continuous integration can use.
- Profiles: plain namespaces first, then Capsule and vCluster, each tested against that tool in continuous integration.
- Distribution: release binaries for Linux, macOS and Windows on amd64 and arm64, a signed container image, a krew plugin and a GitHub Action.
- Unit tests for every check, and an end-to-end test that breaks each protection on purpose and expects exactly the matching checks to fail (the break test of the isolation suite, made automatic).

Done when: the platform's own continuous integration uses the checker instead of `tests/isolation/run.sh`, and the checker gives the expected results on one cloud cluster that is not kind.

### Phase 3: real self-service (v0.5)

- Argo CD ApplicationSet: a merged tenant file creates the tenant, and a deleted file removes it. Offboarding is tested.
- A pull request comment that previews what a tenant file will create.
- The platform SLO: time from merge to a ready tenant, measured over many runs, with the method.
- Cost showback: a report from quota multiplied by documented unit prices, generated in continuous integration.
- A tenant CI template with the Trivy gate.
- A report of active policy exceptions and those that expire soon.

Done when: the definition of done in the problem statement holds without manual steps.

### Phase 4: the website

A public site where people learn what the project is, see the evidence and try it.

- Built with Docusaurus, hosted on GitHub Pages, deployed by a workflow from `main`. Documentation versioned per release.
- **Landing page:** the problem, what the project does, how it works (a diagram from pull request to ready tenant), and a "Proof" section that shows the results of the latest runs on `main` (which checks passed, on which versions).
- **Try it:** "local in five minutes with kind" and "run the checker on your cluster".
- **Tenant file builder:** a form that writes a tenant file, validates it with the same JSON Schema in the browser, and opens a pull request on GitHub. No backend, no login.
- **Docs:** installation, configuration, the check catalogue, policies and runbooks, ADRs, the compatibility table, and the claims-to-tests table (each risk, the control that stops it, the test that proves it).
- Accessible (keyboard use, contrast, alternative text), fast on a phone, and no tracking without consent.

Done when: the site is live, every claim on it links to evidence, and the builder produces files that pass the contract tests.

### Phase 5: launch (v1.0)

- v1.0 means: the platform installs on kind and on one cloud cluster from the documentation, the checker is released on every channel above, the website is live, and the public interfaces (tenant file schema, chart values, checker output schema, exit codes) are declared stable, with a deprecation policy.
- Before v1.0, ask at least five platform engineers outside the project to run the checker on a cluster of theirs and report what was unclear or wrong. Fix what they find.
- A demo video, a blog post, and an announcement where Kubernetes engineers read (r/kubernetes, the CNCF Slack, Hacker News).

### Phase 6: Amazon EKS

Run the platform and the checker on EKS with OpenTofu, an automatic teardown and a stated cost ceiling. Publish the results next to the kind results, including what differs.

## What "solid" means here

A checklist for the whole project. Each item is either done, planned in a phase above, or explicitly not done with a reason.

**Code and tests**
- Linting for every language in the repository: shellcheck, kubeconform, helm lint, `kyverno test`, golangci-lint and `go vet` for the checker, a YAML and Markdown linter.
- Unit tests, offline policy tests, live tests on a cluster, and break tests that prove each suite can fail.
- Tests on more than one Kubernetes version, and the test results kept as artifacts of each run.
- Test coverage reported for the checker, without a coverage target that invites empty tests.

**Security and supply chain**
- Every download checked against a checksum, every action pinned to a commit, every release signed (cosign) with an SBOM and build provenance.
- Dependabot, and a monthly manual bump of the tools it cannot see, with a measurement run.
- Secret scanning (gitleaks), private vulnerability reporting, `SECURITY.md`, and the OpenSSF Scorecard published as a badge.
- Least privilege: the permissions that the chart, Kyverno and the checker need are listed and tested.
- A threat model: what the platform protects against, what it does not, and the trust assumptions (ADR 0001 says internal teams, not hostile customers).

**Releases and compatibility**
- Semantic Versioning, a changelog, upgrade notes, a compatibility table and a deprecation policy for the public interfaces.
- Releases only from signed tags through a workflow, never from a laptop.

**Documentation**
- Quick start, installation, configuration reference, check catalogue, troubleshooting, runbooks, ADRs, architecture diagram, and a page that says what the project does not do.
- Every number with its method, setup and limits.

**Community and governance**
- `LICENSE` (MIT), `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md` (Contributor Covenant), `SECURITY.md`, `GOVERNANCE.md` (one maintainer today, and how that can change), `SUPPORT.md`.
- Issue forms and a pull request template, labels including "good first issue", and a public roadmap (this file and a GitHub project board).
- The Developer Certificate of Origin (`Signed-off-by` lines) for outside contributions, checked by a bot.
- Response times stated honestly for a project with one maintainer.

**Operations of the platform**
- Runbooks for every policy and every alert, the outage behaviour of each component (ADR 0011), backup and restore of the tenant files (they live in Git), and upgrade procedures for Kyverno, Calico and Kubernetes.

## Open decisions

- **The name.** Candidates and their checks are in the pull request that adds this file. The repository, the chart, the checker binary and the website use the same name.
- **Whether the checker lives in this repository or its own.** Its own repository is the usual choice for a tool with its own releases. Decided at the start of Phase 2.
- **How success is measured after launch:** for example installs of the chart, downloads of the checker, outside contributors, and issues from real clusters. Decided before the launch.

## What is not planned

- A web portal with a backend, or a Backstage installation. The pull request stays the interface (ADR 0002). A Backstage software template that opens the same pull request may be published for organisations that already use Backstage.
- A service mesh, cluster per tenant, or a controller that replaces the chart (ADRs 0001, 0003 and 0005).
- Full Prometheus and Grafana on the local demo, because of its memory. Metrics are documented for clusters that already run them.
