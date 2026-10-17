package screens

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTextPreview(t *testing.T) {
	csv := textPreview([]byte("name,size\nweb,10\napi,20\n"), ".csv")
	if len(csv) != 3 || !strings.Contains(ansi.Strip(csv[1]), "web") {
		t.Fatalf("csv table = %q", csv)
	}
	js := strings.Join(textPreview([]byte(`{"a":1,"password":"hunter2"}`), ".json"), "\n")
	if !strings.Contains(js, "\n") || strings.Contains(js, "hunter2") {
		t.Fatalf("json not pretty-printed/redacted: %q", js)
	}
	bin := textPreview([]byte{0x89, 'P', 'N', 'G', 0, 0, 1}, ".txt")
	if !strings.Contains(ansi.Strip(bin[0]), "Binary") {
		t.Fatalf("binary not detected: %q", bin)
	}
}
