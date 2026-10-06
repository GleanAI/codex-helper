package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"codex-helper/internal/codex"
	"codex-helper/internal/security"
	"codex-helper/internal/store"
)

type failingTelegramTransport struct{}

func (failingTelegramTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network unavailable")
}

func TestTelegramTransportErrorsDoNotExposeToken(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = failingTelegramTransport{}
	t.Cleanup(func() { http.DefaultTransport = original })
	token := "123456:SECRET_TOKEN"
	err := telegramCall(token, "sendMessage", map[string]any{}, new(any))
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "/bot") {
		t.Fatalf("unsafe Telegram error: %v", err)
	}
}

func TestSendSMTPStopsWhenServerDoesNotRespond(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	host, portRaw, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = sendSMTPWithTimeout(SMTPSettings{Host: host, Port: port, From: "from@example.com", To: "to@example.com"}, "subject", "text", "html", 100*time.Millisecond)
	if err == nil {
		t.Fatal("sendSMTPWithTimeout unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("SMTP timeout took %s", elapsed)
	}
	select {
	case conn := <-accepted:
		_ = conn.Close()
	default:
	}
}

func newReminderTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DB.Close() })
	vault, err := security.OpenVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &App{store: s, vault: vault, runtimes: map[int64]*accountRuntime{}}
}

func TestTGSendKeepsOrRemovesKeyboard(t *testing.T) {
	original := tgCall
	t.Cleanup(func() { tgCall = original })
	var calls []map[string]any
	tgCall = func(_ string, method string, params any, _ any) error {
		if method != "sendMessage" {
			t.Fatalf("method = %q", method)
		}
		body, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err = json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, decoded)
		return nil
	}
	if err := tgSend(TelegramSettings{Token: "token", ChatID: 1, MenuEnabled: true}, "on"); err != nil {
		t.Fatal(err)
	}
	if err := tgSend(TelegramSettings{Token: "token", ChatID: 1}, "off"); err != nil {
		t.Fatal(err)
	}
	keyboard := calls[0]["reply_markup"].(map[string]any)
	removed := calls[1]["reply_markup"].(map[string]any)
	if keyboard["keyboard"] == nil || removed["remove_keyboard"] != true {
		t.Fatalf("reply markup mismatch: %#v %#v", keyboard, removed)
	}
}

