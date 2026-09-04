package session

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// ---- RunOnce concurrency limit (MaxRunOnceConcurrent) ----

func TestManager_RunOnce_ConcurrencyLimitReached(t *testing.T) {
	cfg := minCfg()
	cfg.Limits.MaxRunOnceConcurrent = 2
	mgr := newManager(cfg, fakeSessions(), nil)
	defer mgr.Shutdown(context.Background())

	// Fill the semaphore directly; RunOnce must fail fast before ever
	// touching m.pool (which is nil here, proving the check runs first).
	mgr.runOnceSem <- struct{}{}
	mgr.runOnceSem <- struct{}{}

	_, err := mgr.RunOnce(context.Background(), "h", RunOnceInput{Command: "ls"})
	if err == nil {
		t.Fatal("expected concurrency limit error")
	}
	if !strings.Contains(err.Error(), "concurrency limit of 2") {
		t.Errorf("unexpected error: %v", err)
	}

	// Draining one slot must let the next call proceed past the semaphore
	// (it still fails on nil pool, but with a different error).
	<-mgr.runOnceSem
	_, err = mgr.RunOnce(context.Background(), "h", RunOnceInput{Command: "ls"})
	if err == nil || !strings.Contains(err.Error(), "no pool configured") {
		t.Errorf("expected nil-pool error once a slot freed up, got: %v", err)
	}
}

// ---- resolveRunTimeout ----

func TestResolveRunTimeout(t *testing.T) {
	cases := []struct {
		name        string
		reqMs       int
		maxMs       int
		wantMs      int
		wantWarning bool
	}{
		{"zero request uses ceiling as default", 0, 60000, 60000, false},
		{"explicit request under unset ceiling", 5000, 0, 5000, false},
		{"request equals ceiling, no clamp warning", 60000, 60000, 60000, false},
		{"request over ceiling is clamped", 90000, 60000, 60000, true},
		{"both zero falls back to hard default", 0, 0, 30000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotMs, warning := resolveRunTimeout(tc.reqMs, tc.maxMs)
			if gotMs != tc.wantMs {
				t.Errorf("resolveRunTimeout(%d, %d) ms = %d, want %d", tc.reqMs, tc.maxMs, gotMs, tc.wantMs)
			}
			if tc.wantWarning && warning == "" {
				t.Errorf("resolveRunTimeout(%d, %d): expected clamp warning, got none", tc.reqMs, tc.maxMs)
			}
			if !tc.wantWarning && warning != "" {
				t.Errorf("resolveRunTimeout(%d, %d): unexpected warning: %q", tc.reqMs, tc.maxMs, warning)
			}
		})
	}
}

func TestEffectiveRunOnceCap(t *testing.T) {
	cases := []struct {
		name          string
		configuredMax int64
		want          int64
	}{
		{"unset falls back to 1 MiB", 0, 1 << 20},
		{"negative treated as unset", -1, 1 << 20},
		{"explicit value passed through", 4096, 4096},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveRunOnceCap(tc.configuredMax); got != tc.want {
				t.Errorf("effectiveRunOnceCap(%d) = %d, want %d", tc.configuredMax, got, tc.want)
			}
		})
	}
}

func TestManager_RunOnce_ConcurrencyUnbounded(t *testing.T) {
	cfg := minCfg()
	cfg.Limits.MaxRunOnceConcurrent = 0
	mgr := newManager(cfg, fakeSessions(), nil)
	defer mgr.Shutdown(context.Background())

	if mgr.runOnceSem != nil {
		t.Fatal("expected nil semaphore when MaxRunOnceConcurrent is 0")
	}

	_, err := mgr.RunOnce(context.Background(), "h", RunOnceInput{Command: "ls"})
	if err == nil || !strings.Contains(err.Error(), "no pool configured") {
		t.Errorf("expected nil-pool error (semaphore bypassed), got: %v", err)
	}
}

// ---- classifyRunResult tests (Bug #15) ----

func TestClassifyRunResult_Success(t *testing.T) {
	c := classifyRunResult(0, "", true, false, 60000)
	if c.HardError {
		t.Fatal("expected no hard error on success")
	}
	if c.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", c.ExitCode)
	}
	if c.Signal != "" {
		t.Errorf("Signal = %q, want empty", c.Signal)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", c.Warnings)
	}
}

func TestClassifyRunResult_CleanNonZeroExit(t *testing.T) {
	// Process exited with code 2, no signal — real exit code must be preserved.
	c := classifyRunResult(2, "", true, false, 60000)
	if c.HardError {
		t.Fatal("unexpected hard error")
	}
	if c.ExitCode != 2 {
		t.Errorf("ExitCode = %d, want 2", c.ExitCode)
	}
	if c.Signal != "" {
		t.Errorf("Signal = %q, want empty", c.Signal)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", c.Warnings)
	}
}

