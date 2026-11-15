#!/usr/bin/env bash
# Run the quick start block of the README exactly as it is written, line by line, and stop at the
# first command that fails. The git clone line is skipped, because this runs inside a clone.
# Usage: scripts/readme-quickstart.sh [--print]   (--print shows the commands and runs nothing)
set -euo pipefail

cd "$(dirname "$0")/.."

block="$(awk '
  /^## Quick start/ { section = 1; next }
  section && /^## / { exit }
  section && /^```bash$/ { inside = 1; next }
  inside && /^```$/ { exit }
  inside { print }
' README.md | grep -v '^git clone ')"

[ -n "$block" ] || { echo "no bash block under the Quick start heading in README.md" >&2; exit 1; }

if [ "${1:-}" = "--print" ]; then
  printf '%s\n' "$block"
  exit 0
fi

# One shell for the whole block, so that "source .venv/bin/activate" and "export PATH" carry over
# to the next lines, as they do for a person who types them. -x prints each command as it runs.
bash -euxo pipefail <<< "$block"
