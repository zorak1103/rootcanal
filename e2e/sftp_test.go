//go:build e2e

package e2e

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSFTP_ReadSeedFile(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	r := h.SFTPRead("testhost-sftp", "/srv/sftp/readme.txt", 0)
	if r.IsError {
		t.Fatalf("SFTPRead failed: %s", r.ErrText)
	}
	if !strings.Contains(r.Content, "Hello from rootcanal e2e test!") {
		t.Errorf("unexpected seed file content: %q", r.Content)
	}
}

func TestSFTP_WriteThenRead(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	path := fmt.Sprintf("/home/testuser/e2e-write-%d.txt", time.Now().UnixNano())
	want := "hello write test\n"

	wr := h.SFTPWrite("testhost-sftp", path, want, false, "")
	if wr.IsError {
		t.Fatalf("SFTPWrite failed: %s", wr.ErrText)
	}

	r := h.SFTPRead("testhost-sftp", path, 0)
	if r.IsError {
		t.Fatalf("SFTPRead failed: %s", r.ErrText)
	}
	if r.Content != want {
		t.Errorf("read back %q, want %q", r.Content, want)
	}
}

func TestSFTP_WriteBinaryRoundTrip(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	path := fmt.Sprintf("/home/testuser/e2e-bin-%d.dat", time.Now().UnixNano())
	data := []byte{0xff, 0xfe, 0x00, 0x01, 0x42, 0xde, 0xad, 0xbe, 0xef}
	encoded := base64.StdEncoding.EncodeToString(data)

	wr := h.SFTPWrite("testhost-sftp", path, encoded, true, "")
	if wr.IsError {
		t.Fatalf("SFTPWrite binary failed: %s", wr.ErrText)
	}

	r := h.SFTPRead("testhost-sftp", path, 0)
	if r.IsError {
		t.Fatalf("SFTPRead binary failed: %s", r.ErrText)
	}
	if !r.Binary {
		t.Error("expected Binary=true for file containing non-UTF-8 bytes")
	}
	decoded, err := base64.StdEncoding.DecodeString(r.Content)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != string(data) {
		t.Errorf("binary roundtrip mismatch: got %x, want %x", decoded, data)
	}
}

func TestSFTP_ListSeedDir(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	lr := h.SFTPList("testhost-sftp", "/srv/sftp")
	if lr.IsError {
		t.Fatalf("SFTPList failed: %s", lr.ErrText)
	}

	names := make(map[string]bool)
	for _, e := range lr.Entries {
		names[e.Name] = true
	}
	for _, want := range []string{"readme.txt", "subdir"} {
		if !names[want] {
			t.Errorf("expected %q in /srv/sftp listing, got entries: %v", want, lr.Entries)
		}
	}
}

func TestSFTP_WriteWithMode(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	path := fmt.Sprintf("/home/testuser/e2e-mode-%d.txt", time.Now().UnixNano())

	wr := h.SFTPWrite("testhost-sftp", path, "mode test", false, "0600")
	if wr.IsError {
		t.Fatalf("SFTPWrite failed: %s", wr.ErrText)
	}

	lr := h.SFTPList("testhost-sftp", "/home/testuser")
	if lr.IsError {
		t.Fatalf("SFTPList failed: %s", lr.ErrText)
	}

	fname := path[strings.LastIndex(path, "/")+1:]
	for _, e := range lr.Entries {
		if e.Name == fname {
			if !strings.HasPrefix(e.Mode, "-rw-------") {
				t.Errorf("expected mode -rw-------, got %q", e.Mode)
			}
			return
		}
	}
	t.Errorf("file %q not found in listing", fname)
}

func TestSFTP_WriteSpecialBitsRejected(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	wr := h.SFTPWrite("testhost-sftp", "/home/testuser/e2e-setuid.txt", "bad", false, "4755")
	h.RequireToolError(wr.IsError, wr.ErrText, "setuid/setgid/sticky")
}
