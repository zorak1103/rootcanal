// Package pathutil holds small filesystem-path helpers shared across
// internal packages (config, sshconn) that previously carried their own
// copy-pasted implementations.
package pathutil

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandTilde replaces a leading "~/" in p with the current user's home
// directory. If the home directory cannot be determined, p is returned
// unchanged. Paths without a leading "~/" are returned unchanged.
func ExpandTilde(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
