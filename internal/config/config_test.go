package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tempFile writes content to a temp file and returns its path.
func tempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---- Expand tests ----

func TestExpand(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{
			name:  "no interpolation",
			input: "key: value",
			want:  "key: value",
		},
		{
			name:  "required var present",
			input: "key: ${MY_VAR}",
			env:   map[string]string{"MY_VAR": "hello"},
			want:  "key: hello",
		},
		{
			name:    "required var missing",
			input:   "key: ${MISSING_VAR_ROOTCANAL}",
			wantErr: true,
		},
		{
			name:  "optional var present overrides default",
			input: "key: ${MY_VAR:-fallback}",
			env:   map[string]string{"MY_VAR": "actual"},
			want:  "key: actual",
		},
		{
			name:  "optional var missing uses default",
			input: "key: ${MISSING_VAR_ROOTCANAL:-fallback}",
			want:  "key: fallback",
		},
		{
			name:  "optional var with empty default",
			input: "key: ${MISSING_VAR_ROOTCANAL:-}",
			want:  "key: ",
		},
		{
			name:  "multiple vars mixed",
			input: "a: ${A_ROOTCANAL}\nb: ${B_ROOTCANAL:-default_b}",
			env:   map[string]string{"A_ROOTCANAL": "val_a"},
			want:  "a: val_a\nb: default_b",
		},
		{
			name:  "no dollar signs",
			input: "hosts:\n  prod:\n    address: 1.2.3.4:22",
			want:  "hosts:\n  prod:\n    address: 1.2.3.4:22",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got, err := Expand([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Expand() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && string(got) != tt.want {
				t.Errorf("Expand() = %q, want %q", string(got), tt.want)
			}
		})
	}
}

// ---- Load tests ----

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_ValidKeyAuth(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	knownHosts := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(keyPath, []byte("key"), 0600)
	_ = os.WriteFile(knownHosts, []byte("kh"), 0600)

	yaml := fmt.Sprintf(`
hosts:
  my-host:
    address: host.example.com:22
    user: deploy
    known_hosts: %s
    auth:
      type: key
      key_path: %s
`, knownHosts, keyPath)

	cfg, err := Load(tempFile(t, "cfg.yaml", yaml))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	h := cfg.Hosts["my-host"]
	if h.User != "deploy" {
		t.Errorf("User = %q, want %q", h.User, "deploy")
	}
}

func TestLoad_ValidAgentAuth(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(knownHosts, []byte("kh"), 0600)

	yaml := fmt.Sprintf(`
hosts:
  staging:
    address: staging.example.com
    user: ops
    known_hosts: %s
    auth:
      type: agent
`, knownHosts)

	cfg, err := Load(tempFile(t, "cfg.yaml", yaml))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	// Address should have :22 appended.
	if cfg.Hosts["staging"].Address != "staging.example.com:22" {
		t.Errorf("Address = %q, want %q", cfg.Hosts["staging"].Address, "staging.example.com:22")
	}
}

func TestLoad_ValidPasswordAuth(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(knownHosts, []byte("kh"), 0600)
	t.Setenv("TEST_RC_PASSWORD", "secret")

	yaml := fmt.Sprintf(`
hosts:
  legacy:
    address: 10.0.0.7:2222
    user: admin
    known_hosts: %s
    auth:
      type: password
      password_env: TEST_RC_PASSWORD
`, knownHosts)

	_, err := Load(tempFile(t, "cfg.yaml", yaml))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
}

