package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
)

func adminHome() [][]BotButton {
	return [][]BotButton{{botButton("➕ ساخت کانفیگ", "adm:create"), botButton("👥 مدیریت کاربران", "adm:users:0")}, {botButton("📊 وضعیت هسته‌ها", "adm:status"), botButton("🧾 رسیدهای منتظر", "adm:receipts")}, {botButton("🛍 منوی مشتری", "home")}}
}
func adminRow() []BotButton { return []BotButton{botButton("⚙️ مدیریت پنل", "adm:home")} }
func (a *App) manageBot(ctx context.Context, t TelegramConfig, u tgUpdate, cmd, noticeID string) (bool, error) {
	user := u.Message.From.ID
	chat := u.Message.Chat.ID
	if u.Callback != nil {
		user = u.Callback.From.ID
		chat = u.Callback.Message.Chat.ID
	}
	if user != t.ChatID || chat != t.ChatID {
		return false, nil
	}
	reply := func(text string, k [][]BotButton) error { return a.queueBotUI(noticeID, t.ChatID, text, k) }
	customer := "admin-" + strconv.FormatInt(t.ChatID, 10)
	f := a.botFlow(customer)
	if cmd == "/admin" || cmd == "⚙️ مدیریت پنل" || cmd == "/start" {
		cmd = "ui:adm:home"
	}
	if !strings.HasPrefix(cmd, "ui:adm:") {
		if f.Kind != "admin-create" || strings.HasPrefix(cmd, "/") || strings.HasPrefix(cmd, "ui:") {
			return false, nil
		}
		switch f.Step {
		case "name":
			if !safeLabel(strings.TrimSpace(cmd), 64) {
				return true, reply("نام ۱ تا ۶۴ نویسه وارد کن.", [][]BotButton{adminRow()})
			}
			f.AdminName = strings.TrimSpace(cmd)
			f.Step = "quota"
			if e := a.saveBotFlow(f); e != nil {
				return true, e
			}
			return true, reply("حجم را به GB بنویس؛ صفر یعنی نامحدود.", [][]BotButton{adminRow()})
		case "quota":
			v, e := strconv.ParseFloat(bankDigits(cmd), 64)
			if e != nil || v < 0 || v > 1000000 {
				return true, reply("حجم باید عدد بین صفر و یک میلیون GB باشد.", [][]BotButton{adminRow()})
			}
			f.AdminQuota = v
			f.Step = "days"
			if e = a.saveBotFlow(f); e != nil {
				return true, e
			}
			return true, reply("تعداد روز اعتبار را بنویس؛ صفر یعنی بدون انقضا.", [][]BotButton{adminRow()})
		case "days":
			v, e := strconv.Atoi(bankDigits(cmd))
			if e != nil || v < 0 || v > 3650 {
				return true, reply("روز را از صفر تا ۳۶۵۰ وارد کن.", [][]BotButton{adminRow()})
			}
			f.AdminDays = v
			f.Step = "profiles"
			if e = a.saveBotFlow(f); e != nil {
				return true, e
			}
			return true, a.adminChoices(f, reply)
		}
		return true, reply("نوع اتصال را با دکمه‌ها انتخاب و ساخت را تأیید کن.", [][]BotButton{adminRow()})
	}
	p := strings.Split(strings.TrimPrefix(cmd, "ui:adm:"), ":")
	action := p[0]
	value := ""
	if len(p) > 1 {
		value = p[1]
	}
	switch action {
	case "home":
		if e := a.saveBotFlow(BotFlow{CustomerID: customer}); e != nil {
			return true, e
		}
		return true, reply("⚙️ مدیریت اختصاصی مدیر\nساخت مستقیم کانفیگ، مدیریت کاربران و بررسی رسیدها", adminHome())
	case "status":
		s := a.snapshot()
		return true, reply(fmt.Sprintf("📊 ariaatashin %s\nXray آماده: %t\nکاربران Xray: %d\nحالت میزبان: %s\n%s", s.Version, s.Ready, len(s.Users), envMode(a.cfg.Mode), a.managedCoreSummary()), adminHome())
	case "create":
		f = newBotFlow(customer, "admin-create", "name")
		f.AdminUUID = newUUID()
		f.AdminPassword = randomToken(20)
		if e := a.saveBotFlow(f); e != nil {
			return true, e
		}
		return true, reply("نام کاربر جدید را بنویس. این ساخت مستقیم مدیر است و سفارش پرداخت ایجاد نمی‌کند.", [][]BotButton{adminRow()})
	case "pick":
		if len(p) != 3 || f.Kind != "admin-create" || f.Step != "profiles" || f.Nonce != value {
			return true, reply("منوی ساخت منقضی شده؛ دوباره شروع کن.", adminHome())
		}
		key := p[2]
		found := false
		for _, profile := range a.snapshot().Profiles {
			if profile.Key == key && profile.Available {
				found = true
			}
		}
		if !found {
			return true, reply("الگو در این میزبان قابل ساخت نیست.", [][]BotButton{adminRow()})
		}
		next := []string{}
		selected := false
		for _, k := range f.AdminProfiles {
			if k == key {
				selected = true
			} else {
				next = append(next, k)
			}
		}
		if !selected {
			next = append(next, key)
		}
		f.AdminProfiles = next
		if e := a.saveBotFlow(f); e != nil {
			return true, e
		}
		return true, a.adminChoices(f, reply)
	case "confirm":
		if f.Kind != "admin-create" || f.Step != "profiles" || f.Nonce != value || len(f.AdminProfiles) == 0 {
			return true, reply("منوی معتبر و حداقل یک اتصال لازم است.", adminHome())
		}
		if e := a.refresh(ctx); e != nil {
			return true, reply("هسته آماده نیست؛ بازیابی اتصال را در پنل بررسی کن.", adminHome())
		}
		a.mu.RLock()
		engine := a.service
		a.mu.RUnlock()
		if engine == nil {
			return true, reply("ارتباط هسته در دسترس نیست.", adminHome())
		}
		body, _ := json.Marshal(CreateRequest{OperationID: f.Nonce, RequestUUID: f.AdminUUID, RequestPassword: f.AdminPassword, Name: f.AdminName, QuotaGB: f.AdminQuota, Days: f.AdminDays, Count: 1, Profiles: f.AdminProfiles})
		r := httptest.NewRequest("POST", "/api/users", bytes.NewReader(body)).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.createUsers(w, r, engine)
		var result struct {
			Created []string `json:"created"`
			Error   string   `json:"error"`
			Warning string   `json:"warning"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		if w.Code != 200 || len(result.Created) == 0 {
			return true, reply("ساخت انجام نشد: "+result.Error+result.Warning, adminHome())
		}
		if e := a.saveBotFlow(BotFlow{CustomerID: customer}); e != nil {
			return true, e
		}
		v, ok := a.findUser(result.Created[0])
		if !ok {
			return true, reply("کاربر ساخته شد؛ همگام‌سازی لینک را در پنل بررسی کن.", adminHome())
		}
		if e := a.queueAdminConnections(v.Email, "admin-create:"+f.Nonce); e != nil {
			return true, e
		}
		return true, a.adminUserCard(v, reply)
	case "deliver":
		if e := a.queueAdminConnections(value, "admin-delivery:"+noticeID); e != nil {
			return true, e
		}
		return true, reply("متن و QR اتصال‌ها در حال ارسال است.", adminHome())
	case "users":
		s := a.snapshot()
		page := pageNumber(value)
		rows := [][]BotButton{}
		for _, v := range listPage(s.Users, page) {
			rows = append(rows, []BotButton{botButton(v.Name, "adm:user:"+v.Email)})
		}
		if page > 0 {
			rows = append(rows, []BotButton{botButton("قبلی", "adm:users:"+strconv.Itoa(page-1))})
		}
		if (page+1)*6 < len(s.Users) {
			rows = append(rows, []BotButton{botButton("بعدی", "adm:users:"+strconv.Itoa(page+1))})
		}
		return true, reply("👥 کاربران؛ یک حساب را انتخاب کن.", append(rows, adminRow()))
	case "user":
		v, ok := a.findUser(value)
		if !ok {
			return true, reply("کاربر پیدا نشد.", adminHome())
		}
		return true, a.adminUserCard(v, reply)
	case "toggle", "reset":
		a.mu.RLock()
		engine := a.service
		a.mu.RUnlock()
		if engine == nil {
			return true, reply("هسته آماده نیست.", adminHome())
		}
		r := httptest.NewRequest("POST", "/api/users/"+value+"/"+action, strings.NewReader("{}")).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.changeUser(w, r, engine)
		if w.Code != 200 {
			return true, reply("تغییر انجام نشد؛ وضعیت هسته را در پنل بررسی کن.", adminHome())
		}
		v, ok := a.findUser(value)
		if !ok {
			return true, reply("کاربر پیدا نشد.", adminHome())
		}
		return true, a.adminUserCard(v, reply)
	case "receipts":
		rows := [][]BotButton{}
		for _, o := range a.salesSnapshot().Orders {
			if o.Status == "receipt" {
				rows = append(rows, []BotButton{botButton(o.ID+" · "+strconv.FormatInt(o.Amount, 10)+" تومان", "admin-review:"+o.ID)})
				if len(rows) >= 20 {
					break
				}
			}
		}
		return true, reply("🧾 رسیدهای منتظر بررسی", append(rows, adminRow()))
	}
	return true, reply("منوی مدیریت پیدا نشد.", adminHome())
}
func (a *App) adminChoices(f BotFlow, reply func(string, [][]BotButton) error) error {
	rows := [][]BotButton{}
	for _, p := range a.snapshot().Profiles {
		if !p.Available {
			continue
		}
		mark := "▫️ "
		for _, k := range f.AdminProfiles {
			if p.Key == k {
				mark = "✅ "
			}
		}
		rows = append(rows, []BotButton{botButton(mark+p.Name, "adm:pick:"+f.Nonce+":"+p.Key)})
	}
	rows = append(rows, []BotButton{botButton("ساخت با تنظیمات انتخاب‌شده", "adm:confirm:"+f.Nonce)}, adminRow())
	return reply(fmt.Sprintf("➕ %s\nحجم: %.2f GB · اعتبار: %d روز\nانتخاب‌شده: %d اتصال\nنوع‌های سازگار را انتخاب کن.", f.AdminName, f.AdminQuota, f.AdminDays, len(f.AdminProfiles)), rows)
}
func (a *App) adminUserCard(v User, reply func(string, [][]BotButton) error) error {
	text := fmt.Sprintf("📦 %s\nوضعیت: %s\nمصرف: %.2f / %.2f GB\nاشتراک:\n%s", v.Name, v.Status, float64(v.Up+v.Down)/(1<<30), float64(v.Quota)/(1<<30), v.Subscription)
	for _, l := range v.Links {
		if len(text)+len(l.Link) < 3700 {
			text += "\n\n" + l.Name + "\n" + l.Link
		}
	}
	return reply(text, [][]BotButton{{botButton("فعال / غیرفعال", "adm:toggle:"+v.Email), botButton("ریست مصرف", "adm:reset:"+v.Email)}, {botButton("📱 متن و QR", "adm:deliver:"+v.Email)}, adminRow()})
}
