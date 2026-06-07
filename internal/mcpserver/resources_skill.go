package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gitlab.com/zorak1103/rootcanal/internal/mcpserver/skills"
)

// registerSkillResources adds one skill:// resource per entry in skills.Catalog.
// The SDK automatically advertises the resources capability when AddResource is called.
func registerSkillResources(srv *mcp.Server) {
	for _, m := range skills.Catalog {
		srv.AddResource(&mcp.Resource{
			URI:         m.URI(),
			Name:        m.Name,
			Description: m.Description,
			MIMEType:    "text/markdown",
		}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			body, err := skills.Read(m.Slug)
			if err != nil {
				return nil, mcp.ResourceNotFoundError(req.Params.URI)
			}
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{
					{
						URI:      req.Params.URI,
						MIMEType: "text/markdown",
						Text:     body,
					},
				},
			}, nil
		})
	}
}
