#!/usr/bin/env bash
# Print the changelog section of one version, without its heading, for the release notes.
# Usage: scripts/release-notes.sh VERSION   (without the leading v)
set -euo pipefail

cd "$(dirname "$0")/.."

version="${1:?usage: scripts/release-notes.sh VERSION}"
notes="$(awk -v v="$version" '
  index($0, "## [" v "]") == 1 { found = 1; next }
  found && /^## \[/ { exit }
  found { print }
' CHANGELOG.md)"
if [ -z "$(tr -d '[:space:]' <<< "$notes")" ]; then
  echo "CHANGELOG.md has no section for $version" >&2
  exit 1
fi
printf '%s\n' "$notes"
