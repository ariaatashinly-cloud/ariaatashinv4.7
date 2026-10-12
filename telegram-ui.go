package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type BotButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}
type BotFlow struct {
	AdminUUID     string   `json:"adminUUID,omitempty"`
	AdminPassword string   `json:"adminPassword,omitempty"`
	AdminName     string   `json:"adminName,omitempty"`
	AdminQuota    float64  `json:"adminQuota,omitempty"`
	AdminDays     int      `json:"adminDays,omitempty"`
	AdminProfiles []string `json:"adminProfiles,omitempty"`
	CustomerID    string   `json:"customerId"`
	Nonce         string   `json:"nonce"`
	Step          string   `json:"step"`
	Kind          string   `json:"kind"`
	PlanID        string   `json:"planId"`
	ServiceID     string   `json:"serviceId"`
	Coupon        string   `json:"coupon"`
	Amount        int64    `json:"amount"`
	QuotedAmount  int64    `json:"quotedAmount"`
	Method        string   `json:"method"`
	OrderID       string   `json:"orderId"`
	Expires       int64    `json:"expires"`
}

func botButton(text, data string) BotButton { return BotButton{Text: text, Data: "ui:" + data} }
func homeButtons() [][]BotButton {
	return [][]BotButton{{botButton("🛍 خرید سرویس", "plans:0"), botButton("📦 سرویس‌های من", "services:0")}, {botButton("💳 کیف پول", "wallet"), botButton("🧾 سفارش‌ها", "orders:0")}, {botButton("🆘 پشتیبانی", "support"), botButton("🌐 فروشگاه / QR", "shop")}, {botButton("🎁 تست اتصال", "trial")}}
}
func homeRow() []BotButton { return []BotButton{botButton("⌂ منوی اصلی", "home")} }
func addRichNotice(s *SalesData, id string, chat int64, text string, buttons [][]BotButton) {
	addNotice(s, id, chat, text)
	for i := range s.Notices {
		if s.Notices[i].ID == id && !s.Notices[i].Sent {
			s.Notices[i].Buttons = buttons
			break
		}
	}
}
func (a *App) queueBotUI(id string, chat int64, text string, buttons [][]BotButton) error {
	return a.salesTxn(func(s *SalesData) error { addRichNotice(s, "tg:"+id, chat, text, buttons); return nil })
}
func (a *App) botFlow(customer string) BotFlow {
	for _, f := range a.salesSnapshot().BotFlows {
		if f.CustomerID == customer && f.Expires > time.Now().UnixMilli() {
			return f
		}
	}
	return BotFlow{}
}
func (a *App) saveBotFlow(f BotFlow) error {
	return a.salesTxn(func(s *SalesData) error {
		out := []BotFlow{}
		now := time.Now().UnixMilli()
		for _, old := range s.BotFlows {
			if old.CustomerID != f.CustomerID && old.Expires > now {
				out = append(out, old)
			}
		}
		if f.Nonce != "" {
			if len(out) >= 10000 {
				return fmt.Errorf("منوی ربات پر است؛ کمی بعد تلاش کن")
			}
			out = append(out, f)
		}
		s.BotFlows = out
		return nil
	})
}
func newBotFlow(customer, kind, step string) BotFlow {
	return BotFlow{CustomerID: customer, Nonce: randomToken(8), Kind: kind, Step: step, Expires: time.Now().Add(30 * time.Minute).UnixMilli()}
}
func paymentTitle(m string) string {
	switch m {
	case "manual":
		return "کارت / حساب و رسید"
	case "wallet":
		return "کیف پول"
	case "zarinpal":
		return "زرین‌پال"
	case "zibal":
		return "زیبال"
	case "stars":
		return "Telegram Stars"
	}
	return m
}
func paymentKeyboard(s SalesData, f BotFlow) [][]BotButton {
	rows := [][]BotButton{}
	for _, m := range paymentMethods(s) {
		if f.Kind == "topup" && m["id"] == "wallet" {
			continue
		}
		rows = append(rows, []BotButton{botButton(paymentTitle(m["id"]), "pay:"+f.Nonce+":"+m["id"])})
	}
	pi := planIndex(&s, f.PlanID)
	if f.Kind != "topup" && pi >= 0 && s.Plans[pi].Stars > 0 && f.Coupon == "" {
		rows = append(rows, []BotButton{botButton(fmt.Sprintf("⭐ %d Stars", s.Plans[pi].Stars), "pay:"+f.Nonce+":stars")})
	}
	if f.Kind != "topup" {
		rows = append(rows, []BotButton{botButton("🏷 کد تخفیف", "coupon:"+f.Nonce)})
	}
	return append(rows, homeRow())
}
func flowSummary(s SalesData, f BotFlow) string {
	if f.Kind == "topup" {
		return fmt.Sprintf("💳 شارژ کیف پول\nمبلغ: %d تومان\nروش پرداخت را انتخاب کن.", f.Amount)
	}
	pi := planIndex(&s, f.PlanID)
	if pi < 0 {
		return "پلن در دسترس نیست"
	}
	p := s.Plans[pi]
	return fmt.Sprintf("🛍 %s\n%s\nحجم: %.2f GB · اعتبار: %d روز\nقیمت: %d تومان\nکد تخفیف: %s\nروش پرداخت را انتخاب کن.", p.Name, p.Description, p.QuotaGB, p.Days, f.QuotedAmount, f.Coupon)
}
func bankInstructions(t ShopSettings) string {
	return strings.TrimSpace(fmt.Sprintf("%s\nبانک: %s\nکارت: %s\nحساب: %s\nشبا: %s\nصاحب حساب: %s", t.PaymentNote, t.BankName, t.CardNumber, t.AccountNumber, t.IBAN, t.CardHolder))
}
func pageNumber(s string) int {
	n, _ := strconv.Atoi(s)
	if n < 0 || n > 10000 {
		return 0
	}
	return n
}
func listPage[T any](items []T, page int) []T {
	start := page * 6
	if start >= len(items) {
		return nil
	}
	return items[start:min(start+6, len(items))]
}
func listNav(kind string, page, count int) [][]BotButton {
	rows := [][]BotButton{}
	r := []BotButton{}
	if page > 0 {
		r = append(r, botButton("قبلی", kind+":"+strconv.Itoa(page-1)))
	}
	if (page+1)*6 < count {
		r = append(r, botButton("بعدی", kind+":"+strconv.Itoa(page+1)))
	}
	if len(r) > 0 {
		rows = append(rows, r)
	}
	return append(rows, homeRow())
}

