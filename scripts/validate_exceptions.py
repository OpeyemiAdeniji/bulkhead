#!/usr/bin/env python3
"""Validate policy exception files before they reach the cluster.

Usage: python3 scripts/validate_exceptions.py [--now TIME] [--max-days N] FILE [FILE ...]

An exception lets one workload of one tenant skip a policy for a limited time
(docs/adr/0008-kyverno-cel-policies-and-expiring-exceptions.md). Each file must:

1. hold exactly one PolicyException of policies.kyverno.io/v1,
2. be named like its file and live in the namespace platform-exceptions,
3. carry the label cleanup.kyverno.io/ttl with an absolute date (YYYY-MM-DDTHHMMSSZ),
   which is in the future and at most --max-days ahead,
4. name its owner, its reason and its tenant in annotations, where the tenant has a
   file in tenants/,
5. refer only to policies that exist in platform/policies,
6. be narrow: at least one match condition, so it cannot exempt a whole policy.

--now fixes the current time (UTC, for example 2026-11-15T00:00:00Z), so that tests
give the same result on every day.

Exit code 0 means every file passed. Exit code 1 means at least one file failed.
No file at all is a pass.
"""

import argparse
import re
import sys
from datetime import datetime, timedelta, timezone
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent
NAMESPACE = "platform-exceptions"
TTL_LABEL = "cleanup.kyverno.io/ttl"
OWNER = "bulkhead.io/owner"
REASON = "bulkhead.io/reason"
TENANT = "bulkhead.io/tenant"
TTL_FORMAT = "%Y-%m-%dT%H%M%SZ"
TTL_PATTERN = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{6}Z$")
EMAIL_PATTERN = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")
MIN_REASON_LENGTH = 20


def known_policies():
    names = set()
    for path in sorted((ROOT / "platform" / "policies").glob("*.yaml")):
        for doc in yaml.safe_load_all(path.read_text()):
            if isinstance(doc, dict) and doc.get("kind") == "ValidatingPolicy":
                names.add(doc.get("metadata", {}).get("name"))
    return names


def known_tenants():
    return {path.stem for path in (ROOT / "tenants").glob("*.yaml")}


def check_ttl(value, now, max_days):
    if not isinstance(value, str) or not TTL_PATTERN.match(value):
        return [
            f"label {TTL_LABEL} must be an absolute date like 2026-12-01T000000Z, "
            f"not '{value}' (a duration restarts when the object is recreated)"
        ]
    try:
        expiry = datetime.strptime(value, TTL_FORMAT).replace(tzinfo=timezone.utc)
    except ValueError:
        return [f"label {TTL_LABEL}: '{value}' is not a real date"]
    if expiry <= now:
        return [f"label {TTL_LABEL}: the date {value} has passed, so the exception has expired"]
    if expiry > now + timedelta(days=max_days):
        return [
            f"label {TTL_LABEL}: the date {value} is more than {max_days} days ahead; "
            "renew a long exception instead of making it permanent"
        ]
    return []


def check_document(doc, path, now, max_days, policies, tenants):
    problems = []
    if doc.get("apiVersion") != "policies.kyverno.io/v1" or doc.get("kind") != "PolicyException":
        return [
            "must be a PolicyException of apiVersion policies.kyverno.io/v1, "
            f"not {doc.get('kind')} of {doc.get('apiVersion')}"
        ]

    meta = doc.get("metadata") or {}
    if meta.get("name") != path.stem:
        problems.append(f"metadata.name: '{meta.get('name')}' must match the file name '{path.stem}'")
    if meta.get("namespace") != NAMESPACE:
        problems.append(
            f"metadata.namespace: '{meta.get('namespace')}' must be {NAMESPACE}, "
            "the only namespace where exceptions are honoured"
        )

    labels = meta.get("labels") or {}
    if TTL_LABEL not in labels:
        problems.append(f"label {TTL_LABEL} is missing, so the exception would never expire")
    else:
        problems.extend(check_ttl(labels[TTL_LABEL], now, max_days))

    annotations = meta.get("annotations") or {}
    owner = annotations.get(OWNER)
    if not owner or not EMAIL_PATTERN.match(str(owner)):
        problems.append(f"annotation {OWNER} must be the contact address of the owning team")
    reason = str(annotations.get(REASON) or "").strip()
    if len(reason) < MIN_REASON_LENGTH:
        problems.append(f"annotation {REASON} must explain why, in at least {MIN_REASON_LENGTH} characters")
    tenant = annotations.get(TENANT)
    if not tenant:
        problems.append(f"annotation {TENANT} must name the tenant that the exception is for")
    elif tenant not in tenants:
        problems.append(f"annotation {TENANT}: '{tenant}' has no file in tenants/")

    spec = doc.get("spec") or {}
    refs = spec.get("policyRefs") or []
    if not refs:
        problems.append("spec.policyRefs must name at least one policy")
    for ref in refs:
        name = (ref or {}).get("name")
        if (ref or {}).get("kind") != "ValidatingPolicy" or name not in policies:
            problems.append(f"spec.policyRefs: '{name}' is not a ValidatingPolicy in platform/policies")
    if not spec.get("matchConditions"):
        problems.append("spec.matchConditions must narrow the exception to the workloads that need it")
    return problems


def check_file(path, now, max_days, policies, tenants):
    try:
        docs = [d for d in yaml.safe_load_all(path.read_text()) if d is not None]
    except OSError as exc:
        return [f"cannot read the file: {exc}"]
    except yaml.YAMLError as exc:
        return [f"not valid YAML: {exc}"]
    if len(docs) != 1 or not isinstance(docs[0], dict):
        return ["the file must contain exactly one YAML mapping"]
    return check_document(docs[0], path, now, max_days, policies, tenants)


def parse_now(value):
    if value is None:
        return datetime.now(timezone.utc)
    return datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)


def main(argv):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--now", help="current time in UTC, for example 2026-11-15T00:00:00Z")
    parser.add_argument("--max-days", type=int, default=90)
    parser.add_argument("files", nargs="*")
    args = parser.parse_args(argv[1:])

    now = parse_now(args.now)
    policies = known_policies()
    tenants = known_tenants()

    failed = 0
    for arg in args.files:
        path = Path(arg)
        problems = check_file(path, now, args.max_days, policies, tenants)
        if problems:
            failed += 1
            print(f"FAIL {path}")
            for problem in problems:
                print(f"  - {problem}")
        else:
            print(f"ok   {path}")
    if not args.files:
        print("no exception files")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
