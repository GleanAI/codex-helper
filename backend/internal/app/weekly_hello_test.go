package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex-helper/internal/codex"
	"codex-helper/internal/store"
)

func weeklyHelloDashboard(now time.Time, fiveHourUsed float64) Dashboard {
	d := reminderDashboard(now.Unix(), fiveHourUsed, now.Add(5*time.Hour).Unix())
	d.Account.Connected = true
	d.Limits = append(d.Limits, LimitBucket{
		LimitID: "codex", WindowType: "secondary", WindowDurationMinutes: 10080,
		ResetsAt: now.Add(7 * 24 * time.Hour).Unix(),
	})
	return d
}

func TestWeeklyHelloResetEvidence(t *testing.T) {
	for _, name := range []string{"normal", "early", "disabled", "first snapshot", "reserve name", "reserve ID", "no advance", "clock only", "too late", "old early snapshot", "tiny drop", "missing duration", "previous duration missing", "missing reset", "zero before reset"} {
		t.Run(name, func(t *testing.T) {
			a := newReminderTestApp(t)
			g := defaults()
			g.AutoHello = name != "disabled"
			g.NotifyAfter = false
			if err := a.store.SetJSON("general", g); err != nil {
				t.Fatal(err)
			}
			now := time.Now().Truncate(time.Second)
			previous := weeklyHelloDashboard(now.Add(-time.Minute), 30)
			previous.Limits[1].UsedPercent = 90
			previous.Limits[1].ResetsAt = now.Unix()
			current := weeklyHelloDashboard(now, 30)
			want := 0
			switch name {
			case "normal", "zero before reset":
				want = 1
				if name == "zero before reset" {
					previous.Limits[1].UsedPercent = 0
				}
			case "early":
				previous.Limits[1].ResetsAt = now.Add(time.Hour).Unix()
				want = 1
			case "reserve name":
				reserve := " GPT-Reserve "
				current.Limits[1].LimitName = &reserve
			case "reserve ID":
				previous.Limits[1].LimitID = "gpt-reserve"
				current.Limits[1].LimitID = "gpt-reserve"
			case "no advance":
				previous.Limits[1].ResetsAt = current.Limits[1].ResetsAt
				current.Limits[1].UsedPercent = 90
			case "clock only":
				current.Limits[1].ResetsAt = previous.Limits[1].ResetsAt
			case "too late":
				previous.Limits[1].ResetsAt = now.Add(-6*time.Hour - time.Second).Unix()
			case "old early snapshot":
				previous.FetchedAt = now.Add(-6*time.Hour - time.Second).Unix()
				previous.Limits[1].ResetsAt = now.Add(time.Hour).Unix()
			case "tiny drop":
				previous.Limits[1].ResetsAt = now.Add(time.Hour).Unix()
				current.Limits[1].UsedPercent = 89.995
			case "missing duration":
				current.Limits[1].WindowDurationMinutes = 0
			case "previous duration missing":
				previous.Limits[1].WindowDurationMinutes = 0
			case "missing reset":
				previous.Limits[1].ResetsAt = now.Add(time.Hour).Unix()
				current.Limits[1].ResetsAt = 0
			}
			if name != "first snapshot" {
				if _, err := a.storeLimitSnapshots(previous); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.storeLimitSnapshots(current); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello_weekly'").Scan(&count); err != nil || count != want {
				t.Fatalf("weekly tasks=%d err=%v; want %d", count, err, want)
			}
		})
	}
}

