package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fakeJSON(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func gatewayFixture(t *testing.T, method string) (*App, SalesCustomer, SalesOrder) {
	t.Helper()
	a, c := salesFixture(t)
	if err := a.saveGateways(GatewaySettings{Zarinpal: GatewayCredential{Enabled: true, Merchant: "merchant-original-123456"}, Zibal: GatewayCredential{Enabled: true, Merchant: "merchant-zibal-123456"}}); err != nil {
		t.Fatal(err)
	}
	o, err := a.newOrder(BuyRequest{CustomerID: c.ID, Kind: "topup", Method: method, Amount: 12345, Idempotency: "gateway-fixture-001"})
	if err != nil {
		t.Fatal(err)
	}
	return a, c, o
}
func TestGatewayConcurrentVerificationRestartCreditsOnce(t *testing.T) {
	a, c, o := gatewayFixture(t, "zarinpal")
	var requests, verifies atomic.Int32
	transport := tgTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.zarinpal.com" || r.Method != "POST" {
			t.Error("untrusted gateway endpoint")
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["merchant_id"] != "merchant-original-123456" || in["amount"] != float64(123450) {
			t.Error("merchant/amount changed", in)
		}
		if strings.HasSuffix(r.URL.Path, "request.json") {
			requests.Add(1)
			if !strings.HasPrefix(in["callback_url"].(string), "https://panel.example/pay/callback/") {
				t.Error("wrong callback")
			}
			return fakeJSON(`{"data":{"code":100,"authority":"A000000000000000000001"}}`), nil
		}
		verifies.Add(1)
		return fakeJSON(`{"data":{"code":100,"ref_id":99119911}}`), nil
	})
	a.paymentHTTP = &http.Client{Transport: transport}
	link, err := a.startPayment(context.Background(), c.ID, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	link2, err := a.startPayment(context.Background(), c.ID, o.ID)
	if err != nil || link != link2 || requests.Load() != 1 {
		t.Fatal("invoice duplicated", err)
	}
	_ = a.saveGateways(GatewaySettings{Zarinpal: GatewayCredential{Enabled: true, Merchant: "merchant-rotated-999"}, Zibal: GatewayCredential{}})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := a.customerPaymentCheck(context.Background(), c.ID, o.ID)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	s := a.salesSnapshot()
	if s.Customers[0].Balance != 12345 || len(s.Ledger) != 1 || s.Payments[0].Status != "verified" || verifies.Load() != 1 {
		t.Fatal("duplicate credit or unverified payment")
	}
	b, err := NewApp(a.cfg, a.base)
	if err != nil {
		t.Fatal(err)
	}
	b.paymentHTTP = &http.Client{Transport: transport}
	if _, err = b.customerPaymentCheck(context.Background(), c.ID, o.ID); err != nil {
		t.Fatal(err)
	}
	if b.salesSnapshot().Customers[0].Balance != 12345 || verifies.Load() != 1 {
		t.Fatal("restart recharged")
	}
}
func TestGatewayCallbackCannotForgeSuccessAmountOrOwner(t *testing.T) {
	a, c, o := gatewayFixture(t, "zarinpal")
	a.paymentHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "request.json") {
			return fakeJSON(`{"data":{"code":100,"authority":"A000000000000000000002"}}`), nil
		}
		return fakeJSON(`{"errors":{"code":-22}}`), nil
	})}
	_, err := a.startPayment(context.Background(), c.ID, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := a.salesSnapshot().Payments[0]
	for _, q := range []string{"?Authority=wrong&Status=OK", "?Authority=" + p.Authority + "&Authority=" + p.Authority, "?Authority=" + p.Authority + "&Status=OK&amount=1"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", p.Callback+q, nil))
		if w.Code >= 500 {
			t.Fatal(w.Code)
		}
	}
	if a.salesSnapshot().Customers[0].Balance != 0 || a.salesSnapshot().Orders[0].Status != "pending" {
		t.Fatal("callback granted credit")
	}
	if _, err = a.customerPaymentCheck(context.Background(), "another", o.ID); err == nil {
		t.Fatal("cross customer check")
	}
	if _, err = a.startPayment(context.Background(), "another", o.ID); err == nil {
		t.Fatal("cross customer invoice")
	}
	if err = a.cancelOrder(c.ID, o.ID, false); err == nil {
		t.Fatal("late payment can be cancelled")
	}
}
func TestGatewayUncertainRequestIsNeverBlindlyRecreated(t *testing.T) {
	a, c, o := gatewayFixture(t, "zibal")
	calls := 0
	a.paymentHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("lost network after provider commit")
	})}
	_, err := a.startPayment(context.Background(), c.ID, o.ID)
	if err == nil {
		t.Fatal("unknown create claimed success")
	}
	_, err = a.startPayment(context.Background(), c.ID, o.ID)
	if err == nil || calls != 1 || a.salesSnapshot().Payments[0].Status != "uncertain" {
		t.Fatal("blind invoice replay")
	}
}

