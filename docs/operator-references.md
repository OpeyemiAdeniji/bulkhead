# Operator references (research)

Many operators read a reference from a custom resource, for example a Secret reference with its own `namespace` field, and act on the object it names with the operator's wide rights. A tenant who may create that resource can point the field at another tenant's namespace. Chen et al. describe this class of weakness in "Breaking the Bulkhead" (NDSS, [arXiv:2507.03387](https://arxiv.org/abs/2507.03387)). In their measurement, about 14% of 2,268 operators were potentially affected, and vendors confirmed 8 vulnerabilities, 7 with CVEs.

`bulkhead check` tests whether a tenant can even **submit** such a resource. It does not make an operator act, because making a vulnerable operator act is the attack itself ([ADR 0021](adr/0021-operator-reference-checks.md)).

```bash
bulkhead check --tenant tenant-alpha --tenant tenant-bravo --level research --operator-refs operator-refs.yaml
```

The checks are at the research level, so they run only with `--level research` (or `--id XNS-01,XNS-02`) and a references file.

## The checks

| Check | As the admin of tenant A, a server-side dry run of the resource, with its namespace field set to | Must be |
|---|---|---|
| XNS-01 | tenant A (its own namespace) | accepted |
| XNS-02 | tenant B (another tenant) | refused at admission |

- Both are **dry runs**: the API server runs every check of a real request, including admission policies and webhooks, but stores nothing. No operator ever sees the resource. So the checks are read-only, and also run with `--read-only`.
- XNS-02 counts only when XNS-01 passed for the same reference and tenant. If the tenant may not create the resource at all, or the body in the file is not valid, both are refused, and XNS-02 is inconclusive: that refusal is not the defence the check is about.
- A pass names the policy or webhook that refused, for example `refused by admission (no-cross-namespace-refs)`.

## The file

```yaml
apiVersion: bulkhead.io/v1alpha1
kind: OperatorReferences
references:
  - name: app-database-secret
    apiVersion: apps.example.com/v1
    kind: Application
    resource: applications
    namespacePath: spec.databaseSecretRef.namespace
    body:
      spec:
        databaseSecretRef:
          name: database-credentials
```

| Field | Meaning |
|---|---|
| `name` | Names the reference in the reports. |
| `apiVersion`, `kind`, `resource` | The custom resource: its group and version, its kind, and its plural name (as in `kubectl api-resources`). |
| `namespacePath` | The dot-separated path of the namespace field, outside `metadata`. |
| `body` | Optional: the smallest object the resource's schema accepts. The checker sets `apiVersion`, `kind`, `metadata` and the namespace field. |

The example is in [`examples/checker/operator-refs.yaml`](../examples/checker/operator-refs.yaml), and the format is [`schemas/operator-refs.schema.json`](../schemas/operator-refs.schema.json). The checker reads the file before it contacts the cluster, and refuses mistakes with exit code 2.

## A defence that XNS-02 accepts

Without an admission rule, XNS-02 fails for every listed reference, which is the honest result: a tenant can submit a resource that points into another tenant. A ValidatingAdmissionPolicy is enough for a field like the one above:

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {name: no-cross-namespace-refs}
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules: [{apiGroups: [apps.example.com], apiVersions: ["*"], operations: [CREATE, UPDATE], resources: [applications]}]
  validations:
    - expression: "!has(object.spec.databaseSecretRef.namespace) || object.spec.databaseSecretRef.namespace == object.metadata.namespace"
      message: the secret reference must stay in the namespace of the resource
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata: {name: no-cross-namespace-refs}
spec:
  policyName: no-cross-namespace-refs
  validationActions: [Deny]
```

**Test the rule; do not trust it by reading.** While writing the end-to-end test, the first version of this rule escaped the field name as `__namespace__`. On Kubernetes 1.37 that name did not resolve: `has()` on it was always false, so the rule let every reference through, while it looked right. XNS-02 failed and showed it. A comparison with the escaped name instead fails with an error, which `failurePolicy: Fail` turns into a refusal of every resource, so XNS-01 fails. The plain name `namespace` worked.

## What a pass does not prove

- That the operator itself is safe. If the admission rule is removed, a vulnerable operator follows the reference again; run the checks after changes to admission.
- Anything about fields or resources that are not listed in the file.
- Anything about the second class of the paper: resources that lead an operator to create cluster-wide objects, such as a ClusterRoleBinding. Whether that happens depends on the operator's code, not on a field that admission can judge.

## Tested

Unit tests run XNS-01 and XNS-02 against a simulated cluster with and without the defence, with a tenant that may not create the resource, with an invalid body, in read-only mode and in a dry run, and check that every request is a dry run sent as a tenant admin. The end-to-end test on kind creates a custom resource definition and the right for tenant admins to create it:

1. Without an admission rule, XNS-02 fails.
2. With the rule above, it passes and names the policy.
3. The same in read-only mode.
4. No resource was stored.
