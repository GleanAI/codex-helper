package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"codex-helper/internal/security"
	"codex-helper/internal/store"
)

func (a *App) api(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	if p == "system/status" {
		if r.Method != http.MethodGet {
			jsonOut(w, http.StatusMethodNotAllowed, map[string]string{"error": "方法不允许"})
			return
		}
		connected := false
		a.mu.RLock()
		for _, rt := range a.runtimes {
			if rt.Ready() {
				connected = true
				break
			}
		}
		a.mu.RUnlock()
		jsonOut(w, 200, map[string]any{"initialized": a.store.Initialized(), "version": Version, "appServer": connected})
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
	if p == "public/overview" {
		a.publicOverview(w, r)
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
	case p == "auth/credentials" && r.Method == http.MethodPut:
		a.updateCredentials(w, r)
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
			DisplayName  string `json:"displayName"`
			ExpectedKind string `json:"expectedKind"`
		}
		if decode(w, r, &in) != nil {
			jsonOut(w, 400, map[string]string{"error": "请求格式错误"})
			break
		}
		in.DisplayName = strings.TrimSpace(in.DisplayName)
		if in.DisplayName == "" {
			in.DisplayName = "新账号"
		}
		if in.ExpectedKind == "" {
			in.ExpectedKind = "any"
		}
		if !store.ValidExpectedKind(in.ExpectedKind) {
			jsonOut(w, 400, map[string]string{"error": "连接类型无效"})
			break
		}
		x, e := a.store.CreateAccount(in.DisplayName, in.ExpectedKind)
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
			if d.Limits == nil {
				d.Limits = []LimitBucket{}
			}
			if d.Usage == nil {
				d.Usage = []UsagePoint{}
			}
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
		a.telegramMu.Lock()
		code := fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
		_ = a.store.SetJSON("telegram_bind", map[string]any{"code": code, "expires": time.Now().Add(10 * time.Minute).Unix()})
		a.telegramMu.Unlock()
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
			DisplayName  string `json:"displayName"`
			ExpectedKind string `json:"expectedKind"`
		}
		if decode(w, r, &in) != nil || strings.TrimSpace(in.DisplayName) == "" {
			jsonOut(w, 400, map[string]string{"error": "名称不能为空"})
			return
		}
		if in.ExpectedKind == "" {
			accounts, _ := a.store.Accounts()
			for _, account := range accounts {
				if account.ID == id {
					in.ExpectedKind = account.ExpectedKind
					break
				}
			}
		}
		if !store.ValidExpectedKind(in.ExpectedKind) {
			jsonOut(w, 400, map[string]string{"error": "连接类型无效"})
			return
		}
		e = a.store.UpdateAccountSettings(id, strings.TrimSpace(in.DisplayName), in.ExpectedKind)
		if e == nil {
			rt.syncing.Lock()
			rt.dash.DisplayName = strings.TrimSpace(in.DisplayName)
			rt.syncing.Unlock()
			jsonOut(w, 200, map[string]bool{"ok": true})
		}
	case action == "" && r.Method == "DELETE":
		a.mu.Lock()
		delete(a.runtimes, id)
		a.mu.Unlock()
		rt.stop()
		rt.syncing.Lock()
		a.reminderSendMu.Lock()
		e = a.store.DeleteAccount(id)
		a.reminderSendMu.Unlock()
		if e != nil {
			rt.syncing.Unlock()
			a.addRuntime(id)
			break
		}
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
		rt.syncing.Unlock()
	case action == "login" && len(parts) > 3 && parts[3] == "device" && r.Method == "POST":
		a.deviceLogin(w, r, id)
	case action == "logout" && r.Method == "POST":
		rt.syncing.Lock()
		defer rt.syncing.Unlock()
		var out any
		e = rt.ensureReady(r.Context())
		if e == nil {
			e = rt.client.Call(r.Context(), "account/logout", map[string]any{}, &out)
		}
		if e == nil {
			e = a.store.DisconnectAccount(id)
			if e == nil {
				rt.dash.Account = AccountView{Connected: false}
				rt.dash.Limits = []LimitBucket{}
				rt.dash.MonthlyCreditLimit = nil
				rt.dash.Summary = UsageSummary{}
				rt.dash.Usage = []UsagePoint{}
				rt.dash.FetchedAt = time.Now().Unix()
				jsonOut(w, 200, map[string]bool{"ok": true})
			}
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
	if decode(w, r, &in) != nil || len(in.Username) < 3 || len(in.Password) < 10 {
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
	key, allowed := a.reserveLoginAttempt(r, time.Now())
	if !allowed {
		jsonOut(w, 429, map[string]string{"error": "尝试次数过多，请稍后再试"})
		return
	}
	succeeded := false
	defer func() { a.finishLoginAttempt(key, succeeded, time.Now()) }()
	var in struct{ Username, Password string }
	_ = decode(w, r, &in)
	var user, hash string
	e := a.store.DB.QueryRow("SELECT username,password_hash FROM admin WHERE id=1").Scan(&user, &hash)
	if e != nil || user != in.Username || !security.VerifyPassword(hash, in.Password) {
		jsonOut(w, 401, map[string]string{"error": "用户名或密码错误"})
		return
	}
	succeeded = true
	a.newSession(w, user)
	jsonOut(w, 200, map[string]bool{"ok": true})
}

const (
	loginWindow      = 15 * time.Minute
	loginMaxEntries  = 10_000
	loginMaxAttempts = 10
)

type loginAttemptState struct {
	failures []time.Time
	inFlight int
	lastSeen time.Time
}

func parseTrustedProxyCIDRs(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			address, addressErr := netip.ParseAddr(value)
			if addressErr != nil {
				return nil, fmt.Errorf("TRUSTED_PROXY_CIDRS 包含无效地址 %q", value)
			}
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func (a *App) reserveLoginAttempt(r *http.Request, now time.Time) (string, bool) {
	key := a.loginClientIP(r).String()
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	if a.loginAttempts == nil {
		a.loginAttempts = map[string]*loginAttemptState{}
	}
	if a.loginCleanupAt.IsZero() || now.Sub(a.loginCleanupAt) >= time.Minute {
		for ip, state := range a.loginAttempts {
			state.failures = recentLoginFailures(state.failures, now)
			if state.inFlight == 0 && len(state.failures) == 0 && now.Sub(state.lastSeen) >= loginWindow {
				delete(a.loginAttempts, ip)
			}
		}
		a.loginCleanupAt = now
	}
	state := a.loginAttempts[key]
	if state == nil {
		if len(a.loginAttempts) >= loginMaxEntries {
			return key, false
		}
		state = &loginAttemptState{}
		a.loginAttempts[key] = state
	}
	state.failures = recentLoginFailures(state.failures, now)
	state.lastSeen = now
	if len(state.failures)+state.inFlight >= loginMaxAttempts {
		return key, false
	}
	state.inFlight++
	return key, true
}

func (a *App) finishLoginAttempt(key string, succeeded bool, now time.Time) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	state := a.loginAttempts[key]
	if state == nil {
		return
	}
	if state.inFlight > 0 {
		state.inFlight--
	}
	if succeeded {
		delete(a.loginAttempts, key)
		return
	}
	state.failures = append(recentLoginFailures(state.failures, now), now)
	state.lastSeen = now
}

func recentLoginFailures(failures []time.Time, now time.Time) []time.Time {
	fresh := failures[:0]
	for _, failure := range failures {
		if now.Sub(failure) < loginWindow {
			fresh = append(fresh, failure)
		}
	}
	return fresh
}

func (a *App) loginClientIP(r *http.Request) netip.Addr {
	peer := parseRequestIP(r.RemoteAddr)
	if !containsIP(a.trustedProxies, peer) {
		return peer
	}
	chain, ok := forwardedChain(r.Header.Get("Forwarded"))
	if !ok || len(chain) == 0 {
		chain, ok = xForwardedForChain(r.Header.Get("X-Forwarded-For"))
	}
	if !ok || len(chain) == 0 {
		return peer
	}
	chain = append(chain, peer)
	for i := len(chain) - 1; i >= 0; i-- {
		if !containsIP(a.trustedProxies, chain[i]) {
			return chain[i]
		}
	}
	return chain[0]
}

func parseRequestIP(value string) netip.Addr {
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	if zone := strings.LastIndex(value, "%"); zone >= 0 {
		value = value[:zone]
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.IPv6Unspecified()
	}
	return address.Unmap()
}

func containsIP(prefixes []netip.Prefix, address netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func xForwardedForChain(value string) ([]netip.Addr, bool) {
	if strings.TrimSpace(value) == "" {
		return nil, false
	}
	parts := strings.Split(value, ",")
	chain := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		address := parseRequestIP(strings.TrimSpace(part))
		if !address.IsValid() || address.IsUnspecified() {
			return nil, false
		}
		chain = append(chain, address)
	}
	return chain, true
}

func forwardedChain(value string) ([]netip.Addr, bool) {
	if strings.TrimSpace(value) == "" {
		return nil, false
	}
	elements := strings.Split(value, ",")
	chain := make([]netip.Addr, 0, len(elements))
	for _, element := range elements {
		found := false
		for _, parameter := range strings.Split(element, ";") {
			name, raw, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(name, "for") {
				continue
			}
			raw = strings.Trim(strings.TrimSpace(raw), `"`)
			address := parseRequestIP(raw)
			if !address.IsValid() || address.IsUnspecified() {
				return nil, false
			}
			chain = append(chain, address)
			found = true
			break
		}
		if !found {
			return nil, false
		}
	}
	return chain, true
}

func (a *App) updateCredentials(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if decode(w, r, &in) != nil || len(in.Username) < 3 || (in.NewPassword != "" && len(in.NewPassword) < 10) {
		jsonOut(w, http.StatusBadRequest, map[string]string{"error": "用户名至少3位，新密码至少10位"})
		return
	}

	var username, passwordHash string
	if err := a.store.DB.QueryRow("SELECT username,password_hash FROM admin WHERE id=1").Scan(&username, &passwordHash); err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": "更新登录凭据失败"})
		return
	}
	if !security.VerifyPassword(passwordHash, in.CurrentPassword) {
		jsonOut(w, http.StatusForbidden, map[string]string{"error": "当前密码错误"})
		return
	}
	if username == in.Username && in.NewPassword == "" {
		jsonOut(w, http.StatusOK, map[string]string{"username": username})
		return
	}

	newHash := passwordHash
	if in.NewPassword != "" {
		newHash = security.Password(in.NewPassword)
	}
	cookie, err := r.Cookie("session")
	if err != nil {
		jsonOut(w, http.StatusUnauthorized, map[string]string{"error": "未登录"})
		return
	}
	updated, err := a.replaceCredentials(in.Username, newHash, passwordHash, cookie.Value)
	if err != nil {
		jsonOut(w, http.StatusInternalServerError, map[string]string{"error": "更新登录凭据失败"})
		return
	}
	if !updated {
		jsonOut(w, http.StatusForbidden, map[string]string{"error": "当前密码错误"})
		return
	}
	jsonOut(w, http.StatusOK, map[string]string{"username": in.Username})
}

func (a *App) replaceCredentials(username, newHash, expectedHash, currentToken string) (bool, error) {
	tx, err := a.store.DB.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(
		"UPDATE admin SET username=?,password_hash=? WHERE id=1 AND password_hash=?",
		username, newHash, expectedHash,
	)
	if err != nil {
		return false, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if updated != 1 {
		return false, nil
	}
	if _, err = tx.Exec("DELETE FROM sessions WHERE token_hash<>?", security.HashToken(currentToken)); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
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
	if decode(w, r, &g) != nil || g.SyncMinutes < 1 || g.SyncMinutes > 60 || g.RetentionDays < 30 || g.RetentionDays > 365 || g.BeforeMinutes < 1 || g.BeforeMinutes > 1440 {
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
	if rt == nil {
		jsonOut(w, 404, map[string]string{"error": "账号不存在"})
		return
	}
	e := rt.ensureReady(r.Context())
	if e == nil {
		e = rt.client.Call(r.Context(), "account/login/start", map[string]any{"type": "chatgptDeviceCode"}, &out)
	}
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": e.Error()})
		return
	}
	jsonOut(w, 200, out)
}

func (a *App) syncAccount(ctx context.Context, id int64) (syncErr error) {
	rt := a.runtime(id)
	if rt == nil {
		return fmt.Errorf("账号不存在")
	}
	rt.syncing.Lock()
	defer func() {
		if syncErr != nil && !errors.Is(syncErr, errRuntimeStopped) && !errors.Is(syncErr, context.Canceled) {
			rt.dash.Stale = true
			rt.dash.LastError = syncErr.Error()
		}
		rt.syncing.Unlock()
	}()
	if err := rt.ensureReady(ctx); err != nil {
		return err
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
	var previousEmail *string
	accounts, _ := a.store.Accounts()
	for _, x := range accounts {
		if x.ID == id {
			name = x.DisplayName
			previousEmail = x.Email
			break
		}
	}
	d := Dashboard{AccountID: id, DisplayName: name, FetchedAt: time.Now().Unix(), Account: AccountView{Connected: ar.Account != nil}, Limits: []LimitBucket{}, Usage: []UsagePoint{}}
	if ar.Account != nil {
		d.Account.Email = ar.Account.Email
		d.Account.PlanType = ar.Account.PlanType
		d.Account.AuthMode = &ar.Account.Type
	}
	if previousEmail == nil {
		previousEmail = rt.dash.Account.Email
	}
	if usageIdentityChanged(previousEmail, d.Account.Email) {
		if err := a.store.DeleteDailyUsage(id); err != nil {
			return err
		}
	}
	if d.Account.Connected && rt.dash.Account.Connected && d.Account.Email != nil && rt.dash.Account.Email != nil && strings.EqualFold(*d.Account.Email, *rt.dash.Account.Email) {
		d.Summary = rt.dash.Summary
	}
	var lr struct {
		RateLimits *rawLimit           `json:"rateLimits"`
		By         map[string]rawLimit `json:"rateLimitsByLimitId"`
	}
	if e := rt.client.Call(ctx, "account/rateLimits/read", map[string]any{}, &lr); e == nil {
		d.MonthlyCreditLimit = monthlyCreditLimitFrom(lr.RateLimits, lr.By)
		if len(lr.By) > 0 {
			for _, x := range lr.By {
				d.Limits = append(d.Limits, flattenLimit(x)...)
			}
		} else if lr.RateLimits != nil {
			d.Limits = flattenLimit(*lr.RateLimits)
		}
	}
	// Some app-server versions expose the workspace plan only on limit buckets.
	if ar.Account != nil && store.AccountKind(d.Account.PlanType) == "unknown" {
		var fallback *string
		consistent := true
		for _, x := range d.Limits {
			if store.AccountKind(x.PlanType) == "unknown" {
				continue
			}
			if fallback == nil {
				fallback = x.PlanType
			} else if store.AccountKind(fallback) != store.AccountKind(x.PlanType) {
				consistent = false
			}
		}
		if consistent && fallback != nil {
			d.Account.PlanType = fallback
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
		usage := make([]store.DailyUsage, 0, len(ur.Daily))
		location, locationErr := time.LoadLocation(a.general().Timezone)
		if locationErr != nil {
			location = time.UTC
		}
		for _, x := range ur.Daily {
			parsed, parseErr := time.ParseInLocation("2006-01-02", x.StartDate, location)
			if parseErr != nil || parsed.Format("2006-01-02") != x.StartDate || x.Tokens < 0 {
				continue
			}
			usage = append(usage, store.DailyUsage{Date: x.StartDate, TotalTokens: x.Tokens})
		}
		if e = a.store.UpsertDailyUsage(id, usage, d.FetchedAt); e != nil {
			return e
		}
	}
	var e error
	d.Usage, e = a.dailyUsageHistory(id, time.Now())
	if e != nil {
		return e
	}
	_, e = a.storeLimitSnapshots(d)
	if e != nil {
		return e
	}
	if e = a.store.UpdateAccount(id, d.Account.Email, d.Account.PlanType, d.Account.Connected); e != nil {
		return e
	}
	rt.dash = d
	promoted, e := a.store.PromoteStagedNotifications(id, d.FetchedAt)
	if e != nil {
		return e
	}
	if promoted > 0 {
		go a.processReminders()
	}
	return nil
}

func usageIdentityChanged(previous, current *string) bool {
	return previous == nil || current == nil || !strings.EqualFold(strings.TrimSpace(*previous), strings.TrimSpace(*current))
}

func (a *App) dailyUsageHistory(accountID int64, now time.Time) ([]UsagePoint, error) {
	g := a.general()
	location, err := time.LoadLocation(g.Timezone)
	if err != nil {
		location = time.UTC
	}
	today := now.In(location)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, location)
	cutoff := today.AddDate(0, 0, -(g.RetentionDays - 1))
	stored, err := a.store.DailyUsage(accountID, cutoff.Format("2006-01-02"), today.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	byDate := make(map[string]int64, len(stored))
	var first time.Time
	for _, point := range stored {
		date, parseErr := time.ParseInLocation("2006-01-02", point.Date, location)
		if parseErr != nil || date.Format("2006-01-02") != point.Date || point.TotalTokens < 0 {
			continue
		}
		if first.IsZero() {
			first = date
		}
		byDate[point.Date] = point.TotalTokens
	}
	if first.IsZero() {
		return []UsagePoint{}, nil
	}
	usage := make([]UsagePoint, 0, int(today.Sub(first).Hours()/24)+1)
	for date := first; !date.After(today); date = date.AddDate(0, 0, 1) {
		key := date.Format("2006-01-02")
		usage = append(usage, UsagePoint{Date: key, TotalTokens: byDate[key]})
	}
	return usage, nil
}

const resetDropTolerance = 0.01

func (a *App) storeLimitSnapshots(d Dashboard) ([]string, error) {
	tx, err := a.store.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	g := a.general()
	resetNotifications := []string{}
	for _, x := range d.Limits {
		var previousID, previousFetchedAt, previousResetsAt int64
		var previousUsed float64
		err = tx.QueryRow(`SELECT id,used_percent,resets_at,fetched_at FROM limit_snapshots
			WHERE account_id=? AND limit_id=? AND window_type=? ORDER BY fetched_at DESC,id DESC LIMIT 1`,
			d.AccountID, x.LimitID, x.WindowType).Scan(&previousID, &previousUsed, &previousResetsAt, &previousFetchedAt)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		age := d.FetchedAt - previousFetchedAt
		if err == nil && g.NotifyAfter {
			withinScheduledWindow := previousResetsAt > 0 && previousResetsAt <= d.FetchedAt && d.FetchedAt-previousResetsAt <= int64((6*time.Hour).Seconds())
			normalReset := withinScheduledWindow && x.ResetsAt > previousResetsAt && x.ResetsAt > d.FetchedAt
			earlyReset := previousResetsAt > d.FetchedAt && age >= 0 && age <= int64((6*time.Hour).Seconds()) && previousUsed-x.UsedPercent > resetDropTolerance
			if normalReset || earlyReset {
				kind := "detected_after"
				key := fmt.Sprintf("%d:%s:%s:detected:%d", d.AccountID, x.LimitID, x.WindowType, previousID)
				if normalReset {
					kind = "after"
					key = fmt.Sprintf("%d:%s:%s:%d:after", d.AccountID, x.LimitID, x.WindowType, previousResetsAt)
				}
				event := notificationEvent{Version: 1, Kind: kind, Confirmed: true, Account: d.DisplayName, DurationMins: x.WindowDurationMinutes,
					Remaining: 100 - x.UsedPercent, PreviousUsed: previousUsed, Used: x.UsedPercent, ResetsAt: x.ResetsAt}
				body, _ := json.Marshal(event)
				_, err = tx.Exec(`INSERT OR IGNORE INTO notifications
					(dedupe_key,channel,kind,status,attempts,last_error,scheduled_at,sent_at,body,account_id)
					VALUES(?,?,?,'staged',0,'',?,NULL,?,?)`, key, "configured", kind, d.FetchedAt, string(body), d.AccountID)
				if err != nil {
					return nil, err
				}
				resetNotifications = append(resetNotifications, key)
			}
		}
		if _, err = tx.Exec("INSERT INTO limit_snapshots(limit_id,window_type,used_percent,duration_mins,resets_at,fetched_at,account_id) VALUES(?,?,?,?,?,?,?)", x.LimitID, x.WindowType, x.UsedPercent, x.WindowDurationMinutes, x.ResetsAt, d.FetchedAt, d.AccountID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return resetNotifications, nil
}

type rawLimit struct {
	LimitID         string                 `json:"limitId"`
	LimitName       *string                `json:"limitName"`
	PlanType        *string                `json:"planType"`
	Primary         *rawLimitWindow        `json:"primary"`
	Secondary       *rawLimitWindow        `json:"secondary"`
	IndividualLimit *rawMonthlyCreditLimit `json:"individualLimit"`
}

type rawMonthlyCreditLimit struct {
	RemainingPercent float64 `json:"remainingPercent"`
	ResetsAt         int64   `json:"resetsAt"`
	Used             string  `json:"used"`
	Limit            string  `json:"limit"`
}

type rawLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int    `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"`
}

func flattenLimit(x rawLimit) []LimitBucket {
	out := []LimitBucket{}
	if x.Primary != nil {
		out = append(out, flattenLimitWindow(x, "primary", x.Primary))
	}
	if x.Secondary != nil {
		out = append(out, flattenLimitWindow(x, "secondary", x.Secondary))
	}
	return out
}

func flattenLimitWindow(limit rawLimit, windowType string, window *rawLimitWindow) LimitBucket {
	duration, resetsAt := 0, int64(0)
	if window.WindowDurationMins != nil {
		duration = *window.WindowDurationMins
	}
	if window.ResetsAt != nil {
		resetsAt = *window.ResetsAt
	}
	return LimitBucket{
		LimitID:               limit.LimitID,
		LimitName:             limit.LimitName,
		WindowType:            windowType,
		UsedPercent:           window.UsedPercent,
		WindowDurationMinutes: duration,
		ResetsAt:              resetsAt,
		PlanType:              limit.PlanType,
	}
}

func monthlyCreditLimitFrom(rateLimits *rawLimit, by map[string]rawLimit) *MonthlyCreditLimit {
	individual := (*rawMonthlyCreditLimit)(nil)
	if rateLimits != nil {
		individual = rateLimits.IndividualLimit
	}
	if individual == nil {
		if canonical, ok := by["codex"]; ok {
			individual = canonical.IndividualLimit
		}
	}
	if individual == nil {
		return nil
	}
	return &MonthlyCreditLimit{
		RemainingPercent: individual.RemainingPercent,
		ResetsAt:         individual.ResetsAt,
		Used:             individual.Used,
		Limit:            individual.Limit,
	}
}

var _ = context.Canceled
var _ = sql.ErrNoRows
var _ = strconv.Itoa
