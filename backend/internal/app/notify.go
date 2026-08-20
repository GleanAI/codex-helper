package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type notificationEvent struct {
	Version      int     `json:"version"`
	Kind         string  `json:"kind"`
	Confirmed    bool    `json:"confirmed,omitempty"`
	Account      string  `json:"account"`
	DurationMins int     `json:"durationMinutes"`
	Remaining    float64 `json:"remainingPercent"`
	PreviousUsed float64 `json:"previousUsedPercent,omitempty"`
	Used         float64 `json:"usedPercent,omitempty"`
	ResetsAt     int64   `json:"resetsAt"`
}

func (a *App) smtpSettings() SMTPSettings {
	var s SMTPSettings
	a.store.GetJSON("smtp", &s)
	if s.Port == 0 {
		s.Port = 587
	}
	if s.Security == "" {
		s.Security = "starttls"
	}
	if enc, ok := a.store.Get("smtp_password"); ok {
		if password, err := a.vault.Decrypt(enc); err == nil {
			s.Configured = password != "" && s.Host != ""
		}
	}
	s.Password = ""
	return s
}
func (a *App) smtpSecret() (SMTPSettings, error) {
	s := a.smtpSettings()
	enc, _ := a.store.Get("smtp_password")
	p, e := a.vault.Decrypt(enc)
	s.Password = p
	s.Configured = p != "" && s.Host != ""
	return s, e
}
func (a *App) smtpAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		jsonOut(w, 200, a.smtpSettings())
		return
	}
	var in SMTPSettings
	if decode(r, &in) != nil || in.Host == "" || in.Port < 1 || in.Port > 65535 || in.From == "" || in.To == "" {
		jsonOut(w, 400, map[string]string{"error": "SMTP 配置不完整"})
		return
	}
	old, _ := a.smtpSecret()
	if in.Password == "" {
		in.Password = old.Password
	}
	enc, e := a.vault.Encrypt(in.Password)
	if e == nil {
		safe := in
		safe.Password = ""
		safe.Configured = in.Password != ""
		e = a.store.SetJSON("smtp", safe)
	}
	if e == nil {
		e = a.store.Set("smtp_password", enc)
	}
	if e != nil {
		jsonOut(w, 500, map[string]string{"error": e.Error()})
		return
	}
	in.Password = ""
	in.Configured = true
	jsonOut(w, 200, in)
}
func (a *App) smtpTest(w http.ResponseWriter, r *http.Request) {
	s, e := a.smtpSecret()
	if e == nil {
		e = sendSMTP(s, "Codex Helper 测试成功", "SMTP 配置成功，后续额度重置提醒将发送到此邮箱。", testEmailHTML(a.general().Timezone))
	}
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": e.Error()})
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}

const smtpTimeout = 35 * time.Second

func sendSMTP(s SMTPSettings, subject, textBody, htmlBody string) error {
	return sendSMTPWithTimeout(s, subject, textBody, htmlBody, smtpTimeout)
}

func sendSMTPWithTimeout(s SMTPSettings, subject, textBody, htmlBody string, timeout time.Duration) error {
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	dialer := net.Dialer{Timeout: timeout}
	conn, e := dialer.Dial("tcp", addr)
	if e != nil {
		return e
	}
	if e = conn.SetDeadline(time.Now().Add(timeout)); e != nil {
		_ = conn.Close()
		return e
	}
	var c *smtp.Client
	if s.Security == "tls" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12})
		if e = tlsConn.Handshake(); e != nil {
			_ = conn.Close()
			return e
		}
		conn = tlsConn
	}
	c, e = smtp.NewClient(conn, s.Host)
	if e != nil {
		_ = conn.Close()
		return e
	}
	defer c.Close()
	if s.Security == "starttls" {
		if e = c.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); e != nil {
			return e
		}
	}
	if s.Username != "" {
		if e = c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); e != nil {
			return e
		}
	}
	if e = c.Mail(s.From); e != nil {
		return e
	}
	if e = c.Rcpt(s.To); e != nil {
		return e
	}
	w, e := c.Data()
	if e != nil {
		return e
	}
	name := s.FromName
	if name == "" {
		name = "Codex Helper"
	}
	boundary := "codex-helper-alternative"
	encodedSubject := "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?="
	msg := fmt.Sprintf("From: %s <%s>\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n--%s\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n--%s\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n--%s--\r\n", name, s.From, s.To, encodedSubject, boundary, boundary, textBody, boundary, htmlBody, boundary)
	if _, e = w.Write([]byte(msg)); e != nil {
		return e
	}
	return w.Close()
}

