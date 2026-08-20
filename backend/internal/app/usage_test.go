package app

import (
	"testing"
	"time"

	"codex-helper/internal/store"
)

func TestUsageIdentityChanged(t *testing.T) {
	first := "User@Example.com"
	same := " user@example.com "
	other := "other@example.com"
	tests := []struct {
		name              string
		previous, current *string
		want              bool
	}{
		{name: "same email", previous: &first, current: &same, want: false},
		{name: "different email", previous: &first, current: &other, want: true},
		{name: "previous identity unknown", previous: nil, current: &other, want: true},
		{name: "logged out", previous: &first, current: nil, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := usageIdentityChanged(tt.previous, tt.current); got != tt.want {
				t.Fatalf("usageIdentityChanged() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestDailyUsageHistoryFillsMissingDaysThroughToday(t *testing.T) {
	a := newReminderTestApp(t)
	g := defaults()
	g.Timezone = "America/New_York"
	g.RetentionDays = 30
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	if err := a.store.UpsertDailyUsage(1, []store.DailyUsage{
		{Date: "2026-08-16", TotalTokens: 100},
		{Date: "2026-08-18", TotalTokens: 300},
	}, 1); err != nil {
		t.Fatal(err)
	}

	// 02:00 UTC is still August 18 in the configured timezone.
	now := time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC)
	usage, err := a.dailyUsageHistory(1, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []UsagePoint{
		{Date: "2026-08-16", TotalTokens: 100},
		{Date: "2026-08-17", TotalTokens: 0},
		{Date: "2026-08-18", TotalTokens: 300},
	}
	if len(usage) != len(want) {
		t.Fatalf("usage = %#v; want %#v", usage, want)
	}
	for i := range want {
		if usage[i].Date != want[i].Date || usage[i].TotalTokens != want[i].TotalTokens {
			t.Fatalf("usage[%d] = %#v; want %#v", i, usage[i], want[i])
		}
	}
}

func TestDailyUsageHistoryHonorsRetentionAndSkipsInvalidRows(t *testing.T) {
	a := newReminderTestApp(t)
	g := defaults()
	g.Timezone = "UTC"
	g.RetentionDays = 30
	if err := a.store.SetJSON("general", g); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB.Exec(`INSERT INTO daily_usage(account_id,date,total_tokens,fetched_at) VALUES
		(1,'2026-07-01',10,1),(1,'2026-08-01-bad',20,1),(1,'2026-08-18',30,1)`); err != nil {
		t.Fatal(err)
	}

	usage, err := a.dailyUsageHistory(1, time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 2 || usage[0].Date != "2026-08-18" || usage[0].TotalTokens != 30 || usage[1].Date != "2026-08-19" || usage[1].TotalTokens != 0 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestDailyUsageHistoryDoesNotInventRangeWithoutOfficialBuckets(t *testing.T) {
	a := newReminderTestApp(t)
	usage, err := a.dailyUsageHistory(1, time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 0 {
		t.Fatalf("usage = %#v; want empty", usage)
	}
}
