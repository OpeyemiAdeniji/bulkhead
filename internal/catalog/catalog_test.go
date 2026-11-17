package catalog

import (
	"regexp"
	"strings"
	"testing"
)

var idPattern = regexp.MustCompile(`^[A-Z]{3}-[0-9]{2}$`)

// prefixes ties the ID prefix to the category, so that an ID tells where a check belongs.
var prefixes = map[Category]string{
	Access: "ACC", Guardrails: "GRD", Admission: "ADM", Network: "NET",
	Resources: "RES", Quota: "QTA", Flows: "FLW", Operators: "XNS",
}

func TestEveryCheckIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range checks {
		if !idPattern.MatchString(c.ID) {
			t.Errorf("%q: the ID must look like ABC-01", c.ID)
		}
		if seen[c.ID] {
			t.Errorf("%s: the ID is used twice", c.ID)
		}
		seen[c.ID] = true
		if want := prefixes[c.Category]; want == "" || !strings.HasPrefix(c.ID, want+"-") {
			t.Errorf("%s: category %q needs the prefix %q", c.ID, c.Category, want)
		}
		switch c.Level {
		case Basic, Hardened, Research:
		default:
			t.Errorf("%s: unknown level %q", c.ID, c.Level)
		}
		switch c.Mode {
		case ReadOnly, Active:
		default:
			t.Errorf("%s: unknown mode %q", c.ID, c.Mode)
		}
		for field, text := range map[string]string{
			"title": c.Title, "proves": c.Proves, "does not prove": c.DoesNotProve, "how": c.How, "fix": c.Fix,
		} {
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s: %s is empty", c.ID, field)
			}
		}
	}
}

func TestEveryBlockedCheckHasAnAllowedPair(t *testing.T) {
	for _, c := range checks {
		switch c.Expect {
		case Blocked:
			p, ok := Get(c.Pair)
			if !ok {
				t.Errorf("%s: pair %q does not exist", c.ID, c.Pair)
				continue
			}
			if p.Expect != Allowed {
				t.Errorf("%s: pair %s must be an allowed check", c.ID, p.ID)
			}
			// A read-only run must be able to run the pair of every read-only check.
			if c.Mode == ReadOnly && p.Mode != ReadOnly {
				t.Errorf("%s is read-only, but its pair %s is active", c.ID, p.ID)
			}
			if levelRank(p.Level) > levelRank(c.Level) {
				t.Errorf("%s (%s) has a pair %s of a higher level (%s), so the pair may not run", c.ID, c.Level, p.ID, p.Level)
			}
		case Allowed:
			if c.Pair != "" {
				t.Errorf("%s: an allowed check has no pair", c.ID)
			}
		default:
			t.Errorf("%s: unknown expected result %q", c.ID, c.Expect)
		}
	}
}

// standalone lists the allowed checks that are not the pair of a blocked check, with the reason.
// Every other allowed check exists to make a blocked result trustworthy.
var standalone = map[string]string{
	"NET-03": "it proves that isolation leaves DNS working, which every tenant needs",
}

func TestEveryAllowedCheckIsThePairOfABlockedCheck(t *testing.T) {
	used := map[string]bool{}
	for _, c := range checks {
		used[c.Pair] = true
	}
	for _, c := range checks {
		if c.Expect != Allowed {
			continue
		}
		_, alone := standalone[c.ID]
		if !used[c.ID] && !alone {
			t.Errorf("%s: no blocked check uses it as its pair; pair it, or list it in standalone with the reason", c.ID)
		}
		if used[c.ID] && alone {
			t.Errorf("%s is listed in standalone, but a blocked check uses it as its pair", c.ID)
		}
	}
}

func TestTextsAvoidContractions(t *testing.T) {
	contraction := regexp.MustCompile(`(?i)\b(can't|don't|doesn't|isn't|won't|it's|aren't|wasn't|didn't|shouldn't)\b`)
	for _, c := range checks {
		for _, text := range []string{c.Title, c.Proves, c.DoesNotProve, c.How, c.Fix} {
			if m := contraction.FindString(text); m != "" {
				t.Errorf("%s: write %q in full", c.ID, m)
			}
		}
	}
}

func TestAllIsSortedAndCopied(t *testing.T) {
	all := All()
	if len(all) != len(checks) {
		t.Fatalf("All returned %d checks, want %d", len(all), len(checks))
	}
	order := map[Category]int{}
	for i, c := range Categories() {
		order[c] = i
	}
	for i := 1; i < len(all); i++ {
		a, b := all[i-1], all[i]
		if order[a.Category] > order[b.Category] || (a.Category == b.Category && a.ID > b.ID) {
			t.Errorf("All is not sorted at %s, %s", a.ID, b.ID)
		}
	}
	all[0].Title = "changed"
	if c, _ := Get(all[0].ID); c.Title == "changed" {
		t.Error("All must return a copy")
	}
}

func TestParse(t *testing.T) {
	if c, err := ParseCategory(" Network "); err != nil || c != Network {
		t.Errorf("ParseCategory: got %q, %v", c, err)
	}
	if _, err := ParseCategory("nope"); err == nil {
		t.Error("ParseCategory accepted an unknown category")
	}
	if l, err := ParseLevel("HARDENED"); err != nil || l != Hardened {
		t.Errorf("ParseLevel: got %q, %v", l, err)
	}
	if _, err := ParseLevel("nope"); err == nil {
		t.Error("ParseLevel accepted an unknown level")
	}
	if _, ok := Get("net-04"); !ok {
		t.Error("Get must ignore case")
	}
}

func levelRank(l Level) int {
	switch l {
	case Basic:
		return 0
	case Hardened:
		return 1
	default:
		return 2
	}
}
