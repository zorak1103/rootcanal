package session

import (
	"testing"
)

func TestCleanOutput(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello world\n", "hello world\n"},
		{"CSI reset", "\x1B[0mhello\x1B[0m", "hello"},
		{"CSI color 256", "\x1B[38;5;231mtext\x1B[m", "text"},
		{"bracketed paste on", "\x1B[?2004hls\x1B[?2004l", "ls"},
		{"cursor up+erase line", "\x1B[1A\x1B[2K", ""},
		{"OSC title set", "\x1B]0;term title\x07text", "text"},
		{"powerline segment", "\x1B[38;5;31m \x1B[0m", " "},
		{"CR+LF normalised", "line1\r\nline2\r", "line1\nline2\n"},
		{"mixed ANSI+text", "\x1B[32m$ \x1B[0mls\r\n", "$ ls\n"},
		{"no mutation", "\x1B[31mred\x1B[0m", "red"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(cleanOutput([]byte(tc.input)))
			if got != tc.want {
				t.Errorf("cleanOutput(%q)\n  got  %q\n  want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCleanOutput_NoMutation(t *testing.T) {
	input := []byte("\x1B[31mred\x1B[0m")
	original := make([]byte, len(input))
	copy(original, input)
	cleanOutput(input)
	for i, b := range input {
		if b != original[i] {
			t.Errorf("cleanOutput mutated input at byte %d", i)
		}
	}
}

func TestStripANSI_ReturnsNewSlice(t *testing.T) {
	input := []byte("\x1B[32mgreen\x1B[0m")
	out := stripANSI(input)
	if string(out) != "green" {
		t.Errorf("stripANSI = %q, want %q", out, "green")
	}
	// Verify input unchanged
	if string(input) != "\x1B[32mgreen\x1B[0m" {
		t.Error("stripANSI mutated input")
	}
}