func TestLoad_UnknownField(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(knownHosts, []byte("kh"), 0600)

	yaml := fmt.Sprintf(`
unknown_top_level_field: foo
hosts:
  h:
    address: h.example.com:22
    user: u
    known_hosts: %s
    auth:
      type: agent
`, knownHosts)

	_, err := Load(tempFile(t, "cfg.yaml", yaml))
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestLoad_InterpolationError(t *testing.T) {
	yaml := `
hosts:
  h:
    address: ${ROOTCANAL_MISSING_HOST_VAR}
    user: u
    known_hosts: system
    auth:
      type: agent
`
	_, err := Load(tempFile(t, "cfg.yaml", yaml))
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
	if !strings.Contains(err.Error(), "ROOTCANAL_MISSING_HOST_VAR") {
		t.Errorf("error should mention the missing var, got: %v", err)
	}
}

func TestLoad_NoHosts(t *testing.T) {
	yaml := "limits:\n  max_sessions_total: 8\n"
	_, err := Load(tempFile(t, "cfg.yaml", yaml))
	if err == nil {
		t.Fatal("expected error for empty hosts")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	_, err := Load(tempFile(t, "cfg.yaml", "{\n  broken yaml here {{"))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoad_DefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(knownHosts, []byte("kh"), 0600)

	// Minimal config with no limits section → all defaults applied.
	yaml := fmt.Sprintf(`
hosts:
  h:
    address: h.example.com:22
    user: u
    known_hosts: %s
    auth:
      type: agent
`, knownHosts)

	cfg, err := Load(tempFile(t, "cfg.yaml", yaml))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Limits.MaxSessionsTotal != defaultMaxSessionsTotal {
		t.Errorf("MaxSessionsTotal = %d, want %d", cfg.Limits.MaxSessionsTotal, defaultMaxSessionsTotal)
	}
	if cfg.Limits.DefaultIdleTimeout != defaultIdleTimeout {
		t.Errorf("DefaultIdleTimeout = %v, want %v", cfg.Limits.DefaultIdleTimeout, defaultIdleTimeout)
	}
	if cfg.Limits.OutputBufferBytes != defaultOutputBufferBytes {
		t.Errorf("OutputBufferBytes = %d, want %d", cfg.Limits.OutputBufferBytes, defaultOutputBufferBytes)
	}
	// Host idle timeout should inherit the global default.
	if cfg.Hosts["h"].IdleTimeout != defaultIdleTimeout {
		t.Errorf("host IdleTimeout = %v, want %v", cfg.Hosts["h"].IdleTimeout, defaultIdleTimeout)
	}
}

func TestLoad_ExplicitLimitsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "known_hosts")
	_ = os.WriteFile(knownHosts, []byte("kh"), 0600)

	yaml := fmt.Sprintf(`
limits:
  max_sessions_total: 16
  default_idle_timeout: 5m
hosts:
  h:
    address: h.example.com:22
    user: u
    known_hosts: %s
    auth:
      type: agent
`, knownHosts)

	cfg, err := Load(tempFile(t, "cfg.yaml", yaml))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Limits.MaxSessionsTotal != 16 {
		t.Errorf("MaxSessionsTotal = %d, want 16", cfg.Limits.MaxSessionsTotal)
	}
	if cfg.Limits.DefaultIdleTimeout != 5*time.Minute {
		t.Errorf("DefaultIdleTimeout = %v, want 5m", cfg.Limits.DefaultIdleTimeout)
	}
}

func TestLoad_KnownHostsSystem(t *testing.T) {
	yaml := `
hosts:
  h:
    address: h.example.com:22
    user: u
    known_hosts: system
    auth:
      type: agent
`
	_, err := Load(tempFile(t, "cfg.yaml", yaml))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
}

// ---- Validate tests ----

func TestValidate_InvalidHostName(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "kh")
	_ = os.WriteFile(kh, nil, 0600)

	tests := []struct {
		name     string
		hostName string
	}{
		{"uppercase", "MyHost"},
		{"starts with dash", "-host"},
		{"empty", ""},
		{"too long", strings.Repeat("a", 64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Hosts: map[string]Host{
					tt.hostName: {
						Address:    "h.example.com:22",
						User:       "u",
						KnownHosts: kh,
						Auth:       Auth{Type: "agent"},
					},
				},
			}
			if err := Validate(cfg); err == nil {
				t.Errorf("Validate() expected error for host name %q", tt.hostName)
			}
		})
	}
}

func TestValidate_NoHosts(t *testing.T) {
	if err := Validate(&Config{}); err == nil {
		t.Fatal("expected error for empty hosts map")
	}
}

func TestValidate_EmptyAddress(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "kh")
	_ = os.WriteFile(kh, nil, 0600)

	cfg := &Config{Hosts: map[string]Host{"h": {User: "u", KnownHosts: kh, Auth: Auth{Type: "agent"}}}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for empty address")
	}
}

func TestValidate_MalformedAddress(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "kh")
	_ = os.WriteFile(kh, nil, 0600)

	tests := []struct{ name, addr string }{
		{"multiple colons", "host:port:extra"},
		{"empty host", ":22"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Hosts: map[string]Host{
				"h": {Address: tt.addr, User: "u", KnownHosts: kh, Auth: Auth{Type: "agent"}},
			}}
			if err := Validate(cfg); err == nil {
				t.Errorf("Validate() expected error for address %q", tt.addr)
			}
		})
	}
}

func TestValidate_EmptyUser(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "kh")
	_ = os.WriteFile(kh, nil, 0600)

	cfg := &Config{Hosts: map[string]Host{"h": {Address: "h:22", KnownHosts: kh, Auth: Auth{Type: "agent"}}}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for empty user")
	}
}

