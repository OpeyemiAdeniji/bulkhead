#!/usr/bin/env bash
# Live tests of the Kyverno policies on the local cluster.
#
# Needs a running cluster with the tenants and Kyverno: make cluster-up, make tenants-apply,
# make kyverno-up. Real requests are made as a tenant admin (the group alpha-admins), through
# impersonation, and as the platform admin.
#
# Every denial check also tests the text of the denial, so a quota error or a Pod Security
# error cannot make it pass by accident.
#
# WITH_EXPIRY=1 also tests that an exception expires (adds about 3 minutes).
# Usage: tests/policy-live/run.sh (or: make test-policy-live)
set -uo pipefail

cd "$(dirname "$0")/../.." || exit 1

CONTEXT="${KUBE_CONTEXT:-kind-bulkhead}"
A="tenant-alpha"
POLICY="disallow-latest-image-tag"
RUNBOOK="docs/runbooks/policy-disallow-latest-tag.md"
EXPECTED=31
if [ "${WITH_EXPIRY:-0}" = "1" ]; then
  EXPECTED=33
fi
passes=0
failures=0
work="$(mktemp -d)"

k() { kubectl --context "$CONTEXT" "$@"; }
as_alpha() { k --as=tester --as-group=alpha-admins "$@"; }

pass() { echo "pass  $1"; passes=$((passes + 1)); }
fail() { echo "FAIL  $1"; failures=$((failures + 1)); }

# expect_ok NAME COMMAND... : the command must succeed.
expect_ok() {
  local name="$1" out
  shift
  if out="$("$@" 2>&1)"; then
    pass "$name"
  else
    fail "$name (the command failed, but it must succeed)"
    echo "      output: $(head -c 400 <<< "$out")"
  fi
}

# expect_blocked NAME TEXT COMMAND... : the command must fail, and its output must contain TEXT.
expect_blocked() {
  local name="$1" text="$2" out
  shift 2
  if out="$("$@" 2>&1)"; then
    fail "$name (it succeeded, but it must be blocked)"
  elif grep -qiF -- "$text" <<< "$out"; then
    pass "$name"
  else
    fail "$name (it failed, but not with: $text)"
    echo "      output: $(head -c 300 <<< "$out")"
  fi
}

# denial_missing OUTPUT NEEDLE... : prints the needles that OUTPUT does not contain.
denial_missing() {
  local out="$1" missing="" needle
  shift
  for needle in "$@"; do
    grep -qF -- "$needle" <<< "$out" || missing="$missing [$needle]"
  done
  echo "$missing"
}

# Workloads follow the restricted Pod Security level, so the tenant namespaces accept them.
RESOURCES='{requests: {cpu: 50m, memory: 32Mi}, limits: {cpu: 50m, memory: 32Mi}}'
SECURITY='{runAsNonRoot: true, runAsUser: 1000, allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}, seccompProfile: {type: RuntimeDefault}}'

pod_manifest() { # NAME IMAGE NAMESPACE
cat <<MANIFEST
apiVersion: v1
kind: Pod
metadata:
  name: $1
  namespace: $3
  labels:
    suite: policy-live
spec:
  terminationGracePeriodSeconds: 0
  containers:
    - name: main
      image: $2
      command: ["sleep", "3600"]
      resources: $RESOURCES
      securityContext: $SECURITY
MANIFEST
}

deployment_manifest() { # NAME IMAGE NAMESPACE
cat <<MANIFEST
apiVersion: apps/v1
kind: Deployment
metadata:
  name: $1
  namespace: $3
  labels:
    suite: policy-live
spec:
  replicas: 1
  selector:
    matchLabels:
      app: $1
  template:
    metadata:
      labels:
        app: $1
        suite: policy-live
    spec:
      terminationGracePeriodSeconds: 0
      containers:
        - name: main
          image: $2
          command: ["sleep", "3600"]
          resources: $RESOURCES
          securityContext: $SECURITY
MANIFEST
}

