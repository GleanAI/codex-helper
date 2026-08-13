package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
