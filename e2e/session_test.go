//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

func TestSession_OpenSendClose(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("OpenSession failed: %s", msg)
	}

	sr := h.Send(id, "echo hello\n", 2000)
	if sr.IsError {
		t.Fatalf("Send failed: %s", sr.ErrText)
	}
	if !strings.Contains(sr.Output, "hello") {
		t.Errorf("expected 'hello' in output, got: %q", sr.Output)
	}

	h.CloseSession(id)
}

func TestSession_ShellPromptVisible(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("OpenSession failed: %s", msg)
	}

	// An empty newline triggers the prompt to appear in the output.
	sr := h.Send(id, "\n", 2000)
	if sr.IsError {
		t.Fatalf("Send failed: %s", sr.ErrText)
	}
	if !strings.Contains(sr.Output, "$") && !strings.Contains(sr.Output, "#") {
		t.Errorf("expected shell prompt ($ or #) in output, got: %q", sr.Output)
	}
}

func TestSession_MultilineLoop(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("OpenSession failed: %s", msg)
	}
	h.Send(id, "\n", 1000) // drain initial prompt

	sr := h.Send(id, "for i in 1 2 3; do echo $i; done\n", 3000)
	if sr.IsError {
		t.Fatalf("Send failed: %s", sr.ErrText)
	}
	for _, want := range []string{"1", "2", "3"} {
		if !strings.Contains(sr.Output, want) {
			t.Errorf("expected %q in loop output, got: %q", want, sr.Output)
		}
	}
}

func TestSession_PTYEnvAndCols(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("OpenSession failed: %s", msg)
	}
	h.Send(id, "\n", 1000) // drain initial prompt

	// $COLUMNS is set by bash from the PTY dimensions (ptyWidth=120 in manager.go).
	// Using $COLUMNS avoids a dependency on ncurses/tput in the Alpine image.
	sr1 := h.Send(id, "echo $COLUMNS\n", 2000)
	if !strings.Contains(sr1.Output, "120") {
		t.Errorf("expected $COLUMNS=120, got: %q", sr1.Output)
	}

	sr2 := h.Send(id, "echo $TERM\n", 2000)
	if !strings.Contains(sr2.Output, "xterm-256color") {
		t.Errorf("expected TERM=xterm-256color, got: %q", sr2.Output)
	}
}

func TestSession_InvalidUTF8Sanitised(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("OpenSession failed: %s", msg)
	}
	h.Send(id, "\n", 1000) // drain initial prompt

	// printf outputs raw bytes \xff\xfe which are not valid UTF-8.
	// The server must replace them with U+FFFD (the replacement character).
	sr := h.Send(id, "printf '\\xff\\xfe'\n", 2000)
	if !strings.Contains(sr.Output, "�") {
		t.Errorf("expected U+FFFD (sanitized UTF-8) in output, got: %q", sr.Output)
	}
}

func TestSession_ClosedFlagOnExit(t *testing.T) {
	h := newHarness(t, testFixtures.MainCfg)

	id, isErr, msg := h.OpenSession("testhost-key")
	if isErr {
		t.Fatalf("OpenSession failed: %s", msg)
	}
	h.Send(id, "\n", 1000) // drain initial prompt

	h.Send(id, "exit\n", 2000) // trigger shell exit

	// Poll until the session is marked closed or gone (up to ~2 s).
	var closed bool
	for range 5 {
		sr := h.Send(id, "\n", 500)
		if sr.Closed || sr.IsError {
			closed = true
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !closed {
		t.Error("expected Closed=true or error after shell exit")
	}
}
