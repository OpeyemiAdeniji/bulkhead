package flows

import (
	"bytes"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"sigs.k8s.io/yaml"
)

const schemaPath = "../../schemas/flows.schema.json"

const good = `
apiVersion: bulkhead.io/v1alpha1
kind: Flows
flows:
  - name: alpha-to-db
    from: tenant-alpha
    to: {service: postgres, namespace: shared-db, port: 5432}
    expect: allowed
  - name: bravo-not-to-db
    description: bravo has no business there
    from: tenant-bravo
    to: {service: postgres, namespace: shared-db, port: 5432}
    expect: blocked
  - name: alpha-to-api
    from: tenant-alpha
    to: {host: api.example.com, port: 443}
    expect: allowed
  - name: alpha-to-ip
    from: tenant-alpha
    to: {host: "10.0.0.7", port: 8080}
    expect: allowed
`

// schemaValid reports whether a document matches the published schema.
func schemaValid(t *testing.T, doc string) bool {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	s, err := c.Compile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	js, err := yaml.YAMLToJSON([]byte(doc))
	if err != nil {
		return false
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(js))
	if err != nil {
		return false
	}
	return s.Validate(inst) == nil
}

func TestAGoodFile(t *testing.T) {
	f, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if !schemaValid(t, good) {
		t.Fatal("the schema refuses a good file")
	}
	if len(f.Flows) != 4 {
		t.Fatalf("got %d flows", len(f.Flows))
	}
	db := f.Flows[0].To
	if db.Address() != "postgres.shared-db.svc" || db.String() != "postgres.shared-db.svc:5432" || db.Label() != "shared-db" {
		t.Errorf("service destination: %s, %s, %s", db.Address(), db.String(), db.Label())
	}
	if api := f.Flows[2].To; api.String() != "api.example.com:443" || api.Label() != "api.example.com" {
		t.Errorf("host destination: %s, %s", api.String(), api.Label())
	}
	if pairs := f.Pairs(f.Flows[1]); len(pairs) != 1 || pairs[0].Name != "alpha-to-db" {
		t.Errorf("pairs of the blocked flow: %v", pairs)
	}
}

func TestTheExampleFileIsValid(t *testing.T) {
	f, err := Load("../../examples/checker/flows.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.CheckSources([]string{"tenant-alpha", "tenant-bravo"}); err != nil {
		t.Fatal(err)
	}
}

// TestBadFiles: the loader refuses each bad file with a message that names the problem, and
// the schema agrees wherever a schema can express the rule.
func TestBadFiles(t *testing.T) {
	flow := func(s string) string {
		return "apiVersion: bulkhead.io/v1alpha1\nkind: Flows\nflows:\n" + s
	}
	ok := "  - {name: a, from: tenant-a, to: {service: s, namespace: ns, port: 80}, expect: allowed}\n"
	for _, c := range []struct {
		name, doc, want string
		schemaSees      bool // false: only the loader can see it (the pair rule, a repeated name)
	}{
		{"wrong kind", "apiVersion: bulkhead.io/v1alpha1\nkind: Flow\nflows: []\n", "kind Flows", true},
		{"no flows", "apiVersion: bulkhead.io/v1alpha1\nkind: Flows\nflows: []\n", "between 1 and 200", true},
		{"unknown field", flow("  - {name: a, from: tenant-a, to: {service: s, namespace: ns, port: 80}, expected: allowed}\n"), "unknown field", true},
		{"bad expect", flow("  - {name: a, from: tenant-a, to: {service: s, namespace: ns, port: 80}, expect: maybe}\n"), "expect must be", true},
		{"bad name", flow("  - {name: A_b, from: tenant-a, to: {service: s, namespace: ns, port: 80}, expect: allowed}\n"), "DNS label", true},
		{"repeated name", flow(ok + ok), "used twice", false},
		{"port zero", flow("  - {name: a, from: tenant-a, to: {service: s, namespace: ns, port: 0}, expect: allowed}\n"), "between 1 and 65535", true},
		{"service without namespace", flow("  - {name: a, from: tenant-a, to: {service: s, port: 80}, expect: allowed}\n"), "both be names", true},
		{"service and host", flow("  - {name: a, from: tenant-a, to: {service: s, namespace: ns, host: h, port: 80}, expect: allowed}\n"), "both service and host", true},
		{"host with namespace", flow("  - {name: a, from: tenant-a, to: {host: h, namespace: ns, port: 80}, expect: allowed}\n"), "belongs to a service", true},
		{"bad host", flow("  - {name: a, from: tenant-a, to: {host: 'not a host!', port: 80}, expect: allowed}\n"), "neither an IP address nor a DNS name", true},
		{"no destination", flow("  - {name: a, from: tenant-a, to: {port: 80}, expect: allowed}\n"), "needs a service", true},
		{"blocked without pair", flow(ok + "  - {name: b, from: tenant-b, to: {service: other, namespace: ns, port: 80}, expect: blocked}\n"), "needs an allowed flow to the same destination", false},
		{"blocked with pair on another port", flow(ok + "  - {name: b, from: tenant-b, to: {service: s, namespace: ns, port: 81}, expect: blocked}\n"), "same destination (s.ns.svc:81)", false},
		{"two documents", flow(ok) + "---\n" + flow(ok), "more than one document", false},
		{"not yaml", "flows: [", "not valid YAML", false},
		// Go's decoder would take "Expect" for "expect"; the schema does not.
		{"field in the wrong case", flow("  - {name: a, from: tenant-a, to: {service: s, namespace: ns, port: 80}, expect: allowed, Expect: allowed}\n"), "Expect", true},
		{"empty via", flow("  - {name: a, from: tenant-a, via: {}, to: {service: s, namespace: ns, port: 80}, expect: allowed}\n"), "between 1 and 10 labels", true},
		{"bad via key", flow("  - {name: a, from: tenant-a, via: {'bad key': x}, to: {service: s, namespace: ns, port: 80}, expect: allowed}\n"), "is not a label key", true},
		{"bad via value", flow("  - {name: a, from: tenant-a, via: {app: 'not ok!'}, to: {service: s, namespace: ns, port: 80}, expect: allowed}\n"), "is not a label value", true},
	} {
		_, err := Parse([]byte(c.doc))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error with %q, got %v", c.name, c.want, err)
		}
		if c.schemaSees && schemaValid(t, c.doc) {
			t.Errorf("%s: the schema accepts what the loader refuses", c.name)
		}
	}
}

