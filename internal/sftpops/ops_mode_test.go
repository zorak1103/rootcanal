package sftpops

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/zorak1103/rootcanal/internal/config"
	"golang.org/x/crypto/ssh"
)

// TestWrite_SpecialModeBitsRejectedAtOpsLayer verifies the defense-in-depth
// chmod check in ops.Write. Even if the mcpserver layer is bypassed, the ops
// layer must refuse modes with setuid/setgid/sticky bits (MAN-009).
func TestWrite_SpecialModeBitsRejectedAtOpsLayer(t *testing.T) {
	cfg := &config.Config{
		Hosts: map[string]config.Host{
			"h": {
				SFTPEnabled:         true,
				SFTPAllowedPrefixes: []string{"/tmp"},
			},
		},
	}

	fakeClient := newFakeFS()
	o := newOps(cfg,
		// pool getter: returns a nil *ssh.Client; newClient ignores it
		func(_ context.Context, _ string) (*ssh.Client, func(), error) {
			return nil, func() {}, nil
		},
		// newClient: ignores the nil ssh.Client and returns the fake FS
		func(_ *ssh.Client) (sftpClientIface, error) {
			return fakeClient, nil
		},
	)

	tests := []struct {
		mode    fs.FileMode
		wantErr bool
		desc    string
	}{
		{0o644, false, "regular mode"},
		{0o755, false, "execute bit"},
		{0o4755, true, "setuid bit"},
		{0o2755, true, "setgid bit"},
		{0o1777, true, "sticky bit"},
		{0o7777, true, "all special bits"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			err := o.Write(context.Background(), "h", "/tmp/f", []byte("x"), tt.mode, false)
			if tt.wantErr {
				if err == nil {
					t.Errorf("mode %04o: expected error, got nil — special bits forwarded to remote", tt.mode)
					return
				}
				if !strings.Contains(err.Error(), "special bits") {
					t.Errorf("mode %04o: error should mention 'special bits', got: %v", tt.mode, err)
				}
			} else {
				if err != nil {
					t.Errorf("mode %04o: unexpected error: %v", tt.mode, err)
				}
			}
		})
	}
}
