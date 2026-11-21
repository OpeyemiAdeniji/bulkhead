// Package flows reads the file in which teams declare their own expected network flows
// (docs/adr/0019-declared-flows-file.md), for example "tenant alpha may reach the shared
// database, tenant bravo may not". The checker probes each flow as FLW-01 or FLW-02. The file
// matches schemas/flows.schema.json, and Load checks the same rules, with messages that say
// which flow is wrong.
package flows

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"
)

// APIVersion and Kind identify the file. v1alpha1 may change before v1.0 of Bulkhead.
const (
	APIVersion = "bulkhead.io/v1alpha1"
	Kind       = "Flows"
	// MaxFlows bounds a file, so a mistake cannot start thousands of probes.
	MaxFlows = 200
)

// Expectations of a flow.
const (
	Allowed = "allowed"
	Blocked = "blocked"
)

// File is a flows file.
type File struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Flows      []Flow `json:"flows"`
}

// Flow is one declared flow.
type Flow struct {
	// Name identifies the flow in the reports. It is unique within the file.
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	From        string `json:"from"`
	// Via selects a running pod in From by its labels, for example {app: orders}. The probe
	// then runs inside that pod, in an ephemeral container (ADR 0020, --probe-workloads).
	Via    map[string]string `json:"via,omitempty"`
	To     Destination       `json:"to"`
	Expect string            `json:"expect"`
}

// Selector is the label selector of Via, for example "app=orders", or "" without Via.
func (fl Flow) Selector() string {
	if len(fl.Via) == 0 {
		return ""
	}
	return labels.SelectorFromSet(fl.Via).String()
}

// UsesWorkloads reports whether a flow of the file is probed from inside a workload.
func (f *File) UsesWorkloads() bool {
	for _, fl := range f.Flows {
		if len(fl.Via) > 0 {
			return true
		}
	}
	return false
}

// WorkloadNamespaces returns the source namespaces of the flows with Via, sorted and unique.
func (f *File) WorkloadNamespaces() []string {
	var out []string
	for _, fl := range f.Flows {
		if len(fl.Via) > 0 && !slices.Contains(out, fl.From) {
			out = append(out, fl.From)
		}
	}
	slices.Sort(out)
	return out
}

// Destination is a Service (Service, Namespace, Port) or a host (Host, Port).
type Destination struct {
	Service   string `json:"service,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Host      string `json:"host,omitempty"`
	Port      int    `json:"port"`
}

// Address is what the probe connects to. A Service is named relative to the cluster domain
// (name.namespace.svc), which the search path of every pod completes.
func (d Destination) Address() string {
	if d.Service != "" {
		return d.Service + "." + d.Namespace + ".svc"
	}
	return d.Host
}

// String is the destination with its port, for example "postgres.shared.svc:5432".
func (d Destination) String() string {
	return net.JoinHostPort(d.Address(), strconv.Itoa(d.Port))
}

// Label names the destination in reports: the namespace of a Service, or the host.
func (d Destination) Label() string {
	if d.Service != "" {
		return d.Namespace
	}
	return d.Host
}

var (
	dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	hostName = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([-A-Za-z0-9]{0,61}[A-Za-z0-9])?)*$`)
)

// Load reads and checks a flows file in YAML or JSON.
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

// Parse reads and checks the content of a flows file. Unknown fields are refused, so a typo
// such as "expected:" is reported instead of ignored.
func Parse(data []byte) (*File, error) {
	// The YAML converter reads the first document only, so a second one would be ignored.
	if documents(data) > 1 {
		return nil, errors.New("holds more than one document; put every flow in one list")
	}
	js, err := yaml.YAMLToJSON(data)
	if err != nil {
		return nil, fmt.Errorf("not valid YAML or JSON: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(js))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		if strings.Contains(err.Error(), "cannot unmarshal bool") {
			// YAML reads no, yes, on, off, y and n as true or false.
			err = fmt.Errorf("%w (YAML reads a bare no, yes, on, off, y or n as true or false; put such a name in quotes)", err)
		}
		return nil, fmt.Errorf("does not match the flows format (schemas/flows.schema.json): %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("holds more than one document; put every flow in one list")
	}
	if err := f.check(); err != nil {
		return nil, err
	}
	return &f, nil
}

// documents counts the YAML documents that have content: a line "---" at the start of a line
// starts a new one (an indented "---" belongs to a text block).
func documents(data []byte) int {
	n, content := 0, false
	for line := range strings.SplitSeq(string(data), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.TrimRight(line, " \t\r") == "---" || strings.HasPrefix(line, "--- "):
			if content {
				n++
			}
			content = false
		case t != "" && !strings.HasPrefix(t, "#"):
			content = true
		}
	}
	if content {
		n++
	}
	return n
}

