package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"codex-helper/internal/security"
)

func (a *App) api(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	if p == "system/status" {
		jsonOut(w, 200, map[string]any{"initialized": a.store.Initialized(), "version": "0.1.0", "appServer": a.codex.Connected()})
		return
	}
	if p == "setup" && r.Method == "POST" {
		a.setup(w, r)
		return
	}
	if p == "auth/login" && r.Method == "POST" {
		a.login(w, r)
		return
	}
	if !a.require(w, r) {
		return
	}
	switch {
	case p == "auth/me":
		var username string
		_ = a.store.DB.QueryRow("SELECT username FROM admin WHERE id=1").Scan(&username)
		jsonOut(w, 200, map[string]string{"username": username})
	case p == "auth/logout" && r.Method == "POST":
		a.logout(w, r)
	case p == "dashboard":
		a.mu.RLock()
		d := a.dash
		a.mu.RUnlock()
		jsonOut(w, 200, d)
	case p == "sync" && r.Method == "POST":
		e := a.sync(r.Context())
		if e != nil {
			jsonOut(w, 502, map[string]string{"error": e.Error()})
		} else {
			jsonOut(w, 200, map[string]bool{"ok": true})
		}
	case p == "settings/general":
		a.generalAPI(w, r)
	case p == "settings/smtp":
		a.smtpAPI(w, r)
	case p == "settings/smtp/test" && r.Method == "POST":
		a.smtpTest(w, r)
	case p == "settings/telegram":
		a.telegramAPI(w, r)
	case p == "settings/telegram/test" && r.Method == "POST":
		a.telegramTest(w, r)
	case p == "settings/telegram/bind" && r.Method == "POST":
		code := fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
		_ = a.store.SetJSON("telegram_bind", map[string]any{"code": code, "expires": time.Now().Add(10 * time.Minute).Unix()})
		jsonOut(w, 200, map[string]string{"code": code})
	case p == "codex/login/device" && r.Method == "POST":
		a.deviceLogin(w, r)
	case p == "codex/logout" && r.Method == "POST":
		var out any
		e := a.codex.Call(r.Context(), "account/logout", map[string]any{}, &out)
		if e != nil {
			jsonOut(w, 502, map[string]string{"error": e.Error()})
		} else {
			jsonOut(w, 200, map[string]bool{"ok": true})
		}
	case p == "maintenance/cleanup" && r.Method == "POST":
		n, e := a.store.Cleanup(a.general().RetentionDays)
		if e != nil {
			jsonOut(w, 500, map[string]string{"error": e.Error()})
		} else {
			jsonOut(w, 200, map[string]int64{"deleted": n})
		}
	case p == "maintenance/backup":
		dir, e := os.MkdirTemp(a.dataDir, "backup-")
		if e != nil {
			jsonOut(w, 500, map[string]string{"error": e.Error()})
			return
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "codex-helper.db")
		if e = a.store.Backup(r.Context(), path); e != nil {
			jsonOut(w, 500, map[string]string{"error": e.Error()})
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="codex-helper.db"`)
		http.ServeFile(w, r, path)
	default:
		jsonOut(w, 404, map[string]string{"error": "接口不存在"})
	}
}

func (a *App) setup(w http.ResponseWriter, r *http.Request) {
	if a.store.Initialized() {
		jsonOut(w, 409, map[string]string{"error": "系统已初始化"})
		return
	}
	var in struct{ Username, Password, Timezone string }
	if decode(r, &in) != nil || len(in.Username) < 3 || len(in.Password) < 10 {
		jsonOut(w, 400, map[string]string{"error": "用户名至少3位，密码至少10位"})
		return
	}
	tx, e := a.store.DB.Begin()
	if e != nil {
		jsonOut(w, 500, map[string]string{"error": e.Error()})
		return
	}
	defer tx.Rollback()
	_, e = tx.Exec("INSERT INTO admin(id,username,password_hash,created_at) VALUES(1,?,?,?)", in.Username, security.Password(in.Password), time.Now().Unix())
	if e == nil {
		g := defaults()
		if in.Timezone != "" {
			if _, z := time.LoadLocation(in.Timezone); z == nil {
				g.Timezone = in.Timezone
			}
		}
		b, _ := json.Marshal(g)
		_, e = tx.Exec("INSERT INTO settings(key,value,updated_at) VALUES('general',?,?),('initialized','true',?)", string(b), time.Now().Unix(), time.Now().Unix())
	}
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		jsonOut(w, 500, map[string]string{"error": e.Error()})
		return
	}
	a.newSession(w, in.Username)
	jsonOut(w, 201, map[string]bool{"ok": true})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if !a.store.Initialized() {
		jsonOut(w, 409, map[string]string{"error": "请先初始化"})
		return
	}
	ip := r.RemoteAddr
	v, _ := a.loginAttempts.LoadOrStore(ip, []time.Time{})
	xs := v.([]time.Time)
	now := time.Now()
	fresh := xs[:0]
	for _, x := range xs {
		if now.Sub(x) < 15*time.Minute {
			fresh = append(fresh, x)
		}
	}
	if len(fresh) >= 10 {
		jsonOut(w, 429, map[string]string{"error": "尝试次数过多，请稍后再试"})
		return
	}
	var in struct{ Username, Password string }
	_ = decode(r, &in)
	var user, hash string
	e := a.store.DB.QueryRow("SELECT username,password_hash FROM admin WHERE id=1").Scan(&user, &hash)
	if e != nil || user != in.Username || !security.VerifyPassword(hash, in.Password) {
		a.loginAttempts.Store(ip, append(fresh, now))
		jsonOut(w, 401, map[string]string{"error": "用户名或密码错误"})
		return
	}
	a.loginAttempts.Delete(ip)
	a.newSession(w, user)
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (a *App) newSession(w http.ResponseWriter, _ string) {
	tok := security.Random(32)
	_, _ = a.store.DB.Exec("DELETE FROM sessions WHERE expires_at<?", time.Now().Unix())
	_, _ = a.store.DB.Exec("INSERT INTO sessions(token_hash,expires_at,created_at) VALUES(?,?,?)", security.HashToken(tok), time.Now().Add(7*24*time.Hour).Unix(), time.Now().Unix())
	http.SetCookie(w, &http.Cookie{Name: "session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: false, MaxAge: 604800})
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("session"); e == nil {
		_, _ = a.store.DB.Exec("DELETE FROM sessions WHERE token_hash=?", security.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Path: "/", MaxAge: -1, HttpOnly: true})
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func (a *App) general() GeneralSettings {
	g := defaults()
	a.store.GetJSON("general", &g)
	if g.SyncMinutes < 1 {
		g.SyncMinutes = 5
	}
	if g.RetentionDays < 1 {
		g.RetentionDays = 90
	}
	return g
}
func (a *App) generalAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		jsonOut(w, 200, a.general())
		return
	}
	var g GeneralSettings
	if decode(r, &g) != nil || g.SyncMinutes < 1 || g.SyncMinutes > 60 || g.RetentionDays < 30 || g.RetentionDays > 365 || g.BeforeMinutes < 1 || g.BeforeMinutes > 1440 {
		jsonOut(w, 400, map[string]string{"error": "设置值不合法"})
		return
	}
	if _, e := time.LoadLocation(g.Timezone); e != nil {
		jsonOut(w, 400, map[string]string{"error": "无效时区"})
		return
	}
	_ = a.store.SetJSON("general", g)
	jsonOut(w, 200, g)
}
func (a *App) deviceLogin(w http.ResponseWriter, r *http.Request) {
	var out map[string]any
	e := a.codex.Call(r.Context(), "account/login/start", map[string]any{"type": "chatgptDeviceCode"}, &out)
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": e.Error()})
		return
	}
	jsonOut(w, 200, out)
}

func (a *App) sync(ctx context.Context) error {
	if !a.codex.Connected() {
		return fmt.Errorf("app-server 未连接")
	}
	ctx, c := context.WithTimeout(ctx, 20*time.Second)
	defer c()
	var ar struct {
		Account *struct {
			Type     string  `json:"type"`
			Email    *string `json:"email"`
			PlanType *string `json:"planType"`
		} `json:"account"`
	}
	if e := a.codex.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &ar); e != nil {
		return e
	}
	d := Dashboard{FetchedAt: time.Now().Unix(), Account: AccountView{Connected: ar.Account != nil}, Limits: []LimitBucket{}, Usage: []UsagePoint{}}
	if ar.Account != nil {
		d.Account.Email = ar.Account.Email
		d.Account.PlanType = ar.Account.PlanType
		d.Account.AuthMode = &ar.Account.Type
	}
	var lr struct {
		RateLimits *rawLimit           `json:"rateLimits"`
		By         map[string]rawLimit `json:"rateLimitsByLimitId"`
	}
	if e := a.codex.Call(ctx, "account/rateLimits/read", map[string]any{}, &lr); e == nil {
		if len(lr.By) > 0 {
			for _, x := range lr.By {
				d.Limits = append(d.Limits, flattenLimit(x)...)
			}
		} else if lr.RateLimits != nil {
			d.Limits = flattenLimit(*lr.RateLimits)
		}
	}
	var ur struct {
		Summary UsageSummary `json:"summary"`
		Daily   []struct {
			StartDate string `json:"startDate"`
			Tokens    int64  `json:"tokens"`
		} `json:"dailyUsageBuckets"`
	}
	if e := a.codex.Call(ctx, "account/usage/read", map[string]any{}, &ur); e == nil {
		d.Summary = ur.Summary
		for _, x := range ur.Daily {
			p := UsagePoint{Date: x.StartDate, TotalTokens: x.Tokens}
			d.Usage = append(d.Usage, p)
			_, _ = a.store.DB.Exec("INSERT INTO daily_usage(date,total_tokens,fetched_at) VALUES(?,?,?) ON CONFLICT(date) DO UPDATE SET total_tokens=excluded.total_tokens,fetched_at=excluded.fetched_at", x.StartDate, x.Tokens, d.FetchedAt)
		}
	}
	for _, x := range d.Limits {
		_, _ = a.store.DB.Exec("INSERT INTO limit_snapshots(limit_id,window_type,used_percent,duration_mins,resets_at,fetched_at) VALUES(?,?,?,?,?,?)", x.LimitID, x.WindowType, x.UsedPercent, x.WindowDurationMinutes, x.ResetsAt, d.FetchedAt)
	}
	a.mu.Lock()
	a.dash = d
	a.mu.Unlock()
	return nil
}

type rawLimit struct {
	LimitID   string       `json:"limitId"`
	LimitName *string      `json:"limitName"`
	PlanType  *string      `json:"planType"`
	Primary   *LimitWindow `json:"primary"`
	Secondary *LimitWindow `json:"secondary"`
}

func flattenLimit(x rawLimit) []LimitBucket {
	out := []LimitBucket{}
	if x.Primary != nil {
		out = append(out, LimitBucket{x.LimitID, x.LimitName, "primary", x.Primary.UsedPercent, x.Primary.WindowDurationMins, x.Primary.ResetsAt, x.PlanType})
	}
	if x.Secondary != nil {
		out = append(out, LimitBucket{x.LimitID, x.LimitName, "secondary", x.Secondary.UsedPercent, x.Secondary.WindowDurationMins, x.Secondary.ResetsAt, x.PlanType})
	}
	return out
}

var _ = context.Canceled
var _ = sql.ErrNoRows
var _ = strconv.Itoa
