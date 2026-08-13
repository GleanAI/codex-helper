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
		connected := false
		a.mu.RLock()
		for _, rt := range a.runtimes {
			if rt.client.Connected() {
				connected = true
				break
			}
		}
		a.mu.RUnlock()
		jsonOut(w, 200, map[string]any{"initialized": a.store.Initialized(), "version": "0.2.0", "appServer": connected})
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
	case p == "accounts" && r.Method == "GET":
		x, e := a.store.Accounts()
		if e != nil {
			jsonOut(w, 500, map[string]string{"error": e.Error()})
		} else {
			jsonOut(w, 200, x)
		}
	case p == "accounts" && r.Method == "POST":
		var in struct {
			DisplayName string `json:"displayName"`
		}
		if decode(r, &in) != nil {
			jsonOut(w, 400, map[string]string{"error": "请求格式错误"})
			break
		}
		in.DisplayName = strings.TrimSpace(in.DisplayName)
		if in.DisplayName == "" {
			in.DisplayName = "新账号"
		}
		x, e := a.store.CreateAccount(in.DisplayName)
		if e == nil {
			a.addRuntime(x.ID)
			jsonOut(w, 201, x)
		} else {
			jsonOut(w, 500, map[string]string{"error": e.Error()})
		}
	case strings.HasPrefix(p, "accounts/"):
		a.accountAPI(w, r, p)
	case p == "dashboard":
		id, _ := strconv.ParseInt(r.URL.Query().Get("accountId"), 10, 64)
		if id == 0 {
			id = 1
		}
		rt := a.runtime(id)
		if rt == nil {
			jsonOut(w, 404, map[string]string{"error": "账号不存在"})
		} else {
			rt.syncing.Lock()
			d := rt.dash
			rt.syncing.Unlock()
			jsonOut(w, 200, d)
		}
	case p == "sync" && r.Method == "POST":
		id, _ := strconv.ParseInt(r.URL.Query().Get("accountId"), 10, 64)
		if id == 0 {
			id = 1
		}
		e := a.syncAccount(r.Context(), id)
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

func (a *App) accountAPI(w http.ResponseWriter, r *http.Request, p string) {
	parts := strings.Split(p, "/")
	if len(parts) < 2 {
		jsonOut(w, 404, map[string]string{"error": "接口不存在"})
		return
	}
	id, e := strconv.ParseInt(parts[1], 10, 64)
	if e != nil {
		jsonOut(w, 400, map[string]string{"error": "账号 ID 无效"})
		return
	}
	rt := a.runtime(id)
	if rt == nil {
		jsonOut(w, 404, map[string]string{"error": "账号不存在"})
		return
	}
	action := ""
	if len(parts) > 2 {
		action = parts[2]
	}
	switch {
	case action == "" && r.Method == "PUT":
		var in struct {
			DisplayName string `json:"displayName"`
		}
		if decode(r, &in) != nil || strings.TrimSpace(in.DisplayName) == "" {
			jsonOut(w, 400, map[string]string{"error": "名称不能为空"})
			return
		}
		e = a.store.RenameAccount(id, strings.TrimSpace(in.DisplayName))
		if e == nil {
			jsonOut(w, 200, map[string]bool{"ok": true})
		}
	case action == "" && r.Method == "DELETE":
		a.mu.Lock()
		delete(a.runtimes, id)
		a.mu.Unlock()
		_ = rt.client.Close()
		e = a.store.DeleteAccount(id)
		if e == nil {
			dir := filepath.Join(a.dataDir, "accounts", strconv.FormatInt(id, 10))
			if id == 1 {
				dir = filepath.Join(a.dataDir, "codex")
			}
			e = os.RemoveAll(dir)
		}
		if e == nil {
			jsonOut(w, 200, map[string]bool{"ok": true})
		}
	case action == "login" && len(parts) > 3 && parts[3] == "device" && r.Method == "POST":
		a.deviceLogin(w, r, id)
	case action == "logout" && r.Method == "POST":
		var out any
		e = rt.client.Call(r.Context(), "account/logout", map[string]any{}, &out)
		if e == nil {
			_ = a.store.UpdateAccount(id, nil, nil, false)
			jsonOut(w, 200, map[string]bool{"ok": true})
		}
	case action == "sync" && r.Method == "POST":
		e = a.syncAccount(r.Context(), id)
		if e == nil {
			jsonOut(w, 200, map[string]bool{"ok": true})
		}
	default:
		jsonOut(w, 404, map[string]string{"error": "接口不存在"})
		return
	}
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": e.Error()})
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
func (a *App) deviceLogin(w http.ResponseWriter, r *http.Request, id int64) {
	var out map[string]any
	rt := a.runtime(id)
	if !rt.client.Connected() {
		jsonOut(w, 503, map[string]string{"error": "该账号服务正在启动，请稍后重试"})
		return
	}
	e := rt.client.Call(r.Context(), "account/login/start", map[string]any{"type": "chatgptDeviceCode"}, &out)
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": e.Error()})
		return
	}
	jsonOut(w, 200, out)
}

func (a *App) syncAccount(ctx context.Context, id int64) error {
	rt := a.runtime(id)
	if rt == nil {
		return fmt.Errorf("账号不存在")
	}
	rt.syncing.Lock()
	defer rt.syncing.Unlock()
	if !rt.client.Connected() {
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
	if e := rt.client.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &ar); e != nil {
		return e
	}
	name := "账号"
	accounts, _ := a.store.Accounts()
	for _, x := range accounts {
		if x.ID == id {
			name = x.DisplayName
			break
		}
	}
	d := Dashboard{AccountID: id, DisplayName: name, FetchedAt: time.Now().Unix(), Account: AccountView{Connected: ar.Account != nil}, Limits: []LimitBucket{}, Usage: []UsagePoint{}}
	if ar.Account != nil {
		d.Account.Email = ar.Account.Email
		d.Account.PlanType = ar.Account.PlanType
		d.Account.AuthMode = &ar.Account.Type
	}
	var lr struct {
		RateLimits *rawLimit           `json:"rateLimits"`
		By         map[string]rawLimit `json:"rateLimitsByLimitId"`
	}
	if e := rt.client.Call(ctx, "account/rateLimits/read", map[string]any{}, &lr); e == nil {
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
	if e := rt.client.Call(ctx, "account/usage/read", map[string]any{}, &ur); e == nil {
		d.Summary = ur.Summary
		for _, x := range ur.Daily {
			p := UsagePoint{Date: x.StartDate, TotalTokens: x.Tokens}
			d.Usage = append(d.Usage, p)
			_, _ = a.store.DB.Exec("INSERT INTO daily_usage(account_id,date,total_tokens,fetched_at) VALUES(?,?,?,?) ON CONFLICT(account_id,date) DO UPDATE SET total_tokens=excluded.total_tokens,fetched_at=excluded.fetched_at", id, x.StartDate, x.Tokens, d.FetchedAt)
		}
	}
	for _, x := range d.Limits {
		_, _ = a.store.DB.Exec("INSERT INTO limit_snapshots(limit_id,window_type,used_percent,duration_mins,resets_at,fetched_at,account_id) VALUES(?,?,?,?,?,?,?)", x.LimitID, x.WindowType, x.UsedPercent, x.WindowDurationMinutes, x.ResetsAt, d.FetchedAt, id)
	}
	rt.dash = d
	_ = a.store.UpdateAccount(id, d.Account.Email, d.Account.PlanType, d.Account.Connected)
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
