package sshconn

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/fileperms"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// hostKeyCallback returns a strict known_hosts-based host key callback and the
// algorithm list pinned to the entry stored for hostport. Pinning prevents a
// server from negotiating a weaker algorithm than the one the key was recorded with.
func hostKeyCallback(h config.Host, hostport string) (ssh.HostKeyCallback, []string, error) {
	path := ResolveKnownHosts(h.KnownHosts)
	if err := fileperms.Check(path); err != nil {
		return nil, nil, err
	}
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, nil, fmt.Errorf("loading known_hosts %q: %w", path, err)
	}
	return cb, knownHostAlgorithms(cb, hostport), nil
}

// knownHostAlgorithms returns the algorithm types stored for hostport in cb.
// It probes the callback with a dummy key; knownhosts responds with a KeyError
// whose Want field lists every known key for that address.
func knownHostAlgorithms(cb ssh.HostKeyCallback, hostport string) []string {
	addr, _ := net.ResolveTCPAddr("tcp", hostport)
	if addr == nil {
		addr = &net.TCPAddr{}
	}
	err := cb(hostport, addr, dummyKey{})
	var kerr *knownhosts.KeyError
	if !errors.As(err, &kerr) || len(kerr.Want) == 0 {
		return nil
	}
	seen := map[string]bool{}
	algos := make([]string, 0, len(kerr.Want))
	for _, kk := range kerr.Want {
		if t := kk.Key.Type(); !seen[t] {
			seen[t] = true
			algos = append(algos, t)
		}
	}
	return algos
}

// dummyKey is a minimal ssh.PublicKey used only to trigger knownhosts.KeyError.
type dummyKey struct{}

func (dummyKey) Type() string                            { return ssh.KeyAlgoED25519 }
func (dummyKey) Marshal() []byte                         { return make([]byte, 51) } // 51 = wire-format for empty ed25519 key
func (dummyKey) Verify(_ []byte, _ *ssh.Signature) error { return fmt.Errorf("dummy key") }

// ResolveKnownHosts converts the "system" sentinel to ~/.ssh/known_hosts.
func ResolveKnownHosts(s string) string {
	if s == "system" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".ssh", "known_hosts")
		}
	}
	return expandPath(s)
}
