package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Health is local runtime state. Tokens and raw Telegram error descriptions
// never enter it, the public admin snapshot, or a customer response.
type TelegramHealth struct {
	Username      string `json:"username"`
	LastPoll      int64  `json:"lastPoll"`
	LastUpdate    int64  `json:"lastUpdate"`
	LastDelivery  int64  `json:"lastDelivery"`
	PollError     string `json:"pollError"`
	DeliveryError string `json:"deliveryError"`
}

func (a *App) recordTelegramPoll(err error) {
	a.advancedMu.Lock()
	defer a.advancedMu.Unlock()
	if err != nil {
		a.telegramHealth.PollError = err.Error()
		return
	}
	a.telegramHealth.LastPoll = time.Now().UnixMilli()
	a.telegramHealth.PollError = ""
}
func (a *App) recordTelegramDelivery(err error) {
	a.advancedMu.Lock()
	defer a.advancedMu.Unlock()
	if err != nil {
		a.telegramHealth.DeliveryError = err.Error()
		return
	}
	a.telegramHealth.LastDelivery = time.Now().UnixMilli()
	a.telegramHealth.DeliveryError = ""
}
func (a *App) telegramPublicStatus() map[string]any {
	t := a.telegramSnapshot()
	a.advancedMu.RLock()
	h := a.telegramHealth
	a.advancedMu.RUnlock()
	s := a.salesSnapshot()
	plans, notices := 0, 0
	for _, p := range s.Plans {
		if p.Enabled {
			plans++
		}
	}
	for _, n := range s.Notices {
		if !n.Sent {
			notices++
		}
	}
	mode := t.Mode
	if mode == "" {
		mode = "poll"
	}
	return map[string]any{
		"enabled": t.Enabled, "notify": t.Notify, "chatId": t.ChatID,
		"tokenConfigured": t.Token != "", "mode": mode, "health": h,
		"shopEnabled": s.Settings.Enabled, "activePlans": plans, "pendingNotices": notices,
		"purchaseConfigured": t.Enabled && t.Token != "" && t.ChatID > 0 && s.Settings.Enabled && plans > 0,
		"commands":           []string{"/start", "/plans", "/services", "/wallet", "/shop", "/support", "/status", "/approve"},
	}
}

// Keep delivery deduplication and polling offset from the latest config, not
// the older snapshot that was read before an API request or form edit.
func (a *App) storeTelegramSettings(in TelegramConfig, polling bool) error {
	a.advancedMu.Lock()
	defer a.advancedMu.Unlock()
	old := a.telegram
	if in.Token == "" {
		in.Token = old.Token
	}
	if !validTelegram(in) {
		return fmt.Errorf("توکن و شناسهٔ مدیر معتبر نیست")
	}
	if old.Token == in.Token {
		in.Offset, in.Seen = old.Offset, append([]int64{}, old.Seen...)
		in.Mode, in.WebhookSecret = old.Mode, old.WebhookSecret
	} else {
		in.Offset, in.Seen, in.Mode, in.WebhookSecret = 0, nil, "poll", ""
	}
	if polling || in.Mode == "" {
		in.Mode, in.WebhookSecret = "poll", ""
	}
	if err := atomicJSON(a.cfg.DataDir, "aria-telegram.json", in); err != nil {
		return err
	}
	if in.Token != old.Token {
		a.telegramHealth = TelegramHealth{}
	}
	a.telegram = in
	return nil
}

type tgIdentity struct {
	Username string `json:"username"`
	IsBot    bool   `json:"is_bot"`
}
type tgWebhookInfo struct {
	URL     string `json:"url"`
	Pending int    `json:"pending_update_count"`
}

func (a *App) inspectTelegram(ctx context.Context, t TelegramConfig) (tgIdentity, tgWebhookInfo, error) {
	var me tgIdentity
	var hook tgWebhookInfo
	if err := a.tgCall(ctx, t, "getMe", map[string]any{}, &me); err != nil {
		return me, hook, err
	}
	if me.Username == "" || !me.IsBot {
		return me, hook, fmt.Errorf("توکن مربوط به یک ربات معتبر نیست")
	}
	if err := a.tgCall(ctx, t, "getWebhookInfo", map[string]any{}, &hook); err != nil {
		return me, hook, err
	}
	a.advancedMu.Lock()
	if a.telegram.Token == t.Token {
		a.telegramHealth.Username = me.Username
	}
	a.advancedMu.Unlock()
	return me, hook, nil
}

