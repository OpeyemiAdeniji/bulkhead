# 0015. A policy protects the platform's objects in tenant namespaces, as a second layer

- Status: accepted
- Date: 2026-11-14

## Context

The isolation of a tenant depends on objects that the platform puts in its namespace: the default-deny network policies, the quota, the limit range, the role and the role bindings. ADR 0007 chose a custom `tenant-admin` role that cannot change them, and promised an admission policy as a second layer for v0.2.

The second layer matters on the day that access rules are wrong, for example when someone binds the built-in `admin` role in a tenant namespace by hand. Observed on 2026-11-14 on the local cluster (Kubernetes v1.37.0, Kyverno v1.19.2), with a group bound to the built-in `admin` role in `tenant-alpha` and no policy in place, as server-side dry runs:

1. The group could delete `default-deny-all`, remove its platform label, change its rules to allow all ingress, and delete the role binding `tenant-admins`.
2. It could not change the role `tenant-admin`. Kubernetes refused it with "attempting to grant RBAC permissions not currently held", because `tenant-admin` holds rights that `admin` does not (write on `endpoints` and on `pods/log`).
3. The `admin` role cannot change quotas or limit ranges. It can only read them.

The kind admin user (`kubernetes-admin`) is in the group `kubeadm:cluster-admins`, not in `system:masters`, as kubeadm does since Kubernetes 1.29.

## Options considered

1. **Rely on the tenant-admin role only.**
   - Pros: nothing to add.
   - Cons: one wrong role binding removes isolation, and nothing notices.
2. **A Kyverno policy that refuses changes and deletions of objects with the platform label, unless the request comes from a platform identity.**
   - Pros: it holds whatever the access rules say, and it uses the label that the chart already sets on every object. The message can tell the developer what to do.
   - Cons: the list of platform identities is fixed in the policy. On another cluster the platform admins must be in one of the listed groups. Like every Kyverno policy, it does not apply while Kyverno is stopped (ADR 0011).
3. **A built-in `ValidatingAdmissionPolicy`.**
   - Pros: no webhook, so no outage window.
   - Cons: a second policy language next to Kyverno, without Kyverno's exceptions. ADR 0004 keeps it for a later case.

## Decision

Use option 2: the `ValidatingPolicy` `protect-platform-objects`.

- It matches UPDATE and DELETE of resource quotas, limit ranges, network policies, roles and role bindings in namespaces with the label `app.kubernetes.io/managed-by: bulkhead`.
- It judges the object as it was before the request, so removing the platform label is refused too.
- It accepts requests from the groups `system:masters`, `kubeadm:cluster-admins`, `bulkhead:platform-admins` and `system:serviceaccounts:kube-system`, and from the user `system:kube-controller-manager`, so that Kubernetes can remove the objects when a tenant namespace is deleted.
- Background scanning is off. The policy only judges changes, and a scan of existing objects has no old object to judge.
- The message states the problem, the fix (a pull request on the tenant file) and the runbook `docs/runbooks/policy-protect-platform-objects.md`.

## Consequences

- Seven live checks in `tests/policy-live/run.sh` (29 in total). With a group bound to the built-in `admin` role: the default-deny policy cannot be deleted, unlabelled or loosened, and the `tenant-admins` binding cannot be deleted; a network policy of its own can still be created and deleted. The platform admin can still delete platform objects, and a namespace with platform objects is still removed completely. With the policy deleted, exactly the four checks that must be blocked failed.
- The policy is tested live only, because the Kyverno CLI does not test updates and deletions offline (ADR 0010).
- Creating a new object with the platform label is not judged, and the namespace object itself is not covered, because changing it needs cluster-wide rights.
- Platform admins on clusters that are not kubeadm or kind must be in `bulkhead:platform-admins` or `system:masters`. Phase 1 makes the list a value of the chart.
- Observation 2 showed that `tenant-admin` grants write on `endpoints`, which Kubernetes removed from its built-in roles because it allows traffic to be redirected (CVE-2021-25740). That is fixed in a separate change.
- Revisit this decision if the platform moves to a GitOps controller whose identity is not in the list (Argo CD in Phase 3 needs its service account added), or if Kubernetes adds a built-in way to protect objects by label.
