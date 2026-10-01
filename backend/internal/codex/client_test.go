package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	client                      *Client
	requests                    []map[string]any
	completionStatus            string
	completionError             string
	turnStartError              string
	turnStarted                 chan struct{}
	turnRelease                 chan struct{}
	interruptError              string
	suppressInterruptCompletion bool
	unsubscribeError            string
	unsubscribeStatus           string
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
		result = `{"turn":{"id":"turn-1","status":"inProgress","items":[]}}`
	case "turn/interrupt":
		result = `{}`
	case "thread/unsubscribe":
		status := w.unsubscribeStatus
		if status == "" {
			status = "unsubscribed"
		}
		result = fmt.Sprintf(`{"status":%q}`, status)
	default:
		return 0, errors.New("unexpected method: " + request.Method)
	}
	w.client.mu.Lock()
	response := w.client.pending[request.ID]
	delete(w.client.pending, request.ID)
	w.client.mu.Unlock()
	if request.Method == "turn/start" && w.turnStartError != "" {
		response <- envelope{Error: w.turnStartError}
	} else if request.Method == "thread/unsubscribe" && w.unsubscribeError != "" {
		response <- envelope{Error: w.unsubscribeError}
	} else if request.Method == "turn/interrupt" && w.interruptError != "" {
		response <- envelope{Error: w.interruptError}
	} else {
		response <- envelope{Result: json.RawMessage(result)}
	}
	if request.Method == "turn/interrupt" && w.interruptError == "" && !w.suppressInterruptCompletion {
		w.client.completeTurn(json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-1","status":"interrupted","items":[]}}`))
	}
	if request.Method == "turn/start" && w.turnStartError == "" {
		status := w.completionStatus
		if status == "" {
			status = "completed"
		}
		params := fmt.Sprintf(`{"threadId":"thread-1","turn":{"id":"turn-1","status":%q,"items":[]}}`, status)
		if w.completionError != "" {
			params = fmt.Sprintf(`{"threadId":"thread-1","turn":{"id":"turn-1","status":%q,"items":[],"error":{"message":%q}}}`, status, w.completionError)
		}
		if w.turnStarted != nil {
			close(w.turnStarted)
		}
		complete := func() { w.client.completeTurn(json.RawMessage(params)) }
		if w.turnRelease != nil {
			go func() {
				<-w.turnRelease
				complete()
			}()
		} else {
			complete()
		}
	}
	return len(p), nil
}

func (w *scriptedWriteCloser) Close() error { return nil }

type blockedTurnStartWriteCloser struct {
	*scriptedWriteCloser
	started   chan struct{}
	release   chan struct{}
	closeOnce sync.Once
}

func (w *blockedTurnStartWriteCloser) Write(p []byte) (int, error) {
	var request struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(p, &request); err != nil {
		return 0, err
	}
	n, err := w.scriptedWriteCloser.Write(p)
	if err == nil && request.Method == "turn/start" {
		// The response is queued, but send cannot finish until Close releases
		// this write. Cancellation must therefore recycle the connection.
		close(w.started)
		<-w.release
	}
	return n, err
}

func (w *blockedTurnStartWriteCloser) Close() error {
	w.closeOnce.Do(func() { close(w.release) })
	return w.scriptedWriteCloser.Close()
}

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
	if len(writer.requests) != 4 {
		t.Fatalf("request count = %d; want 4", len(writer.requests))
	}
	threadParams := writer.requests[1]["params"].(map[string]any)
	if threadParams["sandbox"] != "read-only" {
		t.Fatalf("thread sandbox = %#v; want read-only", threadParams["sandbox"])
	}
	if threadParams["ephemeral"] != true {
		t.Fatalf("thread ephemeral = %#v; want true", threadParams["ephemeral"])
	}
	if threadParams["serviceName"] != "codex-helper-auto-hello" {
		t.Fatalf("thread serviceName = %#v", threadParams["serviceName"])
	}
	turnParams := writer.requests[2]["params"].(map[string]any)
	sandboxPolicy := turnParams["sandboxPolicy"].(map[string]any)
	if sandboxPolicy["type"] != "readOnly" {
		t.Fatalf("turn sandbox = %#v; want readOnly", sandboxPolicy["type"])
	}
	input := turnParams["input"].([]any)
	if input[0].(map[string]any)["text"] != "Hello" {
		t.Fatalf("input = %#v; want Hello", input)
	}
	if writer.requests[3]["method"] != "thread/unsubscribe" {
		t.Fatalf("final request = %#v; want thread/unsubscribe", writer.requests[3])
	}
	unsubscribeParams := writer.requests[3]["params"].(map[string]any)
	if unsubscribeParams["threadId"] != "thread-1" {
		t.Fatalf("thread/unsubscribe params = %#v", unsubscribeParams)
	}
}

