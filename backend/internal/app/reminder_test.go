package app

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

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
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DB.Close() })
	return &App{store: s, runtimes: map[int64]*accountRuntime{}}
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
