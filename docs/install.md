# Install Bulkhead on your own cluster

This guide installs Bulkhead on a cluster that you already run: Amazon EKS, Google Kubernetes Engine, Azure Kubernetes Service, or any conformant cluster. To try it on your machine first, use the kind demo in the README instead.

**Status of this guide.** Everything here is tested on kind (Kubernetes v1.37.0, Calico v3.33.0, Kyverno v1.19.2). The provider sections are written from the providers' documentation and have **not yet been run on EKS, GKE or AKS**. A run on a cloud cluster is planned in Phase 1 of the [roadmap](roadmap.md). Until then, run the isolation suite on your cluster (step 5) before you rely on the isolation.

## What you install

| Part | Installed | What it contains |
|---|---|---|
| Kyverno | once per cluster, by you, if you do not run it yet | the admission controller that runs the policies |
| `charts/bulkhead-platform` | once per cluster | the two policies, the namespace for policy exceptions, and the permission that lets Kyverno delete expired exceptions |
| `charts/bulkhead-tenant` | once per tenant file | the tenant's namespace, quota, default limits, access rules and network policies |

The decision behind this split is [ADR 0016](adr/0016-two-charts-configurable-per-cluster.md).

## 1. Check that your cluster enforces network policies

Bulkhead's network isolation uses the standard `NetworkPolicy` API. A cluster accepts these objects even when nothing enforces them, and then they do nothing. Check your network plugin first:

| Cluster | What enforces network policies |
|---|---|
| Amazon EKS | The Amazon VPC CNI with its network policy feature switched on (`enableNetworkPolicy: "true"` in the add-on configuration, VPC CNI 1.14 or newer), or Calico or Cilium |
| Google Kubernetes Engine | GKE Dataplane V2 (the default on Autopilot), or network policy enforcement switched on for a Standard cluster |
| Azure Kubernetes Service | A network policy engine chosen when the cluster is created: Azure Network Policy Manager, Calico or Cilium |
| kubeadm, kind, other clusters | A network plugin that implements `NetworkPolicy`, such as Calico or Cilium |

Step 5 shows whether it works: the isolation suite's "must be blocked" network checks fail on a cluster that does not enforce network policies.

## 2. Install Kyverno, with policy exceptions enabled

Bulkhead needs Kyverno 1.19 or newer, and policy exceptions enabled in one namespace. If you already run Kyverno, check those two settings. Otherwise:

```bash
helm repo add kyverno https://kyverno.github.io/kyverno/
helm upgrade --install kyverno kyverno/kyverno --version 3.9.2 \
  --namespace kyverno --create-namespace \
  --set features.policyExceptions.enabled=true \
  --set features.policyExceptions.namespace=platform-exceptions \
  --set admissionController.replicas=3 \
  --wait
```

Three admission replicas keep the policies running while one pod restarts. The kind demo uses one replica to save memory, and what happens during an outage with one replica is measured in [ADR 0011](adr/0011-kyverno-outage-behaviour.md). Three replicas have not been measured.

## 3. Install the platform chart

Tell the chart who your platform admins are. Only they, and Kubernetes itself, may change the network policies, quotas and access rules that Bulkhead puts in tenant namespaces ([ADR 0015](adr/0015-protect-platform-objects.md)).

```yaml
# platform-values.yaml
platformAdmins:
  groups:
    - bulkhead:platform-admins     # bind your admins to this group, or list your own groups
  users: []                        # or list user names
```

| Cluster | Who your admins are in Kubernetes |
|---|---|
| Amazon EKS | The Kubernetes groups of an access entry. Create an access entry for your admin role with `kubernetesGroups: [bulkhead:platform-admins]`, and bind that group to `cluster-admin`. An access entry that only uses the EKS cluster admin access policy has no group, so list its user name instead. |
| Google Kubernetes Engine | Google identities. List user e-mail addresses under `users`, or use Google Groups for RBAC and list the group. |
| Azure Kubernetes Service | With Microsoft Entra ID, the object IDs of Entra groups. |
| kubeadm and kind | The group `kubeadm:cluster-admins` (see `examples/kind/bulkhead-platform-values.yaml`). |

```bash
helm upgrade --install bulkhead-platform charts/bulkhead-platform -f platform-values.yaml --wait
kubectl get validatingpolicies.policies.kyverno.io
```

Both policies should show as ready. The service accounts in `kube-system` and the user `system:kube-controller-manager` are always allowed, so that Kubernetes can delete a tenant namespace.

Other values: `exceptions.namespace` (must match Kyverno's `features.policyExceptions.namespace`), `kyverno.namespace` and `kyverno.cleanupServiceAccount` (where Kyverno's cleanup controller runs), and `runbookBaseURL` (where your copy of the runbooks is published). They are listed in `charts/bulkhead-platform/values.yaml`.

## 4. Create tenants

Put the values that are the same for every tenant of your cluster in one file:

```yaml
# cluster-values.yaml
namespacePrefix: tenant-        # platform namespaces must never start with this
podSecurity:
  level: restricted             # or baseline
dns:
  extraCIDRs: []                # GKE with NodeLocal DNSCache: ["169.254.20.10/32"]
```

Then render each tenant file with it, and apply:

```bash
helm template tenant-alpha charts/bulkhead-tenant -f cluster-values.yaml -f tenants/alpha.yaml | kubectl apply -f -
```

Or, from a clone of this repository: `TENANT_VALUES=cluster-values.yaml make tenants-apply`. A GitOps controller can do the same with one application per tenant file (planned with Argo CD in v0.5).

The DNS defaults (`kube-system`, pods labelled `k8s-app: kube-dns`, port 53) match CoreDNS and kube-dns on EKS, GKE, AKS, kubeadm and kind. If your DNS runs elsewhere, set `dns.namespaceLabels`, `dns.podLabels` and `dns.ports`.

## 5. Prove it on your cluster

Run the suites against your cluster with `KUBE_CONTEXT`:

```bash
KUBE_CONTEXT=<your context> make test-isolation
KUBE_CONTEXT=<your context> make test-policy-live
```

Today these suites expect the two sample tenants `alpha` and `bravo` from `tenants/`, at least two worker nodes, and permission to impersonate groups. They create test pods in the tenant namespaces and remove them at the end. Run them on a cluster where that is acceptable, for example a staging cluster. A version that runs on any tenants, with a read-only mode, is the `bulkhead check` command planned for v0.4 ([ADR 0012](adr/0012-active-tenant-isolation-checker.md)).

## Known limits on other clusters

- Not run on EKS, GKE or AKS yet (see the status at the top).
- `protect-platform-objects` must list every identity that changes tenant objects. A GitOps controller, such as Argo CD in v0.5, needs its service account added.
- On GKE Autopilot, some Kyverno settings are restricted. That is not checked.
- The isolation suite's noisy neighbour checks were measured on kind, where nodes share one machine. On real nodes they show that limits are enforced, and nothing about node capacity.
