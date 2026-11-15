# 0016. Two Helm charts, configurable per cluster, with the kind demo as one example

- Status: accepted
- Date: 2026-11-15

## Context

Until v0.2 the platform was built for one kind cluster. The tenant chart lived in `platform/charts/tenant`, the policies were plain files applied by `make kyverno-up`, and that script also installed Kyverno. Several values that differ between clusters were fixed in the files:

- **Platform admins.** `protect-platform-objects` lists `kubeadm:cluster-admins`, the admin group of kubeadm and kind. On Amazon EKS the admins come from access entries, and on Google Kubernetes Engine they are often Google users without a group (ADR 0015).
- **DNS.** The network policy that allows DNS selects `k8s-app: kube-dns` in `kube-system`. That label is the same on EKS, GKE, AKS and kind, but GKE with NodeLocal DNSCache sends DNS to a link-local address on the node, which needs an extra allowance.
- **Kyverno.** A platform team that already runs Kyverno, often highly available, does not want a second install, and a chart that installs a cluster-wide admission controller as a side effect is not acceptable on a shared cluster.
- **Names.** The exceptions namespace, the Kyverno namespace and its cleanup service account, and the runbook links are fixed.

The goal of Phase 1 (`docs/roadmap.md`) is that an engineer installs Bulkhead on a cluster of their own with Helm and the documentation alone.

One limit does not depend on Bulkhead: network policies only work when the cluster's network plugin enforces them. EKS needs the network policy feature of the VPC CNI (or another plugin), GKE needs Dataplane V2 or network policy enforcement, and AKS needs a network policy engine. On a cluster without one, the objects are accepted and do nothing. The isolation suite detects this, because its "must be blocked" network checks fail.

## Options considered

1. **One chart for everything,** with a list of tenants as values.
   - Pros: one release to install.
   - Cons: every tenant change upgrades one release that holds all tenants, so one bad file blocks all of them, and the GitOps step in v0.5 (one Argo CD application per tenant file) does not fit.
2. **Two charts: `bulkhead-platform`, installed once per cluster, and `bulkhead-tenant`, installed once per tenant file.**
   - Pros: matches how the parts change: the platform rarely, tenants often. Each tenant is its own release, which is what an Argo CD ApplicationSet creates. The platform chart can require Kyverno without installing it.
   - Cons: two charts to version and publish, and their shared label must stay in step.
3. **Kustomize bases** that users patch.
   - Pros: no templates.
   - Cons: ADR 0006 already chose Helm for the tenant, and the per-cluster values (admins, DNS) are cleaner as values than as patches.

## Decision

Use option 2.

- **`charts/bulkhead-tenant`** renders one tenant from its tenant file, as before. New values with the old behaviour as default: `namespacePrefix` (`tenant-`), `podSecurity.level` (`restricted`), `dns` (the namespace and pod labels of the DNS service, its ports, and optional extra CIDRs such as the NodeLocal DNSCache address), the size tiers and the container defaults. Cluster-wide values go in one file that is passed before the tenant file: `helm template -f cluster-values.yaml -f tenants/<name>.yaml`.
- **`charts/bulkhead-platform`** holds the two policies, the exceptions namespace and the permission that lets Kyverno's cleanup controller delete expired exceptions. Values: `platformAdmins.groups` and `platformAdmins.users` (default `system:masters` and `bulkhead:platform-admins`), `exceptions.namespace`, `kyverno.namespace` and `kyverno.cleanupServiceAccount`, and `runbookBaseURL`. It requires Kyverno 1.19 or newer with policy exceptions enabled in the exceptions namespace, and it does not install Kyverno.
- Both charts have a `values.schema.json`, so a wrong value fails at install with a clear message.
- The service accounts in `kube-system` and the user `system:kube-controller-manager` are always allowed by `protect-platform-objects`, and cannot be removed by a value, because without them a tenant namespace cannot be deleted.
- **`examples/kind/`** holds everything that is only for the local demo: the kind configuration, the Calico checksums, the pinned Kyverno version and its values, and the values that add `kubeadm:cluster-admins` as a platform admin.
- **`tenants/` and `exceptions/`** are the files that teams own, at the top of the repository.

## Consequences

- The default values change nothing for the kind demo, and every suite runs against the charts.
- The kind admin group is no longer a default. The demo sets it in `examples/kind/bulkhead-platform-values.yaml`, and other clusters set their own.
- The offline policy tests render the platform chart first, so they test what users install.
- Not done in this change: publishing the charts to `ghcr.io` with signatures, the Kubernetes version matrix, and a run on a cloud cluster. They follow in the next Phase 1 changes. Until a cloud run has passed, "works on EKS, GKE and AKS" is a design goal and not a claim.
- Revisit this decision if Argo CD in v0.5 needs a different split, or if Kyverno changes how policy exceptions are enabled.
