<!-- Generated from internal/catalog by `make checks-doc`. Do not edit by hand. -->

# The checks of bulkhead check

The catalogue has 41 checks. Every check that must be blocked is paired with a check that must work, and its result counts only when that pair passed, so a broken test cannot pass by accident ([ADR 0012](adr/0012-active-tenant-isolation-checker.md)).

- **Level:** basic checks run by default; hardened checks are selected on request, because some platforms allow what they block on purpose; research checks are experimental.
- **Mode:** read-only checks only read, ask for access reviews or send server-side dry runs, so they are safe on production clusters (`--read-only`). Active checks create short-lived, labelled test objects and remove them.
- **Tenant A and tenant B** stand for every ordered pair of the tenants under test.

The same list is printed by `bulkhead check --list`, and as JSON by `bulkhead check --list -o json`.

| ID | Level | Mode | Expect | Pair | Title |
|---|---|---|---|---|---|
| [ACC-01](#acc-01) | basic | read-only | allowed | - | A tenant admin can read and list secrets in its own namespace |
| [ACC-02](#acc-02) | basic | read-only | blocked | [ACC-01](#acc-01) | A tenant admin cannot read secrets in another tenant's namespace |
| [ACC-03](#acc-03) | basic | read-only | blocked | [ACC-01](#acc-01) | A tenant admin cannot list secrets in another tenant's namespace |
| [ACC-04](#acc-04) | basic | read-only | blocked | [ACC-05](#acc-05) | A tenant viewer cannot read secrets in its own namespace |
| [ACC-05](#acc-05) | basic | read-only | allowed | - | A tenant viewer can list pods in its own namespace |
| [GRD-01](#grd-01) | basic | read-only | allowed | - | A tenant admin can create a deployment in its own namespace |
| [GRD-02](#grd-02) | basic | read-only | blocked | [GRD-01](#grd-01) | A tenant admin cannot delete the network policies of its namespace |
| [GRD-03](#grd-03) | basic | read-only | blocked | [GRD-01](#grd-01) | A tenant admin cannot delete the resource quota of its namespace |
| [GRD-04](#grd-04) | basic | read-only | blocked | [GRD-01](#grd-01) | A tenant admin cannot delete the limit range of its namespace |
| [GRD-05](#grd-05) | basic | read-only | blocked | [GRD-01](#grd-01) | A tenant admin cannot create role bindings |
| [GRD-06](#grd-06) | basic | read-only | blocked | [GRD-01](#grd-01) | A tenant admin cannot change the Pod Security level of its namespace |
| [GRD-07](#grd-07) | basic | read-only | blocked | [GRD-08](#grd-08) | A tenant admin cannot create endpoints |
| [GRD-08](#grd-08) | basic | read-only | allowed | - | A tenant admin can read the endpoints of its namespace |
| [GRD-09](#grd-09) | basic | read-only | allowed | - | A tenant admin can open a shell in its own pods |
| [GRD-10](#grd-10) | basic | read-only | blocked | [GRD-09](#grd-09) | A tenant admin cannot open a shell in another tenant's pods |
| [ADM-01](#adm-01) | basic | read-only | allowed | - | A pod that meets the namespace's Pod Security level is accepted |
| [ADM-02](#adm-02) | basic | read-only | blocked | [ADM-01](#adm-01) | A privileged pod is refused |
| [NET-01](#net-01) | basic | active | allowed | - | Pods of the same tenant reach each other on the same node |
| [NET-02](#net-02) | basic | active | allowed | - | Pods of the same tenant reach each other across nodes |
| [NET-03](#net-03) | basic | active | allowed | - | Tenant pods can resolve names through the cluster DNS |
| [NET-04](#net-04) | basic | active | blocked | [NET-01](#net-01) | A tenant cannot reach another tenant's pods on the same node |
| [NET-05](#net-05) | basic | active | blocked | [NET-02](#net-02) | A tenant cannot reach another tenant's pods on another node |
| [NET-06](#net-06) | hardened | active | blocked | [NET-01](#net-01) | A tenant cannot open connections to the internet |
| [FLW-01](#flw-01) | basic | active | allowed | - | A flow the team declared as allowed connects |
| [FLW-02](#flw-02) | basic | active | blocked | [FLW-01](#flw-01) | A flow the team declared as blocked does not connect |
| [RES-01](#res-01) | basic | active | blocked | [RES-04](#res-04) | A CPU hog cannot use more than its CPU limit |
| [RES-02](#res-02) | basic | active | blocked | [RES-04](#res-04) | The kernel throttles a CPU hog |
| [RES-03](#res-03) | basic | active | blocked | [RES-05](#res-05) | A memory hog is killed at its memory limit |
| [RES-04](#res-04) | basic | active | allowed | - | Another tenant's server on the same node keeps answering |
| [RES-05](#res-05) | basic | active | allowed | - | Another tenant's server on the same node does not restart |
| [QTA-01](#qta-01) | basic | active | allowed | - | A pod within the quota is accepted |
| [QTA-02](#qta-02) | basic | active | blocked | [QTA-01](#qta-01) | A pod that would exceed the quota is refused |
| [QTA-03](#qta-03) | basic | read-only | allowed | - | A ClusterIP service is accepted |
| [QTA-04](#qta-04) | basic | read-only | blocked | [QTA-03](#qta-03) | A NodePort service is refused |
| [QTA-05](#qta-05) | basic | read-only | blocked | [QTA-03](#qta-03) | A LoadBalancer service is refused |
| [QTA-06](#qta-06) | basic | read-only | blocked | [QTA-07](#qta-07) | A container above the per-container maximum is refused |
| [QTA-07](#qta-07) | basic | read-only | allowed | - | A pod without resources receives the default limits |
| [QTA-08](#qta-08) | basic | active | allowed | - | As many pods as the quota allows can be created |
| [QTA-09](#qta-09) | basic | active | blocked | [QTA-08](#qta-08) | One pod more than the quota's pod count is refused |
| [XNS-01](#xns-01) | research | read-only | allowed | - | A custom resource that references the tenant's own namespace is accepted |
| [XNS-02](#xns-02) | research | read-only | blocked | [XNS-01](#xns-01) | A custom resource that references another tenant's namespace is refused |

## Access between tenants

### ACC-01

**A tenant admin can read and list secrets in its own namespace**

Level basic, read-only, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** The tenant admin identity used by the checker is real and has the access a tenant admin needs, so the refusals of ACC-02 and ACC-03 are about the other namespace and not about a broken identity.
- **Does not prove:** That the secrets themselves are safe: anyone with this access reads them.
- **How:** As the tenant admin, list secrets in tenant A and get one by name. In read-only mode, a request for a name that does not exist must return "not found", not "forbidden".
- **Fix when it fails:** Bind the tenant admin group to a role that can read secrets in its namespace, or correct the identity given to the checker.

### ACC-02

**A tenant admin cannot read secrets in another tenant's namespace**

Level basic, read-only, expected result: blocked. Pair: [ACC-01](#acc-01).

- **Proves:** Access rules separate the tenants: the admin of tenant A gets "forbidden" for a secret in tenant B.
- **Does not prove:** That no other identity in tenant A (a service account, a controller acting for it) can read it. Only the identities given to the checker are tested.
- **How:** As the admin of tenant A, get a secret in tenant B. The answer must be "forbidden".
- **Fix when it fails:** Remove role bindings in tenant B that include tenant A's groups, and cluster role bindings that grant secrets in every namespace.
- **Benchmarks** ([mapping](benchmarks.md)): Multi-tenancy benchmark MTB-PL1-CC-TI-2 Block access to other tenant resources (partly); CIS 5.1.2 Minimize access to secrets (partly); CIS 5.6.1 Create administrative boundaries between resources using namespaces (partly); NSA/CISA Namespaces, p. 14 Partition resources among teams with namespaces (partly); NSA/CISA Secrets, p. 20 Restrict access to Secrets with RBAC (partly); NSA/CISA Role-based access control, p. 26 Give each group only the access it needs, and no access to the resources of other groups (partly)

### ACC-03

**A tenant admin cannot list secrets in another tenant's namespace**

Level basic, read-only, expected result: blocked. Pair: [ACC-01](#acc-01).

- **Proves:** Listing, which returns every secret at once, is refused across tenants as well as reading one by name.
- **Does not prove:** That watch requests are refused. They are covered by the same access rule in Kubernetes, but are not sent.
- **How:** As the admin of tenant A, list secrets in tenant B. The answer must be "forbidden".
- **Fix when it fails:** As for ACC-02.
- **Benchmarks** ([mapping](benchmarks.md)): Multi-tenancy benchmark MTB-PL1-CC-TI-2 Block access to other tenant resources (partly); CIS 5.1.2 Minimize access to secrets (partly); CIS 5.6.1 Create administrative boundaries between resources using namespaces (partly); NSA/CISA Namespaces, p. 14 Partition resources among teams with namespaces (partly); NSA/CISA Secrets, p. 20 Restrict access to Secrets with RBAC (partly); NSA/CISA Role-based access control, p. 26 Give each group only the access it needs, and no access to the resources of other groups (partly)

### ACC-04

**A tenant viewer cannot read secrets in its own namespace**

Level basic, read-only, expected result: blocked. Pair: [ACC-05](#acc-05).

- **Proves:** The read-only role of a tenant does not include secrets, which hold credentials.
- **Does not prove:** That viewers cannot see secret values in other places, for example environment variables in pod specs.
- **How:** As a tenant viewer, get a secret in its own namespace. The answer must be "forbidden".
- **Fix when it fails:** Bind viewers to a role without secrets. The built-in "view" role already excludes them.
- **Benchmarks** ([mapping](benchmarks.md)): CIS 5.1.2 Minimize access to secrets (partly); NSA/CISA Secrets, p. 20 Restrict access to Secrets with RBAC (partly)

### ACC-05

**A tenant viewer can list pods in its own namespace**

Level basic, read-only, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** The viewer identity is real and can read, so the refusal of ACC-04 is about secrets.
- **Does not prove:** Anything about other namespaces.
- **How:** As a tenant viewer, list pods in its own namespace.
- **Fix when it fails:** Bind the viewer group to a read-only role in the namespace, or correct the identity given to the checker.

## Guardrails the tenant cannot change

### GRD-01

**A tenant admin can create a deployment in its own namespace**

Level basic, read-only, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** The tenant admin can do its normal work, so the refusals of the guardrail checks are about the guardrails and not about an identity without rights.
- **Does not prove:** That the deployment would run: admission is tested, scheduling is not.
- **How:** As the tenant admin, create a small deployment in tenant A as a server-side dry run. Nothing is stored.
- **Fix when it fails:** Give the tenant admin a role that can manage workloads in its namespace.

### GRD-02

**A tenant admin cannot delete the network policies of its namespace**

Level basic, read-only, expected result: blocked. Pair: [GRD-01](#grd-01).

- **Proves:** The tenant cannot remove its own network isolation. The checker reports whether access rules or an admission policy refused it.
- **Does not prove:** That the tenant cannot add a network policy that opens traffic. Creating policies may be allowed on purpose.
- **How:** As the tenant admin, delete each platform network policy of tenant A as a server-side dry run.
- **Fix when it fails:** Do not give tenant admins delete rights on network policies, or protect platform objects with an admission policy (Bulkhead's protect-platform-objects).
- **Benchmarks** ([mapping](benchmarks.md)): Multi-tenancy benchmark MTB-PL1-BC-CPI-2 Block Multitenant Resources (partly); CIS 5.3.2 Ensure that all Namespaces have NetworkPolicies defined (partly); NSA/CISA Network policies, p. 16 Use a default policy that denies all ingress and egress traffic (partly)

### GRD-03

**A tenant admin cannot delete the resource quota of its namespace**

Level basic, read-only, expected result: blocked. Pair: [GRD-01](#grd-01).

- **Proves:** The tenant cannot lift its own limit on what it may claim.
- **Does not prove:** That the quota covers every resource that matters.
- **How:** As the tenant admin, delete each resource quota of tenant A as a server-side dry run.
- **Fix when it fails:** Do not give tenant admins write rights on resource quotas.
- **Benchmarks** ([mapping](benchmarks.md)): Multi-tenancy benchmark MTB-PL1-CC-TI-1 Block modification of resource quotas (partly); Multi-tenancy benchmark MTB-PL1-BC-CPI-2 Block Multitenant Resources (partly); NSA/CISA Resource policies, p. 17 Limit the total use of a namespace with a ResourceQuota (partly)

### GRD-04

**A tenant admin cannot delete the limit range of its namespace**

Level basic, read-only, expected result: blocked. Pair: [GRD-01](#grd-01).

- **Proves:** The tenant cannot remove the defaults and maximums for its containers.
- **Does not prove:** That the limits are sensible for the node sizes.
- **How:** As the tenant admin, delete each limit range of tenant A as a server-side dry run.
- **Fix when it fails:** Do not give tenant admins write rights on limit ranges.
- **Benchmarks** ([mapping](benchmarks.md)): Multi-tenancy benchmark MTB-PL1-BC-CPI-2 Block Multitenant Resources (partly); NSA/CISA Resource policies, p. 17 Constrain each Pod or container with a LimitRange (partly)

### GRD-05

**A tenant admin cannot create role bindings**

Level basic, read-only, expected result: blocked. Pair: [GRD-01](#grd-01).

- **Proves:** The tenant cannot grant itself or others more access in its namespace.
- **Does not prove:** That a tenant cannot be given more access by a platform admin by mistake.
- **How:** As the tenant admin, create a role binding to the cluster role "admin" in tenant A as a server-side dry run.
- **Fix when it fails:** Leave role bindings out of the tenant admin role. Kubernetes also prevents escalation beyond the rights the tenant already holds.
- **Benchmarks** ([mapping](benchmarks.md)): Multi-tenancy benchmark MTB-PL2-CC-TI-1 Block role privilege escalation (partly); Multi-tenancy benchmark MTB-PL2-BC-OPS-3 Create Role Bindings (differs); CIS 5.1.8 Limit use of the Bind, Impersonate and Escalate permissions in the Kubernetes cluster (partly); NSA/CISA Role-based access control, p. 26 Give each group only the access it needs, and no access to the resources of other groups (partly)

### GRD-06

**A tenant admin cannot change the Pod Security level of its namespace**

Level basic, read-only, expected result: blocked. Pair: [GRD-01](#grd-01).

- **Proves:** The tenant cannot switch off Pod Security admission by relabelling its namespace.
- **Does not prove:** That Pod Security is enforced at the expected level. ADM-02 tests that.
- **How:** As the tenant admin, set pod-security.kubernetes.io/enforce=privileged on the namespace of tenant A as a server-side dry run.
- **Fix when it fails:** Do not give tenant admins write rights on their namespace object.
- **Benchmarks** ([mapping](benchmarks.md)): CIS 5.2.1 Ensure that the cluster has at least one active policy control mechanism in place (partly); NSA/CISA Pod security enforcement, p. 12 Enforce a minimum Pod security level with Pod Security Admission (partly)

### GRD-07

**A tenant admin cannot create endpoints**

Level basic, read-only, expected result: blocked. Pair: [GRD-08](#grd-08).

- **Proves:** The tenant cannot point a service at addresses of its choice, which could redirect traffic to other tenants or the control plane (CVE-2021-25740).
- **Does not prove:** That EndpointSlices are protected in the same way. They are checked with the same request when the API serves them.
- **How:** As the tenant admin, create an Endpoints object in tenant A as a server-side dry run.
- **Fix when it fails:** Give tenant admins read-only access to endpoints and endpoint slices. The built-in "edit" role allows writing them in older clusters.

### GRD-08

**A tenant admin can read the endpoints of its namespace**

Level basic, read-only, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** The refusal of GRD-07 is about writing, not about endpoints in general, so debugging services still works.
- **Does not prove:** Anything about other namespaces.
- **How:** As the tenant admin, list endpoints in tenant A.
- **Fix when it fails:** Allow get, list and watch on endpoints in the tenant admin role.

### GRD-09

**A tenant admin can open a shell in its own pods**

Level basic, read-only, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** The tenant keeps the access it needs to debug its own workloads.
- **Does not prove:** That a shell is safe: what it can reach is limited by the other checks.
- **How:** As the tenant admin, ask the API whether it may create pods/exec in tenant A (an access review).
- **Fix when it fails:** Allow create on pods/exec in the tenant admin role, if the platform wants tenants to debug their pods.

### GRD-10

**A tenant admin cannot open a shell in another tenant's pods**

Level basic, read-only, expected result: blocked. Pair: [GRD-09](#grd-09).

- **Proves:** A shell into another tenant's pod, which would expose its files, secrets and network position, is refused.
- **Does not prove:** That port-forward, attach and the kubelet API are refused. Only pods/exec is asked about.
- **How:** As the admin of tenant A, ask the API whether it may create pods/exec in tenant B (an access review). The answer must be no.
- **Fix when it fails:** Remove role bindings in tenant B that include tenant A's groups, and cluster role bindings that grant pods/exec in every namespace.
- **Benchmarks** ([mapping](benchmarks.md)): Multi-tenancy benchmark MTB-PL1-CC-TI-2 Block access to other tenant resources (partly); NSA/CISA Role-based access control, p. 26 Give each group only the access it needs, and no access to the resources of other groups (partly)

## Admission of pods

### ADM-01

**A pod that meets the namespace's Pod Security level is accepted**

Level basic, read-only, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** Admission lets a well-formed pod in, so the refusal of ADM-02 is about the privileged setting.
- **Does not prove:** That the pod would be scheduled or start.
- **How:** As the tenant admin, create a pod that meets the restricted level in tenant A as a server-side dry run.
- **Fix when it fails:** Find which policy refused the pod in the message and correct the policy or the namespace labels.

### ADM-02

**A privileged pod is refused**

Level basic, read-only, expected result: blocked. Pair: [ADM-01](#adm-01).

- **Proves:** A tenant cannot run a privileged container, which could take over the node and every tenant on it.
- **Does not prove:** That every other dangerous setting (host namespaces, host paths, added capabilities) is refused. Only privileged is sent.
- **How:** As the tenant admin, create a pod with privileged: true in tenant A as a server-side dry run.
- **Fix when it fails:** Label the namespace pod-security.kubernetes.io/enforce=baseline or restricted, or enforce the same with an admission policy.
- **Benchmarks** ([mapping](benchmarks.md)): Multi-tenancy benchmark MTB-PL1-BC-CPI-5 Block privileged containers (tests); CIS 5.2.2 Minimize the admission of privileged containers (tests); NSA/CISA Recommendations, p. 2 Prevent privileged containers (tests); NSA/CISA Pod security enforcement, p. 12 Enforce a minimum Pod security level with Pod Security Admission (partly)

## Network isolation

### NET-01

**Pods of the same tenant reach each other on the same node**

Level basic, active, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** The test pods and the network work, so a blocked connection in NET-04 means isolation, not a broken test.
- **Does not prove:** That services or DNS names work. Pods are reached by IP address.
- **How:** Start a server and a client of tenant A on one node, and fetch from the server by its pod IP.
- **Fix when it fails:** Allow traffic within a tenant's namespace (Bulkhead's allow-same-namespace policy), and check that the network plugin runs on the node.

### NET-02

**Pods of the same tenant reach each other across nodes**

Level basic, active, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** Traffic between nodes works, so a blocked connection in NET-05 means isolation.
- **Does not prove:** Anything on a cluster with one node, where it is skipped.
- **How:** Start a server of tenant A on one node and a client of tenant A on another, and fetch by pod IP.
- **Fix when it fails:** As for NET-01, and check the network between the nodes.

### NET-03

**Tenant pods can resolve names through the cluster DNS**

Level basic, active, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** A default-deny policy still lets the tenant use DNS, including a node-local DNS cache where the cluster has one.
- **Does not prove:** That names outside the cluster resolve.
- **How:** From a pod of tenant A, look up kubernetes.default.svc.cluster.local through the DNS server in the pod's resolv.conf.
- **Fix when it fails:** Allow egress on port 53 to the DNS pods (kube-dns and node-local-dns in kube-system, or where your DNS runs).

### NET-04

**A tenant cannot reach another tenant's pods on the same node**

Level basic, active, expected result: blocked. Pair: [NET-01](#net-01).

- **Proves:** The network plugin enforces the tenants' network policies between pods on one node, in both directions.
- **Does not prove:** That every port is closed. The test server listens on one port.
- **How:** From a client of tenant A, fetch from a server of tenant B on the same node by pod IP, and the other way round. The connection must time out.
- **Fix when it fails:** Add a default-deny network policy to every tenant namespace, and use a network plugin that enforces network policies.
- **Benchmarks** ([mapping](benchmarks.md)): CIS 5.3.1 Ensure that the CNI in use supports NetworkPolicies (tests); CIS 5.3.2 Ensure that all Namespaces have NetworkPolicies defined (partly); NSA/CISA Network policies, p. 16 Isolate Pods with network policies, because namespaces are not isolated by default (tests); NSA/CISA Network policies, p. 16 Use a CNI plugin that supports the NetworkPolicy API (tests)

### NET-05

**A tenant cannot reach another tenant's pods on another node**

Level basic, active, expected result: blocked. Pair: [NET-02](#net-02).

- **Proves:** Isolation also holds for traffic between nodes, which takes another path through the network plugin.
- **Does not prove:** Anything on a cluster with one node, where it is skipped.
- **How:** From a client of tenant A, fetch from a server of tenant B on another node, and the other way round. The connection must time out.
- **Fix when it fails:** As for NET-04.
- **Benchmarks** ([mapping](benchmarks.md)): CIS 5.3.1 Ensure that the CNI in use supports NetworkPolicies (tests); CIS 5.3.2 Ensure that all Namespaces have NetworkPolicies defined (partly); NSA/CISA Network policies, p. 16 Isolate Pods with network policies, because namespaces are not isolated by default (tests); NSA/CISA Network policies, p. 16 Use a CNI plugin that supports the NetworkPolicy API (tests)

### NET-06

**A tenant cannot open connections to the internet**

Level hardened, active, expected result: blocked. Pair: [NET-01](#net-01).

- **Proves:** Egress is closed by default, so a compromised workload cannot send data out or fetch tools.
- **Does not prove:** That no egress exists through a proxy or an egress gateway that the platform provides on purpose.
- **How:** From a pod of tenant A, connect to a public address on port 443. The connection must time out.
- **Fix when it fails:** Keep the default-deny egress policy, and allow only the destinations a tenant needs. Skip this check if your platform allows internet egress on purpose.
- **Benchmarks** ([mapping](benchmarks.md)): CIS 5.3.2 Ensure that all Namespaces have NetworkPolicies defined (partly); NSA/CISA Network policies, p. 16 Use a default policy that denies all ingress and egress traffic (partly)

## Declared flows (with --flows)

### FLW-01

**A flow the team declared as allowed connects**

Level basic, active, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** The network lets the declared source reach the declared destination on its port, and something answers there. For a destination that blocked flows also name, it shows that the destination is up, so their timeouts are about the network.
- **Does not prove:** That the application on the destination accepts the tenant: the probe opens a TCP connection and sends no data. Without via, the probe comes from the checker's client pod, so policies that select pods by their own labels are not exercised.
- **How:** From the checker's client pod in the source tenant, or, for a flow with via, from inside a running pod that matches its labels (an ephemeral container, with --probe-workloads), open a TCP connection to the destination (nc -z, three seconds). It must connect.
- **Fix when it fails:** Allow the flow: an egress rule in the source tenant and an ingress rule at the destination. Check the service name, namespace and port in the flows file.

### FLW-02

**A flow the team declared as blocked does not connect**

Level basic, active, expected result: blocked. Pair: [FLW-01](#flw-01).

- **Proves:** The network keeps the declared source away from a destination that answers others: the connection times out, or is refused while an allowed flow to the same destination and port connected.
- **Does not prove:** That the destination is unreachable by other routes, for example through a proxy or an ingress, or from sources that are not declared.
- **How:** From the checker's client pod in the source tenant, or from inside the workload its via labels select, open a TCP connection to the destination (nc -z, three seconds). It counts only when an allowed flow to the same destination and port connected in the same run.
- **Fix when it fails:** Deny the flow: remove the egress rule of the source tenant or the ingress rule at the destination that lets it through.

## Noisy neighbours

### RES-01

**A CPU hog cannot use more than its CPU limit**

Level basic, active, expected result: blocked. Pair: [RES-04](#res-04).

- **Proves:** A container that burns CPU is held near its limit, measured from its cgroup over about ten seconds.
- **Does not prove:** That the node has room for every tenant's requests. That is a capacity question.
- **How:** Start a pod of tenant A that spins on two threads with a limit of 500m, and read usage_usec from cpu.stat twice.
- **Fix when it fails:** Give every container a CPU limit, for example with a limit range default.

### RES-02

**The kernel throttles a CPU hog**

Level basic, active, expected result: blocked. Pair: [RES-04](#res-04).

- **Proves:** The limit of RES-01 is enforced by the kernel's CPU throttling, which is the mechanism, and not by chance.
- **Does not prove:** That throttling does not hurt the hog's own latency. It does, on purpose.
- **How:** Read nr_throttled from the hog's cpu.stat. It must be above zero.
- **Fix when it fails:** As for RES-01. Throttling needs cgroup v2 on the nodes.

### RES-03

**A memory hog is killed at its memory limit**

Level basic, active, expected result: blocked. Pair: [RES-05](#res-05).

- **Proves:** A container that keeps allocating memory is stopped at its own limit (OOMKilled, exit code 137) before it starves the node.
- **Does not prove:** That the node never runs out of memory, for example when requests are far below limits.
- **How:** Start a pod of tenant A that allocates memory without end, and read its last state.
- **Fix when it fails:** Give every container a memory limit, for example with a limit range default.

### RES-04

**Another tenant's server on the same node keeps answering**

Level basic, active, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** While tenant A's hogs run, a server of tenant B on the same node still answers. That is the point of the limits.
- **Does not prove:** That its response time is unchanged. Latency is not measured.
- **How:** While the hogs run, fetch from the server of tenant B from a client of tenant B.
- **Fix when it fails:** Find what starved the server: the node's free CPU and memory, its requests, and the hogs' limits.

### RES-05

**Another tenant's server on the same node does not restart**

Level basic, active, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** The server of tenant B was not evicted or killed while tenant A's hogs ran.
- **Does not prove:** Anything over a longer period than the test.
- **How:** Compare the restart count of the server of tenant B before and after the hogs ran.
- **Fix when it fails:** As for RES-04. Set memory requests close to limits so that the node is not overcommitted.

## Quota and limits

### QTA-01

**A pod within the quota is accepted**

Level basic, active, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** The tenant can use its quota, so the refusal of QTA-02 is about the quota.
- **Does not prove:** That the pod is scheduled. Only admission is tested.
- **How:** Read the quota of tenant A, and create a pod that requests about half of the free CPU.
- **Fix when it fails:** Check the quota and the limit range of the namespace: the defaults may not fit inside the quota.

### QTA-02

**A pod that would exceed the quota is refused**

Level basic, active, expected result: blocked. Pair: [QTA-01](#qta-01).

- **Proves:** The resource quota limits what one tenant can claim from the shared cluster.
- **Does not prove:** That what is claimed is used. A quota limits requests, not usage.
- **How:** With the pod of QTA-01 running, create a second pod that would take the requests above the quota.
- **Fix when it fails:** Give every tenant namespace a resource quota with CPU and memory requests and limits.
- **Benchmarks** ([mapping](benchmarks.md)): Multi-tenancy benchmark MTB-PL1-CC-FNS-1 Configure namespace resource quotas (partly); NSA/CISA Resource policies, p. 17 Limit the total use of a namespace with a ResourceQuota (tests)

### QTA-03

**A ClusterIP service is accepted**

Level basic, read-only, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** The tenant can create services for its own traffic, so the refusals of QTA-04 and QTA-05 are about the service types.
- **Does not prove:** That the service routes traffic.
- **How:** As the tenant admin, create a ClusterIP service in tenant A as a server-side dry run.
- **Fix when it fails:** Allow services in the tenant admin role and in the quota.

### QTA-04

**A NodePort service is refused**

Level basic, read-only, expected result: blocked. Pair: [QTA-03](#qta-03).

- **Proves:** A tenant cannot open a port on every node, which bypasses network policies for outside traffic.
- **Does not prove:** That no other way exists to expose a pod outside the cluster, such as an ingress.
- **How:** As the tenant admin, create a NodePort service in tenant A as a server-side dry run.
- **Fix when it fails:** Set services.nodeports: 0 in the tenant's resource quota, or refuse the type with an admission policy.
- **Benchmarks** ([mapping](benchmarks.md)): Multi-tenancy benchmark MTB-PL1-BC-HI-1 Block use of NodePort services (tests)

### QTA-05

**A LoadBalancer service is refused**

Level basic, read-only, expected result: blocked. Pair: [QTA-03](#qta-03).

- **Proves:** A tenant cannot create a cloud load balancer, which costs money and exposes the tenant to the internet.
- **Does not prove:** That load balancers made by other controllers for the tenant are refused.
- **How:** As the tenant admin, create a LoadBalancer service in tenant A as a server-side dry run.
- **Fix when it fails:** Set services.loadbalancers: 0 in the tenant's resource quota, or refuse the type with an admission policy.

### QTA-06

**A container above the per-container maximum is refused**

Level basic, read-only, expected result: blocked. Pair: [QTA-07](#qta-07).

- **Proves:** The limit range caps a single container, so one pod cannot take a whole node.
- **Does not prove:** That the maximum is lower than a node's size.
- **How:** Read the limit range of tenant A, and create a pod with a CPU limit above its maximum as a server-side dry run.
- **Fix when it fails:** Add a max for CPU and memory to the tenant's limit range.
- **Benchmarks** ([mapping](benchmarks.md)): NSA/CISA Resource policies, p. 17 Constrain each Pod or container with a LimitRange (tests)

### QTA-07

**A pod without resources receives the default limits**

Level basic, read-only, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** Containers that set nothing still get limits, so the CPU and memory checks hold for them too. It also shows that a normal pod passes the limit range, so the refusal of QTA-06 is about the maximum.
- **Does not prove:** That the defaults suit the tenant's workloads.
- **How:** Create a pod without resources in tenant A as a server-side dry run, and read the limits that admission filled in.
- **Fix when it fails:** Add default and defaultRequest to the tenant's limit range.

### QTA-08

**As many pods as the quota allows can be created**

Level basic, active, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** The pod count of the quota is reachable with default-sized pods, so the quota and the limit range fit together.
- **Does not prove:** That the pods would fit on the nodes.
- **How:** Read the pod count of the quota of tenant A, and create that many small pods.
- **Fix when it fails:** Make the limit range defaults small enough that the quota's pod count fits inside its CPU and memory.

### QTA-09

**One pod more than the quota's pod count is refused**

Level basic, active, expected result: blocked. Pair: [QTA-08](#qta-08).

- **Proves:** The pod count limits how many pods a tenant can run, independently of their size.
- **Does not prove:** That other object counts (services, volume claims) are limited.
- **How:** With the pods of QTA-08 created, create one more.
- **Fix when it fails:** Set a pods count in the tenant's resource quota.
- **Benchmarks** ([mapping](benchmarks.md)): Multi-tenancy benchmark MTB-PL1-CC-FNS-2 Configure namespace object limits (partly); NSA/CISA Resource policies, p. 17 Limit the total use of a namespace with a ResourceQuota (partly)

## Operators (research, off by default)

### XNS-01

**A custom resource that references the tenant's own namespace is accepted**

Level research, read-only, expected result: allowed. Pair: none, this check makes other results trustworthy.

- **Proves:** The tenant admin may create the listed custom resource, and the body in the references file is valid, so a refusal in XNS-02 is about the other namespace.
- **Does not prove:** That the operator handles the resource correctly: the request is a dry run, so no operator sees it.
- **How:** As the tenant admin, create the custom resource in tenant A as a server-side dry run, with its namespace field (namespacePath in --operator-refs) set to tenant A. It must be accepted. Nothing is stored.
- **Fix when it fails:** Give tenant admins the right to create the resource, or correct the body and the namespacePath in the references file.

### XNS-02

**A custom resource that references another tenant's namespace is refused**

Level research, read-only, expected result: blocked. Pair: [XNS-01](#xns-01).

- **Proves:** Tenant A cannot even submit a resource that would make an operator read or change tenant B's objects, the class of weakness described in "Breaking the Bulkhead" (NDSS 2026). The refusal comes before any operator sees the resource, whatever the operator's code does.
- **Does not prove:** That the operator itself is safe: without the admission rule, a vulnerable operator would follow the reference. Other fields, and references that lead to cluster-wide objects, are not tested.
- **How:** As the admin of tenant A, create the same custom resource in tenant A as a server-side dry run, with its namespace field set to tenant B. It must be refused at admission. It counts only when XNS-01 passed for the same reference and tenant.
- **Fix when it fails:** Add an admission rule (a ValidatingAdmissionPolicy or a policy engine) that refuses the resource when its namespace field differs from the resource's own namespace, or when the requester may not access the referenced object.
