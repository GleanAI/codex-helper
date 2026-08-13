package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSettings(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	if s.Initialized() {
		t.Fatal("fresh DB initialized")
	}
	if e = s.Set("initialized", "true"); e != nil {
		t.Fatal(e)
	}
	if !s.Initialized() {
		t.Fatal("setting not persisted")
	}
}

func TestBackupIncludesCommittedWALData(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()

	if err = s.Set("latest", "committed-in-wal"); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(dir, "snapshot.db")
	if err = s.Backup(context.Background(), backupPath); err != nil {
		t.Fatal(err)
	}

	snapshot, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var value string
	if err = snapshot.QueryRow("SELECT value FROM settings WHERE key='latest'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "committed-in-wal" {
		t.Fatalf("backup value = %q", value)
	}
}
