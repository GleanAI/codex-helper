package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
)

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
