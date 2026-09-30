package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Client struct {
	mu        sync.Mutex
	writeMu   sync.Mutex
	turnMu    sync.Mutex
	cmd       *exec.Cmd
	in        io.WriteCloser
	pending   map[int64]chan envelope
	turns     map[string]chan turnCompletion
	id        atomic.Int64
	connected bool
	configDir string
	notify    func(string, json.RawMessage)
}
type envelope struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  any             `json:"error,omitempty"`
}

type appServerTurn struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type turnCompletion struct {
	turn appServerTurn
	err  error
}

// TurnOutcomeUnknownError means a turn was accepted by app-server but its
// terminal status could not be confirmed. Callers must not automatically
// repeat the message because the original turn may still complete upstream.
type TurnOutcomeUnknownError struct {
	Cause error
}

type rpcError struct {
	method string
	detail any
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("app-server %s: %v", e.method, e.detail)
}

func isRPCError(err error) bool {
	var target *rpcError
	return errors.As(err, &target)
}

func (e *TurnOutcomeUnknownError) Error() string {
	if e == nil || e.Cause == nil {
		return "app-server turn outcome is unknown"
	}
	return "app-server turn outcome is unknown: " + e.Cause.Error()
}

func (e *TurnOutcomeUnknownError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func IsTurnOutcomeUnknown(err error) bool {
	var target *TurnOutcomeUnknownError
	return errors.As(err, &target)
}

func New(configDir string, notify func(string, json.RawMessage)) *Client {
	return &Client{pending: map[int64]chan envelope{}, turns: map[string]chan turnCompletion{}, configDir: configDir, notify: notify}
}
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		return nil
	}
	if e := ensureConfigDir(c.configDir); e != nil {
		return e
	}
	cmd := exec.CommandContext(ctx, "codex", "app-server")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+c.configDir)
	out, e := cmd.StdoutPipe()
	if e != nil {
		return e
	}
	in, e := cmd.StdinPipe()
	if e != nil {
		return e
	}
	cmd.Stderr = os.Stderr
	if e = cmd.Start(); e != nil {
		return e
	}
	c.cmd, c.in, c.connected = cmd, in, true
	go c.read(cmd, out)
	go func() { _ = cmd.Wait(); c.failAll(cmd) }()
	return nil
}

func ensureConfigDir(dir string) error {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return fmt.Errorf("create CODEX_HOME %q: %w", dir, e)
	}
	return nil
}
func (c *Client) Initialize(ctx context.Context) error {
	var out any
	if e := c.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "codex-helper", "title": "Codex Helper", "version": "0.1.0"}, "capabilities": map[string]any{}}, &out); e != nil {
		return e
	}
	return c.send(ctx, map[string]any{"method": "initialized", "params": map[string]any{}})
}

type modelInfo struct {
	ID                        string `json:"id"`
	Model                     string `json:"model"`
	IsDefault                 bool   `json:"isDefault"`
	Hidden                    bool   `json:"hidden"`
	DefaultReasoningEffort    string `json:"defaultReasoningEffort"`
	SupportedReasoningEfforts []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}

type modelListResponse struct {
	Data []modelInfo `json:"data"`
}

const preferredHelloModel = "gpt-6-luna"

