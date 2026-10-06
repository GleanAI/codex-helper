package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

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

func TestAutoHelloLogsKeepLatestFive(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	for i := int64(1); i <= 6; i++ {
		key := fmt.Sprintf("1:codex:primary:%d:hello", i)
		if _, err = s.DB.Exec(`INSERT INTO notifications
			(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
			VALUES(?,'codex','auto_hello','pending',?,'Hello',1)`, key, i); err != nil {
			t.Fatal(err)
		}
		status := "sent"
		var completed *int64 = &i
		if i == 6 {
			status = "failed"
			completed = nil
		}
		if err = s.RecordAutoHelloResult(key, 1, "codex", "primary", i, status, 1, "", completed, i); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM auto_hello_logs").Scan(&count); err != nil || count != 5 {
		t.Fatalf("auto Hello log count=%d err=%v; want 5", count, err)
	}
	log, ok, err := s.LatestAutoHelloLog()
	if err != nil || !ok || log.Status != "failure" || log.AttemptedAt != 6 {
		t.Fatalf("latest auto Hello log=%#v ok=%v err=%v", log, ok, err)
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

func TestAutoHelloStateSurvivesCleanupAndCascadesWithAccount(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	account, err := s.CreateAccount("Auto Hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO auto_hello_state(account_id,limit_id,window_type,started_at)
		VALUES(?,?,?,?)`, account.ID, "codex", "primary", time.Now().AddDate(0, 0, -400).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Cleanup(30); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM auto_hello_state WHERE account_id=?", account.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("state after cleanup = %d, %v; want 1", count, err)
	}
	if err = s.DeleteAccount(account.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM auto_hello_state WHERE account_id=?", account.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("state after account deletion = %d, %v; want 0", count, err)
	}
}

func TestExistingAutoHelloNotificationsSeedOnlyCurrentEpisodeState(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		limitID   string
		scheduled int64
		lastUse   int64
	}{
		{limitID: "current", scheduled: 200, lastUse: 100},
		{limitID: "finished", scheduled: 100, lastUse: 200},
	} {
		key := fmt.Sprintf("1:%s:primary:%d:hello", row.limitID, row.scheduled+18_000)
		if _, err = s.DB.Exec(`INSERT INTO notifications
			(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
			VALUES(?,'codex','auto_hello','sent',?,'Hello',1)`, key, row.scheduled); err != nil {
			t.Fatal(err)
		}
		if _, err = s.DB.Exec(`INSERT INTO limit_snapshots
			(account_id,limit_id,window_type,used_percent,duration_mins,resets_at,fetched_at)
			VALUES(1,?,'primary',1,300,?,?)`, row.limitID, row.lastUse+18_000, row.lastUse); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.DB.Exec("DROP TABLE auto_hello_state"); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	rows, err := s.DB.Query("SELECT limit_id FROM auto_hello_state ORDER BY limit_id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var limits []string
	for rows.Next() {
		var limitID string
		if err = rows.Scan(&limitID); err != nil {
			t.Fatal(err)
		}
		limits = append(limits, limitID)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(limits) != 1 || limits[0] != "current" {
		t.Fatalf("seeded states = %#v; want current only", limits)
	}
}

func TestAutoHelloUpgradeRecoversCountdownAndDoesNotReseedOnRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the exact previous schema; all migration work must preserve rows.
	if _, err = s.DB.Exec(`DROP TABLE auto_hello_state;
		CREATE TABLE auto_hello_state (
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		limit_id TEXT NOT NULL, window_type TEXT NOT NULL, started_at INTEGER NOT NULL,
		PRIMARY KEY(account_id,limit_id,window_type));
		INSERT INTO auto_hello_state VALUES(1,'codex','primary',1000);
		INSERT INTO notifications(dedupe_key,channel,kind,status,scheduled_at,sent_at,body,account_id)
		VALUES('1:codex:primary:19000:hello','codex','auto_hello','sent',1000,1003,'Hello',1),
		('1:other:primary:19000:hello','codex','auto_hello','expired',1000,NULL,'Hello',1);
		INSERT INTO limit_snapshots(account_id,limit_id,window_type,used_percent,duration_mins,resets_at,fetched_at)
		VALUES(1,'codex','primary',0,300,19004,1003),
		(1,'codex','primary',0,300,19004,1600),
		(1,'codex','primary',0,0,99999,19005),
		(1,'codex','primary',0,300,37304,19304),
		(1,'other','primary',0,300,19004,1600);`); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var completed, reset int64
	if err = s.DB.QueryRow(`SELECT completed_at,active_resets_at FROM auto_hello_state
		WHERE account_id=1 AND limit_id='codex'`).Scan(&completed, &reset); err != nil || completed != 1003 || reset != 19004 {
		t.Fatalf("recovered completed=%d reset=%d err=%v; want 1003/19004", completed, reset, err)
	}
	var unknown int
	if err = s.DB.QueryRow(`SELECT COUNT(*) FROM auto_hello_state WHERE limit_id='other'
		AND completed_at IS NULL AND active_resets_at IS NULL`).Scan(&unknown); err != nil || unknown != 1 {
		t.Fatalf("unconfirmed turn gained activity: count=%d err=%v", unknown, err)
	}
	if _, err = s.DB.Exec(`UPDATE auto_hello_state SET started_at=19304,completed_at=NULL,active_resets_at=NULL
		WHERE limit_id='codex'; DELETE FROM auto_hello_state WHERE limit_id='other'; DELETE FROM limit_snapshots`); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var started int64
	if err = s.DB.QueryRow(`SELECT started_at FROM auto_hello_state WHERE limit_id='codex'
		AND completed_at IS NULL AND active_resets_at IS NULL`).Scan(&started); err != nil || started != 19304 {
		t.Fatalf("restart changed episode: started=%d err=%v", started, err)
	}
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM auto_hello_state WHERE limit_id='other'").Scan(&unknown); err != nil || unknown != 0 {
		t.Fatalf("restart resurrected old task: count=%d err=%v", unknown, err)
	}
}

func TestRecordAutoHelloResultRollsBackWhenEpisodeUpdateFails(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if _, err = s.DB.Exec(`INSERT INTO auto_hello_state(account_id,limit_id,window_type,started_at)
		VALUES(1,'codex','primary',1000);
		INSERT INTO notifications(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
		VALUES('1:codex:primary:19000:hello','codex','auto_hello','pending',1000,'Hello',1);
		CREATE TRIGGER fail_episode BEFORE UPDATE ON auto_hello_state
		BEGIN SELECT RAISE(ABORT,'test episode failure'); END;`); err != nil {
		t.Fatal(err)
	}
	completed := int64(1003)
	if err = s.RecordAutoHelloResult("1:codex:primary:19000:hello", 1, "codex", "primary", 1000, "sent", 1, "", &completed, 1003); err == nil {
		t.Fatal("expected failed transaction")
	}
	var status string
	if err = s.DB.QueryRow("SELECT status FROM notifications").Scan(&status); err != nil || status != "pending" {
		t.Fatalf("delivery was partially committed: status=%q err=%v", status, err)
	}
}

func TestDailyUsageUpsertAndRange(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	second, err := s.CreateAccount("Team workspace", "team")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertDailyUsage(1, []DailyUsage{
		{Date: "2026-08-16", TotalTokens: 100},
		{Date: "2026-08-17", TotalTokens: 200},
	}, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertDailyUsage(1, []DailyUsage{{Date: "2026-08-17", TotalTokens: 250}}, 2); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertDailyUsage(second.ID, []DailyUsage{{Date: "2026-08-17", TotalTokens: 999}}, 2); err != nil {
		t.Fatal(err)
	}
	usage, err := s.DailyUsage(1, "2026-08-17", "2026-08-18")
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || usage[0].Date != "2026-08-17" || usage[0].TotalTokens != 250 {
		t.Fatalf("usage = %#v", usage)
	}
	if err = s.DeleteDailyUsage(1); err != nil {
		t.Fatal(err)
	}
	usage, err = s.DailyUsage(1, "2026-08-16", "2026-08-18")
	if err != nil || len(usage) != 0 {
		t.Fatalf("deleted account usage = %#v, %v", usage, err)
	}
	usage, err = s.DailyUsage(second.ID, "2026-08-16", "2026-08-18")
	if err != nil || len(usage) != 1 || usage[0].TotalTokens != 999 {
		t.Fatalf("other account usage = %#v, %v", usage, err)
	}
	email, plan := "team@example.com", "team"
	if err = s.UpdateAccount(second.ID, &email, &plan, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO auto_hello_state(account_id,limit_id,window_type,started_at)
		VALUES(?,?,?,?)`, second.ID, "codex", "primary", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
		VALUES(?, 'codex', 'auto_hello', 'sent', 1, 'Hello', ?)`, fmt.Sprintf("%d:codex:primary:10:hello", second.ID), second.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DisconnectAccount(second.ID); err != nil {
		t.Fatal(err)
	}
	usage, err = s.DailyUsage(second.ID, "2026-08-16", "2026-08-18")
	if err != nil || len(usage) != 0 {
		t.Fatalf("disconnected account usage = %#v, %v", usage, err)
	}
	accounts, err := s.Accounts()
	if err != nil {
		t.Fatal(err)
	}
	if accounts[1].Connected || accounts[1].Email != nil || accounts[1].PlanType != nil {
		t.Fatalf("disconnected account = %#v", accounts[1])
	}
	var autoHelloRows int
	if err = s.DB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM auto_hello_state WHERE account_id=?) +
		(SELECT COUNT(*) FROM notifications WHERE account_id=? AND kind='auto_hello')`, second.ID, second.ID).Scan(&autoHelloRows); err != nil || autoHelloRows != 0 {
		t.Fatalf("auto hello data after disconnect = %d, %v; want 0", autoHelloRows, err)
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

func TestLegacyNotificationsGainBodyColumn(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "codex-helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE notifications (
		dedupe_key TEXT PRIMARY KEY, channel TEXT NOT NULL, kind TEXT NOT NULL,
		status TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT,
		scheduled_at INTEGER NOT NULL, sent_at INTEGER
	);
	INSERT INTO notifications(dedupe_key,channel,kind,status,scheduled_at)
		VALUES('pending-key','configured','after','pending',1);
	INSERT INTO notifications(dedupe_key,channel,kind,status,scheduled_at,sent_at)
		VALUES('sent-key','configured','after','sent',1,2);`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var count int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE dedupe_key='pending-key'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("legacy pending notification was not removed: count=%d err=%v", count, err)
	}
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE dedupe_key='sent-key'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("sent dedupe notification was not preserved: count=%d err=%v", count, err)
	}
	if _, err = s.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,scheduled_at,body) VALUES('key','configured','after','pending',1,'message')`); err != nil {
		t.Fatalf("body column was not added: %v", err)
	}
}

func TestLegacyNotificationsGainAccountIDAndDeleteWithAccount(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "codex-helper.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,display_name TEXT NOT NULL,email TEXT,plan_type TEXT,
		expected_kind TEXT NOT NULL DEFAULT 'any',connected INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
		INSERT INTO accounts VALUES(1,'legacy',NULL,NULL,'any',0,1,1);
		CREATE TABLE notifications (
		dedupe_key TEXT PRIMARY KEY,channel TEXT NOT NULL,kind TEXT NOT NULL,status TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,last_error TEXT,scheduled_at INTEGER NOT NULL,sent_at INTEGER);
		INSERT INTO notifications(dedupe_key,channel,kind,status,scheduled_at,sent_at)
		VALUES('1:codex:primary:10:before','configured','before','sent',1,2);
		INSERT INTO notifications(dedupe_key,channel,kind,status,scheduled_at,sent_at)
		VALUES('codex:primary:10:after','configured','after','sent',1,2);
		INSERT INTO notifications(dedupe_key,channel,kind,status,scheduled_at,sent_at)
		VALUES('unrecognized-key','configured','after','sent',1,2);`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"1:codex:primary:10:before", "codex:primary:10:after"} {
		var accountID int64
		if err = s.DB.QueryRow("SELECT account_id FROM notifications WHERE dedupe_key=?", key).Scan(&accountID); err != nil || accountID != 1 {
			t.Fatalf("%s account_id = %d, err = %v", key, accountID, err)
		}
	}
	var unknownAccountID sql.NullInt64
	if err = s.DB.QueryRow("SELECT account_id FROM notifications WHERE dedupe_key='unrecognized-key'").Scan(&unknownAccountID); err != nil || unknownAccountID.Valid {
		t.Fatalf("unrecognized account_id = %v, err = %v", unknownAccountID, err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatalf("second migration failed: %v", err)
	}
	defer s.DB.Close()
	if err = s.DeleteAccount(1); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE dedupe_key != 'unrecognized-key'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("notification count = %d, err = %v", count, err)
	}
}

func TestPromoteStagedNotificationsRecoversCurrentAndExpiresOld(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	now := int64(100_000)
	for key, scheduledAt := range map[string]int64{"current": now - 60, "old": now - int64((7 * time.Hour).Seconds())} {
		_, err = s.DB.Exec(`INSERT INTO notifications
			(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
			VALUES(?, 'configured', 'after', 'staged', ?, 'message', 1)`, key, scheduledAt)
		if err != nil {
			t.Fatal(err)
		}
	}
	promoted, err := s.PromoteStagedNotifications(1, now)
	if err != nil || promoted != 1 {
		t.Fatalf("promoted = %d, err = %v", promoted, err)
	}
	for key, want := range map[string]string{"current": "pending", "old": "expired"} {
		var status string
		if err = s.DB.QueryRow("SELECT status FROM notifications WHERE dedupe_key=?", key).Scan(&status); err != nil || status != want {
			t.Fatalf("%s status = %q, err = %v", key, status, err)
		}
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

func TestOpenScrubsTelegramTokensFromNotificationErrors(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,scheduled_at,body,last_error)
		VALUES('1:codex:primary:1:before','configured','before','failed',1,'message','Post "https://api.telegram.org/bot123:SECRET/sendMessage": timeout')`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var lastError string
	if err = s.DB.QueryRow("SELECT last_error FROM notifications WHERE dedupe_key='1:codex:primary:1:before'").Scan(&lastError); err != nil {
		t.Fatal(err)
	}
	if lastError != "Telegram 请求失败" {
		t.Fatalf("last_error = %q", lastError)
	}
}
