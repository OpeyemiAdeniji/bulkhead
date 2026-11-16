package exitcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, OK},
		{"usage", Usagef("bad flag %s", "x"), Usage},
		{"error", Errorf("no cluster"), Error},
		{"wrapped usage", fmt.Errorf("context: %w", Usagef("bad")), Usage},
		{"plain error", errors.New("boom"), Error},
		{"checks failed", &Err{Code: ChecksFailed, Err: errors.New("2 checks failed")}, ChecksFailed},
	}
	for _, c := range cases {
		if got := Of(c.err); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
	if Usagef("bad flag %s", "x").Error() != "bad flag x" {
		t.Error("the message must be kept")
	}
}
