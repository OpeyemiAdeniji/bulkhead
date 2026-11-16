#!/usr/bin/env bash
# Tests for the tenant chart.
# - Every tenant file must lint and render.
# - Every rendered object must carry the cost label, so showback never has gaps.
# - Every rendered object must match the Kubernetes API schema (kubeconform).
# - An unknown size must make the chart fail with a clear message.
# - The platform chart lints and renders, its objects match the API schemas, the kind values add
#   the kind admin group, and the values schemas of both charts refuse wrong values.
# Usage: scripts/chart-test.sh (or: make chart-test)
set -euo pipefail

cd "$(dirname "$0")/.."

for tool in helm kubeconform; do
  command -v "$tool" > /dev/null || { echo "missing tool: $tool (run scripts/install-tools.sh)" >&2; exit 1; }
done

# The Kubernetes version whose API schema the objects are checked against.
# Keep it equal to the default Kubernetes version in scripts/cluster-up.sh.
K8S_SCHEMA_VERSION="${K8S_SCHEMA_VERSION:-1.37.0}"

chart="charts/bulkhead-tenant"
failures=0

for file in tenants/*.yaml; do
  name="$(basename "$file" .yaml)"

  if ! helm lint "$chart" -f "$file" > /dev/null; then
    echo "FAIL  lint     $file"
    failures=$((failures + 1))
    continue
  fi
  if ! rendered="$(helm template "tenant-$name" "$chart" -f "$file")"; then
    echo "FAIL  render   $file"
    failures=$((failures + 1))
    continue
  fi

  objects="$(grep -c '^kind:' <<< "$rendered" || true)"
  labelled="$(grep -c '^    cost-centre:' <<< "$rendered" || true)"
  if [ "$objects" -gt 0 ] && [ "$objects" -eq "$labelled" ]; then
    echo "pass  render   $file ($objects objects, all with the cost label)"
  else
    echo "FAIL  labels   $file ($labelled of $objects objects have the cost label)"
    failures=$((failures + 1))
  fi

  # Check every rendered object against the Kubernetes API schema, without a cluster.
  # -strict also rejects fields that the schema does not know, such as a misspelt key.
  if out="$(kubeconform -strict -summary -kubernetes-version "$K8S_SCHEMA_VERSION" <<< "$rendered" 2>&1)"; then
    echo "pass  schema   $file ($(tail -1 <<< "$out"))"
  else
    echo "FAIL  schema   $file"
    echo "$out"
    failures=$((failures + 1))
  fi
done

if output="$(helm template tenant-alpha "$chart" -f tenants/alpha.yaml --set size=huge 2>&1)"; then
  echo "FAIL  unknown size (expected the chart to fail, but it rendered)"
  failures=$((failures + 1))
elif grep -qF "unknown size" <<< "$output"; then
  echo "pass  unknown size is rejected with a clear message"
else
  echo "FAIL  unknown size (it failed, but not with: unknown size)"
  failures=$((failures + 1))
fi

# expect_refused NAME TEXT COMMAND... : the command must fail, and its output must contain TEXT.
expect_refused() {
  local name="$1" text="$2" out
  shift 2
  if out="$("$@" 2>&1)"; then
    echo "FAIL  $name (it rendered, but it must be refused)"
    failures=$((failures + 1))
  elif grep -qF -- "$text" <<< "$out"; then
    echo "pass  $name"
  else
    echo "FAIL  $name (it failed, but not with: $text)"
    echo "$out" | head -5
    failures=$((failures + 1))
  fi
}

expect_refused "a Pod Security level that is not restricted or baseline is refused" "podSecurity/level" \
  helm template t "$chart" -f tenants/alpha.yaml --set podSecurity.level=privileged
# NodeLocal DNSCache pods are allowed by default (GKE answers DNS from them), and the entry can be removed.
out="$(helm template t "$chart" -f tenants/alpha.yaml 2>&1 || true)"
if grep -q 'k8s-app: node-local-dns' <<< "$out"; then
  echo "pass  DNS to NodeLocal DNSCache pods is allowed by default"
else
  echo "FAIL  DNS to NodeLocal DNSCache pods is not allowed by default"; failures=$((failures + 1))
fi
out="$(helm template t "$chart" -f tenants/alpha.yaml --set 'dns.extraPodLabels=null' 2>&1 || true)"
if ! grep -q 'node-local-dns' <<< "$out" && grep -q 'k8s-app: kube-dns' <<< "$out"; then
  echo "pass  dns.extraPodLabels can be emptied, kube-dns stays allowed"
else
  echo "FAIL  emptying dns.extraPodLabels did not work"; failures=$((failures + 1))
fi
expect_refused "a DNS pod label set that is empty is refused" "extraPodLabels" \
  helm template t "$chart" -f tenants/alpha.yaml --set-json 'dns.extraPodLabels=[{}]'
expect_refused "a DNS CIDR that is not a CIDR is refused" "extraCIDRs" \
  helm template t "$chart" -f tenants/alpha.yaml --set 'dns.extraCIDRs[0]=not-a-cidr'
if out="$(helm template t "$chart" -f tenants/alpha.yaml --set namespacePrefix=team- --set 'dns.extraCIDRs[0]=169.254.20.10/32' 2>&1)" &&
  grep -q '^  name: team-alpha$' <<< "$out" && grep -q 'cidr: "169.254.20.10/32"' <<< "$out"; then
  echo "pass  the namespace prefix and an extra DNS CIDR are applied"
else
  echo "FAIL  the namespace prefix or the extra DNS CIDR was not applied"
  failures=$((failures + 1))
fi

echo "== the platform chart"
platform="charts/bulkhead-platform"
# Kyverno's policy kinds are not in the Kubernetes schemas. The CRDs catalog has them.
crds='https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
if ! helm lint "$platform" > /dev/null; then
  echo "FAIL  lint     $platform"
  failures=$((failures + 1))
elif ! rendered="$(helm template bulkhead-platform "$platform")"; then
  echo "FAIL  render   $platform"
  failures=$((failures + 1))
elif out="$(kubeconform -strict -summary -kubernetes-version "$K8S_SCHEMA_VERSION" -schema-location default -schema-location "$crds" <<< "$rendered" 2>&1)"; then
  echo "pass  schema   $platform ($(tail -1 <<< "$out"))"
else
  echo "FAIL  schema   $platform"
  echo "$out"
  failures=$((failures + 1))
fi
kind_policy="$(helm template bulkhead-platform "$platform" -f examples/kind/bulkhead-platform-values.yaml -s templates/protect-platform-objects.yaml)"
default_policy="$(helm template bulkhead-platform "$platform" -s templates/protect-platform-objects.yaml)"
if grep -qF "'kubeadm:cluster-admins'" <<< "$kind_policy" && ! grep -qF "kubeadm:cluster-admins" <<< "$default_policy" &&
  grep -qF "'system:serviceaccounts:kube-system'" <<< "$default_policy" && grep -qF "'system:kube-controller-manager'" <<< "$default_policy"; then
  echo "pass  the kind values add the kind admin group, and the Kubernetes identities are always allowed"
else
  echo "FAIL  the platform admin list is not rendered as expected"
  failures=$((failures + 1))
fi
expect_refused "a platform chart without any platform admin is refused" "platformAdmins" \
  helm template p "$platform" --set 'platformAdmins.groups=null' --set 'platformAdmins.users=null'
expect_refused "a quote in a platform admin group is refused" "platformAdmins" \
  helm template p "$platform" --set "platformAdmins.groups[0]=a'b"
expect_refused "an unknown platform value is refused" "additional properties" \
  helm template p "$platform" --set unknownValue=1

if [ "$failures" -gt 0 ]; then
  echo "$failures chart test(s) failed"
  exit 1
fi
echo "all chart tests passed"
