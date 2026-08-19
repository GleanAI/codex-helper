package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codex-helper/internal/store"
)

func TestPublicOverviewIsAnonymousGroupedAndSanitized(t *testing.T) {
	a := newReminderTestApp(t)
	if err := a.store.Set("initialized", "true"); err != nil {
		t.Fatal(err)
	}
	second, err := a.store.CreateAccount("Team workspace", "team")
	if err != nil {
		t.Fatal(err)
	}
	emailOne, emailTwo := " Test@Example.com ", "test@example.com"
	_, err = a.store.DB.Exec(`UPDATE accounts SET email=?,plan_type='plus',connected=1 WHERE id=1`, emailOne)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.DB.Exec(`UPDATE accounts SET email=?,plan_type='business',connected=1 WHERE id=?`, emailTwo, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.runtimes[1] = &accountRuntime{dash: Dashboard{
		FetchedAt: 100,
		Limits: []LimitBucket{{
			LimitID: "secret-bucket", LimitName: stringPointer("Codex"), WindowDurationMinutes: 300, UsedPercent: 25, ResetsAt: 200,
		}},
		MonthlyCreditLimit: &MonthlyCreditLimit{RemainingPercent: 68, ResetsAt: 300, Used: "8000", Limit: "25000"},
		Summary:            UsageSummary{LifetimeTokens: int64Pointer(999)},
		Usage:              []UsagePoint{{Date: "2026-08-19", TotalTokens: 999}},
	}}
	a.runtimes[second.ID] = &accountRuntime{dash: Dashboard{FetchedAt: 110, Stale: true, LastError: "upstream secret detail"}}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/public/overview", nil)
	a.api(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var body publicOverviewResponse
	if err = json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Cards) != 1 || body.Cards[0].Title != "T**t@E*****e.com" || len(body.Cards[0].Connections) != 2 {
		t.Fatalf("unexpected grouping: %#v", body)
	}
	if body.Cards[0].Connections[0].Status != "healthy" || body.Cards[0].Connections[1].Status != "failed" {
		t.Fatalf("unexpected statuses: %#v", body.Cards[0].Connections)
	}
	if len(body.Cards[0].Connections[0].Limits) != 1 || body.Cards[0].Connections[0].MonthlyCreditLimit == nil {
		t.Fatalf("usage limits missing: %#v", body.Cards[0].Connections[0])
	}
	raw := recorder.Body.String()
	for _, forbidden := range []string{"Test@Example.com", "test@example.com", "upstream secret detail", "secret-bucket", "lifetimeTokens", "totalTokens", `"id"`, `"accountId"`, "8000", "25000"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("public response contains %q: %s", forbidden, raw)
		}
	}
}

func TestPublicOverviewKeepsAccountsWithoutEmailSeparate(t *testing.T) {
	a := newReminderTestApp(t)
	if err := a.store.Set("initialized", "true"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.DB.Exec(`UPDATE accounts SET display_name='同名账号',email='   ' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	second, err := a.store.CreateAccount("同名账号", "any")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.DB.Exec(`UPDATE accounts SET email='' WHERE id=?`, second.ID); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	a.api(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/public/overview", nil))
	var body publicOverviewResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Cards) != 2 || body.Cards[0].EmailIdentified || body.Cards[1].EmailIdentified {
		t.Fatalf("cards = %#v", body.Cards)
	}
	if body.Cards[0].Title != "同名账号" || body.Cards[1].Title != "同名账号" {
		t.Fatalf("blank emails should use display names: %#v", body.Cards)
	}
}

func TestPublicOverviewStatusPrecedence(t *testing.T) {
	a := newReminderTestApp(t)
	base := store.Account{ID: 1, Connected: true, ActualKind: "personal"}
	tests := []struct {
		name      string
		account   store.Account
		dashboard Dashboard
		want      string
	}{
		{name: "offline", account: store.Account{ID: 1}, want: "offline"},
		{name: "failed", account: base, dashboard: Dashboard{LastError: "failed"}, want: "failed"},
		{name: "loading", account: base, want: "loading"},
		{name: "pending", account: store.Account{ID: 1, Connected: true, ActualKind: "unknown"}, dashboard: Dashboard{FetchedAt: 1}, want: "pending"},
		{name: "stale", account: base, dashboard: Dashboard{FetchedAt: 1, Stale: true}, want: "stale"},
		{name: "healthy", account: base, dashboard: Dashboard{FetchedAt: 1}, want: "healthy"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a.runtimes[1] = &accountRuntime{dash: test.dashboard}
			if got := a.publicConnection(test.account).Status; got != test.want {
				t.Fatalf("status = %q; want %q", got, test.want)
			}
		})
	}
}

func TestPublicOverviewRejectsInvalidStateAndMethod(t *testing.T) {
	a := newReminderTestApp(t)
	recorder := httptest.NewRecorder()
	a.api(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/public/overview", nil))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "请先初始化") {
		t.Fatalf("uninitialized status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := a.store.Set("initialized", "true"); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	a.api(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/public/overview", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	a.api(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("accounts status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func stringPointer(value string) *string { return &value }
func int64Pointer(value int64) *int64    { return &value }
