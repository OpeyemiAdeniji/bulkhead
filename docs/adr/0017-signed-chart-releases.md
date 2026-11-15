# 0017. Charts are released to ghcr.io by a workflow, signed without a key, with build provenance

- Status: accepted
- Date: 2026-11-15

## Context

After ADR 0016, Bulkhead is two Helm charts. Until now a user had to clone the repository to install them. The roadmap (Phase 1) asks for charts that people install from a registry, and for a supply chain that a platform team can check before it lets a chart into its cluster: who built this package, from which commit, and has it been changed since.

The v0.1.0 and v0.2.0 releases were made by hand: a signed Git tag from a laptop, and a GitHub release created with `gh`.

## Options considered

1. **A Helm repository on GitHub Pages** (`index.yaml` with the packages).
   - Pros: the classic way, works with every Helm version.
   - Cons: a second branch or site to maintain, and signing a classic repository uses Helm's own provenance files with a GPG key that someone has to keep safe.
2. **OCI artifacts on ghcr.io, signed with cosign without a key, with a build provenance attestation.**
   - Pros: Helm has supported OCI registries for years, and ghcr.io comes with the repository. Keyless signing ties each signature to the identity of the workflow run (repository, workflow file and tag), recorded in the public Sigstore transparency log, so there is no private key to leak or rotate. GitHub's build provenance attestation says which commit and workflow produced the package. Both can be checked by anyone with `cosign verify` and `gh attestation verify`.
   - Cons: the user must know the identity to verify against. Keyless signing depends on Sigstore's public services being available at release time.
3. **Both a classic repository and OCI.**
   - Pros: reaches users of very old Helm versions.
   - Cons: two release paths to keep in step, for a project with one maintainer.

## Decision

Use option 2.

- A tag `vX.Y.Z` runs `.github/workflows/release.yml`. It packages both charts with that version (`scripts/package-charts.sh`), pushes them to `oci://ghcr.io/opeyemiadeniji/charts`, signs each by digest with cosign (keyless), attaches a build provenance attestation to the registry, verifies the signatures as a user would, and creates the GitHub release with the packages and the changelog section as notes (`scripts/release-notes.sh`).
- A tag with a suffix (`vX.Y.Z-rc.1`) makes a pre-release, used to test the workflow end to end before a real release. A final release fails if the changelog has no section for it.
- Every pull request packages both charts with a test version in the `chart` job, so a packaging error shows before a tag.
- The workflow has write permissions for packages, releases, attestations and the signing token only in the release job, and only on tags.
- No software bill of materials for the charts. They contain only templates and values, with no dependencies, so the list would be empty. The `bulkhead check` binary in v0.4 gets one.

## Consequences

- Install: `helm install bulkhead-platform oci://ghcr.io/opeyemiadeniji/charts/bulkhead-platform --version X.Y.Z`.
- Verify: `cosign verify` with the identity `https://github.com/OpeyemiAdeniji/bulkhead/.github/workflows/release.yml@refs/tags/vX.Y.Z` and the issuer `https://token.actions.githubusercontent.com`, and `gh attestation verify oci://... --owner OpeyemiAdeniji`. The commands are in `docs/install.md`.
- Releases are no longer made by hand. The release pull request still moves the changelog section, and pushing the tag is the only manual step.
- A package that ghcr.io creates for a user account is private at first. After the first release, its visibility must be set to public once in the package settings, by the account owner.
- Not tested until the first tag: the push, the signing and the attestation. A pre-release tag tests them before v0.3.0.
- Revisit this decision if the project moves to a GitHub organisation (the registry path and the signing identity change), or if Helm or Sigstore change how OCI charts are signed.
