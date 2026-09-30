package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type errorWriteCloser struct{}

func (errorWriteCloser) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
func (errorWriteCloser) Close() error              { return nil }

type blockedWriteCloser struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

type scriptedWriteCloser struct {
	client   *Client
	requests []map[string]any
}

func (w *scriptedWriteCloser) Write(p []byte) (int, error) {
	var request struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(p, &request); err != nil {
		return 0, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(p, &decoded); err != nil {
		return 0, err
	}
	w.requests = append(w.requests, decoded)
	var result string
	switch request.Method {
	case "model/list":
		result = `{"data":[{"id":"gpt-6-luna","supportedReasoningEfforts":[{"reasoningEffort":"low"}]}]}`
	case "thread/start":
		result = `{"thread":{"id":"thread-1"}}`
	case "turn/start":
		result = `{}`
	default:
		return 0, errors.New("unexpected method: " + request.Method)
	}
	w.client.mu.Lock()
	response := w.client.pending[request.ID]
	delete(w.client.pending, request.ID)
	w.client.mu.Unlock()
	response <- envelope{Result: json.RawMessage(result)}
	return len(p), nil
}

func (w *scriptedWriteCloser) Close() error { return nil }

func (w *blockedWriteCloser) Write([]byte) (int, error) {
	w.startedOnce.Do(func() { close(w.started) })
	<-w.release
	return 0, io.ErrClosedPipe
}
func (w *blockedWriteCloser) Close() error {
	w.startedOnce.Do(func() { close(w.started) })
	w.releaseOnce.Do(func() { close(w.release) })
	return nil
}

func TestEnsureConfigDirCreatesNestedDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "accounts", "2", "codex")
	if err := ensureConfigDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}
	if got := info.Mode().Perm(); got != 0700 {
		t.Fatalf("permissions = %o; want 700", got)
	}
}

func TestEnsureConfigDirReportsCreationFailure(t *testing.T) {
	parent := t.TempDir()
	file := filepath.Join(parent, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(file, "codex")
	err := ensureConfigDir(dir)
	if err == nil {
		t.Fatal("expected directory creation to fail")
	}
	if !strings.Contains(err.Error(), "create CODEX_HOME") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("error = %q; want CODEX_HOME path context", err)
	}
}

func TestSelectHelloModelPrefersLowCostModelAndLowestEffort(t *testing.T) {
	models := []modelInfo{
		{ID: "gpt-6.1-sol", IsDefault: true, SupportedReasoningEfforts: []struct {
			ReasoningEffort string `json:"reasoningEffort"`
		}{{ReasoningEffort: "medium"}}},
		{ID: "gpt-6-luna", SupportedReasoningEfforts: []struct {
			ReasoningEffort string `json:"reasoningEffort"`
		}{{ReasoningEffort: "low"}, {ReasoningEffort: "medium"}}},
	}
	model, effort := selectHelloModel(models)
	if model != "gpt-6-luna" || effort != "low" {
		t.Fatalf("selected model=%q effort=%q; want gpt-6-luna/low", model, effort)
	}
}

func TestSelectHelloModelFallsBackToDefault(t *testing.T) {
	model, effort := selectHelloModel([]modelInfo{{ID: "gpt-6.1-sol", IsDefault: true}})
	if model != "gpt-6.1-sol" || effort != "" {
		t.Fatalf("selected model=%q effort=%q; want default without effort", model, effort)
	}
}

func TestSendMessageUsesReadOnlySandbox(t *testing.T) {
	c := New(t.TempDir(), nil)
	writer := &scriptedWriteCloser{client: c}
	c.connected = true
	c.cmd = &exec.Cmd{}
	c.in = writer
	if err := c.SendMessage(context.Background(), "Hello"); err != nil {
		t.Fatal(err)
	}
	if len(writer.requests) != 3 {
		t.Fatalf("request count = %d; want 3", len(writer.requests))
	}
	threadParams := writer.requests[1]["params"].(map[string]any)
	if threadParams["sandbox"] != "read-only" {
		t.Fatalf("thread sandbox = %#v; want read-only", threadParams["sandbox"])
	}
	turnParams := writer.requests[2]["params"].(map[string]any)
	sandboxPolicy := turnParams["sandboxPolicy"].(map[string]any)
	if sandboxPolicy["type"] != "read-only" {
		t.Fatalf("turn sandbox = %#v; want read-only", sandboxPolicy["type"])
	}
	input := turnParams["input"].([]any)
	if input[0].(map[string]any)["text"] != "Hello" {
		t.Fatalf("input = %#v; want Hello", input)
	}
}

func TestCallCleansPendingAfterWriteFailure(t *testing.T) {
	c := New(t.TempDir(), nil)
	c.connected = true
	c.cmd = &exec.Cmd{}
	c.in = errorWriteCloser{}
	if err := c.Call(context.Background(), "account/read", nil, nil); err == nil {
		t.Fatal("expected write failure")
	}
	if len(c.pending) != 0 {
		t.Fatalf("pending = %d; want 0", len(c.pending))
	}
}

func TestCanceledCallDoesNotTouchConnectedProcess(t *testing.T) {
	c := New(t.TempDir(), nil)
	c.connected = true
	c.cmd = &exec.Cmd{}
	c.in = errorWriteCloser{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Call(ctx, "account/read", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if !c.connected || len(c.pending) != 0 {
		t.Fatalf("connected = %v, pending = %d", c.connected, len(c.pending))
	}
}

func TestCloseInterruptsBlockedWrite(t *testing.T) {
	w := &blockedWriteCloser{started: make(chan struct{}), release: make(chan struct{})}
	c := New(t.TempDir(), nil)
	c.connected = true
	c.cmd = &exec.Cmd{}
	c.in = w
	callDone := make(chan error, 1)
	go func() { callDone <- c.Call(context.Background(), "account/read", nil, nil) }()
	<-w.started
	closeDone := make(chan struct{})
	go func() {
		_ = c.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not interrupt blocked write")
	}
	select {
	case <-callDone:
	case <-time.After(time.Second):
		t.Fatal("Call remained blocked after Close")
	}
}

type chunkWriter struct{ bytes []byte }

func (w *chunkWriter) Write(p []byte) (int, error) {
	n := min(2, len(p))
	w.bytes = append(w.bytes, p[:n]...)
	return n, nil
}

func TestWriteFullHandlesShortWrites(t *testing.T) {
	w := &chunkWriter{}
	if err := writeFull(w, []byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if string(w.bytes) != "abcdef" {
		t.Fatalf("written = %q", w.bytes)
	}
}