// This does not send customer messages or consume updates. Replacing a
// previously registered webhook is a separate, explicit UI choice.
func (a *App) activateTelegram(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token          string `json:"token"`
		ChatID         int64  `json:"chatId"`
		Notify         bool   `json:"notify"`
		ReplaceWebhook bool   `json:"replaceWebhook"`
	}
	if readBody(w, r, &in, 4096) != nil {
		apiError(w, 400, "فرم اتصال معتبر نیست")
		return
	}
	a.mutation.Lock()
	defer a.mutation.Unlock()
	old := a.telegramSnapshot()
	t := TelegramConfig{Token: strings.TrimSpace(in.Token), ChatID: in.ChatID, Notify: in.Notify, Enabled: true}
	if t.Token == "" {
		t.Token = old.Token
	}
	if !validTelegram(t) {
		apiError(w, 400, "توکن BotFather و Chat ID عددی مثبت مدیر را وارد کن")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	me, hook, err := a.inspectTelegram(ctx, t)
	if err != nil {
		apiError(w, 502, err.Error())
		return
	}
	if hook.URL != "" && !in.ReplaceWebhook {
		jsonReply(w, 409, map[string]any{"error": "این توکن Webhook فعال دارد؛ برای اختصاص آن به همین پنل، جایگزینی اتصال قبلی را تأیید کن.", "code": "webhook_active"})
		return
	}
	if hook.URL != "" {
		if err := a.tgCall(ctx, t, "deleteWebhook", map[string]bool{"drop_pending_updates": false}, nil); err != nil {
			apiError(w, 502, err.Error())
			return
		}
	}
	if err := a.storeTelegramSettings(t, true); err != nil {
		apiError(w, 500, "اتصال ذخیره نشد؛ دسترسی پوشهٔ داده را بررسی کن")
		return
	}
	if err := a.salesTxn(func(s *SalesData) error { s.Settings.Enabled = true; return nil }); err != nil {
		apiError(w, 500, "ربات فعال شد، اما فروشگاه ذخیره نشد؛ دوباره فعال‌سازی خرید را بزن")
		return
	}
	a.advancedMu.Lock()
	a.telegramHealth.Username = me.Username
	a.advancedMu.Unlock()
	a.audit("خرید مستقیم در ربات تلگرام فعال شد", "settings")
	_ = a.persist()
	jsonReply(w, 200, a.runtimeSnapshot())
}

func (a *App) telegramDiagnostics(w http.ResponseWriter, r *http.Request) {
	t := a.telegramSnapshot()
	checks := []map[string]any{}
	add := func(key, label string, ok bool, detail string) {
		checks = append(checks, map[string]any{"key": key, "label": label, "ok": ok, "detail": detail})
	}
	add("token", "توکن ذخیره‌شده", t.Token != "", "توکن فقط در تنظیمات خصوصی نگه‌داری می‌شود.")
	add("admin", "شناسهٔ مدیر", t.ChatID > 0, "Chat ID مثبت حساب شخصی مدیر لازم است.")
	add("enabled", "دریافت پیام ربات", t.Enabled, "برای پاسخ‌دادن به /start، ربات باید فعال باشد.")
	s := a.salesSnapshot()
	add("shop", "فروش در ربات", s.Settings.Enabled, "فروشگاه و فروش ربات از یک تنظیم استفاده می‌کنند.")
	plans := 0
	for _, p := range s.Plans {
		if p.Enabled {
			plans++
		}
	}
	add("plans", "پلن فعال", plans > 0, fmt.Sprintf("%d پلن فعال برای انتخاب مشتری وجود دارد.", plans))
	if t.Token != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		me, hook, err := a.inspectTelegram(ctx, t)
		if err != nil {
			add("api", "ارتباط با تلگرام", false, err.Error())
		} else {
			add("api", "ارتباط با تلگرام", true, "ربات @"+me.Username)
			ownURL := strings.TrimRight(a.snapshot().Settings.PublicURL, "/") + "/telegram/webhook"
			ok := (t.Mode == "webhook" && hook.URL == ownURL) || (t.Mode != "webhook" && hook.URL == "")
			add("transport", "تطبیق حالت اتصال", ok, "در حالت Polling نباید Webhook دیگری فعال باشد.")
		}
	}
	ready := true
	for _, c := range checks {
		if !c["ok"].(bool) {
			ready = false
		}
	}
	jsonReply(w, 200, map[string]any{"ready": ready, "checks": checks, "status": a.telegramPublicStatus()})
}
