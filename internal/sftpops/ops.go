package sftpops

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
	"github.com/zorak1103/rootcanal/internal/config"
	"golang.org/x/crypto/ssh"
)

// Entry is a single directory listing entry.
type Entry struct {
	Name    string
	Size    int64
	Mode    fs.FileMode
	ModTime time.Time
	IsDir   bool
}

// Ops is the interface for SFTP file operations against pre-declared hosts.
type Ops interface {
	Read(ctx context.Context, host, path string, maxBytes int) (data []byte, isBinary bool, err error)
	Write(ctx context.Context, host, path string, content []byte, mode fs.FileMode, atomic bool) error
	List(ctx context.Context, host, path string) ([]Entry, error)
}

// sftpClientIface is the subset of *sftp.Client used by this package.
// Returning io.ReadCloser / io.WriteCloser (rather than *sftp.File) keeps the
// interface narrow enough for test fakes.
type sftpClientIface interface {
	Open(path string) (io.ReadCloser, error)
	OpenFile(path string, flag int) (io.WriteCloser, error)
	Chmod(path string, mode fs.FileMode) error
	ReadDir(path string) ([]fs.FileInfo, error)
	Rename(oldpath, newpath string) error
	Remove(path string) error
	// RealPath asks the server to canonicalize path, resolving any symlinks
	// along the way. Used to re-check sftp_allowed_prefixes against the real
	// target after the lexical path.Clean check in validateSFTPPath, closing
	// the gap where a symlink inside an allowed prefix points outside it.
	RealPath(path string) (string, error)
	Close() error
}

// realSFTPClient adapts *sftp.Client to sftpClientIface.
type realSFTPClient struct{ *sftp.Client }

func (r *realSFTPClient) Open(p string) (io.ReadCloser, error) { return r.Client.Open(p) }
func (r *realSFTPClient) OpenFile(p string, f int) (io.WriteCloser, error) {
	return r.Client.OpenFile(p, f)
}
func (r *realSFTPClient) ReadDir(p string) ([]fs.FileInfo, error) { return r.Client.ReadDir(p) }
func (r *realSFTPClient) RealPath(p string) (string, error)       { return r.Client.RealPath(p) }

// poolGetter abstracts pool.Get so sftpops can be tested without a real Pool.
type poolGetter func(ctx context.Context, host string) (*ssh.Client, func(), error)

type ops struct {
	cfg       *config.Config
	get       poolGetter
	newClient func(*ssh.Client) (sftpClientIface, error)
}

// New returns an Ops backed by the given pool.
func New(cfg *config.Config, pool interface {
	Get(context.Context, string) (*ssh.Client, func(), error)
}) Ops {
	return newOps(cfg, pool.Get, defaultNewClient)
}

func newOps(cfg *config.Config, get poolGetter, nc func(*ssh.Client) (sftpClientIface, error)) *ops {
	return &ops{cfg: cfg, get: get, newClient: nc}
}

func defaultNewClient(c *ssh.Client) (sftpClientIface, error) {
	cl, err := sftp.NewClient(c)
	if err != nil {
		return nil, err
	}
	return &realSFTPClient{cl}, nil
}

// validateSFTPPath checks that SFTP is enabled for the host and that in is a
// safe absolute Unix path. It returns the path.Clean form of in.
func (o *ops) validateSFTPPath(host, in string) (string, error) {
	h, ok := o.cfg.Hosts[host]
	if !ok {
		return "", config.UnknownHostError(host)
	}
	if !h.SFTPEnabled {
		return "", fmt.Errorf("host %q: SFTP not enabled", host)
	}
	cleaned := path.Clean(in)
	if !path.IsAbs(cleaned) {
		return "", fmt.Errorf("path %q must be absolute", in)
	}
	if !pathMatchesAnyPrefix(cleaned, h.SFTPAllowedPrefixes) {
		return "", fmt.Errorf("path %q is not under any allowed prefix", cleaned)
	}
	return cleaned, nil
}

// pathMatchesAnyPrefix reports whether p equals a prefix or lives under it.
// "/" matches every absolute path. An empty prefixes slice matches nothing.
func pathMatchesAnyPrefix(p string, prefixes []string) bool {
	for _, pfx := range prefixes {
		if pfx == "/" || p == pfx || strings.HasPrefix(p, pfx+"/") {
			return true
		}
	}
	return false
}

// resolveRealPath asks the SFTP server to canonicalize cleanedPath, following
// any symlinks along the way. It tries the full path first (covers existing
// files, directories, and symlinks); if that fails — e.g. the leaf does not
// exist yet, which is normal when writing a new file — it falls back to
// resolving just the parent directory and rejoining the original basename.
// If both fail, resolution is skipped and cleanedPath is returned unchanged:
// RealPath is a hardening layer on top of the lexical check, not a hard
// requirement for basic operation (some SFTP server implementations may
// restrict or reject REALPATH requests).
func resolveRealPath(client sftpClientIface, cleanedPath string) string {
	if resolved, err := client.RealPath(cleanedPath); err == nil {
		return path.Clean(resolved)
	}
	dir := path.Dir(cleanedPath)
	if realDir, err := client.RealPath(dir); err == nil {
		return path.Join(path.Clean(realDir), path.Base(cleanedPath))
	}
	return cleanedPath
}

