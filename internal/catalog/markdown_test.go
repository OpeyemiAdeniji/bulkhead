package catalog

import (
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/checks.md from the catalogue")

const docPath = "../../docs/checks.md"

// TestChecksDocIsUpToDate fails when docs/checks.md differs from the catalogue.
// make checks-doc rewrites it.
func TestChecksDocIsUpToDate(t *testing.T) {
	want := Markdown()
	if *update {
		if err := os.WriteFile(docPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("%v: run make checks-doc", err)
	}
	if string(got) != want {
		t.Fatal("docs/checks.md is not up to date with the catalogue: run make checks-doc and commit the result")
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