func (a *App) telegramSettings() TelegramSettings {
	var t TelegramSettings
	a.store.GetJSON("telegram", &t)
	if enc, ok := a.store.Get("telegram_token"); ok {
		if token, err := a.vault.Decrypt(enc); err == nil {
			t.Configured = token != ""
		}
	}
	t.Enabled = t.ChatID != 0
	t.MenuEnabled = t.ChatID != 0
	return t
}
func (a *App) telegramSecret() (TelegramSettings, error) {
	t := a.telegramSettings()
	enc, _ := a.store.Get("telegram_token")
	tok, e := a.vault.Decrypt(enc)
	t.Token = tok
	t.Configured = tok != ""
	return t, e
}
func (a *App) telegramAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		jsonOut(w, 200, a.telegramSettings())
		return
	}
	a.telegramMu.Lock()
	defer a.telegramMu.Unlock()
	if r.Method == http.MethodDelete {
		a.telegramDelete(w)
		return
	}
	var in TelegramSettings
	if decode(r, &in) != nil {
		jsonOut(w, 400, map[string]string{"error": "配置格式错误"})
		return
	}
	old, _ := a.telegramSecret()
	if in.Token == "" {
		in.Token = old.Token
	}
	if in.Token == "" {
		jsonOut(w, 400, map[string]string{"error": "Bot Token 必填"})
		return
	}
	in.Enabled = in.ChatID != 0
	in.MenuEnabled = in.ChatID != 0
	var me struct {
		OK     bool `json:"ok"`
		Result struct {
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"result"`
	}
	if e := tgCall(in.Token, "getMe", map[string]any{}, &me); e != nil || !me.OK {
		jsonOut(w, 502, map[string]string{"error": "无法验证 Bot Token"})
		return
	}
	in.BotName = me.Result.FirstName + " @" + me.Result.Username
	enc, e := a.vault.Encrypt(in.Token)
	if e != nil {
		jsonOut(w, 500, map[string]string{"error": e.Error()})
		return
	}
	safe := in
	safe.Token = ""
	safe.Configured = true
	settingsJSON, e := json.Marshal(safe)
	if e == nil {
		e = a.store.SaveTelegram(string(settingsJSON), enc, old.Token != "" && old.Token != in.Token)
	}
	if e != nil {
		jsonOut(w, 500, map[string]string{"error": e.Error()})
		return
	}
	response := TelegramSettingsResponse{TelegramSettings: safe}
	if safe.ChatID != 0 && old.MenuEnabled != safe.MenuEnabled {
		message := "查询菜单已关闭。"
		if safe.MenuEnabled {
			message = "查询菜单已启用。"
		}
		if e = tgSend(safeWithToken(safe, in.Token), message); e != nil {
			response.Warning = "设置已保存，但 Telegram 菜单同步失败，请稍后发送测试消息重试"
		}
	}
	jsonOut(w, 200, response)
}
func safeWithToken(t TelegramSettings, token string) TelegramSettings {
	t.Token = token
	return t
}
func (a *App) telegramDelete(w http.ResponseWriter) {
	old, _ := a.telegramSecret()
	if e := a.store.DeleteTelegram(); e != nil {
		jsonOut(w, 500, map[string]string{"error": e.Error()})
		return
	}
	response := map[string]any{"ok": true}
	if old.Token != "" && old.ChatID != 0 {
		old.MenuEnabled = false
		if e := tgSend(old, "Codex Helper 已解除 Telegram Bot 绑定。"); e != nil {
			response["warning"] = "配置已删除，但未能从 Telegram 会话移除旧菜单"
		}
	}
	jsonOut(w, 200, response)
}
func (a *App) telegramTest(w http.ResponseWriter, r *http.Request) {
	t, e := a.telegramSecret()
	if e == nil {
		if t.ChatID == 0 {
			e = fmt.Errorf("请先绑定 Chat ID")
		} else {
			e = tgSend(t, "✅ <b>Telegram 测试成功</b>\n\n后续额度重置提醒将发送到此会话。")
		}
	}
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": e.Error()})
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}

var tgCall = telegramCall

func telegramCall(token, method string, p any, out any) error {
	b, _ := json.Marshal(p)
	req, e := http.NewRequestWithContext(context.Background(), "POST", "https://api.telegram.org/bot"+token+"/"+method, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 35 * time.Second}
	resp, e := c.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("Telegram HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
func tgSend(t TelegramSettings, text string) error {
	var out any
	params := map[string]any{"chat_id": t.ChatID, "text": text, "parse_mode": "HTML"}
	if t.MenuEnabled {
		params["reply_markup"] = map[string]any{"keyboard": [][]map[string]string{{{"text": "当前用量"}, {"text": "重置时间"}}, {{"text": "历史概览"}, {"text": "账户信息"}}, {{"text": "立即刷新"}}}, "resize_keyboard": true}
	} else {
		params["reply_markup"] = map[string]any{"remove_keyboard": true}
	}
	return tgCall(t.Token, "sendMessage", params, &out)
}
func (a *App) syncLegacyTelegramMenu() {
	a.telegramMu.Lock()
	defer a.telegramMu.Unlock()
	var t TelegramSettings
	if !a.store.GetJSON("telegram", &t) || t.ChatID == 0 || t.MenuEnabled {
		return
	}
	enc, ok := a.store.Get("telegram_token")
	if !ok {
		return
	}
	token, err := a.vault.Decrypt(enc)
	if err != nil || token == "" {
		return
	}
	t.Token = token
	t.Configured = true
	t.Enabled = true
	t.MenuEnabled = true
	if tgSend(t, "✅ <b>Codex 查询菜单已自动启用</b>\n\n现在可以使用菜单查询额度信息。") != nil {
		return
	}
	t.Token = ""
	_ = a.store.SetJSON("telegram", t)
}
func (a *App) telegramLoop() {
	a.syncLegacyTelegramMenu()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
		t, e := a.telegramSecret()
		if e != nil || !t.Configured {
			continue
		}
		var offset int64
		_ = a.store.DB.QueryRow("SELECT offset FROM telegram_updates WHERE id=1").Scan(&offset)
		var out struct {
			OK     bool `json:"ok"`
			Result []struct {
				UpdateID int64 `json:"update_id"`
				Message  *struct {
					Chat struct {
						ID int64 `json:"id"`
					} `json:"chat"`
					Text string `json:"text"`
				} `json:"message"`
			} `json:"result"`
		}
		if tgCall(t.Token, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message"}}, &out) != nil {
			continue
		}
		for _, u := range out.Result {
			offset = u.UpdateID + 1
			if u.Message != nil {
				current, currentErr := a.telegramSecret()
				if currentErr == nil && current.Configured && current.Token == t.Token {
					a.handleTG(current, u.Message.Chat.ID, u.Message.Text)
				}
			}
		}
		current, currentErr := a.telegramSecret()
		if currentErr == nil && current.Configured && current.Token == t.Token {
			_, _ = a.store.DB.Exec("UPDATE telegram_updates SET offset=? WHERE id=1", offset)
		}
	}
}
func (a *App) handleTG(t TelegramSettings, chat int64, text string) {
	if strings.HasPrefix(text, "/bind ") {
		a.telegramMu.Lock()
		defer a.telegramMu.Unlock()
		current, e := a.telegramSecret()
		if e != nil || !current.Configured || current.Token != t.Token {
			return
		}
		t = current
		var b struct {
			Code    string `json:"code"`
			Expires int64  `json:"expires"`
		}
		if a.store.GetJSON("telegram_bind", &b) && b.Expires > time.Now().Unix() && strings.TrimSpace(strings.TrimPrefix(text, "/bind ")) == b.Code {
			t.ChatID = chat
			t.Enabled = true
			t.MenuEnabled = true
			safe := t
			safe.Token = ""
			_ = a.store.SetJSON("telegram", safe)
			_ = a.store.Set("telegram_bind", "{}")
			t.ChatID = chat
			_ = tgSend(t, "✅ <b>绑定成功</b>\n\n额度提醒和 Codex 额度查询菜单已启用。")
		}
		return
	}
	if chat != t.ChatID {
		return
	}
	if !t.MenuEnabled {
		return
	}
	if text == "立即刷新" || text == "/refresh" {
		a.syncAll(context.Background())
	}
	msg := ""
	a.mu.RLock()
	ds := make([]Dashboard, 0, len(a.runtimes))
	for _, rt := range a.runtimes {
		rt.syncing.Lock()
		ds = append(ds, rt.dash)
		rt.syncing.Unlock()
	}
	a.mu.RUnlock()
	switch text {
	case "重置时间", "/reset":
		msg = "⏰ <b>Codex 重置时间</b>\n"
		for _, d := range ds {
			msg += "\n<b>" + html.EscapeString(d.DisplayName) + "</b>\n"
			for _, x := range d.Limits {
				msg += fmt.Sprintf("• %s\n  <b>%s</b>\n  %s\n", limitLabel(x.WindowDurationMinutes), formatTime(x.ResetsAt, a.general().Timezone), relativeTime(x.ResetsAt, time.Now()))
			}
		}
	case "账户信息", "/account":
		msg = "👤 <b>Codex 账户信息</b>\n"
		for _, d := range ds {
			status := "断开"
			if d.Account.Connected {
				status = "正常"
			}
			msg += fmt.Sprintf("\n<b>%s</b>\n连接：%s\n", html.EscapeString(d.DisplayName), status)
			if d.Account.Email != nil {
				msg += "账户：" + html.EscapeString(*d.Account.Email) + "\n"
			}
			if d.Account.PlanType != nil {
				msg += "套餐：" + html.EscapeString(*d.Account.PlanType) + "\n"
			}
		}
	case "历史概览", "/usage":
		msg = "📈 <b>Codex 历史概览</b>\n"
		for _, d := range ds {
			msg += fmt.Sprintf("\n<b>%s</b>\n累计 Tokens：%s\n历史天数：%d 天\n", html.EscapeString(d.DisplayName), num(d.Summary.LifetimeTokens), len(d.Usage))
		}
	default:
		msg = "📊 <b>Codex 当前用量</b>\n"
		for _, d := range ds {
			msg += "\n<b>" + html.EscapeString(d.DisplayName) + "</b>\n"
			for _, x := range d.Limits {
				msg += fmt.Sprintf("• %s：<b>剩余 %.1f%%</b>\n  重置：%s（%s）\n", limitLabel(x.WindowDurationMinutes), 100-x.UsedPercent, formatTime(x.ResetsAt, a.general().Timezone), relativeTime(x.ResetsAt, time.Now()))
			}
		}
	}
	_ = tgSend(t, msg)
}
func num(n *int64) string {
	if n == nil {
		return "暂无"
	}
	return fmt.Sprintf("%d", *n)
}
func (a *App) processReminders() {
	a.reminderMu.Lock()
	g := a.general()
	a.mu.RLock()
	ds := make([]Dashboard, 0, len(a.runtimes))
	for _, rt := range a.runtimes {
		rt.syncing.Lock()
		ds = append(ds, rt.dash)
		rt.syncing.Unlock()
	}
	a.mu.RUnlock()
	now := time.Now()
	for _, d := range ds {
		for _, x := range d.Limits {
			if !g.NotifyBefore || x.ResetsAt <= now.Unix() {
				continue
			}
			at := time.Unix(x.ResetsAt, 0).Add(-time.Duration(g.BeforeMinutes) * time.Minute)
			if now.Before(at) || now.Sub(at) > 6*time.Hour {
				continue
			}
			key := fmt.Sprintf("%d:%s:%s:%d:before", d.AccountID, x.LimitID, x.WindowType, x.ResetsAt)
			event := notificationEvent{Version: 1, Kind: "before", Account: d.DisplayName, DurationMins: x.WindowDurationMinutes, Remaining: 100 - x.UsedPercent, Used: x.UsedPercent, ResetsAt: x.ResetsAt}
			body, _ := json.Marshal(event)
			_, _ = a.store.DB.Exec(`INSERT OR IGNORE INTO notifications
					(dedupe_key,channel,kind,status,attempts,last_error,scheduled_at,sent_at,body)
					VALUES(?,?,'before','pending',0,'',?,NULL,?)`, key, "configured", at.Unix(), string(body))
		}
	}
	a.reminderMu.Unlock()

	a.reminderSendMu.Lock()
	defer a.reminderSendMu.Unlock()
	a.sendPendingReminders(now)
}

func (a *App) sendPendingReminders(now time.Time) {
	type pendingReminder struct {
		key  string
		body string
	}
	rows, err := a.store.DB.Query(`SELECT dedupe_key,body FROM notifications
		WHERE status IN ('pending','failed') AND scheduled_at<=? AND scheduled_at>=? ORDER BY scheduled_at`, now.Unix(), now.Add(-6*time.Hour).Unix())
	if err != nil {
		return
	}
	pending := []pendingReminder{}
	for rows.Next() {
		var p pendingReminder
		if rows.Scan(&p.key, &p.body) == nil && p.body != "" {
			pending = append(pending, p)
		}
	}
	_ = rows.Close()
	for _, p := range pending {
		event, structured := decodeNotification(p.body)
		if structured && ((event.Kind == "before" && event.ResetsAt <= now.Unix()) ||
			((event.Kind == "after" || event.Kind == "detected_after") && !event.Confirmed)) {
			_, _ = a.store.DB.Exec(`UPDATE notifications SET status='expired',last_error='' WHERE dedupe_key=?`, p.key)
			continue
		}
		textBody, telegramBody, subject, htmlBody := renderNotification(event, p.body, a.general().Timezone, now)
		ok := true
		errs := []string{}
		if t, e := a.telegramSecret(); e == nil && t.Enabled && t.ChatID != 0 {
			if e = tgSend(t, telegramBody); e != nil {
				ok = false
				errs = append(errs, e.Error())
			}
		}
		if s, e := a.smtpSecret(); e == nil && s.Enabled {
			if !structured {
				subject = "Codex 额度重置提醒"
			}
			if e = sendSMTP(s, subject, textBody, htmlBody); e != nil {
				ok = false
				errs = append(errs, e.Error())
			}
		}
		status := "sent"
		var sent any = now.Unix()
		if !ok {
			status = "failed"
			sent = nil
		}
		_, _ = a.store.DB.Exec(`UPDATE notifications SET status=?,attempts=attempts+1,last_error=?,sent_at=? WHERE dedupe_key=?`,
			status, strings.Join(errs, "; "), sent, p.key)
	}
}

func decodeNotification(body string) (notificationEvent, bool) {
	var event notificationEvent
	err := json.Unmarshal([]byte(body), &event)
	return event, err == nil && event.Version == 1
}

func limitLabel(minutes int) string {
	switch {
	case minutes <= 0:
		return "Codex 额度"
	case minutes%1440 == 0:
		return fmt.Sprintf("%d 天额度", minutes/1440)
	case minutes%60 == 0:
		return fmt.Sprintf("%d 小时额度", minutes/60)
	default:
		return fmt.Sprintf("%d 分钟额度", minutes)
	}
}

func location(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

var weekdays = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

func formatTime(ts int64, zone string) string {
	t := time.Unix(ts, 0).In(location(zone))
	date := fmt.Sprintf("%d月%d日 %s %02d:%02d", t.Month(), t.Day(), weekdays[t.Weekday()], t.Hour(), t.Minute())
	if t.Year() != time.Now().In(t.Location()).Year() {
		return fmt.Sprintf("%d年%s", t.Year(), date)
	}
	return date
}

func relativeTime(ts int64, now time.Time) string {
	d := time.Unix(ts, 0).Sub(now)
	if d <= 0 {
		if d > -time.Minute {
			return "刚刚重置"
		}
		return "已重置"
	}
	minutes := int(d.Round(time.Minute) / time.Minute)
	if minutes < 1 {
		return "不到 1 分钟后"
	}
	days, hours, mins := minutes/1440, minutes%1440/60, minutes%60
	if days > 0 {
		return fmt.Sprintf("还有 %d 天 %d 小时", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("还有 %d 小时 %d 分", hours, mins)
	}
	return fmt.Sprintf("还有 %d 分钟", mins)
}

func renderNotification(event notificationEvent, legacy, zone string, now time.Time) (string, string, string, string) {
	if event.Version != 1 {
		escaped := html.EscapeString(legacy)
		return legacy, escaped, "Codex 额度重置提醒", emailCard("额度重置提醒", escaped, "", zone, "#2563eb")
	}
	after := event.Kind == "after" || event.Kind == "detected_after"
	title, icon, color := "Codex 即将重置", "⏰", "#2563eb"
	detail := fmt.Sprintf("剩余 %.1f%%", event.Remaining)
	if after {
		title, icon, color = "Codex 额度已重置", "✅", "#059669"
		detail = fmt.Sprintf("当前额度剩余 %.1f%%", event.Remaining)
	}
	when := formatTime(event.ResetsAt, zone)
	relative := relativeTime(event.ResetsAt, now)
	label := limitLabel(event.DurationMins)
	plain := fmt.Sprintf("%s\n\n账号：%s\n额度：%s\n%s\n下次重置：%s\n%s\n时区：%s", title, event.Account, label, detail, when, relative, zone)
	tg := fmt.Sprintf("%s <b>%s</b>\n\n<b>%s</b>\n%s：<b>%s</b>\n\n下次重置\n<b>%s</b>\n%s", icon, title, html.EscapeString(event.Account), label, detail, when, relative)
	content := fmt.Sprintf("<div style=\"font-size:14px;color:#64748b;margin-bottom:8px\">%s · %s</div><div style=\"font-size:24px;font-weight:700;color:#0f172a\">%s</div>", html.EscapeString(event.Account), label, detail)
	return plain, tg, title, emailCard(title, content, when+" · "+relative, zone, color)
}

func emailCard(title, content, prominent, zone, color string) string {
	return fmt.Sprintf(`<!doctype html><html><body style="margin:0;background:#f1f5f9;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;color:#0f172a"><div style="max-width:560px;margin:36px auto;padding:0 16px"><div style="background:#fff;border-radius:16px;overflow:hidden;box-shadow:0 8px 30px rgba(15,23,42,.08)"><div style="height:6px;background:%s"></div><div style="padding:30px"><div style="font-size:22px;font-weight:700;margin-bottom:24px">%s</div>%s<div style="margin-top:24px;padding:18px;border-radius:12px;background:#f8fafc"><div style="font-size:12px;color:#64748b;margin-bottom:6px">下次重置时间</div><div style="font-size:20px;font-weight:700;color:%s">%s</div></div></div><div style="padding:16px 30px;background:#f8fafc;font-size:12px;color:#94a3b8">Codex Helper · 时区 %s</div></div></div></body></html>`, color, html.EscapeString(title), content, color, html.EscapeString(prominent), html.EscapeString(zone))
}

func testEmailHTML(zone string) string {
	return emailCard("✅ SMTP 测试成功", "<div style=\"color:#475569\">邮件通知已配置完成，后续额度重置提醒将发送到此邮箱。</div>", "配置正常", zone, "#059669")
}

var _ = bufio.ErrInvalidUnreadByte
