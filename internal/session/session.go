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

// inflight tracks a running command whose exit-marker has not been received yet.
type inflight struct {
	nonce string
	input string // original user input, used for echo stripping
}

// session holds state for one persistent shell session.
type session struct {
	id          string
	name        string // optional client-supplied name
	host        string
	sshSess     sshSession
	stdin       io.WriteCloser
	releasePool func() // decrements hostpool refcount
	openedAt    time.Time
	done        chan struct{} // closed when remote shell exits

	sendMu sync.Mutex // serialises Send calls
	mu     sync.Mutex // guards: closed, closedReason, lastUsedAt, inflight, lastExitCode

	closed       bool
	closedReason string
	lastUsedAt   time.Time
	inflight     *inflight
	lastExitCode *int

	out *ringBuf
}

func (s *session) isExpired(idleTimeout, maxAge time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	return now.Sub(s.lastUsedAt) >= idleTimeout || now.Sub(s.openedAt) >= maxAge
}
