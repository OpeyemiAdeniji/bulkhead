package catalog

import "slices"

// Source is a published benchmark or guide that checks are mapped to.
type Source string

// The sources. Only documents that were read are listed; docs/benchmarks.md says which edition.
const (
	// MTB is the Kubernetes multi-tenancy benchmarks of the Kubernetes Multi-Tenancy working
	// group, archived in 2023 (kubernetes-retired/multi-tenancy, benchmarks/).
	MTB Source = "mtb"
	// CIS is the CIS Kubernetes Benchmark v2.0, section 5 (Policies), the edition kube-bench uses
	// for Kubernetes 1.34 and 1.35.
	CIS Source = "cis"
)

// Relation says how much of a benchmark item a check covers.
type Relation string

// The relations.
const (
	// Tests: the check tests the behaviour the item asks for, for the tenants under test.
	Tests Relation = "tests"
	// Partly: the check tests part of what the item asks for.
	Partly Relation = "partly"
	// Differs: Bulkhead deliberately blocks what the item asks to allow, or the other way round.
	Differs Relation = "differs"
)

// Reference links a check to an item of a benchmark.
type Reference struct {
	Source   Source   `json:"source"`
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Relation Relation `json:"relation"`
}

// BenchmarkItem is one item of a benchmark, as published.
type BenchmarkItem struct {
	ID    string
	Title string
}

// SourceInfo describes a benchmark.
type SourceInfo struct {
	Source Source
	Name   string
	URL    string
	Items  []BenchmarkItem
}

// Sources returns the benchmarks, with every item of the part that applies to tenants.
func Sources() []SourceInfo {
	return []SourceInfo{
		{
			Source: MTB,
			Name:   "Kubernetes multi-tenancy benchmarks (archived)",
			URL:    "https://github.com/kubernetes-retired/multi-tenancy/tree/master/benchmarks",
			// As in the config.yaml of each test of kubectl-mtb. MTB-PL1-BC-HI-1 is used by two
			// tests in the source, so references name the title as well.
			Items: []BenchmarkItem{
				{"MTB-PL1-CC-CPI-1", "Block access to cluster resources"},
				{"MTB-PL1-BC-CPI-2", "Block Multitenant Resources"},
				{"MTB-PL1-BC-CPI-3", "Block add capabilities"},
				{"MTB-PL1-BC-CPI-4", "Require run as non-root user"},
				{"MTB-PL1-BC-CPI-5", "Block privileged containers"},
				{"MTB-PL1-BC-CPI-6", "Block privilege escalation"},
				{"MTB-PL1-CC-TI-1", "Block modification of resource quotas"},
				{"MTB-PL1-CC-TI-2", "Block access to other tenant resources"},
				{"MTB-PL1-BC-HI-1", "Block use of host path volumes"},
				{"MTB-PL1-BC-HI-1", "Block use of NodePort services"},
				{"MTB-PL1-BC-HI-3", "Block use of host networking and ports"},
				{"MTB-PL1-BC-HI-4", "Block use of host PID"},
				{"MTB-PL1-BC-HI-5", "Block use of host IPC"},
				{"MTB-PL1-CC-FNS-1", "Configure namespace resource quotas"},
				{"MTB-PL1-CC-FNS-2", "Configure namespace object limits"},
				{"MTB-PL1-CC-DI-1", "Require always imagePullPolicy"},
				{"MTB-PL2-CC-TI-1", "Block role privilege escalation"},
				{"MTB-PL2-BC-OPS-3", "Create Role Bindings"},
				{"MTB-PL2-BC-OPS-4", "Create Network Policies"},
			},
		},
		{
			Source: CIS,
			Name:   "CIS Kubernetes Benchmark v2.0, section 5: Policies",
			URL:    "https://www.cisecurity.org/benchmark/kubernetes",
			Items: []BenchmarkItem{
				{"5.1.1", "Ensure that the cluster-admin role is only used where required"},
				{"5.1.2", "Minimize access to secrets"},
				{"5.1.3", "Minimize wildcard use in Roles and ClusterRoles"},
				{"5.1.4", "Minimize access to create pods"},
				{"5.1.5", "Ensure that default service accounts are not actively used"},
				{"5.1.6", "Ensure that Service Account Tokens are only mounted where necessary"},
				{"5.1.7", "Avoid use of system:masters group"},
				{"5.1.8", "Limit use of the Bind, Impersonate and Escalate permissions in the Kubernetes cluster"},
				{"5.1.9", "Minimize access to create persistent volumes"},
				{"5.1.10", "Minimize access to the proxy sub-resource of nodes"},
				{"5.1.11", "Minimize access to the approval sub-resource of certificatesigningrequests objects"},
				{"5.1.12", "Minimize access to webhook configuration objects"},
				{"5.1.13", "Minimize access to the service account token creation"},
				{"5.2.1", "Ensure that the cluster has at least one active policy control mechanism in place"},
				{"5.2.2", "Minimize the admission of privileged containers"},
				{"5.2.3", "Minimize the admission of containers wishing to share the host process ID namespace"},
				{"5.2.4", "Minimize the admission of containers wishing to share the host IPC namespace"},
				{"5.2.5", "Minimize the admission of containers wishing to share the host network namespace"},
				{"5.2.6", "Minimize the admission of containers with allowPrivilegeEscalation"},
				{"5.2.7", "Minimize the admission of root containers"},
				{"5.2.8", "Minimize the admission of containers with the NET_RAW capability"},
				{"5.2.9", "Minimize the admission of containers with capabilities assigned"},
				{"5.2.10", "Minimize the admission of Windows HostProcess containers"},
				{"5.2.11", "Minimize the admission of HostPath volumes"},
				{"5.2.12", "Minimize the admission of containers which use HostPorts"},
				{"5.3.1", "Ensure that the CNI in use supports NetworkPolicies"},
				{"5.3.2", "Ensure that all Namespaces have NetworkPolicies defined"},
				{"5.4.1", "Prefer using Secrets as files over Secrets as environment variables"},
				{"5.4.2", "Consider external secret storage"},
				{"5.5.1", "Configure Image Provenance using ImagePolicyWebhook admission controller"},
				{"5.6.1", "Create administrative boundaries between resources using namespaces"},
				{"5.6.2", "Ensure that the seccomp profile is set to docker/default in your Pod definitions"},
				{"5.6.3", "Apply SecurityContext to your Pods and Containers"},
				{"5.6.4", "The default namespace should not be used"},
			},
		},
	}
}

