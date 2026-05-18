//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// fixtureEnv holds all per-run generated paths and values that tests need.
type fixtureEnv struct {
	TmpDir      string
	BinPath     string // built rootcanal binary
	MainCfg     string // rootcanal.yaml for most tests
	LimitsCfg   string // rootcanal-limits.yaml for session-limit tests
	KHPath      string // known_hosts (correct container host key)
	BadKHPath   string // known_hosts (deliberately wrong key)
	KeyPath     string // private key, no passphrase
	KeyPassPath string // private key, encrypted with passphrase
}

const passphrase = "e2e-passphrase"

// genFixtures generates keypairs, known_hosts files, config files, and the
// rootcanal binary into tmpDir.
func genFixtures(ctx context.Context, tmpDir string, cenv *containerEnv) (*fixtureEnv, error) {
	// ---- keypair (no passphrase) ----
	pub1, priv1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	sshPub1, err := ssh.NewPublicKey(pub1)
	if err != nil {
		return nil, fmt.Errorf("ssh public key: %w", err)
	}
	block1, err := ssh.MarshalPrivateKey(priv1, "")
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	keyPath := filepath.Join(tmpDir, "id_ed25519")
	if err := writeFile(keyPath, pem.EncodeToMemory(block1), 0600); err != nil {
		return nil, err
	}

	// ---- keypair (with passphrase) ----
	pub2, priv2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key (pass): %w", err)
	}
	sshPub2, err := ssh.NewPublicKey(pub2)
	if err != nil {
		return nil, fmt.Errorf("ssh public key (pass): %w", err)
	}
	block2, err := ssh.MarshalPrivateKeyWithPassphrase(priv2, "", []byte(passphrase))
	if err != nil {
		return nil, fmt.Errorf("marshal private key (pass): %w", err)
	}
	keyPassPath := filepath.Join(tmpDir, "id_ed25519_pass")
	if err := writeFile(keyPassPath, pem.EncodeToMemory(block2), 0600); err != nil {
		return nil, err
	}

	// ---- authorized_keys (both public keys combined) ----
	authLine := append(ssh.MarshalAuthorizedKey(sshPub1), ssh.MarshalAuthorizedKey(sshPub2)...)
	if err := cenv.pushAuthorizedKey(ctx, tmpDir, authLine); err != nil {
		return nil, fmt.Errorf("push authorized key: %w", err)
	}

	// ---- known_hosts (correct container host key) ----
	khPath := filepath.Join(tmpDir, "known_hosts")
	addr := fmt.Sprintf("127.0.0.1:%s", cenv.MappedPort)
	khLine := knownhosts.Line([]string{knownhosts.Normalize(addr)}, cenv.HostPubKey)
	if err := writeFile(khPath, []byte(khLine+"\n"), 0600); err != nil {
		return nil, err
	}

	// ---- known_hosts_bad (fresh wrong key — same address, different key) ----
	_, wrongPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate wrong key: %w", err)
	}
	wrongSigner, err := ssh.NewSignerFromKey(wrongPriv)
	if err != nil {
		return nil, fmt.Errorf("wrong signer: %w", err)
	}
	badKHPath := filepath.Join(tmpDir, "known_hosts_bad")
	badLine := knownhosts.Line([]string{knownhosts.Normalize(addr)}, wrongSigner.PublicKey())
	if err := writeFile(badKHPath, []byte(badLine+"\n"), 0600); err != nil {
		return nil, err
	}

	// ---- build rootcanal binary ----
	binPath := filepath.Join(tmpDir, binaryName())
	if err := buildBinary(ctx, binPath); err != nil {
		return nil, fmt.Errorf("build binary: %w", err)
	}

	// ---- render main config ----
	mainCfg := filepath.Join(tmpDir, "rootcanal.yaml")
	if err := renderConfig(mainCfg, configParams{
		Port:        cenv.MappedPort,
		KeyPath:     toSlash(keyPath),
		KeyPassPath: toSlash(keyPassPath),
		KHPath:      toSlash(khPath),
		BadKHPath:   toSlash(badKHPath),
		// Main config: loose session limits so non-limit tests can open freely.
		MaxSessionsTotal:   10,
		MaxSessionsPerHost: 4,
		OutputBufferBytes:  0, // use server default (1 MiB)
	}); err != nil {
		return nil, fmt.Errorf("render main config: %w", err)
	}

	// ---- render limits config ----
	limitsCfg := filepath.Join(tmpDir, "rootcanal-limits.yaml")
	if err := renderConfig(limitsCfg, configParams{
		Port:        cenv.MappedPort,
		KeyPath:     toSlash(keyPath),
		KeyPassPath: toSlash(keyPassPath),
		KHPath:      toSlash(khPath),
		BadKHPath:   toSlash(badKHPath),
		// Tight limits: 2 global, 1 per-host, tiny output buffer.
		MaxSessionsTotal:   2,
		MaxSessionsPerHost: 1,
		OutputBufferBytes:  4096,
	}); err != nil {
		return nil, fmt.Errorf("render limits config: %w", err)
	}

	return &fixtureEnv{
		TmpDir:      tmpDir,
		BinPath:     binPath,
		MainCfg:     mainCfg,
		LimitsCfg:   limitsCfg,
		KHPath:      khPath,
		BadKHPath:   badKHPath,
		KeyPath:     keyPath,
		KeyPassPath: keyPassPath,
	}, nil
}

