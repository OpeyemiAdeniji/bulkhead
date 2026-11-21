# Reports of bulkhead check

One run of `bulkhead check` produces one report. It can be printed in one format (`-o`) and written to files in other formats at the same time (`--report FORMAT=PATH`, repeatable):

```bash
bulkhead check --tenant tenant-alpha --tenant tenant-bravo \
  --report json=reports/bulkhead.json --report junit=reports/junit.xml \
  --report sarif=reports/bulkhead.sarif --report html=reports/bulkhead.html
```

Network results carry `from` and `to` (a tenant namespace, `dns` or `internet`), which the HTML report uses to draw its matrix. Declared flows (`--flows`, [declared-flows.md](declared-flows.md)) carry them too, with the destination's namespace or host as `to`, and are listed in their own section instead of the matrix. With `--probe-workloads`, every report names the pods that received a probe container (`workloadProbes` in JSON).

Every report names the tenancy profile of the run (`--profile`, `run.profile` in JSON, [ADR 0022](adr/0022-tenancy-profiles.md)). A check that does not apply to the profile is reported as skipped, with the reason. Every format carries the same results. The exit code is the same whatever the format: 0 when every selected check passed, 1 when one failed or was inconclusive, 2 for a usage error, 3 when the checks could not run ([ADR 0018](adr/0018-checker-in-this-repository.md)).

| Format | For | What it contains |
|---|---|---|
| `table` (default) | people | the cluster, your identity, the tenants, one line per result with the layer that refused, the summary, how to fix each check that did not pass, and the clean-up |
| `json` | programs and records | everything: the run (times, context, Kubernetes version, mode), the tenants and their identities, the full text of every check of the run, every result, the requests of a dry run, the summary and the exit code. It matches [`schemas/check-report.schema.json`](../schemas/check-report.schema.json). |
| `junit` | CI systems | one test suite per category and one test case per result. Failed and inconclusive results are failures, errors are errors, and skipped, not implemented and planned results are skipped. A failure carries the check's title and fix. |
| `html` | evidence for people, for example an auditor or a review after a cluster upgrade | one self-contained page, with no scripts and nothing loaded from elsewhere, so it opens offline and can be archived: the verdict, the run, the tenants, a **network matrix** (source tenant by destination, including DNS and the internet, with what each cell must be and what was observed), how to fix each failure, every result with the layer that decided it, and the full text of every check, including what it does not prove. It follows the reader's light or dark setting and prints cleanly. |
| `sarif` | GitHub code scanning and other SARIF readers | SARIF 2.1.0. Every check of the run is a rule, with its description, fix and a link to its section in [checks.md](checks.md). The tags of a rule include the benchmark items the check tests, for example `CIS 5.2.2` ([benchmarks.md](benchmarks.md)). Failed results are errors; inconclusive and error results are warnings; passing results are left out. |

## The JSON report

The report has `apiVersion: bulkhead.io/v1alpha1` and `kind: CheckReport`. The `v1alpha1` means that its shape may change before v1.0 of Bulkhead; every change is listed in the [changelog](../CHANGELOG.md), and the schema in `schemas/` always matches the program. A unit test validates a report with every status against the schema, and the end-to-end test validates the report of a real run on kind.

A short way to read it:

```bash
jq '.summary, (.results[] | select(.status != "pass") | {check, subject, message})' reports/bulkhead.json
```

## Where SARIF results point

A cluster has no source file. Each SARIF result therefore points to the path `kubernetes/<context>/<namespace>`, and names the namespace as a logical location. Code scanning shows such results without a code snippet. The fingerprint of a result is made from the context, the check and the subject, so the same problem on the same tenant is recognised as one alert across runs.

## PolicyReport objects in the cluster

With `--policy-reports`, the run also stores its results in the cluster, as one `PolicyReport` (`wgpolicyk8s.io/v1alpha2`, the Policy Report API of the Kubernetes Policy working group) named `bulkhead-check` in each tenant namespace. Dashboards and tools that already read these reports, such as the reports Kyverno writes, then show the isolation of each tenant next to the policy results.

```bash
bulkhead check --tenant tenant-alpha --tenant tenant-bravo --policy-reports
kubectl get policyreports -A -l app.kubernetes.io/managed-by=bulkhead-check
```

```text
NAMESPACE      NAME             KIND        NAME           PASS   FAIL   WARN   ERROR   SKIP   AGE
tenant-alpha   bulkhead-check   Namespace   tenant-alpha   5      0      0      0       0      1s
tenant-bravo   bulkhead-check   Namespace   tenant-bravo   3      0      0      0       2      1s
```

- **Off by default.** It stores objects, so it cannot be combined with `--read-only` or `--dry-run`.
- **Checked before any check runs.** The run stops at once (exit code 3) when the cluster does not serve the API, or when you may not get, create and update `policyreports` in every tenant namespace. Kyverno installs the API; on a cluster without Kyverno, install its CRD from [wg-policy-prototypes](https://github.com/kubernetes-sigs/wg-policy-prototypes).
- **Each run replaces the report**, so it always shows the latest run. An interrupted run writes none, so a report never shows half a run. The reports are an output, not test objects: the clean-up of a run and `bulkhead cleanup` leave them alone. Remove them with `kubectl delete policyreports -A -l app.kubernetes.io/managed-by=bulkhead-check`.
- **A result belongs to the tenant it tested:** the source of a network result, or the tenant its subject starts with. A result between two tenants is in the report of the tenant that acted. The table, JSON and HTML reports name the reports written, and count any result that names no tenant (none of today's checks).
- **Who can read and change them.** The tenant admin role of the `bulkhead-tenant` chart may not read, change or delete `policyreports`. Tenant viewers have the built-in `view` role; Kyverno adds the reading of reports to it, so with Kyverno they may read their report, and without it nobody in the tenant may. The end-to-end test checks both. With other roles, for example the built-in `admin` role, a tenant may be able to change its report, so keep the JSON or HTML report as the record.

Each result maps as follows:

| PolicyReport field | Value |
|---|---|
| `source` | `bulkhead` |
| `policy` | the check ID, for example `NET-04` |
| `rule` | the subject, for example `tenant-alpha -> tenant-bravo` |
| `category` | the category of the check, for example `network` |
| `result` | `pass` and `fail` as they are; `inconclusive` becomes `warn` (the check could not decide, because its pair did not pass either); `error` stays `error`; `skipped` and `not-implemented` become `skip` |
| `scored` | true for `pass` and `fail` only |
| `severity` | from the level of the check: `basic` is `high`, `hardened` is `medium`, `research` is `low` |
| `message` | the message of the result, or else the title of the check |
| `properties` | `title`, `status` (the status of Bulkhead), `expect`, `level`, `runId`, `docs` (a link to the check in [checks.md](checks.md)), and `layer`, `from` and `to` when the result has them |

## In GitHub Actions

```yaml
- name: Check tenant isolation
  run: bulkhead check --tenant tenant-alpha --tenant tenant-bravo --report sarif=bulkhead.sarif --report junit=junit.xml
- name: Upload to code scanning
  if: always()
  uses: github/codeql-action/upload-sarif@<pinned commit>
  with:
    sarif_file: bulkhead.sarif
    category: bulkhead-check
```

A GitHub Action that runs the checker and comments on the pull request is planned ([phase-2-checklist.md](phase-2-checklist.md), work package 10).