func TestWeeklyHelloMergesIdleTaskAndRetries(t *testing.T) {
	for _, name := range []string{"success", "retry", "unknown outcome", "stale", "missing window", "disconnected", "exhausted"} {
		t.Run(name, func(t *testing.T) {
			a := newReminderTestApp(t)
			a.ctx = context.Background()
			g := defaults()
			g.AutoHello = true
			g.NotifyAfter = false
			if err := a.store.SetJSON("general", g); err != nil {
				t.Fatal(err)
			}
			now := time.Now().Truncate(time.Second)
			previous := weeklyHelloDashboard(now.Add(-time.Minute), 30)
			previous.Limits[1].UsedPercent = 90
			previous.Limits[1].ResetsAt = now.Unix()
			if _, err := a.storeLimitSnapshots(previous); err != nil {
				t.Fatal(err)
			}
			d := weeklyHelloDashboard(now, 0)
			if _, err := a.storeLimitSnapshots(d); err != nil {
				t.Fatal(err)
			}
			if _, err := a.store.PromoteStagedNotifications(1, now.Unix()); err != nil {
				t.Fatal(err)
			}
			client := &fakeCodexClient{}
			wantCalls, wantLogs, wantStatus := 1, 1, "sent"
			switch name {
			case "retry":
				client.callError = errors.New("upstream unavailable")
			case "unknown outcome":
				client.callError = &codex.TurnOutcomeUnknownError{Cause: errors.New("timeout")}
				wantStatus = "expired"
			case "stale":
				d.Stale = true
				wantCalls, wantLogs, wantStatus = 0, 0, "failed"
			case "missing window":
				d.Limits = d.Limits[:1]
				wantCalls, wantLogs, wantStatus = 0, 0, "failed"
			case "disconnected":
				d.Account.Connected = false
				wantCalls, wantLogs, wantStatus = 0, 0, "failed"
			case "exhausted":
				if _, err := a.store.DB.Exec("UPDATE notifications SET attempts=7 WHERE kind='auto_hello_weekly'"); err != nil {
					t.Fatal(err)
				}
				wantCalls, wantLogs, wantStatus = 0, 0, "expired"
			}
			a.runtimes[1] = &accountRuntime{client: client, dash: d}
			a.sendPendingReminders(now)
			a.sendPendingReminders(now.Add(time.Minute))
			if name == "retry" {
				_, _, _, calls := client.counts()
				if calls != 1 {
					t.Fatalf("sends before retry delay=%d; want 1", calls)
				}
				client.callError = nil
				a.sendPendingReminders(now.Add(5 * time.Minute))
				wantCalls, wantLogs = 2, 2
			}
			_, _, _, calls := client.counts()
			if calls != wantCalls {
				t.Fatalf("sends=%d; want %d", calls, wantCalls)
			}
			var count int
			if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE status=?", wantStatus).Scan(&count); err != nil || count != 2 {
				t.Fatalf("%s tasks=%d err=%v; want 2", wantStatus, count, err)
			}
			if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM auto_hello_logs").Scan(&count); err != nil || count != wantLogs {
				t.Fatalf("logs=%d err=%v; want %d", count, err, wantLogs)
			}
			if wantStatus == "sent" {
				if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM auto_hello_state WHERE completed_at IS NOT NULL").Scan(&count); err != nil || count != 1 {
					t.Fatalf("completed idle episodes=%d err=%v; want 1", count, err)
				}
				if _, err := a.storeLimitSnapshots(weeklyHelloDashboard(now.Add(time.Minute), 0)); err != nil {
					t.Fatal(err)
				}
				if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count); err != nil || count != 2 {
					t.Fatalf("tasks after idle resync=%d err=%v; want 2", count, err)
				}
			}
		})
	}
}

func TestWeeklyHelloSurvivesRestartAndIsolatesAccounts(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{store: s}
	g := defaults()
	g.AutoHello = true
	g.NotifyAfter = false
	if err = s.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateAccount("second")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	previous := weeklyHelloDashboard(now.Add(-time.Minute), 30)
	previous.Limits[1].ResetsAt = now.Unix()
	if _, err = a.storeLimitSnapshots(previous); err != nil {
		t.Fatal(err)
	}
	current := weeklyHelloDashboard(now, 30)
	if _, err = a.storeLimitSnapshots(current); err != nil {
		t.Fatal(err)
	}
	current.AccountID = second.ID
	if _, err = a.storeLimitSnapshots(current); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	a.store = s
	current.AccountID = 1
	if _, err = a.storeLimitSnapshots(current); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello_weekly' AND account_id=1").Scan(&count); err != nil || count != 1 {
		t.Fatalf("weekly tasks after restart=%d err=%v; want 1", count, err)
	}
	if err = s.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE account_id=?", second.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("second account tasks=%d err=%v; want 0", count, err)
	}
}

