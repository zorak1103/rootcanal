package sshconn

import (
	"context"
	"fmt"
	"net"

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
		return nil, fmt.Errorf("building client config for %q: %w", h.Address, err)
	}

	d := net.Dialer{Timeout: limits.DialTimeout}
	conn, err := d.DialContext(ctx, "tcp", h.Address)
	if err != nil {
		return nil, fmt.Errorf("TCP dial %s: %w", h.Address, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, h.Address, cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH handshake %s: %w", h.Address, err)
	}

	return ssh.NewClient(sshConn, chans, reqs), nil
}

// BuildClientConfig assembles an *ssh.ClientConfig from a Host definition.
func BuildClientConfig(h config.Host) (*ssh.ClientConfig, error) {
	authMethods, err := buildAuthMethods(h)
	if err != nil {
		return nil, err
	}

	cb, algos, err := hostKeyCallback(h)
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