// TestYAMLBooleans: YAML reads a bare "no" as false. The loader says so; quoted, it is a name.
func TestYAMLBooleans(t *testing.T) {
	doc := func(ns string) string {
		return "apiVersion: bulkhead.io/v1alpha1\nkind: Flows\nflows:\n" +
			"  - {name: a, from: tenant-a, to: {service: s, namespace: " + ns + ", port: 80}, expect: allowed}\n"
	}
	if _, err := Parse([]byte(doc("no"))); err == nil || !strings.Contains(err.Error(), "put such a name in quotes") {
		t.Errorf("a bare no must be explained: %v", err)
	}
	if _, err := Parse([]byte(doc(`"no"`))); err != nil {
		t.Errorf("a quoted no is a name: %v", err)
	}
}

// TestADashLineInsideATextIsNotADocument: only "---" at the start of a line starts a document.
func TestADashLineInsideATextIsNotADocument(t *testing.T) {
	doc := "---\napiVersion: bulkhead.io/v1alpha1\nkind: Flows\nflows:\n" +
		"  - name: a\n    description: |\n      before\n      ---\n      after\n" +
		"    from: tenant-a\n    to: {service: s, namespace: ns, port: 80}\n    expect: allowed\n"
	if _, err := Parse([]byte(doc)); err != nil {
		t.Errorf("a leading --- and an indented --- are allowed: %v", err)
	}
}

func TestEveryErrorIsReported(t *testing.T) {
	doc := "apiVersion: bulkhead.io/v1alpha1\nkind: Flows\nflows:\n" +
		"  - {name: a, from: tenant-a, to: {service: s, namespace: ns, port: 0}, expect: allowed}\n" +
		"  - {name: b, from: tenant-a, to: {host: 'x y', port: 80}, expect: maybe}\n"
	_, err := Parse([]byte(doc))
	for _, want := range []string{`flow 1 ("a")`, `flow 2 ("b"): expect`, `flow 2 ("b"): to.host`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("the error misses %q: %v", want, err)
		}
	}
}

func TestVia(t *testing.T) {
	doc := "apiVersion: bulkhead.io/v1alpha1\nkind: Flows\nflows:\n" +
		"  - {name: a, from: tenant-b, via: {app: orders, app.kubernetes.io/part-of: shop}, to: {service: s, namespace: ns, port: 80}, expect: allowed}\n" +
		"  - {name: b, from: tenant-a, to: {service: s, namespace: ns, port: 80}, expect: blocked}\n" +
		"  - {name: c, from: tenant-a, via: {app: web}, to: {service: s, namespace: ns, port: 80}, expect: allowed}\n"
	f, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !schemaValid(t, doc) {
		t.Fatal("the schema refuses a file with via")
	}
	if got := f.Flows[0].Selector(); got != "app=orders,app.kubernetes.io/part-of=shop" {
		t.Errorf("selector %q", got)
	}
	if f.Flows[1].Selector() != "" {
		t.Error("a flow without via has no selector")
	}
	if !f.UsesWorkloads() || strings.Join(f.WorkloadNamespaces(), ",") != "tenant-a,tenant-b" {
		t.Errorf("workload namespaces: %v", f.WorkloadNamespaces())
	}
	plain, _ := Parse([]byte(good))
	if plain.UsesWorkloads() {
		t.Error("a file without via uses no workloads")
	}
}

func TestCheckSources(t *testing.T) {
	f, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	err = f.CheckSources([]string{"tenant-alpha"})
	if err == nil || !strings.Contains(err.Error(), "bravo-not-to-db (from tenant-bravo)") {
		t.Errorf("a flow from a namespace that is not under test must be refused: %v", err)
	}
}
