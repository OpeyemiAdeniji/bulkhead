#!/usr/bin/env bash
# End-to-end tests of bulkhead check on a real cluster: the safety rules of work package 2 in
# docs/phase-2-checklist.md. They need the kind cluster with both tenants
# (make cluster-up tenants-apply), Go, and Helm when Kyverno is not installed. Usage: tests/checker/e2e.sh (or: make test-checker-e2e)
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

echo "== the PolicyReport API"
# Kyverno serves the PolicyReport API. On a cluster without it (the isolation CI job), a run with
# --policy-reports must stop before any check; then only the CRD is installed, from the Kyverno
# chart pinned in examples/kind/kyverno, so that the tests below can store reports.
installed_crd=0
if ! k get crd policyreports.wgpolicyk8s.io > /dev/null 2>&1; then
  expect 3 "without the PolicyReport API, --policy-reports stops before any check" \
    "does not serve the PolicyReport API" -- \
    check "${TENANTS[@]}" --policy-reports
  helm repo add kyverno https://kyverno.github.io/kyverno/ --force-update > /dev/null
  helm template kyverno kyverno/kyverno --version "$(tr -d '[:space:]' < examples/kind/kyverno/chart-version)" \
    --show-only charts/crds/templates/wgpolicyk8s.io/wgpolicyk8s.io_policyreports.yaml |
    k apply --server-side -f - > /dev/null
  k wait --for=condition=Established crd/policyreports.wgpolicyk8s.io --timeout=60s > /dev/null
  installed_crd=1
else
  echo "skip  the cluster serves the PolicyReport API, so the run without it is not tested here"
fi

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
  "0 fail" "Clean-up: removed 20 test objects" \
  "PolicyReports: wrote tenant-alpha/bulkhead-check, tenant-bravo/bulkhead-check." -- \
  check "${TENANTS[@]}" --policy-reports

echo "== PolicyReport objects"
# reports_match TOTAL FAILS-OF-ALPHA: the two reports hold every result of the last run, once,
# and the report of tenant alpha counts the given failures.
reports_match() {
  k get policyreports -A -l app.kubernetes.io/managed-by=bulkhead-check -o json | python3 -c '
import json, sys
total, alpha_fails = int(sys.argv[1]), int(sys.argv[2])
items = {i["metadata"]["namespace"]: i for i in json.load(sys.stdin)["items"]}
assert sorted(items) == ["tenant-alpha", "tenant-bravo"], sorted(items)
assert all(i["metadata"]["name"] == "bulkhead-check" for i in items.values())
assert len({i["metadata"]["labels"]["bulkhead.io/run-id"] for i in items.values()}) == 1, "two runs"
held = sum(len(i["results"]) for i in items.values())
assert held == total, f"the reports hold {held} results, the run had {total}"
for ns, i in items.items():
    s = i["summary"]
    assert sum(s.values()) == len(i["results"]), (ns, s)
    assert all(r["source"] == "bulkhead" and (r["rule"].startswith(ns) or r["properties"].get("from") == ns) for r in i["results"]), ns
assert items["tenant-alpha"]["summary"]["fail"] == alpha_fails, items["tenant-alpha"]["summary"]
' "$1" "$2"
}
total="$(grep -oE '^[0-9]+ results:' "$out" | grep -oE '^[0-9]+')"
if [ -n "$total" ] && reports_match "$total" 0; then
  pass "the full run stored all $total results in one PolicyReport per tenant, without failures"
else
  fail "the PolicyReports of the full run do not match its results"
fi
# The tenant admin role of the chart may not change or delete the report.
if [ "$(k auth can-i delete policyreports -n tenant-alpha --as bulkhead-check --as-group alpha-admins)" = "no" ] &&
  [ "$(k auth can-i update policyreports -n tenant-alpha --as bulkhead-check --as-group alpha-admins)" = "no" ]; then
  pass "a tenant admin may not change or delete its PolicyReport"
else
  fail "a tenant admin may change or delete its PolicyReport"
fi
# Viewers have the built-in view role. Kyverno adds the reading of reports to it; without Kyverno, nothing does.
if k get clusterrole kyverno:rbac:view:policyreports > /dev/null 2>&1; then
  if [ "$(k auth can-i get policyreports -n tenant-alpha --as bulkhead-check --as-group alpha-viewers)" = "yes" ]; then
    pass "with Kyverno, a tenant viewer may read its PolicyReport"
  else
    fail "with Kyverno, a tenant viewer may not read its PolicyReport"
  fi
else
  echo "skip  Kyverno is not installed, so no role lets tenant viewers read PolicyReports"
