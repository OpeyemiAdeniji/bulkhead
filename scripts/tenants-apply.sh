#!/usr/bin/env bash
# Render every tenant file and apply it to a cluster: the local kind cluster, or the kubectl
# context in KUBE_CONTEXT, like the test suites.
# In v0.3 Argo CD replaces this step. Removing a tenant file does not remove the
# tenant here, because kubectl apply does not prune.
# Usage: [KUBE_CONTEXT=<context>] [TENANT_VALUES=<file>] scripts/tenants-apply.sh (or: make tenants-apply)
set -euo pipefail

cd "$(dirname "$0")/.."

CONTEXT="${KUBE_CONTEXT:-kind-bulkhead}"

for tool in helm kubectl; do
  command -v "$tool" > /dev/null || { echo "missing tool: $tool" >&2; exit 1; }
done

# Cluster-wide values of the tenant chart, passed before each tenant file (ADR 0016).
values=""
if [ -n "${TENANT_VALUES:-}" ]; then
  [ -f "$TENANT_VALUES" ] || { echo "TENANT_VALUES file not found: $TENANT_VALUES" >&2; exit 1; }
  values="-f $TENANT_VALUES"
fi

for file in tenants/*.yaml; do
  name="$(basename "$file" .yaml)"
  echo "==> applying tenant $name to $CONTEXT"
  # shellcheck disable=SC2086 # $values is empty or "-f FILE", and is split on purpose
  helm template "tenant-$name" charts/bulkhead-tenant $values -f "$file" \
    | kubectl --context "$CONTEXT" apply -f -
done
