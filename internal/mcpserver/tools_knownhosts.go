package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zorak1103/rootcanal/internal/hostkeys"
)

type acceptHostKeyIn struct {
	Host                string `json:"host"                          jsonschema:"pre-declared host name from rootcanal config"`
	Confirm             bool   `json:"confirm,omitempty"             jsonschema:"false/omitted: preview the new fingerprint without writing; true: rewrite known_hosts to trust the new key"`
	ExpectedFingerprint string `json:"expected_fingerprint,omitempty" jsonschema:"required when confirm=true: the new_fingerprint from the preview; the entry is only rewritten if the live key still matches this value"`
}

type acceptHostKeyOut struct {
	Host               string                `json:"host"`
	CurrentFingerprint string                `json:"current_fingerprint,omitempty"`
	NewFingerprint     string                `json:"new_fingerprint,omitempty"`
	Changed            bool                  `json:"changed,omitempty"`
	KnownHosts         string                `json:"known_hosts,omitempty"`
	Refreshed          bool                  `json:"refreshed,omitempty"`
	StaleEntries       []hostkeys.StaleEntry `json:"stale_entries,omitempty"`
	Message            string                `json:"message,omitempty"`
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
		switch {
		case len(res.StaleEntries) > 0:
			descs := make([]string, len(res.StaleEntries))
			for i, e := range res.StaleEntries {
				descs[i] = fmt.Sprintf("line %d (%s, %s)", e.Line, e.Type, e.Fingerprint)
			}
			msg = fmt.Sprintf(
				"known_hosts has ambiguous entries for %q that must be resolved before this key can be "+
					"trusted: %s. Remove the stale entries (e.g. \"ssh-keygen -R <hostport> -f <known_hosts "+
					"path>\") and re-run ssh_accept_host_key — confirm=true will fail until then.",
				in.Host, strings.Join(descs, "; "))
		case res.Changed:
			msg = fmt.Sprintf(
				"Host key has changed. Before proceeding, verify the new fingerprint %q for %q "+
					"OUT-OF-BAND — e.g. against the hosting provider's console, a config-management "+
					"record, or by contacting whoever rebuilt the host. This preview scan alone cannot "+
					"rule out a man-in-the-middle. Once independently verified, call ssh_accept_host_key "+
					"again with confirm=true and expected_fingerprint=%q.",
				res.NewFP, in.Host, res.NewFP)
		case res.CurrentFP == "":
			msg = fmt.Sprintf(
				"No known_hosts entry exists yet for %q. Before trusting %q, verify it OUT-OF-BAND — e.g. "+
					"against the hosting provider's console or by contacting whoever provisioned the host. "+
					"Once verified, call ssh_accept_host_key again with confirm=true and expected_fingerprint=%q.",
				in.Host, res.NewFP, res.NewFP)
		default:
			msg = "Host key matches the stored entry; no update is needed."
		}
		out := acceptHostKeyOut{
			Host:               res.Host,
			CurrentFingerprint: res.CurrentFP,
			NewFingerprint:     res.NewFP,
			Changed:            res.Changed,
			KnownHosts:         res.KnownHosts,
			StaleEntries:       res.StaleEntries,
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