cronjob_manifest() { # NAME IMAGE NAMESPACE
cat <<MANIFEST
apiVersion: batch/v1
kind: CronJob
metadata:
  name: $1
  namespace: $3
  labels:
    suite: policy-live
spec:
  schedule: "0 0 1 1 *"
  suspend: true
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: Never
          terminationGracePeriodSeconds: 0
          containers:
            - name: main
              image: $2
              command: ["sleep", "3600"]
              resources: $RESOURCES
              securityContext: $SECURITY
MANIFEST
}

create_cronjob_as_alpha() { cronjob_manifest "$1" "$2" "$3" | as_alpha create -f -; }

create_pod_as_alpha() { pod_manifest "$1" "$2" "$3" | as_alpha create -f -; }
create_pod_as_admin() { pod_manifest "$1" "$2" "$3" | k create -f -; }
create_deployment_as_alpha() { deployment_manifest "$1" "$2" "$3" | as_alpha create -f -; }
patch_image_as_alpha() { # KIND NAME JSON_PATH
  as_alpha -n "$A" patch "$1" "$2" --type=json -p "[{\"op\":\"replace\",\"path\":\"$3\",\"value\":\"busybox:latest\"}]"
}

exception_manifest() { # NAME NAMESPACE TTL POD
cat <<MANIFEST
apiVersion: policies.kyverno.io/v1
kind: PolicyException
metadata:
  name: $1
  namespace: $2
  labels:
    cleanup.kyverno.io/ttl: "$3"
spec:
  policyRefs:
    - name: $POLICY
      kind: ValidatingPolicy
  matchConditions:
    - name: only-the-test-pod
      expression: "object.metadata.name == '$4'"
MANIFEST
}

cleanup() {
  k -n "$A" delete deployments -l suite=policy-live --ignore-not-found --wait=false > /dev/null 2>&1
  k -n "$A" delete cronjobs -l suite=policy-live --ignore-not-found --wait=false > /dev/null 2>&1
  k -n "$A" delete pods -l suite=policy-live --ignore-not-found --wait=false > /dev/null 2>&1
  k -n default delete pods -l suite=policy-live --ignore-not-found --wait=false > /dev/null 2>&1
  k delete namespaces -l suite=policy-live --ignore-not-found --wait=false > /dev/null 2>&1
  k -n platform-exceptions delete policyexceptions.policies.kyverno.io live-exception --ignore-not-found > /dev/null 2>&1
  k -n "$A" delete rolebindings,networkpolicies -l suite=policy-live --ignore-not-found > /dev/null 2>&1
  rm -rf "$work"
}
trap cleanup EXIT

if ! k get validatingpolicies.policies.kyverno.io "$POLICY" > /dev/null 2>&1; then
  echo "policy $POLICY not found on context $CONTEXT. Run: make kyverno-up" >&2
  exit 2
fi
if ! k get namespace "$A" > /dev/null 2>&1; then
  echo "tenants not found on context $CONTEXT. Run: make tenants-apply" >&2
  exit 2
fi
cleanup
mkdir -p "$work"

echo "== the policy is installed"
expect_ok "the policy $POLICY exists" k get validatingpolicies.policies.kyverno.io "$POLICY"
ready="$(k get validatingpolicies.policies.kyverno.io "$POLICY" -o jsonpath='{.status.conditionStatus.ready}' 2> /dev/null)"
if [ "$ready" = "true" ]; then
  pass "the policy reports ready"
else
  fail "the policy reports ready='$ready', expected true"
fi

echo "== a tenant admin creates pods and deployments (real requests)"
if out="$(create_pod_as_alpha live-latest busybox:latest "$A" 2>&1)"; then
  fail "a pod with busybox:latest is denied in a tenant namespace (it was created)"
else
  missing="$(denial_missing "$out" "denied the request" "Policy $POLICY failed" "busybox:latest" "Fix:" "Runbook:" "$RUNBOOK")"
  if [ -z "$missing" ]; then
    pass "a pod with busybox:latest is denied, with the image, the fix and the runbook link"
  else
    fail "the pod denial is missing:$missing"
    echo "      output: $(head -c 400 <<< "$out")"
  fi
fi
expect_ok "a pod with busybox:1.36 is created in a tenant namespace" create_pod_as_alpha live-pinned busybox:1.36 "$A"
expect_ok "a pod with busybox:latest is created in the default namespace (not a tenant namespace)" create_pod_as_admin live-outside busybox:latest default
if out="$(create_deployment_as_alpha live-deploy-latest busybox:latest "$A" 2>&1)"; then
  fail "a deployment with busybox:latest is denied in a tenant namespace (it was created)"
