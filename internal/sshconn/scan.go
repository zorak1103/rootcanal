package sshconn

import (
	"context"
	"fmt"
	"net"
	"time"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"golang.org/x/crypto/ssh"
)

// Scanner captures the live host key of an SSH server without verifying it
// against known_hosts. Implement this interface with ProdScanner for
// production use, or a fake in tests.
type Scanner interface {
	ScanHostKey(ctx context.Context, h config.Host, limits config.Limits) (ssh.PublicKey, error)
}

// ProdScanner is the production Scanner implementation.
type ProdScanner struct{}

func (ProdScanner) ScanHostKey(ctx context.Context, h config.Host, limits config.Limits) (ssh.PublicKey, error) {
	return ScanHostKey(ctx, h, limits)
}

// ScanHostKey dials h.Address and returns the host key the server presents,
// WITHOUT verifying it against known_hosts. Host keys are exchanged before
// authentication, so no credentials are used. This is the only place in
// rootcanal that bypasses host-key verification; its sole purpose is to
// surface a fingerprint for human confirmation before re-trusting a rebuilt host.
func ScanHostKey(ctx context.Context, h config.Host, limits config.Limits) (ssh.PublicKey, error) {
	var captured ssh.PublicKey
	captureCB := func(_ string, _ net.Addr, key ssh.PublicKey) error {
		captured = key
		// Return a non-nil error to abort the handshake immediately after key capture.
		return fmt.Errorf("captured")
	}

	cfg := &ssh.ClientConfig{
		User:            h.User,
		HostKeyCallback: captureCB,
	}

	d := net.Dialer{Timeout: limits.DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", h.Address)
	if err != nil {
		return nil, fmt.Errorf("TCP connection failed: %w", err)
	}
	defer conn.Close()

	// Bound the key-exchange phase so a slow server cannot stall indefinitely.
	// Mirrors the handshake deadline in ProdDialer.Dial.
	if limits.DialTimeout > 0 {
		if err := conn.SetDeadline(time.Now().Add(limits.DialTimeout)); err != nil {
			return nil, fmt.Errorf("setting scan deadline: %w", err)
		}
	}

	_, _, _, _ = ssh.NewClientConn(conn, h.Address, cfg)
	if captured != nil {
		return captured, nil
	}
	return nil, fmt.Errorf("scanning host key for %q: server did not present a key", h.Address)
}
