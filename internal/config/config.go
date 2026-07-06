package config

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/zorak1103/rootcanal/internal/fileperms"
	"gopkg.in/yaml.v3"
)

const (
	defaultMaxSessionsTotal   = 32
	defaultMaxSessionsPerHost = 4
	defaultIdleTimeout        = 15 * time.Minute
	defaultMaxSessionAge      = 4 * time.Hour
	defaultOutputBufferBytes  = 1 << 20 // 1 MiB
	defaultDialTimeout        = 10 * time.Second
	defaultSendTimeoutMs      = 2000
	defaultMaxSendTimeoutMs   = 30000
	defaultSFTPMaxReadBytes   = 2 << 20  // 2 MiB
	defaultSFTPMaxWriteBytes  = 25 << 20 // 25 MiB

	// v2.0 additions
	defaultDefaultTerm          = "dumb"
	defaultRunOnceMaxBytes      = int64(1 << 20) // 1 MiB
	defaultRunOnceMaxTimeoutMs  = 60000          // 60 s
	defaultMaxRunOnceConcurrent = 16

	// v2.1 additions
	defaultKeepaliveInterval    = 15 * time.Second
	defaultKeepaliveMaxFailures = 3

	// job registry
	defaultJobTTL  = time.Hour
	defaultMaxJobs = 32

	// detach: separate ceiling so detached jobs can outlive the 60s run_once cap
	defaultDetachMaxDurationMs = 24 * 60 * 60 * 1000 // 24 h
)

// Config is the top-level configuration.
type Config struct {
	Limits Limits          `yaml:"limits"`
	Hosts  map[string]Host `yaml:"hosts"`
}

// Limits holds global resource caps and timeouts.
type Limits struct {
	MaxSessionsTotal     int           `yaml:"max_sessions_total"`
	MaxSessionsPerHost   int           `yaml:"max_sessions_per_host"`
	DefaultIdleTimeout   time.Duration `yaml:"default_idle_timeout"`
	MaxSessionAge        time.Duration `yaml:"max_session_age"`
	OutputBufferBytes    int           `yaml:"output_buffer_bytes"`
	DialTimeout          time.Duration `yaml:"dial_timeout"`
	DefaultSendTimeoutMs int           `yaml:"default_send_timeout_ms"`
	MaxSendTimeoutMs     int           `yaml:"max_send_timeout_ms"`
	SFTPMaxReadBytes     int           `yaml:"sftp_max_read_bytes"`
	SFTPMaxWriteBytes    int           `yaml:"sftp_max_write_bytes"`
	// v2.0 additions
	DefaultTerm          string `yaml:"default_term,omitempty"`
	DefaultCleanOutput   *bool  `yaml:"default_clean_output,omitempty"`
	RunOnceMaxBytes      int64  `yaml:"run_once_max_bytes,omitempty"`
	RunOnceMaxTimeoutMs  int    `yaml:"run_once_max_timeout_ms,omitempty"`
	MaxRunOnceConcurrent int    `yaml:"max_run_once_concurrent,omitempty"`
	// v2.1 additions
	DefaultKeepaliveInterval    time.Duration `yaml:"default_keepalive_interval,omitempty"`
	DefaultKeepaliveMaxFailures int           `yaml:"default_keepalive_max_failures,omitempty"`
	JobTTL                      time.Duration `yaml:"job_ttl,omitempty"`
	MaxJobs                     int           `yaml:"max_jobs,omitempty"`
	// DetachMaxDurationMs is the maximum wall-clock duration for a detached job
	// (ssh_run_once with detach=true). Unlike RunOnceMaxTimeoutMs, which governs
	// the 60 s synchronous cap, this allows detached jobs to run for hours.
	// Default: 86400000 (24 h). Set to 0 to use the default.
	DetachMaxDurationMs int `yaml:"detach_max_duration_ms,omitempty"`
}