func TestTelegramDeleteClearsConfigurationAndOffset(t *testing.T) {
	a := newReminderTestApp(t)
	enc, err := a.vault.Encrypt("secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.Set("telegram_token", enc); err != nil {
		t.Fatal(err)
	}
	if err = a.store.SetJSON("telegram", TelegramSettings{ChatID: 0, Enabled: true, MenuEnabled: true, Configured: true}); err != nil {
		t.Fatal(err)
	}
	if err = a.store.Set("telegram_bind", `{"code":"123456"}`); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.DB.Exec("UPDATE telegram_updates SET offset=42 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	a.telegramAPI(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/settings/telegram", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	for _, key := range []string{"telegram", "telegram_token", "telegram_bind"} {
		if _, ok := a.store.Get(key); ok {
			t.Fatalf("setting %q still exists", key)
		}
	}
	var offset int64
	if err = a.store.DB.QueryRow("SELECT offset FROM telegram_updates WHERE id=1").Scan(&offset); err != nil || offset != 0 {
		t.Fatalf("offset = %d err = %v", offset, err)
	}
	if got := a.telegramSettings(); got.Configured || got.ChatID != 0 || got.Enabled || got.MenuEnabled {
		t.Fatalf("settings after delete = %#v", got)
	}
}

func TestTelegramDeleteWaitsForInFlightSaveAndRemainsFinal(t *testing.T) {
	a := newReminderTestApp(t)
	original := tgCall
	t.Cleanup(func() { tgCall = original })
	getMeStarted := make(chan struct{})
	releaseGetMe := make(chan struct{})
	tgCall = func(_ string, method string, _ any, out any) error {
		if method != "getMe" {
			t.Fatalf("method = %q", method)
		}
		close(getMeStarted)
		<-releaseGetMe
		body := []byte(`{"ok":true,"result":{"first_name":"Test","username":"test_bot"}}`)
		return json.Unmarshal(body, out)
	}

	putDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		body := bytes.NewBufferString(`{"token":"new-token","chatId":0,"enabled":true,"menuEnabled":false}`)
		recorder := httptest.NewRecorder()
		a.telegramAPI(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/settings/telegram", body))
		putDone <- recorder
	}()
	<-getMeStarted

	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		a.telegramAPI(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/settings/telegram", nil))
		deleteDone <- recorder
	}()
	select {
	case recorder := <-deleteDone:
		t.Fatalf("delete completed before save: status=%d body=%s", recorder.Code, recorder.Body.String())
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseGetMe)
	if recorder := <-putDone; recorder.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder := <-deleteDone; recorder.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if _, ok := a.store.Get("telegram_token"); ok {
		t.Fatal("Telegram token was recreated after delete")
	}
	if _, ok := a.store.Get("telegram"); ok {
		t.Fatal("Telegram settings were recreated after delete")
	}
}

func TestBoundTelegramIgnoresLegacyDisabledFlags(t *testing.T) {
	a := newReminderTestApp(t)
	enc, err := a.vault.Encrypt("secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.Set("telegram_token", enc); err != nil {
		t.Fatal(err)
	}
	if err = a.store.SetJSON("telegram", TelegramSettings{ChatID: 123, Enabled: false, MenuEnabled: false, Configured: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err = a.store.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,attempts,last_error,scheduled_at,sent_at,body,account_id)
		VALUES('disabled-test','configured','before','pending',0,'',?,NULL,'message',1)`, now.Unix()); err != nil {
		t.Fatal(err)
	}
	original := tgCall
	t.Cleanup(func() { tgCall = original })
	calls := 0
	tgCall = func(_ string, _ string, _ any, _ any) error {
		calls++
		return nil
	}
	a.sendPendingReminders(now)
	if calls != 1 {
		t.Fatalf("automatic Telegram calls = %d; want 1", calls)
	}
	recorder := httptest.NewRecorder()
	a.telegramTest(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/settings/telegram/test", nil))
	if recorder.Code != http.StatusOK || calls != 2 {
		t.Fatalf("manual test status = %d calls = %d body = %s", recorder.Code, calls, recorder.Body.String())
	}
	settings := a.telegramSettings()
	if !settings.Enabled || !settings.MenuEnabled {
		t.Fatalf("effective settings = %#v", settings)
	}
}

func TestLegacyBoundTelegramRestoresMenuOnce(t *testing.T) {
	a := newReminderTestApp(t)
	enc, err := a.vault.Encrypt("secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.Set("telegram_token", enc); err != nil {
		t.Fatal(err)
	}
	if err = a.store.SetJSON("telegram", TelegramSettings{ChatID: 123, Enabled: false, MenuEnabled: false, Configured: true}); err != nil {
		t.Fatal(err)
	}
	original := tgCall
	t.Cleanup(func() { tgCall = original })
	calls := 0
	var sent map[string]any
	tgCall = func(_ string, method string, params any, _ any) error {
		if method != "sendMessage" {
			t.Fatalf("method = %q", method)
		}
		calls++
		body, marshalErr := json.Marshal(params)
		if marshalErr != nil {
			return marshalErr
		}
		return json.Unmarshal(body, &sent)
	}

	a.syncLegacyTelegramMenu()
	a.syncLegacyTelegramMenu()
	if calls != 1 {
		t.Fatalf("menu restore calls = %d; want 1", calls)
	}
	replyMarkup, ok := sent["reply_markup"].(map[string]any)
	if !ok || replyMarkup["keyboard"] == nil {
		t.Fatalf("send params = %#v", sent)
	}
	var stored TelegramSettings
	if !a.store.GetJSON("telegram", &stored) || !stored.Enabled || !stored.MenuEnabled {
		t.Fatalf("stored settings = %#v", stored)
	}
}

func TestTelegramSaveCannotDisableFeaturesForBoundChat(t *testing.T) {
	a := newReminderTestApp(t)
	original := tgCall
	t.Cleanup(func() { tgCall = original })
	tgCall = func(_ string, method string, _ any, out any) error {
		if method == "sendMessage" {
			return nil
		}
		if method == "getMe" {
			return json.Unmarshal([]byte(`{"ok":true,"result":{"first_name":"Test","username":"test_bot"}}`), out)
		}
		t.Fatalf("method = %q", method)
		return nil
	}

	body := bytes.NewBufferString(`{"token":"token","chatId":123,"enabled":false,"menuEnabled":false}`)
	recorder := httptest.NewRecorder()
	a.telegramAPI(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/settings/telegram", body))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var response TelegramSettings
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Enabled || !response.MenuEnabled {
		t.Fatalf("response = %#v", response)
	}
	var stored TelegramSettings
	if !a.store.GetJSON("telegram", &stored) || !stored.Enabled || !stored.MenuEnabled {
		t.Fatalf("stored settings = %#v", stored)
	}
}

func TestTelegramBindEnablesFeaturesAndSendsMenu(t *testing.T) {
	a := newReminderTestApp(t)
	enc, err := a.vault.Encrypt("secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.Set("telegram_token", enc); err != nil {
		t.Fatal(err)
	}
	if err = a.store.SetJSON("telegram", TelegramSettings{Configured: true}); err != nil {
		t.Fatal(err)
	}
	if err = a.store.SetJSON("telegram_bind", map[string]any{"code": "123456", "expires": time.Now().Add(time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	original := tgCall
	t.Cleanup(func() { tgCall = original })
	var sent map[string]any
	tgCall = func(_ string, method string, params any, _ any) error {
		if method != "sendMessage" {
			t.Fatalf("method = %q", method)
		}
		body, marshalErr := json.Marshal(params)
		if marshalErr != nil {
			return marshalErr
		}
		return json.Unmarshal(body, &sent)
	}

	a.handleTG(a.telegramSecretForTest(t), 456, "/bind 123456")
	settings := a.telegramSettings()
	if settings.ChatID != 456 || !settings.Enabled || !settings.MenuEnabled {
		t.Fatalf("settings = %#v", settings)
	}
	replyMarkup, ok := sent["reply_markup"].(map[string]any)
	if !ok || replyMarkup["keyboard"] == nil {
		t.Fatalf("send params = %#v", sent)
	}
}

func (a *App) telegramSecretForTest(t *testing.T) TelegramSettings {
	t.Helper()
	settings, err := a.telegramSecret()
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

func reminderDashboard(fetchedAt int64, used float64, resetsAt int64) Dashboard {
	return Dashboard{
		AccountID:   1,
		DisplayName: "测试账号",
		FetchedAt:   fetchedAt,
		Limits: []LimitBucket{{
			LimitID:               "codex",
			WindowType:            "primary",
			UsedPercent:           used,
			WindowDurationMinutes: 300,
			ResetsAt:              resetsAt,
		}},
	}
}

func TestAutoHelloQualifiesOnlyForUnusedFiveHourWindow(t *testing.T) {
	now := time.Now().Unix()
	for _, test := range []struct {
		name       string
		used       float64
		resetDelta time.Duration
		want       bool
	}{
		{name: "exact", used: 0, resetDelta: 5 * time.Hour, want: true},
		{name: "within tolerance", used: 0, resetDelta: 5*time.Hour - 5*time.Minute, want: true},
		{name: "used", used: 0.01, resetDelta: 5 * time.Hour, want: false},
		{name: "shortened after small use", used: 0, resetDelta: 4*time.Hour + 54*time.Minute, want: false},
		{name: "outside tolerance", used: 0, resetDelta: 5*time.Hour + 6*time.Minute, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			limit := LimitBucket{WindowDurationMinutes: 300, UsedPercent: test.used, ResetsAt: now + int64(test.resetDelta.Seconds())}
			if got := qualifiesForAutoHello(limit, now); got != test.want {
				t.Fatalf("qualifiesForAutoHello() = %v; want %v", got, test.want)
			}
		})
	}
	if qualifiesForAutoHello(LimitBucket{WindowDurationMinutes: 10080, ResetsAt: now + int64((5 * time.Hour).Seconds())}, now) {
		t.Fatal("non-five-hour window qualified")
	}
}

func TestStoreLimitSnapshotsQueuesAutoHelloOncePerUnusedEpisode(t *testing.T) {
	a := newReminderTestApp(t)
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for i := 0; i < 120; i++ {
		fetchedAt := now + int64(i)*int64((5*time.Minute).Seconds())
		dashboard := reminderDashboard(fetchedAt, 0, fetchedAt+int64((5*time.Hour).Seconds()))
		if _, err := a.storeLimitSnapshots(dashboard); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var kind, status, body string
	if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("auto hello count = %d; want 1", count)
	}
	if err := a.store.DB.QueryRow("SELECT kind,status,body FROM notifications").Scan(&kind, &status, &body); err != nil {
		t.Fatal(err)
	}
	if kind != "auto_hello" || status != "staged" || body != "Hello" {
		t.Fatalf("notification = kind %q status %q body %q", kind, status, body)
	}
}

func TestAutoHelloRetrySchedule(t *testing.T) {
	scheduledAt := time.Now().Truncate(time.Second)
	for attempts, delay := range autoHelloRetryDelays {
		if due, exhausted := autoHelloRetryDue(scheduledAt.Unix(), attempts, scheduledAt.Add(delay-time.Second)); due || exhausted {
			t.Fatalf("attempt %d was due early: due=%v exhausted=%v", attempts, due, exhausted)
		}
		if due, exhausted := autoHelloRetryDue(scheduledAt.Unix(), attempts, scheduledAt.Add(delay)); !due || exhausted {
			t.Fatalf("attempt %d was not due on time: due=%v exhausted=%v", attempts, due, exhausted)
		}
	}
	if due, exhausted := autoHelloRetryDue(scheduledAt.Unix(), len(autoHelloRetryDelays), scheduledAt.Add(6*time.Hour)); due || !exhausted {
		t.Fatalf("exhausted schedule = due %v exhausted %v", due, exhausted)
	}
}

func TestStoreLimitSnapshotsQueuesAutoHelloForNextUnusedEpisode(t *testing.T) {
	a := newReminderTestApp(t)
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err := a.storeLimitSnapshots(reminderDashboard(now, 0, now+int64((5*time.Hour).Seconds()))); err != nil {
		t.Fatal(err)
	}
	if _, err := a.storeLimitSnapshots(reminderDashboard(now+300, 1, now+int64((5*time.Hour).Seconds()))); err != nil {
		t.Fatal(err)
	}
	if _, err := a.storeLimitSnapshots(reminderDashboard(now+600, 0, now+600+int64((5*time.Hour).Seconds()))); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("auto hello count = %d; want 2", count)
	}
}

func TestAutoHelloRearmsAfterConfirmedResetWithZeroPercent(t *testing.T) {
	for _, cleanup := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanup=%v", cleanup), func(t *testing.T) {
			a := newReminderTestApp(t)
			g := defaults()
			g.AutoHello = true
			if err := a.store.SetJSON("general", g); err != nil {
				t.Fatal(err)
			}
			now := time.Now().Unix()
			activeReset := now + 18_004
			initial := reminderDashboard(now, 0, now+18_000)
			if _, err := a.storeLimitSnapshots(initial); err != nil {
				t.Fatal(err)
			}
			client := &fakeCodexClient{}
			a.runtimes[1] = &accountRuntime{client: client, dash: initial}
			if _, err := a.store.PromoteStagedNotifications(1, now); err != nil {
				t.Fatal(err)
			}
			a.sendPendingReminders(time.Unix(now, 0))
			var completed int64
			if err := a.store.DB.QueryRow("SELECT completed_at FROM auto_hello_state WHERE account_id=1").Scan(&completed); err != nil || completed < now {
				t.Fatalf("successful turn did not persist completion: %d, %v", completed, err)
			}
			// The light turn starts a real countdown but usage remains rounded to zero.
			for _, fetched := range []int64{now + 3, now + 600, activeReset - 4} {
				if _, err := a.storeLimitSnapshots(reminderDashboard(fetched, 0, activeReset)); err != nil {
					t.Fatal(err)
				}
			}
			if cleanup {
				if _, err := a.store.DB.Exec("DELETE FROM notifications; DELETE FROM limit_snapshots"); err != nil {
					t.Fatal(err)
				}
			}
			// Missing metadata at the reset must not discard the durable countdown.
			unknown := reminderDashboard(activeReset+1, 0, 0)
			unknown.Limits[0].WindowDurationMinutes = 0
			if _, err := a.storeLimitSnapshots(unknown); err != nil {
				t.Fatal(err)
			}
			for i := int64(0); i < 3; i++ {
				fetched := activeReset + 300 + i*300
				if _, err := a.storeLimitSnapshots(reminderDashboard(fetched, 0, fetched+18_000)); err != nil {
					t.Fatal(err)
				}
			}
			var count int
			if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello' AND status='staged'").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("new-cycle staged tasks = %d; want 1", count)
			}
		})
	}
}

func TestAutoHelloDoesNotRearmWithoutConfirmedActivity(t *testing.T) {
	for _, outcome := range []string{"sent", "failed", "expired"} {
		t.Run(outcome, func(t *testing.T) {
			a := newReminderTestApp(t)
			g := defaults()
			g.AutoHello = true
			if err := a.store.SetJSON("general", g); err != nil {
				t.Fatal(err)
			}
			now := time.Now().Unix()
			if _, err := a.storeLimitSnapshots(reminderDashboard(now, 0, now+18_000)); err != nil {
				t.Fatal(err)
			}
			var completed *int64
			if outcome == "sent" {
				completed = &now
			}
			if err := a.store.RecordAutoHelloResult(fmt.Sprintf("1:codex:primary:%d:hello", now+18_000),
				1, "codex", "primary", now, outcome, 1, "", completed, now); err != nil {
				t.Fatal(err)
			}
			if outcome != "sent" {
				// A countdown cannot establish success for an unconfirmed turn.
				if _, err := a.storeLimitSnapshots(reminderDashboard(now+600, 0, now+18_000)); err != nil {
					t.Fatal(err)
				}
			}
			for i := int64(1); i <= 120; i++ {
				fetched := now + i*600
				if _, err := a.storeLimitSnapshots(reminderDashboard(fetched, 0, fetched+18_000)); err != nil {
					t.Fatal(err)
				}
			}
			var count int
			if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello'").Scan(&count); err != nil || count != 1 {
				t.Fatalf("unconfirmed activity requeued: count=%d err=%v", count, err)
			}
		})
	}
}

func TestAutoHelloRetainsResetWhileDisabled(t *testing.T) {
	a := newReminderTestApp(t)
	g := defaults()
	g.AutoHello = false
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB.Exec(`INSERT INTO auto_hello_state(account_id,limit_id,window_type,started_at,completed_at,active_resets_at)
		VALUES(1,'codex','primary',1000,1003,19004);
		INSERT INTO notifications(dedupe_key,channel,kind,status,scheduled_at,sent_at,body,account_id)
		VALUES('1:codex:primary:19000:hello','codex','auto_hello','sent',1000,1003,'Hello',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.storeLimitSnapshots(reminderDashboard(19304, 0, 37304)); err != nil {
		t.Fatal(err)
	}
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	if _, err := a.storeLimitSnapshots(reminderDashboard(19604, 0, 37604)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello' AND status='staged'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("reenabling lost reset boundary: count=%d err=%v", count, err)
	}
}

func TestAutoHelloUpgradeRearmsOnFirstSync(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Actual incident: the turn completed at 03:45 UTC, reset at 08:45:13 UTC,
	// and the next sync saw an idle window at 08:50 while percentage stayed zero.
	start := time.Date(2026, 10, 1, 3, 45, 9, 0, time.UTC).Unix()
	if _, err = s.DB.Exec(`DROP TABLE auto_hello_state;
		CREATE TABLE auto_hello_state(account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
		limit_id TEXT NOT NULL,window_type TEXT NOT NULL,started_at INTEGER NOT NULL,
		PRIMARY KEY(account_id,limit_id,window_type));
		INSERT INTO auto_hello_state VALUES(1,'codex','primary',?)`, start); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`INSERT INTO notifications(dedupe_key,channel,kind,status,scheduled_at,sent_at,body,account_id)
		VALUES(?,'codex','auto_hello','sent',?,?,'Hello',1)`, fmt.Sprintf("1:codex:primary:%d:hello", start+18_000), start, start+1); err != nil {
		t.Fatal(err)
	}
	for _, fetched := range []int64{start + 3, start + 600, start + 18_300} {
		reset := start + 18_004
		if fetched > reset {
			reset = fetched + 18_000
		}
		if _, err = s.DB.Exec(`INSERT INTO limit_snapshots(account_id,limit_id,window_type,used_percent,duration_mins,resets_at,fetched_at)
			VALUES(1,'codex','primary',0,300,?,?)`, reset, fetched); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	g := defaults()
	g.AutoHello = true
	if err = s.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	a := &App{store: s}
	if _, err = a.storeLimitSnapshots(reminderDashboard(start+40_000, 0, start+58_000)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello' AND status='staged'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("upgrade did not recover incident: count=%d err=%v", count, err)
	}
}

func TestStoreLimitSnapshotsDoesNotRequeueExpiredAutoHelloWhenStillUnused(t *testing.T) {
	a := newReminderTestApp(t)
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err := a.storeLimitSnapshots(reminderDashboard(now, 0, now+int64((5*time.Hour).Seconds()))); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB.Exec("UPDATE notifications SET status='expired' WHERE kind='auto_hello'"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.storeLimitSnapshots(reminderDashboard(now+300, 0, now+300+int64((5*time.Hour).Seconds()))); err != nil {
		t.Fatal(err)
	}
	var active int
	if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello' AND status='staged'").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("active auto hello count = %d; want 0", active)
	}
}

func TestStoreLimitSnapshotsDoesNotStartEpisodeWhenMetadataIsTemporarilyUnknown(t *testing.T) {
	a := newReminderTestApp(t)
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err := a.storeLimitSnapshots(reminderDashboard(now, 0, now+int64((5*time.Hour).Seconds()))); err != nil {
		t.Fatal(err)
	}
	unknown := reminderDashboard(now+300, 0, now+300+int64((5*time.Hour).Seconds()))
	unknown.Limits[0].WindowDurationMinutes = 0
	if _, err := a.storeLimitSnapshots(unknown); err != nil {
		t.Fatal(err)
	}
	if _, err := a.storeLimitSnapshots(reminderDashboard(now+600, 0, now+600+int64((5*time.Hour).Seconds()))); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("auto hello count = %d; want 1", count)
	}
}

func TestStoreLimitSnapshotsKeepsEpisodeMarkerAfterHistoryCleanup(t *testing.T) {
	a := newReminderTestApp(t)
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	if _, err := a.storeLimitSnapshots(reminderDashboard(now, 0, now+int64((5*time.Hour).Seconds()))); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB.Exec("DELETE FROM notifications; DELETE FROM limit_snapshots"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.storeLimitSnapshots(reminderDashboard(now+300, 0, now+300+int64((5*time.Hour).Seconds()))); err != nil {
		t.Fatal(err)
	}
	var notifications, markers int
	if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello'").Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM auto_hello_state").Scan(&markers); err != nil {
		t.Fatal(err)
	}
	if notifications != 0 || markers != 1 {
		t.Fatalf("notifications=%d markers=%d; want 0 and 1", notifications, markers)
	}
}

func TestSendPendingRemindersSendsAutoHelloThroughAccountRuntime(t *testing.T) {
	a := newReminderTestApp(t)
	a.ctx = context.Background()
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	client := &fakeCodexClient{}
	now := time.Now()
	a.runtimes[1] = &accountRuntime{client: client, dash: reminderDashboard(now.Unix(), 0, now.Add(5*time.Hour).Unix())}
	if _, err := a.store.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
		VALUES('1:codex:primary:123:hello','codex','auto_hello','pending',?,'Hello',1)`, now.Unix()); err != nil {
		t.Fatal(err)
	}
	a.sendPendingReminders(time.Now())
	var status string
	if err := a.store.DB.QueryRow("SELECT status FROM notifications WHERE kind='auto_hello'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "sent" {
		t.Fatalf("auto hello status = %q; want sent", status)
	}
	_, _, _, calls := client.counts()
	if calls == 0 {
		t.Fatal("auto hello did not call the account runtime")
	}
}

func TestDeletingAccountWaitsForAutoHelloSend(t *testing.T) {
	a := newReminderTestApp(t)
	a.dataDir = t.TempDir()
	a.ctx = context.Background()
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	client := &fakeCodexClient{
		sendStarted: make(chan struct{}, 1),
		sendRelease: make(chan struct{}),
	}
	now := time.Now()
	a.runtimes[1] = &accountRuntime{client: client, dash: reminderDashboard(now.Unix(), 0, now.Add(5*time.Hour).Unix())}
	if _, err := a.store.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
		VALUES('1:codex:primary:123:hello','codex','auto_hello','pending',?,'Hello',1)`, now.Unix()); err != nil {
		t.Fatal(err)
	}
	sendDone := make(chan struct{})
	go func() {
		a.processReminders()
		close(sendDone)
	}()
	select {
	case <-client.sendStarted:
	case <-time.After(time.Second):
		t.Fatal("auto hello send did not start")
	}
	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		a.accountAPI(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/1", nil), "accounts/1")
		deleteDone <- recorder
	}()
	select {
	case <-deleteDone:
		t.Fatal("account deletion completed while auto hello was sending")
	case <-time.After(50 * time.Millisecond):
	}
	close(client.sendRelease)
	select {
	case <-sendDone:
	case <-time.After(time.Second):
		t.Fatal("auto hello sender did not finish")
	}
	select {
	case recorder := <-deleteDone:
		if recorder.Code != http.StatusOK {
			t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("account deletion did not finish after auto hello")
	}
}

func TestSendPendingRemindersCollapsesDuplicateAutoHelloTasks(t *testing.T) {
	a := newReminderTestApp(t)
	a.ctx = context.Background()
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	client := &fakeCodexClient{}
	a.runtimes[1] = &accountRuntime{client: client, dash: reminderDashboard(now.Unix(), 0, now.Add(5*time.Hour).Unix())}
	for i, scheduledAt := range []int64{now.Add(-time.Minute).Unix(), now.Unix()} {
		key := fmt.Sprintf("1:codex:primary:%d:hello", 100+i)
		if _, err := a.store.DB.Exec(`INSERT INTO notifications
			(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
			VALUES(?,'codex','auto_hello','failed',?,'Hello',1)`, key, scheduledAt); err != nil {
			t.Fatal(err)
		}
	}
	a.sendPendingReminders(now)
	rows, err := a.store.DB.Query("SELECT status,COUNT(*) FROM notifications WHERE kind='auto_hello' GROUP BY status")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	statuses := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err = rows.Scan(&status, &count); err != nil {
			t.Fatal(err)
		}
		statuses[status] = count
	}
	if statuses["expired"] != 1 || statuses["sent"] != 1 {
		t.Fatalf("statuses = %#v; want one expired and one sent", statuses)
	}
	_, _, _, calls := client.counts()
	if calls != 1 {
		t.Fatalf("SendMessage calls = %d; want 1", calls)
	}
}

func TestSendPendingRemindersExpiresAutoHelloWhenWindowWasUsed(t *testing.T) {
	a := newReminderTestApp(t)
	a.ctx = context.Background()
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	client := &fakeCodexClient{}
	a.runtimes[1] = &accountRuntime{client: client, dash: reminderDashboard(now.Unix(), 1, now.Add(5*time.Hour).Unix())}
	if _, err := a.store.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
		VALUES('1:codex:primary:123:hello','codex','auto_hello','pending',?,'Hello',1)`, now.Unix()); err != nil {
		t.Fatal(err)
	}
	a.sendPendingReminders(now)
	var status string
	if err := a.store.DB.QueryRow("SELECT status FROM notifications WHERE kind='auto_hello'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "expired" {
		t.Fatalf("auto hello status = %q; want expired", status)
	}
	_, _, _, calls := client.counts()
	if calls != 0 {
		t.Fatalf("SendMessage calls = %d; want 0", calls)
	}
}

func TestSendPendingRemindersRetriesAutoHelloWhenWindowMetadataIsUnknown(t *testing.T) {
	a := newReminderTestApp(t)
	a.ctx = context.Background()
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	client := &fakeCodexClient{}
	dashboard := reminderDashboard(now.Unix(), 0, now.Add(5*time.Hour).Unix())
	dashboard.Limits[0].WindowDurationMinutes = 0
	a.runtimes[1] = &accountRuntime{client: client, dash: dashboard}
	if _, err := a.store.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
		VALUES('1:codex:primary:123:hello','codex','auto_hello','pending',?,'Hello',1)`, now.Unix()); err != nil {
		t.Fatal(err)
	}
	a.sendPendingReminders(now)
	var status string
	if err := a.store.DB.QueryRow("SELECT status FROM notifications WHERE kind='auto_hello'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("auto hello status = %q; want failed", status)
	}
	_, _, _, calls := client.counts()
	if calls != 0 {
		t.Fatalf("SendMessage calls = %d; want 0", calls)
	}
}

