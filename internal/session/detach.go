package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gitlab.com/zorak1103/rootcanal/internal/config"
	"golang.org/x/crypto/ssh"
)

// resolveDetachTimeout returns the wall-clock deadline (in ms) for a detached
// job. Unlike RunOnce, detached jobs are not bounded by the 60 s synchronous
// cap (RunOnceMaxTimeoutMs). They use a separate, much larger ceiling
// (DetachMaxDurationMs, default 24 h) so that genuinely long-running tasks
// can complete naturally.
//
// Rules:
//   - reqMs ≤ 0 → use maxMs (the configured ceiling)
//   - reqMs > 0 && reqMs ≤ maxMs → use reqMs
//   - reqMs > maxMs (and maxMs > 0) → clamp to maxMs
//   - maxMs ≤ 0 → no cap configured; use reqMs if positive, else 86400000 (safety fallback)
func resolveDetachTimeout(reqMs, maxMs int) int {
	const fallback = 24 * 60 * 60 * 1000 // 24 h safety fallback
	if maxMs <= 0 {
		if reqMs > 0 {
			return reqMs
		}
		return fallback
	}
	if reqMs <= 0 || reqMs > maxMs {
		return maxMs
	}
	return reqMs
}

// Detach starts a command via SSH exec (no PTY), registers it with the job
// registry, and returns immediately. The background goroutine streams
// stdout/stderr into the registry and calls reg.MarkDone when the command exits
// or the deadline fires.
//
// Note: Detach does not enforce MaxRunOnceConcurrent (which is also not
// currently enforced by RunOnce). The MaxJobs registry cap provides the
// effective concurrency limit for detached jobs.
func (m *manager) Detach(ctx context.Context, host string, in RunOnceInput, reg DetachRegistry) (string, error) {
	if m.pool == nil {
		return "", fmt.Errorf("detach: no pool configured")
	}
	if _, ok := m.cfg.Hosts[host]; !ok {
		return "", config.UnknownHostError(host)
	}

	// Detached jobs use DetachMaxDurationMs (default 24 h) as their ceiling,
	// not the 60 s RunOnceMaxTimeoutMs cap. This lets detached jobs run to
	// natural completion without being killed at the synchronous wall-clock limit.
	timeoutMs := resolveDetachTimeout(in.TimeoutMs, m.cfg.Limits.DetachMaxDurationMs)

	client, release, err := m.pool.Get(ctx, host)
	if err != nil {
		return "", err
	}

	sess, err := client.NewSession()
	if err != nil {
		release()
		return "", fmt.Errorf("opening exec session on %q: %w", host, err)
	}

	for k, v := range in.Env {
		_ = sess.Setenv(k, v)
	}

	stdoutPipe, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		release()
		return "", fmt.Errorf("stdout pipe on %q: %w", host, err)
	}
	stderrPipe, err := sess.StderrPipe()
	if err != nil {
		_ = sess.Close()
		release()
		return "", fmt.Errorf("stderr pipe on %q: %w", host, err)
	}

	if in.Stdin != "" {
		sess.Stdin = bytes.NewBufferString(in.Stdin)
	}

	if err := sess.Start(in.Command); err != nil {
		_ = sess.Close()
		release()
		return "", fmt.Errorf("starting command on %q: %w", host, err)
	}

	jobID, regErr := reg.TryRegister(host, in.Command, 0)
	if regErr != nil {
		_ = sess.Signal(ssh.SIGTERM)
		_ = sess.Close()
		release()
		return "", regErr
	}

	deadline := time.Duration(timeoutMs) * time.Millisecond

	go func() {
		defer release()
		defer func() { _ = sess.Close() }()

		// Wire cancel: when reg.Cancel(jobID) is called, send SIGTERM.
		reg.SetCancel(jobID, func() {
			_ = sess.Signal(ssh.SIGTERM)
		})

		// Stream stdout and stderr into the registry concurrently.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			buf := make([]byte, 4096)
			for {
				n, err := stdoutPipe.Read(buf)
				if n > 0 {
					reg.AppendStdout(jobID, buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			buf := make([]byte, 4096)
			for {
				n, err := stderrPipe.Read(buf)
				if n > 0 {
					reg.AppendStderr(jobID, buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()

		runCtx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()

		runDone := make(chan error, 1)
		go func() { runDone <- sess.Wait() }()

		var runErr error
		select {
		case runErr = <-runDone:
		case <-runCtx.Done():
			_ = sess.Signal(ssh.SIGTERM)
			runErr = <-runDone
		}

		wg.Wait() // wait for stream goroutines to drain

		var code *int
		if runErr == nil {
			c := 0
			code = &c
		} else {
			var exitErr *ssh.ExitError
			if errors.As(runErr, &exitErr) {
				c := exitErr.ExitStatus()
				code = &c
			}
			// signal exit → code stays nil
		}
		reg.MarkDone(jobID, code)
	}()

	return jobID, nil
}
