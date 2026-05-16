package sshconn

import (
	"context"
	"fmt"
	"net"
	"time"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"golang.org/x/crypto/ssh"
)

// Dialer opens SSH client connections.
type Dialer interface {
	Dial(ctx context.Context, h config.Host, limits config.Limits) (*ssh.Client, error)
}

// ProdDialer dials real SSH connections.
type ProdDialer struct{}

func (ProdDialer) Dial(ctx context.Context, h config.Host, limits config.Limits) (*ssh.Client, error) {
	cfg, err := BuildClientConfig(h)
	if err != nil {
		return nil, fmt.Errorf("building SSH client config: %w", err)
	}

	d := net.Dialer{Timeout: limits.DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", h.Address)
	if err != nil {
		return nil, fmt.Errorf("TCP connection failed: %w", err)
	}

	// Bound the SSH handshake so a slow or malicious server cannot stall the
	// goroutine indefinitely. The deadline is cleared after a successful
	// handshake so long-lived SSH sessions are not killed when it expires.
	if err := conn.SetDeadline(time.Now().Add(limits.DialTimeout)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("setting SSH handshake deadline: %w", err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, h.Address, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("SSH handshake failed: %w", err)
	}

	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = sshConn.Close()
		return nil, fmt.Errorf("clearing handshake deadline: %w", err)
	}

	return ssh.NewClient(sshConn, chans, reqs), nil
}

// BuildClientConfig assembles an *ssh.ClientConfig from a Host definition.
func BuildClientConfig(h config.Host) (*ssh.ClientConfig, error) {
	authMethods, err := buildAuthMethods(h)
	if err != nil {
		return nil, err
	}

	cb, algos, err := hostKeyCallback(h, h.Address)
	if err != nil {
		return nil, err
	}

	return &ssh.ClientConfig{
		User:              h.User,
		Auth:              authMethods,
		HostKeyCallback:   cb,
		HostKeyAlgorithms: algos,
	}, nil
}