else
  missing="$(denial_missing "$out" "denied the request" "busybox:latest" "Fix:" "Runbook:" "$RUNBOOK")"
  if [ -z "$missing" ]; then
    pass "a deployment with busybox:latest is denied at its own creation, with the fix and the runbook link"
  else
    fail "the deployment denial is missing:$missing"
    echo "      output: $(head -c 400 <<< "$out")"
  fi
fi
expect_ok "a deployment with busybox:1.36 is created in a tenant namespace" create_deployment_as_alpha live-deploy-pinned busybox:1.36 "$A"

echo "== a tenant admin changes the image of a workload that already exists (real requests)"
pod_manifest live-update-pod busybox:1.36 "$A" | as_alpha create -f - > /dev/null 2>&1
deployment_manifest live-update-deploy busybox:1.36 "$A" | as_alpha create -f - > /dev/null 2>&1
sleep 3
expect_blocked "changing the image of a running pod to latest is denied" "Policy $POLICY failed" patch_image_as_alpha pod live-update-pod /spec/containers/0/image
expect_blocked "changing the image of a deployment to latest is denied at the deployment itself" "Policy $POLICY failed" patch_image_as_alpha deployment live-update-deploy /spec/template/spec/containers/0/image

# Ephemeral containers are added through their own subresource (kubectl debug). Tenant admins
# may not add them, so the platform admin tries: the policy holds for every identity.
k -n "$A" wait --for=condition=Ready pod/live-update-pod --timeout=120s > /dev/null 2>&1 || true
add_debug_container() { # NAME IMAGE
  k -n "$A" debug pod/live-update-pod --image="$2" --profile=restricted -c "$1" -- sleep 1
}
if out="$(add_debug_container live-debug-latest busybox:latest 2>&1)"; then
  fail "an ephemeral container with busybox:latest is denied (it was added)"
else
  missing="$(denial_missing "$out" "denied the request" "Policy $POLICY-ephemeral failed" "busybox:latest" "Fix:" "Runbook:" "$RUNBOOK")"
  if [ -z "$missing" ]; then
    pass "an ephemeral container with busybox:latest is denied, with the fix and the runbook link"
  else
    fail "the ephemeral container denial is missing:$missing"
    echo "      output: $(head -c 400 <<< "$out")"
  fi
fi
expect_ok "an ephemeral container with busybox:1.36 is added" add_debug_container live-debug-pinned busybox:1.36

echo "== a tenant admin cannot change policies or exceptions (real attempts, server-side dry run)"
helm template bulkhead-platform charts/bulkhead-platform -s "templates/$POLICY.yaml" > "$work/policy.yaml"
far="$(date -u -d '+1 hour' +%Y-%m-%dT%H%M%SZ)"
exception_manifest probe-exception platform-exceptions "$far" nothing > "$work/exception-platform.yaml"
exception_manifest probe-exception "$A" "$far" nothing > "$work/exception-tenant.yaml"
expect_blocked "a tenant admin cannot create an exception in platform-exceptions" "forbidden" as_alpha create -f "$work/exception-platform.yaml" --dry-run=server
expect_blocked "a tenant admin cannot create an exception in its own namespace" "forbidden" as_alpha create -f "$work/exception-tenant.yaml" --dry-run=server
expect_blocked "a tenant admin cannot create a policy" "forbidden" as_alpha create -f "$work/policy.yaml" --dry-run=server
expect_blocked "a tenant admin cannot delete the policy" "forbidden" as_alpha delete validatingpolicies.policies.kyverno.io "$POLICY" --dry-run=server
expect_ok "the platform admin can create the same exception (dry run), so the refusals above are about the identity" k create -f "$work/exception-platform.yaml" --dry-run=server
# The example that passes make test-exceptions must also be accepted by the cluster, so the
# offline check and the real PolicyException schema agree. Its date is moved into the future.
sed "s/cleanup.kyverno.io\/ttl: .*/cleanup.kyverno.io\/ttl: \"$far\"/" tests/exceptions/valid/vendor-agent-latest.yaml > "$work/example-exception.yaml"
expect_ok "the example exception that passes the offline check is accepted by the cluster (dry run)" k create -f "$work/example-exception.yaml" --dry-run=server

