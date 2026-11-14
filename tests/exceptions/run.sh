#!/usr/bin/env bash
# Tests for the policy exception check (scripts/validate_exceptions.py).
# - Every file in tests/exceptions/valid must pass.
# - Every file in tests/exceptions/invalid must fail, and its output must contain
#   the text given on its first line as "# expect: <text>".
# The time is fixed with --now, so that the dates in the files give the same result
# on every day.
set -euo pipefail

cd "$(dirname "$0")/../.."

NOW="2026-11-15T00:00:00Z"
check() { python3 scripts/validate_exceptions.py --now "$NOW" "$@"; }
failures=0

for file in tests/exceptions/valid/*.yaml; do
  if check "$file" > /dev/null; then
    echo "pass  valid    $file"
  else
    echo "FAIL  valid    $file (expected it to pass)"
    check "$file" || true
    failures=$((failures + 1))
  fi
done

for file in tests/exceptions/invalid/*.yaml; do
  expected="$(sed -n '1s/^# expect: //p' "$file")"
  if [ -z "$expected" ]; then
    echo "FAIL  invalid  $file (line 1 must be '# expect: <text>')"
    failures=$((failures + 1))
    continue
  fi
  if output="$(check "$file")"; then
    echo "FAIL  invalid  $file (expected it to fail, but it passed)"
    failures=$((failures + 1))
  elif grep -qF -- "$expected" <<< "$output"; then
    echo "pass  invalid  $file"
  else
    echo "FAIL  invalid  $file (it failed, but not with: $expected)"
    echo "$output"
    failures=$((failures + 1))
  fi
done

# The valid file must fail once its date has passed, so the date check uses --now.
if python3 scripts/validate_exceptions.py --now "2026-12-02T00:00:00Z" tests/exceptions/valid/*.yaml > /dev/null; then
  echo "FAIL  expiry   the valid file still passed after its date"
  failures=$((failures + 1))
else
  echo "pass  expiry   the valid file fails after its date"
fi

if [ "$failures" -gt 0 ]; then
  echo "$failures exception test(s) failed"
  exit 1
fi
echo "all exception tests passed"
