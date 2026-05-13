package sshconn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"golang.org/x/crypto/ssh"
)

func buildAuthMethods(h config.Host) ([]ssh.AuthMethod, error) {
	switch h.Auth.Type {
	case "key":
		return buildKeyAuth(h.Auth)
	case "agent":
		return buildAgentAuth()
	case "password":
		return buildPasswordAuth(h.Auth)
	default:
		return nil, fmt.Errorf("unknown auth type %q", h.Auth.Type)
	}
}

func buildKeyAuth(a config.Auth) ([]ssh.AuthMethod, error) {
	keyBytes, err := os.ReadFile(expandPath(a.KeyPath))
	if err != nil {
		return nil, fmt.Errorf("reading key %q: %w", a.KeyPath, err)
	}

	var signer ssh.Signer
	if a.PassphraseEnv != "" {
		passphrase := []byte(os.Getenv(a.PassphraseEnv))
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
