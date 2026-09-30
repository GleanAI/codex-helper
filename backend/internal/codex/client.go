package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	cmd       *exec.Cmd
	in        io.WriteCloser
	pending   map[int64]chan envelope
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

func New(configDir string, notify func(string, json.RawMessage)) *Client {
	return &Client{pending: map[int64]chan envelope{}, configDir: configDir, notify: notify}
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
		"sandbox":        "read-only",
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
		if model != "" {
			delete(threadParams, "model")
			if fallbackErr := c.Call(ctx, "thread/start", threadParams, &thread); fallbackErr != nil {
				return err
			}
			activeModel = ""
			effort = ""
		} else {
			return err
		}
	}
	if thread.Thread.ID == "" {
		return errors.New("app-server thread/start returned no thread")
	}
	turnParams := map[string]any{
		"threadId":       thread.Thread.ID,
		"input":          []map[string]string{{"type": "text", "text": message}},
		"approvalPolicy": "never",
		"sandboxPolicy":  map[string]any{"type": "read-only"},
	}
	if activeModel != "" {
		turnParams["model"] = activeModel
	}
	if effort != "" {
		turnParams["effort"] = effort
	}
	return c.Call(ctx, "turn/start", turnParams, nil)
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
		} else if e.Method != "" && c.notify != nil {
			go c.notify(e.Method, e.Params)
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
			return fmt.Errorf("app-server %s: %v", method, e.Error)
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
	if in != nil {
		_ = in.Close()
	}
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Kill()
	}
	return nil
}
func (c *Client) Connected() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.connected }