// check applies the rules of the schema, and the pair rule, which a schema cannot express.
func (f *File) check() error {
	if f.APIVersion != APIVersion || f.Kind != Kind {
		return fmt.Errorf("apiVersion must be %s and kind %s, not %q and %q", APIVersion, Kind, f.APIVersion, f.Kind)
	}
	if len(f.Flows) == 0 || len(f.Flows) > MaxFlows {
		return fmt.Errorf("must declare between 1 and %d flows, not %d", MaxFlows, len(f.Flows))
	}
	var errs []error
	names := map[string]bool{}
	for i, fl := range f.Flows {
		where := fmt.Sprintf("flow %d (%q)", i+1, fl.Name)
		if !dnsLabel.MatchString(fl.Name) {
			errs = append(errs, fmt.Errorf("%s: name must be lower-case letters, digits and hyphens (a DNS label)", where))
		}
		if names[fl.Name] {
			errs = append(errs, fmt.Errorf("%s: the name is used twice", where))
		}
		names[fl.Name] = true
		if len(fl.Description) > 500 {
			errs = append(errs, fmt.Errorf("%s: the description is longer than 500 characters", where))
		}
		if !dnsLabel.MatchString(fl.From) {
			errs = append(errs, fmt.Errorf("%s: from must be a namespace name", where))
		}
		if fl.Expect != Allowed && fl.Expect != Blocked {
			errs = append(errs, fmt.Errorf("%s: expect must be %q or %q", where, Allowed, Blocked))
		}
		if err := fl.To.check(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", where, err))
		}
		if err := checkVia(fl.Via); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", where, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	// A blocked flow counts only when the destination answered someone, so it needs an
	// allowed flow to the same destination (ADR 0019).
	for i, fl := range f.Flows {
		if fl.Expect == Blocked && len(f.Pairs(fl)) == 0 {
			errs = append(errs, fmt.Errorf("flow %d (%q): a blocked flow needs an allowed flow to the same destination (%s), "+
				"which shows that the destination answers; without it a destination that is down would make this flow pass", i+1, fl.Name, fl.To))
		}
	}
	return errors.Join(errs...)
}

// checkVia checks a label selector: Kubernetes label keys and values, at most ten of them.
func checkVia(via map[string]string) error {
	if via == nil {
		return nil
	}
	if len(via) == 0 || len(via) > 10 {
		return errors.New("via needs between 1 and 10 labels")
	}
	keys := slices.Sorted(maps.Keys(via))
	for _, k := range keys {
		if msgs := validation.IsQualifiedName(k); len(msgs) > 0 {
			return fmt.Errorf("via: %q is not a label key: %s", k, strings.Join(msgs, "; "))
		}
		if msgs := validation.IsValidLabelValue(via[k]); len(msgs) > 0 {
			return fmt.Errorf("via: %q is not a label value: %s", via[k], strings.Join(msgs, "; "))
		}
	}
	return nil
}

func (d Destination) check() error {
	if d.Port < 1 || d.Port > 65535 {
		return fmt.Errorf("to.port must be between 1 and 65535, not %d", d.Port)
	}
	switch {
	case d.Service != "" && d.Host != "":
		return errors.New("to has both service and host; give one")
	case d.Service != "":
		if !dnsLabel.MatchString(d.Service) || !dnsLabel.MatchString(d.Namespace) {
			return errors.New("to.service and to.namespace must both be names (DNS labels)")
		}
	case d.Host != "":
		if d.Namespace != "" {
			return errors.New("to.namespace belongs to a service; a host is reached by its name or address")
		}
		if net.ParseIP(d.Host) == nil && (len(d.Host) > 253 || !hostName.MatchString(d.Host)) {
			return fmt.Errorf("to.host %q is neither an IP address nor a DNS name", d.Host)
		}
	default:
		return errors.New("to needs a service (with namespace) or a host")
	}
	return nil
}

// Pairs returns the allowed flows to the destination of fl.
func (f *File) Pairs(fl Flow) []Flow {
	var out []Flow
	for _, o := range f.Flows {
		if o.Expect == Allowed && o.To.String() == fl.To.String() {
			out = append(out, o)
		}
	}
	return out
}

// CheckSources makes sure every flow starts in a tenant under test: the probe runs from the
// checker's client pod in that namespace.
func (f *File) CheckSources(tenants []string) error {
	var missing []string
	for _, fl := range f.Flows {
		found := false
		for _, t := range tenants {
			found = found || t == fl.From
		}
		if !found {
			missing = append(missing, fmt.Sprintf("%s (from %s)", fl.Name, fl.From))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("flows must start in a tenant under test (--tenant %s): %s", strings.Join(tenants, ", --tenant "), strings.Join(missing, ", "))
	}
	return nil
}