func TestSendMessageRecyclesProcessWhenThreadCleanupFails(t *testing.T) {
	c := New(t.TempDir(), nil)
	writer := &scriptedWriteCloser{client: c, unsubscribeError: "cleanup rejected"}
	c.connected = true
	c.cmd = &exec.Cmd{}
	c.in = writer
	if err := c.SendMessage(context.Background(), "Hello"); err != nil {
		t.Fatalf("SendMessage error = %v; delivery was completed", err)
	}
	if c.Connected() {
		t.Fatal("client remained connected after thread cleanup failure")
	}
}

func TestSendMessageUnsubscribesAfterTurnStartFailure(t *testing.T) {
	c := New(t.TempDir(), nil)
	writer := &scriptedWriteCloser{client: c, turnStartError: "invalid turn"}
	c.connected = true
	c.cmd = &exec.Cmd{}
	c.in = writer
	err := c.SendMessage(context.Background(), "Hello")
	if err == nil || !strings.Contains(err.Error(), "invalid turn") {
		t.Fatalf("SendMessage error = %v", err)
	}
	if len(writer.requests) != 4 || writer.requests[3]["method"] != "thread/unsubscribe" {
		t.Fatalf("requests = %#v; want cleanup after turn/start failure", writer.requests)
	}
	if !c.Connected() {
		t.Fatal("client was recycled after successful thread cleanup")
	}
}

func TestRPCErrorIsDistinguishedFromUnknownOutcome(t *testing.T) {
	err := &rpcError{method: "turn/start", detail: "invalid turn"}
	if !isRPCError(err) || IsTurnOutcomeUnknown(err) {
		t.Fatalf("rpc error classification failed: %v", err)
	}
	unknown := &TurnOutcomeUnknownError{Cause: context.DeadlineExceeded}
	if isRPCError(unknown) || !IsTurnOutcomeUnknown(unknown) {
		t.Fatalf("unknown outcome classification failed: %v", unknown)
	}
}

func TestSendMessageWaitsForTurnCompletion(t *testing.T) {
	c := New(t.TempDir(), nil)
	started := make(chan struct{})
	release := make(chan struct{})
	writer := &scriptedWriteCloser{client: c, turnStarted: started, turnRelease: release}
	c.connected = true
	c.cmd = &exec.Cmd{}
	c.in = writer
	done := make(chan error, 1)
	go func() { done <- c.SendMessage(context.Background(), "Hello") }()
	<-started
	select {
	case err := <-done:
		t.Fatalf("SendMessage returned before turn/completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSendMessageReturnsCompletedTurnFailure(t *testing.T) {
	for _, test := range []struct {
		status  string
		message string
	}{
		{status: "failed", message: "upstream rejected turn"},
		{status: "interrupted", message: "status interrupted"},
	} {
		t.Run(test.status, func(t *testing.T) {
			c := New(t.TempDir(), nil)
			writer := &scriptedWriteCloser{client: c, completionStatus: test.status}
			if test.status == "failed" {
				writer.completionError = test.message
			}
			c.connected = true
			c.cmd = &exec.Cmd{}
			c.in = writer
			err := c.SendMessage(context.Background(), "Hello")
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("SendMessage error = %v", err)
			}
		})
	}
}

func TestSendMessageCancellationCleansTurnWaiter(t *testing.T) {
	c := New(t.TempDir(), nil)
	started := make(chan struct{})
	release := make(chan struct{})
	writer := &scriptedWriteCloser{client: c, turnStarted: started, turnRelease: release}
	c.connected = true
	c.cmd = &exec.Cmd{}
	c.in = writer
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = c.Close()
		close(release)
	})
	done := make(chan error, 1)
	go func() { done <- c.SendMessage(ctx, "Hello") }()
	<-started
	cancel()
	err := <-done
	assertCanceledMessageCleaned(t, c, writer.requests, err)
}

func TestSendMessageCancellationDuringTurnStartWriteCleansWaiters(t *testing.T) {
	c := New(t.TempDir(), nil)
	releaseCompletion := make(chan struct{})
	writer := &blockedTurnStartWriteCloser{
		scriptedWriteCloser: &scriptedWriteCloser{client: c, turnRelease: releaseCompletion},
		started:             make(chan struct{}),
		release:             make(chan struct{}),
	}
	c.connected = true
	c.cmd = &exec.Cmd{}
	c.in = writer
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = c.Close()
		close(releaseCompletion)
	})
	done := make(chan error, 1)
	go func() { done <- c.SendMessage(ctx, "Hello") }()
	select {
	case <-writer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn/start write did not start")
	}
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SendMessage did not return after cancellation during turn/start write")
	}
	if len(writer.requests) != 3 {
		t.Fatalf("requests = %#v; want only model/list, thread/start and turn/start", writer.requests)
	}
	assertCanceledMessageCleaned(t, c, writer.requests, err)
}