func TestSendPendingRemindersDoesNotRetryUnknownTurnOutcome(t *testing.T) {
	a := newReminderTestApp(t)
	a.ctx = context.Background()
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	client := &fakeCodexClient{callError: &codex.TurnOutcomeUnknownError{Cause: errors.New("interrupt was not confirmed")}}
	a.runtimes[1] = &accountRuntime{client: client, dash: reminderDashboard(now.Unix(), 0, now.Add(5*time.Hour).Unix())}
	if _, err := a.store.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
		VALUES('1:codex:primary:123:hello','codex','auto_hello','pending',?,'Hello',1)`, now.Unix()); err != nil {
		t.Fatal(err)
	}
	a.sendPendingReminders(now)
	var status string
	if err := a.store.DB.QueryRow("SELECT status FROM notifications WHERE kind='auto_hello'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "expired" {
		t.Fatalf("auto hello status = %q; want expired", status)
	}
}

func TestSendPendingRemindersBacksOffFailedAutoHello(t *testing.T) {
	a := newReminderTestApp(t)
	a.ctx = context.Background()
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	client := &fakeCodexClient{callError: errors.New("upstream unavailable")}
	a.runtimes[1] = &accountRuntime{client: client, dash: reminderDashboard(now.Unix(), 0, now.Add(5*time.Hour).Unix())}
	if _, err := a.store.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
		VALUES('1:codex:primary:123:hello','codex','auto_hello','pending',?,'Hello',1)`, now.Unix()); err != nil {
		t.Fatal(err)
	}
	a.sendPendingReminders(now)
	a.sendPendingReminders(now.Add(4 * time.Minute))
	_, _, _, calls := client.counts()
	if calls != 1 {
		t.Fatalf("SendMessage calls before retry delay = %d; want 1", calls)
	}
	a.sendPendingReminders(now.Add(5 * time.Minute))
	_, _, _, calls = client.counts()
	if calls != 2 {
		t.Fatalf("SendMessage calls after retry delay = %d; want 2", calls)
	}
	var status string
	var attempts int
	if err := a.store.DB.QueryRow("SELECT status,attempts FROM notifications WHERE kind='auto_hello'").Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || attempts != 2 {
		t.Fatalf("auto hello status=%q attempts=%d; want failed/2", status, attempts)
	}
}

