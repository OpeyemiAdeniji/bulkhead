<!-- Generated from internal/catalog by `make checks-doc`. Do not edit by hand. -->

# The checks and published benchmarks

This page links the checks of `bulkhead check` ([checks.md](checks.md)) to the items of published benchmarks, for readers who know them. It was made by reading each item and each check, and it maps only where they overlap.

- **tests:** the check tests the behaviour the item asks for, on the tenants under test.
- **partly:** the check tests a part of it. For example, a check that a tenant cannot read another tenant's secrets is a part of an item about all of another tenant's objects.
- **differs:** Bulkhead deliberately blocks what the item asks to allow.

A pass is evidence for an item on the cluster and the tenants tested, not a certification. Most CIS items are about configuration that a person reviews ("Manual" in the benchmark), and the checks test the behaviour that such configuration should produce, from the tenant's side. An item without a check is not tested by Bulkhead.

## Kubernetes multi-tenancy benchmarks (archived)

Source: https://github.com/kubernetes-retired/multi-tenancy/tree/master/benchmarks

The benchmarks were archived in 2023, and their README said they were not ready for use. The items are taken from the `config.yaml` of each test of `kubectl-mtb`. The source gives the ID MTB-PL1-BC-HI-1 to two tests (host path volumes and NodePort services), so the title tells them apart. Two level 2 items ask that tenants may create their own role bindings and network policies. Bulkhead's tenant admin may not create role bindings, by design (GRD-05), because a binding to the cluster role `admin` is a way to escalate.

