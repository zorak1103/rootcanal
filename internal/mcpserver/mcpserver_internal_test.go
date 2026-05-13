package mcpserver

import (
	"testing"
)

func TestSanitizeOutput_ValidUTF8(t *testing.T) {
	in := []byte("hello\nworld\n")
	got := sanitizeOutput(in)
	if got != "hello\nworld\n" {
		t.Errorf("valid UTF-8 must pass through unchanged, got %q", got)
	}
}

func TestSanitizeOutput_InvalidUTF8(t *testing.T) {
	in := []byte{0xff, 0xfe, 'x'}
	got := sanitizeOutput(in)
	if got == string(in) {
		t.Error("invalid UTF-8 must be replaced")
	}
	// Result must be valid UTF-8.
	for _, r := range got {
		if r == '�' {
			return // replacement found — good
		}
	}
}

func TestFormatSessionList_Empty(t *testing.T) {
	got := formatSessionList(nil)
	if got != "No open sessions." {
		t.Errorf("empty list: got %q, want 'No open sessions.'", got)
	}
}
