#!/usr/bin/env bash
# Build the local kind cluster and install Calico.
# Usage: scripts/cluster-up.sh (or: make cluster-up)
set -euo pipefail

CLUSTER_NAME="tenant-platform"
CALICO_VERSION="v3.33.0"
CALICO_BASE="https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests"

cd "$(dirname "$0")/.."

for tool in docker kind kubectl; do
  command -v "$tool" > /dev/null || { echo "missing tool: $tool" >&2; exit 1; }
done
docker info > /dev/null 2>&1 || { echo "Docker is not running" >&2; exit 1; }

if kind get clusters 2> /dev/null | grep -qx "$CLUSTER_NAME"; then
  echo "cluster $CLUSTER_NAME already exists. Run 'make cluster-down' first." >&2
  exit 1
fi

# Always talk to our own cluster, never to whatever context is current.
k() { kubectl --context "kind-${CLUSTER_NAME}" "$@"; }

start=$SECONDS

echo "==> creating kind cluster $CLUSTER_NAME"
kind create cluster --name "$CLUSTER_NAME" --config platform/kind/kind-config.yaml
after_create=$SECONDS

echo "==> installing Calico $CALICO_VERSION"
# Calico publishes no checksums for these manifests, so the hashes in
# platform/calico/manifests.sha256 were recorded on first download (2026-11-14).
# A changed file at the same URL stops the build here instead of being applied.
calico_dir="$(mktemp -d)"
trap 'rm -rf "$calico_dir"' EXIT
for f in v3_projectcalico_org.yaml tigera-operator.yaml custom-resources.yaml; do
  curl -fsSL -o "$calico_dir/$f" "${CALICO_BASE}/$f"
done
(cd "$calico_dir" && sha256sum -c -) < platform/calico/manifests.sha256
k create -f "$calico_dir/v3_projectcalico_org.yaml"
k create -f "$calico_dir/tigera-operator.yaml"
k wait --for=condition=Established crd/installations.operator.tigera.io --timeout=120s
k create -f "$calico_dir/custom-resources.yaml"

echo "==> waiting for nodes and DNS"
k wait --for=condition=Ready node --all --timeout=600s
k -n kube-system wait --for=condition=Ready pod -l k8s-app=kube-dns --timeout=300s

echo "cluster ready: kind create took $((after_create - start))s, total $((SECONDS - start))s"
