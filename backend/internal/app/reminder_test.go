package app

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"codex-helper/internal/security"
	"codex-helper/internal/store"
)

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

func TestDisabledTelegramSkipsAutomaticReminderButAllowsManualTest(t *testing.T) {
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
		(dedupe_key,channel,kind,status,attempts,last_error,scheduled_at,sent_at,body)
		VALUES('disabled-test','configured','before','pending',0,'',?,NULL,'message')`, now.Unix()); err != nil {
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
	if calls != 0 {
		t.Fatalf("automatic Telegram calls = %d; want 0", calls)
	}
	recorder := httptest.NewRecorder()
	a.telegramTest(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/settings/telegram/test", nil))
	if recorder.Code != http.StatusOK || calls != 1 {
		t.Fatalf("manual test status = %d calls = %d body = %s", recorder.Code, calls, recorder.Body.String())
	}
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
	if detected, err := a.storeLimitSnapshots(reminderDashboard(now, 42, now+3600)); err != nil || detected {
		t.Fatalf("initial snapshot: detected=%v err=%v", detected, err)
	}
	if detected, err := a.storeLimitSnapshots(reminderDashboard(now+60, 3, now+7200)); err != nil || !detected {
		t.Fatalf("reset snapshot: detected=%v err=%v", detected, err)
	}
	var kind, body string
	if err := a.store.DB.QueryRow("SELECT kind,body FROM notifications").Scan(&kind, &body); err != nil {
		t.Fatal(err)
	}
	event, ok := decodeNotification(body)
	if kind != "detected_after" || !ok || event.PreviousUsed != 42 || event.Used != 3 || event.Account != "测试账号" {
		t.Fatalf("notification kind=%q body=%q", kind, body)
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
	if err != nil || !detected {
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
			if err != nil || detected || notificationCount(t, a) != 0 {
				t.Fatalf("detected=%v notifications=%d err=%v", detected, notificationCount(t, a), err)
			}
		})
	}
}
