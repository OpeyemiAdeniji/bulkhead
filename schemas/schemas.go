// Package schemas embeds the published JSON Schemas of the checker's input files, so the
// checker validates them with exactly the schemas it publishes. Go's JSON decoder matches
// field names without regard to case, so a typo such as "nameSpacePath" would otherwise be
// accepted; the schemas match names exactly.
package schemas

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The schema files.
const (
	Flows        = "flows.schema.json"
	OperatorRefs = "operator-refs.schema.json"
)

var (
	//go:embed flows.schema.json
	flowsSchema []byte
	//go:embed operator-refs.schema.json
	operatorRefsSchema []byte

	files = map[string][]byte{Flows: flowsSchema, OperatorRefs: operatorRefsSchema}
	cache sync.Map // name -> *jsonschema.Schema
)

func compiled(name string) (*jsonschema.Schema, error) {
	if s, ok := cache.Load(name); ok {
		return s.(*jsonschema.Schema), nil
	}
	data, ok := files[name]
	if !ok {
		return nil, fmt.Errorf("no embedded schema %s", name)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err := c.AddResource(name, doc); err != nil {
		return nil, err
	}
	s, err := c.Compile(name)
	if err != nil {
		return nil, err
	}
	cache.Store(name, s)
	return s, nil
}

// Validate checks a JSON document against the named schema. The error lists the first
// problems the schema found, one per line.
func Validate(name string, js []byte) error {
	s, err := compiled(name)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(js))
	if err != nil {
		return err
	}
	if err := s.Validate(inst); err != nil {
		lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
		if len(lines) > 8 {
			lines = append(lines[:8], "  ...")
		}
		return fmt.Errorf("does not match schemas/%s:\n%s", name, strings.Join(lines, "\n"))
	}
	return nil
}
