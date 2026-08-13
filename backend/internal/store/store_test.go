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

func TestAccountsAndPerAccountUsage(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	accounts, err := s.Accounts()
	if err != nil || len(accounts) != 1 || accounts[0].ID != 1 {
		t.Fatalf("default accounts = %#v, %v", accounts, err)
	}
	second, err := s.CreateAccount("Team workspace")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, second.ID} {
		if _, err = s.DB.Exec("INSERT INTO daily_usage(account_id,date,total_tokens,fetched_at) VALUES(?,?,?,?)", id, "2026-08-13", id*100, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.DeleteAccount(second.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM daily_usage WHERE account_id=?", second.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("usage was not cascaded: %d, %v", count, err)
	}
}

func TestAccountKindAndValidation(t *testing.T) {
	tests := []struct{ plan, kind string }{
		{"plus", "personal"}, {"Pro", "personal"}, {"team", "team"},
		{"business", "team"}, {"self_serve_business_usage_based", "team"},
		{"enterprise", "unknown"}, {"", "unknown"},
	}
	for _, tt := range tests {
		plan := tt.plan
		if got := AccountKind(&plan); got != tt.kind {
			t.Errorf("AccountKind(%q) = %q; want %q", tt.plan, got, tt.kind)
		}
	}
	if got := validationStatus("team", true, ptr("plus")); got != "mismatch" {
		t.Fatalf("team/plus validation = %q; want mismatch", got)
	}
	if got := validationStatus("team", true, ptr("business")); got != "matched" {
		t.Fatalf("team/business validation = %q; want matched", got)
	}
}

func TestExistingAccountsGainExpectedKind(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "codex-helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT, display_name TEXT NOT NULL, email TEXT,
		plan_type TEXT, connected INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	); INSERT INTO accounts VALUES(1,'旧连接','user@example.com','team',1,1,1);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	accounts, err := s.Accounts()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts = %#v, %v", accounts, err)
	}
	if accounts[0].ExpectedKind != "any" || accounts[0].ActualKind != "team" || accounts[0].ValidationStatus != "matched" {
		t.Fatalf("migrated account = %#v", accounts[0])
	}
}

func ptr(value string) *string { return &value }

func TestLegacyUsageMigratesToDefaultAccount(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "codex-helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE daily_usage(date TEXT PRIMARY KEY,total_tokens INTEGER NOT NULL,fetched_at INTEGER NOT NULL); INSERT INTO daily_usage VALUES('2026-08-12',321,1)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var accountID, tokens int64
	if err = s.DB.QueryRow("SELECT account_id,total_tokens FROM daily_usage WHERE date='2026-08-12'").Scan(&accountID, &tokens); err != nil {
		t.Fatal(err)
	}
	if accountID != 1 || tokens != 321 {
		t.Fatalf("migrated row = account %d, tokens %d", accountID, tokens)
	}
}

func TestPopulatedLegacyLimitSnapshotsMigrateToDefaultAccount(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "codex-helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE limit_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			limit_id TEXT NOT NULL,
			window_type TEXT NOT NULL,
			used_percent REAL NOT NULL,
			duration_mins INTEGER NOT NULL,
			resets_at INTEGER NOT NULL,
			fetched_at INTEGER NOT NULL
		);
		CREATE INDEX idx_limits_time ON limit_snapshots(fetched_at);
		INSERT INTO limit_snapshots(id,limit_id,window_type,used_percent,duration_mins,resets_at,fetched_at)
			VALUES(7,'codex','primary',42.5,300,1700000000,1699990000);
	`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var id, accountID, duration int64
	var limitID, window string
	var used float64
	err = s.DB.QueryRow("SELECT id,account_id,limit_id,window_type,used_percent,duration_mins FROM limit_snapshots").Scan(&id, &accountID, &limitID, &window, &used, &duration)
	if err != nil {
		t.Fatal(err)
	}
	if id != 7 || accountID != 1 || limitID != "codex" || window != "primary" || used != 42.5 || duration != 300 {
		t.Fatalf("migrated limit = id %d, account %d, %s/%s, %.1f, duration %d", id, accountID, limitID, window, used, duration)
	}
	var indexCount int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_limits_time'").Scan(&indexCount); err != nil || indexCount != 1 {
		t.Fatalf("limit index was not recreated: %d, %v", indexCount, err)
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
