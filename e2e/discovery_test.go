//go:build e2e

package e2e

import (
	"testing"
)

func TestListHosts_ReturnsKnownHosts(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	r := h.ListHosts()
	if r.IsError {
		t.Fatalf("ListHosts: %s", r.ErrText)
	}
	if len(r.Hosts) == 0 {
		t.Fatal("expected at least one host")
	}

	names := make(map[string]bool)
	for _, host := range r.Hosts {
		names[host.Name] = true
		// Credentials must not appear in the response.
		if host.AuthType == "" {
			t.Errorf("host %q has empty auth_type", host.Name)
		}
	}
	if !names["testhost-key"] {
		t.Error("expected testhost-key in hosts list")
	}
	if !names["testhost-sftp"] {
		t.Error("expected testhost-sftp in hosts list")
	}
}

func TestListHosts_SFTPFlag(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	r := h.ListHosts()
	if r.IsError {
		t.Fatalf("ListHosts: %s", r.ErrText)
	}

	for _, host := range r.Hosts {
		switch host.Name {
		case "testhost-sftp", "testhost-sftp-restricted":
			if !host.SFTPEnabled {
				t.Errorf("host %q: sftp_enabled should be true", host.Name)
			}
		case "testhost-sftp-disabled":
			if host.SFTPEnabled {
				t.Errorf("host %q: sftp_enabled should be false", host.Name)
			}
		}
	}
}

func TestHostCapabilities_SSH(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	r := h.HostCapabilities("testhost-key")
	if r.IsError {
		t.Fatalf("HostCapabilities: %s", r.ErrText)
	}
	if !r.SSH {
		t.Error("SSH should be true")
	}
	if r.SFTP {
		t.Error("testhost-key: SFTP should be false")
	}
	if r.IdleTimeoutMs <= 0 {
		t.Errorf("IdleTimeoutMs should be positive, got %d", r.IdleTimeoutMs)
	}
}

func TestHostCapabilities_SFTP(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	r := h.HostCapabilities("testhost-sftp")
	if r.IsError {
		t.Fatalf("HostCapabilities: %s", r.ErrText)
	}
	if !r.SFTP {
		t.Error("testhost-sftp: SFTP should be true")
	}
	if len(r.SFTPAllowedPrefixes) == 0 {
		t.Error("testhost-sftp: SFTPAllowedPrefixes should not be empty")
	}
}

func TestHostCapabilities_UnknownHost(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	r := h.HostCapabilities("no-such-host")
	if !r.IsError {
		t.Error("expected error for unknown host")
	}
}
