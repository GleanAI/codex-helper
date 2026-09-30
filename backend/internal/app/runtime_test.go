package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"codex-helper/internal/security"
)

func TestSystemStatusRejectsNonGETMethods(t *testing.T) {
	a := newReminderTestApp(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/system/status", nil)
	a.api(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestSystemStatusReturnsBuildVersion(t *testing.T) {
	a := newReminderTestApp(t)
	originalVersion := Version
	Version = "1.2.3-test"
	t.Cleanup(func() { Version = originalVersion })
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	a.api(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Version != "1.2.3-test" {
		t.Fatalf("version = %q; want %q", body.Version, "1.2.3-test")
	}
}

func TestDecodeRejectsTrailingAndOversizedJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "second object", body: `{"value":1}{"value":2}`},
		{name: "trailing garbage", body: `{"value":1}garbage`},
		{name: "oversized trailing whitespace", body: `{"value":1}` + strings.Repeat(" ", 1<<20)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			recorder := httptest.NewRecorder()
			var value struct {
				Value int `json:"value"`
			}
			if err := decode(recorder, request, &value); err == nil {
				t.Fatal("decode unexpectedly accepted invalid body")
			}
		})
	}
}

func TestDashboardSerializesNilListsAsEmptyArrays(t *testing.T) {
	a := newReminderTestApp(t)
	a.runtimes[1] = &accountRuntime{}
	_, err := a.store.DB.Exec("INSERT INTO sessions(token_hash,expires_at,created_at) VALUES(?,?,?)", security.HashToken("test-session"), time.Now().Add(time.Hour).Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard?accountId=1", nil)
	request.AddCookie(&http.Cookie{Name: "session", Value: "test-session"})
	a.api(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Limits []LimitBucket `json:"limits"`
		Usage  []UsagePoint  `json:"usage"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Limits == nil || body.Usage == nil {
		t.Fatalf("nil lists in response: %s", recorder.Body.String())
	}
}

func TestFlattenLimitDecodesAppServerWindowDurations(t *testing.T) {
	var response struct {
		RateLimits *rawLimit `json:"rateLimits"`
	}
	payload := `{
		"rateLimits": {
			"limitId": "codex",
			"primary": {"usedPercent": 25, "windowDurationMins": 300, "resetsAt": 1786665600},
			"secondary": {"usedPercent": 60, "windowDurationMins": 10080, "resetsAt": 1787270400}
		}
	}`
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		t.Fatal(err)
	}
	if response.RateLimits == nil {
		t.Fatal("rateLimits is nil")
	}
	limits := flattenLimit(*response.RateLimits)
	if len(limits) != 2 {
		t.Fatalf("len(limits) = %d; want 2", len(limits))
	}
	if limits[0].WindowType != "primary" || limits[0].WindowDurationMinutes != 300 || limits[0].ResetsAt != 1786665600 {
		t.Fatalf("primary = %#v", limits[0])
	}
	if limits[1].WindowType != "secondary" || limits[1].WindowDurationMinutes != 10080 || limits[1].ResetsAt != 1787270400 {
		t.Fatalf("secondary = %#v", limits[1])
	}
}

func TestFlattenLimitHandlesNullWindowMetadata(t *testing.T) {
	var limit rawLimit
	payload := `{
		"limitId": "codex",
		"primary": {"usedPercent": 25, "windowDurationMins": null, "resetsAt": null}
	}`
	if err := json.Unmarshal([]byte(payload), &limit); err != nil {
		t.Fatal(err)
	}
	limits := flattenLimit(limit)
	if len(limits) != 1 {
		t.Fatalf("len(limits) = %d; want 1", len(limits))
	}
	if limits[0].WindowDurationMinutes != 0 || limits[0].ResetsAt != 0 {
		t.Fatalf("limit = %#v; want zero optional metadata", limits[0])
	}
}

func TestMonthlyCreditLimitDecodesFromRateLimits(t *testing.T) {
	var response struct {
		RateLimits *rawLimit `json:"rateLimits"`
	}
	payload := `{
		"rateLimits": {
			"limitId": "codex",
			"secondary": {"usedPercent": 7, "windowDurationMins": 10080, "resetsAt": 1787716800},
			"individualLimit": {"remainingPercent": 68, "resetsAt": 1788235200, "used": "8000", "limit": "25000"}
		}
	}`
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		t.Fatal(err)
	}
	monthly := monthlyCreditLimitFrom(response.RateLimits, nil)
	if monthly == nil {
		t.Fatal("monthly credit limit is nil")
	}
	if monthly.RemainingPercent != 68 || monthly.ResetsAt != 1788235200 || monthly.Used != "8000" || monthly.Limit != "25000" {
		t.Fatalf("monthly credit limit = %#v", monthly)
	}
}

func TestMonthlyCreditLimitFallsBackToCanonicalBucket(t *testing.T) {
	by := map[string]rawLimit{
		"codex_other": {},
		"codex": {
			IndividualLimit: &rawMonthlyCreditLimit{RemainingPercent: 100, ResetsAt: 1788235200, Used: "0", Limit: "500"},
		},
	}
	monthly := monthlyCreditLimitFrom(&rawLimit{}, by)
	if monthly == nil || monthly.RemainingPercent != 100 || monthly.Limit != "500" {
		t.Fatalf("monthly credit limit = %#v", monthly)
	}
	if monthlyCreditLimitFrom(&rawLimit{}, map[string]rawLimit{"codex": {}}) != nil {
		t.Fatal("missing individualLimit should remain nil")
	}
}

func TestAccountRenameUpdatesDashboardImmediately(t *testing.T) {
	a := newReminderTestApp(t)
	a.runtimes[1] = &accountRuntime{dash: Dashboard{
		AccountID:   1,
		DisplayName: "旧名称",
		Limits:      []LimitBucket{{LimitID: "primary"}},
		Usage:       []UsagePoint{{Date: "2026-08-14", TotalTokens: 42}},
	}}
	_, err := a.store.DB.Exec("INSERT INTO sessions(token_hash,expires_at,created_at) VALUES(?,?,?)", security.HashToken("test-session"), time.Now().Add(time.Hour).Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}

	rename := httptest.NewRequest(http.MethodPut, "/api/v1/accounts/1", bytes.NewBufferString(`{"displayName":"  新名称  "}`))
	rename.AddCookie(&http.Cookie{Name: "session", Value: "test-session"})
	rename.Header.Set("X-Requested-With", "codex-helper")
	renameRecorder := httptest.NewRecorder()
	a.api(renameRecorder, rename)
	if renameRecorder.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", renameRecorder.Code, renameRecorder.Body.String())
	}

	dashboard := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard?accountId=1", nil)
	dashboard.AddCookie(&http.Cookie{Name: "session", Value: "test-session"})
	dashboardRecorder := httptest.NewRecorder()
	a.api(dashboardRecorder, dashboard)
	if dashboardRecorder.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d, body = %s", dashboardRecorder.Code, dashboardRecorder.Body.String())
	}
	var body Dashboard
	if err = json.Unmarshal(dashboardRecorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.DisplayName != "新名称" {
		t.Fatalf("displayName = %q; want %q", body.DisplayName, "新名称")
	}
	if len(body.Limits) != 1 || body.Limits[0].LimitID != "primary" || len(body.Usage) != 1 || body.Usage[0].TotalTokens != 42 {
		t.Fatalf("dashboard data changed: %#v", body)
	}
}

func TestInvalidAccountRenameKeepsDashboardName(t *testing.T) {
	a := newReminderTestApp(t)
	a.runtimes[1] = &accountRuntime{dash: Dashboard{AccountID: 1, DisplayName: "旧名称"}}
	_, err := a.store.DB.Exec("INSERT INTO sessions(token_hash,expires_at,created_at) VALUES(?,?,?)", security.HashToken("test-session"), time.Now().Add(time.Hour).Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}

	rename := httptest.NewRequest(http.MethodPut, "/api/v1/accounts/1", bytes.NewBufferString(`{"displayName":"   "}`))
	rename.AddCookie(&http.Cookie{Name: "session", Value: "test-session"})
	rename.Header.Set("X-Requested-With", "codex-helper")
	recorder := httptest.NewRecorder()
	a.api(recorder, rename)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	a.runtimes[1].syncing.Lock()
	name := a.runtimes[1].dash.DisplayName
	a.runtimes[1].syncing.Unlock()
	if name != "旧名称" {
		t.Fatalf("displayName = %q; want %q", name, "旧名称")
	}
	accounts, err := a.store.Accounts()
	if err != nil {
		t.Fatal(err)
	}
	if accounts[0].DisplayName != "默认账号" {
		t.Fatalf("stored displayName = %q; want %q", accounts[0].DisplayName, "默认账号")
	}
}

type fakeCodexClient struct {
	mu          sync.Mutex
	connected   bool
	starts      int
	initializes int
	closes      int
	calls       int
	initErrors  []error
	callError   error
	initStarted chan struct{}
	initRelease chan struct{}
	sendStarted chan struct{}
	sendRelease chan struct{}
}

type blockingSyncClient struct {
	fakeCodexClient
	accountReadStarted chan struct{}
	releaseAccountRead chan struct{}
}

func (f *blockingSyncClient) Call(ctx context.Context, method string, params any, out any) error {
	if method == "account/read" {
		close(f.accountReadStarted)
		select {
		case <-f.releaseAccountRead:
		case <-ctx.Done():
			return ctx.Err()
		}
		return json.Unmarshal([]byte(`{"account":{"type":"chatgpt","email":"old@example.com","planType":"plus"}}`), out)
	}
	return f.fakeCodexClient.Call(ctx, method, params, out)
}

func (f *fakeCodexClient) Start(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	f.connected = true
	return nil
}

func (f *fakeCodexClient) Initialize(context.Context) error {
	f.mu.Lock()
	f.initializes++
	var err error
	if len(f.initErrors) > 0 {
		err, f.initErrors = f.initErrors[0], f.initErrors[1:]
	}
	started, release := f.initStarted, f.initRelease
	f.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	return err
}

func (f *fakeCodexClient) Call(_ context.Context, method string, _ any, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.callError != nil {
		return f.callError
	}
	if method == "account/login/start" {
		return json.Unmarshal([]byte(`{"type":"chatgptDeviceCode","loginId":"login-1","verificationUrl":"https://example.test/device","userCode":"ABCD-EFGH"}`), out)
	}
	return nil
}

func (f *fakeCodexClient) SendMessage(ctx context.Context, _ string) error {
	f.mu.Lock()
	f.calls++
	started, release := f.sendStarted, f.sendRelease
	f.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.callError
}

func TestSyncFailureMarksExistingDashboardStale(t *testing.T) {
	a := newReminderTestApp(t)
	client := &fakeCodexClient{connected: true, callError: errors.New("account read failed")}
	a.runtimes[1] = &accountRuntime{client: client, ready: true, dash: Dashboard{FetchedAt: 123}}
	if err := a.syncAccount(context.Background(), 1); err == nil {
		t.Fatal("sync unexpectedly succeeded")
	}
	a.runtimes[1].syncing.Lock()
	dashboard := a.runtimes[1].dash
	a.runtimes[1].syncing.Unlock()
	if !dashboard.Stale || dashboard.LastError != "account read failed" || dashboard.FetchedAt != 123 {
		t.Fatalf("dashboard = %#v", dashboard)
	}
}

func TestLogoutWaitsForSyncAndKeepsAccountDisconnected(t *testing.T) {
	a := newReminderTestApp(t)
	client := &blockingSyncClient{fakeCodexClient: fakeCodexClient{connected: true}, accountReadStarted: make(chan struct{}), releaseAccountRead: make(chan struct{})}
	a.runtimes[1] = &accountRuntime{client: client, ready: true, dash: Dashboard{Account: AccountView{Connected: true}}}
	syncDone := make(chan error, 1)
	go func() { syncDone <- a.syncAccount(context.Background(), 1) }()
	<-client.accountReadStarted
	logoutDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		a.accountAPI(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/accounts/1/logout", nil), "accounts/1/logout")
		logoutDone <- recorder
	}()
	select {
	case <-logoutDone:
		t.Fatal("logout completed before in-flight sync")
	case <-time.After(50 * time.Millisecond):
	}
	close(client.releaseAccountRead)
	if err := <-syncDone; err != nil {
		t.Fatal(err)
	}
	if recorder := <-logoutDone; recorder.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var email *string
	var connected bool
	if err := a.store.DB.QueryRow("SELECT email,connected FROM accounts WHERE id=1").Scan(&email, &connected); err != nil {
		t.Fatal(err)
	}
	if email != nil || connected || a.runtimes[1].dash.Account.Connected {
		t.Fatalf("account remained connected: email=%v db=%v dashboard=%v", email, connected, a.runtimes[1].dash.Account.Connected)
	}
}

func TestDeleteAccountCascadesQueuedNotifications(t *testing.T) {
	a := newReminderTestApp(t)
	account, err := a.store.CreateAccount("delete me")
	if err != nil {
		t.Fatal(err)
	}
	a.runtimes[account.ID] = &accountRuntime{client: &fakeCodexClient{}}
	for _, status := range []string{"pending", "failed", "staged"} {
		_, err = a.store.DB.Exec(`INSERT INTO notifications
			(dedupe_key,channel,kind,status,scheduled_at,body,account_id)
			VALUES(?, 'configured', 'before', ?, 1, 'message', ?)`, fmt.Sprintf("%d:key:%s", account.ID, status), status, account.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	recorder := httptest.NewRecorder()
	a.accountAPI(recorder, httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d", account.ID), nil), fmt.Sprintf("accounts/%d", account.ID))
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var count int
	if err = a.store.DB.QueryRow("SELECT COUNT(*) FROM notifications WHERE account_id=?", account.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("notification count = %d, err = %v", count, err)
	}
}

func TestDeleteAccountRestoresRuntimeWhenDatabaseDeleteFails(t *testing.T) {
	a := newReminderTestApp(t)
	account, err := a.store.CreateAccount("keep me")
	if err != nil {
		t.Fatal(err)
	}
	original := &accountRuntime{client: &fakeCodexClient{}}
	a.runtimes[account.ID] = original
	if _, err = a.store.DB.Exec(`CREATE TABLE account_delete_blocker (
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT);
		INSERT INTO account_delete_blocker(account_id) VALUES(?)`, account.ID); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	a.accountAPI(recorder, httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d", account.ID), nil), fmt.Sprintf("accounts/%d", account.ID))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var count int
	if err = a.store.DB.QueryRow("SELECT COUNT(*) FROM accounts WHERE id=?", account.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("account count = %d, err = %v", count, err)
	}
	restored := a.runtime(account.ID)
	if restored == nil || restored == original {
		t.Fatalf("runtime was not restored: original=%p restored=%p", original, restored)
	}
	restored.stateMu.RLock()
	stopped := restored.stopped
	restored.stateMu.RUnlock()
	if stopped {
		t.Fatal("restored runtime is stopped")
	}
}

func (f *fakeCodexClient) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	f.connected = false
	return nil
}

func (f *fakeCodexClient) Connected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *fakeCodexClient) counts() (starts, initializes, closes, calls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.initializes, f.closes, f.calls
}

func TestEnsureReadySerializesColdStart(t *testing.T) {
	client := &fakeCodexClient{}
	rt := &accountRuntime{client: client}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- rt.ensureReady(context.Background())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	starts, initializes, _, _ := client.counts()
	if starts != 1 || initializes != 1 {
		t.Fatalf("cold starts = %d, initializes = %d; want 1 each", starts, initializes)
	}
}

func TestEnsureReadyRetriesAfterInitializeFailure(t *testing.T) {
	client := &fakeCodexClient{initErrors: []error{errors.New("handshake failed")}}
	rt := &accountRuntime{client: client}
	if err := rt.ensureReady(context.Background()); err == nil {
		t.Fatal("first initialization unexpectedly succeeded")
	}
	if err := rt.ensureReady(context.Background()); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	starts, initializes, closes, _ := client.counts()
	if starts != 2 || initializes != 2 || closes != 1 {
		t.Fatalf("starts = %d, initializes = %d, closes = %d; want 2, 2, 1", starts, initializes, closes)
	}
}

func TestStopWaitsForStartupAndPreventsRestart(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	client := &fakeCodexClient{initStarted: started, initRelease: release}
	rt := &accountRuntime{client: client}
	readyDone := make(chan error, 1)
	go func() { readyDone <- rt.ensureReady(context.Background()) }()
	<-started
	stopDone := make(chan struct{})
	go func() { rt.stop(); close(stopDone) }()
	close(release)
	if err := <-readyDone; err != nil {
		t.Fatalf("startup failed: %v", err)
	}
	<-stopDone
	if err := rt.ensureReady(context.Background()); !errors.Is(err, errRuntimeStopped) {
		t.Fatalf("restart error = %v; want stopped", err)
	}
	starts, initializes, closes, _ := client.counts()
	if starts != 1 || initializes != 1 || closes != 1 {
		t.Fatalf("starts = %d, initializes = %d, closes = %d; want 1 each", starts, initializes, closes)
	}
}

func TestDeviceLoginStartsColdRuntime(t *testing.T) {
	client := &fakeCodexClient{}
	a := &App{runtimes: map[int64]*accountRuntime{2: {client: client}}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/v1/accounts/2/login/device", nil)
	a.deviceLogin(recorder, request, 2)
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	starts, initializes, _, calls := client.counts()
	if starts != 1 || initializes != 1 || calls != 1 {
		t.Fatalf("starts = %d, initializes = %d, calls = %d; want 1 each", starts, initializes, calls)
	}
	if status := a.runtimes[2].deviceLoginResult("login-1"); status != "pending" {
		t.Fatalf("device login status = %q; want pending", status)
	}
}

func TestDeviceLoginCompletionMatchesLoginID(t *testing.T) {
	rt := &accountRuntime{client: &fakeCodexClient{connected: true}}
	rt.startDeviceLogin("current-login")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &App{ctx: ctx, runtimes: map[int64]*accountRuntime{2: rt}}

	a.onCodexNotification(2)("account/login/completed", json.RawMessage(`{"loginId":"older-login","success":false,"error":"cancelled"}`))
	if status := rt.deviceLoginResult("current-login"); status != "pending" {
		t.Fatalf("status after stale completion = %q; want pending", status)
	}

	a.onCodexNotification(2)("account/login/completed", json.RawMessage(`{"loginId":"current-login","success":false,"error":"cancelled"}`))
	if status := rt.deviceLoginResult("current-login"); status != "failed" {
		t.Fatalf("status after matching completion = %q; want failed", status)
	}

	rt.startDeviceLogin("successful-login")
	a.onCodexNotification(2)("account/login/completed", json.RawMessage(`{"loginId":"successful-login","success":true,"error":null}`))
	if status := rt.deviceLoginResult("successful-login"); status != "completed" {
		t.Fatalf("status after successful completion = %q; want completed", status)
	}
}

func TestAccountClassificationReadyRequiresConnectedKnownPlan(t *testing.T) {
	team := "team"
	unknown := "unknown"
	tests := []struct {
		name    string
		account AccountView
		want    bool
	}{
		{name: "disconnected", account: AccountView{PlanType: &team}, want: false},
		{name: "missing plan", account: AccountView{Connected: true}, want: false},
		{name: "unknown plan", account: AccountView{Connected: true, PlanType: &unknown}, want: false},
		{name: "classified", account: AccountView{Connected: true, PlanType: &team}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &accountRuntime{dash: Dashboard{Account: tt.account}}
			if got := accountClassificationReady(rt); got != tt.want {
				t.Fatalf("accountClassificationReady() = %v; want %v", got, tt.want)
			}
		})
	}
}
