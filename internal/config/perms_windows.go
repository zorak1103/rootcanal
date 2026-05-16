//go:build windows

package config

// checkFilePerms is a no-op on Windows; POSIX permission bits do not apply.
func checkFilePerms(_ string) error { return nil }
