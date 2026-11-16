package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OpeyemiAdeniji/bulkhead/internal/catalog"
	"github.com/OpeyemiAdeniji/bulkhead/internal/exitcode"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := run(t, "version")
	if code != exitcode.OK || !strings.HasPrefix(out, "bulkhead ") {
		t.Fatalf("version: code %d, output %q", code, out)
	}
	code, out, _ = run(t, "version", "-o", "json")
	var info map[string]string
	if code != exitcode.OK || json.Unmarshal([]byte(out), &info) != nil || info["version"] == "" || info["goVersion"] == "" {
		t.Fatalf("version -o json: code %d, output %q", code, out)
	}
	if code, _, _ := run(t, "version", "-o", "yaml"); code != exitcode.Usage {
		t.Errorf("an unknown output must be a usage error, got %d", code)
	}
}

func TestCheckListTable(t *testing.T) {
	code, out, _ := run(t, "check", "--list")
	if code != exitcode.OK {
		t.Fatalf("code %d", code)
	}
	for _, c := range catalog.All() {
		if !strings.Contains(out, c.ID) {
			t.Errorf("the table misses %s", c.ID)
		}
	}
}

func TestCheckListJSONWithFilters(t *testing.T) {
	code, out, _ := run(t, "check", "--list", "-o", "json", "--category", "network", "--level", "basic")
	if code != exitcode.OK {
		t.Fatalf("code %d", code)
	}
	var got []catalog.Check
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no checks selected")
	}
	for _, c := range got {
		if c.Category != catalog.Network || c.Level != catalog.Basic {
			t.Errorf("%s does not match the filters", c.ID)
		}
	}
}

func TestCheckUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"check", "--list", "--category", "nope"},
		{"check", "--list", "--level", "nope"},
		{"check", "--list", "-o", "xml"},
		{"check", "--no-such-flag"},
		{"check", "extra-argument"},
		{"check"}, // running is not built yet
		{"no-such-command"},
	} {
		if code, _, errOut := run(t, args...); code != exitcode.Usage || !strings.HasPrefix(errOut, "Error: ") {
			t.Errorf("%v: got code %d and %q, want a usage error", args, code, errOut)
		}
	}
}
