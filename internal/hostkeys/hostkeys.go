package hostkeys

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/zorak1103/rootcanal/internal/config"
	"github.com/zorak1103/rootcanal/internal/sshconn"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Scanner captures the live host key of an SSH server without verifying it.
// sshconn.ProdScanner satisfies this interface.
type Scanner interface {
	ScanHostKey(ctx context.Context, h config.Host, limits config.Limits) (ssh.PublicKey, error)
}

// InspectResult is returned by Inspect.
type InspectResult struct {
	Host       string `json:"host"`
	CurrentFP  string `json:"current_fingerprint"` // SHA256 of stored key matching live key type; "" if none
	NewFP      string `json:"new_fingerprint"`     // SHA256 of freshly scanned live key
	Changed    bool   `json:"changed"`             // true only when a same-type entry exists with a different fingerprint
	KnownHosts string `json:"known_hosts"`         // resolved path (shown to operator)
	// StaleEntries lists every entry stored for this host that Accept cannot
	// safely supersede: more than one entry, or an entry of a different key
	// type than the live key. Non-empty means confirm=true will fail until the
	// operator removes the stale entries — see selectTarget.
	StaleEntries []StaleEntry `json:"stale_entries,omitempty"`
}

// StaleEntry describes one known_hosts entry that ssh_accept_host_key cannot
// safely rewrite or supersede without operator intervention.
type StaleEntry struct {
	Type        string `json:"type"`
	Line        int    `json:"line"`
	Fingerprint string `json:"fingerprint"`
}

// AcceptResult is returned by Accept.
type AcceptResult struct {
	Host       string `json:"host"`
	NewFP      string `json:"new_fingerprint"`
	KnownHosts string `json:"known_hosts"`
	Refreshed  bool   `json:"refreshed"` // false if key was already current; no write occurred
}

// Refresher updates a host's known_hosts entry after a server rebuild.
type Refresher interface {
	Inspect(ctx context.Context, host string) (InspectResult, error)
	Accept(ctx context.Context, host, expectedFingerprint string) (AcceptResult, error)
}

type prodRefresher struct {
	cfg     *config.Config
	scanner Scanner
}

// New returns a production Refresher.
// Pass sshconn.ProdScanner{} as scanner in production.
func New(cfg *config.Config, scanner Scanner) Refresher {
	return &prodRefresher{cfg: cfg, scanner: scanner}
}

func (r *prodRefresher) resolveHost(host string) (config.Host, string, error) {
	h, ok := r.cfg.Hosts[host]
	if !ok {
		return config.Host{}, "", config.UnknownHostError(host)
	}
	if !h.AllowKnownHostsUpdate {
		return config.Host{}, "", fmt.Errorf(
			"host %q does not allow known_hosts updates: "+
				"set allow_known_hosts_update: true in config", host)
	}
	return h, sshconn.ResolveKnownHosts(h.KnownHosts), nil
}

// Inspect scans the host's live key and compares it against the stored entry.
// It does NOT modify known_hosts.
func (r *prodRefresher) Inspect(ctx context.Context, host string) (InspectResult, error) {
	h, path, err := r.resolveHost(host)
	if err != nil {
		return InspectResult{}, err
	}
	liveKey, err := r.scanner.ScanHostKey(ctx, h, r.cfg.Limits)
	if err != nil {
		return InspectResult{}, fmt.Errorf("scanning host key: %w", err)
	}
	newFP := ssh.FingerprintSHA256(liveKey)
	entries, err := probeStoredEntries(path, h.Address)
	if err != nil {
		// Do not report this as CurrentFP=="" (= "no key stored, safe to trust")
		// — that reading is exactly what an operator or LLM would act on to
		// confirm ssh_accept_host_key. A probe failure means we don't know the
		// current state at all, so fail the call instead of guessing.
		return InspectResult{}, fmt.Errorf("checking stored host key: %w", err)
	}
	matched, stale := classifyEntries(entries, liveKey.Type())
	currentFP := ""
	changed := false
	if matched != nil {
		currentFP = ssh.FingerprintSHA256(matched.Key)
		changed = currentFP != newFP
	}
	var staleEntries []StaleEntry
	for _, e := range stale {
		staleEntries = append(staleEntries, StaleEntry{
			Type:        e.Key.Type(),
			Line:        e.Line,
			Fingerprint: ssh.FingerprintSHA256(e.Key),
		})
	}
	return InspectResult{
		Host:         host,
		CurrentFP:    currentFP,
		NewFP:        newFP,
		Changed:      changed,
		KnownHosts:   path,
		StaleEntries: staleEntries,
	}, nil
}

