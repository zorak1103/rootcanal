package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/config"
)

func TestHandleListHosts_IncludesSFTPAllowedPrefixes(t *testing.T) {
	cfg := &config.Config{
		Hosts: map[string]config.Host{
			"myhost": {
				Address:             "h.example.com:22",
				User:                "u",
				Auth:                config.Auth{Type: "agent"},
				SFTPEnabled:         true,
				SFTPAllowedPrefixes: []string{"/srv/app", "/var/log"},
			},
		},
	}
	h := handleListHosts(cfg)
	_, out, err := h(context.Background(), &mcp.CallToolRequest{}, struct{}{})
	if err != nil {
		t.Fatalf("handleListHosts: %v", err)
	}
	if len(out.Hosts) != 1 {
		t.Fatalf("expected 1 host, got %d", len(out.Hosts))
	}
	host := out.Hosts[0]
	if len(host.SFTPAllowedPrefixes) != 2 {
		t.Errorf("SFTPAllowedPrefixes = %v, want [/srv/app /var/log]", host.SFTPAllowedPrefixes)
	}
	if host.SFTPAllowedPrefixes[0] != "/srv/app" {
		t.Errorf("first prefix = %q, want /srv/app", host.SFTPAllowedPrefixes[0])
	}
	if host.SFTPAllowedPrefixes[1] != "/var/log" {
		t.Errorf("second prefix = %q, want /var/log", host.SFTPAllowedPrefixes[1])
	}
}

func TestHandleListHosts_PlainHostHasEmptyPrefixes(t *testing.T) {
	cfg := &config.Config{
		Hosts: map[string]config.Host{
			"plain": {Address: "h.example.com:22", User: "u", Auth: config.Auth{Type: "agent"}},
		},
	}
	h := handleListHosts(cfg)
	_, out, err := h(context.Background(), &mcp.CallToolRequest{}, struct{}{})
	if err != nil {
		t.Fatalf("handleListHosts: %v", err)
	}
	if len(out.Hosts[0].SFTPAllowedPrefixes) != 0 {
		t.Errorf("non-SFTP host should have empty SFTPAllowedPrefixes, got %v", out.Hosts[0].SFTPAllowedPrefixes)
	}
}
