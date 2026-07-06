package logging_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/zorak1103/rootcanal/internal/logging"
)

func TestSwappable_InitialHandler(t *testing.T) {
	var buf bytes.Buffer
	initial := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	swap := logging.New(initial)
	log := slog.New(swap)
	log.Info("hello from initial")

	if !strings.Contains(buf.String(), "hello from initial") {
		t.Errorf("expected log message in initial handler output, got: %q", buf.String())
	}
}

func TestSwappable_Swap(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewTextHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelDebug})
	h2 := slog.NewTextHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelDebug})

	swap := logging.New(h1)
	log := slog.New(swap)

	log.Info("before swap")
	swap.Swap(h2)
	log.Info("after swap")

	if !strings.Contains(buf1.String(), "before swap") {
		t.Errorf("expected 'before swap' in h1, got: %q", buf1.String())
	}
	if strings.Contains(buf1.String(), "after swap") {
		t.Errorf("did not expect 'after swap' in h1 after swap")
	}
	if !strings.Contains(buf2.String(), "after swap") {
		t.Errorf("expected 'after swap' in h2 after swap, got: %q", buf2.String())
	}
}

func TestSwappable_Enabled(t *testing.T) {
	var buf bytes.Buffer
	// Info-level handler — Debug should be disabled.
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	swap := logging.New(h)

	if swap.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Debug should be disabled for an Info-level handler")
	}
	if !swap.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Info should be enabled")
	}
}

func TestSwappable_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	swap := logging.New(h)

	log := slog.New(swap).With("key", "value")
	log.Info("with attrs")

	if !strings.Contains(buf.String(), "key=value") {
		t.Errorf("expected 'key=value' in output, got: %q", buf.String())
	}
}

func TestSwappable_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	swap := logging.New(h)

	log := slog.New(swap).WithGroup("grp")
	log.Info("grouped", "k", "v")

	if !strings.Contains(buf.String(), "grp.k=v") {
		t.Errorf("expected group prefix in output, got: %q", buf.String())
	}
}
