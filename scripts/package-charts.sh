#!/usr/bin/env bash
# Package both charts with one version, and check the packages.
# Usage: scripts/package-charts.sh VERSION OUT_DIR
#   VERSION is a Semantic Version without the leading v, for example 0.3.0 or 0.3.0-rc.1.
# The release workflow uses it for a tag, and CI runs it on every pull request with a test version.
set -euo pipefail

cd "$(dirname "$0")/.."

version="${1:?usage: scripts/package-charts.sh VERSION OUT_DIR}"
out="${2:?usage: scripts/package-charts.sh VERSION OUT_DIR}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || { echo "not a Semantic Version: $version" >&2; exit 1; }
command -v helm > /dev/null || { echo "missing tool: helm" >&2; exit 1; }

mkdir -p "$out"
for chart in charts/bulkhead-tenant charts/bulkhead-platform; do
  name="$(basename "$chart")"
  values=()
  # The tenant chart needs a tenant file to render.
  [ "$name" = bulkhead-tenant ] && values=(-f tenants/alpha.yaml)
  helm lint "$chart" --quiet "${values[@]}" > /dev/null
  helm package "$chart" --version "$version" --app-version "$version" --destination "$out" > /dev/null
  pkg="$out/$name-$version.tgz"
  [ -f "$pkg" ] || { echo "package not found: $pkg" >&2; exit 1; }
  # The package must not contain the offline tests, and must render on its own.
  if tar -tzf "$pkg" | grep -q "^$name/tests/"; then
    echo "$pkg contains tests/" >&2
    exit 1
  fi
  helm template "$name" "$pkg" "${values[@]}" > /dev/null
  echo "packaged $pkg ($(tar -tzf "$pkg" | wc -l) files)"
done