func TestSendPendingRemindersExpiresExhaustedAutoHello(t *testing.T) {
	a := newReminderTestApp(t)
	a.ctx = context.Background()
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	client := &fakeCodexClient{}
	a.runtimes[1] = &accountRuntime{client: client, dash: reminderDashboard(now.Unix(), 0, now.Add(5*time.Hour).Unix())}
	if _, err := a.store.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,attempts,scheduled_at,body,account_id)
		VALUES('1:codex:primary:123:hello','codex','auto_hello','failed',7,?,'Hello',1)`, now.Add(-5*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	a.sendPendingReminders(now)
	var status string
	if err := a.store.DB.QueryRow("SELECT status FROM notifications WHERE kind='auto_hello'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "expired" {
		t.Fatalf("auto hello status = %q; want expired", status)
	}
	_, _, _, calls := client.counts()
	if calls != 0 {
		t.Fatalf("SendMessage calls = %d; want 0", calls)
	}
}

func notificationCount(t *testing.T, a *App) int {
	t.Helper()
	var count int
	if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestStoreLimitSnapshotsDetectsEarlyReset(t *testing.T) {
	a := newReminderTestApp(t)
	now := time.Now().Unix()
	if detected, err := a.storeLimitSnapshots(reminderDashboard(now, 42, now+3600)); err != nil || len(detected) != 0 {
		t.Fatalf("initial snapshot: detected=%v err=%v", detected, err)
	}
	if detected, err := a.storeLimitSnapshots(reminderDashboard(now+60, 3, now+7200)); err != nil || len(detected) == 0 {
		t.Fatalf("reset snapshot: detected=%v err=%v", detected, err)
	}
	var kind, body string
	if err := a.store.DB.QueryRow("SELECT kind,body FROM notifications").Scan(&kind, &body); err != nil {
		t.Fatal(err)
	}
	event, ok := decodeNotification(body)
	if kind != "detected_after" || !ok || !event.Confirmed || event.PreviousUsed != 42 || event.Used != 3 || event.Account != "测试账号" || len(event.Windows) != 1 {
		t.Fatalf("notification kind=%q body=%q", kind, body)
	}
}

func TestStoreLimitSnapshotsConfirmsScheduledResetFromAdvancedWindow(t *testing.T) {
	a := newReminderTestApp(t)
	oldReset := time.Now().Unix()
	if detected, err := a.storeLimitSnapshots(reminderDashboard(oldReset-60, 0, oldReset)); err != nil || len(detected) != 0 {
		t.Fatalf("initial snapshot: detected=%v err=%v", detected, err)
	}
	newReset := oldReset + int64((7 * 24 * time.Hour).Seconds())
	if detected, err := a.storeLimitSnapshots(reminderDashboard(oldReset+60, 0, newReset)); err != nil || len(detected) == 0 {
		t.Fatalf("advanced window: detected=%v err=%v", detected, err)
	}
	var kind, body string
	if err := a.store.DB.QueryRow("SELECT kind,body FROM notifications").Scan(&kind, &body); err != nil {
		t.Fatal(err)
	}
	event, ok := decodeNotification(body)
	if kind != "after" || !ok || !event.Confirmed || event.Remaining != 100 || event.ResetsAt != newReset {
		t.Fatalf("notification kind=%q event=%#v body=%q", kind, event, body)
	}
}

func TestStoreLimitSnapshotsDoesNotConfirmScheduledResetBeforeWindowAdvances(t *testing.T) {
	a := newReminderTestApp(t)
	oldReset := time.Now().Unix()
	_, _ = a.storeLimitSnapshots(reminderDashboard(oldReset-60, 100, oldReset))
	detected, err := a.storeLimitSnapshots(reminderDashboard(oldReset+60, 0, oldReset))
	if err != nil || len(detected) != 0 || notificationCount(t, a) != 0 {
		t.Fatalf("detected=%v notifications=%d err=%v", detected, notificationCount(t, a), err)
	}
}

func TestNotificationFormatting(t *testing.T) {
	if got := limitLabel(300); got != "5 小时额度" {
		t.Fatalf("300 minute label = %q", got)
	}
	if got := limitLabel(10080); got != "7 天额度" {
		t.Fatalf("7 day label = %q", got)
	}
	reset := time.Date(2026, 8, 20, 3, 51, 0, 0, time.UTC)
	now := reset.Add(-2*time.Hour - 15*time.Minute)
	event := notificationEvent{Version: 1, Kind: "before", Account: "A < B", DurationMins: 300, Remaining: 27.5, ResetsAt: reset.Unix()}
	plain, telegram, subject, email := renderNotification(event, "", "Asia/Shanghai", now)
	for _, value := range []string{plain, telegram, email} {
		if strings.Contains(value, "codex/primary") || strings.Contains(value, "2026-08-20T03:51:00Z") {
			t.Fatalf("internal label or RFC3339 leaked: %q", value)
		}
	}
	if subject != "Codex 即将重置" || !strings.Contains(telegram, "Codex 即将重置") || !strings.Contains(telegram, "8月20日 周四 11:51") || !strings.Contains(telegram, "还有 2 小时 15 分") {
		t.Fatalf("telegram = %q subject = %q", telegram, subject)
	}
	if !strings.Contains(telegram, "A &lt; B") || !strings.Contains(email, "multipart") && !strings.Contains(email, "Codex Helper") {
		t.Fatalf("output was not escaped or rendered: telegram=%q email=%q", telegram, email)
	}
}

func TestAutomaticTelegramReminderIncludesAllWindows(t *testing.T) {
	now := time.Date(2026, 9, 30, 4, 47, 0, 0, time.UTC)
	reserve := "gpt-reserve"
	preview := "Review <preview>"
	windows := []notificationWindow{
		{LimitName: &reserve, WindowDurationMinutes: 10080, UsedPercent: 0, ResetsAt: now.Add(7 * 24 * time.Hour).Unix()},
		{LimitName: &preview, WindowDurationMinutes: 0, UsedPercent: 50, ResetsAt: now.Add(30 * time.Minute).Unix()},
		{WindowDurationMinutes: 10080, UsedPercent: 11, ResetsAt: now.Add(4*24*time.Hour + time.Hour).Unix()},
		{WindowDurationMinutes: 300, UsedPercent: 0, ResetsAt: now.Add(5 * time.Hour).Unix()},
	}

	for _, test := range []struct {
		kind  string
		title string
	}{
		{kind: "before", title: "Codex 即将重置"},
		{kind: "after", title: "Codex 额度已重置"},
		{kind: "detected_after", title: "Codex 额度已重置"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			event := notificationEvent{Version: 1, Kind: test.kind, Confirmed: test.kind != "before", Account: "GPT <plus>", DurationMins: 300, Remaining: 100, ResetsAt: windows[3].ResetsAt, Windows: windows}
			plain, telegram, subject, email := renderNotification(event, "", "Asia/Shanghai", now)
			if subject != test.title || !strings.Contains(telegram, test.title) || !strings.Contains(telegram, "GPT &lt;plus&gt;") {
				t.Fatalf("unexpected reminder heading: subject=%q telegram=%q", subject, telegram)
			}
			labels := []string{"5 小时窗口", "7 天窗口", "gpt-reserve · 7 天窗口", "Review &lt;preview&gt; · 限额窗口"}
			previous := -1
			for _, label := range labels {
				index := strings.Index(telegram, label)
				if index <= previous {
					t.Fatalf("label %q was missing or out of order: %q", label, telegram)
				}
				previous = index
			}
			for _, expected := range []string{"剩余 100.0%", "剩余 89.0%", "还有 5 小时 0 分", "还有 7 天 0 小时"} {
				if !strings.Contains(telegram, expected) {
					t.Fatalf("Telegram reminder lost %q: %q", expected, telegram)
				}
			}
			if !strings.Contains(plain, "额度：5 小时额度") || !strings.Contains(email, "5 小时额度") || strings.Contains(plain, "gpt-reserve") || strings.Contains(email, "gpt-reserve") {
				t.Fatalf("non-Telegram bodies unexpectedly changed: plain=%q email=%q", plain, email)
			}
			if windows[0].LimitName != &reserve {
				t.Fatalf("input windows were reordered: %#v", windows)
			}
		})
	}
}

func TestTelegramUsageMatchesWebLimitLabelsAndOrder(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	codex := "Codex"
	reserve := " gpt-reserve "
	other := "Review <preview>"
	limits := []LimitBucket{
		{LimitName: &reserve, WindowDurationMinutes: 10080, UsedPercent: 0, ResetsAt: now.Add(7 * 24 * time.Hour).Unix()},
		{LimitName: &other, WindowDurationMinutes: 0, UsedPercent: 50, ResetsAt: now.Add(30 * time.Minute).Unix()},
		{LimitName: &codex, WindowDurationMinutes: 10080, UsedPercent: 9, ResetsAt: now.Add(4*24*time.Hour + 9*time.Hour).Unix()},
		{LimitName: &codex, WindowDurationMinutes: 300, UsedPercent: 2, ResetsAt: now.Add(time.Hour + 49*time.Minute).Unix()},
	}
	dashboards := []Dashboard{{DisplayName: "GPT <plus>", Limits: limits}}

	for name, message := range map[string]string{
		"current usage": renderTelegramCurrentUsage(dashboards, "Asia/Shanghai", now),
		"reset times":   renderTelegramResetTimes(dashboards, "Asia/Shanghai", now),
	} {
		labels := []string{
			"Codex · 5 小时窗口",
			"Codex · 7 天窗口",
			" gpt-reserve  · 7 天窗口",
			"Review &lt;preview&gt; · 限额窗口",
		}
		previous := -1
		for _, label := range labels {
			index := strings.Index(message, label)
			if index <= previous {
				t.Fatalf("%s label %q was missing or out of order: %q", name, label, message)
			}
			previous = index
		}
		if !strings.Contains(message, "GPT &lt;plus&gt;") {
			t.Fatalf("%s did not escape the account name: %q", name, message)
		}
	}

	usage := renderTelegramCurrentUsage(dashboards, "Asia/Shanghai", now)
	if !strings.Contains(usage, "剩余 98.0%") || !strings.Contains(usage, "还有 1 小时 49 分") {
		t.Fatalf("current usage lost percentage or relative reset time: %q", usage)
	}
	if limits[0].LimitName != &reserve {
		t.Fatalf("input limits were reordered: %#v", limits)
	}
}

func TestConfirmedResetNotificationUsesRemainingAndNextReset(t *testing.T) {
	now := time.Date(2026, 8, 20, 3, 51, 0, 0, time.UTC)
	nextReset := now.Add(7 * 24 * time.Hour)
	event := notificationEvent{Version: 1, Kind: "after", Confirmed: true, Account: "GPT Plus", DurationMins: 10080, Remaining: 100, Used: 0, ResetsAt: nextReset.Unix()}
	plain, telegram, subject, email := renderNotification(event, "", "Asia/Shanghai", now)
	for name, value := range map[string]string{"plain": plain, "telegram": telegram, "email": email} {
		if !strings.Contains(value, "当前额度剩余 100.0%") || !strings.Contains(value, "8月27日 周四 11:51") || !strings.Contains(value, "还有 7 天 0 小时") {
			t.Fatalf("%s did not contain confirmed reset values: %q", name, value)
		}
		if strings.Contains(value, "当前已用 100.0%") || strings.Contains(value, "刚刚重置") || strings.Contains(value, "8月20日 周四 11:51") {
			t.Fatalf("%s contained stale reset values: %q", name, value)
		}
	}
	if subject != "Codex 额度已重置" {
		t.Fatalf("subject = %q", subject)
	}
}

func TestSendPendingRemindersExpiresUnconfirmedAndLateNotifications(t *testing.T) {
	a := newReminderTestApp(t)
	now := time.Now()
	events := map[string]notificationEvent{
		"old-after":   {Version: 1, Kind: "after", Account: "旧记录", ResetsAt: now.Add(time.Hour).Unix()},
		"late-before": {Version: 1, Kind: "before", Account: "迟到提醒", ResetsAt: now.Add(-time.Minute).Unix()},
	}
	for key, event := range events {
		body, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.store.DB.Exec(`INSERT INTO notifications
			(dedupe_key,channel,kind,status,attempts,last_error,scheduled_at,sent_at,body,account_id)
			VALUES(?, 'configured', ?, 'pending', 0, '', ?, NULL, ?, 1)`, key, event.Kind, now.Unix(), string(body)); err != nil {
			t.Fatal(err)
		}
	}
	a.sendPendingReminders(now)
	rows, err := a.store.DB.Query("SELECT dedupe_key,status FROM notifications ORDER BY dedupe_key")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, status string
		if err = rows.Scan(&key, &status); err != nil {
			t.Fatal(err)
		}
		if status != "expired" {
			t.Fatalf("%s status = %q", key, status)
		}
	}
}

func TestProcessRemindersDoesNotScheduleAfterFromExpiredDashboard(t *testing.T) {
	a := newReminderTestApp(t)
	g := defaults()
	g.NotifyBefore = true
	g.NotifyAfter = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	a.runtimes[1] = &accountRuntime{dash: reminderDashboard(time.Now().Unix(), 100, time.Now().Add(-time.Minute).Unix())}
	a.processReminders()
	if count := notificationCount(t, a); count != 0 {
		t.Fatalf("notifications = %d", count)
	}
}

func TestProcessRemindersStoresAllWindowSnapshots(t *testing.T) {
	a := newReminderTestApp(t)
	g := defaults()
	g.NotifyBefore = true
	g.BeforeMinutes = 30
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	reserve := "gpt-reserve"
	dashboard := Dashboard{
		AccountID:   1,
		DisplayName: "测试账号",
		FetchedAt:   now.Unix(),
		Limits: []LimitBucket{
			{LimitID: "codex", WindowType: "primary", WindowDurationMinutes: 300, UsedPercent: 25, ResetsAt: now.Add(15 * time.Minute).Unix()},
			{LimitID: "gpt-reserve", LimitName: &reserve, WindowType: "secondary", WindowDurationMinutes: 10080, UsedPercent: 10, ResetsAt: now.Add(7 * 24 * time.Hour).Unix()},
		},
	}
	a.runtimes[1] = &accountRuntime{dash: dashboard}
	a.processReminders()

	var body string
	if err := a.store.DB.QueryRow("SELECT body FROM notifications").Scan(&body); err != nil {
		t.Fatal(err)
	}
	event, ok := decodeNotification(body)
	if !ok || len(event.Windows) != 2 || event.Windows[0].UsedPercent != 25 || event.Windows[1].LimitName == nil || *event.Windows[1].LimitName != reserve {
		t.Fatalf("notification did not store the complete window snapshot: %#v", event)
	}
}

func TestConfirmedNotificationRemainsStagedUntilDashboardPublication(t *testing.T) {
	a := newReminderTestApp(t)
	oldReset := time.Now().Unix()
	_, _ = a.storeLimitSnapshots(reminderDashboard(oldReset-60, 100, oldReset))
	keys, err := a.storeLimitSnapshots(reminderDashboard(oldReset+60, 0, oldReset+3600))
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	a.sendPendingReminders(time.Unix(oldReset+60, 0))
	var status string
	if err = a.store.DB.QueryRow("SELECT status FROM notifications WHERE dedupe_key=?", keys[0]).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "staged" {
		t.Fatalf("status = %q", status)
	}
}

func TestProcessRemindersReleasesScheduleLockBeforeNetworkSend(t *testing.T) {
	a := newReminderTestApp(t)
	enc, err := a.vault.Encrypt("secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.Set("telegram_token", enc); err != nil {
		t.Fatal(err)
	}
	if err = a.store.SetJSON("telegram", TelegramSettings{ChatID: 123, Configured: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err = a.store.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,attempts,last_error,scheduled_at,sent_at,body,account_id)
		VALUES('slow-send','configured','before','pending',0,'',?,NULL,'legacy message',1)`, now.Unix()); err != nil {
		t.Fatal(err)
	}
	original := tgCall
	t.Cleanup(func() { tgCall = original })
	sendStarted := make(chan struct{})
	releaseSend := make(chan struct{})
	tgCall = func(_ string, _ string, _ any, _ any) error {
		close(sendStarted)
		<-releaseSend
		return nil
	}
	done := make(chan struct{})
	go func() {
		a.processReminders()
		close(done)
	}()
	<-sendStarted
	locked := make(chan struct{})
	go func() {
		a.reminderMu.Lock()
		a.reminderMu.Unlock()
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(time.Second):
		close(releaseSend)
		<-done
		t.Fatal("schedule lock remained held during network send")
	}
	close(releaseSend)
	<-done
}

