package session

import (
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// sshSession is a narrow interface over *ssh.Session for testability.
type sshSession interface {
	setOutput(w io.Writer)
	StdinPipe() (io.WriteCloser, error)
	RequestPty(term string, h, w int, modes ssh.TerminalModes) error
	Shell() error
	Wait() error
	Close() error
}

// realSSHSession adapts *ssh.Session to sshSession.
type realSSHSession struct{ *ssh.Session }

func (s *realSSHSession) setOutput(w io.Writer) {
	s.Stdout = w
	s.Stderr = w
}

// session holds state for one persistent shell session.
type session struct {
	id          string
	host        string
	sshSess     sshSession
	stdin       io.WriteCloser
	releasePool func() // decrements hostpool refcount
	openedAt    time.Time
	done        chan struct{} // closed when remote shell exits

	sendMu     sync.Mutex // serialises Send calls
	mu         sync.Mutex // guards: closed, lastUsedAt
	closed     bool
	lastUsedAt time.Time

	out *ringBuf
}

func (s *session) isExpired(idleTimeout, maxAge time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	return now.Sub(s.lastUsedAt) >= idleTimeout || now.Sub(s.openedAt) >= maxAge
}