fi
# A later run replaces the reports: with alpha able to read bravo's secrets, alpha's report shows the two failures.
k -n tenant-bravo create role break-secrets --verb=get,list --resource=secrets > /dev/null
k -n tenant-bravo create rolebinding break-secrets --role=break-secrets --group=alpha-admins > /dev/null
expect 1 "a later run replaces the PolicyReports" "PolicyReports: wrote tenant-alpha/bulkhead-check" -- \
  check "${TENANTS[@]}" --category access --policy-reports
k -n tenant-bravo delete rolebinding break-secrets > /dev/null
k -n tenant-bravo delete role break-secrets > /dev/null
total="$(grep -oE '^[0-9]+ results:' "$out" | grep -oE '^[0-9]+')"
if [ -n "$total" ] && reports_match "$total" 2; then
  pass "the replaced PolicyReport of tenant alpha shows its two failures"
else
  fail "the PolicyReports were not replaced by the later run"
fi
expect 2 "--policy-reports is refused in a read-only run" "cannot be combined with --read-only" -- \
  check "${TENANTS[@]}" --read-only --policy-reports
k delete policyreports -A -l app.kubernetes.io/managed-by=bulkhead-check > /dev/null
[ "$installed_crd" -eq 1 ] && k delete crd policyreports.wgpolicyk8s.io > /dev/null

echo "== reports: one run writes every format"
reports="$(mktemp -d)"
# A real run with two failures (alpha may read bravo's secrets): JSON on stdout, JUnit and SARIF to files.
k -n tenant-bravo create role break-secrets --verb=get,list --resource=secrets > /dev/null
k -n tenant-bravo create rolebinding break-secrets --role=break-secrets --group=alpha-admins > /dev/null
"$BIN" check "${TENANTS[@]}" --read-only --category access -o json \
  --report "junit=$reports/junit.xml" --report "sarif=$reports/out.sarif" --report "html=$reports/report.html" > "$reports/report.json" 2> /dev/null
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
h = open(d + "/report.html").read()
assert "2 results failed. Exit code 1." in h and h.count('class="s fail">') >= 2 and "<script" not in h, "html"
EOF
then
  pass "JUnit, SARIF and HTML report the same two failures as the JSON report"
else
  fail "JUnit, SARIF or HTML disagree with the JSON report"
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

echo "== declared flows: alpha may reach a shared service, bravo may not"
# A shared namespace with a web service on port 8080, and a flows file that declares the intent.
# The tenants deny all egress by default, so at first alpha cannot reach it either.
SHARED=bhc-e2e-shared
k create namespace "$SHARED" > /dev/null
k -n "$SHARED" run web --image=busybox:1.36 --port=8080 --labels=app=web \
  -- sh -c 'echo ok > /tmp/index.html && exec httpd -f -p 8080 -h /tmp' > /dev/null
k -n "$SHARED" expose pod web --port=8080 > /dev/null
k -n "$SHARED" wait --for=condition=Ready pod/web --timeout=120s > /dev/null
flows_file="$(mktemp --suffix=.yaml)"
cat > "$flows_file" << EOF
apiVersion: bulkhead.io/v1alpha1
kind: Flows
flows:
  - name: alpha-to-shared
    from: tenant-alpha
    to: {service: web, namespace: $SHARED, port: 8080}
    expect: allowed
  - name: bravo-not-to-shared
    from: tenant-bravo
    to: {service: web, namespace: $SHARED, port: 8080}
    expect: blocked
EOF
# allow_egress NAMESPACE NAME SELECTOR: an egress policy for every pod of the namespace.
allow_egress() {
  k apply -f - > /dev/null << EOF
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: $2, namespace: $1}
spec:
  podSelector: {}
  policyTypes: [Egress]
  egress: [$3]
EOF
}

expect 1 "a flow the policies do not allow fails FLW-01, and makes its blocked pair inconclusive" \
  "FLW-01 fail alpha-to-shared" "no connection to web.$SHARED.svc:8080" \
  "FLW-02 inconclusive bravo-not-to-shared" "may be down" -- \
  check "${TENANTS[@]}" --category flows --flows "$flows_file"

allow_egress tenant-alpha bhc-e2e-allow-shared "{to: [{namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: $SHARED}}}], ports: [{port: 8080, protocol: TCP}]}"
expect 0 "with an egress policy for alpha, both declared flows hold" \
  "FLW-01 pass alpha-to-shared" "FLW-02 pass bravo-not-to-shared" "while the allowed flow alpha-to-shared connected" "0 fail" -- \
  check "${TENANTS[@]}" --category flows --flows "$flows_file"