func (a *App) botOrder(ctx context.Context, id string, c SalesCustomer, replyID string) error {
	s := a.salesSnapshot()
	oi := orderIndex(&s, id)
	if oi < 0 || s.Orders[oi].CustomerID != c.ID {
		return a.queueBotUI(replyID, c.TelegramID, "سفارش مشتری پیدا نشد.", homeButtons())
	}
	o := s.Orders[oi]
	rows := [][]BotButton{}
	text := fmt.Sprintf("🧾 سفارش %s\n%s · %d %s\nوضعیت: %s", o.ID, o.Plan.Name, o.Amount, o.Currency, orderStatusLabel(o.Status))
	if o.Status == "fulfilled" {
		if o.ServiceID != "" {
			rows = append(rows, []BotButton{botButton("📦 کانفیگ و سرویس", "service:"+o.ServiceID)})
		} else {
			text += "\n✅ اعتبار به کیف پول اضافه شد."
		}
	}
	if o.Method == "manual" && (o.Status == "pending" || o.Status == "receipt") {
		text += "\n\n" + bankInstructions(s.Settings)
		rows = append(rows, []BotButton{botButton("🧾 ارسال رسید", "receipt:"+id), botButton("لغو سفارش", "cancel:"+id)})
	}
	if gatewayMethod(o.Method) && (o.Status == "pending" || o.Status == "paid" || o.Status == "uncertain") {
		if o.Status == "pending" {
			link, err := a.startPayment(ctx, c.ID, id)
			if err == nil {
				rows = append(rows, []BotButton{{Text: "💳 پرداخت امن · " + paymentTitle(o.Method), URL: link}})
			} else {
				text += "\n" + err.Error()
			}
		}
		rows = append(rows, []BotButton{botButton("🔎 بررسی پرداخت", "check:"+id)})
	}
	if o.Method == "stars" && o.Status == "pending" {
		rows = append(rows, []BotButton{botButton("⭐ صورتحساب Stars", "stars:"+id)})
	}
	return a.queueBotUI(replyID, c.TelegramID, text, append(rows, homeRow()))
}
func orderStatusLabel(s string) string {
	m := map[string]string{"pending": "منتظر پرداخت", "receipt": "در حال بررسی رسید", "paid": "پرداخت تأیید شده", "fulfilled": "تحویل شده", "provisioning": "در حال ساخت", "uncertain": "تحویل در حال بازیابی", "cancelled": "لغو شده", "failed": "نیازمند بررسی"}
	if v := m[s]; v != "" {
		return v
	}
	return s
}
func (a *App) adminBotUI(ctx context.Context, t TelegramConfig, cmd, noticeID string) (bool, error) {
	if !strings.HasPrefix(cmd, "ui:admin-") {
		return false, nil
	}
	p := strings.Split(cmd, ":")
	reply := func(text string, k [][]BotButton) error { return a.queueBotUI(noticeID, t.ChatID, text, k) }
	if len(p) != 3 {
		return true, nil
	}
	id := p[2]
	s := a.salesSnapshot()
	i := orderIndex(&s, id)
	if i < 0 {
		return true, reply("سفارش پیدا نشد.", nil)
	}
	o := s.Orders[i]
	if p[1] == "admin-review" {
		if o.TelegramPhoto != "" {
			if err := a.salesTxn(func(s *SalesData) error { queueReceiptPhoto(s, "receipt-review:"+noticeID, t.ChatID, id); return nil }); err != nil {
				return true, err
			}
		}
		k := [][]BotButton{{botButton("✅ مبلغ واقعی را بررسی کردم؛ تأیید", "admin-approve:"+id)}, {botButton("رد رسید / لغو", "admin-reject:"+id)}}
		return true, reply(fmt.Sprintf("بررسی رسید\n%s\n%d تومان\n%s\nعکس رسید جداگانه ارسال می‌شود و در پنل هم قابل بازکردن است. پس از تطبیق با تراکنش واقعی تأیید کن.", id, o.Amount, o.Receipt), k)
	}
	if p[1] == "admin-approve" {
		_, err := a.approveOrder(ctx, id)
		if err != nil {
			return true, reply(err.Error(), nil)
		}
		return true, reply("✅ پرداخت ثبت شد و سفارش تحویل شد.", nil)
	}
	if p[1] == "admin-reject" {
		if err := a.cancelOrder("", id, true); err != nil {
			return true, reply(err.Error(), nil)
		}
		return true, reply("سفارش رد شد.", nil)
	}
	return true, nil
}