func TestValidate_AuthErrors(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "kh")
	keyPath := filepath.Join(dir, "key")
	_ = os.WriteFile(kh, nil, 0600)
	_ = os.WriteFile(keyPath, nil, 0600)

	tests := []struct {
		name string
		auth Auth
	}{
		{"type missing", Auth{}},
		{"type invalid", Auth{Type: "foobar"}},
		{"key missing key_path", Auth{Type: "key"}},
		{"key nonexistent key_path", Auth{Type: "key", KeyPath: filepath.Join(dir, "nonexistent-key")}},
		{"password missing password_env", Auth{Type: "password"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Hosts: map[string]Host{
				"h": {Address: "h:22", User: "u", KnownHosts: kh, Auth: tt.auth},
			}}
			if err := Validate(cfg); err == nil {
				t.Errorf("Validate() expected error for auth %+v", tt.auth)
			}
		})
	}
}

func TestValidate_KeyAuthValid(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "kh")
	keyPath := filepath.Join(dir, "key")
	_ = os.WriteFile(kh, nil, 0600)
	_ = os.WriteFile(keyPath, []byte("key"), 0600)

	cfg := &Config{Hosts: map[string]Host{
		"h": {Address: "h:22", User: "u", KnownHosts: kh, Auth: Auth{Type: "key", KeyPath: keyPath}},
	}}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
}

func TestValidate_KnownHostsErrors(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name       string
		knownHosts string
	}{
		{"empty", ""},
		{"nonexistent file", filepath.Join(dir, "no-such-file")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Hosts: map[string]Host{
				"h": {Address: "h:22", User: "u", KnownHosts: tt.knownHosts, Auth: Auth{Type: "agent"}},
			}}
			if err := Validate(cfg); err == nil {
				t.Errorf("Validate() expected error for known_hosts %q", tt.knownHosts)
			}
		})
	}
}

// ---- normalizeAddress tests ----

