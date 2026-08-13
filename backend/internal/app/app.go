package app

import (
	"context"
	"crypto/tls"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"codex-helper/internal/codex"
	"codex-helper/internal/security"
	"codex-helper/internal/store"
	webassets "codex-helper/internal/web"
)

type App struct {
	dataDir       string
	store         *store.Store
	vault         *security.Vault
	server        *http.Server
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.RWMutex
	runtimes      map[int64]*accountRuntime
	loginAttempts sync.Map
}
type accountRuntime struct {
	client  *codex.Client
	dash    Dashboard
	syncing sync.Mutex
}

func New() (*App, error) {
	dir := env("DATA_DIR", "/data")
	s, e := store.Open(dir)
	if e != nil {
		return nil, e
	}
	v, e := security.OpenVault(dir)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{dataDir: dir, store: s, vault: v, ctx: ctx, cancel: cancel, runtimes: map[int64]*accountRuntime{}}
	accounts, _ := s.Accounts()
	for _, account := range accounts {
		a.addRuntime(account.ID)
	}
	a.server = &http.Server{Addr: env("LISTEN_ADDR", ":8080"), Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	return a, nil
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func (a *App) Run() error {
	if e := os.MkdirAll(filepath.Join(a.dataDir, "codex"), 0700); e != nil {
		return e
	}
	go a.keepCodex()
	go a.scheduler()
	go a.telegramLoop()
	log.Printf("codex-helper listening on %s", a.server.Addr)
	e := a.server.ListenAndServe()
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
func (a *App) Close() {
	a.cancel()
	ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	_ = a.server.Shutdown(ctx)
	a.mu.RLock()
	for _, rt := range a.runtimes {
		_ = rt.client.Close()
	}
	a.mu.RUnlock()
	_ = a.store.DB.Close()
}
func (a *App) keepCodex() {
	for {
		select {
		case <-a.ctx.Done():
			return
		default:
		}
		a.mu.RLock()
		ids := make([]int64, 0, len(a.runtimes))
		for id := range a.runtimes {
			ids = append(ids, id)
		}
		a.mu.RUnlock()
		for _, id := range ids {
			rt := a.runtime(id)
			if rt == nil || rt.client.Connected() {
				continue
			}
			if e := rt.client.Start(a.ctx); e == nil {
				ctx, c := context.WithTimeout(a.ctx, 20*time.Second)
				e = rt.client.Initialize(ctx)
				c()
				if e == nil {
					_ = a.syncAccount(context.Background(), id)
				} else {
					log.Printf("app-server initialize: %v", e)
					// A live process is not necessarily an initialized process. Tear it
					// down so the next iteration starts a fresh protocol session.
					_ = rt.client.Close()
				}
			}
		}
		time.Sleep(time.Second)
	}
}
func (a *App) onCodexNotification(id int64) func(string, json.RawMessage) {
	return func(method string, _ json.RawMessage) {
		if method == "account/updated" || method == "account/rateLimits/updated" {
			go a.syncAccount(context.Background(), id)
		}
	}
}
func (a *App) addRuntime(id int64) {
	dir := filepath.Join(a.dataDir, "accounts", strconv.FormatInt(id, 10), "codex")
	if id == 1 {
		// Account 1 deliberately keeps the legacy path so upgrades retain the
		// existing login, and fresh installs use the same deterministic path.
		dir = filepath.Join(a.dataDir, "codex")
	}
	rt := &accountRuntime{client: codex.New(dir, a.onCodexNotification(id)), dash: Dashboard{Limits: []LimitBucket{}, Usage: []UsagePoint{}, Stale: true}}
	a.mu.Lock()
	a.runtimes[id] = rt
	a.mu.Unlock()
}
func (a *App) runtime(id int64) *accountRuntime {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.runtimes[id]
}
func (a *App) syncAll(ctx context.Context) {
	a.mu.RLock()
	ids := make([]int64, 0, len(a.runtimes))
	for id := range a.runtimes {
		ids = append(ids, id)
	}
	a.mu.RUnlock()
	for _, id := range ids {
		_ = a.syncAccount(ctx, id)
	}
}
func (a *App) scheduler() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-t.C:
			g := a.general()
			if time.Now().Unix()%(int64(g.SyncMinutes)*60) < 60 {
				a.syncAll(context.Background())
			}
			_, _ = a.store.Cleanup(g.RetentionDays)
			go a.processReminders()
		}
	}
}

func (a *App) routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/health/live", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]any{"status": "ok"}) })
	m.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		if e := a.store.Health(r.Context()); e != nil {
			jsonOut(w, 503, map[string]string{"error": e.Error()})
			return
		}
		connected := false
		a.mu.RLock()
		for _, rt := range a.runtimes {
			if rt.client.Connected() {
				connected = true
				break
			}
		}
		a.mu.RUnlock()
		jsonOut(w, 200, map[string]any{"status": "ok", "appServer": connected})
	})
	m.HandleFunc("/api/v1/", a.api)
	sub, _ := fs.Sub(webassets.Assets, "dist")
	files := http.FileServer(http.FS(sub))
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if _, e := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/")); e == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		b, _ := fs.ReadFile(sub, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
	return securityHeaders(m)
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func (a *App) authed(r *http.Request) bool {
	c, e := r.Cookie("session")
	if e != nil {
		return false
	}
	var x int
	return a.store.DB.QueryRow("SELECT 1 FROM sessions WHERE token_hash=? AND expires_at>?", security.HashToken(c.Value), time.Now().Unix()).Scan(&x) == nil
}
func (a *App) require(w http.ResponseWriter, r *http.Request) bool {
	if !a.authed(r) {
		jsonOut(w, 401, map[string]string{"error": "未登录"})
		return false
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		if r.Header.Get("X-Requested-With") != "codex-helper" {
			jsonOut(w, 403, map[string]string{"error": "请求来源校验失败"})
			return false
		}
	}
	return true
}

// Keep an explicit embed reference visible to tooling.
var _ embed.FS
var _ = sql.ErrNoRows
var _ = strconv.Itoa
var _ = tls.VersionTLS13
var _ = smtp.SendMail
