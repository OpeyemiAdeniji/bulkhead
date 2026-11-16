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
    show
    return
  fi
  for t in "${texts[@]}"; do
    if [ "$t" = "0 fail" ]; then
      # "0 fail" means: no failed result in the summary line.
      if grep -qE '^[0-9]+ results:.* [1-9][0-9]* fail' "$out"; then
        fail "$what (a check failed)"
        show
        return
      fi
      continue
    fi
    if ! grep -qF -- "$t" "$out"; then
      fail "$what (the output lacks: $t)"
      show
      return
    fi
  done
  pass "$what"
}

# show prints, after a failure, every result that did not pass and the end of the output.
show() {
  grep -E '^[A-Z]{3}-[0-9]{2} (fail|error|inconclusive) ' "$out" | sed 's/^/      /'
  sed 's/^/      /' "$out" | tail -6
}

leftovers() { k get pods,services,secrets,configmaps,deployments -A -l app.kubernetes.io/managed-by=bulkhead-check --no-headers 2> /dev/null | wc -l; }

make checker-build > /dev/null || { echo "the checker does not build"; exit 1; }

echo "== safety rules on a real cluster"
# A dry run sends nothing and lists the requests: one identity review per tenant identity.
expect 0 "a dry run sends nothing and lists its requests" \
  "Dry run: nothing was sent" "review SelfSubjectReview as bulkhead-check (groups alpha-admins)" -- \
  check "${TENANTS[@]}" --dry-run

# A run confirms every tenant identity with the API server through impersonation, and every
# check of the basic level passes on the isolated cluster. The network, noisy-neighbour and
# quota checks start 20 test pods along the way, and the run removes them.
expect 0 "a run confirms each identity, and every check passes" \
  "Every identity was confirmed with the API server" "tenant-bravo: admin bulkhead-check (groups bravo-admins)" \
  "ACC-02 pass" "GRD-05 pass" "ADM-02 pass tenant-alpha" "refused by pod-security" "refused by quota (tenant-quota)" \
  "refused by limit-range" "NET-04 pass tenant-alpha -> tenant-bravo" "blocked: the connection timed out" \
  "RES-03 pass" "OOMKilled" "QTA-08 pass tenant-alpha" "filled the pod count" \
  "0 fail" "Clean-up: removed 20 test objects" -- \
  check "${TENANTS[@]}"

echo "== reports: one run writes every format"
reports="$(mktemp -d)"
# A real run with two failures (alpha may read bravo's secrets): JSON on stdout, JUnit and SARIF to files.
k -n tenant-bravo create role break-secrets --verb=get,list --resource=secrets > /dev/null
k -n tenant-bravo create rolebinding break-secrets --role=break-secrets --group=alpha-admins > /dev/null
"$BIN" check "${TENANTS[@]}" --read-only --category access -o json \
  --report "junit=$reports/junit.xml" --report "sarif=$reports/out.sarif" > "$reports/report.json" 2> /dev/null
code=$?
k -n tenant-bravo delete rolebinding break-secrets > /dev/null
k -n tenant-bravo delete role break-secrets > /dev/null
if [ "$code" -eq 1 ] && BULKHEAD_REPORT_FILE="$reports/report.json" go test -count=1 -run TestAReportFileMatchesTheSchema ./internal/report > /dev/null; then
  pass "the JSON report of a real run matches schemas/check-report.schema.json"
else
  fail "the JSON report of a real run does not match the schema (exit code $code)"
fi
if python3 - "$reports" << 'EOF'
import json, sys, xml.etree.ElementTree as ET
d = sys.argv[1]
r = json.load(open(d + "/report.json"))
assert r["exitCode"] == 1 and r["summary"]["fail"] == 2, (r["exitCode"], r["summary"])
j = ET.parse(d + "/junit.xml").getroot()
assert int(j.get("failures")) == 2 and int(j.get("tests")) == r["summary"]["total"], j.attrib
s = json.load(open(d + "/out.sarif"))
results = s["runs"][0]["results"]
assert s["version"] == "2.1.0" and len(results) == 2 and {x["ruleId"] for x in results} == {"ACC-02", "ACC-03"}, results
EOF
then
  pass "JUnit and SARIF report the same two failures as the JSON report"
else
  fail "JUnit or SARIF disagree with the JSON report"
fi
rm -rf "$reports"

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

# Without network policies, traffic between tenants and to the internet flows: exactly the
# "blocked" network checks fail (8 cross-tenant paths and 2 internet probes), and the "must work" ones pass.
k -n tenant-alpha delete networkpolicy --all > /dev/null
k -n tenant-bravo delete networkpolicy --all > /dev/null
expect 1 "without network policies, exactly NET-04, NET-05 and NET-06 fail" \
  "NET-01 pass" "NET-02 pass" "NET-03 pass" "NET-04 fail" "NET-05 fail" "NET-06 fail" \
  "the server answered, but this traffic must be blocked" " pass, 10 fail" -- \
  check "${TENANTS[@]}" --category network --level basic,hardened
make tenants-apply > /dev/null

# Without its resource quota, tenant alpha may claim anything and open node ports and load
# balancers: alpha's quota checks fail, and bravo's still pass.
k -n tenant-alpha delete resourcequota tenant-quota > /dev/null
expect 1 "without a quota, exactly the quota checks of that tenant fail" \
  "QTA-01 fail tenant-alpha no resource quota limits requests.cpu" "QTA-04 fail tenant-alpha" "QTA-05 fail tenant-alpha" \
  "QTA-08 fail tenant-alpha" "QTA-01 pass tenant-bravo" "QTA-04 pass tenant-bravo" "QTA-09 pass tenant-bravo" -- \
  check "${TENANTS[@]}" --category quota
make tenants-apply > /dev/null

echo "== interrupt: Ctrl+C while test pods exist"
"$BIN" check "${TENANTS[@]}" --category network > "$out" 2>&1 &
pid=$!
for _ in $(seq 1 120); do
  [ "$(leftovers)" -gt 0 ] && break
  sleep 1
done
if [ "$(leftovers)" -eq 0 ]; then
  fail "the run created no test pods to interrupt"
else
  sleep 2
  kill -INT "$pid"
  wait "$pid"
  code=$?
  if [ "$code" -eq 3 ] && grep -q "interrupted" "$out" && [ "$(leftovers)" -eq 0 ]; then
    pass "after Ctrl+C the run stops with exit code 3 and removes its test pods"
  else
    fail "after Ctrl+C: exit code $code, $(leftovers) test objects left"
    sed 's/^/      /' "$out" | tail -8
  fi
fi

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