echo "== platform objects cannot be changed, even with too many rights (real attempts)"
# A role binding to the built-in admin role is a mistake that the tenant file cannot make, but a
# person can. That role may delete network policies and change roles (ADR 0007), so it shows that
# the policy, not the tenant-admin role, protects the platform objects. Changes to the tenant-admin
# role are not tried: Kubernetes refuses them already, because that role holds rights that the admin
# role does not (RBAC escalation prevention).
PROTECT="protect-platform-objects"
k -n "$A" create rolebinding policy-live-broken-admin --clusterrole=admin --group=policy-live-broken-admins > /dev/null
k -n "$A" label rolebinding policy-live-broken-admin suite=policy-live > /dev/null
as_broken() { k --as=tester --as-group=policy-live-broken-admins "$@"; }
for _ in $(seq 1 20); do
  as_broken auth can-i delete networkpolicies -n "$A" > /dev/null 2>&1 && break
  sleep 1
done
expect_blocked "with the admin role, the default-deny policy cannot be deleted" "Policy $PROTECT failed" as_broken -n "$A" delete networkpolicy default-deny-all --dry-run=server
expect_blocked "with the admin role, the platform label cannot be removed from it" "Policy $PROTECT failed" as_broken -n "$A" label networkpolicy default-deny-all app.kubernetes.io/managed-by- --dry-run=server
expect_blocked "with the admin role, the tenant-admins role binding cannot be deleted" "Policy $PROTECT failed" as_broken -n "$A" delete rolebinding tenant-admins --dry-run=server
expect_blocked "with the admin role, the default-deny policy cannot be loosened to allow all ingress" "Policy $PROTECT failed" as_broken -n "$A" patch networkpolicy default-deny-all --type=json -p '[{"op":"add","path":"/spec/ingress","value":[{}]}]' --dry-run=server
own_policy() {
  printf '%s\n' "apiVersion: networking.k8s.io/v1" "kind: NetworkPolicy" "metadata: {name: policy-live-own, labels: {suite: policy-live}}" "spec: {podSelector: {}, policyTypes: [Ingress]}" \
    | as_broken -n "$A" create -f - && as_broken -n "$A" delete networkpolicy policy-live-own
}
expect_ok "with the admin role, a network policy without the platform label can be created and deleted" own_policy
expect_ok "the platform admin can delete the default-deny policy (dry run), so the refusals above are about the identity" k -n "$A" delete networkpolicy default-deny-all --dry-run=server
k -n "$A" delete rolebinding policy-live-broken-admin --ignore-not-found > /dev/null
# Removing a tenant must still work: the namespace controller deletes the platform objects.
O="policy-live-offboard-$$"
offboard() {
  k create namespace "$O" > /dev/null &&
    k label namespace "$O" app.kubernetes.io/managed-by=bulkhead suite=policy-live > /dev/null &&
    printf '%s\n' "apiVersion: networking.k8s.io/v1" "kind: NetworkPolicy" "metadata: {name: default-deny-all, labels: {app.kubernetes.io/managed-by: bulkhead}}" "spec: {podSelector: {}, policyTypes: [Ingress, Egress]}" \
      | k -n "$O" create -f - > /dev/null &&
    k delete namespace "$O" --wait=true --timeout=120s > /dev/null
}
expect_ok "a namespace with platform objects is removed completely (the namespace controller may delete them)" offboard

echo "== a workload that already runs latest is not blocked by unrelated updates"
G="policy-live-old-$$"
k create namespace "$G" > /dev/null 2>&1
k label namespace "$G" suite=policy-live > /dev/null 2>&1
pod_manifest live-old-pod busybox:latest "$G" | k create -f - > /dev/null 2>&1
deployment_manifest live-old-deploy busybox:latest "$G" | k create -f - > /dev/null 2>&1
cronjob_manifest live-old-cron busybox:latest "$G" | k create -f - > /dev/null 2>&1
if ! k -n "$G" get pod live-old-pod > /dev/null 2>&1 || ! k -n "$G" get deployment live-old-deploy > /dev/null 2>&1 || ! k -n "$G" get cronjob live-old-cron > /dev/null 2>&1; then
  echo "setup failed: could not create the latest workloads in the scratch namespace $G" >&2
  exit 2
