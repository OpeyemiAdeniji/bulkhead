package operatorrefs

import (
	"bytes"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"sigs.k8s.io/yaml"
)

const good = `
apiVersion: bulkhead.io/v1alpha1
kind: OperatorReferences
references:
  - name: storage-secret
    apiVersion: tempo.grafana.com/v1alpha1
    kind: TempoStack
    resource: tempostacks
    namespacePath: spec.storage.secret.namespace
    body:
      spec:
        storage:
          secret: {name: storage, type: s3}
  - name: simple
    apiVersion: probe.bulkhead.io/v1
    kind: Reference
    resource: references
    namespacePath: spec.secretRef.namespace
`

func schemaValid(t *testing.T, doc string) bool {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	s, err := c.Compile("../../schemas/operator-refs.schema.json")
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
	r := f.References[0]
	if gvr := r.GVR(); gvr.Group != "tempo.grafana.com" || gvr.Version != "v1alpha1" || gvr.Resource != "tempostacks" {
		t.Errorf("GVR %v", gvr)
	}
	u, err := r.Object("tenant-a", "bhc-x", map[string]string{"k": "v"}, "tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	if u.GetNamespace() != "tenant-a" || u.GetName() != "bhc-x" || u.GetKind() != "TempoStack" || u.GetLabels()["k"] != "v" {
		t.Errorf("object metadata: %v", u.Object)
	}
	secret := u.Object["spec"].(map[string]any)["storage"].(map[string]any)["secret"].(map[string]any)
	if secret["namespace"] != "tenant-b" || secret["name"] != "storage" {
		t.Errorf("the namespace field or the body is wrong: %v", secret)
	}
	// Each object is a copy: the next one does not see the first one's namespace.
	v, _ := r.Object("tenant-a", "bhc-y", nil, "tenant-a")
	if v.Object["spec"].(map[string]any)["storage"].(map[string]any)["secret"].(map[string]any)["namespace"] != "tenant-a" || secret["namespace"] != "tenant-b" {
		t.Error("objects share their body")
	}
	if _, err := f.References[1].Object("a", "b", nil, "c"); err != nil {
		t.Errorf("a reference without a body: %v", err)
	}
}

func TestBadFiles(t *testing.T) {
	head := "apiVersion: bulkhead.io/v1alpha1\nkind: OperatorReferences\nreferences:\n"
	ref := func(fields string) string {
		return head + "  - {name: r, apiVersion: example.com/v1, kind: Thing, resource: things, namespacePath: spec.ref.namespace" + fields + "}\n"
	}
	ok := ref("")
	for _, c := range []struct {
		name, doc, want string
		schemaSees      bool
	}{
		{"wrong kind", "apiVersion: bulkhead.io/v1alpha1\nkind: Flows\nreferences: []\n", "kind OperatorReferences", true},
		{"no references", head + "  []\n", "between 1 and 50 references", true},
		{"unknown field", ref(", namespacepaths: x"), "unknown field", true},
		// Go's decoder would take this for namespacePath; the schema does not.
		{"field in the wrong case", ref(", nameSpacePath: x"), "nameSpacePath", true},
		{"core api version", strings.Replace(ok, "example.com/v1", "v1", 1), "group/version of a custom resource", true},
		{"bad kind", strings.Replace(ok, "kind: Thing", "kind: thing", 1), "must be a Kind", true},
		{"bad resource", strings.Replace(ok, "resource: things", "resource: Things", 1), "plural name", true},
		{"metadata path", strings.Replace(ok, "spec.ref.namespace", "metadata.namespace", 1), "outside metadata", true},
		{"one-part path", strings.Replace(ok, "spec.ref.namespace", "namespace", 1), "dot-separated path", true},
		{"body sets metadata", ref(", body: {metadata: {name: x}}"), "must not set metadata", true},
		{"path through a value", ref(", body: {spec: {ref: plain}}"), "does not fit the body", false},
		{"repeated name", ok + strings.TrimPrefix(ok, head), "used twice", false},
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

func TestTheExampleFileIsValid(t *testing.T) {
	if _, err := Load("../../examples/checker/operator-refs.yaml"); err != nil {
		t.Fatal(err)
	}
}
