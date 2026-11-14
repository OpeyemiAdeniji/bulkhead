# Security policy

This is a reference implementation, not a production platform. It has no real tenants and no supported releases with security fixes.

## Reporting a problem

If you find a way to break one of the isolation or policy claims in the README, for example a tenant that can reach another tenant or remove its own guardrails, please report it privately through [GitHub private vulnerability reporting](https://github.com/OpeyemiAdeniji/bulkhead/security/advisories/new).

Include the steps to reproduce it, the versions of kind, Kubernetes, Calico and Kyverno, and what you expected to happen. A claim that turns out to be wrong will be corrected in the README and covered by a new test.

## Scope

In scope: the tenant chart, the Kyverno policies, the contract schema, the scripts and the claims that the tests make.

Out of scope: vulnerabilities in Kubernetes, kind, Calico, Kyverno or Helm themselves. Please report those to the upstream projects.
