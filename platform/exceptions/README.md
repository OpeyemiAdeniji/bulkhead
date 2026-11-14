# Policy exceptions

An exception lets named workloads of one tenant skip one policy until a date. It is a Kyverno `PolicyException` (`policies.kyverno.io/v1`), one per file, named `<name>.yaml` after its `metadata.name`. The decision is in [ADR 0008](../../docs/adr/0008-kyverno-cel-policies-and-expiring-exceptions.md).

`make validate-exceptions` checks every file in this folder, and continuous integration runs it on every pull request. A file is refused unless:

- it lives in the namespace `platform-exceptions`, the only namespace where Kyverno honours exceptions, and where tenants have no rights;
- the label `cleanup.kyverno.io/ttl` is an absolute UTC date (`2026-12-01T000000Z`), in the future and at most 90 days ahead. When the date passes, Kyverno deletes the exception, and the next request is judged by the policy again;
- the annotations `bulkhead.io/owner` (the team's contact address), `bulkhead.io/reason` (why, in at least 20 characters) and `bulkhead.io/tenant` (a tenant with a file in `tenants/`) are set;
- every policy in `spec.policyRefs` exists in `platform/policies`;
- `spec.matchConditions` narrows the exception to the workloads that need it.

`make kyverno-up` validates and applies the files in this folder. An example that passes is `tests/exceptions/valid/vendor-agent-latest.yaml`.

To extend an exception, change its date in a new pull request. The review of that pull request is the approval.