// SendMessage starts a short, isolated turn for an automated message. The
// model catalog is optional because older app-server versions may not expose
// model/list; the fixed low-cost model remains the first compatibility choice.
func (c *Client) SendMessage(ctx context.Context, message string) error {
	model, effort := c.helloModel(ctx)
	activeModel := model
	threadParams := map[string]any{
		"approvalPolicy": "never",
		"ephemeral":      true,
		"sandbox":        "read-only",
		"serviceName":    "codex-helper-auto-hello",
	}
	if model != "" {
		threadParams["model"] = model
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := c.Call(ctx, "thread/start", threadParams, &thread); err != nil {
		if model == "" || !isRPCError(err) {
			if !isRPCError(err) {
				_ = c.Close()
			}
			return err
		}
		delete(threadParams, "model")
		if fallbackErr := c.Call(ctx, "thread/start", threadParams, &thread); fallbackErr != nil {
			if !isRPCError(fallbackErr) {
				_ = c.Close()
			}
			return fallbackErr
		}
		activeModel = ""
		effort = ""
	}
	if thread.Thread.ID == "" {
		return errors.New("app-server thread/start returned no thread")
	}
	defer c.releaseThread(thread.Thread.ID)
	completed := make(chan turnCompletion, 1)
	c.turnMu.Lock()
	if c.turns == nil {
		c.turns = map[string]chan turnCompletion{}
	}
	if _, exists := c.turns[thread.Thread.ID]; exists {
		c.turnMu.Unlock()
		return errors.New("app-server thread already has an active turn")
	}
	c.turns[thread.Thread.ID] = completed
	c.turnMu.Unlock()
	defer c.removeTurnWaiter(thread.Thread.ID, completed)

	turnParams := map[string]any{
		"threadId":       thread.Thread.ID,
		"input":          []map[string]string{{"type": "text", "text": message}},
		"approvalPolicy": "never",
		"sandboxPolicy":  map[string]any{"type": "readOnly"},
	}
	if activeModel != "" {
		turnParams["model"] = activeModel
	}
	if effort != "" {
		turnParams["effort"] = effort
	}
	var started struct {
		Turn appServerTurn `json:"turn"`
	}
	if err := c.Call(ctx, "turn/start", turnParams, &started); err != nil {
		if !isRPCError(err) {
			return &TurnOutcomeUnknownError{Cause: err}
		}
		return err
	}
	if started.Turn.ID == "" {
		return errors.New("app-server turn/start returned no turn")
	}
	if started.Turn.Status != "" && started.Turn.Status != "inProgress" {
		return completedTurnError(started.Turn)
	}
	select {
	case result := <-completed:
		if result.err != nil {
			return &TurnOutcomeUnknownError{Cause: result.err}
		}
		if result.turn.ID != "" && result.turn.ID != started.Turn.ID {
			return &TurnOutcomeUnknownError{Cause: errors.New("app-server completed an unexpected turn")}
		}
		return completedTurnError(result.turn)
	case <-ctx.Done():
		return c.interruptTurn(thread.Thread.ID, started.Turn.ID, completed, ctx.Err())
	}
}

const (
	turnInterruptTimeout = 5 * time.Second
	threadCleanupTimeout = 5 * time.Second
)

func (c *Client) releaseThread(threadID string) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), threadCleanupTimeout)
	defer cancel()
	var result struct {
		Status string `json:"status"`
	}
	err := c.Call(cleanupCtx, "thread/unsubscribe", map[string]any{"threadId": threadID}, &result)
	if err == nil && (result.Status == "unsubscribed" || result.Status == "notSubscribed" || result.Status == "notLoaded") {
		return
	}
	if err == nil {
		err = fmt.Errorf("unexpected status %q", result.Status)
	}
	log.Printf("app-server thread cleanup failed; recycling process: %v", err)
	_ = c.Close()
}

func (c *Client) interruptTurn(threadID, turnID string, completed <-chan turnCompletion, cause error) error {
	interruptCtx, cancel := context.WithTimeout(context.Background(), turnInterruptTimeout)
	defer cancel()
	interruptErr := c.Call(interruptCtx, "turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	}, nil)
	if interruptErr != nil {
		select {
		case result := <-completed:
			return turnCompletionAfterCancellation(result, turnID, cause)
		default:
			return &TurnOutcomeUnknownError{Cause: fmt.Errorf("%w; interrupt failed: %v", cause, interruptErr)}
		}
	}
	select {
	case result := <-completed:
		return turnCompletionAfterCancellation(result, turnID, cause)
	case <-interruptCtx.Done():
		select {
		case result := <-completed:
			return turnCompletionAfterCancellation(result, turnID, cause)
		default:
			return &TurnOutcomeUnknownError{Cause: fmt.Errorf("%w; interrupt completion was not observed", cause)}
		}
	}
}

func turnCompletionAfterCancellation(result turnCompletion, turnID string, cause error) error {
	if result.err != nil {
		return &TurnOutcomeUnknownError{Cause: fmt.Errorf("%w; %v", cause, result.err)}
	}
	if result.turn.ID != "" && result.turn.ID != turnID {
		return &TurnOutcomeUnknownError{Cause: fmt.Errorf("%w; app-server completed an unexpected turn", cause)}
	}
	if err := completedTurnError(result.turn); err != nil {
		return fmt.Errorf("%w; %v", cause, err)
	}
	return nil
}

func completedTurnError(turn appServerTurn) error {
	if turn.Status == "completed" {
		return nil
	}
	message := ""
	if turn.Error != nil {
		message = strings.TrimSpace(turn.Error.Message)
	}
	if message == "" {
		message = "status " + turn.Status
	}
	return fmt.Errorf("app-server turn did not complete: %s", message)
}

func (c *Client) removeTurnWaiter(threadID string, waiter chan turnCompletion) {
	c.turnMu.Lock()
	if c.turns[threadID] == waiter {
		delete(c.turns, threadID)
	}
	c.turnMu.Unlock()
}

func (c *Client) completeTurn(params json.RawMessage) {
	var completed struct {
		ThreadID string        `json:"threadId"`
		Turn     appServerTurn `json:"turn"`
	}
	if json.Unmarshal(params, &completed) != nil || completed.ThreadID == "" {
		return
	}
	c.turnMu.Lock()
	waiter := c.turns[completed.ThreadID]
	if waiter != nil {
		delete(c.turns, completed.ThreadID)
	}
	c.turnMu.Unlock()
	if waiter != nil {
		waiter <- turnCompletion{turn: completed.Turn}
	}
}

func (c *Client) failTurnWaiters(err error) {
	c.turnMu.Lock()
	waiters := make([]chan turnCompletion, 0, len(c.turns))
	for threadID, waiter := range c.turns {
		waiters = append(waiters, waiter)
		delete(c.turns, threadID)
	}
	c.turnMu.Unlock()
	for _, waiter := range waiters {
		waiter <- turnCompletion{err: err}
	}
}

func (c *Client) helloModel(ctx context.Context) (string, string) {
	var listed modelListResponse
	if err := c.Call(ctx, "model/list", map[string]any{"limit": 20, "includeHidden": false}, &listed); err != nil {
		return preferredHelloModel, "low"
	}
	return selectHelloModel(listed.Data)
}

func selectHelloModel(models []modelInfo) (string, string) {
	var fallback *modelInfo
	for i := range models {
		model := &models[i]
		if model.Hidden {
			continue
		}
		id := strings.ToLower(model.ID)
		if id == "" {
			id = strings.ToLower(model.Model)
		}
		if id == preferredHelloModel || strings.Contains(id, "mini") || strings.Contains(id, "luna") {
			return modelName(*model), lowestEffort(*model)
		}
		if fallback == nil || model.IsDefault {
			fallback = model
		}
	}
	if fallback == nil {
		return preferredHelloModel, "low"
	}
	return modelName(*fallback), lowestEffort(*fallback)
}

func modelName(model modelInfo) string {
	if model.ID != "" {
		return model.ID
	}
	return model.Model
}

func lowestEffort(model modelInfo) string {
	for _, candidate := range []string{"minimal", "low", "medium", "high", "xhigh"} {
		for _, supported := range model.SupportedReasoningEfforts {
			if supported.ReasoningEffort == candidate {
				return candidate
			}
		}
	}
	return ""
}
func (c *Client) read(cmd *exec.Cmd, r io.Reader) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for s.Scan() {
		var e envelope
		if json.Unmarshal(s.Bytes(), &e) != nil {
			continue
		}
		if e.ID != nil {
			c.mu.Lock()
			ch := c.pending[*e.ID]
			delete(c.pending, *e.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- e
			}
		} else if e.Method != "" {
			if e.Method == "turn/completed" {
				c.completeTurn(e.Params)
			}
			if c.notify != nil {
				go c.notify(e.Method, e.Params)
			}
		}
	}
	c.failProcess(cmd, true)
}
func (c *Client) failAll(cmd *exec.Cmd) { c.failProcess(cmd, false) }

