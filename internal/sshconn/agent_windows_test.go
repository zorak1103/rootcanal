//go:build windows

package sshconn

import (
	"errors"
	"net"
	"testing"
)

func TestBuildAgentAuth_Windows_Error(t *testing.T) {
	old := dialAgentPipe
	dialAgentPipe = func() (net.Conn, error) {
		return nil, errors.New("pipe not available")
	}
	t.Cleanup(func() { dialAgentPipe = old })

	_, err := buildAgentAuth()
	if err == nil {
		t.Fatal("expected error when pipe unavailable")
	}
}

func TestBuildAgentAuth_Windows_Success(t *testing.T) {
	// Use an in-memory net.Pipe as the fake agent connection.
	// The auth method stores the Signers callback but does not call it here —
	// so the fake pipe not speaking the SSH agent protocol is fine.
	conn1, conn2 := net.Pipe()
	t.Cleanup(func() { conn1.Close(); conn2.Close() })

	old := dialAgentPipe
	dialAgentPipe = func() (net.Conn, error) { return conn1, nil }
	t.Cleanup(func() { dialAgentPipe = old })

	methods, err := buildAgentAuth()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(methods) == 0 {
		t.Error("expected at least one auth method")
	}
}
