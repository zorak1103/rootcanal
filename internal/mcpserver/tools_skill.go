package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/mcpserver/skills"
)

// ---- get_skill ----

type getSkillIn struct {
	Action string `json:"action"          jsonschema:"action to perform; list returns all skill slugs, read returns a skill body"`
	Skill  string `json:"skill,omitempty" jsonschema:"slug of the skill to read; required when action=read"`
}

type skillEntry struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	URI         string `json:"uri"`
}

func handleGetSkill() func(context.Context, *mcp.CallToolRequest, getSkillIn) (*mcp.CallToolResult, any, error) {
	// Pre-sort catalog entries and build valid-slug string once at construction time.
	entries := make([]skillEntry, 0, len(skills.Catalog))
	for _, m := range skills.Catalog {
		entries = append(entries, skillEntry{
			Slug:        m.Slug,
			Name:        m.Name,
			Description: m.Description,
			URI:         m.URI(),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Slug < entries[j].Slug })
	slugList := make([]string, len(entries))
	for i, e := range entries {
		slugList[i] = e.Slug
	}
	validSlugs := strings.Join(slugList, ", ")

	return func(_ context.Context, _ *mcp.CallToolRequest, in getSkillIn) (*mcp.CallToolResult, any, error) {
		switch in.Action {
		case "list":
			b, err := json.Marshal(entries)
			if err != nil {
				return toolErr(fmt.Errorf("marshal response: %w", err))
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
			}, entries, nil

		case "read":
			if in.Skill == "" {
				return toolErr(fmt.Errorf("skill parameter required for action=read"))
			}
			body, err := skills.Read(in.Skill)
			if err != nil {
				return toolErr(fmt.Errorf("unknown skill %q; valid: %s", in.Skill, validSlugs))
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: body}},
			}, nil, nil

		default:
			return toolErr(fmt.Errorf("unknown action %q; use list or read", in.Action))
		}
	}
}
