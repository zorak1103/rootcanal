package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zorak1103/rootcanal/internal/config"
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

func TestHandleHostCapabilities_Success(t *testing.T) {
	cfg := &config.Config{
		Hosts: map[string]config.Host{
			"nas": {
				Address:             "nas.example.com:22",
				User:                "u",
				Auth:                config.Auth{Type: "agent"},
				SFTPEnabled:         true,
				SFTPAllowedPrefixes: []string{"/data"},
			},
		},
	}
	h := handleHostCapabilities(cfg)
	_, out, err := h(context.Background(), &mcp.CallToolRequest{}, hostCapIn{Host: "nas"})
	if err != nil {
		t.Fatalf("handleHostCapabilities: %v", err)
	}
	if !out.SSH {
		t.Error("want SSH=true")
	}
	if !out.SFTP {
		t.Error("want SFTP=true")
	}
	if len(out.SFTPAllowedPrefixes) != 1 || out.SFTPAllowedPrefixes[0] != "/data" {
		t.Errorf("SFTPAllowedPrefixes = %v, want [/data]", out.SFTPAllowedPrefixes)
	}
}

func TestHandleHostCapabilities_UnknownHost(t *testing.T) {
	cfg := &config.Config{Hosts: map[string]config.Host{}}
	h := handleHostCapabilities(cfg)
	result, _, err := h(context.Background(), &mcp.CallToolRequest{}, hostCapIn{Host: "nohost"})
	if err != nil {
		t.Fatalf("handler returned Go error; want toolErr result: %v", err)
	}
	if !result.IsError {
		t.Error("want IsError=true for unknown host")
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
