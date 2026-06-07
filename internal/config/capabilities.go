package config

import "fmt"

// UnknownHostError returns an error for a host name that is not in the loaded
// config. The message explains that rootcanal reads config once at startup and
// a restart is needed for newly added hosts.
func UnknownHostError(name string) error {
	return fmt.Errorf(
		"unknown host %q: not in loaded config — if you recently added it to the "+
			"config file, restart rootcanal to pick it up (config is read once at startup). "+
			"Use ssh_list_hosts to see currently loaded hosts", name)
}

// CapabilitiesInfo describes what rootcanal can do on a given host.
type CapabilitiesInfo struct {
	SSH                 bool     `json:"ssh"`
	SFTP                bool     `json:"sftp"`
	SFTPAllowedPrefixes []string `json:"sftp_allowed_prefixes"`
	IdleTimeoutMs       int64    `json:"idle_timeout_ms"`
	MaxSessionAgeMs     int64    `json:"max_session_age_ms"`
	Term                string   `json:"term,omitempty"`
	CleanOutput         *bool    `json:"clean_output,omitempty"`
}

// Capabilities returns the capability set for a named host.
func (c *Config) Capabilities(host string) (CapabilitiesInfo, error) {
	h, ok := c.Hosts[host]
	if !ok {
		return CapabilitiesInfo{}, UnknownHostError(host)
	}
	return CapabilitiesInfo{
		SSH:                 true,
		SFTP:                h.SFTPEnabled,
		SFTPAllowedPrefixes: h.SFTPAllowedPrefixes,
		IdleTimeoutMs:       h.IdleTimeout.Milliseconds(),
		MaxSessionAgeMs:     c.Limits.MaxSessionAge.Milliseconds(),
		Term:                h.Term,
		CleanOutput:         h.CleanOutput,
	}, nil
}