// Accept re-scans the host's live key, verifies it matches expectedFingerprint
// (from a prior Inspect call), and atomically rewrites the known_hosts entry.
func (r *prodRefresher) Accept(ctx context.Context, host, expectedFingerprint string) (AcceptResult, error) {
	if expectedFingerprint == "" {
		return AcceptResult{}, fmt.Errorf(
			"expected_fingerprint is required for confirm: " +
				"call ssh_accept_host_key without confirm=true first to get the fingerprint")
	}
	h, path, err := r.resolveHost(host)
	if err != nil {
		return AcceptResult{}, err
	}
	liveKey, err := r.scanner.ScanHostKey(ctx, h, r.cfg.Limits)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("scanning host key: %w", err)
	}
	newFP := ssh.FingerprintSHA256(liveKey)
	if newFP != expectedFingerprint {
		return AcceptResult{}, fmt.Errorf(
			"host key changed since preview: expected %s but live key is %s — "+
				"call ssh_accept_host_key without confirm=true to re-inspect",
			expectedFingerprint, newFP)
	}
	entries, err := probeStoredEntries(path, h.Address)
	if err != nil {
		// Same reasoning as Inspect: a probe failure must fail the call, never
		// be read as "no key stored" — that would fall through to the append
		// path and leave a superseded key trusted.
		return AcceptResult{}, fmt.Errorf("checking stored host key: %w", err)
	}
	matched, _ := classifyEntries(entries, liveKey.Type())
	if matched != nil && ssh.FingerprintSHA256(matched.Key) == newFP {
		return AcceptResult{Host: host, NewFP: newFP, KnownHosts: path, Refreshed: false}, nil
	}
	// selectTarget re-derives the same partition classifyEntries just computed
	// (no extra file I/O — entries was already read above) and turns an
	// ambiguous state (multiple entries, or one of a different type) into an
	// error instead of a guess. See its doc comment for why guessing here is
	// the exact bug this package exists to close.
	lineNum, err := selectTarget(entries, liveKey, path, h.Address)
	if err != nil {
		return AcceptResult{}, err
	}
	var expectKey ssh.PublicKey
	if matched != nil {
		expectKey = matched.Key
	}
	if err := writeKnownHostsEntry(path, h.Address, liveKey, lineNum, expectKey); err != nil {
		return AcceptResult{}, fmt.Errorf("rewriting known_hosts: %w", err)
	}
	return AcceptResult{Host: host, NewFP: newFP, KnownHosts: path, Refreshed: true}, nil
}

// storedEntry is one known_hosts line matching a probed hostport.
type storedEntry struct {
	Key  ssh.PublicKey
	Line int // 1-indexed
}

