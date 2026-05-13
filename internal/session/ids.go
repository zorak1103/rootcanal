package session

import (
	"crypto/rand"
	"encoding/base32"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newSessionID generates a short random session identifier (e.g. "s_A3KF7QX2").
func newSessionID() string {
	var buf [5]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic("session: crypto/rand unavailable: " + err.Error())
	}
	return "s_" + b32.EncodeToString(buf[:])
}
