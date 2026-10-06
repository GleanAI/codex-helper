package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }

type DailyUsage struct {
	Date        string
	TotalTokens int64
}

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
CREATE TABLE IF NOT EXISTS notifications (dedupe_key TEXT PRIMARY KEY, channel TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT, scheduled_at INTEGER NOT NULL, sent_at INTEGER, body TEXT NOT NULL DEFAULT '', account_id INTEGER REFERENCES accounts(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS telegram_updates (id INTEGER PRIMARY KEY CHECK(id=1), offset INTEGER NOT NULL DEFAULT 0);
INSERT OR IGNORE INTO telegram_updates(id,offset) VALUES(1,0);
`)
	if err != nil {
		return err
	}
	if err = s.ensureNotificationBody(); err != nil {
		return err
	}
	if _, err = s.DB.Exec(`UPDATE notifications SET last_error='Telegram 请求失败'
		WHERE instr(COALESCE(last_error,''),'api.telegram.org/bot') > 0`); err != nil {
		return err
	}
	if err = s.migrateAccounts(); err != nil {
		return err
	}
	if err = s.migrateNotificationAccounts(); err != nil {
		return err
	}
	return s.migrateAutoHelloState()
}

func (s *Store) migrateAutoHelloState() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS auto_hello_state (
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		limit_id TEXT NOT NULL,
		window_type TEXT NOT NULL,
		started_at INTEGER NOT NULL,
		PRIMARY KEY(account_id,limit_id,window_type)
	)`); err != nil {
		return err
	}
	var logsExist int
	if err = tx.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='auto_hello_logs'").Scan(&logsExist); err != nil {
		return err
	}
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS auto_hello_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		status TEXT NOT NULL,
		attempted_at INTEGER NOT NULL
	)`); err != nil {
		return err
	}
	if _, err = tx.Exec("CREATE INDEX IF NOT EXISTS idx_auto_hello_logs_time ON auto_hello_logs(attempted_at,id)"); err != nil {
		return err
	}
	if logsExist == 0 {
		if _, err = tx.Exec(`INSERT INTO auto_hello_logs(account_id,status,attempted_at)
			SELECT account_id,'success',sent_at FROM notifications
			WHERE kind='auto_hello' AND status='sent' AND sent_at IS NOT NULL
			ORDER BY sent_at DESC,dedupe_key DESC LIMIT 5`); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM auto_hello_logs WHERE id NOT IN (
		SELECT id FROM auto_hello_logs ORDER BY attempted_at DESC,id DESC LIMIT 5
	)`); err != nil {
		return err
	}
	upgrading := false
	for _, column := range []string{"completed_at", "active_resets_at"} {
		var exists int
		if err = tx.QueryRow("SELECT COUNT(*) FROM pragma_table_info('auto_hello_state') WHERE name=?", column).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			upgrading = true
			if _, err = tx.Exec("ALTER TABLE auto_hello_state ADD COLUMN " + column + " INTEGER"); err != nil {
				return err
			}
		}
	}
	// Seed legacy episodes only on upgrade. Replaying old tasks on every boot
	// could resurrect a marker from a window which has already finished.
	if !upgrading {
		return tx.Commit()
	}
	type legacyTask struct {
		accountID  int64
		limitID    string
		windowType string
		scheduled  int64
		completed  sql.NullInt64
	}
	rows, err := tx.Query(`SELECT account_id,dedupe_key,scheduled_at,
		CASE WHEN status='sent' THEN sent_at END FROM notifications
		WHERE kind='auto_hello' AND account_id IS NOT NULL ORDER BY scheduled_at DESC,dedupe_key DESC`)
	if err != nil {
		return err
	}
	tasks := []legacyTask{}
	for rows.Next() {
		var accountID, scheduled int64
		var key string
		var completed sql.NullInt64
		if err = rows.Scan(&accountID, &key, &scheduled, &completed); err != nil {
			rows.Close()
			return err
		}
		limitID, windowType, ok := autoHelloNotificationIdentity(key, accountID)
		if ok {
			tasks = append(tasks, legacyTask{accountID: accountID, limitID: limitID, windowType: windowType, scheduled: scheduled, completed: completed})
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, task := range tasks {
		var lastUse sql.NullInt64
		if err = tx.QueryRow(`SELECT MAX(fetched_at) FROM limit_snapshots
			WHERE account_id=? AND limit_id=? AND window_type=? AND used_percent>0`,
			task.accountID, task.limitID, task.windowType).Scan(&lastUse); err != nil {
			return err
		}
		if lastUse.Valid && lastUse.Int64 >= task.scheduled {
			continue
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO auto_hello_state(account_id,limit_id,window_type,started_at)
			VALUES(?,?,?,?)`, task.accountID, task.limitID, task.windowType, task.scheduled); err != nil {
			return err
		}
		if task.completed.Valid {
			if _, err = tx.Exec(`UPDATE auto_hello_state SET completed_at=COALESCE(completed_at,?)
				WHERE account_id=? AND limit_id=? AND window_type=? AND started_at<=?`,
				task.completed.Int64, task.accountID, task.limitID, task.windowType, task.scheduled); err != nil {
				return err
			}
		}
	}
	// A countdown below the unused-window tolerance proves the window started,
	// even when a light turn leaves the reported percentage at zero.
	if _, err = tx.Exec(`UPDATE auto_hello_state SET active_resets_at=(
		SELECT MAX(resets_at) FROM limit_snapshots l
		WHERE l.account_id=auto_hello_state.account_id AND l.limit_id=auto_hello_state.limit_id
		AND l.window_type=auto_hello_state.window_type AND l.duration_mins=300
		AND l.fetched_at>=auto_hello_state.completed_at
		AND l.resets_at-l.fetched_at>0 AND l.resets_at-l.fetched_at<17700
	) WHERE completed_at IS NOT NULL`); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordAutoHelloResult commits delivery and episode completion together.
func (s *Store) RecordAutoHelloResult(key string, accountID int64, limitID, windowType string, scheduledAt int64, status string, attempts int, lastError string, completedAt *int64, attemptedAtValues ...int64) error {
	attemptedAt := int64(0)
	if len(attemptedAtValues) > 0 {
		attemptedAt = attemptedAtValues[0]
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE notifications SET status=?,attempts=?,last_error=?,sent_at=? WHERE dedupe_key=?`,
		status, attempts, lastError, completedAt, key); err != nil {
		return err
	}
	if status == "sent" && completedAt != nil {
		if _, err = tx.Exec(`UPDATE auto_hello_state SET completed_at=?
			WHERE account_id=? AND limit_id=? AND window_type=? AND started_at<=?`,
			*completedAt, accountID, limitID, windowType, scheduledAt); err != nil {
			return err
		}
	}
	if attemptedAt > 0 {
		logStatus := "failure"
		if status == "sent" {
			logStatus = "success"
		}
		if _, err = tx.Exec(`INSERT INTO auto_hello_logs(account_id,status,attempted_at) VALUES(?,?,?)`, accountID, logStatus, attemptedAt); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM auto_hello_logs WHERE id NOT IN (
			SELECT id FROM auto_hello_logs ORDER BY attempted_at DESC,id DESC LIMIT 5
		)`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type AutoHelloLog struct {
	Status      string
	AttemptedAt int64
}

func (s *Store) LatestAutoHelloLog() (AutoHelloLog, bool, error) {
	var log AutoHelloLog
	err := s.DB.QueryRow(`SELECT status,attempted_at FROM auto_hello_logs
		ORDER BY attempted_at DESC,id DESC LIMIT 1`).Scan(&log.Status, &log.AttemptedAt)
	if err == sql.ErrNoRows {
		return AutoHelloLog{}, false, nil
	}
	if err != nil {
		return AutoHelloLog{}, false, err
	}
	return log, true, nil
}

func autoHelloNotificationIdentity(key string, accountID int64) (string, string, bool) {
	base, ok := strings.CutSuffix(key, ":hello")
	if !ok {
		return "", "", false
	}
	resetSeparator := strings.LastIndexByte(base, ':')
	if resetSeparator < 0 {
		return "", "", false
	}
	if _, err := strconv.ParseInt(base[resetSeparator+1:], 10, 64); err != nil {
		return "", "", false
	}
	base = base[:resetSeparator]
	accountPrefix := strconv.FormatInt(accountID, 10) + ":"
	if !strings.HasPrefix(base, accountPrefix) {
		return "", "", false
	}
	limitAndWindow := strings.TrimPrefix(base, accountPrefix)
	windowSeparator := strings.LastIndexByte(limitAndWindow, ':')
	if windowSeparator <= 0 || windowSeparator == len(limitAndWindow)-1 {
		return "", "", false
	}
	return limitAndWindow[:windowSeparator], limitAndWindow[windowSeparator+1:], true
}

func (s *Store) migrateNotificationAccounts() error {
	rows, err := s.DB.Query("PRAGMA table_info(notifications)")
	if err != nil {
		return err
	}
	hasAccountID := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		hasAccountID = hasAccountID || name == "account_id"
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if !hasAccountID {
		if _, err = s.DB.Exec("ALTER TABLE notifications ADD COLUMN account_id INTEGER REFERENCES accounts(id) ON DELETE CASCADE"); err != nil {
			return err
		}
	}
	if _, err = s.DB.Exec("CREATE INDEX IF NOT EXISTS idx_notifications_account_status_time ON notifications(account_id,status,scheduled_at)"); err != nil {
		return err
	}

	accounts := map[int64]bool{}
	accountRows, err := s.DB.Query("SELECT id FROM accounts")
	if err != nil {
		return err
	}
	for accountRows.Next() {
		var id int64
		if err = accountRows.Scan(&id); err != nil {
			accountRows.Close()
			return err
		}
		accounts[id] = true
	}
	if err = accountRows.Close(); err != nil {
		return err
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	notificationRows, err := tx.Query("SELECT dedupe_key,status FROM notifications WHERE account_id IS NULL")
	if err != nil {
		return err
	}
	type legacyNotification struct{ key, status string }
	legacy := []legacyNotification{}
	for notificationRows.Next() {
		var notification legacyNotification
		if err = notificationRows.Scan(&notification.key, &notification.status); err != nil {
			notificationRows.Close()
			return err
		}
		legacy = append(legacy, notification)
	}
	if err = notificationRows.Close(); err != nil {
		return err
	}
	for _, notification := range legacy {
		accountID, identified := notificationAccountID(notification.key, accounts)
		if identified {
			if _, err = tx.Exec("UPDATE notifications SET account_id=? WHERE dedupe_key=?", accountID, notification.key); err != nil {
				return err
			}
		} else if notification.status != "sent" {
			if _, err = tx.Exec("DELETE FROM notifications WHERE dedupe_key=?", notification.key); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func notificationAccountID(key string, accounts map[int64]bool) (int64, bool) {
	prefix, _, found := strings.Cut(key, ":")
	if found {
		if accountID, err := strconv.ParseInt(prefix, 10, 64); err == nil && accounts[accountID] {
			return accountID, true
		}
	}

	// Before multi-account support, reminder keys had no account prefix:
	// <limitID>:<windowType>:<resetsAt>:<before|after>. All data from that
	// schema belongs to the default account created as account 1.
	parts := strings.Split(key, ":")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || !accounts[1] {
		return 0, false
	}
	if _, err := strconv.ParseInt(parts[2], 10, 64); err != nil {
		return 0, false
	}
	if parts[3] != "before" && parts[3] != "after" {
		return 0, false
	}
	return 1, true
}

func (s *Store) ensureNotificationBody() error {
	rows, err := s.DB.Query("PRAGMA table_info(notifications)")
	if err != nil {
		return err
	}
	hasBody := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if err = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		hasBody = hasBody || name == "body"
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if hasBody {
		return nil
	}
	// Legacy unsent rows have no recoverable message body. Remove them so the
	// reminder scheduler can recreate every still-applicable retry from the
	// current dashboard instead of having INSERT OR IGNORE collide with an
	// empty-body row. Sent rows remain as dedupe records.
	_, err = s.DB.Exec(`ALTER TABLE notifications ADD COLUMN body TEXT NOT NULL DEFAULT '';
		DELETE FROM notifications WHERE status != 'sent';`)
	return err
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
	result, err := s.DB.Exec("UPDATE accounts SET email=?,plan_type=?,connected=?,updated_at=? WHERE id=?", email, plan, connected, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) DeleteAccount(id int64) error {
	result, err := s.DB.Exec("DELETE FROM accounts WHERE id=?", id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) PromoteStagedNotifications(accountID, now int64) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE notifications SET status='expired',last_error=''
		WHERE account_id=? AND status='staged' AND scheduled_at<?`, accountID, now-int64((6*time.Hour).Seconds())); err != nil {
		return 0, err
	}
	result, err := tx.Exec(`UPDATE notifications SET status='pending'
		WHERE account_id=? AND status='staged' AND scheduled_at>=?`, accountID, now-int64((6*time.Hour).Seconds()))
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return affected, nil
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
func (s *Store) SaveTelegram(settingsJSON, encryptedToken string, resetOffset bool) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for key, value := range map[string]string{"telegram": settingsJSON, "telegram_token": encryptedToken} {
		if _, err = tx.Exec("INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at", key, value, now); err != nil {
			return err
		}
	}
	if resetOffset {
		if _, err = tx.Exec("UPDATE telegram_updates SET offset=0 WHERE id=1"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) DeleteTelegram() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM settings WHERE key IN ('telegram','telegram_token','telegram_bind')"); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE telegram_updates SET offset=0 WHERE id=1"); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) GetJSON(key string, v any) bool {
	raw, ok := s.Get(key)
	return ok && json.Unmarshal([]byte(raw), v) == nil
}
func (s *Store) Initialized() bool { _, ok := s.Get("initialized"); return ok }

func (s *Store) UpsertDailyUsage(accountID int64, usage []DailyUsage, fetchedAt int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, point := range usage {
		if _, err = tx.Exec(`INSERT INTO daily_usage(account_id,date,total_tokens,fetched_at)
			VALUES(?,?,?,?) ON CONFLICT(account_id,date) DO UPDATE SET
			total_tokens=excluded.total_tokens,fetched_at=excluded.fetched_at`,
			accountID, point.Date, point.TotalTokens, fetchedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DailyUsage(accountID int64, fromDate, throughDate string) ([]DailyUsage, error) {
	rows, err := s.DB.Query(`SELECT date,total_tokens FROM daily_usage
		WHERE account_id=? AND date>=? AND date<=? ORDER BY date`, accountID, fromDate, throughDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	usage := []DailyUsage{}
	for rows.Next() {
		var point DailyUsage
		if err = rows.Scan(&point.Date, &point.TotalTokens); err != nil {
			return nil, err
		}
		usage = append(usage, point)
	}
	return usage, rows.Err()
}

func (s *Store) DeleteDailyUsage(accountID int64) error {
	_, err := s.DB.Exec("DELETE FROM daily_usage WHERE account_id=?", accountID)
	return err
}

func (s *Store) DeleteAutoHelloData(accountID int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM auto_hello_state WHERE account_id=?", accountID); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM auto_hello_logs WHERE account_id=?", accountID); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM notifications WHERE account_id=? AND kind='auto_hello'", accountID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DisconnectAccount(accountID int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM daily_usage WHERE account_id=?", accountID); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM auto_hello_state WHERE account_id=?", accountID); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM auto_hello_logs WHERE account_id=?", accountID); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM notifications WHERE account_id=? AND kind='auto_hello'", accountID); err != nil {
		return err
	}
	result, err := tx.Exec("UPDATE accounts SET email=NULL,plan_type=NULL,connected=0,updated_at=? WHERE id=?", time.Now().Unix(), accountID)
	if err != nil {
		return err
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
		return affectedErr
	} else if affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

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