func TestClassifyRunResult_KilledByDeadline(t *testing.T) {
	// The harness sent SIGTERM because the deadline fired (killedByDeadline=true).
	// Warning must mention the timeout cap, NOT NAT/keepalive.
	c := classifyRunResult(-1, "TERM", true, true, 60000)
	if c.HardError {
		t.Fatal("unexpected hard error")
	}
	if c.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", c.ExitCode)
	}
	if c.Signal != "TERM" {
		t.Errorf("Signal = %q, want TERM", c.Signal)
	}
	if len(c.Warnings) == 0 {
		t.Fatal("expected a warning")
	}
	if strings.Contains(c.Warnings[0], "NAT") || strings.Contains(c.Warnings[0], "keepalive") {
		t.Errorf("deadline warning must not mention NAT/keepalive, got: %s", c.Warnings[0])
	}
	if !strings.Contains(c.Warnings[0], "60000") {
		t.Errorf("deadline warning should include the timeout_ms value (60000), got: %s", c.Warnings[0])
	}
}

func TestClassifyRunResult_ExternalSignal(t *testing.T) {
	// TERM arrived but we did not kill it — keepalive/NAT warning applies.
	c := classifyRunResult(-1, "TERM", true, false, 60000)
	if c.HardError {
		t.Fatal("unexpected hard error")
	}
	if c.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", c.ExitCode)
	}
	if c.Signal != "TERM" {
		t.Errorf("Signal = %q, want TERM", c.Signal)
	}
	if len(c.Warnings) == 0 {
		t.Fatal("expected a warning")
	}
	if !strings.Contains(c.Warnings[0], "NAT") {
		t.Errorf("external-signal warning should mention NAT, got: %s", c.Warnings[0])
	}
}

func TestClassifyRunResult_HardError(t *testing.T) {
	// Non-ExitError (IO problem) — must set HardError so RunOnce wraps and returns it.
	c := classifyRunResult(0, "", false, false, 60000)
	if !c.HardError {
		t.Fatal("expected HardError = true for non-ExitError")
	}
}

// ---- extractExitCode tests (covers the nil and non-ExitError branches) ----

func TestExtractExitCode_NilError(t *testing.T) {
	code, sig, isExit := extractExitCode(nil)
	if !isExit {
		t.Error("expected isExitErr=true for nil error")
	}
	if code != 0 {
		t.Errorf("exitCode = %d, want 0", code)
	}
	if sig != "" {
		t.Errorf("signal = %q, want empty", sig)
	}
}

func TestExtractExitCode_HardIOError(t *testing.T) {
	// A non-ExitError (e.g., network IO failure) must return isExitErr=false
	// so the caller wraps it as a hard error.
	_, _, isExit := extractExitCode(fmt.Errorf("connection reset by peer"))
	if isExit {
		t.Error("expected isExitErr=false for non-ssh.ExitError")
	}
}

func TestExtractExitCode_ExitError(t *testing.T) {
	exitCode, signal, isExitErr := extractExitCode(&ssh.ExitError{})
	if !isExitErr {
		t.Error("expected isExitErr=true for *ssh.ExitError")
	}
	if exitCode != 0 || signal != "" {
		t.Errorf("got (%d, %q), want (0, \"\") for a zero-value ExitError", exitCode, signal)
	}
}

func TestCappedBuffer_Write_BelowCap(t *testing.T) {
	cb := &cappedBuffer{cap: 100}
	n, err := cb.Write([]byte("hello"))
	if n != 5 || err != nil {
		t.Errorf("Write = (%d, %v), want (5, nil)", n, err)
	}
	if cb.String() != "hello" {
		t.Errorf("String = %q, want %q", cb.String(), "hello")
	}
	if cb.Truncated() {
		t.Error("Truncated should be false")
	}
}

func TestCappedBuffer_Write_ExactCap(t *testing.T) {
	cb := &cappedBuffer{cap: 5}
	cb.Write([]byte("hello"))
	if cb.String() != "hello" {
		t.Errorf("String = %q, want %q", cb.String(), "hello")
	}
	if cb.Truncated() {
		t.Error("Truncated should be false for exactly cap bytes")
	}
}

func TestCappedBuffer_Write_OverCap(t *testing.T) {
	cb := &cappedBuffer{cap: 3}
	n, err := cb.Write([]byte("hello"))
	// io.Writer contract: must return len(p) even when truncating.
	if n != 5 || err != nil {
		t.Errorf("Write = (%d, %v), want (5, nil)", n, err)
	}
	if cb.String() != "hel" {
		t.Errorf("String = %q, want %q", cb.String(), "hel")
	}
	if !cb.Truncated() {
		t.Error("Truncated should be true")
	}
}

