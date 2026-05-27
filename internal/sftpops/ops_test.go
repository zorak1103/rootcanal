package sftpops

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"gitlab.com/zorak1103/rootcanal/internal/config"
	"gitlab.com/zorak1103/rootcanal/internal/hostpool"
	"gitlab.com/zorak1103/rootcanal/internal/sshconn"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ---- fake pool getter ----

func okPool(c *ssh.Client) poolGetter {
	return func(_ context.Context, _ string) (*ssh.Client, func(), error) {
		return c, func() {}, nil
	}
}

func errPool(err error) poolGetter {
	return func(_ context.Context, _ string) (*ssh.Client, func(), error) {
		return nil, nil, err
	}
}

// ---- fake sftpClientIface ----

type fakeFS struct {
	files     map[string][]byte
	dirs      map[string][]fs.FileInfo
	openErr   error
	writeErr  error
	chmodErr  error
	listErr   error
	renameErr error
	removeErr error
}

func newFakeFS() *fakeFS {
	return &fakeFS{
		files: make(map[string][]byte),
		dirs:  make(map[string][]fs.FileInfo),
	}
}

func (f *fakeFS) Open(path string) (io.ReadCloser, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	data, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeFS) OpenFile(path string, _ int) (io.WriteCloser, error) {
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	return &captureBuf{path: path, fs: f}, nil
}

func (f *fakeFS) Chmod(_ string, _ fs.FileMode) error { return f.chmodErr }

func (f *fakeFS) ReadDir(path string) ([]fs.FileInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	infos, ok := f.dirs[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return infos, nil
}

func (f *fakeFS) Close() error { return nil }

func (f *fakeFS) Rename(oldpath, newpath string) error {
	if f.renameErr != nil {
		return f.renameErr
	}
	data, ok := f.files[oldpath]
	if !ok {
		return os.ErrNotExist
	}
	f.files[newpath] = data
	delete(f.files, oldpath)
	return nil
}

func (f *fakeFS) Remove(path string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	delete(f.files, path)
	return nil
}

// captureBuf captures Write calls and stores them in fakeFS on Close.
type captureBuf struct {
	path string
	buf  bytes.Buffer
	fs   *fakeFS
}

func (b *captureBuf) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *captureBuf) Close() error {
	b.fs.files[b.path] = b.buf.Bytes()
	return nil
}

// fakeFileInfo implements fs.FileInfo.
type fakeFileInfo struct {
	name  string
	size  int64
	mode  fs.FileMode
	isDir bool
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.isDir }
func (f fakeFileInfo) Sys() any           { return nil }

// ---- helpers ----

func minCfg() *config.Config {
	return &config.Config{
		Limits: config.Limits{
			SFTPMaxReadBytes:  5 << 20,
			SFTPMaxWriteBytes: 25 << 20,
		},
		Hosts: map[string]config.Host{
			// SFTPAllowedPrefixes: ["/"] grants access to any absolute path.
			// Tests that exercise specific path restrictions override this inline.
			"h": {Address: "h:22", User: "u", SFTPEnabled: true, SFTPAllowedPrefixes: []string{"/"}},
		},
	}
}

func newTestOps(cfg *config.Config, get poolGetter, ffs *fakeFS) *ops {
	return newOps(cfg, get, func(_ *ssh.Client) (sftpClientIface, error) {
		return ffs, nil
	})
}

// ---- fakePoolImpl for testing New() ----

type fakePoolImpl struct{}

func (f *fakePoolImpl) Get(_ context.Context, _ string) (*ssh.Client, func(), error) {
	return nil, func() {}, errors.New("fake: no connection")
}

// ---- New ----

func TestNew_ReturnsNonNil(t *testing.T) {
	ops := New(minCfg(), &fakePoolImpl{})
	if ops == nil {
		t.Fatal("New must return a non-nil Ops")
	}
}

// ---- Read tests ----

