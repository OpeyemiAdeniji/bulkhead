#!/usr/bin/env bash
# End-to-end tests of bulkhead check on a real cluster: the safety rules of work package 2 in
# docs/phase-2-checklist.md. They need the kind cluster with both tenants
# (make cluster-up tenants-apply) and Go. Usage: tests/checker/e2e.sh (or: make test-checker-e2e)
set -uo pipefail

cd "$(dirname "$0")/../.." || exit 1

CONTEXT="${KUBE_CONTEXT:-kind-bulkhead}"
BIN="bin/bulkhead"
TENANTS=(--context "$CONTEXT" --tenant tenant-alpha --tenant tenant-bravo)
failures=0
out="$(mktemp)"
trap 'rm -f "$out"' EXIT

k() { kubectl --context "$CONTEXT" "$@"; }
pass() { echo "pass  $1"; }
fail() { echo "FAIL  $1"; failures=$((failures + 1)); }

# expect CODE DESCRIPTION TEXT... -- ARGS: run the checker, check the exit code and that the output has each text.
expect() {
  local want="$1" what="$2"
  shift 2
  local texts=()
  while [ "$1" != "--" ]; do texts+=("$1"); shift; done
  shift
  "$BIN" "$@" > "$out" 2>&1
  local code=$?
  if [ "$code" -ne "$want" ]; then
    fail "$what (exit code $code, want $want)"
    sed 's/^/      /' "$out" | tail -15
    return
  fi
  for t in "${texts[@]}"; do
    if ! grep -qF -- "$t" "$out"; then
      fail "$what (the output lacks: $t)"
      sed 's/^/      /' "$out" | tail -15
      return
    fi
  done
  pass "$what"
}

leftovers() { k get pods,services,secrets,configmaps,deployments -A -l app.kubernetes.io/managed-by=bulkhead-check --no-headers 2> /dev/null | wc -l; }

make checker-build > /dev/null || { echo "the checker does not build"; exit 1; }

echo "== safety rules on a real cluster"
# A dry run sends nothing and lists the requests: one identity review per tenant identity.
expect 0 "a dry run sends nothing and lists its requests" \
  "Dry run: nothing was sent" "review SelfSubjectReview as bulkhead-check (groups alpha-admins)" -- \
  check "${TENANTS[@]}" --dry-run

# A run confirms every tenant identity with the API server through impersonation. No check is
# implemented yet, so nothing ran and the exit code is 3. Update this when the first check lands.
expect 3 "a run confirms each tenant identity with the API server" \
  "Every identity was confirmed with the API server" "tenant-bravo: admin bulkhead-check (groups bravo-admins)" "Clean-up: removed 0 test objects" -- \
  check "${TENANTS[@]}"

expect 3 "a read-only run confirms the identities too" \
  "(read-only)" "Every identity was confirmed" -- \
  check "${TENANTS[@]}" --read-only

expect 2 "a system namespace is refused" "system namespace" -- check --context "$CONTEXT" --tenant kube-system
expect 3 "a namespace that does not exist is reported" "does not exist" -- check --context "$CONTEXT" --tenant tenant-nope
expect 3 "a tenant without the admin binding is reported" "--admin-binding" -- check "${TENANTS[@]}" --admin-binding nope

echo "== clean-up of a run that was killed"
k -n tenant-alpha create configmap bhc-e2e-left --from-literal=a=b > /dev/null
k -n tenant-alpha label configmap bhc-e2e-left app.kubernetes.io/managed-by=bulkhead-check bulkhead.io/run-id=e2e-killed > /dev/null
k -n tenant-alpha create configmap tenant-own --from-literal=a=b > /dev/null
k -n tenant-alpha label configmap tenant-own bulkhead.io/run-id=e2e-killed > /dev/null
expect 0 "cleanup removes the objects of a run" "Removed 1 test objects" -- \
  cleanup --context "$CONTEXT" --tenant tenant-alpha --run-id e2e-killed
if k -n tenant-alpha get configmap tenant-own > /dev/null 2>&1; then
  pass "cleanup leaves objects without the managed-by label alone"
else
  fail "cleanup removed an object that the checker did not make"
fi
k -n tenant-alpha delete configmap tenant-own --ignore-not-found > /dev/null

n="$(leftovers)"
if [ "$n" -eq 0 ]; then pass "no test objects are left in the cluster"; else fail "$n test objects are left in the cluster"; fi

echo
if [ "$failures" -gt 0 ]; then
  echo "$failures checker end-to-end test(s) failed"
  exit 1
fi
echo "all checker end-to-end tests passed"
