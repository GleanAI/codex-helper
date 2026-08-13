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
	"sync"
	"sync/atomic"
	"time"
)

type Client struct {
	mu        sync.Mutex
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
func (c *Client) Initialize(ctx context.Context) error {
	var out any
	if e := c.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "codex-helper", "title": "Codex Helper", "version": "0.1.0"}, "capabilities": map[string]any{}}, &out); e != nil {
		return e
	}
	return c.send(map[string]any{"method": "initialized", "params": map[string]any{}})
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
	c.failAll(cmd)
}
func (c *Client) failAll(cmd *exec.Cmd) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// A previous process may finish after its replacement has started. It must
	// not mark the new connection as disconnected or fail its pending calls.
	if c.cmd != cmd {
		return
	}
	c.connected = false
	c.cmd = nil
	c.in = nil
	for id, ch := range c.pending {
		ch <- envelope{Error: "app-server disconnected"}
		delete(c.pending, id)
	}
}
func (c *Client) send(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		return errors.New("app-server unavailable")
	}
	b, _ := json.Marshal(v)
	b = append(b, '\n')
	_, e := c.in.Write(b)
	return e
}
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	id := c.id.Add(1)
	ch := make(chan envelope, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	if e := c.send(map[string]any{"id": id, "method": method, "params": params}); e != nil {
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
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-time.After(20 * time.Second):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return errors.New("app-server timeout")
	}
}
func (c *Client) Close() error {
	c.mu.Lock()
	cmd := c.cmd
	c.cmd = nil
	c.in = nil
	c.connected = false
	for id, ch := range c.pending {
		ch <- envelope{Error: "app-server disconnected"}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Kill()
	}
	return nil
}
func (c *Client) Connected() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.connected }
