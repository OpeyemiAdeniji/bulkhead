package catalog

import (
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/checks.md and docs/benchmarks.md from the catalogue")

// TestChecksDocIsUpToDate fails when docs/checks.md or docs/benchmarks.md differs from the
// catalogue. make checks-doc rewrites them.
func TestChecksDocIsUpToDate(t *testing.T) {
	for path, want := range map[string]string{
		"../../docs/checks.md":     Markdown(),
		"../../docs/benchmarks.md": BenchmarksMarkdown(),
	} {
		if *update {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v: run make checks-doc", err)
		}
		if string(got) != want {
			t.Fatalf("%s is not up to date with the catalogue: run make checks-doc and commit the result", path)
		}
	}
}

func TestMarkdownHasEveryCheck(t *testing.T) {
	md := Markdown()
	for _, c := range All() {
		if !strings.Contains(md, "\n### "+c.ID+"\n") {
			t.Errorf("no section for %s", c.ID)
		}
	}
	for _, cat := range Categories() {
		if categoryTitles[cat] == "" {
			t.Errorf("category %s has no title", cat)
		}
	}
}
