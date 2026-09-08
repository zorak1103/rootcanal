package mcpserver

import (
	"strings"
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

func TestFormatEntries_EmptyDirectory(t *testing.T) {
	got := formatEntries("/tmp/empty", nil)
	want := "/tmp/empty: (empty directory)"
	if got != want {
		t.Errorf("formatEntries(empty) = %q, want %q", got, want)
	}
}

func TestFormatEntries_NonEmptyDirectory(t *testing.T) {
	got := formatEntries("/tmp/dir", []entrySummary{
		{Name: "readme.txt", Size: 42, Mode: "-rw-r--r--", ModTime: "2026-01-01T00:00:00Z"},
	})
	if strings.Contains(got, "(empty directory)") {
		t.Errorf("formatEntries(non-empty) = %q, must not report an empty directory", got)
	}
	if !strings.Contains(got, "readme.txt") {
		t.Errorf("formatEntries(non-empty) = %q, want to contain the entry name", got)
	}
}
