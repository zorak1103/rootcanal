package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// cappedBuffer is an io.Writer that discards bytes past the capacity limit.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	cap       int64
	written   int64
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	remaining := c.cap - c.written
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		c.truncated = true
		p = p[:remaining]
	}
	n, err := c.buf.Write(p)
	c.written += int64(n)
	return len(p), err
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func (m *manager) RunOnce(ctx context.Context, host string, in RunOnceInput) (RunOnceOutput, error) {
	if m.pool == nil {
		return RunOnceOutput{}, fmt.Errorf("run_once: no pool configured")
	}
	if _, ok := m.cfg.Hosts[host]; !ok {
		return RunOnceOutput{}, fmt.Errorf("unknown host %q", host)
	}

	client, release, err := m.pool.Get(ctx, host)
	if err != nil {
		return RunOnceOutput{}, err
	}
	defer release()

	sess, err := client.NewSession()
	if err != nil {
		return RunOnceOutput{}, fmt.Errorf("opening exec session on %q: %w", host, err)
	}
	defer sess.Close()

	var warnings []string
	for k, v := range in.Env {
		if err := sess.Setenv(k, v); err != nil {
			warnings = append(warnings, fmt.Sprintf("setenv %s: %v", k, err))
		}
	}

	maxBytes := m.cfg.Limits.RunOnceMaxBytes
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}

	stdout := &cappedBuffer{cap: maxBytes}
	stderr := &cappedBuffer{cap: maxBytes}
	sess.Stdout = stdout
	sess.Stderr = stderr

	if in.Stdin != "" {
		sess.Stdin = bytes.NewBufferString(in.Stdin)
	}

	timeoutMs := in.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = m.cfg.Limits.RunOnceMaxTimeoutMs
	}
	maxTimeoutMs := m.cfg.Limits.RunOnceMaxTimeoutMs
	if maxTimeoutMs > 0 && timeoutMs > maxTimeoutMs {
		warnings = append(warnings, fmt.Sprintf("timeout_ms clamped from %d to %d", in.TimeoutMs, maxTimeoutMs))
		timeoutMs = maxTimeoutMs
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- sess.Run(in.Command) }()

	var runErr error
	select {
	case runErr = <-runDone:
	case <-runCtx.Done():
		_ = sess.Signal(ssh.SIGTERM)
		_ = sess.Close()
		runErr = <-runDone
	}

	out := RunOnceOutput{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Truncated: stdout.truncated || stderr.truncated,
		Warnings:  warnings,
	}

	if runErr == nil {
		out.ExitCode = 0
	} else {
		var exitErr *ssh.ExitError
		if errors.As(runErr, &exitErr) {
			out.ExitCode = exitErr.ExitStatus()
			if sig := exitErr.Signal(); sig != "" {
				out.Signal = sig
				out.ExitCode = -1
			}
		} else {
			return out, fmt.Errorf("run_once on %q: %w", host, runErr)
		}
	}

	return out, nil
}
