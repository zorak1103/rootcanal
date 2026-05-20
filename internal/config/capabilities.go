package config

import "fmt"

// CapabilitiesInfo describes what rootcanal can do on a given host.
type CapabilitiesInfo struct {
	SSH                 bool     `json:"ssh"`
	SFTP                bool     `json:"sftp"`
	SFTPAllowedPrefixes []string `json:"sftp_allowed_prefixes"`
	IdleTimeoutMs       int64    `json:"idle_timeout_ms"`
	MaxSessionAgeMs     int64    `json:"max_session_age_ms"`
}

// Capabilities returns the capability set for a named host.
func (c *Config) Capabilities(host string) (CapabilitiesInfo, error) {
	h, ok := c.Hosts[host]
	if !ok {
		return CapabilitiesInfo{}, fmt.Errorf("unknown host %q", host)
	}
	return CapabilitiesInfo{
		SSH:                 true,
		SFTP:                h.SFTPEnabled,
		SFTPAllowedPrefixes: h.SFTPAllowedPrefixes,
		IdleTimeoutMs:       h.IdleTimeout.Milliseconds(),
		MaxSessionAgeMs:     c.Limits.MaxSessionAge.Milliseconds(),
	}, nil
}
