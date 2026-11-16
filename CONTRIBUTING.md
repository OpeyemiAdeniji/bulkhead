# Working agreement

This project follows a trunk-based workflow with short-lived branches.

## Workflow

1. Open or pick an issue.
2. Branch from `main`: `feat/short-name`, `fix/short-name`, `docs/short-name`. Aim to merge within a day or two.
3. Commit small and often using [Conventional Commits](https://www.conventionalcommits.org/): `feat: add disk threshold check`.
4. Push and open a pull request. Fill in the template. Wait for CI.
5. Squash-merge, delete the branch.

`main` is protected: no direct pushes, CI must pass.

## Setup

```bash
pipx install pre-commit
make setup
```

This installs the pre-commit hooks (formatting checks, secret scanning, commit message check).

The project uses several languages, and you only need the tools for the part you change:

| Part | Tools | Check before a pull request |
|---|---|---|
| Charts and scripts | bash, Python 3, Helm (`scripts/install-tools.sh` installs the pinned versions) | `make lint chart-test policy-test` |
| Local cluster tests | Docker, kind, kubectl | `make cluster-up tenants-apply test-isolation` |
| The checker (`cmd/`, `internal/`) | Go 1.26 or newer; the `toolchain` line of `go.mod` names the version CI uses | `make checker-test` |

Work on the checker follows [docs/phase-2-checklist.md](docs/phase-2-checklist.md). Tick the boxes your pull request completes.

## Releases

Tag with semantic versions (`v0.1.0` for a working skeleton, `v1.0.0` when complete), update `CHANGELOG.md`, and create a GitHub Release from the tag.

## Decisions

Record significant design choices in `docs/adr/` using `adr-template.md`.
