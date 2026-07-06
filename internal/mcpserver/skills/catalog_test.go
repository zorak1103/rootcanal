package skills_test

import (
	"strings"
	"testing"

	"github.com/zorak1103/rootcanal/internal/mcpserver/skills"
)

func TestCatalogSlugs(t *testing.T) {
	// Update this list when adding or removing skill docs.
	wantSlugs := []string{
		"session-workflow",
		"output-cleanliness",
		"runonce-vs-session",
		"sftp-and-safety",
	}
	if got, want := len(skills.Catalog), len(wantSlugs); got != want {
		t.Fatalf("catalog has %d entries, want %d; update wantSlugs if you added/removed a skill", got, want)
	}
	for i, m := range skills.Catalog {
		if m.Slug != wantSlugs[i] {
			t.Errorf("Catalog[%d].Slug = %q, want %q", i, m.Slug, wantSlugs[i])
		}
	}
}

func TestURIPrefix(t *testing.T) {
	for _, m := range skills.Catalog {
		want := skills.URIPrefix + m.Slug
		got := m.URI()
		if got != want {
			t.Errorf("slug %q: URI() = %q, want %q", m.Slug, got, want)
		}
		if !strings.HasPrefix(got, "skill://rootcanal/") {
			t.Errorf("slug %q: URI %q does not start with skill://rootcanal/", m.Slug, got)
		}
	}
}

func TestReadAllSlugs(t *testing.T) {
	for _, m := range skills.Catalog {
		content, err := skills.Read(m.Slug)
		if err != nil {
			t.Errorf("Read(%q) error: %v", m.Slug, err)
			continue
		}
		if len(content) == 0 {
			t.Errorf("Read(%q) returned empty content", m.Slug)
		}
		if content[len(content)-1] != '\n' {
			t.Errorf("Read(%q) content does not end with newline", m.Slug)
		}
	}
}

func TestReadUnknownSlug(t *testing.T) {
	_, err := skills.Read("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown slug, got nil")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error %q does not mention the slug", err.Error())
	}
}

func TestCatalogDescriptions(t *testing.T) {
	for _, m := range skills.Catalog {
		if m.Name == "" {
			t.Errorf("slug %q has empty Name", m.Slug)
		}
		if m.Description == "" {
			t.Errorf("slug %q has empty Description", m.Slug)
		}
	}
}
