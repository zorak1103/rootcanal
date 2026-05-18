//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// testFixtures is set by TestMain and read by all test functions.
var testFixtures *fixtureEnv

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	os.Exit(run(ctx, m))
}

func run(ctx context.Context, m *testing.M) int {
	tmpDir, err := os.MkdirTemp("", "rootcanal-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: MkdirTemp: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmpDir)

	// Start SSH container. If Docker is unavailable, skip gracefully.
	cenv, err := startSSHContainer(ctx)
	if err != nil {
		if isDockerUnavailable(err) {
			fmt.Fprintln(os.Stderr, "e2e: SKIP — Docker is not available:", err)
			return 0
		}
		fmt.Fprintf(os.Stderr, "e2e: startSSHContainer: %v\n", err)
		return 1
	}
	defer cenv.terminate(context.Background())

	// Generate keypairs, known_hosts, config files, and the rootcanal binary.
	fx, err := genFixtures(ctx, tmpDir, cenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: genFixtures: %v\n", err)
		return 1
	}
	testFixtures = fx

	return m.Run()
}

// buildBinary compiles the rootcanal binary into dst. It uses the project root
// (one level up from the e2e/ directory, which is the test working dir) as the
// working directory so that go build resolves the module correctly.
func buildBinary(ctx context.Context, dst string) error {
	projectRoot, err := filepath.Abs("..")
	if err != nil {
		return fmt.Errorf("abs project root: %w", err)
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-o", dst, "./cmd/rootcanal")
	cmd.Dir = projectRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build: %w\n%s", err, out)
	}
	return nil
}

// binaryName returns the platform-appropriate binary name.
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "rootcanal.exe"
	}
	return "rootcanal"
}

// isDockerUnavailable reports whether err looks like a Docker connectivity failure.
func isDockerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, kw := range []string{
		"Cannot connect to the Docker daemon",
		"docker: not found",
		"docker daemon is not running",
		"connection refused",
		"no such file",
	} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}
