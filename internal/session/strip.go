package session

import (
	"bytes"
	"regexp"

	"github.com/jimschubert/stripansi"
)

// oscSequence matches OSC sequences (ESC ] ... BEL or ESC ] ... ST).
// The jimschubert/stripansi library mishandles OSC payloads that contain
// spaces or other characters outside its allowed set, so we apply a
// dedicated pass first.
var oscSequence = regexp.MustCompile(`\x1B\][^\x07\x1B]*(?:\x07|\x1B\\)`)

// stripANSI removes ANSI/VT100 escape sequences from b without mutating it.
func stripANSI(b []byte) []byte {
	b = oscSequence.ReplaceAll(b, nil)
	return stripansi.Bytes(b)
}

// cleanOutput strips ANSI escape sequences and normalises \r\n and bare \r to \n.
// It does not mutate b.
func cleanOutput(b []byte) []byte {
	b = oscSequence.ReplaceAll(b, nil)
	b = stripansi.Bytes(b)
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	b = bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
	return b
}
