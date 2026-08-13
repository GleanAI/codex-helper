package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

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
		e = sendSMTP(s, "Codex Helper 测试邮件", "SMTP 配置成功，后续用量重置提醒将发送到此邮箱。")
	}
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": e.Error()})
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func sendSMTP(s SMTPSettings, subject, body string) error {
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	var c *smtp.Client
	var e error
	if s.Security == "tls" {
		conn, x := tls.Dial("tcp", addr, &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12})
		if x != nil {
			return x
		}
		c, e = smtp.NewClient(conn, s.Host)
	} else {
		c, e = smtp.Dial(addr)
	}
	if e != nil {
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
	msg := fmt.Sprintf("From: %s <%s>\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s", name, s.From, s.To, subject, body)
	if _, e = w.Write([]byte(msg)); e != nil {
		return e
	}
	return w.Close()
}

func (a *App) telegramSettings() TelegramSettings {
	var t TelegramSettings
	a.store.GetJSON("telegram", &t)
	if _, ok := a.store.Get("telegram_token"); ok {
		t.Configured = true
	}
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
	enc, _ := a.vault.Encrypt(in.Token)
	safe := in
	safe.Token = ""
	safe.Configured = true
	_ = a.store.Set("telegram_token", enc)
	_ = a.store.SetJSON("telegram", safe)
	jsonOut(w, 200, safe)
}
func (a *App) telegramTest(w http.ResponseWriter, r *http.Request) {
	t, e := a.telegramSecret()
	if e == nil {
		if t.ChatID == 0 {
			e = fmt.Errorf("请先绑定 Chat ID")
		} else {
			e = tgSend(t, "Codex Helper Telegram 配置成功。")
		}
	}
	if e != nil {
		jsonOut(w, 502, map[string]string{"error": e.Error()})
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func tgCall(token, method string, p any, out any) error {
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
	params := map[string]any{"chat_id": t.ChatID, "text": text}
	if t.MenuEnabled {
		params["reply_markup"] = map[string]any{"keyboard": [][]map[string]string{{{"text": "当前用量"}, {"text": "重置时间"}}, {{"text": "历史概览"}, {"text": "账户信息"}}, {{"text": "立即刷新"}}}, "resize_keyboard": true}
	}
	return tgCall(t.Token, "sendMessage", params, &out)
}
func (a *App) telegramLoop() {
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
				a.handleTG(t, u.Message.Chat.ID, u.Message.Text)
			}
		}
		_, _ = a.store.DB.Exec("UPDATE telegram_updates SET offset=? WHERE id=1", offset)
	}
}
func (a *App) handleTG(t TelegramSettings, chat int64, text string) {
	if strings.HasPrefix(text, "/bind ") {
		var b struct {
			Code    string `json:"code"`
			Expires int64  `json:"expires"`
		}
		if a.store.GetJSON("telegram_bind", &b) && b.Expires > time.Now().Unix() && strings.TrimSpace(strings.TrimPrefix(text, "/bind ")) == b.Code {
			t.ChatID = chat
			safe := t
			safe.Token = ""
			_ = a.store.SetJSON("telegram", safe)
			_ = a.store.Set("telegram_bind", "{}")
			t.ChatID = chat
			message := "绑定成功。Codex 用量提醒将发送到此会话。"
			if t.MenuEnabled {
				message = "绑定成功。现在可以使用菜单查询 Codex 用量。"
			}
			_ = tgSend(t, message)
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
		_ = a.sync(context.Background())
	}
	a.mu.RLock()
	d := a.dash
	a.mu.RUnlock()
	msg := "Codex 用量\n"
	switch text {
	case "重置时间", "/reset":
		for _, x := range d.Limits {
			msg += fmt.Sprintf("%s/%s：%s\n", x.LimitID, x.WindowType, time.Unix(x.ResetsAt, 0).Format(time.RFC3339))
		}
	case "账户信息", "/account":
		msg += fmt.Sprintf("连接：%v\n", d.Account.Connected)
		if d.Account.Email != nil {
			msg += "账户：" + *d.Account.Email + "\n"
		}
		if d.Account.PlanType != nil {
			msg += "套餐：" + *d.Account.PlanType
		}
	case "历史概览", "/usage":
		msg += fmt.Sprintf("Lifetime tokens：%s\n历史天数：%d", num(d.Summary.LifetimeTokens), len(d.Usage))
	default:
		for _, x := range d.Limits {
			msg += fmt.Sprintf("%s/%s：%.1f%%，重置 %s\n", x.LimitID, x.WindowType, x.UsedPercent, time.Unix(x.ResetsAt, 0).Format(time.RFC3339))
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
	g := a.general()
	a.mu.RLock()
	d := a.dash
	a.mu.RUnlock()
	now := time.Now()
	for _, x := range d.Limits {
		for _, kind := range []string{"before", "after"} {
			if kind == "before" && !g.NotifyBefore {
				continue
			}
			if kind == "after" && !g.NotifyAfter {
				continue
			}
			at := time.Unix(x.ResetsAt, 0)
			if kind == "before" {
				at = at.Add(-time.Duration(g.BeforeMinutes) * time.Minute)
			}
			if now.Before(at) || now.Sub(at) > 6*time.Hour {
				continue
			}
			key := fmt.Sprintf("%s:%s:%d:%s", x.LimitID, x.WindowType, x.ResetsAt, kind)
			var exists int
			if a.store.DB.QueryRow("SELECT 1 FROM notifications WHERE dedupe_key=? AND status='sent'", key).Scan(&exists) == nil {
				continue
			}
			body := fmt.Sprintf("Codex %s/%s 当前用量 %.1f%%，重置时间 %s。", x.LimitID, x.WindowType, x.UsedPercent, time.Unix(x.ResetsAt, 0).Format(time.RFC3339))
			ok := true
			errs := []string{}
			if t, e := a.telegramSecret(); e == nil && t.Enabled && t.ChatID != 0 {
				if e = tgSend(t, body); e != nil {
					ok = false
					errs = append(errs, e.Error())
				}
			}
			if s, e := a.smtpSecret(); e == nil && s.Enabled {
				if e = sendSMTP(s, "Codex 用量重置提醒", body); e != nil {
					ok = false
					errs = append(errs, e.Error())
				}
			}
			status := "sent"
			var sent any = time.Now().Unix()
			if !ok {
				status = "failed"
				sent = nil
			}
			_, _ = a.store.DB.Exec(`INSERT INTO notifications(dedupe_key,channel,kind,status,attempts,last_error,scheduled_at,sent_at)
				VALUES(?,?,?,?,?,?,?,?)
				ON CONFLICT(dedupe_key) DO UPDATE SET
				status=excluded.status,
				attempts=notifications.attempts+1,
				last_error=excluded.last_error,
				sent_at=excluded.sent_at`, key, "configured", kind, status, 1, strings.Join(errs, "; "), at.Unix(), sent)
		}
	}
}

var _ = bufio.ErrInvalidUnreadByte