allow_egress tenant-bravo bhc-e2e-allow-all "{}"
expect 1 "when bravo may send anywhere, FLW-02 fails" \
  "FLW-01 pass alpha-to-shared" "FLW-02 fail bravo-not-to-shared" "must be blocked" -- \
  check "${TENANTS[@]}" --category flows --flows "$flows_file"

expect 2 "a flow from a namespace that is not under test is refused before the run" \
  "flows must start in a tenant under test" "bravo-not-to-shared (from tenant-bravo)" -- \
  check --context "$CONTEXT" --tenant tenant-alpha --flows "$flows_file"

k -n tenant-bravo delete networkpolicy bhc-e2e-allow-all > /dev/null

echo "== declared flows from inside a workload: only the orders pods of alpha may reach the shared service"
# A label policy: alpha's egress to the shared service is now allowed for pods with app=orders only.
k -n tenant-alpha delete networkpolicy bhc-e2e-allow-shared > /dev/null
k apply -f - > /dev/null << EOF
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: bhc-e2e-allow-orders, namespace: tenant-alpha}
spec:
  podSelector: {matchLabels: {app: orders}}
  policyTypes: [Egress]
  egress: [{to: [{namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: $SHARED}}}], ports: [{port: 8080, protocol: TCP}]}]
---
apiVersion: v1
kind: Pod
metadata: {name: e2e-orders, namespace: tenant-alpha, labels: {app: orders}}
spec:
  securityContext: {runAsNonRoot: true, runAsUser: 65534, seccompProfile: {type: RuntimeDefault}}
  terminationGracePeriodSeconds: 0
  containers:
    - name: app
      image: busybox:1.36
      command: [sleep, "3600"]
      resources: {requests: {cpu: 10m, memory: 16Mi}, limits: {cpu: 50m, memory: 32Mi}}
      securityContext: {allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}}
EOF
k -n tenant-alpha wait --for=condition=Ready pod/e2e-orders --timeout=120s > /dev/null
workload_flows="$(mktemp --suffix=.yaml)"
cat > "$workload_flows" << EOF
apiVersion: bulkhead.io/v1alpha1
kind: Flows
flows:
  - name: orders-to-shared
    from: tenant-alpha
    via: {app: orders}
    to: {service: web, namespace: $SHARED, port: 8080}
    expect: allowed
  - name: rest-of-alpha-not-to-shared
    from: tenant-alpha
    to: {service: web, namespace: $SHARED, port: 8080}
    expect: blocked
EOF

# The checker's own client pod does not have the label, so the plain flow of alpha now fails:
# a probe from the namespace cannot test a policy written for one workload.
expect 1 "with a label policy, the probe from the checker's pod no longer reaches the service" \
  "FLW-01 fail alpha-to-shared" -- \
  check "${TENANTS[@]}" --category flows --flows "$flows_file"

expect 2 "flows with via are refused without --probe-workloads" "allow that with --probe-workloads" -- \
  check "${TENANTS[@]}" --flows "$workload_flows"

expect 0 "from inside the orders pod the flow works, and from the rest of the namespace it is blocked" \
  "FLW-01 pass orders-to-shared" "from inside tenant-alpha/e2e-orders" \
  "FLW-02 pass rest-of-alpha-not-to-shared" "while the allowed flow orders-to-shared connected" \
  "Probe containers were added to tenant-alpha/e2e-orders" "0 fail" -- \
  check "${TENANTS[@]}" --category flows --flows "$workload_flows" --probe-workloads

# The run told its probe container to exit: it is terminated, and still listed in the pod's spec.
state=""
for _ in $(seq 1 30); do
  state="$(k -n tenant-alpha get pod e2e-orders -o jsonpath='{.status.ephemeralContainerStatuses[0].state.terminated.reason}')"
  [ -n "$state" ] && break
  sleep 1
done
if [ -n "$state" ] && [ "$(k -n tenant-alpha get pod e2e-orders -o jsonpath='{.spec.ephemeralContainers[0].securityContext.runAsNonRoot}')" = "true" ]; then
  pass "the restricted probe container exited after the run ($state), and stays in the pod's spec"
else
  fail "the probe container did not exit after the run, or is not restricted"
fi

k -n tenant-alpha delete pod e2e-orders --wait=false > /dev/null
k -n tenant-alpha delete networkpolicy bhc-e2e-allow-orders > /dev/null
k delete namespace "$SHARED" --wait=false > /dev/null
rm -f "$flows_file" "$workload_flows"

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
