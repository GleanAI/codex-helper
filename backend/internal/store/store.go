package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "codex-helper.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	s := &Store{DB: db}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.DB.Exec(`
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS admin (id INTEGER PRIMARY KEY CHECK(id=1), username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (token_hash TEXT PRIMARY KEY, expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS daily_usage (date TEXT PRIMARY KEY, total_tokens INTEGER NOT NULL, fetched_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS limit_snapshots (id INTEGER PRIMARY KEY AUTOINCREMENT, limit_id TEXT NOT NULL, window_type TEXT NOT NULL, used_percent REAL NOT NULL, duration_mins INTEGER NOT NULL, resets_at INTEGER NOT NULL, fetched_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS idx_limits_time ON limit_snapshots(fetched_at);
CREATE TABLE IF NOT EXISTS notifications (dedupe_key TEXT PRIMARY KEY, channel TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT, scheduled_at INTEGER NOT NULL, sent_at INTEGER);
CREATE TABLE IF NOT EXISTS telegram_updates (id INTEGER PRIMARY KEY CHECK(id=1), offset INTEGER NOT NULL DEFAULT 0);
INSERT OR IGNORE INTO telegram_updates(id,offset) VALUES(1,0);
`)
	return err
}

func (s *Store) Get(key string) (string, bool) {
	var v string
	err := s.DB.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v)
	return v, err == nil
}
func (s *Store) Set(key, value string) error {
	_, err := s.DB.Exec("INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at", key, value, time.Now().Unix())
	return err
}
func (s *Store) SetJSON(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.Set(key, string(b))
}
func (s *Store) GetJSON(key string, v any) bool {
	raw, ok := s.Get(key)
	return ok && json.Unmarshal([]byte(raw), v) == nil
}
func (s *Store) Initialized() bool { _, ok := s.Get("initialized"); return ok }
func (s *Store) Cleanup(days int) (int64, error) {
	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	var n int64
	for _, q := range []string{"DELETE FROM limit_snapshots WHERE fetched_at < ?", "DELETE FROM notifications WHERE scheduled_at < ?"} {
		r, e := s.DB.Exec(q, cutoff)
		if e != nil {
			return n, e
		}
		x, _ := r.RowsAffected()
		n += x
	}
	r, e := s.DB.Exec("DELETE FROM daily_usage WHERE date < ?", time.Now().AddDate(0, 0, -days).Format("2006-01-02"))
	if e == nil {
		x, _ := r.RowsAffected()
		n += x
	}
	return n, e
}
func (s *Store) Health(ctx context.Context) error {
	if err := s.DB.PingContext(ctx); err != nil {
		return fmt.Errorf("sqlite: %w", err)
	}
	return nil
}

// Backup creates a transactionally consistent standalone SQLite snapshot.
// VACUUM INTO includes committed WAL contents without interrupting writers.
func (s *Store) Backup(ctx context.Context, path string) error {
	_, err := s.DB.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}
