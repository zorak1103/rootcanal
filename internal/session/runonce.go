package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zorak1103/rootcanal/internal/config"
	"golang.org/x/crypto/ssh"
)

// drainGrace is the extra window given to a still-draining output stream after
// the wall-clock deadline fires. If the remote process already exited and the
// SSH library is merely copying buffered bytes, this window lets sess.Run
// return the real exit code rather than having us send SIGTERM and lose it.
const drainGrace = 2 * time.Second

// resolveRunTimeout computes the effective wall-clock timeout (ms) for
// RunOnce from the caller's requested value (reqMs, 0 = "use the default")
// and the configured ceiling (maxMs, 0 = "no ceiling configured"). It returns
// a non-empty warning when the requested value had to be clamped down to
// maxMs.
func resolveRunTimeout(reqMs, maxMs int) (timeoutMs int, warning string) {
	timeoutMs = reqMs
	if timeoutMs <= 0 {
		timeoutMs = maxMs
	}
	if maxMs > 0 && timeoutMs > maxMs {
		warning = fmt.Sprintf("timeout_ms clamped from %d to %d", reqMs, maxMs)
		timeoutMs = maxMs
	}
	if timeoutMs <= 0 {
		timeoutMs = 30000 // hard fallback if config defaults were not applied
	}
	return timeoutMs, warning
}

// runWithDeadline executes cmd on sess and waits for it to finish or runCtx
// to expire. If the deadline fires, the process may have already exited and
// the SSH library may still be draining buffered output (issue #15); this
// gives it a short grace window before forcibly sending SIGTERM and closing
// the session. killedByDeadline is true only when that forced path was taken.
func runWithDeadline(sess *ssh.Session, cmd string, runCtx context.Context) (runErr error, killedByDeadline bool) {
	runDone := make(chan error, 1)
	go func() { runDone <- sess.Run(cmd) }()

	select {
	case runErr = <-runDone:
	case <-runCtx.Done():
		select {
		case runErr = <-runDone:
		case <-time.After(drainGrace):
			killedByDeadline = true
			_ = sess.Signal(ssh.SIGTERM)
			_ = sess.Close()
			runErr = <-runDone
		}
	}
	return runErr, killedByDeadline
}

// runClassification is the structured outcome of classifyRunResult.
type runClassification struct {
	ExitCode  int
	Signal    string
	Warnings  []string
	HardError bool // true means the caller must return a wrapped error, not a result
}

// classifyRunResult translates the exit-info fields extracted from a sess.Run
// error into RunOnce output fields. The parameters are:
//   - exitCode: ExitStatus() from *ssh.ExitError, or 0 on clean success
//   - signal:   Signal()     from *ssh.ExitError, or "" if none
//   - isExitErr: true when err was nil (success) or *ssh.ExitError; false for hard IO errors
//   - killedByDeadline: true when the run harness itself sent the SIGTERM
//   - timeoutMs: the configured wall-clock cap in milliseconds (used in warnings)
func classifyRunResult(exitCode int, signal string, isExitErr, killedByDeadline bool, timeoutMs int) runClassification {
	if !isExitErr {
		return runClassification{HardError: true}
	}
	if signal == "" {
		// Clean exit (nil error or ExitError with a status code but no signal).
		return runClassification{ExitCode: exitCode}
	}
	// Process was terminated by a signal.
	w := runClassification{ExitCode: -1, Signal: signal}
	if killedByDeadline {
		w.Warnings = append(w.Warnings, fmt.Sprintf(
			"process exceeded the %d ms wall-clock timeout and was terminated (SIGTERM); "+
				"it was still running at the deadline. "+
				"Increase timeout_ms or use detach=true for long-running commands.", timeoutMs))
	} else {
		w.Warnings = append(w.Warnings,
			"process terminated by signal "+signal+
				" — possible causes: network idle timeout (NAT/firewall), OOM kill, "+
				"explicit signal sent to process. "+
				"If recurring on commands with no output, set keepalive_interval on the host.")
	}
	return w
}

// extractExitCode unpacks a sess.Run / sess.Wait error into the flat fields
// consumed by classifyRunResult. It is intentionally thin — all decision logic
// lives in classifyRunResult so it can be unit-tested without a real SSH server.
func extractExitCode(err error) (exitCode int, signal string, isExitErr bool) {
	if err == nil {
		return 0, "", true
	}
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitStatus(), exitErr.Signal(), true
	}
	return 0, "", false
}

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
	orig := len(p)
	remaining := c.cap - c.written
	if remaining <= 0 {
		c.truncated = true
		return orig, nil
	}
	if int64(orig) > remaining {
		c.truncated = true
		p = p[:remaining]
	}
	n, err := c.buf.Write(p)
	c.written += int64(n)
	return orig, err
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// Truncated reports whether any bytes were discarded due to the cap limit.
func (c *cappedBuffer) Truncated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.truncated
}

func (m *manager) RunOnce(ctx context.Context, host string, in RunOnceInput) (RunOnceOutput, error) {
	if m.pool == nil {
		return RunOnceOutput{}, fmt.Errorf("run_once: no pool configured")
	}
	if _, ok := m.cfg.Hosts[host]; !ok {
		return RunOnceOutput{}, config.UnknownHostError(host)
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
	defer func() { _ = sess.Close() }()

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

	timeoutMs, timeoutWarning := resolveRunTimeout(in.TimeoutMs, m.cfg.Limits.RunOnceMaxTimeoutMs)
	if timeoutWarning != "" {
		warnings = append(warnings, timeoutWarning)
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	runErr, killedByDeadline := runWithDeadline(sess, in.Command, runCtx)

	out := RunOnceOutput{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Truncated: stdout.Truncated() || stderr.Truncated(),
		Warnings:  warnings,
	}

	exitCode, signal, isExitErr := extractExitCode(runErr)
	c := classifyRunResult(exitCode, signal, isExitErr, killedByDeadline, timeoutMs)
	if c.HardError {
		return out, fmt.Errorf("run_once on %q: %w", host, runErr)
	}
	out.ExitCode = c.ExitCode
	out.Signal = c.Signal
	out.Warnings = append(out.Warnings, c.Warnings...)

	return out, nil
}
