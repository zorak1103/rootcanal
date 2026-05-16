//go:build !windows

package sshconn

import (
	"fmt"
	"os"
)

// checkFilePerms returns an error if the file at path is readable by group or
// world. Mirrors ssh(1) strict-mode behaviour for private keys and known_hosts.
func checkFilePerms(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %q: %w", path, err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("file %q has insecure permissions %04o: must not be readable by group or world", path, perm)
	}
	return nil
}
