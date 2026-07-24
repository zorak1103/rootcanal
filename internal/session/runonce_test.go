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
