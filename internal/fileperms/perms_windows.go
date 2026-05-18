//go:build windows

package fileperms

// Check is a no-op on Windows; POSIX permission bits do not apply.
func Check(_ string) error { return nil }
