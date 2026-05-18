//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"
)

const (
	containerUser = "testuser"
	containerPass = "hunter2hunter2"
)

type containerEnv struct {
	container  testcontainers.Container
	MappedPort string       // host-side port number string, e.g. "32768"
	HostPubKey ssh.PublicKey
}

// startSSHContainer builds and starts the e2e SSH container. The container
// is left running; call terminate when done.
func startSSHContainer(ctx context.Context) (*containerEnv, error) {
	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    "testdata",
				Dockerfile: "sshd/Dockerfile",
				// Rebuild on each run so the baked host key is fresh.
				KeepImage: false,
			},
			ExposedPorts: []string{"22/tcp"},
			WaitingFor: wait.ForLog("Server listening on 0.0.0.0").
				WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	}

	c, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("start SSH container: %w", err)
	}

	port, err := c.MappedPort(ctx, "22/tcp")
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("get mapped port: %w", err)
	}

	pubKey, err := readContainerHostKey(ctx, c)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, fmt.Errorf("read host key: %w", err)
	}

	return &containerEnv{
		container:  c,
		MappedPort: port.Port(),
		HostPubKey: pubKey,
	}, nil
}

// pushAuthorizedKey writes pubKeyLine to the container's authorized_keys and
// fixes ownership so sshd StrictModes accepts it.
func (ce *containerEnv) pushAuthorizedKey(ctx context.Context, tmpDir string, pubKeyLine []byte) error {
	localPath := filepath.Join(tmpDir, "authorized_keys")
	if err := os.WriteFile(localPath, pubKeyLine, 0600); err != nil {
		return fmt.Errorf("write authorized_keys: %w", err)
	}
	if err := ce.container.CopyFileToContainer(ctx, localPath, "/home/testuser/.ssh/authorized_keys", 0600); err != nil {
		return fmt.Errorf("copy authorized_keys: %w", err)
	}
	code, _, err := ce.container.Exec(ctx, []string{
		"chown", "testuser:testuser", "/home/testuser/.ssh/authorized_keys",
	})
	if err != nil {
		return fmt.Errorf("chown authorized_keys: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("chown authorized_keys: exit %d", code)
	}
	return nil
}

func (ce *containerEnv) terminate(ctx context.Context) {
	_ = ce.container.Terminate(ctx)
}

// readContainerHostKey reads the ed25519 host public key by executing
// `cat` inside the running container.
func readContainerHostKey(ctx context.Context, c testcontainers.Container) (ssh.PublicKey, error) {
	exitCode, reader, err := c.Exec(ctx, []string{"cat", "/etc/ssh/ssh_host_ed25519_key.pub"})
	if err != nil {
		return nil, fmt.Errorf("exec cat host key: %w", err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("exec cat host key: exit code %d", exitCode)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read host key output: %w", err)
	}
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return nil, fmt.Errorf("parse authorized key (%q): %w", data, err)
	}
	return pubKey, nil
}
