package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if err != nil {
		return err
	}
	return s.migrateAccounts()
}

func (s *Store) migrateAccounts() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		display_name TEXT NOT NULL,
		email TEXT,
		plan_type TEXT,
		expected_kind TEXT NOT NULL DEFAULT 'any',
		connected INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`); err != nil {
		return err
	}
	var hasExpectedKind bool
	rows, qerr := tx.Query("PRAGMA table_info(accounts)")
	if qerr != nil {
		return qerr
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		_ = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk)
		hasExpectedKind = hasExpectedKind || name == "expected_kind"
	}
	rows.Close()
	if !hasExpectedKind {
		if _, err = tx.Exec("ALTER TABLE accounts ADD COLUMN expected_kind TEXT NOT NULL DEFAULT 'any'"); err != nil {
			return err
		}
	}
	var count int
	if err = tx.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err = tx.Exec("INSERT INTO accounts(id,display_name,created_at,updated_at) VALUES(1,'默认账号',?,?)", time.Now().Unix(), time.Now().Unix()); err != nil {
			return err
		}
	}
	for _, table := range []string{"daily_usage", "limit_snapshots"} {
		var found int
		rows, qerr := tx.Query("PRAGMA table_info(" + table + ")")
		if qerr != nil {
			return qerr
		}
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var def any
			_ = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk)
			if name == "account_id" {
				found = 1
			}
		}
		rows.Close()
		if found == 0 {
			if table == "daily_usage" {
				_, err = tx.Exec(`ALTER TABLE daily_usage RENAME TO daily_usage_legacy;
				CREATE TABLE daily_usage (account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,date TEXT NOT NULL,total_tokens INTEGER NOT NULL,fetched_at INTEGER NOT NULL,PRIMARY KEY(account_id,date));
				INSERT INTO daily_usage SELECT 1,date,total_tokens,fetched_at FROM daily_usage_legacy;
				DROP TABLE daily_usage_legacy;`)
			} else {
				_, err = tx.Exec(`ALTER TABLE limit_snapshots RENAME TO limit_snapshots_legacy;
				CREATE TABLE limit_snapshots (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
					limit_id TEXT NOT NULL,
					window_type TEXT NOT NULL,
					used_percent REAL NOT NULL,
					duration_mins INTEGER NOT NULL,
					resets_at INTEGER NOT NULL,
					fetched_at INTEGER NOT NULL
				);
				INSERT INTO limit_snapshots(id,account_id,limit_id,window_type,used_percent,duration_mins,resets_at,fetched_at)
					SELECT id,1,limit_id,window_type,used_percent,duration_mins,resets_at,fetched_at FROM limit_snapshots_legacy;
				DROP TABLE limit_snapshots_legacy;
				CREATE INDEX idx_limits_time ON limit_snapshots(fetched_at);`)
			}
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

type Account struct {
	ID                int64   `json:"id"`
	DisplayName       string  `json:"displayName"`
	Email             *string `json:"email"`
	PlanType          *string `json:"planType"`
	ExpectedKind      string  `json:"expectedKind"`
	ActualKind        string  `json:"actualKind"`
	ValidationStatus  string  `json:"validationStatus"`
	PossibleDuplicate bool    `json:"possibleDuplicate"`
	Connected         bool    `json:"connected"`
	CreatedAt         int64   `json:"createdAt"`
	UpdatedAt         int64   `json:"updatedAt"`
}

func (s *Store) Accounts() ([]Account, error) {
	rows, e := s.DB.Query("SELECT id,display_name,email,plan_type,expected_kind,connected,created_at,updated_at FROM accounts ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var a Account
		if e = rows.Scan(&a.ID, &a.DisplayName, &a.Email, &a.PlanType, &a.ExpectedKind, &a.Connected, &a.CreatedAt, &a.UpdatedAt); e != nil {
			return nil, e
		}
		a.ActualKind, a.ValidationStatus = AccountKind(a.PlanType), validationStatus(a.ExpectedKind, a.Connected, a.PlanType)
		out = append(out, a)
	}
	for i := range out {
		if out[i].Email == nil || out[i].ActualKind == "unknown" {
			continue
		}
		for j := range out {
			if i != j && out[j].Email != nil && strings.EqualFold(*out[i].Email, *out[j].Email) && out[i].ActualKind == out[j].ActualKind {
				out[i].PossibleDuplicate = true
				break
			}
		}
	}
	return out, rows.Err()
}
func (s *Store) CreateAccount(name string, kinds ...string) (Account, error) {
	expectedKind := "any"
	if len(kinds) > 0 {
		expectedKind = kinds[0]
	}
	now := time.Now().Unix()
	r, e := s.DB.Exec("INSERT INTO accounts(display_name,expected_kind,created_at,updated_at) VALUES(?,?,?,?)", name, expectedKind, now, now)
	if e != nil {
		return Account{}, e
	}
	id, _ := r.LastInsertId()
	return Account{ID: id, DisplayName: name, ExpectedKind: expectedKind, ActualKind: "unknown", ValidationStatus: "pending", CreatedAt: now, UpdatedAt: now}, nil
}
func (s *Store) UpdateAccountSettings(id int64, name, expectedKind string) error {
	r, e := s.DB.Exec("UPDATE accounts SET display_name=?,expected_kind=?,updated_at=? WHERE id=?", name, expectedKind, time.Now().Unix(), id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) RenameAccount(id int64, name string) error {
	var kind string
	if err := s.DB.QueryRow("SELECT expected_kind FROM accounts WHERE id=?", id).Scan(&kind); err != nil {
		return err
	}
	return s.UpdateAccountSettings(id, name, kind)
}

func ValidExpectedKind(kind string) bool {
	return kind == "any" || kind == "personal" || kind == "team"
}

func AccountKind(plan *string) string {
	if plan == nil {
		return "unknown"
	}
	switch strings.ToLower(strings.TrimSpace(*plan)) {
	case "free", "go", "plus", "pro", "prolite":
		return "personal"
	case "team", "business", "self_serve_business_prolite", "self_serve_business_usage_based":
		return "team"
	default:
		return "unknown"
	}
}

func validationStatus(expected string, connected bool, plan *string) string {
	if !connected {
		return "pending"
	}
	actual := AccountKind(plan)
	if actual == "unknown" {
		return "unknown"
	}
	if expected == "any" || expected == actual {
		return "matched"
	}
	return "mismatch"
}
func (s *Store) UpdateAccount(id int64, email, plan *string, connected bool) error {
	_, e := s.DB.Exec("UPDATE accounts SET email=?,plan_type=?,connected=?,updated_at=? WHERE id=?", email, plan, connected, time.Now().Unix(), id)
	return e
}
func (s *Store) DeleteAccount(id int64) error {
	_, e := s.DB.Exec("DELETE FROM accounts WHERE id=?", id)
	return e
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
