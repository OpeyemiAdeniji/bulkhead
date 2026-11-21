// Package operatorrefs reads the file that lists the cross-namespace references of installed
// operators that XNS-01 and XNS-02 test at admission (docs/adr/0021-operator-reference-checks.md).
// An entry names a custom resource and the path of a namespace field in it, for example the
// namespace of a Secret reference. The file matches schemas/operator-refs.schema.json.
package operatorrefs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/OpeyemiAdeniji/bulkhead/schemas"
)

// APIVersion and Kind identify the file. v1alpha1 may change before v1.0 of Bulkhead.
const (
	APIVersion = "bulkhead.io/v1alpha1"
	Kind       = "OperatorReferences"
	// MaxReferences bounds a file.
	MaxReferences = 50
)

// File is an operator references file.
type File struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	References []Reference `json:"references"`
}

// Reference is one namespace field of one custom resource.
type Reference struct {
	Name string `json:"name"`
	// APIVersion and Kind of the custom resource, for example tempo.grafana.com/v1alpha1, TempoStack.
	APIVersion string `json:"apiVersion"`
	ResKind    string `json:"kind"`
	// Resource is the plural name of the resource, for example tempostacks.
	Resource string `json:"resource"`
	// NamespacePath is the dot-separated path of the namespace field, for example
	// spec.storage.secret.namespace.
	NamespacePath string `json:"namespacePath"`
	// Body is the smallest object the resource's schema accepts, without apiVersion, kind and
	// metadata, which the checker sets.
	Body map[string]any `json:"body,omitempty"`
}

var (
	dnsLabel   = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	apiVersion = regexp.MustCompile(`^([a-z0-9]([-a-z0-9.]*[a-z0-9])?/)?v[0-9]+((alpha|beta)[0-9]+)?$`)
	kindName   = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{0,62}$`)
	plural     = regexp.MustCompile(`^[a-z][a-z0-9]{0,62}$`)
	fieldPath  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(\.[A-Za-z][A-Za-z0-9]*)+$`)
)

// GVR is the resource the checker creates, as a dry run.
func (r Reference) GVR() schema.GroupVersionResource {
	gv, _ := schema.ParseGroupVersion(r.APIVersion)
	return gv.WithResource(r.Resource)
}

// Object returns the custom resource in namespace ns, named name with the labels, whose
// namespace field points to target.
func (r Reference) Object(ns, name string, labels map[string]string, target string) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{Object: map[string]any{}}
	if r.Body != nil {
		u.Object = runtimeCopy(r.Body)
	}
	u.SetAPIVersion(r.APIVersion)
	u.SetKind(r.ResKind)
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetLabels(labels)
	if err := unstructured.SetNestedField(u.Object, target, strings.Split(r.NamespacePath, ".")...); err != nil {
		return nil, fmt.Errorf("set %s: %w", r.NamespacePath, err)
	}
	return u, nil
}

// runtimeCopy deep-copies a JSON object, so every Object is independent of the file.
func runtimeCopy(m map[string]any) map[string]any {
	data, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return out
}

// Load reads and checks a references file in YAML or JSON.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Parse reads and checks the content of a references file. Unknown fields are refused.
func Parse(data []byte) (*File, error) {
	js, err := yaml.YAMLToJSON(data)
	if err != nil {
		return nil, fmt.Errorf("not valid YAML or JSON: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(js))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("does not match the operator references format (schemas/operator-refs.schema.json): %w", err)
	}
	if err := f.check(); err != nil {
		return nil, err
	}
	// The schema matches field names exactly; the decoder above does not.
	if err := schemas.Validate(schemas.OperatorRefs, js); err != nil {
		return nil, err
	}
	return &f, nil
}

func (f *File) check() error {
	if f.APIVersion != APIVersion || f.Kind != Kind {
		return fmt.Errorf("apiVersion must be %s and kind %s, not %q and %q", APIVersion, Kind, f.APIVersion, f.Kind)
	}
	if len(f.References) == 0 || len(f.References) > MaxReferences {
		return fmt.Errorf("must list between 1 and %d references, not %d", MaxReferences, len(f.References))
	}
	var errs []error
	names := map[string]bool{}
	for i, r := range f.References {
		where := fmt.Sprintf("reference %d (%q)", i+1, r.Name)
		bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(where+": "+format, a...)) }
		if !dnsLabel.MatchString(r.Name) {
			bad("name must be lower-case letters, digits and hyphens (a DNS label)")
		}
		if names[r.Name] {
			bad("the name is used twice")
		}
		names[r.Name] = true
		if !apiVersion.MatchString(r.APIVersion) || !strings.Contains(r.APIVersion, "/") {
			bad("apiVersion must be group/version of a custom resource, for example example.com/v1")
		}
		if !kindName.MatchString(r.ResKind) {
			bad("kind must be a Kind, for example TempoStack")
		}
		if !plural.MatchString(r.Resource) {
			bad("resource must be the plural name of the resource, in lower case, for example tempostacks")
		}
		if !fieldPath.MatchString(r.NamespacePath) || strings.HasPrefix(r.NamespacePath, "metadata.") {
			bad("namespacePath must be a dot-separated path outside metadata, for example spec.secretRef.namespace")
		}
		for _, k := range []string{"apiVersion", "kind", "metadata"} {
			if _, ok := r.Body[k]; ok {
				bad("body must not set %s; the checker sets it", k)
			}
		}
		if fieldPath.MatchString(r.NamespacePath) {
			if _, err := r.Object("ns", "name", nil, "target"); err != nil {
				bad("namespacePath %s does not fit the body: %v", r.NamespacePath, err)
			}
		}
	}
	return errors.Join(errs...)
}
