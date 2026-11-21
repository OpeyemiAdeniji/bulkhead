# 0022. Tenancy profiles: how the checker finds tenants on different platforms

- Status: accepted
- Date: 2026-11-21

## Context

`bulkhead check` assumes the model of Bulkhead's charts: a tenant is one namespace, and its admins and viewers are the subjects of two role bindings in it. Work package 7 of `docs/phase-2-checklist.md` asks for profiles, so that the same checks run on platforms that model tenants differently: plain namespaces, Capsule and vCluster.

The documentation of the two projects was read on 2026-11-21. The facts below come from it, and the profiles of Capsule and vCluster are confirmed on a real cluster before they are released.

- **Capsule** (v0.15.2, projectcapsule.dev):
  - A tenant is a cluster-scoped `Tenant` (`capsule.clastix.io/v1beta2`), with owners (`spec.owners`: users, groups or service accounts) and any number of namespaces (`status.namespaces`, label `capsule.clastix.io/tenant`).
  - Each owner gets the cluster roles `admin` and `capsule-namespace-deleter` in every namespace of the tenant.
  - Capsule installs no network policy by itself: network isolation exists only if the platform configures it.
  - The release notes name Kubernetes 1.36 as the minimum, and the installation needs cert-manager.
- **vCluster** (v0.38.0, vcluster.com):
  - A tenant is a virtual cluster: its own API server, in one host namespace. The tenant is cluster-admin inside it.
  - Pods, Services, Secrets, ConfigMaps and claims are copied to the host namespace under rewritten names (`name-x-namespace-x-vcluster`).
  - Network isolation between virtual clusters (`policies.networkPolicy`), quotas, limit ranges and Pod Security are all off by default. Without network policies, "a pod in one cluster can reach pods in another".
  - The tenant's kubeconfig is in the secret `vc-<name>` of the host namespace.

The three models differ in what a tenant can address. In Bulkhead's model and in Capsule, every tenant uses the same API server, so "read another tenant's Secret" is a real test. In vCluster, another tenant's objects have no name in the tenant's API at all; the boundary is the host: its network, its quota, and its nodes.

## Options considered

1. **One configuration file that describes any model.** Flexible, but every user must describe their platform correctly before the first run, and a wrong description gives wrong results with confidence.
2. **A profile per model, built into the checker** (chosen). `--profile namespaces|capsule|vcluster` selects how tenants and identities are found, and which checks do not apply, with the reason. Each profile is tested in CI against the real software.
3. **Detect the model automatically.** Convenient, but a cluster can run Capsule and plain namespaces side by side, and a wrong guess would be silent.

## Decision

- `--profile` selects the profile; `namespaces` is the default, so existing commands keep their meaning. The profile is recorded in every report (`run.profile`).
- A profile decides:
  1. what `--tenant` names;
  2. which namespace the checks use for each tenant;
  3. the identities the checker acts as;
  4. which checks do not apply.

  A check that does not apply is reported as skipped with the profile's reason. It is never left out silently and never passed.
- **namespaces:** as today. `--tenant` names a namespace. The admin and viewer are the subjects of two role bindings (`--admin-binding`, `--viewer-binding`), so the profile fits any platform that binds teams in their namespaces, not only Bulkhead's chart.
- **capsule:**
  - `--tenant` names a Capsule `Tenant`.
  - The checks use its first namespace by name, and the report lists all of them.
  - The admin is an owner of the tenant: a group first, then a service account, then a user, as in the namespaces profile. Capsule has no viewer, so the viewer checks are skipped.
  - Which checks do not apply is decided by measuring Capsule's defaults on a real cluster, not from the documentation. For example, the documentation suggests that owners may create role bindings (GRD-05).
- **vcluster:**
  - `--tenant` names a virtual cluster as `name` in its host namespace.
  - The checks that act as the tenant use the virtual cluster's own API, with the kubeconfig of its `vc-<name>` secret.
  - The checks of the host's boundary use the host: network between the copied pods, quota and limits in the host namespace, Pod Security.
  - The access checks between tenants do not apply, because another tenant's objects cannot be named. The exact list is decided by measuring.
- The profiles are delivered one at a time, each with its CI test: namespaces first, then capsule, then vcluster.

## Consequences

- The checker serves the three common ways of sharing a cluster, with results that mean the same in each: a skipped check says why.
- Capsule and vCluster are installed in CI from pinned chart versions, which adds time to the pipeline and two dependencies to keep up to date.
- A platform that mixes models, or uses another tool such as Hierarchical Namespaces, is not covered. The namespaces profile, with its configurable role bindings, is the fallback.
