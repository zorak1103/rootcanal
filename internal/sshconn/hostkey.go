package sshconn

import (
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// hostKeyCallback returns a strict known_hosts-based host key callback.
// TODO(M3): pin HostKeyAlgorithms from the known_hosts entry to prevent downgrade.
func hostKeyCallback(h config.Host) (ssh.HostKeyCallback, []string, error) {
	path := resolveKnownHosts(h.KnownHosts)
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, nil, fmt.Errorf("loading known_hosts %q: %w", path, err)
	}
	return cb, nil, nil
}

// resolveKnownHosts converts the "system" sentinel to ~/.ssh/known_hosts.
func resolveKnownHosts(s string) string {
	if s == "system" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".ssh", "known_hosts")
		}
	}
	return expandPath(s)
}
