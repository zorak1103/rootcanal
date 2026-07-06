package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var hostNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// Validate checks all hosts and limits for correctness and normalises addresses.
// Returns a joined error listing all validation problems found.
func Validate(cfg *Config) error {
	var errs []error

	if len(cfg.Hosts) == 0 {
		return errors.New("config: no hosts defined")
	}

	for name := range cfg.Hosts {
		h := cfg.Hosts[name]
		if !hostNameRe.MatchString(name) {
			errs = append(errs, fmt.Errorf("host %q: name must match [a-z0-9][a-z0-9._-]{0,62}", name))
		}

		addr, err := normalizeAddress(h.Address)
		if err != nil {
			errs = append(errs, fmt.Errorf("host %q: %w", name, err))
		} else {
			h.Address = addr
		}

		if h.User == "" {
			errs = append(errs, fmt.Errorf("host %q: user is required", name))
		}

		if err := validateAuth(name, h.Auth); err != nil {
			errs = append(errs, err)
		}

		if err := validateKnownHosts(name, h.KnownHosts); err != nil {
			errs = append(errs, err)
		}

		if err := validateSFTPConfig(name, &h); err != nil {
			errs = append(errs, err)
		}

		cfg.Hosts[name] = h
	}

	return errors.Join(errs...)
}

func validateAuth(hostName string, a Auth) error {
	switch a.Type {
	case AuthTypeKey:
		if a.KeyPath == "" {
			return fmt.Errorf("host %q auth: key_path is required for type 'key'", hostName)
		}
		if _, err := os.Stat(expandPath(a.KeyPath)); err != nil {
			return fmt.Errorf("host %q auth: key_path %q: %w", hostName, a.KeyPath, err)
		}
	case AuthTypeAgent:
		// no additional requirements
	case AuthTypePassword:
		if a.PasswordEnv == "" {
			return fmt.Errorf("host %q auth: password_env is required for type 'password'", hostName)
		}
	case "":
		return fmt.Errorf("host %q auth: type is required", hostName)
	default:
		return fmt.Errorf("host %q auth: type %q is not valid (use 'key', 'agent', or 'password')", hostName, a.Type)
	}
	return nil
}

func validateKnownHosts(hostName, known string) error {
	if known == "" {
		return fmt.Errorf("host %q: known_hosts is required", hostName)
	}
	if known == "system" {
		return nil // resolved to ~/.ssh/known_hosts at connection time
	}
	if _, err := os.Stat(expandPath(known)); err != nil {
		return fmt.Errorf("host %q: known_hosts %q: %w", hostName, known, err)
	}
	return nil
}

// normalizeAddress ensures the address has a port, defaulting to 22.
func normalizeAddress(addr string) (string, error) {
	if addr == "" {
		return "", errors.New("address is required")
	}
	if strings.ContainsRune(addr, ':') {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return "", fmt.Errorf("invalid address %q: %w", addr, err)
		}
		if host == "" {
			return "", fmt.Errorf("invalid address %q: host part is empty", addr)
		}
		if port == "" {
			port = "22"
		}
		return net.JoinHostPort(host, port), nil
	}
	return net.JoinHostPort(addr, "22"), nil
}

func validateSFTPConfig(hostName string, h *Host) error {
	if len(h.SFTPAllowedPrefixes) > 0 && !h.SFTPEnabled {
		return fmt.Errorf("host %q: sftp_allowed_prefixes requires sftp_enabled: true", hostName)
	}
	for _, prefix := range h.SFTPAllowedPrefixes {
		if !path.IsAbs(prefix) {
			return fmt.Errorf("host %q: sftp_allowed_prefixes entry %q must be an absolute Unix path", hostName, prefix)
		}
		if path.Clean(prefix) != prefix {
			return fmt.Errorf("host %q: sftp_allowed_prefixes entry %q must be a clean path (got %q)", hostName, prefix, path.Clean(prefix))
		}
	}
	return nil
}

// expandPath replaces a leading ~ with the user's home directory.
func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
