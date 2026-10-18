package screens

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestReadableLogs(t *testing.T) {
	cases := map[string][]string{
		`{"level":"error","msg":"payment failed","order":"o-42","time":"2026-10-18T10:00:00Z","error":"timeout\n  at pay()"}`: {"ERROR payment failed", "order=o-42", "timeout", "at pay()"},
		`2026-10-18T10:00:00.000Z	abc-123	INFO	{"message":"user login","user":{"id":"u-1"}}`:                                  {"INFO  user login", "user.id=u-1"},
		"plain text line": {"plain text line"},
	}
	for in, wants := range cases {
		got := ansi.Strip(strings.Join(readable(in), "\n"))
		for _, w := range wants {
			if !strings.Contains(got, w) {
				t.Errorf("readable(%q) = %q, missing %q", in, got, w)
			}
		}
		if strings.Contains(got, "2026-10-18T10:00:00Z") {
			t.Errorf("timestamp field should be dropped: %q", got)
		}
	}
}