func TestRead_Success(t *testing.T) {
	ffs := newFakeFS()
	ffs.files["/etc/hosts"] = []byte("127.0.0.1 localhost\n")

	o := newTestOps(minCfg(), okPool(nil), ffs)
	data, binary, err := o.Read(context.Background(), "h", "/etc/hosts", 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if binary {
		t.Error("expected isBinary=false for text content")
	}
	if string(data) != "127.0.0.1 localhost\n" {
		t.Errorf("Read() = %q, want exact content", data)
	}
}

func TestRead_BinaryDetection(t *testing.T) {
	ffs := newFakeFS()
	ffs.files["/bin/null"] = []byte{0x00, 0x01, 0x02} // contains null byte

	o := newTestOps(minCfg(), okPool(nil), ffs)
	_, binary, err := o.Read(context.Background(), "h", "/bin/null", 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !binary {
		t.Error("expected isBinary=true for content with null byte")
	}
}

func TestRead_InvalidUTF8(t *testing.T) {
	ffs := newFakeFS()
	ffs.files["/bad.txt"] = []byte{0xff, 0xfe, 'x'} // invalid UTF-8

	o := newTestOps(minCfg(), okPool(nil), ffs)
	_, binary, err := o.Read(context.Background(), "h", "/bad.txt", 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !binary {
		t.Error("expected isBinary=true for invalid UTF-8")
	}
}

func TestRead_MaxBytesRespected(t *testing.T) {
	ffs := newFakeFS()
	ffs.files["/large.txt"] = bytes.Repeat([]byte("x"), 1000)

	o := newTestOps(minCfg(), okPool(nil), ffs)
	data, _, err := o.Read(context.Background(), "h", "/large.txt", 10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(data) != 10 {
		t.Errorf("Read() returned %d bytes, want 10", len(data))
	}
}

func TestRead_PoolError(t *testing.T) {
	o := newTestOps(minCfg(), errPool(errors.New("dial failed")), newFakeFS())
	_, _, err := o.Read(context.Background(), "h", "/etc/hosts", 0)
	if err == nil {
		t.Fatal("expected pool error")
	}
}

func TestRead_FileNotFound(t *testing.T) {
	o := newTestOps(minCfg(), okPool(nil), newFakeFS())
	_, _, err := o.Read(context.Background(), "h", "/no/such/file", 0)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestRead_OpenError(t *testing.T) {
	ffs := newFakeFS()
	ffs.openErr = errors.New("permission denied")
	o := newTestOps(minCfg(), okPool(nil), ffs)
	_, _, err := o.Read(context.Background(), "h", "/etc/shadow", 0)
	if err == nil {
		t.Fatal("expected open error")
	}
}

// ---- Write tests ----

func TestWrite_Success(t *testing.T) {
	ffs := newFakeFS()
	o := newTestOps(minCfg(), okPool(nil), ffs)

	err := o.Write(context.Background(), "h", "/tmp/out.txt", []byte("hello"), 0, false)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if string(ffs.files["/tmp/out.txt"]) != "hello" {
		t.Errorf("file content = %q, want %q", ffs.files["/tmp/out.txt"], "hello")
	}
}

func TestWrite_WithMode(t *testing.T) {
	ffs := newFakeFS()
	o := newTestOps(minCfg(), okPool(nil), ffs)

	err := o.Write(context.Background(), "h", "/tmp/script.sh", []byte("#!/bin/sh"), 0755, false)
	if err != nil {
		t.Fatalf("Write with mode: %v", err)
	}
}

func TestWrite_ChmodError(t *testing.T) {
	ffs := newFakeFS()
	ffs.chmodErr = errors.New("chmod failed")
	o := newTestOps(minCfg(), okPool(nil), ffs)

	err := o.Write(context.Background(), "h", "/tmp/f.sh", []byte("x"), 0755, false)
	if err == nil {
		t.Fatal("expected chmod error")
	}
}

func TestWrite_SizeLimitExceeded(t *testing.T) {
	cfg := minCfg()
	cfg.Limits.SFTPMaxWriteBytes = 4

	o := newTestOps(cfg, okPool(nil), newFakeFS())
	err := o.Write(context.Background(), "h", "/tmp/big.bin", bytes.Repeat([]byte("x"), 5), 0, false)
	if err == nil {
		t.Fatal("expected size limit error")
	}
}

func TestWrite_PoolError(t *testing.T) {
	o := newTestOps(minCfg(), errPool(errors.New("no conn")), newFakeFS())
	err := o.Write(context.Background(), "h", "/tmp/f", []byte("x"), 0, false)
	if err == nil {
		t.Fatal("expected pool error")
	}
}

func TestWrite_OpenFileError(t *testing.T) {
	ffs := newFakeFS()
	ffs.writeErr = errors.New("read only filesystem")
	o := newTestOps(minCfg(), okPool(nil), ffs)
	err := o.Write(context.Background(), "h", "/etc/locked", []byte("x"), 0, false)
	if err == nil {
		t.Fatal("expected open error")
	}
}

// ---- List tests ----

func TestList_Success(t *testing.T) {
	ffs := newFakeFS()
	ffs.dirs["/tmp"] = []fs.FileInfo{
		fakeFileInfo{name: "file.txt", size: 42, mode: 0644},
		fakeFileInfo{name: "subdir", isDir: true, mode: fs.ModeDir | 0755},
	}

	o := newTestOps(minCfg(), okPool(nil), ffs)
	entries, err := o.List(context.Background(), "h", "/tmp")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List() returned %d entries, want 2", len(entries))
	}
	if entries[0].Name != "file.txt" || entries[0].Size != 42 {
		t.Errorf("unexpected first entry: %+v", entries[0])
	}
	if !entries[1].IsDir {
		t.Error("expected second entry to be a directory")
	}
}

func TestList_PoolError(t *testing.T) {
	o := newTestOps(minCfg(), errPool(errors.New("dial failed")), newFakeFS())
	_, err := o.List(context.Background(), "h", "/tmp")
	if err == nil {
		t.Fatal("expected pool error")
	}
}

func TestList_ReadDirError(t *testing.T) {
	ffs := newFakeFS()
	ffs.listErr = errors.New("directory not found")
	o := newTestOps(minCfg(), okPool(nil), ffs)
	_, err := o.List(context.Background(), "h", "/no/dir")
	if err == nil {
		t.Fatal("expected readdir error")
	}
}

// ---- Path validation tests ----

func TestValidateSFTPPath_SFTPDisabled(t *testing.T) {
	cfg := &config.Config{
		Limits: config.Limits{SFTPMaxReadBytes: 5 << 20, SFTPMaxWriteBytes: 25 << 20},
		Hosts:  map[string]config.Host{"h": {Address: "h:22", User: "u", SFTPEnabled: false}},
	}
	o := newTestOps(cfg, okPool(nil), newFakeFS())
	_, _, err := o.Read(context.Background(), "h", "/etc/hosts", 0)
	if err == nil || !containsStr(err.Error(), "not enabled") {
		t.Fatalf("expected SFTP-disabled error, got: %v", err)
	}
}

func TestValidateSFTPPath_RelativePath(t *testing.T) {
	o := newTestOps(minCfg(), okPool(nil), newFakeFS())
	_, _, err := o.Read(context.Background(), "h", "../etc/passwd", 0)
	if err == nil || !containsStr(err.Error(), "must be absolute") {
		t.Fatalf("expected absolute-path error, got: %v", err)
	}
}

func TestValidateSFTPPath_TraversalCleaned_ThenAllowlistRejected(t *testing.T) {
	cfg := &config.Config{
		Limits: config.Limits{SFTPMaxReadBytes: 5 << 20, SFTPMaxWriteBytes: 25 << 20},
		Hosts: map[string]config.Host{
			"h": {Address: "h:22", User: "u", SFTPEnabled: true, SFTPAllowedPrefixes: []string{"/srv/app"}},
		},
	}
	o := newTestOps(cfg, okPool(nil), newFakeFS())
	// /srv/app/../etc/passwd cleans to /etc/passwd — outside the allowlist.
	_, _, err := o.Read(context.Background(), "h", "/srv/app/../etc/passwd", 0)
	if err == nil || !containsStr(err.Error(), "not under any allowed prefix") {
		t.Fatalf("expected allowlist error after traversal cleaning, got: %v", err)
	}
}

func TestValidateSFTPPath_AllowlistMatch(t *testing.T) {
	ffs := newFakeFS()
	ffs.files["/srv/app/config.json"] = []byte(`{"ok":true}`)
	cfg := &config.Config{
		Limits: config.Limits{SFTPMaxReadBytes: 5 << 20, SFTPMaxWriteBytes: 25 << 20},
		Hosts: map[string]config.Host{
			"h": {Address: "h:22", User: "u", SFTPEnabled: true, SFTPAllowedPrefixes: []string{"/srv/app"}},
		},
	}
	o := newTestOps(cfg, okPool(nil), ffs)
	_, _, err := o.Read(context.Background(), "h", "/srv/app/config.json", 0)
	if err != nil {
		t.Fatalf("unexpected error for path inside allowlist: %v", err)
	}
}

func TestValidateSFTPPath_AllowlistPrefixEdgeCase(t *testing.T) {
	// /srv/apple must NOT match prefix /srv/app.
	cfg := &config.Config{
		Limits: config.Limits{SFTPMaxReadBytes: 5 << 20, SFTPMaxWriteBytes: 25 << 20},
		Hosts: map[string]config.Host{
			"h": {Address: "h:22", User: "u", SFTPEnabled: true, SFTPAllowedPrefixes: []string{"/srv/app"}},
		},
	}
	o := newTestOps(cfg, okPool(nil), newFakeFS())
	_, _, err := o.Read(context.Background(), "h", "/srv/apple/secret", 0)
	if err == nil || !containsStr(err.Error(), "not under any allowed prefix") {
		t.Fatalf("expected allowlist error for /srv/apple (edge case), got: %v", err)
	}
}

func TestValidateSFTPPath_AllowlistExactMatch(t *testing.T) {
	// Path equal to the prefix itself is allowed (e.g. listing the prefix dir).
	ffs := newFakeFS()
	ffs.dirs["/srv/app"] = nil
	cfg := &config.Config{
		Limits: config.Limits{SFTPMaxReadBytes: 5 << 20, SFTPMaxWriteBytes: 25 << 20},
		Hosts: map[string]config.Host{
			"h": {Address: "h:22", User: "u", SFTPEnabled: true, SFTPAllowedPrefixes: []string{"/srv/app"}},
		},
	}
	o := newTestOps(cfg, okPool(nil), ffs)
	_, err := o.List(context.Background(), "h", "/srv/app")
	if err != nil {
		t.Fatalf("unexpected error for path equal to prefix: %v", err)
	}
}

func TestValidateSFTPPath_EmptyAllowlist_DeniesAll(t *testing.T) {
	// Empty or absent sftp_allowed_prefixes rejects every path.
	cfg := &config.Config{
		Limits: config.Limits{SFTPMaxReadBytes: 5 << 20, SFTPMaxWriteBytes: 25 << 20},
		Hosts:  map[string]config.Host{"h": {Address: "h:22", User: "u", SFTPEnabled: true}},
	}
	o := newTestOps(cfg, okPool(nil), newFakeFS())
	_, _, err := o.Read(context.Background(), "h", "/etc/hosts", 0)
	if err == nil || !containsStr(err.Error(), "not under any allowed prefix") {
		t.Fatalf("expected allowlist error for empty prefix list, got: %v", err)
	}
}

func TestValidateSFTPPath_SlashPrefix_AllowsAnyAbsolute(t *testing.T) {
	// "/" as a prefix is the explicit "allow all absolute paths" escape hatch.
	ffs := newFakeFS()
	ffs.files["/etc/hosts"] = []byte("127.0.0.1 localhost\n")
	o := newTestOps(minCfg(), okPool(nil), ffs) // minCfg uses ["/"]
	_, _, err := o.Read(context.Background(), "h", "/etc/hosts", 0)
	if err != nil {
		t.Fatalf("unexpected error with prefix [\"/\"]: %v", err)
	}
}

func TestValidateSFTPPath_Write_Disabled(t *testing.T) {
	cfg := &config.Config{
		Limits: config.Limits{SFTPMaxReadBytes: 5 << 20, SFTPMaxWriteBytes: 25 << 20},
		Hosts:  map[string]config.Host{"h": {Address: "h:22", User: "u", SFTPEnabled: false}},
	}
	o := newTestOps(cfg, okPool(nil), newFakeFS())
	err := o.Write(context.Background(), "h", "/tmp/f", []byte("x"), 0, false)
	if err == nil || !containsStr(err.Error(), "not enabled") {
		t.Fatalf("expected SFTP-disabled error, got: %v", err)
	}
}

func TestValidateSFTPPath_List_RelativePath(t *testing.T) {
	o := newTestOps(minCfg(), okPool(nil), newFakeFS())
	_, err := o.List(context.Background(), "h", "relative/dir")
	if err == nil || !containsStr(err.Error(), "must be absolute") {
		t.Fatalf("expected absolute-path error, got: %v", err)
	}
}

func containsStr(s, sub string) bool {
	return strings.Contains(s, sub)
}

// ---- SFTP integration test (covers defaultNewClient + realSFTPClient adapters) ----

func startSFTPServer(t *testing.T) (addr, khPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverSigner, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	srvCfg := &ssh.ServerConfig{NoClientAuth: true}
	srvCfg.AddHostKey(serverSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr = ln.Addr().String()
	t.Cleanup(func() { ln.Close() })

	khPath = filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, serverSigner.PublicKey())
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSFTPConn(conn, srvCfg)
		}
	}()
	return addr, khPath
}

func serveSFTPConn(conn net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go func(ch ssh.Channel, reqs <-chan *ssh.Request) {
			defer ch.Close()
			for req := range reqs {
				if req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp" {
					req.Reply(true, nil)
					srv, err := sftp.NewServer(ch)
					if err != nil {
						return
					}
					srv.Serve()
					return
				}
				req.Reply(false, nil)
			}
		}(ch, requests)
	}
}

