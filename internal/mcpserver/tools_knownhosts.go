package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/hostkeys"
)

type acceptHostKeyIn struct {
	Host                string `json:"host"                          jsonschema:"pre-declared host name from rootcanal config"`
	Confirm             bool   `json:"confirm,omitempty"             jsonschema:"false/omitted: preview the new fingerprint without writing; true: rewrite known_hosts to trust the new key"`
	ExpectedFingerprint string `json:"expected_fingerprint,omitempty" jsonschema:"required when confirm=true: the new_fingerprint from the preview; the entry is only rewritten if the live key still matches this value"`
}

type acceptHostKeyOut struct {
	Host               string `json:"host"`
	CurrentFingerprint string `json:"current_fingerprint,omitempty"`
	NewFingerprint     string `json:"new_fingerprint,omitempty"`
	Changed            bool   `json:"changed,omitempty"`
	KnownHosts         string `json:"known_hosts,omitempty"`
	Refreshed          bool   `json:"refreshed,omitempty"`
	Message            string `json:"message,omitempty"`
}

func handleAcceptHostKey(hk hostkeys.Refresher) func(context.Context, *mcp.CallToolRequest, acceptHostKeyIn) (*mcp.CallToolResult, acceptHostKeyOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in acceptHostKeyIn) (*mcp.CallToolResult, acceptHostKeyOut, error) {
		if in.Confirm {
			res, err := hk.Accept(ctx, in.Host, in.ExpectedFingerprint)
			if err != nil {
				r, _, _ := toolErr(err)
				return r, acceptHostKeyOut{}, nil
			}
			out := acceptHostKeyOut{
				Host:           res.Host,
				NewFingerprint: res.NewFP,
				KnownHosts:     res.KnownHosts,
				Refreshed:      res.Refreshed,
			}
			b, err := json.Marshal(out)
			if err != nil {
				r, _, _ := toolErr(fmt.Errorf("marshal response: %w", err))
				return r, out, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, out, nil
		}

		res, err := hk.Inspect(ctx, in.Host)
		if err != nil {
			r, _, _ := toolErr(err)
			return r, acceptHostKeyOut{}, nil
		}
		var msg string
		if res.Changed {
			msg = fmt.Sprintf(
				"Host key has changed. Verify with the server operator that %q was legitimately "+
					"rebuilt, then call ssh_accept_host_key again with confirm=true and "+
					"expected_fingerprint=%q.",
				in.Host, res.NewFP)
		} else {
			msg = "Host key matches the stored entry; no update is needed."
		}
		out := acceptHostKeyOut{
			Host:               res.Host,
			CurrentFingerprint: res.CurrentFP,
			NewFingerprint:     res.NewFP,
			Changed:            res.Changed,
			KnownHosts:         res.KnownHosts,
			Message:            msg,
		}
		b, err := json.Marshal(out)
		if err != nil {
			r, _, _ := toolErr(fmt.Errorf("marshal response: %w", err))
			return r, out, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, out, nil
	}
}
