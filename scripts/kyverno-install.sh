#!/usr/bin/env bash
# Install Kyverno into the local kind cluster at the pinned chart version, then the
# bulkhead-platform chart with the kind values, then the policy exceptions in exceptions/.
# On other clusters, install Kyverno your own way and only the bulkhead-platform chart
# (docs/install.md). Usage: scripts/kyverno-install.sh (or: make kyverno-up)
set -euo pipefail

cd "$(dirname "$0")/.."

CONTEXT="kind-bulkhead"
VERSION="$(tr -d '[:space:]' < examples/kind/kyverno/chart-version)"

for tool in helm kubectl; do
  command -v "$tool" > /dev/null || { echo "missing tool: $tool" >&2; exit 1; }
done
[ -n "$VERSION" ] || { echo "examples/kind/kyverno/chart-version is empty" >&2; exit 1; }

helm repo add kyverno https://kyverno.github.io/kyverno/ --force-update > /dev/null
helm repo update kyverno > /dev/null

# The kyverno CLI is pinned in examples/kind/kyverno/app-version. The chart must install the same version.
chart_app="$(helm show chart kyverno/kyverno --version "$VERSION" | awk '/^appVersion:/ {print $2}' | tr -d '"')"
want_app="$(tr -d '[:space:]' < examples/kind/kyverno/app-version)"
if [ "${chart_app#v}" != "${want_app#v}" ]; then
  echo "chart $VERSION installs Kyverno '${chart_app:-unknown}', but examples/kind/kyverno/app-version says $want_app" >&2
  exit 1
fi

start=$SECONDS
echo "==> installing Kyverno chart $VERSION"
echo "    Helm waits for the pods and prints nothing meanwhile (up to about 3 minutes). Do not press Ctrl+C: it leaves the release in a pending state."
helm upgrade --install kyverno kyverno/kyverno \
  --version "$VERSION" \
  --namespace kyverno --create-namespace \
  --kube-context "$CONTEXT" \
  --values examples/kind/kyverno/values.yaml \
  --wait --timeout 10m

echo "==> installing the bulkhead-platform chart"
# Helm --wait returns when the deployment is ready, and in one CI run the next command ran 70 ms later and
# got "connection refused" from the service. Wait until the service has an address behind it first.
kubectl --context "$CONTEXT" -n kyverno wait --for=condition=Available deployment/kyverno-admission-controller --timeout=120s
for _ in $(seq 1 24); do
  addresses="$(kubectl --context "$CONTEXT" -n kyverno get endpointslices -l kubernetes.io/service-name=kyverno-svc -o jsonpath='{range .items[*].endpoints[*]}{.conditions.ready}{" "}{.addresses[0]}{"\n"}{end}' 2> /dev/null | grep -E '^true ' || true)"
  [ -n "$addresses" ] && break
  sleep 5
done
[ -n "${addresses:-}" ] || { echo "kyverno-svc has no ready address after 2 minutes" >&2; exit 1; }
# The webhook that validates policies is served by the admission controller. Helm --wait returns when
# the deployment is ready, but on a cold start the webhook can still refuse connections for a while
# (seen once in CI). So the install is retried, with a limit.
applied=0
for attempt in $(seq 1 24); do
  if out="$(helm upgrade --install bulkhead-platform charts/bulkhead-platform \
      --kube-context "$CONTEXT" --values examples/kind/bulkhead-platform-values.yaml --wait --timeout 5m 2>&1)"; then
    echo "$out"
    applied=1
    break
  fi
  # Only a webhook that does not answer yet is worth waiting for. Any other error is final.
  if ! grep -qE 'connection refused|failed calling webhook|no endpoints available|context deadline exceeded' <<< "$out"; then
    echo "$out" >&2
    exit 1
  fi
  echo "platform install failed (attempt $attempt of 24), retrying in 5 s: $(head -c 160 <<< "$out")"
  sleep 5
done
[ "$applied" -eq 1 ] || { echo "the platform chart was not installed after 24 attempts" >&2; echo "$out" >&2; exit 1; }
for p in $(kubectl --context "$CONTEXT" get validatingpolicies.policies.kyverno.io -o name); do
  kubectl --context "$CONTEXT" wait --for=jsonpath='{.status.conditionStatus.ready}'=true "$p" --timeout=120s
done

exceptions=(exceptions/*.yaml)
if [ -e "${exceptions[0]}" ]; then
  echo "==> validating and applying ${#exceptions[@]} policy exception(s)"
  python3 scripts/validate_exceptions.py "${exceptions[@]}"
  kubectl --context "$CONTEXT" apply -f "${exceptions[@]}"
else
  echo "==> no policy exceptions in exceptions/"
fi

echo "kyverno ready: $((SECONDS - start)) s"
kubectl --context "$CONTEXT" -n kyverno get pods
