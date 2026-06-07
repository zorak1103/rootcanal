package skills

import (
	"embed"
	"fmt"
)

//go:embed *.md
var content embed.FS

// URIPrefix is the scheme and authority for all skill resources.
const URIPrefix = "skill://rootcanal/"

// Meta describes a single skill document in the catalog.
type Meta struct {
	Slug        string
	Name        string
	Description string
}

// URI returns the full resource URI for this skill.
func (m Meta) URI() string { return URIPrefix + m.Slug }

// Catalog lists every embedded skill document.
var Catalog = []Meta{
	{
		Slug:        "session-workflow",
		Name:        "Session Workflow",
		Description: "Open/send/close lifecycle, parallel N-host pattern, still_running continuation, and session inventory via ssh_session_list.",
	},
	{
		Slug:        "output-cleanliness",
		Name:        "Output Cleanliness",
		Description: "Marker-based deterministic completion, default ANSI/echo/prompt stripping, raw mode, exit_code, truncated, and closed_reason values.",
	},
	{
		Slug:        "runonce-vs-session",
		Name:        "RunOnce vs Session",
		Description: "When to use ssh_run_once vs persistent sessions; detach/job_status/job_cancel; nohup fallback; full resource limits table.",
	},
	{
		Slug:        "sftp-and-safety",
		Name:        "SFTP and Safety",
		Description: "SFTP read/write/list tools, allowed prefixes, size limits, atomic write, hard safety rules, and error recovery guidance.",
	},
}

// Read returns the Markdown content for the given slug.
// It returns an error if the slug is not in the catalog.
// The underlying embed.FS error is replaced with a clean user-facing message
// rather than wrapped, to avoid leaking internal file paths to MCP clients.
func Read(slug string) (string, error) {
	b, err := content.ReadFile(slug + ".md")
	if err != nil {
		return "", fmt.Errorf("unknown skill %q", slug)
	}
	return string(b), nil
}