func TestDeleteWaitsForInFlightReminderAndPreventsLaterSend(t *testing.T) {
	a := newReminderTestApp(t)
	account, err := a.store.CreateAccount("delete during send")
	if err != nil {
		t.Fatal(err)
	}
	a.runtimes[account.ID] = &accountRuntime{client: &fakeCodexClient{}}
	enc, err := a.vault.Encrypt("secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.Set("telegram_token", enc); err != nil {
		t.Fatal(err)
	}
	if err = a.store.SetJSON("telegram", TelegramSettings{ChatID: 123, Configured: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err = a.store.DB.Exec(`INSERT INTO notifications
		(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
		VALUES(?, 'configured', 'before', 'pending', ?, 'message', ?)`, fmt.Sprintf("%d:pending", account.ID), now.Unix(), account.ID); err != nil {
		t.Fatal(err)
	}
	original := tgCall
	t.Cleanup(func() { tgCall = original })
	sendStarted := make(chan struct{})
	releaseSend := make(chan struct{})
	calls := 0
	tgCall = func(_ string, _ string, _ any, _ any) error {
		calls++
		close(sendStarted)
		<-releaseSend
		return nil
	}
	sendDone := make(chan struct{})
	go func() {
		a.processReminders()
		close(sendDone)
	}()
	<-sendStarted
	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		a.accountAPI(recorder, httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d", account.ID), nil), fmt.Sprintf("accounts/%d", account.ID))
		deleteDone <- recorder
	}()
	select {
	case <-deleteDone:
		t.Fatal("delete completed while reminder send was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseSend)
	<-sendDone
	if recorder := <-deleteDone; recorder.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	a.processReminders()
	if calls != 1 {
		t.Fatalf("Telegram calls = %d; want 1", calls)
	}
}

func TestLegacyNotificationFormatting(t *testing.T) {
	legacy := "旧消息 <保留>"
	event, ok := decodeNotification(legacy)
	if ok {
		t.Fatal("legacy notification decoded as structured")
	}
	plain, telegram, subject, email := renderNotification(event, legacy, "UTC", time.Now())
	if plain != legacy || telegram != "旧消息 &lt;保留&gt;" || subject != "Codex 额度重置提醒" || !strings.Contains(email, "旧消息 &lt;保留&gt;") {
		t.Fatalf("legacy render mismatch: %q %q %q", plain, telegram, subject)
	}
}

func TestStoreLimitSnapshotsUsesScheduledAfterDedupeKey(t *testing.T) {
	a := newReminderTestApp(t)
	now := time.Now().Unix()
	resetAt := now + 30
	_, _ = a.storeLimitSnapshots(reminderDashboard(now, 70, resetAt))
	detected, err := a.storeLimitSnapshots(reminderDashboard(now+60, 0, now+3600))
	if err != nil || len(detected) == 0 {
		t.Fatalf("detected=%v err=%v", detected, err)
	}
	var key, kind string
	if err = a.store.DB.QueryRow("SELECT dedupe_key,kind FROM notifications").Scan(&key, &kind); err != nil {
		t.Fatal(err)
	}
	exact := "1:codex:primary:" + strconv.FormatInt(resetAt, 10) + ":after"
	if key != exact || kind != "after" {
		t.Fatalf("key=%q kind=%q, want %q after", key, kind, exact)
	}
}

func TestStoreLimitSnapshotsIgnoresNonResetChanges(t *testing.T) {
	tests := []struct {
		name        string
		oldUsed     float64
		newUsed     float64
		age         time.Duration
		notifyAfter bool
	}{
		{name: "increase", oldUsed: 10, newUsed: 20, age: time.Minute, notifyAfter: true},
		{name: "tolerance", oldUsed: 10, newUsed: 9.995, age: time.Minute, notifyAfter: true},
		{name: "old snapshot", oldUsed: 50, newUsed: 0, age: 7 * time.Hour, notifyAfter: true},
		{name: "disabled", oldUsed: 50, newUsed: 0, age: time.Minute, notifyAfter: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newReminderTestApp(t)
			g := defaults()
			g.NotifyAfter = tt.notifyAfter
			if err := a.store.SetJSON("general", g); err != nil {
				t.Fatal(err)
			}
			now := time.Now().Unix()
			_, _ = a.storeLimitSnapshots(reminderDashboard(now, tt.oldUsed, now+3600))
			detected, err := a.storeLimitSnapshots(reminderDashboard(now+int64(tt.age.Seconds()), tt.newUsed, now+7200))
			if err != nil || len(detected) != 0 || notificationCount(t, a) != 0 {
				t.Fatalf("detected=%v notifications=%d err=%v", detected, notificationCount(t, a), err)
			}
		})
	}
}
