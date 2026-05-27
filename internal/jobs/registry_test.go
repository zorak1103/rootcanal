package jobs_test

import (
	"testing"
	"time"

	"gitlab.com/zorak1103/rootcanal/internal/jobs"
)

func TestRegistry_TryRegisterAndGet(t *testing.T) {
	reg := jobs.NewRegistry(10, time.Minute)
	defer reg.Close()

	id, err := reg.TryRegister("myhost", "echo hello", 0)
	if err != nil {
		t.Fatalf("TryRegister: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty job ID")
	}
	job, ok := reg.Get(id)
	if !ok {
		t.Fatal("Get: job not found immediately after Register")
	}
	if job.Host != "myhost" || job.Command != "echo hello" {
		t.Errorf("job fields wrong: %+v", job)
	}
	if !job.Running() {
		t.Error("job should be running immediately after register")
	}
}

func TestRegistry_MarkDoneAndGet(t *testing.T) {
	reg := jobs.NewRegistry(10, time.Minute)
	defer reg.Close()

	id, _ := reg.TryRegister("h", "cmd", 99)
	code := 0
	reg.MarkDone(id, &code)

	job, ok := reg.Get(id)
	if !ok {
		t.Fatal("Get: job not found after MarkDone")
	}
	if job.Running() {
		t.Error("job should not be running after MarkDone")
	}
	if job.ExitCode() == nil || *job.ExitCode() != 0 {
		t.Errorf("ExitCode = %v, want 0", job.ExitCode())
	}
}

func TestRegistry_TTLEviction(t *testing.T) {
	reg := jobs.NewRegistry(10, 1*time.Millisecond)
	defer reg.Close()

	id, _ := reg.TryRegister("h", "cmd", 1)
	code := 0
	reg.MarkDone(id, &code)

	time.Sleep(50 * time.Millisecond)
	reg.Reap()

	_, ok := reg.Get(id)
	if ok {
		t.Error("job should have been evicted after TTL")
	}
}

func TestRegistry_MaxJobs_RejectsWhenFull(t *testing.T) {
	reg := jobs.NewRegistry(2, time.Minute)
	defer reg.Close()

	_, _ = reg.TryRegister("h", "c1", 1)
	_, _ = reg.TryRegister("h", "c2", 2)
	_, err := reg.TryRegister("h", "c3", 3)
	if err == nil {
		t.Error("expected error when at cap")
	}
}

func TestRegistry_AppendOutputAndTail(t *testing.T) {
	reg := jobs.NewRegistry(10, time.Minute)
	defer reg.Close()

	id, _ := reg.TryRegister("h", "cmd", 1)
	reg.AppendStdout(id, []byte("hello\n"))
	reg.AppendStdout(id, []byte("world\n"))
	reg.AppendStderr(id, []byte("err\n"))

	job, _ := reg.Get(id)
	if job.StdoutTail(100) != "hello\nworld\n" {
		t.Errorf("StdoutTail = %q", job.StdoutTail(100))
	}
	if job.StderrTail(100) != "err\n" {
		t.Errorf("StderrTail = %q", job.StderrTail(100))
	}
}

func TestRegistry_Cancel(t *testing.T) {
	reg := jobs.NewRegistry(10, time.Minute)
	defer reg.Close()

	canceled := false
	id, _ := reg.TryRegister("h", "cmd", 1)
	reg.SetCancel(id, func() { canceled = true })
	reg.Cancel(id)

	if !canceled {
		t.Error("cancel func should have been called")
	}
}

func TestRegistry_CancelBeforeSetCancel(t *testing.T) {
	reg := jobs.NewRegistry(10, time.Minute)
	defer reg.Close()

	id, _ := reg.TryRegister("h", "cmd", 1)
	reg.Cancel(id) // cancel before SetCancel

	called := false
	reg.SetCancel(id, func() { called = true })

	if !called {
		t.Error("SetCancel should call fn immediately when cancel was already requested")
	}
}

func TestRegistry_ElapsedSeconds(t *testing.T) {
	reg := jobs.NewRegistry(10, time.Minute)
	defer reg.Close()

	id, _ := reg.TryRegister("h", "cmd", 1)
	time.Sleep(50 * time.Millisecond)
	job, _ := reg.Get(id)
	if job.ElapsedSeconds() < 0 {
		t.Error("elapsed should be >= 0")
	}
}