fi
k label namespace "$G" app.kubernetes.io/managed-by=bulkhead > /dev/null 2>&1
sleep 3
expect_ok "an unrelated change (a label) to a running pod with busybox:latest is allowed" k -n "$G" label pod live-old-pod touched=yes --overwrite
expect_blocked "swapping the image of that pod for another latest image is denied" "Policy $POLICY failed" k -n "$G" patch pod live-old-pod --type=json -p '[{"op":"replace","path":"/spec/containers/0/image","value":"alpine:latest"}]'
expect_ok "an unrelated change (a label) to a deployment with busybox:latest is allowed" k -n "$G" label deployment live-old-deploy touched=yes --overwrite
k -n "$G" scale deployment live-old-deploy --replicas=2 > /dev/null 2>&1
rs_msg=""
for _ in $(seq 1 15); do
  rs_msg="$(k -n "$G" get rs -l app=live-old-deploy -o jsonpath='{.items[*].status.conditions[?(@.type=="ReplicaFailure")].message}' 2> /dev/null)"
  [ -n "$rs_msg" ] && break
  sleep 2
done
if grep -qF -- "Policy $POLICY failed" <<< "$rs_msg"; then
  pass "scaling that deployment up is accepted, but its new latest pod is still denied at the pod"
else
  fail "scaling up created no denied pod (ReplicaFailure message: '${rs_msg:-none}')"
fi
expect_blocked "a cronjob with busybox:latest is denied at its own creation" "Policy $POLICY failed" create_cronjob_as_alpha live-cron-latest busybox:latest "$A"
expect_ok "an unrelated change (a label) to a cronjob with busybox:latest is allowed" k -n "$G" label cronjob live-old-cron touched=yes --overwrite
expect_blocked "swapping the image of that cronjob for another latest image is denied" "Policy $POLICY failed" k -n "$G" patch cronjob live-old-cron --type=json -p '[{"op":"replace","path":"/spec/jobTemplate/spec/template/spec/containers/0/image","value":"alpine:latest"}]'

if [ "${WITH_EXPIRY:-0}" = "1" ]; then
  echo "== an exception expires (about 3 minutes)"
  expiry_epoch="$(date -u -d '+2 minutes' +%s)"
  label="$(date -u -d "@$expiry_epoch" +%Y-%m-%dT%H%M%SZ)"
  exception_manifest live-exception platform-exceptions "$label" exception-live > "$work/live-exception.yaml"
  expect_ok "the exception exists in platform-exceptions" k create -f "$work/live-exception.yaml"
  sleep 10
  expect_ok "while the exception exists, a pod with busybox:latest is admitted" create_pod_as_alpha exception-live busybox:latest "$A"
  k -n "$A" delete pod exception-live --ignore-not-found --wait=true --timeout=60s > /dev/null 2>&1
  while [ "$(date -u +%s)" -lt $((expiry_epoch + 15)) ]; do sleep 5; done
  expect_blocked "after its date, the same pod is denied again" "Policy $POLICY failed" create_pod_as_alpha exception-live busybox:latest "$A"
  if k -n platform-exceptions get policyexceptions.policies.kyverno.io live-exception > /dev/null 2>&1; then
    fail "the exception was not deleted after its date"
  else
    pass "the exception was deleted by Kyverno after its date"
  fi
fi

echo
if [ "$passes" -ne "$EXPECTED" ] && [ "$failures" -eq 0 ]; then
  echo "$passes checks passed, but $EXPECTED were expected, so a check did not run"
  exit 1
fi
if [ "$failures" -gt 0 ]; then
  echo "$failures live policy check(s) failed, $passes passed"
  exit 1
fi
echo "all $passes live policy checks passed"
echo "Limits: one tenant identity (alpha-admins) and two policies. The behaviour while Kyverno is"
echo "down, and StatefulSets, DaemonSets and Jobs, are not tested here."
