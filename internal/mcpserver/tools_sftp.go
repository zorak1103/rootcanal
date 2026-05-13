package mcpserver

import (
	"context"
	"encoding/base64"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/sftpops"
)

// ---- sftp_read ----

type sftpReadIn struct {
	Host     string `json:"host"               jsonschema:"pre-declared host name from rootcanal config"`
	Path     string `json:"path"               jsonschema:"absolute path of the file on the remote host"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"maximum bytes to read; defaults to the server limit"`
}

type sftpReadOut struct {
	Content string `json:"content"           jsonschema:"file content as UTF-8 text, or base64 if binary=true"`
	Binary  bool   `json:"binary,omitempty"  jsonschema:"true when content is base64-encoded binary data"`
	Size    int    `json:"size"              jsonschema:"number of bytes read"`
}

func handleSFTPRead(ops sftpops.Ops) func(context.Context, *mcp.CallToolRequest, sftpReadIn) (*mcp.CallToolResult, sftpReadOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in sftpReadIn) (*mcp.CallToolResult, sftpReadOut, error) {
		data, isBinary, err := ops.Read(ctx, in.Host, in.Path, in.MaxBytes)
		if err != nil {
			r, _, _ := toolErr(err)
			return r, sftpReadOut{}, nil
		}

		var content string
		if isBinary {
			content = base64.StdEncoding.EncodeToString(data)
		} else {
			content = sanitizeOutput(data)
		}

		out := sftpReadOut{Content: content, Binary: isBinary, Size: len(data)}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: content}},
		}, out, nil
	}
}

// ---- sftp_write ----

type sftpWriteIn struct {
	Host    string `json:"host"             jsonschema:"pre-declared host name from rootcanal config"`
	Path    string `json:"path"             jsonschema:"absolute path of the file to write on the remote host"`
	Content string `json:"content"          jsonschema:"file content; base64-encode binary data and set binary=true"`
	Binary  bool   `json:"binary,omitempty" jsonschema:"set to true when content is base64-encoded"`
	Mode    string `json:"mode,omitempty"   jsonschema:"Unix file permissions in octal notation e.g. '0644' or '755'; omit to keep default"`
}

func handleSFTPWrite(ops sftpops.Ops) func(context.Context, *mcp.CallToolRequest, sftpWriteIn) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in sftpWriteIn) (*mcp.CallToolResult, any, error) {
		var content []byte
		if in.Binary {
			decoded, err := base64.StdEncoding.DecodeString(in.Content)
			if err != nil {
				return toolErr(fmt.Errorf("base64 decode: %w", err))
			}
			content = decoded
		} else {
			content = []byte(in.Content)
		}

		var mode fs.FileMode
		if in.Mode != "" {
			n, err := strconv.ParseUint(strings.TrimPrefix(in.Mode, "0"), 8, 32)
			if err != nil {
				return toolErr(fmt.Errorf("invalid mode %q: use octal notation such as '0644' or '755'", in.Mode))
			}
			mode = fs.FileMode(n)
		}

		if err := ops.Write(ctx, in.Host, in.Path, content, mode); err != nil {
			return toolErr(err)
		}

		msg := fmt.Sprintf("Written %d bytes to %s:%s", len(content), in.Host, in.Path)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		}, nil, nil
	}
}

// ---- sftp_list ----

type sftpListIn struct {
	Host string `json:"host" jsonschema:"pre-declared host name from rootcanal config"`
	Path string `json:"path" jsonschema:"absolute path of the directory to list on the remote host"`
}

type sftpListOut struct {
	Path    string         `json:"path"`
	Entries []entrySummary `json:"entries"`
}

type entrySummary struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime string `json:"mod_time"`
	IsDir   bool   `json:"is_dir,omitempty"`
}

func handleSFTPList(ops sftpops.Ops) func(context.Context, *mcp.CallToolRequest, sftpListIn) (*mcp.CallToolResult, sftpListOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in sftpListIn) (*mcp.CallToolResult, sftpListOut, error) {
		entries, err := ops.List(ctx, in.Host, in.Path)
		if err != nil {
			r, _, _ := toolErr(err)
			return r, sftpListOut{}, nil
		}

		summaries := make([]entrySummary, len(entries))
		for i, e := range entries {
			summaries[i] = entrySummary{
				Name:    e.Name,
				Size:    e.Size,
				Mode:    e.Mode.String(),
				ModTime: e.ModTime.UTC().Format(time.RFC3339),
				IsDir:   e.IsDir,
			}
		}

		out := sftpListOut{Path: in.Path, Entries: summaries}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: formatEntries(in.Path, summaries)}},
		}, out, nil
	}
}

func formatEntries(path string, entries []entrySummary) string {
	if len(entries) == 0 {
		return path + ": (empty directory)"
	}
	var b strings.Builder
	b.WriteString(path + ":\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "  %s  %8d  %s  %s\n", e.Mode, e.Size, e.ModTime[:10], e.Name)
	}
	return b.String()
}