// configParams is the data passed to the rootcanal.yaml template.
type configParams struct {
	Port               string
	KeyPath            string
	KeyPassPath        string
	KHPath             string
	BadKHPath          string
	MaxSessionsTotal   int
	MaxSessionsPerHost int
	OutputBufferBytes  int
}

func renderConfig(dst string, p configParams) error {
	var buf bytes.Buffer
	// Replace the {{containerUser}} call with the actual value inline since
	// FuncMap must be set before parsing. We use a simple string replace instead.
	raw := strings.ReplaceAll(configTmplRaw, "{{containerUser}}", containerUser)
	tmpl, err := template.New("cfg").Parse(raw)
	if err != nil {
		return err
	}
	if err := tmpl.Execute(&buf, p); err != nil {
		return err
	}
	return writeFile(dst, buf.Bytes(), 0600)
}

// configTmplRaw is the raw template text (containerUser is substituted before parsing).
const configTmplRaw = `limits:
  max_sessions_total: {{.MaxSessionsTotal}}
  max_sessions_per_host: {{.MaxSessionsPerHost}}{{if gt .OutputBufferBytes 0}}
  output_buffer_bytes: {{.OutputBufferBytes}}{{end}}
  default_send_timeout_ms: 2000
  max_send_timeout_ms: 30000
  sftp_max_read_bytes: 65536
  sftp_max_write_bytes: 65536

hosts:
  testhost-key:
    address: "127.0.0.1:{{.Port}}"
    user: testuser
    auth:
      type: key
      key_path: "{{.KeyPath}}"
    known_hosts: "{{.KHPath}}"

  testhost-keypass:
    address: "127.0.0.1:{{.Port}}"
    user: testuser
    auth:
      type: key
      key_path: "{{.KeyPassPath}}"
      passphrase_env: TEST_RC_PASSPHRASE
    known_hosts: "{{.KHPath}}"

  testhost-pwd:
    address: "127.0.0.1:{{.Port}}"
    user: testuser
    auth:
      type: password
      password_env: TEST_RC_PASSWORD
    known_hosts: "{{.KHPath}}"

  testhost-sftp:
    address: "127.0.0.1:{{.Port}}"
    user: testuser
    auth:
      type: key
      key_path: "{{.KeyPath}}"
    known_hosts: "{{.KHPath}}"
    sftp_enabled: true
    sftp_allowed_prefixes:
      - "/home/testuser"
      - "/srv/sftp"

  testhost-sftp-restricted:
    address: "127.0.0.1:{{.Port}}"
    user: testuser
    auth:
      type: key
      key_path: "{{.KeyPath}}"
    known_hosts: "{{.KHPath}}"
    sftp_enabled: true
    sftp_allowed_prefixes:
      - "/srv/sftp"

  testhost-sftp-disabled:
    address: "127.0.0.1:{{.Port}}"
    user: testuser
    auth:
      type: key
      key_path: "{{.KeyPath}}"
    known_hosts: "{{.KHPath}}"
    sftp_enabled: false

  testhost-bad-hostkey:
    address: "127.0.0.1:{{.Port}}"
    user: testuser
    auth:
      type: key
      key_path: "{{.KeyPath}}"
    known_hosts: "{{.BadKHPath}}"
`

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// toSlash converts a path to forward slashes so it embeds safely in YAML on
// all platforms. rootcanal uses os.Open which accepts forward slashes on Windows.
func toSlash(p string) string {
	return filepath.ToSlash(p)
}
