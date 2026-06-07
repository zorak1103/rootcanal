package hostkeys

import (
	"context"
	"errors"
	"fmt"
	"net"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/sshconn"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Scanner captures the live host key of an SSH server without verifying it.
// sshconn.ProdScanner satisfies this interface.
type Scanner interface {
	ScanHostKey(ctx context.Context, h config.Host, limits config.Limits) (ssh.PublicKey, error)
}

// InspectResult is returned by Inspect.
type InspectResult struct {
	Host       string `json:"host"`
	CurrentFP  string `json:"current_fingerprint"` // SHA256 of stored key matching live key type; "" if none
	NewFP      string `json:"new_fingerprint"`     // SHA256 of freshly scanned live key
	Changed    bool   `json:"changed"`
	KnownHosts string `json:"known_hosts"` // resolved path (shown to operator)
}

// AcceptResult is returned by Accept.
type AcceptResult struct {
	Host       string `json:"host"`
	NewFP      string `json:"new_fingerprint"`
	KnownHosts string `json:"known_hosts"`
	Refreshed  bool   `json:"refreshed"` // false if key was already current; no write occurred
}

// Refresher updates a host's known_hosts entry after a server rebuild.
type Refresher interface {
	Inspect(ctx context.Context, host string) (InspectResult, error)
	Accept(ctx context.Context, host, expectedFingerprint string) (AcceptResult, error)
}

type prodRefresher struct {
	cfg     *config.Config
	scanner Scanner
}

// New returns a production Refresher.
// Pass sshconn.ProdScanner{} as scanner in production.
func New(cfg *config.Config, scanner Scanner) Refresher {
	return &prodRefresher{cfg: cfg, scanner: scanner}
}

func (r *prodRefresher) resolveHost(host string) (config.Host, string, error) {
	h, ok := r.cfg.Hosts[host]
	if !ok {
		return config.Host{}, "", config.UnknownHostError(host)
	}
	if !h.AllowKnownHostsUpdate {
		return config.Host{}, "", fmt.Errorf(
			"host %q does not allow known_hosts updates: "+
				"set allow_known_hosts_update: true in config", host)
	}
	return h, sshconn.ResolveKnownHosts(h.KnownHosts), nil
}

// Inspect scans the host's live key and compares it against the stored entry.
// It does NOT modify known_hosts.
func (r *prodRefresher) Inspect(ctx context.Context, host string) (InspectResult, error) {
	h, path, err := r.resolveHost(host)
	if err != nil {
		return InspectResult{}, err
	}
	liveKey, err := r.scanner.ScanHostKey(ctx, h, r.cfg.Limits)
	if err != nil {
		return InspectResult{}, fmt.Errorf("scanning host key: %w", err)
	}
	newFP := ssh.FingerprintSHA256(liveKey)
	currentFP := storedFingerprint(path, h.Address, liveKey.Type())
	return InspectResult{
		Host:       host,
		CurrentFP:  currentFP,
		NewFP:      newFP,
		Changed:    currentFP != newFP,
		KnownHosts: path,
	}, nil
}

// Accept re-scans the host's live key, verifies it matches expectedFingerprint
// (from a prior Inspect call), and atomically rewrites the known_hosts entry.
func (r *prodRefresher) Accept(ctx context.Context, host, expectedFingerprint string) (AcceptResult, error) {
	if expectedFingerprint == "" {
		return AcceptResult{}, fmt.Errorf(
			"expected_fingerprint is required for confirm: " +
				"call ssh_accept_host_key without confirm=true first to get the fingerprint")
	}
	h, path, err := r.resolveHost(host)
	if err != nil {
		return AcceptResult{}, err
	}
	liveKey, err := r.scanner.ScanHostKey(ctx, h, r.cfg.Limits)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("scanning host key: %w", err)
	}
	newFP := ssh.FingerprintSHA256(liveKey)
	if newFP != expectedFingerprint {
		return AcceptResult{}, fmt.Errorf(
			"host key changed since preview: expected %s but live key is %s — "+
				"call ssh_accept_host_key without confirm=true to re-inspect",
			expectedFingerprint, newFP)
	}
	currentFP := storedFingerprint(path, h.Address, liveKey.Type())
	if currentFP == newFP {
		return AcceptResult{Host: host, NewFP: newFP, KnownHosts: path, Refreshed: false}, nil
	}
	if err := rewriteKnownHostsEntry(path, h.Address, liveKey); err != nil {
		return AcceptResult{}, fmt.Errorf("rewriting known_hosts: %w", err)
	}
	return AcceptResult{Host: host, NewFP: newFP, KnownHosts: path, Refreshed: true}, nil
}

// storedFingerprint returns the SHA256 fingerprint of the stored key of keyType
// for hostport in path. Returns "" if no entry of that type exists.
func storedFingerprint(path, hostport, keyType string) string {
	cb, err := knownhosts.New(path)
	if err != nil {
		return ""
	}
	addr, _ := net.ResolveTCPAddr("tcp", hostport)
	if addr == nil {
		addr = &net.TCPAddr{}
	}
	probeErr := cb(hostport, addr, probeKey{})
	var kerr *knownhosts.KeyError
	if !errors.As(probeErr, &kerr) {
		return ""
	}
	for _, kk := range kerr.Want {
		if kk.Key.Type() == keyType {
			return ssh.FingerprintSHA256(kk.Key)
		}
	}
	return ""
}

// probeKey is a minimal ssh.PublicKey used only to trigger knownhosts.KeyError.
// It matches the same pattern used in internal/sshconn/hostkey.go.
type probeKey struct{}

func (probeKey) Type() string                            { return "ecdsa-sha2-nistp256" }
func (probeKey) Marshal() []byte                         { return make([]byte, 51) }
func (probeKey) Verify(_ []byte, _ *ssh.Signature) error { return fmt.Errorf("probe key") }