func ref(s Source, id, title string, r Relation) Reference {
	return Reference{Source: s, ID: id, Title: title, Relation: r}
}

// references maps checks to benchmark items. A check is mapped only where it tests what the
// item asks for, or a part of it; checks that make other results trustworthy (the "allowed"
// pairs) are not mapped unless they test an item themselves.
var references = map[string][]Reference{
	"ACC-02": {
		ref(MTB, "MTB-PL1-CC-TI-2", "Block access to other tenant resources", Partly),
		ref(CIS, "5.1.2", "Minimize access to secrets", Partly),
		ref(CIS, "5.6.1", "Create administrative boundaries between resources using namespaces", Partly),
	},
	"ACC-03": {
		ref(MTB, "MTB-PL1-CC-TI-2", "Block access to other tenant resources", Partly),
		ref(CIS, "5.1.2", "Minimize access to secrets", Partly),
		ref(CIS, "5.6.1", "Create administrative boundaries between resources using namespaces", Partly),
	},
	"ACC-04": {
		ref(CIS, "5.1.2", "Minimize access to secrets", Partly),
	},
	"GRD-02": {
		ref(MTB, "MTB-PL1-BC-CPI-2", "Block Multitenant Resources", Partly),
		ref(CIS, "5.3.2", "Ensure that all Namespaces have NetworkPolicies defined", Partly),
	},
	"GRD-03": {
		ref(MTB, "MTB-PL1-CC-TI-1", "Block modification of resource quotas", Partly),
		ref(MTB, "MTB-PL1-BC-CPI-2", "Block Multitenant Resources", Partly),
	},
	"GRD-04": {
		ref(MTB, "MTB-PL1-BC-CPI-2", "Block Multitenant Resources", Partly),
	},
	"GRD-05": {
		ref(MTB, "MTB-PL2-CC-TI-1", "Block role privilege escalation", Partly),
		ref(MTB, "MTB-PL2-BC-OPS-3", "Create Role Bindings", Differs),
		ref(CIS, "5.1.8", "Limit use of the Bind, Impersonate and Escalate permissions in the Kubernetes cluster", Partly),
	},
	"GRD-06": {
		ref(CIS, "5.2.1", "Ensure that the cluster has at least one active policy control mechanism in place", Partly),
	},
	"GRD-10": {
		ref(MTB, "MTB-PL1-CC-TI-2", "Block access to other tenant resources", Partly),
	},
	"ADM-02": {
		ref(MTB, "MTB-PL1-BC-CPI-5", "Block privileged containers", Tests),
		ref(CIS, "5.2.2", "Minimize the admission of privileged containers", Tests),
	},
	"NET-04": {
		ref(CIS, "5.3.1", "Ensure that the CNI in use supports NetworkPolicies", Tests),
		ref(CIS, "5.3.2", "Ensure that all Namespaces have NetworkPolicies defined", Partly),
	},
	"NET-05": {
		ref(CIS, "5.3.1", "Ensure that the CNI in use supports NetworkPolicies", Tests),
		ref(CIS, "5.3.2", "Ensure that all Namespaces have NetworkPolicies defined", Partly),
	},
	"NET-06": {
		ref(CIS, "5.3.2", "Ensure that all Namespaces have NetworkPolicies defined", Partly),
	},
	"QTA-02": {
		ref(MTB, "MTB-PL1-CC-FNS-1", "Configure namespace resource quotas", Partly),
	},
	"QTA-04": {
		ref(MTB, "MTB-PL1-BC-HI-1", "Block use of NodePort services", Tests),
	},
	"QTA-09": {
		ref(MTB, "MTB-PL1-CC-FNS-2", "Configure namespace object limits", Partly),
	},
}

// referencesOf returns a copy of the references of a check, so callers cannot change the map.
func referencesOf(id string) []Reference { return slices.Clone(references[id]) }
