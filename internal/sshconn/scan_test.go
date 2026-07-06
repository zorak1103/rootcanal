package sshconn

import (
	"context"
	"testing"
	"time"

	"github.com/zorak1103/rootcanal/internal/config"
)

func TestScanHostKey_ReturnsServerKey(t *testing.T) {
	addr, _ := startTestSSHServer(t)
	h := config.Host{Address: addr, User: "u", Auth: config.Auth{Type: "agent"}}
	limits := config.Limits{DialTimeout: 2 * time.Second}

	key, err := ScanHostKey(context.Background(), h, limits)
	if err != nil {
		t.Fatalf("ScanHostKey: %v", err)
	}
	if key == nil {
		t.Fatal("ScanHostKey returned nil key")
	}
	if key.Type() == "" {
		t.Error("returned key has empty Type()")
	}
}

func TestScanHostKey_Unreachable(t *testing.T) {
	h := config.Host{Address: "127.0.0.1:19999", User: "u", Auth: config.Auth{Type: "agent"}}
	limits := config.Limits{DialTimeout: 300 * time.Millisecond}

	_, err := ScanHostKey(context.Background(), h, limits)
	if err == nil {
		t.Fatal("expected error for unreachable host")
	}
}

func TestProdScanner_ImplementsScanner(t *testing.T) {
	addr, _ := startTestSSHServer(t)
	h := config.Host{Address: addr, User: "u", Auth: config.Auth{Type: "agent"}}
	limits := config.Limits{DialTimeout: 2 * time.Second}
	key, err := ProdScanner{}.ScanHostKey(context.Background(), h, limits)
	if err != nil {
		t.Fatalf("ProdScanner.ScanHostKey: %v", err)
	}
	if key == nil {
		t.Fatal("nil key")
	}
}