// probeStoredEntries returns every known_hosts entry matching hostport, in
// file order. Returns (nil, nil) when nothing matches — the caller should
// append.
//
// A probe that fails for any other reason is an error, never "not stored":
// reporting "not stored" would make the caller append a *second* entry for
// the host, and knownhosts accepts a match against any listed entry — so a
// superseded key would stay trusted, which is the exact opposite of what
// ssh_accept_host_key is for.
func probeStoredEntries(path, hostport string) ([]storedEntry, error) {
	if hostport == "" {
		// An empty hostport makes knownhosts fall back to remote.String() (":0"),
		// which matches nothing and returns an empty KeyError.Want — i.e. "not
		// stored". That is the exact conflation this function exists to avoid
		// (see the doc comment above), so reject it explicitly rather than
		// relying solely on config.normalizeAddress to keep hostport non-empty.
		return nil, fmt.Errorf("probing known_hosts %q: empty hostport", path)
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// rootcanal will not create the operator's trust store — reporting
			// this the same way as a corrupt/unreadable file would leave the
			// operator guessing why ssh_accept_host_key refuses to bootstrap a
			// brand new known_hosts file.
			return nil, fmt.Errorf(
				"known_hosts %q does not exist; create it first (e.g. \"touch %s && chmod 600 %s\") — "+
					"rootcanal will not create your trust store", path, path, path)
		}
		return nil, fmt.Errorf("checking known_hosts %q: %w", path, err)
	}
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("loading known_hosts %q: %w", path, err)
	}
	probeErr := cb(hostport, sshconn.ProbeRemote(), probeKey{})
	if probeErr == nil {
		// probeKey's fake marshaled bytes matched a stored key — i.e. an entry
		// definitely exists. Reporting "not stored" here would be the exact
		// duplicate-append bug this function exists to prevent, so this is an
		// error, not a nil result, even though it should never happen in
		// practice (probeKey.Marshal() cannot parse as a real public key).
		return nil, fmt.Errorf("probing known_hosts %q for %q: probe key unexpectedly matched a stored entry", path, hostport)
	}
	var kerr *knownhosts.KeyError
	if !errors.As(probeErr, &kerr) {
		return nil, fmt.Errorf("probing known_hosts %q for %q: %w", path, hostport, probeErr)
	}
	entries := make([]storedEntry, 0, len(kerr.Want))
	for _, kk := range kerr.Want {
		if kk.Filename != path {
			continue
		}
		entries = append(entries, storedEntry{Key: kk.Key, Line: kk.Line})
	}
	return entries, nil
}

// classifyEntries partitions entries into the single entry matching
// liveKeyType (safe to supersede) and every other stale entry. matched is nil
// whenever entries doesn't contain exactly one liveKeyType match — either
// none exist, more than one exists, or the sole entry differs in type —
// because none of those states says unambiguously which key to replace.
func classifyEntries(entries []storedEntry, liveKeyType string) (matched *storedEntry, stale []storedEntry) {
	if len(entries) == 1 && entries[0].Key.Type() == liveKeyType {
		return &entries[0], nil
	}
	return nil, entries
}

// selectTarget decides what Accept should do with entries found for hostport,
// given the freshly scanned liveKey. Returns the 1-indexed line to rewrite, or
// 0 to append when nothing is stored at all. Errors when more than one entry
// exists, or when the sole entry is of a different key type than liveKey —
// either case means "which key is being superseded" is ambiguous, and
// appending anyway would leave an unrelated key trusted indefinitely.
func selectTarget(entries []storedEntry, liveKey ssh.PublicKey, path, hostport string) (int, error) {
	matched, stale := classifyEntries(entries, liveKey.Type())
	if matched != nil {
		return matched.Line, nil
	}
	if len(stale) == 0 {
		return 0, nil
	}
	if len(stale) > 1 {
		return 0, fmt.Errorf(
			"known_hosts %q has %d stored entries for %q; refusing to guess which one to supersede — "+
				"remove the stale entries (\"ssh-keygen -R %s -f %s\") and retry",
			path, len(stale), hostport, hostport, path)
	}
	e := stale[0]
	return 0, fmt.Errorf(
		"known_hosts %q line %d already trusts a %s key for %q but the live key is %s; "+
			"rootcanal will not append a second entry that leaves the old key trusted — "+
			"remove the stale entry (\"ssh-keygen -R %s -f %s\") and retry",
		path, e.Line, e.Key.Type(), hostport, liveKey.Type(), hostport, path)
}

// probeKey is a minimal ssh.PublicKey used only to trigger knownhosts.KeyError.
// It matches the same pattern used in internal/sshconn/hostkey.go.
type probeKey struct{}

func (probeKey) Type() string                            { return "ecdsa-sha2-nistp256" }
func (probeKey) Marshal() []byte                         { return make([]byte, 51) }
func (probeKey) Verify(_ []byte, _ *ssh.Signature) error { return fmt.Errorf("probe key") }