func (c *Client) failProcess(cmd *exec.Cmd, kill bool) {
	c.mu.Lock()
	// A previous process may finish after its replacement has started. It must
	// not mark the new connection as disconnected or fail its pending calls.
	if c.cmd != cmd {
		c.mu.Unlock()
		return
	}
	in := c.in
	c.connected = false
	c.cmd = nil
	c.in = nil
	for id, ch := range c.pending {
		ch <- envelope{Error: "app-server disconnected"}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.failTurnWaiters(errors.New("app-server disconnected"))
	if in != nil {
		_ = in.Close()
	}
	if kill && cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (c *Client) send(ctx context.Context, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return errors.New("app-server unavailable")
	}
	cmd, in := c.cmd, c.in
	c.mu.Unlock()

	written := make(chan error, 1)
	go func() {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		c.mu.Lock()
		current := c.connected && c.cmd == cmd && c.in == in
		c.mu.Unlock()
		if !current {
			written <- errors.New("app-server unavailable")
			return
		}
		written <- writeFull(in, b)
	}()

	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case err = <-written:
		if err != nil {
			c.failProcess(cmd, true)
		}
		return err
	case <-ctx.Done():
		c.failProcess(cmd, true)
		<-written
		return ctx.Err()
	case <-timer.C:
		c.failProcess(cmd, true)
		<-written
		return errors.New("app-server timeout")
	}
}

func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(b) {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	id := c.id.Add(1)
	ch := make(chan envelope, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	if e := c.send(callCtx, map[string]any{"id": id, "method": method, "params": params}); e != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return e
	}
	select {
	case e := <-ch:
		if e.Error != nil {
			return &rpcError{method: method, detail: e.Error}
		}
		if out != nil {
			return json.Unmarshal(e.Result, out)
		}
		return nil
	case <-callCtx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.New("app-server timeout")
	}
}
func (c *Client) Close() error {
	c.mu.Lock()
	cmd := c.cmd
	in := c.in
	c.cmd = nil
	c.in = nil
	c.connected = false
	for id, ch := range c.pending {
		ch <- envelope{Error: "app-server disconnected"}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.failTurnWaiters(errors.New("app-server disconnected"))
	if in != nil {
		_ = in.Close()
	}
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Kill()
	}
	return nil
}
func (c *Client) Connected() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.connected }
