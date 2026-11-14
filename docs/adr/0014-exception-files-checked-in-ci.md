# 0014. Exception files are checked in continuous integration, with an owner, a reason and at most 90 days

- Status: accepted
- Date: 2026-11-14

## Context

ADR 0008 decided that every policy exception carries the label `cleanup.kyverno.io/ttl` with an absolute date, so that Kyverno deletes it when the date passes. It also said that continuous integration would refuse an exception file without that label, or with a date that has passed. That check did not exist yet, and there was no place in the repository for exception files.

An expiry date alone does not stop an exception from living for years, and it does not say who asked for it or why. Policy exceptions that nobody owns and that never end are a common way for guardrails to wear away.

## Options considered

1. **Only the checks that ADR 0008 named:** the label exists, and its date is in the future.
   - Pros: the smallest change.
   - Cons: a date ten years ahead passes. Nobody can be asked about an exception, and a reviewer cannot judge it without a reason.
2. **Those checks, plus required annotations for the owner, the reason and the tenant, a maximum length, and a narrow scope.**
   - Pros: every exception has someone to ask and a reason to judge, and it ends within a known time. A long need is renewed in a pull request, which is reviewed again.
   - Cons: more rules to learn, and a renewal every 90 days for a long need.
3. **A separate approval tool or a ticket reference.**
   - Pros: richer approval workflows.
   - Cons: a second system next to the pull request, which ADR 0002 rejects.

## Decision

Use option 2. Exception files live in `platform/exceptions/<name>.yaml`, and `scripts/validate_exceptions.py` refuses a file unless:

- it is one `PolicyException` of `policies.kyverno.io/v1`, named like its file, in the namespace `platform-exceptions`;
- `cleanup.kyverno.io/ttl` is an absolute date in the future and at most 90 days ahead (the limit is a flag of the script);
- the annotations `bulkhead.io/owner` (a contact address), `bulkhead.io/reason` (at least 20 characters) and `bulkhead.io/tenant` (a tenant with a file in `tenants/`) are set;
- every policy in `spec.policyRefs` is a `ValidatingPolicy` in `platform/policies`;
- `spec.matchConditions` is not empty.

The `contract` job runs the check on the files in `platform/exceptions` and runs its tests (`tests/exceptions/`), with a fixed time so that the tests give the same result every day. `make kyverno-up` validates and applies the files. The live policy suite checks that the example file that passes offline is also accepted by the cluster, so the offline rules and the real schema cannot drift apart unnoticed.

## Consequences

- An exception cannot be merged without an owner, a reason, a tenant and an end date within 90 days.
- An exception whose date passes while it is merged makes the `contract` job fail on the next pull request. That is on purpose: the expired file has to be removed or renewed.
- The check cannot judge whether a match condition is narrow enough. It only checks that one exists. The review of the pull request judges the rest.
- The annotation prefix `bulkhead.io/` is a name, not a domain the project owns. If the project gets a domain, the prefix follows it in a later version, with a migration note.
- Revisit this decision if teams need exceptions longer than 90 days often, or if approvals need more than a pull request review.
