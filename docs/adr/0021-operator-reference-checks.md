# 0021. Test cross-namespace references of operators at admission, with dry runs

- Status: accepted
- Date: 2026-11-21

## Context

ADR 0012 planned two research checks, XNS-01 and XNS-02, for a class of weakness described by Chen et al., "Breaking the Bulkhead: Demystifying Cross-Namespace Reference Vulnerabilities in Kubernetes Operators" (NDSS, [arXiv:2507.03387](https://arxiv.org/abs/2507.03387)).

An operator reads a field of a namespaced custom resource, for example a Secret reference with its own `namespace`, and acts on the object it names. A tenant who may create that resource in its own namespace can point the field at another tenant's namespace, and the operator, which has wide rights, reads or changes the other tenant's object for it.

The authors analysed 2,268 operators and flagged about 14% as potentially vulnerable. Vendors confirmed 8 vulnerabilities, 7 with CVEs, for example in the Tempo, Metal3 and NVIDIA AIStore operators. A second class makes the operator create cluster-scoped objects, such as a ClusterRoleBinding, from a namespaced resource.

The checklist planned to create such a resource and to check that the operator refuses or ignores it. Reading the paper changed that plan:

- **Making a vulnerable operator act is the attack itself.** In the confirmed cases, it reads another namespace's Secret, or grants cluster-wide rights. On a real cluster, a test that "succeeds" in finding the weakness has already done the harm.
- There is no common signal for "the operator ignored it": operators report through their own status fields, events or nothing at all, and the paper does not describe one.
- The authors' mitigation for cluster administrators does not need the operator at all. An admission webhook refuses a resource whose reference points to a namespace the requesting user may not access, before the operator ever sees it. The paper reports about 4 ms of extra latency.

## Options considered

1. **Create the resource and watch the operator** (the original plan). It is the only way to observe the operator itself, but it performs the attack, and it needs a signal per operator. Rejected for the checker; it remains a task for an operator's own test suite, on a throwaway cluster.
2. **Test the admission defence with server-side dry runs** (chosen). The checker sends, as the tenant admin and with `dryRun=All`, a resource that references the tenant's own namespace (must be accepted) and one that references another tenant's namespace (must be refused). Nothing is stored, so no operator ever sees either resource, and the checks are safe on production clusters.
3. **Read the operator's code or its RBAC.** That is the paper's own static analysis; it needs the source and does not test the cluster.

## Decision

- **A file of references:** the platform team lists the references to test, in a file (`--operator-refs FILE`, `apiVersion: bulkhead.io/v1alpha1`, `kind: OperatorReferences`, schema in `schemas/operator-refs.schema.json`). Each entry has:
  - a `name`;
  - the custom resource's `apiVersion`, `kind` and `resource` (its plural name);
  - `namespacePath`, the dot-separated path of the namespace field, for example `spec.storage.secret.namespace`;
  - an optional `body`, the smallest object the resource's schema accepts.

  The checker sets `metadata` and the namespace field itself.
- **XNS-01 (allowed):** as each tenant admin, a dry-run create of the resource in its own namespace, with the namespace field set to the same namespace, is accepted. It shows that the tenant may create the resource and that the body is valid, so a refusal in XNS-02 is about the other namespace.
- **XNS-02 (blocked, pair XNS-01):** the same dry run, with the namespace field set to another tenant's namespace, is refused. Like every blocked check, it names the layer that refused it: an admission policy or webhook, or access rules. Its result counts only when XNS-01 passed for the same reference and the same tenant.
- **Read-only:** both checks only send dry runs, so they are now read-only checks, and run with `--read-only`. They stay at the research level, off by default: they run with `--level research` or `--id`, and only with `--operator-refs`.
- **Cluster-scoped references** (the second class of the paper) are not tested: whether a namespaced resource leads to cluster-wide rights depends on the operator's code, not on a field that admission can judge.

## Consequences

- A platform can show that tenants cannot even submit a cross-tenant reference, which removes the attack for every operator behind that admission rule, whatever its code does.
- A pass says nothing about the operator itself. If the admission rule is removed, a vulnerable operator becomes exploitable again, and the checks would then fail, which is the point of running them.
- Bulkhead does not ship such an admission rule yet. On a cluster without one, XNS-02 fails for every listed reference, which is the expected and honest result. A configurable policy in the platform chart is a natural follow-up.
- The texts of XNS-01 and XNS-02 in the catalogue change from "the operator acts / does not follow" to "the reference is accepted / refused at admission".
