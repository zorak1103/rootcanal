package sshconn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zorak1103/rootcanal/internal/config"
	"github.com/zorak1103/rootcanal/internal/fileperms"
	"golang.org/x/crypto/ssh"
)

func buildAuthMethods(h *config.Host) ([]ssh.AuthMethod, error) {
	switch h.Auth.Type {
	case config.AuthTypeKey:
		return buildKeyAuth(h.Auth)
	case config.AuthTypeAgent:
		return buildAgentAuth()
	case config.AuthTypePassword:
		return buildPasswordAuth(h.Auth)
	default:
		return nil, fmt.Errorf("unknown auth type %q", h.Auth.Type)
	}
}

func buildKeyAuth(a config.Auth) ([]ssh.AuthMethod, error) {
	keyPath := expandPath(a.KeyPath)
	if err := fileperms.Check(keyPath); err != nil {
		return nil, err
	}

	keyBytes, err := os.ReadFile(keyPath) // #nosec G304 — path is operator-controlled config, same false-positive class as GOSEC-001
	if err != nil {
		return nil, fmt.Errorf("reading key %q: %w", a.KeyPath, err)
	}
	defer zero(keyBytes)

	var signer ssh.Signer
	if a.PassphraseEnv != "" {
		passphrase := []byte(os.Getenv(a.PassphraseEnv))
		defer zero(passphrase)
		signer, err = ssh.ParsePrivateKeyWithPassphrase(keyBytes, passphrase)
	} else {
		signer, err = ssh.ParsePrivateKey(keyBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("parsing private key %q: %w", a.KeyPath, err)
	}

	return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
}

func buildPasswordAuth(a config.Auth) ([]ssh.AuthMethod, error) {
	pw := os.Getenv(a.PasswordEnv)
	if pw == "" {
		return nil, fmt.Errorf("env var %q is empty or unset", a.PasswordEnv)
	}
	// pw is a Go string (immutable); it cannot be zeroed. The env var is kept
	// in place so repeated dials can re-read it.
	return []ssh.AuthMethod{ssh.Password(pw)}, nil
}

// expandPath replaces a leading ~/ with the user's home directory.
func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// zero overwrites b with zeroes to limit the time sensitive bytes live in memory.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
