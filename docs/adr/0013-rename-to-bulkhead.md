# 0013. Rename the project to Bulkhead

- Status: accepted
- Date: 2026-11-14

## Context

The project is becoming an open source project (`docs/roadmap.md`), and the name `k8s-tenant-platform` describes it but is hard to search for and to remember. The isolation checker (ADR 0012) also needs a command name. The repository, the chart, the checker, the website and the label on platform objects should use one name.

Candidates were searched on GitHub on 2026-11-14 for an account of the same name and for repositories with the name that mention Kubernetes or tenancy. Domains and trademarks were not checked.

## Options considered

1. **Tenantry.** Clear meaning. Rejected: it is the name of an existing commercial multi-tenancy product for .NET (the `tenantry-org` organisation, with a paid tier and the `tenantry.dev` domain), so it would collide in the same field.
2. **Bulkhead.** The sealed walls that split a ship into compartments, so that one flooded compartment does not sink the rest. No Kubernetes or tenancy repository uses it, and the GitHub account of that name is free. The NDSS 2026 paper "Breaking the Bulkhead" uses the word for Kubernetes namespace isolation. Con: "bulkhead pattern" is a known resilience term, so plain web searches are noisy.
3. **Cofferdam** and **Watertight.** Free in this field. Cofferdam is harder to spell and its GitHub account is taken. Watertight is an everyday word and very hard to search for.

## Decision

Use **Bulkhead** (option 2).

- The repository is `OpeyemiAdeniji/bulkhead`. GitHub redirects the old URLs and clone addresses.
- The checker is a subcommand: `bulkhead check`.
- The label on every platform-managed object, which the policies select on, is `app.kubernetes.io/managed-by: bulkhead`.
- The local kind cluster is named `bulkhead`, so its context is `kind-bulkhead` and its nodes are `bulkhead-control-plane` and `bulkhead-worker`, `bulkhead-worker2`.
- Where a search needs it, the project is called "Bulkhead for Kubernetes".

## Consequences

- Records written before this ADR keep the old names, because they describe what happened then: ADR 0009 names the old label, and `docs/measurements.md` and the isolation runbook name the old nodes.
- A cluster that was set up before the rename has namespaces with the old label. The policies no longer match them until the tenants are applied again. The local cluster is rebuilt with `make cluster-down`, `make cluster-up`, `make tenants-apply` and `make kyverno-up`.
- The v0.1.0 release and its tag keep the old name.
- The local folder of a clone keeps its old name until it is cloned again. Nothing in the repository depends on the folder name.
- Revisit this decision if a trademark or an existing project with the same name in this field turns up before the launch.