func (a *App) customerBotUI(ctx context.Context, t TelegramConfig, u tgUpdate, c SalesCustomer, cmd, noticeID string) (bool, error) {
	labels := map[string]string{"🛍 خرید سرویس": "/plans", "📦 سرویس‌های من": "/services", "💳 کیف پول": "/wallet", "🧾 سفارش‌ها": "/orders", "🆘 پشتیبانی": "/support", "🌐 فروشگاه / QR": "/shop", "🎁 تست اتصال": "/trial", "⌂ منوی اصلی": "/start"}
	if v := labels[cmd]; v != "" {
		cmd = v
	}
	commands := map[string]string{"/start": "home", "/help": "home", "/plans": "plans:0", "/services": "services:0", "/wallet": "wallet", "/orders": "orders:0", "/shop": "shop", "/trial": "trial", "/support": "support"}
	if v := commands[cmd]; v != "" {
		cmd = "ui:" + v
	}
	reply := func(text string, k [][]BotButton) error { return a.queueBotUI(noticeID, c.TelegramID, text, k) }
	s := a.salesSnapshot()
	f := a.botFlow(c.ID)
	if !strings.HasPrefix(cmd, "ui:") {
		if f.Nonce == "" || f.Step == "payment" || f.Step == "order" || strings.HasPrefix(cmd, "/") {
			return false, nil
		}
		switch f.Step {
		case "coupon":
			code := strings.ToUpper(strings.TrimSpace(cmd))
			pi := planIndex(&s, f.PlanID)
			if pi < 0 {
				return true, reply("پلن پیدا نشد؛ دوباره انتخاب کن.", homeButtons())
			}
			price := s.Plans[pi].Price
			found := false
			for _, v := range s.Coupons {
				if v.Code == code && v.Enabled && (v.Expires == 0 || v.Expires > time.Now().UnixMilli()) && (v.Limit == 0 || v.Used < v.Limit) {
					price -= price * v.Percent / 100
					found = true
					break
				}
			}
			if !found {
				return true, reply("کد تخفیف معتبر نیست؛ کد دیگر بفرست یا منوی اصلی را بزن.", [][]BotButton{homeRow()})
			}
			f.Coupon = code
			f.QuotedAmount = price
			f.Step = "payment"
			if err := a.saveBotFlow(f); err != nil {
				return true, err
			}
			return true, reply(flowSummary(s, f), paymentKeyboard(s, f))
		case "topup":
			amount, err := strconv.ParseInt(bankDigits(cmd), 10, 64)
			if err != nil || amount < 1000 || amount > 1000000000 {
				return true, reply("مبلغ را به تومان، بین ۱۰۰۰ و یک میلیارد وارد کن.", [][]BotButton{homeRow()})
			}
			f.Amount = amount
			f.Step = "payment"
			if err = a.saveBotFlow(f); err != nil {
				return true, err
			}
			return true, reply(flowSummary(s, f), paymentKeyboard(s, f))
		case "support":
			if err := a.createTicket(c.ID, cmd); err != nil {
				return true, reply(err.Error(), [][]BotButton{homeRow()})
			}
			if err := a.saveBotFlow(BotFlow{CustomerID: c.ID}); err != nil {
				return true, err
			}
			return true, reply("✅ تیکت ثبت شد. پاسخ مدیر همینجا می‌آید.", homeButtons())
		case "receipt":
			photo := ""
			text := cmd
			if len(u.Message.Photo) > 0 {
				photo = u.Message.Photo[len(u.Message.Photo)-1].FileID
				text = u.Message.Caption
			}
			if err := a.submitReceipt(c.ID, f.OrderID, text, "", photo); err != nil {
				return true, reply(err.Error(), [][]BotButton{homeRow()})
			}
			f.Step = "order"
			if err := a.saveBotFlow(f); err != nil {
				return true, err
			}
			return true, reply("🧾 رسید ثبت شد؛ پس از بررسی و تأیید مدیر، سرویس خودکار تحویل می‌شود.", [][]BotButton{{botButton("سفارش من", "order:"+f.OrderID)}, homeRow()})
		}
		return false, nil
	}
	p := strings.Split(strings.TrimPrefix(cmd, "ui:"), ":")
	value := ""
	if len(p) > 1 {
		value = p[1]
	}
	rows := [][]BotButton{}
	switch p[0] {
	case "home":
		if err := a.saveBotFlow(BotFlow{CustomerID: c.ID}); err != nil {
			return true, err
		}
		welcome := s.Settings.Welcome
		if welcome == "" {
			welcome = "به فروشگاه اتصال خوش آمدی. پلن را انتخاب کن، پرداخت کن و کانفیگ آماده بگیر."
		}
		return true, reply("🔥 "+s.Settings.Name+"\n\n"+welcome+fmt.Sprintf("\n\n💳 موجودی شما: %d تومان", c.Balance), homeButtons())
	case "shop":
		link, err := a.portalLink(c.ID)
		if err != nil {
			return true, reply(err.Error(), homeButtons())
		}
		return true, reply("🌐 فروشگاه اختصاصی شما\nخرید، QR، مدیریت سرویس و تیکت.\nلینک یک‌بار مصرف؛ ۳۰ دقیقه معتبر است. آن را برای دیگران نفرست.", [][]BotButton{{{Text: "بازکردن فروشگاه ↗", URL: link}}, homeRow()})
	case "plans", "renew":
		service := ""
		if p[0] == "renew" {
			si := serviceIndex(&s, value)
			if si < 0 || s.Services[si].CustomerID != c.ID {
				return true, reply("سرویس مشتری پیدا نشد.", homeButtons())
			}
			service = value
			value = "0"
			if len(p) > 2 {
				value = p[2]
			}
		}
		page := pageNumber(value)
		items := []SalesPlan{}
		for _, v := range s.Plans {
			if v.Enabled && (service == "" || v.NodeID == s.Services[serviceIndex(&s, service)].NodeID) {
				items = append(items, v)
			}
		}
		for _, v := range listPage(items, page) {
			data := "plan:" + v.ID
			if service != "" {
				data += "/" + service
			}
			rows = append(rows, []BotButton{botButton(fmt.Sprintf("%s · %d تومان", v.Name, v.Price), data)})
		}
		if service != "" {
			rows = append(rows, listNav("renew:"+service, page, len(items))...)
		} else {
			rows = append(rows, listNav("plans", page, len(items))...)
		}
		text := "🛍 یک پلن انتخاب کن؛ حجم و مدت در مرحله بعد نمایش داده می‌شود."
		if len(items) == 0 {
			text = "پلن فعالی روی این مقصد موجود نیست."
		}
		return true, reply(text, rows)
	case "plan":
		ids := strings.Split(value, "/")
		pi := planIndex(&s, ids[0])
		if pi < 0 || !s.Plans[pi].Enabled {
			return true, reply("پلن در دسترس نیست.", homeButtons())
		}
		f = newBotFlow(c.ID, "buy", "payment")
		f.PlanID = ids[0]
		f.QuotedAmount = s.Plans[pi].Price
		if len(ids) == 2 {
			si := serviceIndex(&s, ids[1])
			if si < 0 || s.Services[si].CustomerID != c.ID || s.Services[si].NodeID != s.Plans[pi].NodeID {
				return true, reply("تمدید برای این سرویس مجاز نیست.", homeButtons())
			}
			f.Kind = "renew"
			f.ServiceID = ids[1]
		}
		if err := a.saveBotFlow(f); err != nil {
			return true, err
		}
		return true, reply(flowSummary(s, f), paymentKeyboard(s, f))
	case "wallet":
		return true, reply(fmt.Sprintf("💳 کیف پول شما: %d تومان\nبرای شارژ، مبلغ را انتخاب یا وارد کن. پرداخت تکراری دوباره اعتبار اضافه نمی‌کند.", c.Balance), [][]BotButton{{botButton("۵۰ هزار تومان", "topup:50000"), botButton("۱۰۰ هزار تومان", "topup:100000")}, {botButton("مبلغ دلخواه", "topup:custom")}, homeRow()})
	case "topup":
		f = newBotFlow(c.ID, "topup", "payment")
		if value == "custom" {
			f.Step = "topup"
			if err := a.saveBotFlow(f); err != nil {
				return true, err
			}
			return true, reply("مبلغ شارژ را به تومان بنویس.", [][]BotButton{homeRow()})
		}
		if value != "50000" && value != "100000" {
			return true, nil
		}
		f.Amount, _ = strconv.ParseInt(value, 10, 64)
		if err := a.saveBotFlow(f); err != nil {
			return true, err
		}
		return true, reply(flowSummary(s, f), paymentKeyboard(s, f))
	case "coupon":
		if f.Nonce == "" || f.Nonce != value || f.OrderID != "" || f.Kind == "topup" {
			return true, reply("این منو منقضی شده؛ پلن را دوباره انتخاب کن.", homeButtons())
		}
		f.Step = "coupon"
		if err := a.saveBotFlow(f); err != nil {
			return true, err
		}
		return true, reply("کد تخفیف را بنویس.", [][]BotButton{homeRow()})
	case "pay":
		if len(p) != 3 || f.Nonce == "" || f.Nonce != value {
			return true, reply("این منو منقضی شده؛ دوباره انتخاب کن.", homeButtons())
		}
		if f.OrderID != "" {
			return true, a.botOrder(ctx, f.OrderID, c, noticeID)
		}
		if f.Step != "payment" {
			return true, reply("ابتدا مراحل منوی فعلی را کامل کن.", homeButtons())
		}
		expected := f.QuotedAmount
		if f.Kind == "topup" {
			expected = f.Amount
		}
		o, err := a.newOrder(BuyRequest{CustomerID: c.ID, Kind: f.Kind, PlanID: f.PlanID, ServiceID: f.ServiceID, Method: p[2], Amount: f.Amount, Coupon: f.Coupon, ExpectedAmount: &expected, Idempotency: "botflow:" + c.ID + ":" + f.Nonce})
		if err != nil {
			return true, reply(err.Error(), paymentKeyboard(s, f))
		}
		f.OrderID = o.ID
		f.Step = "order"
		f.Method = o.Method
		if err = a.saveBotFlow(f); err != nil {
			return true, err
		}
		if o.Status == "paid" {
			if _, err = a.fulfillOrder(ctx, o.ID); err != nil {
				return true, reply("پرداخت ثبت شد؛ تحویل در حال بازیابی است. دوباره پرداخت نکن.", homeButtons())
			}
		}
		if o.Method == "stars" {
			if err := a.sendStarsInvoice(ctx, t, c.TelegramID, o); err != nil {
				return true, err
			}
		}
		return true, a.botOrder(ctx, o.ID, c, noticeID)
	case "orders":
		items := []SalesOrder{}
		for i := len(s.Orders) - 1; i >= 0; i-- {
			if s.Orders[i].CustomerID == c.ID {
				items = append(items, s.Orders[i])
			}
		}
		page := pageNumber(value)
		for _, o := range listPage(items, page) {
			rows = append(rows, []BotButton{botButton(o.ID+" · "+orderStatusLabel(o.Status), "order:"+o.ID)})
		}
		return true, reply("🧾 سفارش‌های شما", append(rows, listNav("orders", page, len(items))...))
	case "order":
		return true, a.botOrder(ctx, value, c, noticeID)
	case "check":
		_, err := a.customerPaymentCheck(ctx, c.ID, value)
		if err != nil {
			return true, reply(err.Error()+"\nدر صورت پرداخت، رسید را نگه دار و بررسی پرداخت را دوباره بزن.", [][]BotButton{{botButton("بررسی دوباره", "check:"+value)}, homeRow()})
		}
		return true, a.botOrder(ctx, value, c, noticeID)
	case "stars":
		oi := orderIndex(&s, value)
		if oi < 0 || s.Orders[oi].CustomerID != c.ID || s.Orders[oi].Method != "stars" || s.Orders[oi].Status != "pending" {
			return true, reply("صورتحساب فعال پیدا نشد.", homeButtons())
		}
		return true, a.sendStarsInvoice(ctx, t, c.TelegramID, s.Orders[oi])
	case "receipt":
		oi := orderIndex(&s, value)
		if oi < 0 || s.Orders[oi].CustomerID != c.ID || s.Orders[oi].Method != "manual" || (s.Orders[oi].Status != "pending" && s.Orders[oi].Status != "receipt") {
			return true, reply("سفارش منتظر رسید پیدا نشد.", homeButtons())
		}
		f = newBotFlow(c.ID, "", "receipt")
		f.OrderID = value
		if err := a.saveBotFlow(f); err != nil {
			return true, err
		}
		return true, reply("🧾 عکس رسید یا شماره پیگیری را ارسال کن. مبلغ: "+strconv.FormatInt(s.Orders[oi].Amount, 10)+" تومان\n"+bankInstructions(s.Settings), [][]BotButton{homeRow()})
	case "cancel":
		if err := a.cancelOrder(c.ID, value, false); err != nil {
			return true, reply(err.Error(), homeButtons())
		}
		return true, reply("سفارش لغو شد.", homeButtons())
	case "services":
		items := []SoldService{}
		for _, v := range s.Services {
			if v.CustomerID == c.ID {
				items = append(items, v)
			}
		}
		page := pageNumber(value)
		for _, v := range listPage(items, page) {
			rows = append(rows, []BotButton{botButton("📦 "+v.Name, "service:"+v.ID)})
		}
		return true, reply("📦 سرویس‌های شما · لینک، فایل و تمدید", append(rows, listNav("services", page, len(items))...))
	case "service", "config", "sync":
		si := serviceIndex(&s, value)
		if si < 0 || s.Services[si].CustomerID != c.ID {
			return true, reply("سرویس مشتری پیدا نشد.", homeButtons())
		}
		v := s.Services[si]
		if p[0] == "sync" {
			live, err := a.syncSoldService(ctx, value)
			if err != nil {
				return true, reply("مقصد پاسخ نداد؛ سرویس حذف نشده است.", homeButtons())
			}
			v = live
		}
		if p[0] == "config" {
			return true, a.salesTxn(func(s *SalesData) error {
				queueConnectionDelivery(s, "tg-connections:"+noticeID, c.TelegramID, v.ID, "", len(v.Links))
				addNotice(s, "tg:"+noticeID, c.TelegramID, "کانفیگ‌های "+v.Name)
				for i := range s.Notices {
					if s.Notices[i].ID == "tg:"+noticeID {
						s.Notices[i].DocumentService = v.ID
					}
				}
				return nil
			})
		}
		text := fmt.Sprintf("📦 %s\nمصرف: %.2f / %.2f GB\n", v.Name, float64(v.Used)/(1<<30), float64(v.Quota)/(1<<30))
		if v.Expiry > 0 {
			text += "انقضا: " + time.UnixMilli(v.Expiry).UTC().Format("2006-01-02") + " UTC\n"
		}
		if v.Subscription != "" {
			text += "اشتراک:\n" + v.Subscription
		}
		return true, reply(text, [][]BotButton{{botButton("📄 فایل کانفیگ", "config:"+v.ID), botButton("🔄 تمدید", "renew:"+v.ID)}, {botButton("مصرف تازه", "sync:"+v.ID), botButton("QR / فروشگاه", "shop")}, homeRow()})
	case "support":
		f = newBotFlow(c.ID, "", "support")
		if err := a.saveBotFlow(f); err != nil {
			return true, err
		}
		return true, reply("🆘 پیام پشتیبانی را بنویس؛ به مدیر می‌رسد.\n"+s.Settings.Support, [][]BotButton{homeRow()})
	case "trial":
		o, err := a.newTrial(ctx, c.ID)
		if err != nil {
			return true, reply(err.Error(), homeButtons())
		}
		return true, a.botOrder(ctx, o.ID, c, noticeID)
	}
	return true, nil
}

