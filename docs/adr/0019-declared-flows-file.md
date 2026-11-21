# 0019. Teams declare their own expected flows in a file

- Status: accepted
- Date: 2026-11-17

## Context

The built-in network checks (NET-01 to NET-06) test the isolation that every tenant needs: a tenant reaches itself and DNS, and nothing else. Real platforms also have flows of their own: a tenant may reach a shared database, a monitoring endpoint may scrape every tenant, and nobody else may reach a payments service. The checker cannot guess those flows, and a team that writes network policies for them has no way to test them other than by hand.

Work package 6 of `docs/phase-2-checklist.md` asks for a file in which teams declare such flows, with a JSON Schema.

The safety and trust rules of the checker apply here as well (ADR 0012): probes are made from labelled test pods acting as the tenant, nothing is sent in a dry run, and a "must be blocked" result counts only when a matching "must work" result shows that the test itself works.

## Options considered

1. **Read the flows from the network policies themselves.** No file to write, but it tests the policies against themselves: a wrong policy would be declared correct. Rejected.
2. **A file of flows between tenants and destinations, probed from the checker's test pods** (chosen). The team states its intent, and the checker tests the cluster against that intent.
3. **Probes from inside the team's own workloads.** The most realistic source, because the real pod's labels and service account apply. It needs ephemeral containers and changes running workloads, so it is a separate, optional item of the checklist and not the default.

For the probe, HTTP (`wget`, as NET-01 to NET-06 use) only tests web servers. A declared destination can be a database or any TCP service, so the probe is a TCP connection (`nc -z`) that sends no data.

## Decision

- **Format.** A YAML or JSON file with `apiVersion: bulkhead.io/v1alpha1`, `kind: Flows` and a list of `flows`. Each flow has a `name` (unique, a DNS label), a `from` tenant namespace, a destination and `expect: allowed` or `expect: blocked`. The destination is either a Service (`service`, `namespace`, `port`) or a host (`host`, a DNS name or IP address, and `port`). The schema is published as `schemas/flows.schema.json`, and the checker refuses a file that does not match it, with exit code 2, before it connects to the cluster.
- **The pair rule for declared flows.** Every blocked flow must have an allowed flow to the same destination in the same file, and its result counts only when that allowed flow passed. Otherwise a destination that is down, or a port that is wrong, would make every blocked flow pass. The checker refuses a file without such a pair.
- **Sources.** `from` must be one of the tenants under test, because the probe runs from the checker's client pod in that namespace, which the tenant admin started. Flows from other namespaces are not tested.
- **Results.** Two new checks in the catalogue, in a new category `flows`: FLW-01 (a declared allowed flow works) and FLW-02 (a declared blocked flow is blocked, pair FLW-01). Each flow is one result, with its name as subject and `from` and `to` set, so it appears in every report format and in the network matrix of the HTML report. The two checks run only when `--flows FILE` is given.
- **Verdicts.** An allowed flow passes when the connection is made. A blocked flow passes when the connection times out, or when it is refused while its allowed pair connected to the same destination, which shows that the refusal comes from the network and not from a closed port. A name that does not resolve is an error, not a pass.

## Consequences

- Teams can test the flows their own network policies are meant to allow and deny, in the same run and reports as the built-in checks.
- The file is versioned (`v1alpha1`) and may change before v1.0 of Bulkhead; changes are announced in the changelog.
- Probes come from the checker's pods, with the checker's labels. A policy that selects pods by their own labels (`app: billing`) is not exercised by these probes; the optional ephemeral-container probes of the checklist address that later.
- A destination that only some tenants may reach needs at least one tenant under test that may reach it, because of the pair rule.