func TestOps_SFTPIntegration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("integration test uses OS temp paths; Unix path validation rejects Windows paths")
	}
	addr, khPath := startSFTPServer(t)
	t.Setenv("TEST_SFTP_INT_PASS", "irrelevant")
	dir := t.TempDir()

	cfg := &config.Config{
		Limits: config.Limits{
			SFTPMaxReadBytes:  5 << 20,
			SFTPMaxWriteBytes: 25 << 20,
		},
		Hosts: map[string]config.Host{
			"sftp-test": {
				Address:             addr,
				User:                "u",
				KnownHosts:          khPath,
				Auth:                config.Auth{Type: "password", PasswordEnv: "TEST_SFTP_INT_PASS"},
				SFTPEnabled:         true,
				SFTPAllowedPrefixes: []string{"/"},
			},
		},
	}

	pool := hostpool.New(cfg, sshconn.ProdDialer{})
	defer pool.Close()

	ops := New(cfg, pool)

	// Write a file via SFTP.
	testFile := filepath.Join(dir, "hello.txt")
	if err := ops.Write(context.Background(), "sftp-test", testFile, []byte("hello sftp\n"), 0, false); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read it back.
	data, binary, err := ops.Read(context.Background(), "sftp-test", testFile, 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if binary {
		t.Error("expected text, not binary")
	}
	if string(data) != "hello sftp\n" {
		t.Errorf("Read = %q, want %q", data, "hello sftp\n")
	}

	// List the directory.
	entries, err := ops.List(context.Background(), "sftp-test", dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "hello.txt" {
			found = true
		}
	}
	if !found {
		t.Error("hello.txt not found in directory listing")
	}
}

