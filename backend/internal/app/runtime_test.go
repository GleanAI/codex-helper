package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	initStarted chan struct{}
	initRelease chan struct{}
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
	if method == "account/login/start" {
		result := out.(*map[string]any)
		*result = map[string]any{"verificationUrl": "https://example.test/device", "userCode": "ABCD-EFGH"}
	}
	return nil
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