func TestGatewayLostInvoiceRecoveryStillRequiresProviderProof(t *testing.T) {
	a, c, o := gatewayFixture(t, "zarinpal")
	paid := false
	a.paymentHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "request.json") {
			return nil, fmt.Errorf("lost create response")
		}
		if paid {
			return fakeJSON(`{"data":{"code":100,"ref_id":668899}}`), nil
		}
		return fakeJSON(`{"errors":{"code":-22}}`), nil
	})}
	_, _ = a.startPayment(context.Background(), c.ID, o.ID)
	if _, e := a.recoverPayment(context.Background(), o.ID, "bad"); e == nil {
		t.Fatal("malformed authority")
	}
	if _, e := a.recoverPayment(context.Background(), o.ID, "A000000000000000000006"); e == nil {
		t.Fatal("unpaid recovered invoice approved")
	}
	if a.salesSnapshot().Customers[0].Balance != 0 {
		t.Fatal("recovery credited without proof")
	}
	paid = true
	if _, e := a.recoverPayment(context.Background(), o.ID, "A000000000000000000006"); e != nil {
		t.Fatal(e)
	}
	if a.salesSnapshot().Customers[0].Balance != 12345 {
		t.Fatal("recovery failed")
	}
	if _, e := a.recoverPayment(context.Background(), o.ID, "A000000000000000000007"); e == nil {
		t.Fatal("verified binding changed")
	}
}
func TestMalformedGatewayRequestIsUncertain(t *testing.T) {
	for _, method := range []string{"zarinpal", "zibal"} {
		a, c, o := gatewayFixture(t, method)
		count := 0
		a.paymentHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) { count++; return fakeJSON(`{}`), nil })}
		_, _ = a.startPayment(context.Background(), c.ID, o.ID)
		_, _ = a.startPayment(context.Background(), c.ID, o.ID)
		if count != 1 || a.salesSnapshot().Payments[0].Status != "uncertain" {
			t.Fatal("malformed response replayed", method)
		}
	}
}
func TestTelegramDiscoveryIsPrivateReadOnlyAndDoesNotEnable(t *testing.T) {
	a := testApp(t)
	token := "123456:" + strings.Repeat("a", 35)
	a.telegramHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "getUpdates") {
			t.Error("discovery wrote remote state")
		}
		return fakeJSON(`{"ok":true,"result":[{"message":{"chat":{"id":555,"type":"private"},"from":{"id":555,"first_name":"من"}}},{"message":{"chat":{"id":-99,"type":"group"},"from":{"id":555}}},{"message":{"chat":{"id":777,"type":"private"},"from":{"id":777,"is_bot":true}}}]}`), nil
	})}
	w := httptest.NewRecorder()
	a.discoverTelegram(w, browserRequest("POST", "/api/telegram/discover", "https://panel.example", fmt.Sprintf(`{"token":"%s"}`, token)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "555") || strings.Contains(w.Body.String(), "777") || strings.Contains(w.Body.String(), token) {
		t.Fatal("invalid private discovery")
	}
	if a.telegramSnapshot().Enabled || a.telegramSnapshot().ChatID != 0 {
		t.Fatal("discovery granted admin")
	}
	a.telegram.Enabled = true
	w = httptest.NewRecorder()
	a.discoverTelegram(w, httptest.NewRequest("POST", "/api/telegram/discover", strings.NewReader(`{}`)))
	if w.Code != 409 {
		t.Fatal("polling conflict not refused")
	}
}
func TestZibalVerifiedAmountAndInquiryRecovery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		amount   int64
		status   int
		ref      string
		expected bool
	}{{"correct", 123450, 1, "123987", true}, {"wrong amount", 123451, 1, "123987", false}, {"unpaid", 123450, -1, "123987", false}, {"missing reference", 123450, 1, "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			a, c, o := gatewayFixture(t, "zibal")
			a.paymentHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/v1/request":
					return fakeJSON(`{"result":100,"trackId":12345678}`), nil
				case "/v1/verify":
					return fakeJSON(`{"result":201}`), nil
				case "/v1/inquiry":
					return fakeJSON(fmt.Sprintf(`{"result":100,"status":%d,"amount":%d,"refNumber":"%s"}`, tc.status, tc.amount, tc.ref)), nil
				}
				t.Fatal(r.URL)
				return nil, nil
			})}
			_, err := a.startPayment(context.Background(), c.ID, o.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.customerPaymentCheck(context.Background(), c.ID, o.ID)
			if (err == nil) != tc.expected {
				t.Fatal("wrong verification outcome", err)
			}
			balance := a.salesSnapshot().Customers[0].Balance
			if (balance == 12345) != tc.expected {
				t.Fatal("unverified credit")
			}
		})
	}
}
func TestGatewayProvider101RecoveryAfterLocalWriteFailure(t *testing.T) {
	a, c, o := gatewayFixture(t, "zarinpal")
	calls := 0
	a.paymentHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "request.json") {
			return fakeJSON(`{"data":{"code":100,"authority":"A000000000000000000003"}}`), nil
		}
		calls++
		if calls == 1 {
			if err := os.Rename(filepath.Join(a.cfg.DataDir, "aria-commerce.json"), filepath.Join(a.cfg.DataDir, "commerce-before-failure.json")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(a.cfg.DataDir, "aria-commerce.json"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		return fakeJSON(`{"data":{"code":101,"ref_id":"998822"}}`), nil
	})}
	_, err := a.startPayment(context.Background(), c.ID, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.customerPaymentCheck(context.Background(), c.ID, o.ID)
	if err == nil {
		t.Fatal("disk failure ignored")
	}
	_ = os.Remove(filepath.Join(a.cfg.DataDir, "aria-commerce.json"))
	_ = os.Rename(filepath.Join(a.cfg.DataDir, "commerce-before-failure.json"), filepath.Join(a.cfg.DataDir, "aria-commerce.json"))
	b, e := NewApp(a.cfg, a.base)
	if e != nil {
		t.Fatal(e)
	}
	b.paymentHTTP = a.paymentHTTP
	_, err = b.customerPaymentCheck(context.Background(), c.ID, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.salesSnapshot().Customers[0].Balance != 12345 || len(b.salesSnapshot().Ledger) != 1 {
		t.Fatal("101 recovery lost credit")
	}
}
func TestGatewaySecretsAreNotInPublicAPIOrPortableBackup(t *testing.T) {
	a, c, o := gatewayFixture(t, "zarinpal")
	a.paymentHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		return fakeJSON(`{"data":{"code":100,"authority":"A000000000000000000004"}}`), nil
	})}
	_, _ = a.startPayment(context.Background(), c.ID, o.ID)
	for _, path := range []string{"/api/sales/backup", "/api/sales/gateways"} {
		w := httptest.NewRecorder()
		a.salesAPI(w, httptest.NewRequest("GET", path, nil))
		for _, secret := range []string{"merchant-original-123456", a.salesSnapshot().Payments[0].TokenHash} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("secret in", path)
			}
		}
	}
	b, _ := json.Marshal(a.publicSales())
	if strings.Contains(string(b), "merchant-original-123456") {
		t.Fatal("public merchant")
	}
}
func botText(text string, id int64) tgUpdate {
	m := tgMessage{Text: text, Date: time.Now().Unix()}
	m.Chat.ID = id
	m.Chat.Type = "private"
	m.From.ID = id
	m.From.FirstName = "خریدار"
	return tgUpdate{Message: m}
}
func TestBotCallbacksCannotReplayWalletDebitOrCrossCustomer(t *testing.T) {
	a, c := salesFixture(t)
	_ = a.salesTxn(func(s *SalesData) error { s.Customers[0].Balance = 50000; return nil })
	f := newBotFlow(c.ID, "buy", "payment")
	f.PlanID = "p-test"
	f.QuotedAmount = 10000
	_ = a.saveBotFlow(f)
	tg := TelegramConfig{}
	for i := 0; i < 2; i++ {
		_, err := a.customerBotUI(context.Background(), tg, botText("", 555), c, "ui:pay:"+f.Nonce+":wallet", fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
	}
	s := a.salesSnapshot()
	if len(s.Orders) != 1 || len(s.Ledger) != 1 || s.Customers[0].Balance != 40000 {
		t.Fatal("callback debited twice")
	}
	other, e := a.createCustomer("دیگری", 777)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = a.customerBotUI(context.Background(), tg, botText("", 777), other, "ui:pay:"+f.Nonce+":wallet", "other")
	if len(a.salesSnapshot().Orders) != 1 {
		t.Fatal("nonce ownership broken")
	}
	b, e := NewApp(a.cfg, a.base)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = b.customerBotUI(context.Background(), tg, botText("", 555), c, "ui:pay:"+f.Nonce+":wallet", "restart")
	if len(b.salesSnapshot().Ledger) != 1 {
		t.Fatal("restart callback debited")
	}
}
func TestBotQuoteFenceAndCouponBeforePayment(t *testing.T) {
	a, c := salesFixture(t)
	f := newBotFlow(c.ID, "buy", "coupon")
	f.PlanID = "p-test"
	f.QuotedAmount = 10000
	_ = a.saveBotFlow(f)
	_ = a.salesTxn(func(s *SalesData) error {
		s.Coupons = []Coupon{{Code: "FIRE10", Percent: 10, Limit: 1, Enabled: true}}
		s.Customers[0].Balance = 50000
		return nil
	})
	handled, e := a.customerBotUI(context.Background(), TelegramConfig{}, botText("FIRE10", 555), c, "FIRE10", "coupon")
	if !handled || e != nil {
		t.Fatal(e)
	}
	f = a.botFlow(c.ID)
	if f.QuotedAmount != 9000 || f.Coupon != "FIRE10" {
		t.Fatal("coupon quote")
	}
	_ = a.salesTxn(func(s *SalesData) error { s.Plans[0].Price = 20000; return nil })
	_, _ = a.customerBotUI(context.Background(), TelegramConfig{}, botText("", 555), c, "ui:pay:"+f.Nonce+":wallet", "pay")
	s := a.salesSnapshot()
	if len(s.Orders) != 0 || s.Customers[0].Balance != 50000 || s.Coupons[0].Used != 0 {
		t.Fatal("stale quote charged")
	}
}
func TestTrialUniqueOrderSurvivesFailureAndRestart(t *testing.T) {
	a, c := salesFixture(t)
	_ = a.salesTxn(func(s *SalesData) error {
		s.Settings.TrialEnabled = true
		s.Settings.TrialDays = 1
		s.Settings.TrialQuotaGB = .2
		s.Settings.TrialProfiles = []string{"vless-ws"}
		return nil
	})
	for i := 0; i < 2; i++ {
		_, _ = a.newTrial(context.Background(), c.ID)
	}
	s := a.salesSnapshot()
	if len(s.Orders) != 1 || s.Orders[0].Kind != "trial" || len(s.Ledger) != 0 || s.Customers[0].TrialOrder == "" {
		t.Fatal("duplicate free trial")
	}
	b, e := NewApp(a.cfg, a.base)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = b.newTrial(context.Background(), c.ID)
	if len(b.salesSnapshot().Orders) != 1 {
		t.Fatal("restart free trial duplicated")
	}
}
func TestBotAdminButtonsOwnershipAndVerifiedReceipt(t *testing.T) {
	a, c := salesFixture(t)
	tg := TelegramConfig{Enabled: true, Token: "123456:" + strings.Repeat("a", 35), ChatID: 99}
	a.telegram = tg
	o, e := a.newOrder(BuyRequest{CustomerID: c.ID, Kind: "topup", Method: "manual", Amount: 50000, Idempotency: "admin-bot-button"})
	if e != nil {
		t.Fatal(e)
	}
	_ = a.submitReceipt(c.ID, o.ID, "reference", "", "")
	u := botText("ui:admin-approve:"+o.ID, 555)
	u.ID = 51
	if e = a.processUpdate(context.Background(), tg, u); e != nil {
		t.Fatal(e)
	}
	if a.salesSnapshot().Orders[0].Status != "receipt" {
		t.Fatal("customer approved own receipt")
	}
	u = botText("ui:admin-approve:"+o.ID, 99)
	u.ID = 52
	if e = a.processUpdate(context.Background(), tg, u); e != nil {
		t.Fatal(e)
	}
	if a.salesSnapshot().Customers[0].Balance != 50000 {
		t.Fatal("admin approval failed")
	}
	u.ID = 53
	_ = a.processUpdate(context.Background(), tg, u)
	if len(a.salesSnapshot().Ledger) != 1 {
		t.Fatal("admin button duplicate credit")
	}
}
func TestConfigDocumentContainsEveryLinkAndPrivateOwner(t *testing.T) {
	a, c := salesFixture(t)
	v := SoldService{ID: "s-doc", CustomerID: c.ID, Name: "تست", Subscription: "https://panel.example/sub/secret", Links: []string{strings.Repeat("vless://config", 450), "vmess://config-two"}}
	_ = a.salesTxn(func(s *SalesData) error { s.Services = []SoldService{v}; return nil })
	calls := 0
	a.telegramHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/bottoken/sendDocument" {
			t.Error(r.URL.Path)
		}
		if err := r.ParseMultipartForm(2 << 20); err != nil {
			t.Error(err)
		}
		file, _, err := r.FormFile("document")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(file)
		_ = file.Close()
		if !strings.Contains(string(b), v.Links[0]) || !strings.Contains(string(b), v.Links[1]) || r.FormValue("chat_id") != "555" {
			t.Error("config truncated or wrong owner")
		}
		return fakeJSON(`{"ok":true,"result":{}}`), nil
	})}
	if e := a.tgRichSend(context.Background(), TelegramConfig{Token: "token", ChatID: 555}, SalesNotice{ChatID: 555, DocumentService: v.ID}); e != nil {
		t.Fatal(e)
	}
	if e := a.tgRichSend(context.Background(), TelegramConfig{Token: "token", ChatID: 777}, SalesNotice{ChatID: 777, DocumentService: v.ID}); e == nil || calls != 1 {
		t.Fatal("file leaked to other owner")
	}
}
func TestBankNormalizationAndReminderThresholds(t *testing.T) {
	a, _ := salesFixture(t)
	s := ShopSettings{Name: "ariaatashin", CardNumber: "۶۲۱۹-۸۶۱۰-۰۰۰۰-۱۲۳۴", IBAN: "IR820540102680020817909002"}
	if e := validateShopSettings(&s, a); e != nil {
		t.Fatal(e)
	}
	if s.CardNumber != "6219861000001234" {
		t.Fatal(s.CardNumber)
	}
	s.IBAN = "IR000000000000000000000000"
	if e := validateShopSettings(&s, a); e == nil {
		t.Fatal("bad IBAN accepted")
	}
	now := time.Now().UnixMilli()
	v := SoldService{Quota: 100, Used: 96}
	key, _ := serviceReminder(v, now)
	if !strings.HasSuffix(key, ":95") {
		t.Fatal(key)
	}
	v.Expiry = now + 1000
	key, _ = serviceReminder(v, now)
	if !strings.HasSuffix(key, ":24h") {
		t.Fatal(key)
	}
}
func TestShopGatewayRoutesRequireOwnerAndSameOrigin(t *testing.T) {
	a, _, o := gatewayFixture(t, "zarinpal")
	for _, path := range []string{"/shop/api/orders/" + url.PathEscape(o.ID) + "/pay", "/shop/api/orders/" + o.ID + "/check", "/shop/api/trial"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader("{}")))
		if w.Code != 403 {
			t.Fatal("CSRF guard missing", w.Code)
		}
	}
}
