# 0018. Build the checker in this repository, as one Go module

- Status: accepted
- Date: 2026-11-16

## Context

ADR 0012 decided to build `bulkhead check`, an active tenant isolation checker in Go. The roadmap left open where it lives and how the code is laid out, to be decided at the start of Phase 2. This record makes those decisions, so that the work packages in `docs/phase-2-checklist.md` build on one base.

## Options considered

1. **A separate repository** (for example `bulkhead-check`).
   - Pros: its own issues, releases and version numbers. People who only want the checker do not see charts.
   - Cons: two places for issues and stars. The checker's end-to-end tests need the kind cluster, tenants and break tests that live here, so they would be copied or fetched across repositories. The roadmap's definition of done for Phase 2 is that this repository's own CI runs the checker.
2. **This repository, one Go module at the root.**
   - Pros: one project name, one place for issues, and the CI that already builds kind clusters with tenants tests the checker directly. A change to the charts and the matching change to the checks land in one pull request. Several Kubernetes projects keep their command-line tool in the main repository, for example Kyverno.
   - Cons: the charts and the checker share one tag and one version number. The repository gets a Go toolchain next to bash and Python.
3. **This repository, with the checker in its own Go module in a subfolder.**
   - Pros: the checker could get its own version tags (`checker/v0.4.0`).
   - Cons: two module paths and nested tags are harder to use and to release, for no need that exists today.

## Decision

Option 2. The checker lives in this repository, in one Go module at the root: `github.com/OpeyemiAdeniji/bulkhead`.

- **Layout:** `cmd/bulkhead` holds the `main` package. Everything else is under `internal/`, so no Go API is promised to other programs before v1.0: `internal/catalog` (the check catalogue), `internal/cli` (the commands), `internal/exitcode` and `internal/version`. Later packages follow the work packages of the checklist.
- **One version for the project.** A tag `vX.Y.Z` releases the charts and the checker together, as the release workflow already does for the charts.
- **Go:** the `go` line of `go.mod` names the oldest Go release the module supports (1.26), and the `toolchain` line the release that CI and the maintainers use (1.27.4). Go supports its two newest major releases, so the module supports both.
- **Libraries:** `spf13/cobra` for the commands, as `kubectl` and most Kubernetes command-line tools use. `client-go` is added with the first package that talks to a cluster. Every new dependency is named in its pull request with the reason.
- **The catalogue is Go code and the single source of truth.** Each check has an ID such as `NET-04`, a level, a mode, an expected result, the "must work" check it is paired with, what it proves, what it does not prove, how it is run, and the fix. A unit test enforces these rules. The documentation page of the checks is generated from the catalogue, so the two cannot drift.
- **Exit codes:** 0 when every selected check passed, 1 when at least one check failed, 2 for a usage error, 3 when the checks could not run (for example, no cluster, or a safety rule refused to start). Continuous integration can rely on them.
- **Checks in CI:** `gofmt`, `go vet`, the tests with the race detector, `govulncheck` and `golangci-lint`, with every tool version pinned.

## Consequences

- One repository to watch, and the checker is tested on the same clusters and tenants as the charts.
- A release of the charts is also a release of the checker. A fix in either one makes a new version of both. That is acceptable while both are small.
- Contributors need Go for the checker, and still need bash, Python and Helm for the rest. `CONTRIBUTING.md` lists them.
- Revisit if the checker gets users who do not use Bulkhead's charts and want their own release schedule. Moving `cmd/` and `internal/` into a new repository is possible without breaking anyone, because nothing is exported.
