//go:build windows

package sshconn

// checkFilePerms is a no-op on Windows; POSIX permission bits do not apply.
func checkFilePerms(_ string) error { return nil }