// checkResolvedPath re-validates the server-resolved form of cleanedPath
// against the host's allowed prefixes. validateSFTPPath only checks the
// lexical (path.Clean'd) form of the caller-supplied path; if a component of
// that path is a symlink pointing outside the allowed prefixes, the lexical
// check alone would let it through. This closes that gap. A small TOCTOU
// window remains if the symlink is swapped between this check and the actual
// Open/OpenFile/ReadDir call — SFTP has no atomic "open only if it resolves
// under X" primitive, so that residual window is accepted.
func (o *ops) checkResolvedPath(host, cleanedPath, resolved string) error {
	if resolved == cleanedPath {
		return nil // nothing to re-check; the lexical check already covers this
	}
	h := o.cfg.Hosts[host] // presence already guaranteed by validateSFTPPath
	if !pathMatchesAnyPrefix(resolved, h.SFTPAllowedPrefixes) {
		return fmt.Errorf("path %q resolves to %q via a symlink, which is outside any allowed prefix", cleanedPath, resolved)
	}
	return nil
}

// openSFTP is the shared dial-and-open helper used by all three operations.
func (o *ops) openSFTP(ctx context.Context, host string) (sftpClientIface, func(), error) {
	client, release, err := o.get(ctx, host)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %q: %w", host, err)
	}
	sftpClient, err := o.newClient(client)
	if err != nil {
		release()
		return nil, nil, fmt.Errorf("opening SFTP on %q: %w", host, err)
	}
	cleanup := func() {
		_ = sftpClient.Close()
		release()
	}
	return sftpClient, cleanup, nil
}

func (o *ops) Read(ctx context.Context, host, p string, maxBytes int) (data []byte, isBinary bool, err error) {
	cleanedPath, err := o.validateSFTPPath(host, p)
	if err != nil {
		return nil, false, err
	}

	sftpClient, cleanup, err := o.openSFTP(ctx, host)
	if err != nil {
		return nil, false, err
	}
	defer cleanup()

	if resolveErr := o.checkResolvedPath(host, cleanedPath, resolveRealPath(sftpClient, cleanedPath)); resolveErr != nil {
		return nil, false, resolveErr
	}

	f, err := sftpClient.Open(cleanedPath)
	if err != nil {
		return nil, false, fmt.Errorf("opening %q on %q: %w", cleanedPath, host, err)
	}
	defer f.Close()

	limit := o.cfg.Limits.SFTPMaxReadBytes
	if maxBytes > 0 && maxBytes < limit {
		limit = maxBytes
	}

	data, err = io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil {
		return nil, false, fmt.Errorf("reading %q on %q: %w", p, host, err)
	}

	isBinary = bytes.IndexByte(data, 0) != -1 || !utf8.Valid(data)
	return data, isBinary, nil
}

func (o *ops) Write(ctx context.Context, host, fpath string, content []byte, mode fs.FileMode, atomicWrite bool) error {
	cleanedPath, err := o.validateSFTPPath(host, fpath)
	if err != nil {
		return err
	}

	limit := o.cfg.Limits.SFTPMaxWriteBytes
	if limit > 0 && len(content) > limit {
		return fmt.Errorf("content size %d exceeds SFTP write limit of %d bytes", len(content), limit)
	}

	sftpClient, cleanup, err := o.openSFTP(ctx, host)
	if err != nil {
		return err
	}
	defer cleanup()

	if resolveErr := o.checkResolvedPath(host, cleanedPath, resolveRealPath(sftpClient, cleanedPath)); resolveErr != nil {
		return resolveErr
	}

	writePath := cleanedPath
	if atomicWrite {
		dir := path.Dir(cleanedPath)
		base := path.Base(cleanedPath)
		writePath = dir + "/." + base + ".rootcanal.tmp"
		// Validate the temp path passes prefix check too.
		if _, tmpErr := o.validateSFTPPath(host, writePath); tmpErr != nil {
			return fmt.Errorf("atomic write: temp path %q not in allowed prefixes: %w", writePath, tmpErr)
		}
	}

	f, err := sftpClient.OpenFile(writePath, sftpWriteFlags)
	if err != nil {
		return fmt.Errorf("opening %q on %q for write: %w", writePath, host, err)
	}

	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		if atomicWrite {
			_ = sftpClient.Remove(writePath)
		}
		return fmt.Errorf("writing to %q on %q: %w", writePath, host, err)
	}

	// Close commits the SFTP write; errors here indicate data loss.
	if err := f.Close(); err != nil {
		if atomicWrite {
			_ = sftpClient.Remove(writePath)
		}
		return fmt.Errorf("closing %q on %q after write: %w", writePath, host, err)
	}

	if atomicWrite {
		if err := sftpClient.Rename(writePath, cleanedPath); err != nil {
			_ = sftpClient.Remove(writePath)
			return fmt.Errorf("atomic rename %q → %q on %q: %w", writePath, cleanedPath, host, err)
		}
	}

	if mode != 0 {
		if uint32(mode)&0o7000 != 0 {
			return fmt.Errorf("refusing chmod %q on %q: special bits set in mode %04o", cleanedPath, host, uint32(mode))
		}
		if err := sftpClient.Chmod(cleanedPath, mode); err != nil {
			return fmt.Errorf("chmod %q on %q: %w", cleanedPath, host, err)
		}
	}
	return nil
}

const sftpWriteFlags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC

func (o *ops) List(ctx context.Context, host, p string) ([]Entry, error) {
	cleanedPath, err := o.validateSFTPPath(host, p)
	if err != nil {
		return nil, err
	}

	sftpClient, cleanup, err := o.openSFTP(ctx, host)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if resolveErr := o.checkResolvedPath(host, cleanedPath, resolveRealPath(sftpClient, cleanedPath)); resolveErr != nil {
		return nil, resolveErr
	}

	infos, err := sftpClient.ReadDir(cleanedPath)
	if err != nil {
		return nil, fmt.Errorf("listing %q on %q: %w", cleanedPath, host, err)
	}

	entries := make([]Entry, len(infos))
	for i, info := range infos {
		entries[i] = Entry{
			Name:    info.Name(),
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime(),
			IsDir:   info.IsDir(),
		}
	}
	return entries, nil
}
