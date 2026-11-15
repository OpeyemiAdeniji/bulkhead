# Runbook: a change to a platform object was rejected

## What you see

    Error from server: admission webhook "vpol.validate.kyverno.svc-fail" denied the request: Policy protect-platform-objects failed: NetworkPolicy default-deny-all in tenant-alpha is managed by the platform and keeps the tenant isolated, so it cannot be changed or deleted from here. Fix: change the tenant file in a pull request, or ask the platform team. Runbook: <this page>

The first part of the text comes from Kubernetes and Kyverno. The part after `failed:` is the message of the policy.

## Why

Every tenant namespace contains objects that the platform created from the tenant file: three network policies (`default-deny-all`, `allow-dns-egress`, `allow-same-namespace`), the quota `tenant-quota`, the limit range `tenant-limits`, the role `tenant-admin` and the role bindings `tenant-admins` and `tenant-viewers`. They carry the label `app.kubernetes.io/managed-by: bulkhead`.

Together they keep the tenant isolated and inside its size. If one of them is changed or deleted, for example the default-deny network policy, the tenant is no longer isolated. So the policy `protect-platform-objects` refuses every change and deletion of these objects, unless it comes from the platform admins or from Kubernetes itself.

Normally you will not see this message, because the `tenant-admin` role does not allow these changes in the first place (ADR 0007). If you see it, someone has more rights in the namespace than the tenant file grants, for example a role binding to the built-in `admin` role. Tell the platform team, because that binding is a mistake.

## How to fix it

- **To change the size, the access groups or anything else that the platform created:** change the tenant file `tenants/<name>.yaml` in a pull request. After the merge, the platform applies the new objects.
- **To add your own network policy, role or role binding:** create a new object with a name of your own and without the label `app.kubernetes.io/managed-by: bulkhead`. The policy does not judge objects without that label. Whether the role of the tenant allows it is a separate question.

## Who may change platform objects

Requests from these identities are accepted:

- the groups `system:masters`, `kubeadm:cluster-admins` (the admin group of kubeadm and kind clusters since Kubernetes 1.29) and `bulkhead:platform-admins` (the group to bind for platform admins on other clusters);
- the service accounts in `kube-system` and the user `system:kube-controller-manager`, so that Kubernetes can delete the objects when a tenant namespace is removed.

## Not covered

- Creating a new object with the platform label is not judged. Such an object is not part of the baseline, and once it exists only the platform may change it.
- The tenant namespace itself is not covered. Changing a namespace needs cluster-wide rights, which tenants do not get.
- While Kyverno is stopped on purpose, the policy does not apply, like every Kyverno policy (ADR 0011).
- The policy is tested live only. The Kyverno CLI does not test updates and deletions offline (ADR 0010).
