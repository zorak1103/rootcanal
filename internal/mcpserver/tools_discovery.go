package mcpserver

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/config"
)

// ---- ssh_list_hosts ----

type hostEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Address     string `json:"address"`
	User        string `json:"user"`
	AuthType    string `json:"auth_type"`
	SFTPEnabled bool   `json:"sftp_enabled"`
}

type listHostsOut struct {
	Hosts []hostEntry `json:"hosts"`
}

func handleListHosts(cfg *config.Config) func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, listHostsOut, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listHostsOut, error) {
		entries := make([]hostEntry, 0, len(cfg.Hosts))
		for name, h := range cfg.Hosts {
			entries = append(entries, hostEntry{
				Name:        name,
				Description: h.Description,
				Address:     h.Address,
				User:        h.User,
				AuthType:    h.Auth.Type,
				SFTPEnabled: h.SFTPEnabled,
			})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		out := listHostsOut{Hosts: entries}
		b, _ := json.Marshal(out)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
		}, out, nil
	}
}

// ---- ssh_host_capabilities ----

type hostCapIn struct {
	Host string `json:"host" jsonschema:"pre-declared host name"`
}

type hostCapOut struct {
	SSH                 bool     `json:"ssh"`
	SFTP                bool     `json:"sftp"`
	SFTPAllowedPrefixes []string `json:"sftp_allowed_prefixes"`
	IdleTimeoutMs       int64    `json:"idle_timeout_ms"`
	MaxSessionAgeMs     int64    `json:"max_session_age_ms"`
	Term                string   `json:"term,omitempty"`
	CleanOutput         *bool    `json:"clean_output,omitempty"`
}

func handleHostCapabilities(cfg *config.Config) func(context.Context, *mcp.CallToolRequest, hostCapIn) (*mcp.CallToolResult, hostCapOut, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, in hostCapIn) (*mcp.CallToolResult, hostCapOut, error) {
		caps, err := cfg.Capabilities(in.Host)
		if err != nil {
			r, _, _ := toolErr(err)
			return r, hostCapOut{}, nil
		}
		out := hostCapOut{
			SSH:                 caps.SSH,
			SFTP:                caps.SFTP,
			SFTPAllowedPrefixes: caps.SFTPAllowedPrefixes,
			IdleTimeoutMs:       caps.IdleTimeoutMs,
			MaxSessionAgeMs:     caps.MaxSessionAgeMs,
			Term:                caps.Term,
			CleanOutput:         caps.CleanOutput,
		}
		b, _ := json.Marshal(out)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
		}, out, nil
	}
}
