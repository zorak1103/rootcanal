package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zorak1103/rootcanal/internal/hostkeys"
)

// --- fakeRefresher ---

type fakeRefresher struct {
	inspectResult hostkeys.InspectResult
	inspectErr    error
	acceptResult  hostkeys.AcceptResult
	acceptErr     error
}

func (f *fakeRefresher) Inspect(_ context.Context, _ string) (hostkeys.InspectResult, error) {
	return f.inspectResult, f.inspectErr
}

func (f *fakeRefresher) Accept(_ context.Context, _, _ string) (hostkeys.AcceptResult, error) {
	return f.acceptResult, f.acceptErr
}

// --- tests ---

func TestHandleAcceptHostKey_Preview(t *testing.T) {
	fr := &fakeRefresher{
		inspectResult: hostkeys.InspectResult{
			Host:       "web1",
			CurrentFP:  "SHA256:OLD",
			NewFP:      "SHA256:NEW",
			Changed:    true,
			KnownHosts: "/tmp/kh",
		},
	}
	h := handleAcceptHostKey(fr)
	_, out, err := h(context.Background(), &mcp.CallToolRequest{}, acceptHostKeyIn{Host: "web1"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if out.NewFingerprint != "SHA256:NEW" {
		t.Errorf("NewFingerprint = %q, want SHA256:NEW", out.NewFingerprint)
	}
	if !out.Changed {
		t.Error("want Changed=true")
	}
	if out.Message == "" {
		t.Error("want non-empty Message in preview result")
	}
}

func TestHandleAcceptHostKey_Preview_ReturnsSuccessResult(t *testing.T) {
	fr := &fakeRefresher{
		inspectResult: hostkeys.InspectResult{
			Host:       "web1",
			CurrentFP:  "SHA256:OLD",
			NewFP:      "SHA256:NEW",
			Changed:    true,
			KnownHosts: "/tmp/kh",
		},
	}
	h := handleAcceptHostKey(fr)
	result, out, err := h(context.Background(), &mcp.CallToolRequest{}, acceptHostKeyIn{Host: "web1"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("want IsError=false on a successful preview, got a tool error result")
	}
	if len(result.Content) != 1 {
		t.Fatalf("want exactly 1 content item, got %d", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is not *mcp.TextContent: %T", result.Content[0])
	}
	var decoded acceptHostKeyOut
	if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil {
		t.Fatalf("content text is not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(decoded, out) {
		t.Errorf("decoded content = %+v, want %+v", decoded, out)
	}
}

func TestHandleAcceptHostKey_Preview_NotChanged(t *testing.T) {
	fr := &fakeRefresher{
		inspectResult: hostkeys.InspectResult{
			Host:       "web1",
			CurrentFP:  "SHA256:SAME",
			NewFP:      "SHA256:SAME",
			Changed:    false,
			KnownHosts: "/tmp/kh",
		},
	}
	h := handleAcceptHostKey(fr)
	_, out, err := h(context.Background(), &mcp.CallToolRequest{}, acceptHostKeyIn{Host: "web1"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if out.Changed {
		t.Error("want Changed=false")
	}
	if out.Message != "Host key matches the stored entry; no update is needed." {
		t.Errorf("unexpected message for unchanged key: %q", out.Message)
	}
}

func TestHandleAcceptHostKey_Preview_NoEntryStored(t *testing.T) {
	fr := &fakeRefresher{
		inspectResult: hostkeys.InspectResult{
			Host:       "web1",
			CurrentFP:  "",
			NewFP:      "SHA256:NEW",
			Changed:    false,
			KnownHosts: "/tmp/kh",
		},
	}
	h := handleAcceptHostKey(fr)
	_, out, err := h(context.Background(), &mcp.CallToolRequest{}, acceptHostKeyIn{Host: "web1"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if out.Changed {
		t.Error("want Changed=false when nothing was ever stored")
	}
	if !strings.Contains(out.Message, "No known_hosts entry exists yet") {
		t.Errorf("unexpected message when nothing is stored: %q", out.Message)
	}
}

func TestHandleAcceptHostKey_Preview_StaleEntries(t *testing.T) {
	fr := &fakeRefresher{
		inspectResult: hostkeys.InspectResult{
			Host:       "web1",
			CurrentFP:  "",
			NewFP:      "SHA256:NEW",
			Changed:    false,
			KnownHosts: "/tmp/kh",
			StaleEntries: []hostkeys.StaleEntry{
				{Type: "ssh-rsa", Line: 3, Fingerprint: "SHA256:OLD"},
			},
		},
	}
	h := handleAcceptHostKey(fr)
	_, out, err := h(context.Background(), &mcp.CallToolRequest{}, acceptHostKeyIn{Host: "web1"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if len(out.StaleEntries) != 1 {
		t.Fatalf("want 1 stale entry in output, got %+v", out.StaleEntries)
	}
	if !strings.Contains(out.Message, "ambiguous entries") || !strings.Contains(out.Message, "line 3") {
		t.Errorf("message should lead with the ambiguity blocker and cite the stale line: %q", out.Message)
	}
}

func TestHandleAcceptHostKey_Confirm(t *testing.T) {
	fr := &fakeRefresher{
		acceptResult: hostkeys.AcceptResult{
			Host:       "web1",
			NewFP:      "SHA256:NEW",
			KnownHosts: "/tmp/kh",
			Refreshed:  true,
		},
	}
	h := handleAcceptHostKey(fr)
	_, out, err := h(context.Background(), &mcp.CallToolRequest{}, acceptHostKeyIn{
		Host:                "web1",
		Confirm:             true,
		ExpectedFingerprint: "SHA256:NEW",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !out.Refreshed {
		t.Error("want Refreshed=true")
	}
}

func TestHandleAcceptHostKey_Confirm_ReturnsSuccessResult(t *testing.T) {
	fr := &fakeRefresher{
		acceptResult: hostkeys.AcceptResult{
			Host:       "web1",
			NewFP:      "SHA256:NEW",
			KnownHosts: "/tmp/kh",
			Refreshed:  true,
		},
	}
	h := handleAcceptHostKey(fr)
	result, out, err := h(context.Background(), &mcp.CallToolRequest{}, acceptHostKeyIn{
		Host:                "web1",
		Confirm:             true,
		ExpectedFingerprint: "SHA256:NEW",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("want IsError=false on successful accept, got a tool error result")
	}
	if len(result.Content) != 1 {
		t.Fatalf("want exactly 1 content item, got %d", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is not *mcp.TextContent: %T", result.Content[0])
	}
	var decoded acceptHostKeyOut
	if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil {
		t.Fatalf("content text is not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(decoded, out) {
		t.Errorf("decoded content = %+v, want %+v", decoded, out)
	}
}

func TestHandleAcceptHostKey_GatingError(t *testing.T) {
	fr := &fakeRefresher{
		inspectErr: fmt.Errorf(`host "web1" does not allow known_hosts updates`),
	}
	h := handleAcceptHostKey(fr)
	result, _, err := h(context.Background(), &mcp.CallToolRequest{}, acceptHostKeyIn{Host: "web1"})
	if err != nil {
		t.Fatalf("handler returned Go error; want toolErr result: %v", err)
	}
	if !result.IsError {
		t.Error("want IsError=true for gating error")
	}
}

func TestHandleAcceptHostKey_MissingExpectedFingerprint(t *testing.T) {
	fr := &fakeRefresher{
		acceptErr: fmt.Errorf("expected_fingerprint is required"),
	}
	h := handleAcceptHostKey(fr)
	result, _, err := h(context.Background(), &mcp.CallToolRequest{}, acceptHostKeyIn{
		Host:    "web1",
		Confirm: true,
		// ExpectedFingerprint deliberately omitted
	})
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	if !result.IsError {
		t.Error("want IsError=true when expected_fingerprint is missing")
	}
}
