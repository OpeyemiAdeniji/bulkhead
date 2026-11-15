#!/usr/bin/env bash
# Build the local kind cluster and install a network plugin that enforces NetworkPolicy.
# Usage: scripts/cluster-up.sh (or: make cluster-up)
#   K8S_VERSION  Kubernetes version of the nodes, one of examples/kind/node-images (default 1.37.0)
#   CNI          calico (default) or cilium
set -euo pipefail

CLUSTER_NAME="bulkhead"
K8S_VERSION="${K8S_VERSION:-1.37.0}"
CNI="${CNI:-calico}"
CALICO_VERSION="v3.33.0"
CALICO_BASE="https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests"

cd "$(dirname "$0")/.."
CILIUM_VERSION="$(tr -d '[:space:]' < examples/kind/cilium-version)"

for tool in docker kind kubectl; do
  command -v "$tool" > /dev/null || { echo "missing tool: $tool" >&2; exit 1; }
done
case "$CNI" in
  calico) ;;
  cilium) command -v helm > /dev/null || { echo "missing tool: helm (needed for Cilium)" >&2; exit 1; } ;;
  *) echo "unknown CNI: $CNI (choose calico or cilium)" >&2; exit 2 ;;
esac
# Node images are pinned by digest, as the kind release notes list them.
image="$(awk -v v="$K8S_VERSION" '$1 == v {print $2}' examples/kind/node-images)"
[ -n "$image" ] || { echo "no node image for Kubernetes $K8S_VERSION in examples/kind/node-images" >&2; exit 2; }
# The Calico manifests create MutatingAdmissionPolicy objects of admissionregistration.k8s.io/v1,
# which Kubernetes serves from 1.36. On 1.35 the install fails (seen in CI on 2026-11-15).
minor="$(cut -d. -f2 <<< "$K8S_VERSION")"
if [ "$CNI" = calico ] && [ "$minor" -lt 36 ]; then
  echo "Calico $CALICO_VERSION needs Kubernetes 1.36 or newer. For Kubernetes $K8S_VERSION use CNI=cilium." >&2
  exit 2
fi
docker info > /dev/null 2>&1 || { echo "Docker is not running" >&2; exit 1; }

if kind get clusters 2> /dev/null | grep -qx "$CLUSTER_NAME"; then
  echo "cluster $CLUSTER_NAME already exists. Run 'make cluster-down' first." >&2
  exit 1
fi

# Always talk to our own cluster, never to whatever context is current.
k() { kubectl --context "kind-${CLUSTER_NAME}" "$@"; }

start=$SECONDS

echo "==> creating kind cluster $CLUSTER_NAME (Kubernetes $K8S_VERSION, network plugin $CNI)"
kind create cluster --name "$CLUSTER_NAME" --config examples/kind/kind-config.yaml --image "$image"
after_create=$SECONDS

install_calico() {
  echo "==> installing Calico $CALICO_VERSION"
  # Calico publishes no checksums for these manifests, so the hashes in
  # examples/kind/calico-manifests.sha256 were recorded on first download (2026-11-14).
  # A changed file at the same URL stops the build here instead of being applied.
  calico_dir="$(mktemp -d)"
  trap 'rm -rf "$calico_dir"' EXIT
  for f in v3_projectcalico_org.yaml tigera-operator.yaml custom-resources.yaml; do
    curl -fsSL -o "$calico_dir/$f" "${CALICO_BASE}/$f"
  done
  (cd "$calico_dir" && sha256sum -c -) < examples/kind/calico-manifests.sha256
  k create -f "$calico_dir/v3_projectcalico_org.yaml"
  k create -f "$calico_dir/tigera-operator.yaml"
  k wait --for=condition=Established crd/installations.operator.tigera.io --timeout=120s
  k create -f "$calico_dir/custom-resources.yaml"
}

install_cilium() {
  echo "==> installing Cilium $CILIUM_VERSION"
  # kube-proxy stays, so Cilium only does pod networking and NetworkPolicy, like Calico here.
  # IP addresses come from the pod ranges that Kubernetes gives each node.
  helm repo add cilium https://helm.cilium.io/ --force-update > /dev/null
  helm upgrade --install cilium cilium/cilium --version "$CILIUM_VERSION" \
    --kube-context "kind-${CLUSTER_NAME}" --namespace kube-system \
    --set ipam.mode=kubernetes \
    --set image.pullPolicy=IfNotPresent \
    --set operator.replicas=1 \
    --wait --timeout 10m
}

"install_$CNI"

echo "==> waiting for nodes and DNS"
k wait --for=condition=Ready node --all --timeout=600s
k -n kube-system wait --for=condition=Ready pod -l k8s-app=kube-dns --timeout=300s

echo "cluster ready: Kubernetes $K8S_VERSION with $CNI, kind create took $((after_create - start))s, total $((SECONDS - start))s"
