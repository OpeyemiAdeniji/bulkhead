# Tenancy profiles

`bulkhead check` runs on platforms that model tenants differently. `--profile` says which model the cluster uses ([ADR 0022](adr/0022-tenancy-profiles.md)):

| Profile | `--tenant` names | The checks use | The admin is | Viewer |
|---|---|---|---|---|
| `namespaces` (default) | a namespace | that namespace | the first subject of the role binding `--admin-binding` (default `tenant-admins`) | the first subject of `--viewer-binding` (default `tenant-viewers`, empty for none) |
| `capsule` | a Capsule `Tenant` | its first namespace by name | an owner of the tenant: a group first, then a service account, then a user | none |

Every report names the profile (`run.profile`). A check that does not apply to the profile is reported as **skipped, with the reason**; it is never passed and never left out. A vCluster profile is planned.

## namespaces

The model of Bulkhead's tenant chart, and of any platform that gives each team a namespace and binds the team in it. With another binding name:

```bash
bulkhead check --tenant team-a --tenant team-b --admin-binding team-admins --viewer-binding ""
```

The end-to-end test runs it on two namespaces that Bulkhead did not make, each with its team bound to the built-in `admin` role.

## capsule

```bash
bulkhead check --profile capsule --tenant alpha --tenant bravo
```

The checker reads each `Tenant` (`capsule.clastix.io/v1beta2`): its namespaces from `status.namespaces`, and its owners from `status.owners`, or from `spec.owners` when the status has none. Capsule names its role bindings with a hash (`capsule:managed:<hash>`), so the owners are not read from role bindings. The tenant needs at least one namespace.

### What was measured

On kind (Kubernetes 1.37, Calico) with Capsule 0.15.2 and cert-manager v1.21.3, with two pairs of tenants. Every basic and hardened check ran; the CI job `capsule` repeats this on every pull request (`make test-capsule`).

| Tenants | Result |
|---|---|
| **Bare**: owners only, nothing else configured | The access checks **pass**: an owner cannot read another tenant's secrets or open a shell in its pods. These **fail**: <ul><li>network isolation (NET-04, NET-05, NET-06);</li><li>the protection of network policies, quotas and limit ranges (GRD-02, GRD-03, GRD-04), because there are none;</li><li>Pod Security (ADM-02);</li><li>quota and limits (QTA-01, QTA-04 to QTA-08).</li></ul>Capsule installs none of these by itself: a platform has to configure them. |
| **Configured**: network policies, a quota, a limit range, Pod Security `restricted` and no NodePort or LoadBalancer services, in the `Tenant` spec ([the tenants of the test](../tests/checker/capsule/tenants.yaml)) | **Every check passes.** Capsule's own webhook refuses an owner's deletion of the network policy Capsule created, although the `admin` role allows deleting network policies (GRD-02: `refused by admission (managed.validating.projectcapsule.dev)`). The quota and limit range are protected by access rules. NodePort and LoadBalancer services are refused by Capsule's services webhook. |

### Checks that do not apply

| Check | Why |
|---|---|
| GRD-05 (a tenant admin cannot create role bindings) | Capsule gives owners the `admin` role in their namespaces so that they can bind others to it: self-service by design. |
| ACC-04, ACC-05 (the viewer) | A Capsule tenant has owners, and no read-only viewer role. |

### Worth knowing

- **Capsule's webhooks refuse requests while Capsule is down.** In one local run, on a laptop that ran Kyverno, cert-manager, Capsule and the checker's CPU and memory tests at once, the API server became slow, and Capsule's controller exited nine times, because it could not renew its leader election lease within 5 seconds. While it restarted, pod creation in the tenant namespaces failed (`failed calling webhook "generic.rules.mutating.projectcapsule.dev"`), and QTA-08 reported an error. Run alone, all 18 quota checks passed. A single Capsule replica is therefore a point where tenants cannot create pods; more replicas, or more room for the API server, reduce that.
- Capsule 0.15 warns that the `Tenant` fields used above for network policies, limit ranges, quotas, services and namespace labels are deprecated, in favour of rules and TenantReplications. The checks test what ends up in the namespaces, so they stay valid when the configuration moves.
- The checks use the first namespace of each tenant. Isolation between two namespaces of the **same** tenant is not tested.
