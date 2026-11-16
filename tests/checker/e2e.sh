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
  # Spaces are squeezed, so the expected texts do not depend on the table's column widths.
  "$BIN" "$@" 2>&1 | tr -s ' ' > "$out"
  local code=${PIPESTATUS[0]}
  if [ "$code" -ne "$want" ]; then
    fail "$what (exit code $code, want $want)"
    sed 's/^/      /' "$out" | tail -15
    return
  fi
  for t in "${texts[@]}"; do
    if [ "$t" = "0 fail" ]; then
      # "0 fail" means: no failed result in the summary line.
      if grep -qE '^[0-9]+ results:.* [1-9][0-9]* fail' "$out"; then
        fail "$what (a check failed)"
        sed 's/^/      /' "$out" | tail -15
        return
      fi
      continue
    fi
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

# A run confirms every tenant identity with the API server through impersonation, and every
# implemented check passes on the isolated cluster. The active checks are not implemented yet.
expect 0 "a run confirms each identity, and every implemented check passes" \
  "Every identity was confirmed with the API server" "tenant-bravo: admin bulkhead-check (groups bravo-admins)" \
  "ACC-02 pass" "GRD-05 pass" "ADM-02 pass tenant-alpha" "refused by pod-security" "refused by quota (tenant-quota)" \
  "refused by limit-range" "Clean-up: removed 0 test objects" -- \
  check "${TENANTS[@]}"

expect 0 "a read-only run skips the active checks" \
  "(read-only)" "Every identity was confirmed" "active check, not run in read-only mode" -- \
  check "${TENANTS[@]}" --read-only

echo "== break tests: remove one protection, and exactly its checks must fail"
# alpha's admins may read bravo's secrets: ACC-02 and ACC-03 fail for alpha -> bravo, and only there.
k -n tenant-bravo create role break-secrets --verb=get,list --resource=secrets > /dev/null
k -n tenant-bravo create rolebinding break-secrets --role=break-secrets --group=alpha-admins > /dev/null
expect 1 "cross-tenant secret access fails ACC-02 and ACC-03" \
  "ACC-02 fail tenant-alpha -> tenant-bravo" "ACC-03 fail tenant-alpha -> tenant-bravo" \
  "ACC-02 pass tenant-bravo -> tenant-alpha" " pass, 2 fail" -- \
  check "${TENANTS[@]}" --read-only --category access
k -n tenant-bravo delete rolebinding break-secrets > /dev/null
k -n tenant-bravo delete role break-secrets > /dev/null

# The built-in admin role lets a tenant create role bindings: GRD-05 fails for alpha.
k -n tenant-alpha create rolebinding break-admin --clusterrole=admin --group=alpha-admins > /dev/null
expect 1 "the built-in admin role fails GRD-05" \
  "GRD-05 fail tenant-alpha" "GRD-05 pass tenant-bravo" -- \
  check "${TENANTS[@]}" --read-only --id GRD-05
k -n tenant-alpha delete rolebinding break-admin > /dev/null

expect 0 "with the protections back, the same checks pass" "0 fail" -- \
  check "${TENANTS[@]}" --read-only --category access,guardrails

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