| Item | Title | Checks |
|---|---|---|
| MTB-PL1-CC-CPI-1 | Block access to cluster resources | not tested |
| MTB-PL1-BC-CPI-2 | Block Multitenant Resources | [GRD-02](checks.md#grd-02) (partly), [GRD-03](checks.md#grd-03) (partly), [GRD-04](checks.md#grd-04) (partly) |
| MTB-PL1-BC-CPI-3 | Block add capabilities | not tested |
| MTB-PL1-BC-CPI-4 | Require run as non-root user | not tested |
| MTB-PL1-BC-CPI-5 | Block privileged containers | [ADM-02](checks.md#adm-02) (tests) |
| MTB-PL1-BC-CPI-6 | Block privilege escalation | not tested |
| MTB-PL1-CC-TI-1 | Block modification of resource quotas | [GRD-03](checks.md#grd-03) (partly) |
| MTB-PL1-CC-TI-2 | Block access to other tenant resources | [ACC-02](checks.md#acc-02) (partly), [ACC-03](checks.md#acc-03) (partly), [GRD-10](checks.md#grd-10) (partly) |
| MTB-PL1-BC-HI-1 | Block use of host path volumes | not tested |
| MTB-PL1-BC-HI-1 | Block use of NodePort services | [QTA-04](checks.md#qta-04) (tests) |
| MTB-PL1-BC-HI-3 | Block use of host networking and ports | not tested |
| MTB-PL1-BC-HI-4 | Block use of host PID | not tested |
| MTB-PL1-BC-HI-5 | Block use of host IPC | not tested |
| MTB-PL1-CC-FNS-1 | Configure namespace resource quotas | [QTA-02](checks.md#qta-02) (partly) |
| MTB-PL1-CC-FNS-2 | Configure namespace object limits | [QTA-09](checks.md#qta-09) (partly) |
| MTB-PL1-CC-DI-1 | Require always imagePullPolicy | not tested |
| MTB-PL2-CC-TI-1 | Block role privilege escalation | [GRD-05](checks.md#grd-05) (partly) |
| MTB-PL2-BC-OPS-3 | Create Role Bindings | [GRD-05](checks.md#grd-05) (differs) |
| MTB-PL2-BC-OPS-4 | Create Network Policies | not tested |

9 of 19 items have at least one check.

## CIS Kubernetes Benchmark v2.0, section 5: Policies

Source: https://www.cisecurity.org/benchmark/kubernetes

The IDs and titles are those of section 5 (Policies) of the CIS Kubernetes Benchmark v2.0, as listed in the `cis-2.0` configuration of kube-bench, which uses that edition for Kubernetes 1.34 and 1.35. Sections 1 to 4 are about the control plane and the nodes, which tenants do not reach, and are not mapped. The full benchmark is available from CIS.

| Item | Title | Checks |
|---|---|---|
| 5.1.1 | Ensure that the cluster-admin role is only used where required | not tested |
| 5.1.2 | Minimize access to secrets | [ACC-02](checks.md#acc-02) (partly), [ACC-03](checks.md#acc-03) (partly), [ACC-04](checks.md#acc-04) (partly) |
| 5.1.3 | Minimize wildcard use in Roles and ClusterRoles | not tested |
| 5.1.4 | Minimize access to create pods | not tested |
| 5.1.5 | Ensure that default service accounts are not actively used | not tested |
| 5.1.6 | Ensure that Service Account Tokens are only mounted where necessary | not tested |
| 5.1.7 | Avoid use of system:masters group | not tested |
| 5.1.8 | Limit use of the Bind, Impersonate and Escalate permissions in the Kubernetes cluster | [GRD-05](checks.md#grd-05) (partly) |
| 5.1.9 | Minimize access to create persistent volumes | not tested |
| 5.1.10 | Minimize access to the proxy sub-resource of nodes | not tested |
| 5.1.11 | Minimize access to the approval sub-resource of certificatesigningrequests objects | not tested |
| 5.1.12 | Minimize access to webhook configuration objects | not tested |
| 5.1.13 | Minimize access to the service account token creation | not tested |
| 5.2.1 | Ensure that the cluster has at least one active policy control mechanism in place | [GRD-06](checks.md#grd-06) (partly) |
| 5.2.2 | Minimize the admission of privileged containers | [ADM-02](checks.md#adm-02) (tests) |
| 5.2.3 | Minimize the admission of containers wishing to share the host process ID namespace | not tested |
| 5.2.4 | Minimize the admission of containers wishing to share the host IPC namespace | not tested |
| 5.2.5 | Minimize the admission of containers wishing to share the host network namespace | not tested |
| 5.2.6 | Minimize the admission of containers with allowPrivilegeEscalation | not tested |
| 5.2.7 | Minimize the admission of root containers | not tested |
| 5.2.8 | Minimize the admission of containers with the NET_RAW capability | not tested |
| 5.2.9 | Minimize the admission of containers with capabilities assigned | not tested |
| 5.2.10 | Minimize the admission of Windows HostProcess containers | not tested |
| 5.2.11 | Minimize the admission of HostPath volumes | not tested |
| 5.2.12 | Minimize the admission of containers which use HostPorts | not tested |
| 5.3.1 | Ensure that the CNI in use supports NetworkPolicies | [NET-04](checks.md#net-04) (tests), [NET-05](checks.md#net-05) (tests) |
| 5.3.2 | Ensure that all Namespaces have NetworkPolicies defined | [GRD-02](checks.md#grd-02) (partly), [NET-04](checks.md#net-04) (partly), [NET-05](checks.md#net-05) (partly), [NET-06](checks.md#net-06) (partly) |
| 5.4.1 | Prefer using Secrets as files over Secrets as environment variables | not tested |
| 5.4.2 | Consider external secret storage | not tested |
| 5.5.1 | Configure Image Provenance using ImagePolicyWebhook admission controller | not tested |
| 5.6.1 | Create administrative boundaries between resources using namespaces | [ACC-02](checks.md#acc-02) (partly), [ACC-03](checks.md#acc-03) (partly) |
| 5.6.2 | Ensure that the seccomp profile is set to docker/default in your Pod definitions | not tested |
| 5.6.3 | Apply SecurityContext to your Pods and Containers | not tested |
| 5.6.4 | The default namespace should not be used | not tested |

7 of 34 items have at least one check.

## NSA and CISA Kubernetes Hardening Guide, version 1.2 (August 2022)

Source: https://media.defense.gov/2022/Aug/29/2003066362/-1/-1/0/CTR_KUBERNETES_HARDENING_GUIDANCE_1.2_20220829.PDF

The guide has no item numbers, so each item is named by its section and the page number printed in the guide, and its title summarises one recommendation of that section. The items are the recommendations of the parts that tenants reach: Pod security, namespaces, network policies, resource policies, access to Secrets, cloud metadata and access control. The control plane, etcd, encryption, authentication methods, logging and upgrades are for cluster administrators and are not mapped. The guide's site refuses automated downloads; open the link in a browser.

| Item | Title | Checks |
|---|---|---|
| Recommendations, p. 2 | Prevent privileged containers | [ADM-02](checks.md#adm-02) (tests) |
| Recommendations, p. 2 | Deny hostPID, hostIPC, hostNetwork and host paths | not tested |
| Recommendations, p. 2 | Reject containers that run as root or allow elevation to root | not tested |
| Non-root containers, p. 9 | Build and run containers as non-root users | not tested |
| Immutable container file systems, p. 10 | Run containers with read-only file systems where possible | not tested |
| Pod security enforcement, p. 12 | Enforce a minimum Pod security level with Pod Security Admission | [GRD-06](checks.md#grd-06) (partly), [ADM-02](checks.md#adm-02) (partly) |
| Protecting Pod service account tokens, p. 12 | Do not mount service account tokens that are not needed | not tested |
| Hardening container environments, p. 13 | Use seccomp, sandboxes or hypervisor-backed containers | not tested |
| Namespaces, p. 14 | Partition resources among teams with namespaces | [ACC-02](checks.md#acc-02) (partly), [ACC-03](checks.md#acc-03) (partly) |
| Network policies, p. 16 | Isolate Pods with network policies, because namespaces are not isolated by default | [NET-04](checks.md#net-04) (tests), [NET-05](checks.md#net-05) (tests) |
| Network policies, p. 16 | Use a CNI plugin that supports the NetworkPolicy API | [NET-04](checks.md#net-04) (tests), [NET-05](checks.md#net-05) (tests) |
| Network policies, p. 16 | Use a default policy that denies all ingress and egress traffic | [GRD-02](checks.md#grd-02) (partly), [NET-06](checks.md#net-06) (partly) |
| Resource policies, p. 17 | Constrain each Pod or container with a LimitRange | [GRD-04](checks.md#grd-04) (partly), [QTA-06](checks.md#qta-06) (tests) |
| Resource policies, p. 17 | Limit the total use of a namespace with a ResourceQuota | [GRD-03](checks.md#grd-03) (partly), [QTA-02](checks.md#qta-02) (tests), [QTA-09](checks.md#qta-09) (partly) |
| Resource policies, p. 17 | Limit process IDs per node and per Pod | not tested |
| Secrets, p. 20 | Restrict access to Secrets with RBAC | [ACC-02](checks.md#acc-02) (partly), [ACC-03](checks.md#acc-03) (partly), [ACC-04](checks.md#acc-04) (partly) |
| Protecting sensitive cloud infrastructure, p. 21 | Keep Pods away from cloud metadata services | not tested |
| Role-based access control, p. 26 | Give each group only the access it needs, and no access to the resources of other groups | [ACC-02](checks.md#acc-02) (partly), [ACC-03](checks.md#acc-03) (partly), [GRD-05](checks.md#grd-05) (partly), [GRD-10](checks.md#grd-10) (partly) |

10 of 18 items have at least one check.