func assertCanceledMessageCleaned(t *testing.T, c *Client, requests []map[string]any, err error) {
	t.Helper()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SendMessage error = %v; want context canceled", err)
	}
	c.mu.Lock()
	pending := len(c.pending)
	c.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending calls = %d; want 0", pending)
	}
	c.turnMu.Lock()
	remaining := len(c.turns)
	c.turnMu.Unlock()
	if remaining != 0 {
		t.Fatalf("turn waiters = %d; want 0", remaining)
	}
	switch len(requests) {
	case 3:
		if c.Connected() {
			t.Fatal("client remained connected after cancellation during turn/start write")
		}
		if !IsTurnOutcomeUnknown(err) {
			t.Fatalf("SendMessage error = %v; want unknown turn/start outcome", err)
		}
	case 4:
		if requests[3]["method"] != "thread/unsubscribe" {
			t.Fatalf("requests = %#v; want cleanup after unknown turn/start outcome", requests)
		}
	case 5:
		if requests[3]["method"] != "turn/interrupt" || requests[4]["method"] != "thread/unsubscribe" {
			t.Fatalf("requests = %#v; want turn/interrupt followed by cleanup", requests)
		}
	default:
		t.Fatalf("requests = %#v; want cancellation during turn/start write or before or after response consumption", requests)
	}
	for i, method := range []string{"model/list", "thread/start", "turn/start"} {
		if requests[i]["method"] != method {
			t.Fatalf("request %d = %#v; want %s", i, requests[i], method)
		}
	}
}

func TestInterruptTurnUsesAcceptedTurnID(t *testing.T) {
	c := New(t.TempDir(), nil)
	writer := &scriptedWriteCloser{client: c}
	c.connected = true
	c.cmd = &exec.Cmd{}
	c.in = writer
	completed := make(chan turnCompletion, 1)
	c.turns["thread-1"] = completed
	err := c.interruptTurn("thread-1", "turn-1", completed, context.Canceled)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "status interrupted") {
		t.Fatalf("interruptTurn error = %v", err)
	}
	if len(writer.requests) != 1 || writer.requests[0]["method"] != "turn/interrupt" {
		t.Fatalf("requests = %#v; want turn/interrupt", writer.requests)
	}
	interruptParams := writer.requests[0]["params"].(map[string]any)
	if interruptParams["threadId"] != "thread-1" || interruptParams["turnId"] != "turn-1" {
		t.Fatalf("turn/interrupt params = %#v", interruptParams)
	}
	c.turnMu.Lock()
	remaining := len(c.turns)
	c.turnMu.Unlock()
	if remaining != 0 {
		t.Fatalf("turn waiters = %d; want 0", remaining)
	}
}

func TestSendMessageDoesNotRetryWhenInterruptedTurnOutcomeIsUnknown(t *testing.T) {
	c := New(t.TempDir(), nil)
	started := make(chan struct{})
	release := make(chan struct{})
	writer := &scriptedWriteCloser{
		client:         c,
		turnStarted:    started,
		turnRelease:    release,
		interruptError: "interrupt rejected",
	}
	c.connected = true
	c.cmd = &exec.Cmd{}
	c.in = writer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.SendMessage(ctx, "Hello") }()
	<-started
	cancel()
	err := <-done
	if !IsTurnOutcomeUnknown(err) || !errors.Is(err, context.Canceled) {
		t.Fatalf("SendMessage error = %v; want unknown outcome wrapping cancellation", err)
	}
	close(release)
}

func TestCloseInterruptsTurnWaiter(t *testing.T) {
	c := New(t.TempDir(), nil)
	started := make(chan struct{})
	release := make(chan struct{})
	writer := &scriptedWriteCloser{client: c, turnStarted: started, turnRelease: release}
	c.connected = true
	c.cmd = &exec.Cmd{}
	c.in = writer
	done := make(chan error, 1)
	go func() { done <- c.SendMessage(context.Background(), "Hello") }()
	<-started
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("SendMessage error = %v; want disconnected", err)
	}
	c.turnMu.Lock()
	remaining := len(c.turns)
	c.turnMu.Unlock()
	if remaining != 0 {
		t.Fatalf("turn waiters = %d; want 0", remaining)
	}
	close(release)
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
