// Package catalog holds the checks of bulkhead check. It is the single source of truth
// for their IDs, pairs and texts (docs/adr/0018-checker-in-this-repository.md).
package catalog

import (
	"fmt"
	"sort"
	"strings"
)

// Category groups checks by what they test.
type Category string

// The categories, in the order they are listed.
const (
	Access     Category = "access"
	Guardrails Category = "guardrails"
	Admission  Category = "admission"
	Network    Category = "network"
	Resources  Category = "resources"
	Quota      Category = "quota"
	Operators  Category = "operators"
)

// Categories returns every category in listing order.
func Categories() []Category {
	return []Category{Access, Guardrails, Admission, Network, Resources, Quota, Operators}
}

// Level says when a check runs.
type Level string

const (
	// Basic checks run by default. Every multi-tenant cluster should pass them.
	Basic Level = "basic"
	// Hardened checks are selected on request. Some platforms allow what they block on purpose.
	Hardened Level = "hardened"
	// Research checks are experimental and off by default, with their own safety rules.
	Research Level = "research"
)

// Mode says whether a check creates objects in the cluster.
type Mode string

const (
	// ReadOnly checks only read, ask for access reviews or send server-side dry runs.
	// They run in --read-only mode.
	ReadOnly Mode = "read-only"
	// Active checks create short-lived, labelled test objects and remove them afterwards.
	Active Mode = "active"
)

// Expect is the result a healthy multi-tenant cluster gives.
type Expect string

const (
	// Allowed means the action must work. These checks prove that the test itself works.
	Allowed Expect = "allowed"
	// Blocked means the action must be refused or fail.
	Blocked Expect = "blocked"
)

// Check is one entry of the catalogue.
type Check struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Category Category `json:"category"`
	Level    Level    `json:"level"`
	Mode     Mode     `json:"mode"`
	Expect   Expect   `json:"expect"`
	// Pair is the ID of the "allowed" check that must pass for a "blocked" result to count,
	// so that a broken test cannot pass by accident. Empty for "allowed" checks.
	Pair         string `json:"pair,omitempty"`
	Proves       string `json:"proves"`
	DoesNotProve string `json:"doesNotProve"`
	How          string `json:"how"`
	Fix          string `json:"fix"`
}

// All returns a copy of every check, sorted by category order and then by ID.
func All() []Check {
	out := make([]Check, len(checks))
	copy(out, checks)
	order := map[Category]int{}
	for i, c := range Categories() {
		order[c] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return order[out[i].Category] < order[out[j].Category]
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns the check with the given ID.
func Get(id string) (Check, bool) {
	for _, c := range checks {
		if strings.EqualFold(c.ID, id) {
			return c, true
		}
	}
	return Check{}, false
}

// ParseCategory turns a name into a Category.
func ParseCategory(s string) (Category, error) {
	for _, c := range Categories() {
		if string(c) == strings.ToLower(strings.TrimSpace(s)) {
			return c, nil
		}
	}
	return "", fmt.Errorf("unknown category %q (known: %s)", s, joinCategories())
}

// ParseLevel turns a name into a Level.
func ParseLevel(s string) (Level, error) {
	switch l := Level(strings.ToLower(strings.TrimSpace(s))); l {
	case Basic, Hardened, Research:
		return l, nil
	}
	return "", fmt.Errorf("unknown level %q (known: basic, hardened, research)", s)
}

func joinCategories() string {
	names := make([]string, 0, len(Categories()))
	for _, c := range Categories() {
		names = append(names, string(c))
	}
	return strings.Join(names, ", ")
}
