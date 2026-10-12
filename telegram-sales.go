package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *App) queueBot(id string, chat int64, text string) error {
	return a.salesTxn(func(s *SalesData) error { addNotice(s, "tg:"+id, chat, text); return nil })
}
func (a *App) flushSalesNotices(ctx context.Context) {
	if !a.noticeMu.TryLock() {
		return
	}
	defer a.noticeMu.Unlock()
	t := a.telegramSnapshot()
	if !t.Enabled || t.Token == "" {
		return
	}
	sent := 0
	for _, n := range a.salesSnapshot().Notices {
		if n.Sent || n.ChatID <= 0 {
			continue
		}
		delay := time.Duration(1<<min(n.Attempts, 8)) * time.Second
		if time.Now().UnixMilli()-n.LastAttempt < delay.Milliseconds() {
			continue
		}
		target := t
		target.ChatID = n.ChatID
		err := a.tgRichSend(ctx, target, n)
		a.recordTelegramDelivery(err)
		_ = a.salesTxn(func(s *SalesData) error {
			for i := range s.Notices {
				v := &s.Notices[i]
				if v.ID == n.ID {
					v.Attempts++
					v.LastAttempt = time.Now().UnixMilli()
					v.Sent = err == nil
					break
				}
			}
			return nil
		})
		sent++
		if sent >= 10 || ctx.Err() != nil {
			break
		}
	}
	// Keep recent sent IDs for deduplication while retaining every unsent notice.
	_ = a.salesTxn(func(s *SalesData) error {
		sentCount := 0
		for _, n := range s.Notices {
			if n.Sent {
				sentCount++
			}
		}
		if sentCount <= 2000 {
			return nil
		}
		out := []SalesNotice{}
		for _, n := range s.Notices {
			if n.Sent && sentCount > 2000 {
				sentCount--
				continue
			}
			out = append(out, n)
		}
		s.Notices = out
		return nil
	})
}
func (a *App) processUpdate(ctx context.Context, t TelegramConfig, u tgUpdate) error {
	a.telegramDispatchMu.Lock()
	defer a.telegramDispatchMu.Unlock()
	current := a.telegramSnapshot()
	if !current.Enabled || current.Token != t.Token {
		return nil
	}
	for _, id := range current.Seen {
		if id == u.ID {
			return nil
		}
	}
	if err := a.dispatchUpdate(ctx, current, u); err != nil {
		return err
	}
	a.advancedMu.Lock()
	defer a.advancedMu.Unlock()
	next := a.telegram
	if next.Token != t.Token {
		return nil
	}
	next.Seen = append(next.Seen, u.ID)
	if len(next.Seen) > 2000 {
		next.Seen = next.Seen[len(next.Seen)-2000:]
	}
	if next.Mode != "webhook" && u.ID >= next.Offset {
		next.Offset = u.ID + 1
	}
	if err := atomicJSON(a.cfg.DataDir, "aria-telegram.json", next); err != nil {
		return err
	}
	a.telegram = next
	a.telegramHealth.LastUpdate = time.Now().UnixMilli()
	return nil
}
func (a *App) dispatchUpdate(ctx context.Context, t TelegramConfig, u tgUpdate) error {
	if u.Checkout != nil {
		v := u.Checkout
		s := a.salesSnapshot()
		i := orderIndex(&s, v.Payload)
		ci := customerIndex(&s, "tg-"+strconv.FormatInt(v.From.ID, 10))
		ok := t.Enabled && s.Settings.Enabled && !v.From.Bot && ci >= 0 && !s.Customers[ci].Blocked && i >= 0
		if ok {
			o := s.Orders[i]
			ok = o.CustomerID == s.Customers[ci].ID && o.Status == "pending" && o.Method == "stars" && o.Currency == v.Currency && o.Amount == v.Amount
		}
		payload := map[string]any{"pre_checkout_query_id": v.ID, "ok": ok}
		if !ok {
			payload["error_message"] = "سفارش فعال و مبلغ مطابق پیدا نشد؛ سفارش تازه بساز."
		}
		return a.tgCall(ctx, t, "answerPreCheckoutQuery", payload, nil)
	}
	m := u.Message
	cmd := strings.TrimSpace(m.Text)
	name := m.From.FirstName
	chat := m.Chat.ID
	user := m.From.ID
	bot := m.From.IsBot
	if cb := u.Callback; cb != nil {
		m = cb.Message
		cmd = cb.Data
		chat = m.Chat.ID
		user = cb.From.ID
		bot = cb.From.Bot
		name = cb.From.Name
		_ = a.tgCall(ctx, t, "answerCallbackQuery", map[string]string{"callback_query_id": cb.ID}, nil)
	}
	if bot || chat <= 0 || chat != user || m.Chat.Type != "private" {
		return nil
	}
	noticeID := strconv.FormatInt(u.ID, 10)
	reply := func(text string) error { return a.queueBot(noticeID, chat, text) }
	s := a.salesSnapshot()
	customerID := "tg-" + strconv.FormatInt(user, 10)
	// successful_payment comes exclusively from authenticated Telegram transport.
	if m.Payment != nil {
		p := m.Payment
		i := orderIndex(&s, p.Payload)
		if i < 0 || s.Orders[i].CustomerID != customerID || p.Currency != "XTR" || p.Charge == "" {
			return reply("پرداخت با سفارش مطابق نیست؛ به پشتیبانی اطلاع بده.")
		}
		if err := a.confirmPayment(p.Payload, p.Charge, p.Amount, "stars"); err != nil {
			return reply(err.Error())
		}
		_, err := a.fulfillOrder(ctx, p.Payload)
		if err != nil {
			return reply("پرداخت ثبت شد و اعتبار محفوظ است؛ تحویل سرویس در حال بازیابی است.")
		}
		return nil
	}
	// A new callback can reference a menu sent days ago. Telegram authenticates
	// the update; ownership, deduplication and expiring purchase nonces still
	// apply. Only ordinary message age is checked here.
	if u.Callback == nil && (m.Date < time.Now().Add(-24*time.Hour).Unix() || m.Date > time.Now().Add(time.Minute).Unix()) {
		return nil
	}
	if user == t.ChatID {
		if handled, err := a.manageBot(ctx, t, u, cmd, noticeID); handled {
			return err
		}
		if handled, err := a.adminBotUI(ctx, t, cmd, noticeID); handled {
			return err
		}
		if strings.HasPrefix(cmd, "/approve ") {
			_, err := a.approveOrder(ctx, strings.TrimSpace(strings.TrimPrefix(cmd, "/approve ")))
			if err != nil {
				return reply(err.Error())
			}
			return reply("✅ سفارش تأیید و تحویل شد.")
		}
		if strings.HasPrefix(cmd, "/reject ") {
			if err := a.cancelOrder("", strings.TrimSpace(strings.TrimPrefix(cmd, "/reject ")), true); err != nil {
				return reply(err.Error())
			}
			return reply("سفارش رد شد.")
		}
		if cmd == "/status" || cmd == "/users" || strings.HasPrefix(cmd, "/sub ") {
			return reply(a.telegramCommand(t, m))
		}
	}
	if strings.HasPrefix(cmd, "ui:adm:") || cmd == "/admin" || cmd == "⚙️ مدیریت پنل" {
		return nil
	}
	if !s.Settings.Enabled {
		if cmd == "/start" || cmd == "/help" || strings.HasPrefix(cmd, "ui:") {
			return reply("فروشگاه فعلاً غیرفعال است. مدیر می‌تواند از بخش ربات تلگرام در پنل، خرید در ربات را فعال کند.")
		}
		return nil
	}
	if !safeLabel(name, 64) {
		name = "مشتری تلگرام"
	}
	c, err := a.createCustomer(name, user)
	if err != nil {
		return err
	}
	if c.Blocked {
		return reply("حساب غیرفعال است؛ با پشتیبانی تماس بگیر.")
	}
	if handled, err := a.customerBotUI(ctx, t, u, c, cmd, noticeID); handled {
		return err
	}
	if len(m.Photo) > 0 {
		order := c.ReceiptOrder
		text := m.Caption
		if strings.HasPrefix(text, "/receipt ") {
			parts := strings.Fields(text)
			if len(parts) >= 2 {
				order = parts[1]
				text = strings.Join(parts[2:], " ")
			}
		}
		if order == "" {
			return reply("اول /receipt شناسه‌سفارش را بفرست، سپس عکس رسید را ارسال کن.")
		}
		if err := a.submitReceipt(c.ID, order, text, "", m.Photo[len(m.Photo)-1].FileID); err != nil {
			return reply(err.Error())
		}
		return reply("🧾 رسید ثبت شد؛ پس از تأیید مدیر، سرویس تحویل می‌شود.")
	}
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}
	command := strings.Split(parts[0], "@")[0]
	switch command {
	case "/start", "/help":
		return reply("🔥 " + s.Settings.Name + "\n/plans خرید سرویس\n/services سرویس‌ها و تمدید\n/wallet کیف پول\n/shop فروشگاه گرافیکی\n/support متن‌پیام\n/receipt شناسه‌سفارش رسید\n\nمدیر: /approve شناسه‌سفارش و /reject شناسه‌سفارش")
	case "/shop":
		link, err := a.portalLink(c.ID)
		if err != nil {
			return err
		}
		return reply("فروشگاه خصوصی شما؛ این لینک یک‌بار مصرف و ۳۰ دقیقه معتبر است:\n" + link)
	case "/plans":
		lines := []string{"پلن‌های فعال · تومان"}
		for _, p := range s.Plans {
			if p.Enabled {
				lines = append(lines, fmt.Sprintf("%s · %.2f GB · %d روز\n%d تومان\n/buy %s manual\n/buy %s wallet", p.Name, p.QuotaGB, p.Days, p.Price, p.ID, p.ID))
				if p.Stars > 0 {
					lines = append(lines, fmt.Sprintf("%d Stars: /buy %s stars", p.Stars, p.ID))
				}
			}
		}
		return reply(strings.Join(lines, "\n\n"))
	case "/services":
		lines := []string{"سرویس‌های شما"}
		for _, v := range s.Services {
			if v.CustomerID == c.ID {
				lines = append(lines, fmt.Sprintf("%s\n%s\n%.2f / %.2f GB\n/renew %s شناسه‌پلن manual", v.Name, v.Subscription, float64(v.Used)/(1<<30), float64(v.Quota)/(1<<30), v.ID))
			}
		}
		return reply(strings.Join(lines, "\n\n"))
	case "/wallet":
		return reply(fmt.Sprintf("کیف پول: %d تومان\nشارژ: /topup مبلغ‌تومان", c.Balance))
	case "/support":
		if len(parts) < 2 {
			return reply("بعد از /support متن پیام را بنویس.")
		}
		if err := a.createTicket(c.ID, strings.Join(parts[1:], " ")); err != nil {
			return reply(err.Error())
		}
		return reply("تیکت پشتیبانی ثبت شد.")
	case "/receipt":
		if len(parts) < 2 {
			return reply("/receipt o-شناسه‌سفارش شماره‌پیگیری\nیا ابتدا شناسه را بفرست، بعد عکس رسید را ارسال کن.")
		}
		if len(parts) > 2 {
			err = a.submitReceipt(c.ID, parts[1], strings.Join(parts[2:], " "), "", "")
			if err != nil {
				return reply(err.Error())
			}
			return reply("رسید برای بررسی مدیر ثبت شد.")
		}
		err = a.salesTxn(func(s *SalesData) error {
			ci := customerIndex(s, c.ID)
			oi := orderIndex(s, parts[1])
			if oi < 0 || ci < 0 || s.Orders[oi].CustomerID != c.ID || s.Orders[oi].Method != "manual" || s.Orders[oi].Status != "pending" && s.Orders[oi].Status != "receipt" {
				return fmt.Errorf("سفارش منتظر رسید پیدا نشد")
			}
			s.Customers[ci].ReceiptOrder = parts[1]
			return nil
		})
		if err != nil {
			return reply(err.Error())
		}
		return reply("حالا عکس رسید همین سفارش را بفرست.")
	case "/cancel":
		if len(parts) < 2 {
			return reply("/cancel شناسه‌سفارش")
		}
		if err := a.cancelOrder(c.ID, parts[1], false); err != nil {
			return reply(err.Error())
		}
		return reply("سفارش لغو شد.")
	case "/buy", "/renew", "/topup":
		req := BuyRequest{CustomerID: c.ID, Kind: "buy", Method: "manual", Idempotency: "tg-" + noticeID}
		if command == "/topup" {
			if len(parts) != 2 {
				return reply("/topup مبلغ‌تومان")
			}
			req.Kind = "topup"
			req.Amount, _ = strconv.ParseInt(parts[1], 10, 64)
		} else if command == "/renew" {
			if len(parts) < 3 {
				return reply("/renew شناسه‌سرویس شناسه‌پلن manual یا wallet")
			}
			req.Kind = "renew"
			req.ServiceID = parts[1]
			req.PlanID = parts[2]
			if len(parts) > 3 {
				req.Method = parts[3]
			}
		} else {
			if len(parts) < 2 {
				return reply("/buy شناسه‌پلن manual یا wallet یا stars")
			}
			req.PlanID = parts[1]
			if len(parts) > 2 {
				req.Method = parts[2]
			}
		}
		o, err := a.newOrder(req)
		if err != nil {
			return reply(err.Error())
		}
		if gatewayMethod(o.Method) {
			return a.botOrder(ctx, o.ID, c, noticeID)
		}
		if o.Method == "stars" {
			return a.sendStarsInvoice(ctx, t, chat, o)
		}
		if o.Status == "paid" {
			_, err = a.fulfillOrder(ctx, o.ID)
			if err != nil {
				return reply("اعتبار کسر و ثبت شد؛ تحویل سفارش در حال بازیابی است.")
			}
			return nil
		}
		return reply(fmt.Sprintf("سفارش %s\nمبلغ: %d تومان\n%s\nکارت: %s · %s\nثبت رسید: /receipt %s شماره‌پیگیری\nلغو: /cancel %s", o.ID, o.Amount, s.Settings.PaymentNote, s.Settings.CardNumber, s.Settings.CardHolder, o.ID, o.ID))
	}
	return nil
}
func (a *App) sendStarsInvoice(ctx context.Context, t TelegramConfig, chat int64, o SalesOrder) error {
	return a.tgCall(ctx, t, "sendInvoice", map[string]any{"chat_id": chat, "title": string([]rune(o.Plan.Name)[:min(32, len([]rune(o.Plan.Name)))]), "description": "خرید یا تمدید سرویس " + o.Plan.Name, "payload": o.ID, "provider_token": "", "currency": "XTR", "prices": []map[string]any{{"label": o.Plan.Name, "amount": o.Amount}}}, nil)
}
func (a *App) telegramWebhook(w http.ResponseWriter, r *http.Request) {
	t := a.telegramSnapshot()
	if r.Method != "POST" || !t.Enabled || t.Mode != "webhook" || t.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token")), []byte(t.WebhookSecret)) != 1 {
		http.NotFound(w, r)
		return
	}
	var u tgUpdate
	if readBody(w, r, &u, 1<<20) != nil {
		apiError(w, 400, "update invalid")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := a.processUpdate(ctx, t, u); err != nil {
		apiError(w, 503, "processing deferred")
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}
func (a *App) configureWebhook(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode string `json:"mode"`
	}
	if readBody(w, r, &in, 1024) != nil || in.Mode != "poll" && in.Mode != "webhook" {
		apiError(w, 400, "حالت معتبر نیست")
		return
	}
	t := a.telegramSnapshot()
	if !t.Enabled || t.Token == "" {
		apiError(w, 400, "ابتدا ربات را فعال کن")
		return
	}
	if in.Mode == "webhook" {
		origin := a.snapshot().Settings.PublicURL
		if !strings.HasPrefix(origin, "https://") {
			apiError(w, 400, "دامنهٔ عمومی HTTPS لازم است")
			return
		}
		if t.WebhookSecret == "" {
			t.WebhookSecret = randomToken(32)
		}
		t.Mode = "webhook"
		if err := a.storeTelegramMode(t); err != nil {
			apiError(w, 500, err.Error())
			return
		}
		if err := a.tgCall(r.Context(), t, "setWebhook", map[string]any{"url": strings.TrimRight(origin, "/") + "/telegram/webhook", "secret_token": t.WebhookSecret, "allowed_updates": []string{"message", "callback_query", "pre_checkout_query"}, "drop_pending_updates": false}, nil); err != nil {
			apiError(w, 502, "تنظیم webhook کامل نشد؛ دوباره همان عملیات را اجرا کن")
			return
		}
	} else {
		if err := a.tgCall(r.Context(), t, "deleteWebhook", map[string]bool{"drop_pending_updates": false}, nil); err != nil {
			apiError(w, 502, err.Error())
			return
		}
		t.Mode = "poll"
		if err := a.storeTelegramMode(t); err != nil {
			apiError(w, 500, err.Error())
			return
		}
	}
	jsonReply(w, 200, a.runtimeSnapshot())
}
func (a *App) storeTelegramMode(t TelegramConfig) error {
	a.advancedMu.Lock()
	defer a.advancedMu.Unlock()
	if a.telegram.Token != t.Token {
		return fmt.Errorf("توکن هم‌زمان تغییر کرد")
	}
	next := a.telegram
	next.Mode = t.Mode
	next.WebhookSecret = t.WebhookSecret
	if err := atomicJSON(a.cfg.DataDir, "aria-telegram.json", next); err != nil {
		return err
	}
	a.telegram = next
	return nil
}
