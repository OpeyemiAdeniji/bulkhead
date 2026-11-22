#!/usr/bin/env bash
# End-to-end test of the capsule profile (docs/adr/0022-tenancy-profiles.md) against a real
# Capsule: installs cert-manager and Capsule at pinned versions on the kind cluster, creates the
# tenants of tests/checker/capsule/tenants.yaml, and runs the checker against them.
#
# 1. Bare tenants: exactly the checks of the protections that Capsule does not install by itself
#    fail, and the checks that do not apply are skipped with the profile's reason.
# 2. Configured tenants: every check passes.
#
# Usage: tests/checker/capsule-e2e.sh (or: make test-capsule). Needs the kind cluster
# (make cluster-up), Go, Helm and Python 3.
set -uo pipefail

cd "$(dirname "$0")/../.." || exit 1

CONTEXT="${KUBE_CONTEXT:-kind-bulkhead}"
CERT_MANAGER_VERSION="v1.21.3"
CAPSULE_VERSION="0.15.2"
BIN="bin/bulkhead"
failures=0
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

k() { kubectl --context "$CONTEXT" "$@"; }
pass() { echo "pass  $1"; }
fail() { echo "FAIL  $1"; failures=$((failures + 1)); }

make checker-build > /dev/null || { echo "the checker does not build"; exit 1; }

echo "== install cert-manager $CERT_MANAGER_VERSION and Capsule $CAPSULE_VERSION"
helm repo add jetstack https://charts.jetstack.io --force-update > /dev/null
helm repo add projectcapsule https://projectcapsule.github.io/charts --force-update > /dev/null
helm upgrade --install cert-manager jetstack/cert-manager --version "$CERT_MANAGER_VERSION" --kube-context "$CONTEXT" \
  --namespace cert-manager --create-namespace --set crds.enabled=true --wait --timeout 10m > /dev/null || { echo "cert-manager did not install"; exit 1; }
helm upgrade --install capsule projectcapsule/capsule --version "$CAPSULE_VERSION" --kube-context "$CONTEXT" \
  --namespace capsule-system --create-namespace --wait --timeout 10m > /dev/null || { echo "Capsule did not install"; exit 1; }

echo "== tenants, and a namespace created by each tenant's owner"
# The Tenant webhook may take a moment to answer after the install.
for _ in $(seq 1 20); do k apply -f tests/checker/capsule/tenants.yaml > /dev/null 2>&1 && break; sleep 3; done
for t in capb-alpha capb-bravo capc-alpha capc-bravo; do
  created=0
  for _ in $(seq 1 20); do
    if k get namespace "$t-1" > /dev/null 2>&1 ||
      k create namespace "$t-1" --as bulkhead-check --as-group "$t-admins" --as-group projectcapsule.dev > /dev/null 2>&1; then
      created=1
      break
    fi
    sleep 3
  done
  [ "$created" -eq 1 ] || { echo "the owner of $t could not create its namespace"; exit 1; }
done
# Wait until Capsule has put the configured protections into the namespaces.
for t in capc-alpha capc-bravo; do
  for _ in $(seq 1 40); do
    [ "$(k -n "$t-1" get networkpolicy,resourcequota,limitrange --no-headers 2> /dev/null | wc -l)" -ge 3 ] && break
    sleep 3
  done
done

# run NAME TENANTS... : run the checker with the capsule profile, as JSON, into $work/NAME.json.
run() {
  local name="$1"
  shift
  local args=()
  for t in "$@"; do args+=(--tenant "$t"); done
  "$BIN" check --context "$CONTEXT" --profile capsule "${args[@]}" --level basic,hardened -o json > "$work/$name.json" 2> "$work/$name.err"
  echo $?
}

# judge NAME CODE EXPECTED_CODE EXPECTED_FAILING_CHECKS: the run's exit code, the checks with a
# failed result, and the checks that must be skipped for the profile.
judge() {
  python3 - "$work/$1.json" "$2" "$3" "$4" << 'EOF'
import json, sys
path, code, want_code, want_fail = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), sys.argv[4]
r = json.load(open(path))
assert r["run"]["profile"] == "capsule", r["run"]["profile"]
failing = sorted({x["check"] for x in r["results"] if x["status"] in ("fail", "error", "inconclusive")})
want = sorted(want_fail.split()) if want_fail else []
problems = []
if code != want_code:
    problems.append(f"exit code {code}, want {want_code}")
if failing != want:
    problems.append(f"failing {failing}, want {want}")
skipped = {x["check"]: x.get("message", "") for x in r["results"] if x["status"] == "skipped" and "does not apply to the capsule profile" in x.get("message", "")}
if sorted(skipped) != ["ACC-04", "ACC-05", "GRD-05"]:
    problems.append(f"skipped for the profile: {sorted(skipped)}, want ACC-04, ACC-05, GRD-05")
if problems:
    print("; ".join(problems))
    for x in r["results"]:
        if x["status"] in ("fail", "error", "inconclusive"):
            print(f'      {x["check"]} {x["status"]} {x.get("subject", "")} {x.get("message", "")[:140]}')
    sys.exit(1)
EOF
}

echo "== bare Capsule tenants: only the protections Capsule does not install fail"
# Measured on Capsule 0.15.2: owners cannot read each other's secrets or exec into each other's
# pods, but without configuration there is no network policy, quota, limit range or Pod Security.
code="$(run bare capb-alpha capb-bravo)"
bare_fail="GRD-02 GRD-03 GRD-04 ADM-02 NET-04 NET-05 NET-06 QTA-01 QTA-04 QTA-05 QTA-06 QTA-07 QTA-08"
if out="$(judge bare "$code" 1 "$bare_fail")"; then
  pass "bare tenants fail exactly the network, quota, limit and Pod Security checks, and pass the access checks"
else
  fail "bare tenants: $out"
fi

echo "== configured Capsule tenants: every check passes"
code="$(run configured capc-alpha capc-bravo)"
if out="$(judge configured "$code" 0 "")"; then
  pass "configured tenants pass every check"
else
  fail "configured tenants: $out"
fi
# Which layer protects Capsule's own network policy: its webhook, since the admin role may delete network policies.
if python3 -c "
import json, sys
r = json.load(open('$work/configured.json'))
ok = [x for x in r['results'] if x['check'] == 'GRD-02' and x['status'] == 'pass' and 'projectcapsule.dev' in x.get('message', '')]
sys.exit(0 if len(ok) == 2 else 1)"; then
  pass "Capsule's own webhook refuses the deletion of its network policies (GRD-02)"
else
  fail "GRD-02 was not refused by Capsule's webhook"
fi

echo "== errors are clear"
if "$BIN" check --context "$CONTEXT" --profile capsule --tenant no-such-tenant --read-only > "$work/missing.out" 2>&1; then
  fail "a missing Capsule tenant was accepted"
elif grep -q 'capsule tenant "no-such-tenant" does not exist' "$work/missing.out"; then
  pass "a missing Capsule tenant is reported by name"
else
  fail "a missing Capsule tenant: $(head -c 300 "$work/missing.out")"
fi

echo
if [ "$failures" -gt 0 ]; then
  echo "$failures capsule test(s) failed"
  exit 1
fi
echo "all capsule tests passed"