// ---- newClient error path ----

func TestOpenSFTP_NewClientError(t *testing.T) {
	cfg := minCfg()
	badClientFactory := func(_ *ssh.Client) (sftpClientIface, error) {
		return nil, errors.New("sftp init failed")
	}
	o := newOps(cfg, okPool(nil), badClientFactory)
	_, _, err := o.Read(context.Background(), "h", "/f", 0)
	if err == nil {
		t.Fatal("expected newClient error to propagate")
	}
}

// ---- Atomic write tests ----

func TestWrite_Atomic_HappyPath(t *testing.T) {
	ffs := newFakeFS()
	ffs.files["/etc/app/config.yaml"] = []byte("old content")
	// minCfg uses sftp_allowed_prefixes: ["/"] — allows any absolute path.
	o := newTestOps(minCfg(), okPool(nil), ffs)
	err := o.Write(context.Background(), "h", "/etc/app/config.yaml", []byte("new content"), 0, true)
	if err != nil {
		t.Fatalf("Write(atomic): %v", err)
	}
	if string(ffs.files["/etc/app/config.yaml"]) != "new content" {
		t.Errorf("target file = %q, want new content", ffs.files["/etc/app/config.yaml"])
	}
	for k := range ffs.files {
		if strings.HasSuffix(k, ".rootcanal.tmp") {
			t.Errorf("temp file %q was not removed after successful atomic write", k)
		}
	}
}