// Host is a pre-declared SSH target.
type Host struct {
	Address             string        `yaml:"address"`
	User                string        `yaml:"user"`
	Auth                Auth          `yaml:"auth"`
	KnownHosts          string        `yaml:"known_hosts"`
	IdleTimeout         time.Duration `yaml:"idle_timeout"`
	Description         string        `yaml:"description,omitempty"`
	SFTPEnabled         bool          `yaml:"sftp_enabled,omitempty"`
	SFTPAllowedPrefixes []string      `yaml:"sftp_allowed_prefixes,omitempty"`
	// v2.0 additions
	Term        string `yaml:"term,omitempty"`
	CleanOutput *bool  `yaml:"clean_output,omitempty"`
	// KeepaliveInterval overrides the global default. Set to 0 to disable keepalives for this host.
	// When nil (not set in YAML), the global default is used.
	KeepaliveInterval *time.Duration `yaml:"keepalive_interval,omitempty"`
	// KeepaliveMaxFailures overrides the global default. Set to 0 to never disconnect
	// based on keepalive failures (keepalives are still sent).
	KeepaliveMaxFailures *int `yaml:"keepalive_max_failures,omitempty"`
	// AllowKnownHostsUpdate permits the ssh_accept_host_key MCP tool to rewrite
	// this host's known_hosts entry after a server rebuild. Default false.
	AllowKnownHostsUpdate bool `yaml:"allow_known_hosts_update,omitempty"`
}

// Auth specifies how to authenticate to a host.
type Auth struct {
	Type          string `yaml:"type"`
	KeyPath       string `yaml:"key_path,omitempty"`
	PassphraseEnv string `yaml:"passphrase_env,omitempty"`
	PasswordEnv   string `yaml:"password_env,omitempty"`
}

// Load reads, interpolates, and validates a config file.
func Load(path string) (*Config, error) {
	if err := fileperms.Check(path); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}

	expanded, err := Expand(raw)
	if err != nil {
		return nil, fmt.Errorf("interpolating config: %w", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)

	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	applyDefaults(&cfg)

	if err := Validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	l := &cfg.Limits
	if l.MaxSessionsTotal == 0 {
		l.MaxSessionsTotal = defaultMaxSessionsTotal
	}
	if l.MaxSessionsPerHost == 0 {
		l.MaxSessionsPerHost = defaultMaxSessionsPerHost
	}
	if l.DefaultIdleTimeout == 0 {
		l.DefaultIdleTimeout = defaultIdleTimeout
	}
	if l.MaxSessionAge == 0 {
		l.MaxSessionAge = defaultMaxSessionAge
	}
	if l.OutputBufferBytes == 0 {
		l.OutputBufferBytes = defaultOutputBufferBytes
	}
	if l.DialTimeout == 0 {
		l.DialTimeout = defaultDialTimeout
	}
	if l.DefaultSendTimeoutMs == 0 {
		l.DefaultSendTimeoutMs = defaultSendTimeoutMs
	}
	if l.MaxSendTimeoutMs == 0 {
		l.MaxSendTimeoutMs = defaultMaxSendTimeoutMs
	}
	if l.SFTPMaxReadBytes == 0 {
		l.SFTPMaxReadBytes = defaultSFTPMaxReadBytes
	}
	if l.SFTPMaxWriteBytes == 0 {
		l.SFTPMaxWriteBytes = defaultSFTPMaxWriteBytes
	}
	if l.DefaultTerm == "" {
		l.DefaultTerm = defaultDefaultTerm
	}
	if l.DefaultCleanOutput == nil {
		t := true
		l.DefaultCleanOutput = &t
	}
	if l.RunOnceMaxBytes == 0 {
		l.RunOnceMaxBytes = defaultRunOnceMaxBytes
	}
	if l.RunOnceMaxTimeoutMs == 0 {
		l.RunOnceMaxTimeoutMs = defaultRunOnceMaxTimeoutMs
	}
	if l.MaxRunOnceConcurrent == 0 {
		l.MaxRunOnceConcurrent = defaultMaxRunOnceConcurrent
	}
	if l.DefaultKeepaliveInterval == 0 {
		l.DefaultKeepaliveInterval = defaultKeepaliveInterval
	}
	if l.DefaultKeepaliveMaxFailures == 0 {
		l.DefaultKeepaliveMaxFailures = defaultKeepaliveMaxFailures
	}
	if l.JobTTL == 0 {
		l.JobTTL = defaultJobTTL
	}
	if l.MaxJobs == 0 {
		l.MaxJobs = defaultMaxJobs
	}
	if l.DetachMaxDurationMs == 0 {
		l.DetachMaxDurationMs = defaultDetachMaxDurationMs
	}

	for name, h := range cfg.Hosts {
		if h.IdleTimeout == 0 {
			h.IdleTimeout = cfg.Limits.DefaultIdleTimeout
		}
		cfg.Hosts[name] = h
	}
}