func TestCappedBuffer_Write_AfterFull(t *testing.T) {
	cb := &cappedBuffer{cap: 3}
	cb.Write([]byte("abc"))
	n, err := cb.Write([]byte("more")) // all discarded; must still return (4, nil)
	if n != 4 || err != nil {
		t.Errorf("Write after full = (%d, %v), want (4, nil)", n, err)
	}
	if cb.String() != "abc" {
		t.Errorf("String = %q, want %q", cb.String(), "abc")
	}
	if !cb.Truncated() {
		t.Error("Truncated should be true")
	}
}

func TestCappedBuffer_Write_Empty(t *testing.T) {
	cb := &cappedBuffer{cap: 10}
	n, err := cb.Write([]byte{})
	if n != 0 || err != nil {
		t.Errorf("Write([]) = (%d, %v)", n, err)
	}
}

func TestCappedBuffer_Write_EmptyWriteToFullBufferNotTruncated(t *testing.T) {
	// A buffer already at cap (but never truncated) receiving a zero-length
	// write must NOT be flagged as truncated: nothing was discarded.
	cb := &cappedBuffer{cap: 3}
	if _, err := cb.Write([]byte("abc")); err != nil {
		t.Fatalf("setup Write: %v", err)
	}
	if cb.Truncated() {
		t.Fatal("setup: filling exactly to cap should not be truncated")
	}

	n, err := cb.Write(nil)
	if n != 0 || err != nil {
		t.Errorf("Write(nil) on full buffer = (%d, %v), want (0, nil)", n, err)
	}
	if cb.Truncated() {
		t.Error("empty write to a full buffer must not set Truncated")
	}

	n, err = cb.Write([]byte{})
	if n != 0 || err != nil {
		t.Errorf("Write([]byte{}) on full buffer = (%d, %v), want (0, nil)", n, err)
	}
	if cb.Truncated() {
		t.Error("empty write ([]byte{}) to a full buffer must not set Truncated")
	}
}

// ---- RunOnce against a real in-process exec-capable SSH server ----
//
// These tests drive RunOnce past client.NewSession() and sess.Run against a
// real exec channel, which no other test in this package reaches (m.pool is
// otherwise nil or points at an unreachable address).

// TestManager_RunOnce_StdinRoundTrip drives the `if in.Stdin != ""` branch:
// the remote command (cat) echoes the piped stdin back into stdout.
func TestManager_RunOnce_StdinRoundTrip(t *testing.T) {
	requireSh(t)
	mgr := newLiveExecManager(t)

	const payload = "hello-runonce-stdin\n"
	out, err := mgr.RunOnce(context.Background(), "h", RunOnceInput{Command: "cat", Stdin: payload})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if !strings.Contains(out.Stdout, "hello-runonce-stdin") {
		t.Errorf("Stdout = %q, want to contain %q", out.Stdout, payload)
	}
	if out.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", out.ExitCode)
	}
}

// TestManager_RunOnce_NoTimeoutWarning drives the `if timeoutWarning != ""`
// guard on the path where no clamping was needed: a plain call with no
// explicit timeout_ms must not synthesize a spurious warning.
func TestManager_RunOnce_NoTimeoutWarning(t *testing.T) {
	requireSh(t)
	mgr := newLiveExecManager(t)

	out, err := mgr.RunOnce(context.Background(), "h", RunOnceInput{Command: "echo hi"})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none for a plain call needing no timeout clamp", out.Warnings)
	}
	if !strings.Contains(out.Stdout, "hi") {
		t.Errorf("Stdout = %q, want to contain %q", out.Stdout, "hi")
	}
}

// TestManager_RunOnce_SetenvError drives RunOnce's sess.Setenv error guard:
// when the remote server rejects an "env" channel request, Setenv returns an
// error that must be surfaced as a warning (not silently dropped), and the
// command must still run successfully.
func TestManager_RunOnce_SetenvError(t *testing.T) {
	requireSh(t)
	mgr := newLiveExecManagerWithOptions(t, execServerOptions{rejectEnv: true})

	out, err := mgr.RunOnce(context.Background(), "h", RunOnceInput{
		Command: "echo hi",
		Env:     map[string]string{"FOO": "bar"},
	})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	found := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "setenv FOO") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a 'setenv FOO' warning, got: %v", out.Warnings)
	}
	if !strings.Contains(out.Stdout, "hi") {
		t.Errorf("Stdout = %q, want to contain %q (command should still run)", out.Stdout, "hi")
	}
}

func TestCappedBuffer_Concurrent(t *testing.T) {
	// cappedBuffer must be safe for concurrent writes (SSH library writes from
	// its mux goroutine).
	cb := &cappedBuffer{cap: 1000}
	done := make(chan struct{})
	for range 10 {
		go func() {
			for range 20 {
				cb.Write([]byte(strings.Repeat("x", 5)))
			}
			done <- struct{}{}
		}()
	}
	for range 10 {
		<-done
	}
	// Just verify it didn't panic and String/Truncated don't race.
	_ = cb.String()
	_ = cb.Truncated()
}