func TestWrite_Atomic_WriteError_OriginalUntouched(t *testing.T) {
	ffs := newFakeFS()
	ffs.files["/etc/app/config.yaml"] = []byte("original")
	ffs.writeErr = errors.New("disk full")
	// minCfg uses sftp_allowed_prefixes: ["/"] — allows any absolute path.
	o := newTestOps(minCfg(), okPool(nil), ffs)
	err := o.Write(context.Background(), "h", "/etc/app/config.yaml", []byte("new"), 0, true)
	if err == nil {
		t.Fatal("expected error from failed write")
	}
	if string(ffs.files["/etc/app/config.yaml"]) != "original" {
		t.Errorf("original file was mutated on write error: %q", ffs.files["/etc/app/config.yaml"])
	}
}

func TestWrite_Atomic_RenameError_TempRemoved(t *testing.T) {
	ffs := newFakeFS()
	ffs.files["/etc/app/config.yaml"] = []byte("original")
	ffs.renameErr = errors.New("rename failed")
	// minCfg uses sftp_allowed_prefixes: ["/"] — allows any absolute path.
	o := newTestOps(minCfg(), okPool(nil), ffs)
	err := o.Write(context.Background(), "h", "/etc/app/config.yaml", []byte("new"), 0, true)
	if err == nil {
		t.Fatal("expected error from rename failure")
	}
	// Original must be untouched.
	if string(ffs.files["/etc/app/config.yaml"]) != "original" {
		t.Errorf("original file was mutated: %q", ffs.files["/etc/app/config.yaml"])
	}
	// Temp file must be cleaned up after the rename error.
	for k := range ffs.files {
		if strings.HasSuffix(k, ".rootcanal.tmp") {
			t.Errorf("temp file %q was not removed after rename error", k)
		}
	}
}
