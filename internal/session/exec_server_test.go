package session

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zorak1103/rootcanal/internal/config"
	"github.com/zorak1103/rootcanal/internal/hostpool"
	"github.com/zorak1103/rootcanal/internal/sshconn"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// newLiveExecManager wires a Manager backed by a real *hostpool.Pool against
// a freshly started startExecSSHServer instance, under a single host key
// "h". It registers cleanup (Shutdown + pool.Close) via t.Cleanup. Intended
// for RunOnce/Detach/NewManager tests that need real SSH exec semantics
// (client.NewSession() to succeed, sess.Start/Run to actually execute).
func newLiveExecManager(t *testing.T) Manager {
	t.Helper()
	return newLiveExecManagerWithOptions(t, execServerOptions{})
}

// newLiveExecManagerWithOptions is like newLiveExecManager but lets the
// caller customize the exec server's behavior (e.g. rejecting "env" channel
// requests to exercise RunOnce's sess.Setenv error-warning path).
func newLiveExecManagerWithOptions(t *testing.T, opts execServerOptions) Manager {
	t.Helper()
	addr, khPath := startExecSSHServerWithOptions(t, opts)

	envVar := "TEST_EXEC_SSH_PASS_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	t.Setenv(envVar, "irrelevant")

	cfg := &config.Config{
		Limits: config.Limits{
			MaxSessionsTotal:     32,
			MaxSessionsPerHost:   4,
			DefaultIdleTimeout:   15 * time.Minute,
			MaxSessionAge:        4 * time.Hour,
			OutputBufferBytes:    4096,
			DefaultSendTimeoutMs: 2000,
			MaxSendTimeoutMs:     30000,
			DialTimeout:          5 * time.Second,
			RunOnceMaxBytes:      1 << 20,
			RunOnceMaxTimeoutMs:  60000,
		},
		Hosts: map[string]config.Host{
			"h": {
				Address:    addr,
				User:       "u",
				KnownHosts: khPath,
				Auth:       config.Auth{Type: "password", PasswordEnv: envVar},
			},
		},
	}

	pool := hostpool.New(cfg, sshconn.ProdDialer{})
	mgr := NewManager(cfg, pool, nil)
	t.Cleanup(func() {
		_ = mgr.Shutdown(context.Background())
		pool.Close()
	})
	return mgr
}

// startExecSSHServerWithOptions starts an in-process SSH server on
// 127.0.0.1:0 with NoClientAuth (modeled on hostpool.startSSHServer /
// sftpops.startSFTPServer) that additionally handles "exec" and "env"
// requests on session channels by actually running the requested command via
// `sh -c` on the host machine. This unlocks real coverage of the
// RunOnce/Detach code paths past client.NewSession(), which no other test in
// this package reaches.
//
// Tests relying on real shell semantics should call requireSh(t) first so
// they skip cleanly on machines without a POSIX shell in PATH.

// execServerOptions customizes startExecSSHServerWithOptions' behavior for
// tests that need to exercise error paths in the client (e.g. rejecting
// "env" requests to force sess.Setenv to return an error).
type execServerOptions struct {
	rejectEnv bool
}

func startExecSSHServerWithOptions(t *testing.T, opts execServerOptions) (addr, knownHostsPath string) {
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

	khPath := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, serverSigner.PublicKey())
	if err := os.WriteFile(khPath, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	knownHostsPath = khPath

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveExecConn(conn, srvCfg, opts)
		}
	}()

	return addr, knownHostsPath
}

// requireSh skips the calling test if no POSIX shell is available in PATH.
// The exec test server always shells out via "sh -c"; CI runs on Linux where
// this is always present, but bare Windows dev boxes may lack it.
func requireSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell (sh) in PATH; skipping real-exec test")
	}
}

func serveExecConn(conn net.Conn, cfg *ssh.ServerConfig, opts execServerOptions) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go handleExecSession(ch, requests, opts)
	}
}

// execExitStatusMsg mirrors the SSH wire format for an "exit-status" request
// (RFC 4254 §6.10).
type execExitStatusMsg struct {
	Status uint32
}

