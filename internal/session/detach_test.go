package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zorak1103/rootcanal/internal/jobs"
)

// ---- resolveDetachTimeout tests (Bug #18) ----

func TestResolveDetachTimeout_DefaultsToMax(t *testing.T) {
	// No caller-requested timeout → use the detach max cap.
	got := resolveDetachTimeout(0, 86400000)
	if got != 86400000 {
		t.Errorf("resolveDetachTimeout(0, 86400000) = %d, want 86400000", got)
	}
}

func TestResolveDetachTimeout_CallerRequestedWithinCap(t *testing.T) {
	// Caller asks for 2 min — honour it, don't clamp.
	got := resolveDetachTimeout(120000, 86400000)
	if got != 120000 {
		t.Errorf("resolveDetachTimeout(120000, 86400000) = %d, want 120000", got)
	}
}

func TestResolveDetachTimeout_CallerRequestedExceedsCap(t *testing.T) {
	// Caller asks for > max — clamp to max.
	const oneWeekMs = 7 * 24 * 60 * 60 * 1000
	got := resolveDetachTimeout(oneWeekMs, 86400000)
	if got != 86400000 {
		t.Errorf("resolveDetachTimeout(%d, 86400000) = %d, want 86400000", oneWeekMs, got)
	}
}

func TestResolveDetachTimeout_ZeroMaxMeansNoClamp(t *testing.T) {
	// maxMs=0 means "no cap configured" — should use the requested value or
	// the compiled-in fallback (never block on 0ms).
	got := resolveDetachTimeout(5000, 0)
	if got <= 0 {
		t.Errorf("resolveDetachTimeout(5000, 0) = %d, want positive", got)
	}
}

func TestResolveDetachTimeout_BothZeroUsesFallback(t *testing.T) {
	// Neither caller nor config provide a value → safety fallback (24 h).
	got := resolveDetachTimeout(0, 0)
	const fallback = 24 * 60 * 60 * 1000
	if got != fallback {
		t.Errorf("resolveDetachTimeout(0, 0) = %d, want %d (24h fallback)", got, fallback)
	}
}

// ---- Detach against a real in-process exec-capable SSH server ----
//
// The tests below drive Detach all the way through client.NewSession(),
// StdoutPipe/StderrPipe, sess.Start, and streamDetached's exit-code
// classification — code paths that no other test in this package reaches,
// since m.pool is otherwise nil or points at an unreachable address.

// waitForJobDone polls reg for jobID to stop running, failing the test if it
// doesn't happen within a few seconds. Short-poll sleeps match the pattern
// already used elsewhere in this package (e.g. TestPool idle-timer tests).
func waitForJobDone(t *testing.T, reg *jobs.Registry, jobID string) *jobs.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := reg.Get(jobID)
		if !ok {
			t.Fatalf("job %q not found in registry", jobID)
		}
		if !job.Running() {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %q did not finish within timeout", jobID)
	return nil
}

// TestManager_Detach_HappyPath exercises Detach's success path against a
// real SSH exec session: client.NewSession, StdoutPipe, StderrPipe, and
// sess.Start all succeed, and reg.TryRegister succeeds. Before this test,
// none of the five inverted "if err != nil" guards on that path were ever
// evaluated with a live error to actually check, since no test produced a
// working *ssh.Client/*ssh.Session.
func TestManager_Detach_HappyPath(t *testing.T) {
	requireSh(t)
	mgr := newLiveExecManager(t)
	reg := jobs.NewRegistry(10, time.Minute)
	t.Cleanup(reg.Close)

	jobID, err := mgr.Detach(context.Background(), "h", RunOnceInput{Command: "true"}, reg)
	if err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if jobID == "" {
		t.Fatal("expected non-empty jobID")
	}

	waitForJobDone(t, reg, jobID)
}

// TestManager_Detach_StdinRoundTrip drives Detach's `if in.Stdin != ""`
// branch: the remote command (cat) echoes the piped stdin back, and the
// bytes must show up in the job's recorded stdout.
func TestManager_Detach_StdinRoundTrip(t *testing.T) {
	requireSh(t)
	mgr := newLiveExecManager(t)
	reg := jobs.NewRegistry(10, time.Minute)
	t.Cleanup(reg.Close)

	const payload = "hello-detach-stdin\n"
	jobID, err := mgr.Detach(context.Background(), "h", RunOnceInput{Command: "cat", Stdin: payload}, reg)
	if err != nil {
		t.Fatalf("Detach: %v", err)
	}

	job := waitForJobDone(t, reg, jobID)
	if got := job.StdoutTail(4096); !strings.Contains(got, "hello-detach-stdin") {
		t.Errorf("job stdout = %q, want to contain %q", got, payload)
	}
}

// TestManager_Detach_ExitCodeRecording drives streamDetached's
// `if runErr == nil` branch both ways: a clean success must record exit code
// 0, and a clean non-zero exit must record that exact code (not 0 and not
// nil). This is the highest-value target — inverting the guard would report
// nil for success and 0 for a real failure.
func TestManager_Detach_ExitCodeRecording(t *testing.T) {
	requireSh(t)

	tests := []struct {
		name    string
		command string
		want    int
	}{
		{"clean success records 0", "true", 0},
		{"clean non-zero exit records that code", "exit 3", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newLiveExecManager(t)
			reg := jobs.NewRegistry(10, time.Minute)
			t.Cleanup(reg.Close)

			jobID, err := mgr.Detach(context.Background(), "h", RunOnceInput{Command: tc.command}, reg)
			if err != nil {
				t.Fatalf("Detach: %v", err)
			}

			job := waitForJobDone(t, reg, jobID)
			ec := job.ExitCode()
			if ec == nil {
				t.Fatal("expected non-nil exit code for a clean exit")
			}
			if *ec != tc.want {
				t.Errorf("ExitCode = %d, want %d", *ec, tc.want)
			}
		})
	}
}
