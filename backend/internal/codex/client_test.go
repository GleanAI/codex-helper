package codex

import (
	"context"
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
