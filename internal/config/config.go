package config

import (
	"bytes"
	"fmt"
	"os"
	"time"

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
	defaultSFTPMaxReadBytes   = 5 << 20  // 5 MiB
	defaultSFTPMaxWriteBytes  = 25 << 20 // 25 MiB
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

	for name, h := range cfg.Hosts {
		if h.IdleTimeout == 0 {
			h.IdleTimeout = cfg.Limits.DefaultIdleTimeout
		}
		cfg.Hosts[name] = h
	}
}