func TestDisableAutoHelloExpiresWeeklyAndIdleTasks(t *testing.T) {
	a := newReminderTestApp(t)
	g := defaults()
	g.AutoHello = true
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"auto_hello", "auto_hello_weekly"} {
		for _, status := range []string{"staged", "pending", "failed", "sent"} {
			if _, err := a.store.DB.Exec(`INSERT INTO notifications(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
				VALUES(?,'codex',?,?,?,'Hello',1)`, kind+status, kind, status, time.Now().Unix()); err != nil {
				t.Fatal(err)
			}
		}
	}
	g.AutoHello = false
	body, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	a.generalAPI(recorder, httptest.NewRequest(http.MethodPut, "/api/v1/settings/general", strings.NewReader(string(body))))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var expired, sent int
	if err = a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE status='expired'").Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if err = a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE status='sent'").Scan(&sent); err != nil {
		t.Fatal(err)
	}
	if expired != 6 || sent != 2 {
		t.Fatalf("expired=%d sent=%d; want 6 and 2", expired, sent)
	}
}

type weeklySnapshotClient struct {
	fakeCodexClient
	email    *string
	resetsAt int64
}

func (c *weeklySnapshotClient) Call(ctx context.Context, method string, params any, out any) error {
	var response any
	switch method {
	case "account/read":
		response = map[string]any{"account": map[string]any{"type": "chatgpt", "email": c.email, "planType": "plus"}}
	case "account/rateLimits/read":
		response = map[string]any{"rateLimits": map[string]any{
			"limitId":   "codex",
			"secondary": map[string]any{"usedPercent": 0, "windowDurationMins": 10080, "resetsAt": c.resetsAt},
		}}
	default:
		return c.fakeCodexClient.Call(ctx, method, params, out)
	}
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func TestWeeklyHelloDoesNotCompareDifferentIdentities(t *testing.T) {
	for _, name := range []string{"changed email", "unknown email"} {
		t.Run(name, func(t *testing.T) {
			a := newReminderTestApp(t)
			g := defaults()
			g.AutoHello = true
			g.NotifyBefore, g.NotifyAfter = false, false
			if err := a.store.SetJSON("general", g); err != nil {
				t.Fatal(err)
			}
			oldEmail, newEmail, plan := "old@example.com", "new@example.com", "plus"
			if err := a.store.UpdateAccount(1, &oldEmail, &plan, true); err != nil {
				t.Fatal(err)
			}
			now := time.Now().Truncate(time.Second)
			previous := weeklyHelloDashboard(now.Add(-time.Minute), 30)
			previous.Limits[1].UsedPercent = 90
			previous.Limits[1].ResetsAt = now.Unix()
			if _, err := a.storeLimitSnapshots(previous); err != nil {
				t.Fatal(err)
			}
			client := &weeklySnapshotClient{email: &newEmail, resetsAt: now.Add(7 * 24 * time.Hour).Unix()}
			if name == "unknown email" {
				client.email = nil
			}
			a.runtimes[1] = &accountRuntime{client: client}
			if err := a.syncAccount(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello_weekly'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("weekly tasks for new/unknown identity=%d err=%v; want 0", count, err)
			}
		})
	}
}

func TestWeeklyHelloQueuesAfterResetWithUsedFiveHourWindow(t *testing.T) {
	a := newReminderTestApp(t)
	g := defaults()
	g.AutoHello = true
	g.NotifyAfter = false
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	previous := weeklyHelloDashboard(now.Add(-time.Minute), 30)
	previous.Limits[1].UsedPercent = 90
	previous.Limits[1].ResetsAt = now.Unix()
	if _, err := a.storeLimitSnapshots(previous); err != nil {
		t.Fatal(err)
	}
	d := weeklyHelloDashboard(now, 30)
	if _, err := a.storeLimitSnapshots(d); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE kind='auto_hello_weekly' AND status='staged'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("weekly Hello tasks=%d err=%v; want 1 staged task", count, err)
	}
	// Publishing and promoting, as syncAccount does, must precede sending.
	client := &fakeCodexClient{}
	a.ctx = context.Background()
	a.runtimes[1] = &accountRuntime{client: client, dash: d}
	if _, err := a.store.PromoteStagedNotifications(1, now.Unix()); err != nil {
		t.Fatal(err)
	}
	a.sendPendingReminders(now)
	if _, err := a.storeLimitSnapshots(weeklyHelloDashboard(now.Add(time.Minute), 30)); err != nil {
		t.Fatal(err)
	}
	a.sendPendingReminders(now.Add(time.Minute))
	_, _, _, calls := client.counts()
	if calls != 1 {
		t.Fatalf("Hello sends=%d; want 1", calls)
	}
	if err := a.store.DB.QueryRow("SELECT COUNT(*) FROM auto_hello_logs WHERE status='success'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("Hello logs=%d err=%v; want 1", count, err)
	}
}
