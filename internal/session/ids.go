package session

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/zorak1103/rootcanal/internal/config"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// randRead is the random-byte source. Replaced in tests to exercise the panic branch.
var randRead = rand.Read

// newSessionID generates a short random session identifier (e.g. "s_A3KF7QX2").
func newSessionID() string {
	var buf [5]byte
	if _, err := randRead(buf[:]); err != nil {
		panic("session: crypto/rand unavailable: " + err.Error())
	}
	return "s_" + b32.EncodeToString(buf[:])
}

// nameRe reuses config.NamePattern (the same pattern host names must match)
// so client-supplied session names and host names stay in sync by
// construction instead of via two hand-copied regexes.
var nameRe = regexp.MustCompile(config.NamePattern)

// newMarkerNonce returns an 8-char base32 random token for in-band markers.
func newMarkerNonce() string {
	var buf [5]byte
	if _, err := randRead(buf[:]); err != nil {
		panic("session: crypto/rand unavailable: " + err.Error())
	}
	return b32.EncodeToString(buf[:])
}

// validateName checks a client-supplied session name.
// Names must not start with "s_" (reserved for auto-generated IDs).
func validateName(name string) error {
	if strings.HasPrefix(name, "s_") {
		return errors.New(`session name must not start with "s_" (reserved prefix)`)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid session name %q: must match ^[a-z0-9][a-z0-9._-]{0,62}$", name)
	}
	return nil
}
