package catalog

import (
	"regexp"
	"strings"
	"testing"
)

var itemID = map[Source]*regexp.Regexp{
	MTB: regexp.MustCompile(`^MTB-PL[12]-(CC|BC)-[A-Z]+-[0-9]+$`),
	CIS: regexp.MustCompile(`^5\.[1-6]\.[0-9]+$`),
}

func TestEveryBenchmarkItemIsWellFormed(t *testing.T) {
	for _, src := range Sources() {
		if src.Name == "" || !strings.HasPrefix(src.URL, "https://") || len(src.Items) == 0 || sourceLabel[src.Source] == "" || sourceNotes[src.Source] == "" {
			t.Errorf("%s: incomplete source", src.Source)
		}
		seen := map[BenchmarkItem]bool{}
		for _, item := range src.Items {
			if !itemID[src.Source].MatchString(item.ID) || item.Title == "" {
				t.Errorf("%s: bad item %+v", src.Source, item)
			}
			if seen[item] {
				t.Errorf("%s: item %s listed twice", src.Source, item.ID)
			}
			seen[item] = true
		}
	}
}

// TestEveryReferenceIsAnItemThatWasRead: a reference must name an item of a listed source with
// its exact title, so a mistyped ID or a title of another item fails.
func TestEveryReferenceIsAnItemThatWasRead(t *testing.T) {
	known := map[Source]map[BenchmarkItem]bool{}
	for _, src := range Sources() {
		known[src.Source] = map[BenchmarkItem]bool{}
		for _, item := range src.Items {
			known[src.Source][item] = true
		}
	}
	for id, refs := range references {
		c, ok := Get(id)
		if !ok {
			t.Errorf("references name the unknown check %s", id)
			continue
		}
		seen := map[Reference]bool{}
		for _, r := range refs {
			if !known[r.Source][BenchmarkItem{r.ID, r.Title}] {
				t.Errorf("%s: %s %s %q is not an item of the source", id, r.Source, r.ID, r.Title)
			}
			switch r.Relation {
			case Tests, Partly:
			case Differs:
				if c.Expect != Blocked {
					t.Errorf("%s: only a check that blocks something can differ from an item that allows it", id)
				}
			default:
				t.Errorf("%s: unknown relation %q", id, r.Relation)
			}
			if seen[r] {
				t.Errorf("%s: %s listed twice", id, r.ID)
			}
			seen[r] = true
		}
	}
}

func TestReferencesAreCopied(t *testing.T) {
	c, _ := Get("ADM-02")
	if len(c.References) == 0 {
		t.Fatal("ADM-02 has no references")
	}
	c.References[0].ID = "changed"
	if again, _ := Get("ADM-02"); again.References[0].ID == "changed" {
		t.Error("Get must return a copy of the references")
	}
}

func TestBenchmarksPage(t *testing.T) {
	md := BenchmarksMarkdown()
	for _, want := range []string{
		"| MTB-PL1-BC-CPI-5 | Block privileged containers | [ADM-02](checks.md#adm-02) (tests) |",
		"| MTB-PL2-BC-OPS-3 | Create Role Bindings | [GRD-05](checks.md#grd-05) (differs) |",
		"| MTB-PL1-BC-HI-4 | Block use of host PID | not tested |",
		"| 5.3.1 | Ensure that the CNI in use supports NetworkPolicies | [NET-04](checks.md#net-04) (tests), [NET-05](checks.md#net-05) (tests) |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the page misses %q", want)
		}
	}
	if !strings.Contains(Markdown(), "- **Benchmarks** ([mapping](benchmarks.md)): Multi-tenancy benchmark MTB-PL1-BC-CPI-5 Block privileged containers (tests); CIS 5.2.2") {
		t.Error("docs/checks.md does not list the benchmarks of ADM-02")
	}
}