func TestNormalizeAddress(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"empty", "", "", true},
		{"host only", "example.com", "example.com:22", false},
		{"host and port", "example.com:2222", "example.com:2222", false},
		{"IPv6 with port", "[::1]:22", "[::1]:22", false},
		{"empty host part", ":22", "", true},
		{"multiple colons plain", "a:b:c", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeAddress(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeAddress(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("normalizeAddress(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---- expandPath tests ----

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input string
		want  string
	}{
		{"~/foo/bar", filepath.Join(home, "foo/bar")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~no-slash", "~no-slash"}, // ~ not followed by / → no expansion
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := expandPath(tt.input); got != tt.want {
				t.Errorf("expandPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---- SFTP config validation tests ----

func validHostCfg(t *testing.T) (kh string, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	kh = filepath.Join(dir, "known_hosts")
	keyPath = filepath.Join(dir, "id_ed25519")
	_ = os.WriteFile(kh, []byte("kh"), 0600)
	_ = os.WriteFile(keyPath, []byte("key"), 0600)
	return kh, keyPath
}

func TestValidate_SFTPEnabled_NoPrefix(t *testing.T) {
	kh, _ := validHostCfg(t)
	cfg := &Config{Hosts: map[string]Host{
		"h": {Address: "h:22", User: "u", KnownHosts: kh, Auth: Auth{Type: "agent"}, SFTPEnabled: true},
	}}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
}

func TestValidate_SFTPEnabled_WithCleanAbsolutePrefixes(t *testing.T) {
	kh, _ := validHostCfg(t)
	cfg := &Config{Hosts: map[string]Host{
		"h": {
			Address: "h:22", User: "u", KnownHosts: kh, Auth: Auth{Type: "agent"},
			SFTPEnabled:         true,
			SFTPAllowedPrefixes: []string{"/srv/app", "/var/log"},
		},
	}}
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
}

func TestValidate_SFTPPrefix_RequiresSFTPEnabled(t *testing.T) {
	kh, _ := validHostCfg(t)
	cfg := &Config{Hosts: map[string]Host{
		"h": {
			Address: "h:22", User: "u", KnownHosts: kh, Auth: Auth{Type: "agent"},
			SFTPEnabled:         false,
			SFTPAllowedPrefixes: []string{"/srv/app"},
		},
	}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error: sftp_allowed_prefixes without sftp_enabled")
	}
}

func TestValidate_SFTPPrefix_RelativeRejected(t *testing.T) {
	kh, _ := validHostCfg(t)
	cfg := &Config{Hosts: map[string]Host{
		"h": {
			Address: "h:22", User: "u", KnownHosts: kh, Auth: Auth{Type: "agent"},
			SFTPEnabled:         true,
			SFTPAllowedPrefixes: []string{"srv/app"},
		},
	}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error: relative prefix")
	}
}

func TestValidate_SFTPPrefix_UncleanRejected(t *testing.T) {
	kh, _ := validHostCfg(t)
	cfg := &Config{Hosts: map[string]Host{
		"h": {
			Address: "h:22", User: "u", KnownHosts: kh, Auth: Auth{Type: "agent"},
			SFTPEnabled:         true,
			SFTPAllowedPrefixes: []string{"/srv/app/"},
		},
	}}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error: unclean prefix (trailing slash)")
	}
}

func TestCapabilities(t *testing.T) {
	cfg := &Config{
		Limits: Limits{MaxSessionAge: 4 * time.Hour},
		Hosts: map[string]Host{
			"mynas": {
				IdleTimeout:         15 * time.Minute,
				SFTPEnabled:         true,
				SFTPAllowedPrefixes: []string{"/data"},
			},
		},
	}
	got, err := cfg.Capabilities("mynas")
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !got.SSH {
		t.Error("SSH should be true")
	}
	if !got.SFTP {
		t.Error("SFTP should be true")
	}
	if len(got.SFTPAllowedPrefixes) != 1 || got.SFTPAllowedPrefixes[0] != "/data" {
		t.Errorf("SFTPAllowedPrefixes = %v", got.SFTPAllowedPrefixes)
	}
	if got.IdleTimeoutMs != (15 * time.Minute).Milliseconds() {
		t.Errorf("IdleTimeoutMs = %d", got.IdleTimeoutMs)
	}
	if got.MaxSessionAgeMs != (4 * time.Hour).Milliseconds() {
		t.Errorf("MaxSessionAgeMs = %d", got.MaxSessionAgeMs)
	}

	_, err = cfg.Capabilities("unknown")
	if err == nil {
		t.Error("expected error for unknown host")
	}
}

func TestApplyDefaults_NewV2Fields(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	if cfg.Limits.DefaultTerm != defaultDefaultTerm {
		t.Errorf("DefaultTerm = %q, want %q", cfg.Limits.DefaultTerm, defaultDefaultTerm)
	}
	if cfg.Limits.DefaultCleanOutput == nil || !*cfg.Limits.DefaultCleanOutput {
		t.Error("DefaultCleanOutput should default to true")
	}
	if cfg.Limits.RunOnceMaxBytes != defaultRunOnceMaxBytes {
		t.Errorf("RunOnceMaxBytes = %d, want %d", cfg.Limits.RunOnceMaxBytes, defaultRunOnceMaxBytes)
	}
	if cfg.Limits.RunOnceMaxTimeoutMs != defaultRunOnceMaxTimeoutMs {
		t.Errorf("RunOnceMaxTimeoutMs = %d, want %d", cfg.Limits.RunOnceMaxTimeoutMs, defaultRunOnceMaxTimeoutMs)
	}
	if cfg.Limits.MaxRunOnceConcurrent != defaultMaxRunOnceConcurrent {
		t.Errorf("MaxRunOnceConcurrent = %d, want %d", cfg.Limits.MaxRunOnceConcurrent, defaultMaxRunOnceConcurrent)
	}
	if cfg.Limits.SFTPMaxReadBytes != defaultSFTPMaxReadBytes {
		t.Errorf("SFTPMaxReadBytes = %d, want %d (2 MiB)", cfg.Limits.SFTPMaxReadBytes, defaultSFTPMaxReadBytes)
	}
	if cfg.Limits.DefaultKeepaliveInterval != 15*time.Second {
		t.Errorf("DefaultKeepaliveInterval = %v, want 15s", cfg.Limits.DefaultKeepaliveInterval)
	}
	if cfg.Limits.DefaultKeepaliveMaxFailures != 3 {
		t.Errorf("DefaultKeepaliveMaxFailures = %d, want 3", cfg.Limits.DefaultKeepaliveMaxFailures)
	}
}

func TestCapabilities_TermAndCleanOutput(t *testing.T) {
	clean := true
	cfg := &Config{
		Limits: Limits{MaxSessionAge: time.Hour},
		Hosts: map[string]Host{
			"h": {
				IdleTimeout: time.Minute,
				Term:        "xterm-256color",
				CleanOutput: &clean,
			},
		},
	}
	got, err := cfg.Capabilities("h")
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if got.Term != "xterm-256color" {
		t.Errorf("Term = %q, want xterm-256color", got.Term)
	}
	if got.CleanOutput == nil || !*got.CleanOutput {
		t.Error("CleanOutput should be &true")
	}
}

func TestLoad_SFTPFields_RoundTrip(t *testing.T) {
	kh, _ := validHostCfg(t)
	yaml := fmt.Sprintf(`
hosts:
  h:
    address: h.example.com:22
    user: u
    known_hosts: %s
    auth:
      type: agent
    sftp_enabled: true
    sftp_allowed_prefixes:
      - /srv/app
      - /var/log
`, kh)
	cfg, err := Load(tempFile(t, "cfg.yaml", yaml))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	h := cfg.Hosts["h"]
	if !h.SFTPEnabled {
		t.Error("SFTPEnabled should be true")
	}
	if len(h.SFTPAllowedPrefixes) != 2 || h.SFTPAllowedPrefixes[0] != "/srv/app" {
		t.Errorf("SFTPAllowedPrefixes = %v, want [/srv/app /var/log]", h.SFTPAllowedPrefixes)
	}
}
