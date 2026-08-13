package app

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"codex-helper/internal/store"
)

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
	if kind != "detected_after" || !strings.Contains(body, "42.0% → 3.0%") || !strings.Contains(body, "测试账号") {
		t.Fatalf("notification kind=%q body=%q", kind, body)
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
