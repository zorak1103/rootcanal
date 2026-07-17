package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandTilde_LeadingTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory available in this environment: %v", err)
	}
	got := ExpandTilde("~/.ssh/id_ed25519")
	want := filepath.Join(home, ".ssh", "id_ed25519")
	if got != want {
		t.Errorf("ExpandTilde(%q) = %q, want %q", "~/.ssh/id_ed25519", got, want)
	}
}

func TestExpandTilde_NoLeadingTilde(t *testing.T) {
	got := ExpandTilde("/etc/rootcanal/key")
	if got != "/etc/rootcanal/key" {
		t.Errorf("ExpandTilde should not modify an absolute path, got %q", got)
	}
}

func TestExpandTilde_BareTilde_NotExpanded(t *testing.T) {
	// Only the "~/" prefix (tilde-slash) is recognized; a bare "~" is not.
	got := ExpandTilde("~")
	if got != "~" {
		t.Errorf("ExpandTilde(%q) = %q, want unchanged %q", "~", got, "~")
	}
}
