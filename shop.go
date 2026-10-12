package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Telegram initData is verified on the server; an unsigned user object is never
// accepted. The short login window is independent of the portal session lifetime.
func validateInitData(raw, token string, now time.Time) (int64, string, error) {
	if token == "" || len(raw) > 16384 {
		return 0, "", fmt.Errorf("ورود تلگرام فعال نیست")
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return 0, "", fmt.Errorf("دادهٔ ورود معتبر نیست")
	}
	for _, v := range q {
		if len(v) != 1 {
			return 0, "", fmt.Errorf("دادهٔ ورود تکراری است")
		}
	}
	signed, err := hex.DecodeString(q.Get("hash"))
	if err != nil || len(signed) != 32 {
		return 0, "", fmt.Errorf("امضای ورود معتبر نیست")
	}
	q.Del("hash")
	keys := []string{}
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := []string{}
	for _, k := range keys {
		lines = append(lines, k+"="+q.Get(k))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	if !hmac.Equal(signed, mac.Sum(nil)) {
		return 0, "", fmt.Errorf("امضای ورود تلگرام مطابقت ندارد")
	}
	date, err := strconv.ParseInt(q.Get("auth_date"), 10, 64)
	if err != nil || date < now.Add(-5*time.Minute).Unix() || date > now.Add(30*time.Second).Unix() {
		return 0, "", fmt.Errorf("لینک ورود منقضی شده؛ فروشگاه را دوباره از ربات باز کن")
	}
	var user struct {
		ID    int64  `json:"id"`
		First string `json:"first_name"`
		Last  string `json:"last_name"`
		Bot   bool   `json:"is_bot"`
	}
	if json.Unmarshal([]byte(q.Get("user")), &user) != nil || user.ID <= 0 || user.Bot {
		return 0, "", fmt.Errorf("کاربر تلگرام معتبر نیست")
	}
	name := strings.TrimSpace(user.First + " " + user.Last)
	if !safeLabel(name, 64) {
		name = "مشتری تلگرام"
	}
	return user.ID, name, nil
}
func (a *App) portalSession(r *http.Request) (SalesCustomer, bool) {
	c, e := r.Cookie("aria_customer")
	if e != nil || len(c.Value) != 64 {
		return SalesCustomer{}, false
	}
	hash := hashToken(c.Value)
	s := a.salesSnapshot()
	for _, v := range s.Sessions {
		if !v.Magic && v.Expires > time.Now().UnixMilli() && subtle.ConstantTimeCompare([]byte(hash), []byte(v.Hash)) == 1 {
			ci := customerIndex(&s, v.CustomerID)
			if ci >= 0 && !s.Customers[ci].Blocked {
				return s.Customers[ci], true
			}
		}
	}
	return SalesCustomer{}, false
}
func (a *App) portalLink(customer string) (string, error) {
	token := randomToken(32)
	err := a.salesTxn(func(s *SalesData) error {
		ci := customerIndex(s, customer)
		if ci < 0 || s.Customers[ci].Blocked {
			return fmt.Errorf("مشتری فعال نیست")
		}
		trimPortal(s)
		s.Sessions = append(s.Sessions, PortalSession{Hash: hashToken(token), CustomerID: customer, Expires: time.Now().Add(30 * time.Minute).UnixMilli(), Magic: true})
		return nil
	})
	return strings.TrimRight(a.snapshot().Settings.PublicURL, "/") + "/shop#access=" + token, err
}
func trimPortal(s *SalesData) {
	out := []PortalSession{}
	for _, v := range s.Sessions {
		if v.Expires > time.Now().UnixMilli() {
			out = append(out, v)
		}
	}
	if len(out) > 10000 {
		out = out[len(out)-10000:]
	}
	s.Sessions = out
}
func (a *App) shopLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Init   string `json:"initData"`
		Access string `json:"access"`
	}
	if readBody(w, r, &in, 20000) != nil {
		apiError(w, 400, "دادهٔ ورود معتبر نیست")
		return
	}
	var customer string
	if in.Init != "" {
		t := a.telegramSnapshot()
		if !t.Enabled {
			apiError(w, 403, "ربات غیرفعال است")
			return
		}
		id, name, err := validateInitData(in.Init, t.Token, time.Now())
		if err != nil {
			apiError(w, 403, err.Error())
			return
		}
		c, err := a.createCustomer(name, id)
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		customer = c.ID
	}
	token := randomToken(32)
	err := a.salesTxn(func(s *SalesData) error {
		trimPortal(s)
		if in.Access != "" {
			if len(in.Access) != 64 {
				return fmt.Errorf("لینک ورود معتبر نیست")
			}
			hash := hashToken(in.Access)
			found := -1
			for i, v := range s.Sessions {
				if v.Magic && hmac.Equal([]byte(hash), []byte(v.Hash)) {
					customer = v.CustomerID
					found = i
					break
				}
			}
			if found < 0 {
				return fmt.Errorf("لینک ورود مصرف شده یا منقضی است؛ لینک تازه از ربات بگیر")
			}
			s.Sessions = append(s.Sessions[:found], s.Sessions[found+1:]...)
		}
		ci := customerIndex(s, customer)
		if ci < 0 || s.Customers[ci].Blocked {
			return fmt.Errorf("مشتری فعال نیست")
		}
		s.Sessions = append(s.Sessions, PortalSession{Hash: hashToken(token), CustomerID: customer, Expires: time.Now().Add(time.Hour).UnixMilli()})
		return nil
	})
	if err != nil {
		apiError(w, 403, err.Error())
		return
	}
	origin, _ := a.requestOrigin(r)
	secure := strings.HasPrefix(origin, "https:")
	http.SetCookie(w, &http.Cookie{Name: "aria_customer", Value: token, Path: "/shop", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: 3600})
	jsonReply(w, 200, map[string]bool{"ok": true})
}
func (a *App) shopHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !a.salesSnapshot().Settings.Enabled {
		apiError(w, 503, "فروشگاه فعلاً غیرفعال است")
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/shop/api/") {
		if r.URL.Path != "/shop" && r.URL.Path != "/shop/" {
			http.NotFound(w, r)
			return
		}
		b, _ := assets.ReadFile("web/shop.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Del("X-Frame-Options")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://telegram.org; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors https://web.telegram.org; base-uri 'none'; form-action 'self'; object-src 'none'")
		w.Write(b)
		return
	}
	if r.Method != "GET" {
		origin, _ := a.requestOrigin(r)
		if r.Header.Get("X-Requested-With") != "aria-shop" || r.Header.Get("Origin") != origin || origin == "" || origin != a.snapshot().Settings.PublicURL {
			apiError(w, 403, "درخواست فروشگاه از مبدأ معتبر لازم است")
			return
		}
	}
	path := strings.TrimPrefix(r.URL.Path, "/shop/api/")
	if path == "catalog" && r.Method == "GET" {
		s := a.salesSnapshot()
		plans := []SalesPlan{}
		for _, p := range s.Plans {
			if p.Enabled {
				plans = append(plans, p)
			}
		}
		jsonReply(w, 200, map[string]any{"settings": s.Settings, "plans": plans, "paymentMethods": paymentMethods(s)})
		return
	}
	if path == "login" && r.Method == "POST" {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if !a.integrationLimit("shop:"+host, 20) {
			apiError(w, 429, "کمی بعد تلاش کن")
			return
		}
		a.shopLogin(w, r)
		return
	}
	c, ok := a.portalSession(r)
	if !ok {
		apiError(w, 401, "فروشگاه را از ربات یا لینک خصوصی مدیر باز کن")
		return
	}
	if path == "me" && r.Method == "GET" {
		s := a.salesSnapshot()
		orders := []SalesOrder{}
		services := []SoldService{}
		tickets := []SalesTicket{}
		ledger := []WalletEntry{}
		for _, o := range s.Orders {
			if o.CustomerID == c.ID {
				o.Provision = ProvisionInput{}
				o.Idempotency = ""
				o.TelegramPhoto = ""
				o.ReceiptFile = ""
				orders = append(orders, o)
			}
		}
		for _, v := range s.Services {
			if v.CustomerID == c.ID {
				services = append(services, v)
			}
		}
		for _, v := range s.Tickets {
			if v.CustomerID == c.ID {
				tickets = append(tickets, v)
			}
		}
		for _, v := range s.Ledger {
			if v.CustomerID == c.ID {
				ledger = append(ledger, v)
			}
		}
		jsonReply(w, 200, map[string]any{"customer": c, "orders": lastOrders(orders, 200), "services": services, "tickets": tickets, "ledger": ledger})
		return
	}
	if path == "logout" && r.Method == "POST" {
		cookie, _ := r.Cookie("aria_customer")
		_ = a.salesTxn(func(s *SalesData) error {
			out := []PortalSession{}
			for _, v := range s.Sessions {
				if cookie == nil || v.Hash != hashToken(cookie.Value) {
					out = append(out, v)
				}
			}
			s.Sessions = out
			return nil
		})
		http.SetCookie(w, &http.Cookie{Name: "aria_customer", Path: "/shop", MaxAge: -1, HttpOnly: true})
		jsonReply(w, 200, map[string]bool{"ok": true})
		return
	}
	if !a.integrationLimit("customer:"+c.ID, 120) {
		apiError(w, 429, "درخواست‌ها زیاد است؛ کمی بعد تلاش کن")
		return
	}
	if path == "orders" && r.Method == "POST" {
		var in BuyRequest
		if readBody(w, r, &in, 4096) != nil {
			apiError(w, 400, "فرم سفارش معتبر نیست")
			return
		}
		in.CustomerID = c.ID
		if in.Method == "stars" {
			apiError(w, 400, "پرداخت Stars را از ربات شروع کن")
			return
		}
		o, err := a.newOrder(in)
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		if o.Status == "paid" {
			o, _ = a.fulfillOrder(r.Context(), o.ID)
		}
		o.Provision = ProvisionInput{}
		o.Idempotency = ""
		jsonReply(w, 200, o)
		return
	}
	if path == "trial" && r.Method == "POST" {
		o, err := a.newTrial(r.Context(), c.ID)
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		o.Provision = ProvisionInput{}
		jsonReply(w, 200, o)
		return
	}
	if path == "tickets" && r.Method == "POST" {
		var in struct {
			Text string `json:"text"`
		}
		if readBody(w, r, &in, 8192) != nil {
			apiError(w, 400, "متن معتبر نیست")
			return
		}
		if err := a.createTicket(c.ID, in.Text); err != nil {
			apiError(w, 400, err.Error())
			return
		}
		jsonReply(w, 200, map[string]bool{"ok": true})
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 3 && parts[0] == "orders" && r.Method == "POST" {
		var err error
		switch parts[2] {
		case "pay":
			link, e := a.startPayment(r.Context(), c.ID, parts[1])
			if e != nil {
				apiError(w, 400, e.Error())
				return
			}
			jsonReply(w, 200, map[string]string{"url": link})
			return
		case "check":
			o, e := a.customerPaymentCheck(r.Context(), c.ID, parts[1])
			if e != nil {
				apiError(w, 400, e.Error())
				return
			}
			o.Provision = ProvisionInput{}
			jsonReply(w, 200, o)
			return
		case "cancel":
			err = a.cancelOrder(c.ID, parts[1], false)
		case "receipt":
			err = a.portalReceipt(w, r, c.ID, parts[1])
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		jsonReply(w, 200, map[string]bool{"ok": true})
		return
	}
	if len(parts) == 3 && parts[0] == "services" && parts[2] == "sync" && r.Method == "POST" {
		s := a.salesSnapshot()
		i := serviceIndex(&s, parts[1])
		if i < 0 || s.Services[i].CustomerID != c.ID {
			apiError(w, 404, "سرویس مشتری یافت نشد")
			return
		}
		v, err := a.syncSoldService(r.Context(), parts[1])
		if err != nil {
			apiError(w, 503, "همگام‌سازی سرویس کامل نشد")
			return
		}
		jsonReply(w, 200, v)
		return
	}
	http.NotFound(w, r)
}
func (a *App) createTicket(customer, text string) error {
	text = strings.TrimSpace(text)
	if len([]rune(text)) < 3 || len([]rune(text)) > 2000 {
		return fmt.Errorf("متن تیکت باید ۳ تا ۲۰۰۰ حرف باشد")
	}
	t := a.telegramSnapshot()
	return a.salesTxn(func(s *SalesData) error {
		ci := customerIndex(s, customer)
		if ci < 0 || s.Customers[ci].Blocked {
			return fmt.Errorf("مشتری فعال نیست")
		}
		open := 0
		for _, v := range s.Tickets {
			if v.CustomerID == customer && v.Status == "open" {
				open++
			}
		}
		if open >= 10 {
			return fmt.Errorf("ابتدا تیکت‌های باز را پیگیری کن")
		}
		id := "t-" + randomToken(8)
		s.Tickets = append(s.Tickets, SalesTicket{ID: id, CustomerID: customer, Text: text, Status: "open", Created: time.Now().UnixMilli()})
		addNotice(s, "ticket:"+id, t.ChatID, "تیکت تازه\n"+s.Customers[ci].Name+"\n"+text)
		return nil
	})
}
func (a *App) portalReceipt(w http.ResponseWriter, r *http.Request, customer, id string) error {
	s := a.salesSnapshot()
	i := orderIndex(&s, id)
	if i < 0 || s.Orders[i].CustomerID != customer {
		return fmt.Errorf("سفارش یافت نشد")
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		var in struct {
			Receipt string `json:"receipt"`
		}
		if readBody(w, r, &in, 4096) != nil {
			return fmt.Errorf("رسید معتبر نیست")
		}
		return a.submitReceipt(customer, id, in.Receipt, "", "")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if r.ParseMultipartForm(4<<20) != nil {
		return fmt.Errorf("عکس رسید باید JPEG یا PNG و کمتر از ۴ مگابایت باشد")
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	f, _, err := r.FormFile("photo")
	if err != nil {
		return a.submitReceipt(customer, id, r.FormValue("receipt"), "", "")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4<<20))
	if err != nil || len(b) < 8 {
		return fmt.Errorf("عکس رسید خوانده نشد")
	}
	kind := http.DetectContentType(b)
	if kind != "image/png" && kind != "image/jpeg" {
		return fmt.Errorf("فقط عکس PNG یا JPEG پذیرفته می‌شود")
	}
	dir := filepath.Join(a.cfg.DataDir, "receipts")
	if os.MkdirAll(dir, 0700) != nil {
		return fmt.Errorf("پوشهٔ خصوصی رسید قابل نوشتن نیست")
	}
	name := randomToken(16) + ".receipt"
	path := filepath.Join(dir, name)
	if os.WriteFile(path, b, 0600) != nil {
		return fmt.Errorf("ذخیرهٔ رسید انجام نشد")
	}
	if err = a.submitReceipt(customer, id, r.FormValue("receipt"), name, ""); err != nil {
		_ = os.Remove(path)
	}
	return err
}
func (a *App) adminReceipt(w http.ResponseWriter, r *http.Request, id string) {
	s := a.salesSnapshot()
	i := orderIndex(&s, id)
	if i < 0 {
		http.NotFound(w, r)
		return
	}
	o := s.Orders[i]
	var b []byte
	var err error
	if o.ReceiptFile != "" && filepath.Base(o.ReceiptFile) == o.ReceiptFile {
		b, err = os.ReadFile(filepath.Join(a.cfg.DataDir, "receipts", o.ReceiptFile))
	} else if o.TelegramPhoto != "" {
		b, err = a.telegramPhoto(r.Context(), o.TelegramPhoto)
	} else {
		http.NotFound(w, r)
		return
	}
	if err != nil || len(b) > 4<<20 {
		apiError(w, 502, "عکس رسید دریافت نشد")
		return
	}
	kind := http.DetectContentType(b)
	if kind != "image/jpeg" && kind != "image/png" {
		apiError(w, 400, "فرمت عکس پشتیبانی نمی‌شود")
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}
func (a *App) telegramPhoto(ctx context.Context, id string) ([]byte, error) {
	t := a.telegramSnapshot()
	var f struct {
		Path string `json:"file_path"`
	}
	if err := a.tgCall(ctx, t, "getFile", map[string]string{"file_id": id}, &f); err != nil {
		return nil, err
	}
	if f.Path == "" || strings.Contains(f.Path, "..") || strings.ContainsAny(f.Path, "?#\\") {
		return nil, fmt.Errorf("مسیر عکس معتبر نیست")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.telegram.org/file/bot"+t.Token+"/"+f.Path, nil)
	if err != nil {
		return nil, fmt.Errorf("عکس دریافت نشد")
	}
	cl := a.telegramHTTP
	if cl == nil {
		cl = &http.Client{Timeout: 20 * time.Second, CheckRedirect: noRedirect}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("عکس دریافت نشد")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("عکس دریافت نشد")
	}
	return io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
}
