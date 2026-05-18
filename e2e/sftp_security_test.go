//go:build e2e

package e2e

import "testing"

func TestSFTPSec_DisabledHostRejected(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	r := h.SFTPRead("testhost-sftp-disabled", "/srv/sftp/readme.txt", 0)
	h.RequireToolError(r.IsError, r.ErrText, "SFTP not enabled")
}

func TestSFTPSec_PrefixAllowed(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	// /srv/sftp is in the allowed prefixes for testhost-sftp-restricted.
	r := h.SFTPRead("testhost-sftp-restricted", "/srv/sftp/readme.txt", 0)
	if r.IsError {
		t.Fatalf("expected success, got error: %s", r.ErrText)
	}
}

func TestSFTPSec_PrefixDeniedAbsolute(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	// /etc/passwd is not under the allowed prefix /srv/sftp.
	r := h.SFTPRead("testhost-sftp-restricted", "/etc/passwd", 0)
	h.RequireToolError(r.IsError, r.ErrText, "not under any allowed prefix")
}

func TestSFTPSec_PrefixDeniedTraversal(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	// After path.Clean, /srv/sftp/../etc/passwd becomes /etc/passwd — denied.
	r := h.SFTPRead("testhost-sftp-restricted", "/srv/sftp/../etc/passwd", 0)
	h.RequireToolError(r.IsError, r.ErrText, "not under any allowed prefix")
}

func TestSFTPSec_AbsolutePathRequired(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	r := h.SFTPRead("testhost-sftp-restricted", "readme.txt", 0)
	h.RequireToolError(r.IsError, r.ErrText, "must be absolute")
}