// handleExecSession services the requests channel of a single "session"
// channel: it accepts "env" requests (recorded for the eventual exec) and a
// single "exec" request, which it actually runs via sh -c, wiring stdin from
// the channel and stdout/stderr back to it before replying with exit-status.
// handleExecSession services the requests channel of a single "session"
// channel: it accepts "env" requests (recorded for the eventual exec),
// "pty-req" (acknowledged but not backed by a real terminal — sufficient for
// tests that only need Open's PTY request to succeed), and either a single
// "exec" request (runs cmdStr via sh -c) or a "shell" request (runs a plain
// sh reading commands from the channel line by line, matching what
// startShellSession/bootSession expect from an interactive session). Either
// terminates the channel after replying with exit-status.
func handleExecSession(ch ssh.Channel, requests <-chan *ssh.Request, opts execServerOptions) {
	defer ch.Close()
	env := os.Environ()
	for req := range requests {
		switch req.Type {
		case "env":
			if opts.rejectEnv {
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
				continue
			}
			if name, value, ok := decodeEnvPayload(req.Payload); ok {
				env = append(env, name+"="+value)
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
			} else if req.WantReply {
				_ = req.Reply(false, nil)
			}
		case "pty-req":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		case "shell":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			runProcess(ch, exec.Command("sh"), env)
			return
		case "exec":
			cmdStr, ok := decodeExecPayload(req.Payload)
			if !ok {
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
				return
			}
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			runProcess(ch, exec.Command("sh", "-c", cmdStr), env)
			return
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// decodeExecPayload unpacks the SSH-encoded command string carried by an
// "exec" channel request: a 4-byte big-endian length prefix followed by the
// command bytes.
func decodeExecPayload(payload []byte) (string, bool) {
	if len(payload) < 4 {
		return "", false
	}
	n := binary.BigEndian.Uint32(payload[:4])
	if uint64(len(payload)-4) < uint64(n) {
		return "", false
	}
	return string(payload[4 : 4+n]), true
}

// decodeEnvPayload unpacks the SSH-encoded (name, value) string pair carried
// by an "env" channel request.
func decodeEnvPayload(payload []byte) (name, value string, ok bool) {
	if len(payload) < 4 {
		return "", "", false
	}
	n := binary.BigEndian.Uint32(payload[:4])
	if uint64(len(payload)-4) < uint64(n) {
		return "", "", false
	}
	name = string(payload[4 : 4+n])
	rest := payload[4+n:]
	if len(rest) < 4 {
		return "", "", false
	}
	n2 := binary.BigEndian.Uint32(rest[:4])
	if uint64(len(rest)-4) < uint64(n2) {
		return "", "", false
	}
	value = string(rest[4 : 4+n2])
	return name, value, true
}

// runExec actually runs cmdStr via sh -c, wiring the channel as stdin/stdout/
// stderr, then replies with the process's exit-status. Stdin is copied in a
// detached goroutine (via a stdin pipe rather than assigning cmd.Stdin
// directly) so that a client which never sends or closes stdin cannot hang
// cmd.Wait() — only the copy goroutine blocks, and it is unblocked when the
// caller closes ch.
// runProcess wires cmd's stdin/stdout/stderr to ch, starts it, waits for it
// to finish, then replies with exit-status. Used for both "exec" (a fixed
// sh -c command) and "shell" (a bare sh reading commands line by line off
// the channel, matching what bootSession/Send expect from an interactive
// session). Stdin is copied in a detached goroutine (via a stdin pipe rather
// than assigning cmd.Stdin directly) so that a client which never sends or
// closes stdin cannot hang cmd.Wait() — only the copy goroutine blocks, and
// it is unblocked when the caller closes ch.
func runProcess(ch ssh.Channel, cmd *exec.Cmd, env []string) {
	cmd.Env = env
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		sendExitStatus(ch, 255)
		return
	}

	if err := cmd.Start(); err != nil {
		sendExitStatus(ch, 255)
		return
	}

	go func() {
		_, _ = io.Copy(stdinPipe, ch)
		_ = stdinPipe.Close()
	}()

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 255
		}
	}

	_ = ch.CloseWrite()
	sendExitStatus(ch, exitCode)
}

func sendExitStatus(ch ssh.Channel, code int) {
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(&execExitStatusMsg{Status: uint32(code)}))
}
