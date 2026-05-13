package sftpops

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
	"gitlab.com/zorak1103/rootcanal/internal/config"
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
	Write(ctx context.Context, host, path string, content []byte, mode fs.FileMode) error
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
	Close() error
}

// realSFTPClient adapts *sftp.Client to sftpClientIface.
type realSFTPClient struct{ *sftp.Client }

func (r *realSFTPClient) Open(path string) (io.ReadCloser, error)       { return r.Client.Open(path) }
func (r *realSFTPClient) OpenFile(path string, f int) (io.WriteCloser, error) {
	return r.Client.OpenFile(path, f)
}
func (r *realSFTPClient) ReadDir(path string) ([]fs.FileInfo, error) { return r.Client.ReadDir(path) }

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
		sftpClient.Close()
		release()
	}
	return sftpClient, cleanup, nil
}

func (o *ops) Read(ctx context.Context, host, path string, maxBytes int) ([]byte, bool, error) {
	sftpClient, cleanup, err := o.openSFTP(ctx, host)
	if err != nil {
		return nil, false, err
	}
	defer cleanup()

	f, err := sftpClient.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("opening %q on %q: %w", path, host, err)
	}
	defer f.Close()

	limit := o.cfg.Limits.SFTPMaxReadBytes
	if maxBytes > 0 && maxBytes < limit {
		limit = maxBytes
	}

	data, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil {
		return nil, false, fmt.Errorf("reading %q on %q: %w", path, host, err)
	}

	isBinary := bytes.IndexByte(data, 0) != -1 || !utf8.Valid(data)
	return data, isBinary, nil
}

func (o *ops) Write(ctx context.Context, host, path string, content []byte, mode fs.FileMode) error {
	limit := o.cfg.Limits.SFTPMaxWriteBytes
	if limit > 0 && len(content) > limit {
		return fmt.Errorf("content size %d exceeds SFTP write limit of %d bytes", len(content), limit)
	}

	sftpClient, cleanup, err := o.openSFTP(ctx, host)
	if err != nil {
		return err
	}
	defer cleanup()

	f, err := sftpClient.OpenFile(path, sftpWriteFlags)
	if err != nil {
		return fmt.Errorf("opening %q on %q for write: %w", path, host, err)
	}
	defer f.Close()

	if _, err := f.Write(content); err != nil {
		return fmt.Errorf("writing to %q on %q: %w", path, host, err)
	}

	if mode != 0 {
		if err := sftpClient.Chmod(path, mode); err != nil {
			return fmt.Errorf("chmod %q on %q: %w", path, host, err)
		}
	}
	return nil
}

const sftpWriteFlags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC

func (o *ops) List(ctx context.Context, host, path string) ([]Entry, error) {
	sftpClient, cleanup, err := o.openSFTP(ctx, host)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	infos, err := sftpClient.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("listing %q on %q: %w", path, host, err)
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