func queueServiceDelivery(s *SalesData, o SalesOrder, v SoldService) {
	ci := customerIndex(s, o.CustomerID)
	if ci < 0 {
		return
	}
	chat := s.Customers[ci].TelegramID
	queueConnectionDelivery(s, "delivery-connections:"+o.ID, chat, v.ID, "", len(v.Links))
	text := "✅ سرویس آماده شد\nسفارش " + o.ID + "\n" + v.Name + "\nاشتراک:\n" + v.Subscription + "\nفایل همهٔ کانفیگ‌ها در پیام بعدی است."
	addRichNotice(s, "delivery:"+o.ID, chat, text, [][]BotButton{{botButton("📦 سرویس و تمدید", "service:"+v.ID), botButton("🌐 فروشگاه / QR", "shop")}})
	addNotice(s, "delivery-file:"+o.ID, chat, "📄 کانفیگ‌های "+v.Name)
	for i := range s.Notices {
		if s.Notices[i].ID == "delivery-file:"+o.ID {
			s.Notices[i].DocumentService = v.ID
		}
	}
}
func (a *App) tgRichSend(ctx context.Context, t TelegramConfig, n SalesNotice) error {
	if n.ConnectionKind != "" {
		return a.connectionNotice(ctx, t, n)
	}
	if n.ReceiptOrder != "" {
		return a.sendReceiptPhoto(ctx, t, n)
	}
	if n.DocumentService != "" {
		s := a.salesSnapshot()
		si := serviceIndex(&s, n.DocumentService)
		if si < 0 {
			return fmt.Errorf("فایل سرویس آماده نیست")
		}
		v := s.Services[si]
		ci := customerIndex(&s, v.CustomerID)
		if ci < 0 || s.Customers[ci].TelegramID != n.ChatID {
			return fmt.Errorf("مالک فایل مطابقت ندارد")
		}
		return a.tgDocument(ctx, t, v)
	}
	if len(n.Buttons) == 0 {
		return a.tgSend(ctx, t, n.Text)
	}
	if len([]rune(n.Text)) > 4000 {
		return fmt.Errorf("پیام ربات بیش از حد بلند است؛ فایل کانفیگ را دریافت کن")
	}
	return a.tgCall(ctx, t, "sendMessage", map[string]any{"chat_id": n.ChatID, "text": n.Text, "protect_content": false, "link_preview_options": map[string]bool{"is_disabled": true}, "reply_markup": map[string]any{"inline_keyboard": n.Buttons}}, nil)
}
func (a *App) tgDocument(ctx context.Context, t TelegramConfig, v SoldService) error {
	if v.NodeID == "" || v.NodeID == "local" {
		if u, ok := a.findUser(v.Email); ok {
			v.Links = []string{}
			for _, l := range u.Links {
				v.Links = append(v.Links, l.Link)
			}
		}
	}
	content := "ariaatashin\n" + v.Name + "\nSubscription: " + v.Subscription + "\n\n" + strings.Join(v.Links, "\n") + "\n"
	if len(content) > 1<<20 {
		return fmt.Errorf("فایل بیش از حد بزرگ است")
	}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("chat_id", strconv.FormatInt(t.ChatID, 10))
	_ = writer.WriteField("protect_content", "false")
	_ = writer.WriteField("caption", "📄 "+v.Name+" · لینک‌ها را در برنامه اتصال وارد کن.")
	part, err := writer.CreateFormFile("document", "ariaatashin-configs.txt")
	if err != nil {
		return err
	}
	_, _ = part.Write([]byte(content))
	_ = writer.Close()
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.telegram.org/bot"+t.Token+"/sendDocument", &buf)
	if err != nil {
		return fmt.Errorf("ارسال فایل آماده نشد")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	cl := a.telegramHTTP
	if cl == nil {
		cl = &http.Client{Timeout: 20 * time.Second, CheckRedirect: noRedirect}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("ارسال فایل به تلگرام تأیید نشد")
	}
	defer resp.Body.Close()
	var result struct {
		OK bool `json:"ok"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&result) != nil || !result.OK {
		return fmt.Errorf("تلگرام فایل را نپذیرفت")
	}
	return nil
}
