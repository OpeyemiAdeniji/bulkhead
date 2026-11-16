# Reports of bulkhead check

One run of `bulkhead check` produces one report. It can be printed in one format (`-o`) and written to files in other formats at the same time (`--report FORMAT=PATH`, repeatable):

```bash
bulkhead check --tenant tenant-alpha --tenant tenant-bravo \
  --report json=reports/bulkhead.json --report junit=reports/junit.xml \
  --report sarif=reports/bulkhead.sarif --report html=reports/bulkhead.html
```

Network results carry `from` and `to` (a tenant namespace, `dns` or `internet`), which the HTML report uses to draw its matrix.

Every format carries the same results. The exit code is the same whatever the format: 0 when every selected check passed, 1 when one failed or was inconclusive, 2 for a usage error, 3 when the checks could not run ([ADR 0018](adr/0018-checker-in-this-repository.md)).

| Format | For | What it contains |
|---|---|---|
| `table` (default) | people | the cluster, your identity, the tenants, one line per result with the layer that refused, the summary, how to fix each check that did not pass, and the clean-up |
| `json` | programs and records | everything: the run (times, context, Kubernetes version, mode), the tenants and their identities, the full text of every check of the run, every result, the requests of a dry run, the summary and the exit code. It matches [`schemas/check-report.schema.json`](../schemas/check-report.schema.json). |
| `junit` | CI systems | one test suite per category and one test case per result. Failed and inconclusive results are failures, errors are errors, and skipped, not implemented and planned results are skipped. A failure carries the check's title and fix. |
| `html` | evidence for people, for example an auditor or a review after a cluster upgrade | one self-contained page, with no scripts and nothing loaded from elsewhere, so it opens offline and can be archived: the verdict, the run, the tenants, a **network matrix** (source tenant by destination, including DNS and the internet, with what each cell must be and what was observed), how to fix each failure, every result with the layer that decided it, and the full text of every check, including what it does not prove. It follows the reader's light or dark setting and prints cleanly. |
| `sarif` | GitHub code scanning and other SARIF readers | SARIF 2.1.0. Every check of the run is a rule, with its description, fix and a link to its section in [checks.md](checks.md). Failed results are errors; inconclusive and error results are warnings; passing results are left out. |

## The JSON report

The report has `apiVersion: bulkhead.io/v1alpha1` and `kind: CheckReport`. The `v1alpha1` means that its shape may change before v1.0 of Bulkhead; every change is listed in the [changelog](../CHANGELOG.md), and the schema in `schemas/` always matches the program. A unit test validates a report with every status against the schema, and the end-to-end test validates the report of a real run on kind.

A short way to read it:

```bash
jq '.summary, (.results[] | select(.status != "pass") | {check, subject, message})' reports/bulkhead.json
```

## Where SARIF results point

A cluster has no source file. Each SARIF result therefore points to the path `kubernetes/<context>/<namespace>`, and names the namespace as a logical location. Code scanning shows such results without a code snippet. The fingerprint of a result is made from the context, the check and the subject, so the same problem on the same tenant is recognised as one alert across runs.

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
