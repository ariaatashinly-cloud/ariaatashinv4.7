package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type GatewayCredential struct {
	Enabled  bool   `json:"enabled"`
	Merchant string `json:"merchant,omitempty"`
}
type GatewaySettings struct {
	Zarinpal GatewayCredential `json:"zarinpal"`
	Zibal    GatewayCredential `json:"zibal"`
}
type PaymentAttempt struct {
	ID              string `json:"id"`
	OrderID         string `json:"orderId"`
	Provider        string `json:"provider"`
	Merchant        string `json:"merchant"`
	Authority       string `json:"authority"`
	Callback        string `json:"callback"`
	TokenHash       string `json:"tokenHash"`
	AmountIRR       int64  `json:"amountIRR"`
	Status          string `json:"status"`
	Reference       string `json:"reference"`
	VerifyRequested bool   `json:"verifyRequested"`
	Created         int64  `json:"created"`
	Updated         int64  `json:"updated"`
}

func gatewayMethod(m string) bool { return m == "zarinpal" || m == "zibal" }
func gatewayCredential(s SalesData, m string) GatewayCredential {
	if m == "zarinpal" {
		return s.Gateways.Zarinpal
	}
	if m == "zibal" {
		return s.Gateways.Zibal
	}
	return GatewayCredential{}
}
func publicGateways(s SalesData) any {
	return map[string]any{"zarinpal": map[string]bool{"enabled": s.Gateways.Zarinpal.Enabled, "configured": s.Gateways.Zarinpal.Merchant != ""}, "zibal": map[string]bool{"enabled": s.Gateways.Zibal.Enabled, "configured": s.Gateways.Zibal.Merchant != ""}}
}
func paymentMethods(s SalesData) []map[string]string {
	v := []map[string]string{{"id": "manual", "name": "کارت / حساب و رسید"}, {"id": "wallet", "name": "کیف پول"}}
	for _, m := range []string{"zarinpal", "zibal"} {
		g := gatewayCredential(s, m)
		if g.Enabled && g.Merchant != "" {
			label := "زرین‌پال"
			if m == "zibal" {
				label = "زیبال"
			}
			v = append(v, map[string]string{"id": m, "name": label})
		}
	}
	return v
}
func (a *App) saveGateways(v GatewaySettings) error {
	return a.salesTxn(func(s *SalesData) error {
		for _, m := range []string{"zarinpal", "zibal"} {
			next := v.Zarinpal
			old := s.Gateways.Zarinpal
			if m == "zibal" {
				next = v.Zibal
				old = s.Gateways.Zibal
			}
			next.Merchant = strings.TrimSpace(next.Merchant)
			if next.Merchant == "" {
				next.Merchant = old.Merchant
			}
			if next.Enabled && (len(next.Merchant) < 8 || len(next.Merchant) > 128) {
				return fmt.Errorf("مرچنت معتبر برای درگاه فعال لازم است")
			}
			if next.Merchant != "" && !regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`).MatchString(next.Merchant) {
				return fmt.Errorf("قالب مرچنت معتبر نیست")
			}
			if next.Enabled && m == "zibal" && strings.EqualFold(next.Merchant, "zibal") {
				return fmt.Errorf("مرچنت آزمایشی زیبال برای فروش واقعی پذیرفته نمی‌شود")
			}
			if m == "zarinpal" {
				s.Gateways.Zarinpal = next
			} else {
				s.Gateways.Zibal = next
			}
		}
		return nil
	})
}
func (a *App) gatewayCall(ctx context.Context, endpoint string, payload, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("درخواست درگاه معتبر نیست")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	cl := a.paymentHTTP
	if cl == nil {
		cl = &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: noRedirect}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("ارتباط با درگاه تأیید نشد؛ از بررسی پرداخت استفاده کن")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("درگاه پاسخ موفق نداد")
	}
	d := json.NewDecoder(io.LimitReader(resp.Body, 65536))
	if d.Decode(out) != nil {
		return fmt.Errorf("پاسخ درگاه معتبر نیست")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("پاسخ درگاه معتبر نیست")
	}
	return nil
}
func paymentIndex(s *SalesData, id string) int {
	for i := range s.Payments {
		if s.Payments[i].ID == id {
			return i
		}
	}
	return -1
}

func paymentSummaries(s SalesData) []map[string]any {
	out := []map[string]any{}
	for _, p := range s.Payments {
		out = append(out, map[string]any{"id": p.ID, "orderId": p.OrderID, "provider": p.Provider, "authority": p.Authority, "status": p.Status, "reference": p.Reference, "updated": p.Updated})
	}
	return out
}

// An administrator may recover a lost invoice ID from the provider dashboard.
// This only restores the invoice binding; the provider must still verify payment.
func (a *App) recoverPayment(ctx context.Context, orderID, authority string) (SalesOrder, error) {
	authority = strings.TrimSpace(authority)
	a.paymentMu.Lock()
	err := a.salesTxn(func(s *SalesData) error {
		oi := orderIndex(s, orderID)
		if oi < 0 || !gatewayMethod(s.Orders[oi].Method) {
			return fmt.Errorf("سفارش درگاه پیدا نشد")
		}
		pi := -1
		for i := len(s.Payments) - 1; i >= 0; i-- {
			if s.Payments[i].OrderID == orderID {
				pi = i
				break
			}
		}
		if pi < 0 {
			return fmt.Errorf("تلاش پرداخت ثبت‌شده پیدا نشد")
		}
		p := &s.Payments[pi]
		if p.Provider == "zarinpal" {
			if !regexp.MustCompile(`^[A-Za-z0-9]{16,80}$`).MatchString(authority) {
				return fmt.Errorf("Authority زرین‌پال معتبر نیست")
			}
		} else {
			n, err := strconv.ParseInt(authority, 10, 64)
			if err != nil || n <= 0 || strconv.FormatInt(n, 10) != authority {
				return fmt.Errorf("trackId زیبال معتبر نیست")
			}
		}
		if p.Status != "creating" && p.Status != "uncertain" {
			if p.Authority == authority && (p.Status == "pending" || p.Status == "verified") {
				return nil
			}
			return fmt.Errorf("این تلاش پرداخت قابل تغییر نیست")
		}
		for _, v := range s.Payments {
			if v.ID != p.ID && v.Provider == p.Provider && v.Merchant == p.Merchant && v.Authority == authority {
				return fmt.Errorf("شناسه به سفارش دیگری تعلق دارد")
			}
		}
		p.Authority = authority
		p.Status = "pending"
		p.Updated = time.Now().UnixMilli()
		return nil
	})
	a.paymentMu.Unlock()
	if err != nil {
		return SalesOrder{}, err
	}
	s := a.salesSnapshot()
	oi := orderIndex(&s, orderID)
	return a.customerPaymentCheck(ctx, s.Orders[oi].CustomerID, orderID)
}
func paymentURL(p PaymentAttempt) string {
	if p.Provider == "zarinpal" {
		return "https://www.zarinpal.com/pg/StartPay/" + url.PathEscape(p.Authority)
	}
	return "https://gateway.zibal.ir/start/" + url.PathEscape(p.Authority)
}
func (a *App) startPayment(ctx context.Context, customer, orderID string) (string, error) {
	a.paymentMu.Lock()
	defer a.paymentMu.Unlock()
	s := a.salesSnapshot()
	oi := orderIndex(&s, orderID)
	ci := customerIndex(&s, customer)
	if oi < 0 || ci < 0 || s.Customers[ci].Blocked || s.Orders[oi].CustomerID != customer {
		return "", fmt.Errorf("سفارش مشتری یافت نشد")
	}
	o := s.Orders[oi]
	if !gatewayMethod(o.Method) || o.Currency != "IRT" || o.Amount < 1000 || o.Amount > 1000000000 || o.Status != "pending" {
		return "", fmt.Errorf("سفارش فعال قابل پرداخت با درگاه نیست")
	}
	for i := len(s.Payments) - 1; i >= 0; i-- {
		p := s.Payments[i]
		if p.OrderID != o.ID {
			continue
		}
		if p.Status == "pending" && p.Authority != "" {
			return paymentURL(p), nil
		}
		if p.Status == "creating" || p.Status == "uncertain" {
			return "", fmt.Errorf("نتیجهٔ ایجاد درگاه هنوز مشخص نیست؛ سفارش را برای بررسی به پشتیبانی بده")
		}
	}
	g := gatewayCredential(s, o.Method)
	if !g.Enabled || g.Merchant == "" {
		return "", fmt.Errorf("درگاه انتخاب‌شده فعال و تنظیم‌شده نیست")
	}
	origin, err := validatePublicURL(a.snapshot().Settings.PublicURL)
	if err != nil || origin.Scheme != "https" {
		return "", fmt.Errorf("برای درگاه، آدرس عمومی HTTPS پنل باید آماده باشد")
	}
	token := randomToken(32)
	now := time.Now().UnixMilli()
	p := PaymentAttempt{ID: "pay-" + randomToken(8), OrderID: o.ID, Provider: o.Method, Merchant: g.Merchant, TokenHash: hashToken(token), AmountIRR: o.Amount * 10, Status: "creating", Created: now, Updated: now}
	p.Callback = strings.TrimRight(origin.String(), "/") + "/pay/callback/" + token
	err = a.salesTxn(func(s *SalesData) error {
		oi := orderIndex(s, o.ID)
		if oi < 0 || s.Orders[oi].Status != "pending" {
			return fmt.Errorf("وضعیت سفارش تغییر کرده است")
		}
		s.Payments = append(s.Payments, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	if p.Provider == "zarinpal" {
		var r struct {
			Data struct {
				Code      int    `json:"code"`
				Authority string `json:"authority"`
			} `json:"data"`
			Errors struct {
				Code int `json:"code"`
			} `json:"errors"`
		}
		err = a.gatewayCall(ctx, "https://api.zarinpal.com/pg/v4/payment/request.json", map[string]any{"merchant_id": p.Merchant, "amount": p.AmountIRR, "currency": "IRR", "callback_url": p.Callback, "description": "سفارش " + o.ID + " · " + s.Settings.Name}, &r)
		if err == nil {
			if r.Data.Code != 100 {
				if r.Data.Code != 0 || r.Errors.Code < 0 {
					p.Status = "failed"
				}
				err = fmt.Errorf("درگاه ایجاد نشد؛ مرچنت و مبلغ را بررسی کن")
			} else if !regexp.MustCompile(`^[A-Za-z0-9]{16,80}$`).MatchString(r.Data.Authority) {
				err = fmt.Errorf("شناسهٔ درگاه معتبر نیست")
			} else {
				p.Authority = r.Data.Authority
			}
		}
	} else {
		var r struct {
			Result  int   `json:"result"`
			TrackID int64 `json:"trackId"`
		}
		err = a.gatewayCall(ctx, "https://gateway.zibal.ir/v1/request", map[string]any{"merchant": p.Merchant, "amount": p.AmountIRR, "callbackUrl": p.Callback, "description": "سفارش " + o.ID, "orderId": p.ID}, &r)
		if err == nil {
			if r.Result != 100 {
				if r.Result != 0 {
					p.Status = "failed"
				}
				err = fmt.Errorf("درگاه ایجاد نشد؛ مرچنت و مبلغ را بررسی کن")
			} else if r.TrackID <= 0 {
				err = fmt.Errorf("شناسهٔ درگاه معتبر نیست")
			} else {
				p.Authority = strconv.FormatInt(r.TrackID, 10)
			}
		}
	}
	if err != nil && p.Status != "failed" {
		p.Status = "uncertain"
	}
	if err == nil {
		p.Status = "pending"
	}
	p.Updated = time.Now().UnixMilli()
	saveErr := a.salesTxn(func(s *SalesData) error {
		pi := paymentIndex(s, p.ID)
		if pi < 0 {
			return fmt.Errorf("تلاش پرداخت یافت نشد")
		}
		for _, other := range s.Payments {
			if other.ID != p.ID && other.Provider == p.Provider && other.Merchant == p.Merchant && other.Authority != "" && other.Authority == p.Authority {
				return fmt.Errorf("شناسهٔ درگاه قبلاً برای سفارش دیگر ثبت شده است")
			}
		}
		s.Payments[pi] = p
		return nil
	})
	if saveErr != nil {
		return "", saveErr
	}
	if err != nil {
		return "", err
	}
	return paymentURL(p), nil
}
func providerReference(raw json.RawMessage) string {
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}
func (a *App) verifyGateway(ctx context.Context, p PaymentAttempt) (string, error) {
	if p.Provider == "zarinpal" {
		var r struct {
			Data struct {
				Code int             `json:"code"`
				Ref  json.RawMessage `json:"ref_id"`
			} `json:"data"`
		}
		if err := a.gatewayCall(ctx, "https://api.zarinpal.com/pg/v4/payment/verify.json", map[string]any{"merchant_id": p.Merchant, "amount": p.AmountIRR, "authority": p.Authority}, &r); err != nil {
			return "", err
		}
		ref := providerReference(r.Data.Ref)
		if (r.Data.Code != 100 && r.Data.Code != 101) || !regexp.MustCompile(`^[1-9][0-9]{0,30}$`).MatchString(ref) {
			return "", fmt.Errorf("پرداخت توسط زرین‌پال تأیید نشده است")
		}
		return ref, nil
	}
	track, err := strconv.ParseInt(p.Authority, 10, 64)
	if err != nil || track <= 0 {
		return "", fmt.Errorf("شناسهٔ تراکنش معتبر نیست")
	}
	var r struct {
		Result  int             `json:"result"`
		Status  int             `json:"status"`
		Amount  int64           `json:"amount"`
		Ref     json.RawMessage `json:"refNumber"`
		OrderID string          `json:"orderId"`
	}
	if err = a.gatewayCall(ctx, "https://gateway.zibal.ir/v1/verify", map[string]any{"merchant": p.Merchant, "trackId": track}, &r); err != nil {
		return "", err
	}
	if r.Result == 201 {
		r = struct {
			Result  int             `json:"result"`
			Status  int             `json:"status"`
			Amount  int64           `json:"amount"`
			Ref     json.RawMessage `json:"refNumber"`
			OrderID string          `json:"orderId"`
		}{}
		if err = a.gatewayCall(ctx, "https://gateway.zibal.ir/v1/inquiry", map[string]any{"merchant": p.Merchant, "trackId": track}, &r); err != nil {
			return "", err
		}
	}
	ref := providerReference(r.Ref)
	if r.Result != 100 || r.Status != 1 || r.Amount != p.AmountIRR || r.OrderID != "" && r.OrderID != p.ID || !regexp.MustCompile(`^[1-9][0-9]{0,30}$`).MatchString(ref) {
		return "", fmt.Errorf("پرداخت، مبلغ یا شناسه توسط زیبال تأیید نشد")
	}
	return ref, nil
}
func (a *App) verifyPaymentAttempt(ctx context.Context, id string) (SalesOrder, error) {
	a.paymentMu.Lock()
	s := a.salesSnapshot()
	pi := paymentIndex(&s, id)
	if pi < 0 {
		a.paymentMu.Unlock()
		return SalesOrder{}, fmt.Errorf("تلاش پرداخت یافت نشد")
	}
	p := s.Payments[pi]
	oi := orderIndex(&s, p.OrderID)
	if oi < 0 || p.Authority == "" || p.Status == "failed" || p.Status == "uncertain" || p.AmountIRR != s.Orders[oi].Amount*10 || p.Provider != s.Orders[oi].Method {
		a.paymentMu.Unlock()
		return SalesOrder{}, fmt.Errorf("تراکنش ثبت‌شده قابل بررسی نیست")
	}
	var err error
	if p.Status != "verified" {
		// Persist the recovery request before calling an API that can mark paid on its server.
		err = a.salesTxn(func(s *SalesData) error {
			i := paymentIndex(s, p.ID)
			s.Payments[i].VerifyRequested = true
			s.Payments[i].Updated = time.Now().UnixMilli()
			return nil
		})
		var ref string
		if err == nil {
			ref, err = a.verifyGateway(ctx, p)
		}
		if err == nil {
			err = a.salesTxn(func(s *SalesData) error {
				if e := confirmPaymentTxn(s, p.OrderID, "gateway:"+p.Provider+":"+ref, p.AmountIRR/10, p.Provider); e != nil {
					return e
				}
				i := paymentIndex(s, p.ID)
				if i < 0 {
					return fmt.Errorf("تلاش پرداخت یافت نشد")
				}
				s.Payments[i].Status = "verified"
				s.Payments[i].Reference = ref
				s.Payments[i].VerifyRequested = false
				s.Payments[i].Updated = time.Now().UnixMilli()
				return nil
			})
		}
	}
	a.paymentMu.Unlock()
	if err != nil {
		return SalesOrder{}, err
	}
	o, err := a.fulfillOrder(ctx, p.OrderID)
	return o, err
}
func (a *App) customerPaymentCheck(ctx context.Context, customer, id string) (SalesOrder, error) {
	s := a.salesSnapshot()
	oi := orderIndex(&s, id)
	if oi < 0 || s.Orders[oi].CustomerID != customer {
		return SalesOrder{}, fmt.Errorf("سفارش مشتری یافت نشد")
	}
	for i := len(s.Payments) - 1; i >= 0; i-- {
		if s.Payments[i].OrderID == id {
			return a.verifyPaymentAttempt(ctx, s.Payments[i].ID)
		}
	}
	return SalesOrder{}, fmt.Errorf("ابتدا لینک درگاه را ایجاد کن")
}
func (a *App) reconcilePayments(ctx context.Context) {
	for _, p := range a.salesSnapshot().Payments {
		if p.VerifyRequested && p.Authority != "" && p.Status == "pending" && time.Now().UnixMilli()-p.Updated > 60000 {
			job, cancel := context.WithTimeout(ctx, 20*time.Second)
			_, _ = a.verifyPaymentAttempt(job, p.ID)
			cancel()
			if ctx.Err() != nil {
				return
			}
		}
	}
}
func (a *App) paymentCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "GET required", 405)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/pay/callback/")
	if len(token) != 64 {
		http.NotFound(w, r)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid query", 400)
		return
	}
	for _, v := range q {
		if len(v) != 1 {
			http.Error(w, "duplicate query", 400)
			return
		}
	}
	var p PaymentAttempt
	hash := hashToken(token)
	for _, v := range a.salesSnapshot().Payments {
		if subtle.ConstantTimeCompare([]byte(v.TokenHash), []byte(hash)) == 1 {
			p = v
			break
		}
	}
	if p.ID == "" {
		http.NotFound(w, r)
		return
	}
	authority := q.Get("Authority")
	if p.Provider == "zibal" {
		authority = q.Get("trackId")
	}
	if p.Authority == "" || authority != p.Authority {
		http.Error(w, "payment mismatch", 400)
		return
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !a.integrationLimit("pay:"+host+":"+p.ID, 20) {
		http.Error(w, "try later", 429)
		return
	}
	job, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	o, err := a.verifyPaymentAttempt(job, p.ID)
	title := "پرداخت هنوز تأیید نشده است"
	message := "به ربات برگرد و بررسی پرداخت را بزن. صرف نمایش این صفحه پرداخت را تأیید نمی‌کند."
	if err == nil && o.Status == "fulfilled" {
		title = "پرداخت تأیید شد؛ سفارش تحویل شد"
		message = "کانفیگ یا اعتبار در حساب شما و ربات قرار گرفت."
	} else {
		s := a.salesSnapshot()
		pi := paymentIndex(&s, p.ID)
		if pi >= 0 && s.Payments[pi].Status == "verified" {
			title = "پرداخت تأیید شد؛ تحویل در حال بازیابی است"
			message = "پرداخت ثبت شده است. دوباره پرداخت نکن؛ پنل تحویل را پیگیری می‌کند."
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `<!doctype html><html lang="fa" dir="rtl"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>ariaatashin · پرداخت</title><link rel="stylesheet" href="/style.css"><link rel="stylesheet" href="/sales.css"><link rel="stylesheet" href="/shop.css"><body class="shop-page"><main class="shop-main"><article class="panel shop-service"><h1>%s</h1><p>%s</p><a class="button primary" href="/shop">بازگشت به فروشگاه</a></article></main></body></html>`, html.EscapeString(title), html.EscapeString(message))
}
