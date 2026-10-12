package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

func salesFixture(t *testing.T) (*App, SalesCustomer) {
	t.Helper()
	a := testApp(t)
	c, err := a.createCustomer("مشتری تست", 555)
	if err != nil {
		t.Fatal(err)
	}
	err = a.salesTxn(func(s *SalesData) error {
		s.Settings.Enabled = true
		s.Plans = []SalesPlan{{ID: "p-test", Name: "پلن تست", Price: 10000, Stars: 5, QuotaGB: 5, Days: 30, Profiles: []string{"vless-ws"}, Enabled: true}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, c
}
func TestWalletDuplicateApprovalSurvivesRestart(t *testing.T) {
	a, c := salesFixture(t)
	o, e := a.newOrder(BuyRequest{CustomerID: c.ID, Kind: "topup", Method: "manual", Amount: 50000, Idempotency: "topup-unique"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.approveOrder(context.Background(), o.ID); e == nil {
		t.Fatal("unverified receipt approved")
	}
	if e = a.submitReceipt(c.ID, o.ID, "bank reference 123", "", ""); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := a.approveOrder(context.Background(), o.ID); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	b, e := NewApp(a.cfg, a.base)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.approveOrder(context.Background(), o.ID); e != nil {
		t.Fatal(e)
	}
	s := b.salesSnapshot()
	if s.Customers[0].Balance != 50000 || len(s.Ledger) != 1 || len(s.Orders) != 1 {
		t.Fatal("duplicate wallet credit", s)
	}
	st, _ := os.Stat(filepath.Join(a.cfg.DataDir, "aria-commerce.json"))
	if st.Mode().Perm() != 0600 {
		t.Fatal("private commerce file required")
	}
}
func TestOrderIdempotencyAndWalletDebit(t *testing.T) {
	a, c := salesFixture(t)
	_ = a.salesTxn(func(s *SalesData) error { s.Customers[0].Balance = 30000; return nil })
	req := BuyRequest{CustomerID: c.ID, PlanID: "p-test", Kind: "buy", Method: "wallet", Idempotency: "purchase-unique"}
	o, e := a.newOrder(req)
	if e != nil {
		t.Fatal(e)
	}
	second, e := a.newOrder(req)
	if e != nil || second.ID != o.ID {
		t.Fatal(second, e)
	}
	s := a.salesSnapshot()
	if s.Customers[0].Balance != 20000 || len(s.Ledger) != 1 {
		t.Fatal("duplicate debit")
	}
	req.Method = "manual"
	if _, e = a.newOrder(req); e == nil {
		t.Fatal("conflicting replay")
	}
	req = BuyRequest{CustomerID: c.ID, Kind: "topup", Method: "manual", Amount: 10000, Idempotency: "topup-repeat"}
	_, _ = a.newOrder(req)
	req.Amount = 20000
	if _, e = a.newOrder(req); e == nil {
		t.Fatal("amount conflict accepted")
	}
}
func TestCouponReservationAndOwnership(t *testing.T) {
	a, c := salesFixture(t)
	other, _ := a.createCustomer("دیگر", 123)
	_ = a.salesTxn(func(s *SalesData) error {
		s.Coupons = []Coupon{{Code: "FIRE", Percent: 10, Limit: 1, Enabled: true}}
		return nil
	})
	o, e := a.newOrder(BuyRequest{CustomerID: c.ID, PlanID: "p-test", Kind: "buy", Method: "manual", Coupon: "FIRE", Idempotency: "coupon-order"})
	if e != nil || o.Amount != 9000 {
		t.Fatal(o, e)
	}
	if e = a.submitReceipt(other.ID, o.ID, "fake", "", ""); e == nil {
		t.Fatal("other customer receipt accepted")
	}
	if e = a.cancelOrder(other.ID, o.ID, false); e == nil {
		t.Fatal("other customer cancelled order")
	}
	if e = a.cancelOrder(c.ID, o.ID, false); e != nil {
		t.Fatal(e)
	}
	if a.salesSnapshot().Coupons[0].Used != 0 {
		t.Fatal("coupon reservation not returned")
	}
}
func TestPaymentChargeDeduplicationAndAmount(t *testing.T) {
	a, c := salesFixture(t)
	req := BuyRequest{CustomerID: c.ID, PlanID: "p-test", Kind: "buy", Method: "stars", Idempotency: "stars-order1"}
	o, _ := a.newOrder(req)
	if e := a.confirmPayment(o.ID, "charge", 4, "stars"); e == nil {
		t.Fatal("wrong amount")
	}
	if e := a.confirmPayment(o.ID, "charge", 5, "api"); e == nil {
		t.Fatal("wrong method")
	}
	if e := a.confirmPayment(o.ID, "charge", 5, "stars"); e != nil {
		t.Fatal(e)
	}
	if e := a.confirmPayment(o.ID, "charge", 5, "stars"); e != nil {
		t.Fatal(e)
	}
	req.Idempotency = "stars-order2"
	o2, _ := a.newOrder(req)
	if e := a.confirmPayment(o2.ID, "charge", 5, "stars"); e == nil {
		t.Fatal("charge reused")
	}
}
func TestCommerceAtomicWriteFailure(t *testing.T) {
	a, c := salesFixture(t)
	_ = os.Remove(filepath.Join(a.cfg.DataDir, "aria-commerce.json"))
	if e := os.Mkdir(filepath.Join(a.cfg.DataDir, "aria-commerce.json"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := a.newOrder(BuyRequest{CustomerID: c.ID, Kind: "topup", Method: "manual", Amount: 10000, Idempotency: "write-fails"}); e == nil {
		t.Fatal("missing write error")
	}
	if len(a.salesSnapshot().Orders) != 0 {
		t.Fatal("memory published after failed write")
	}
}
func signedInit(token string, id int64, date int64) string {
	q := url.Values{"auth_date": {fmt.Sprint(date)}, "query_id": {"test"}, "user": {fmt.Sprintf(`{"id":%d,"first_name":"Aria"}`, id)}}
	data := "auth_date=" + q.Get("auth_date") + "\nquery_id=test\nuser=" + q.Get("user")
	s := hmac.New(sha256.New, []byte("WebAppData"))
	s.Write([]byte(token))
	m := hmac.New(sha256.New, s.Sum(nil))
	m.Write([]byte(data))
	q.Set("hash", hex.EncodeToString(m.Sum(nil)))
	return q.Encode()
}
func TestTelegramInitDataAuthentication(t *testing.T) {
	token := "123456:" + strings.Repeat("a", 35)
	raw := signedInit(token, 555, time.Now().Unix())
	id, _, e := validateInitData(raw, token, time.Now())
	if e != nil || id != 555 {
		t.Fatal(id, e)
	}
	for _, bad := range []string{raw + "&auth_date=123", strings.Replace(raw, "Aria", "Evil", 1), signedInit(token, 555, time.Now().Add(-10*time.Minute).Unix()), signedInit(token, 555, time.Now().Add(time.Minute).Unix())} {
		if _, _, e = validateInitData(bad, token, time.Now()); e == nil {
			t.Fatal("tampered/stale data accepted")
		}
	}
}
func shopRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "https://panel.example"+path, strings.NewReader(body))
	r.Header.Set("Origin", "https://panel.example")
	r.Header.Set("X-Requested-With", "aria-shop")
	r.Header.Set("X-ARIA-Origin", "https://panel.example")
	r.Header.Set("Content-Type", "application/json")
	return r
}
func TestPortalOneTimeLoginAndCustomerIsolation(t *testing.T) {
	a, c := salesFixture(t)
	link, e := a.portalLink(c.ID)
	if e != nil {
		t.Fatal(e)
	}
	raw := strings.Split(link, "#access=")[1]
	login := shopRequest("POST", "/shop/api/login", `{"access":"`+raw+`"}`)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, login)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure {
		t.Fatal("unsafe portal cookie")
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, shopRequest("POST", "/shop/api/login", `{"access":"`+raw+`"}`))
	if w.Code != 403 {
		t.Fatal("one time link reused", w.Code)
	}
	other, _ := a.createCustomer("other", 0)
	o, _ := a.newOrder(BuyRequest{CustomerID: other.ID, Kind: "topup", Method: "manual", Amount: 1000, Idempotency: "other-order"})
	r := shopRequest("POST", "/shop/api/orders/"+o.ID+"/cancel", "{}")
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("other order mutable", w.Code)
	}
	r = shopRequest("GET", "/shop/api/me", "")
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), o.ID) || w.Code != 200 {
		t.Fatal("other order leaked", w.Code)
	}
	r = shopRequest("POST", "/shop/api/tickets", `{"text":"hello"}`)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("X-ARIA-Origin", "https://evil.example")
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross origin write", w.Code)
	}
}
func TestPublicShopAssetsAndDisabledStore(t *testing.T) {
	a, _ := salesFixture(t)
	for _, p := range []string{"/shop", "/shop.js", "/shop.css", "/sales.js", "/fire-natural.png"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "https://panel.example"+p, nil))
		if w.Code != 200 {
			t.Fatal(p, w.Code)
		}
	}
	_ = a.salesTxn(func(s *SalesData) error { s.Settings.Enabled = false; return nil })
	w := httptest.NewRecorder()
	a.ServeHTTP(w, shopRequest("GET", "/shop/api/catalog", ""))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
func TestAPIKeyScopesRevocationAndSecretRedaction(t *testing.T) {
	a, _ := salesFixture(t)
	w := httptest.NewRecorder()
	rkey := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"reader","scopes":["clients:read"]}`))
	rkey.Header.Set("Content-Type", "application/json")
	out, e := a.createIntegrationKey(w, rkey)
	if e != nil {
		t.Fatal(e)
	}
	token := out.(map[string]any)["key"].(string)
	k, ok := a.integrationKey(token)
	if !ok {
		t.Fatal("new key invalid")
	}
	r := httptest.NewRequest("POST", "/api/v1/clients", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("scope bypass", w.Code)
	}
	b, _ := json.Marshal(a.publicSales())
	if strings.Contains(string(b), token) || strings.Contains(string(b), hashToken(token)) {
		t.Fatal("key leaked")
	}
	_ = a.salesTxn(func(s *SalesData) error { s.Keys[0].Revoked = true; return nil })
	if _, ok = a.integrationKey(token); ok {
		t.Fatal("revoked key valid")
	}
	if hasScope(k, "payments:approve") {
		t.Fatal("excess privilege")
	}
}
func TestPanelPrivateAddressesAndDNSGuard(t *testing.T) {
	a := testApp(t)
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.12.3", "[::1]"} {
		n := PanelNode{Name: "test", Type: "aria", URL: "https://" + host}
		if a.validateNode(n, false) == nil {
			t.Fatal("private address accepted", host)
		}
	}
	if !blockedPanelIP(net.ParseIP("100.64.12.3")) {
		t.Fatal("CGN bypass")
	}
	req, _ := http.NewRequest("GET", "https://localhost/api", nil)
	if _, err := a.nodeClient().Do(req); err == nil {
		t.Fatal("private DNS accepted")
	}
}
func TestTelegramUntrustedAdminCommandsAndWebhook(t *testing.T) {
	a, c := salesFixture(t)
	tgc := TelegramConfig{Enabled: true, Token: "123456:" + strings.Repeat("a", 35), ChatID: 99, Mode: "webhook", WebhookSecret: "secret"}
	a.telegram = tgc
	o, _ := a.newOrder(BuyRequest{CustomerID: c.ID, Kind: "topup", Method: "manual", Amount: 1000, Idempotency: "admin-fence"})
	_ = a.submitReceipt(c.ID, o.ID, "reference", "", "")
	m := tgMessage{Text: "/approve " + o.ID, Date: time.Now().Unix()}
	m.Chat.ID = 555
	m.From.ID = 555
	m.Chat.Type = "private"
	if e := a.processUpdate(context.Background(), tgc, tgUpdate{ID: 1, Message: m}); e != nil {
		t.Fatal(e)
	}
	if a.salesSnapshot().Orders[0].Status != "receipt" {
		t.Fatal("customer can approve")
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "/telegram/webhook", strings.NewReader(`{"update_id":2}`)))
	if w.Code != 404 {
		t.Fatal("unsigned webhook accepted")
	}
}
func TestTelegramPreCheckoutMustMatchOwnerAmount(t *testing.T) {
	a, c := salesFixture(t)
	tgc := TelegramConfig{Enabled: true, Token: "123456:" + strings.Repeat("a", 35), ChatID: 99}
	a.telegram = tgc
	o, _ := a.newOrder(BuyRequest{CustomerID: c.ID, Kind: "buy", Method: "stars", PlanID: "p-test", Idempotency: "precheckout"})
	var result bool
	a.telegramHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		result, _ = in["ok"].(bool)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`)), Header: http.Header{}}, nil
	})}
	var u tgUpdate
	_ = json.Unmarshal([]byte(fmt.Sprintf(`{"update_id":1,"pre_checkout_query":{"id":"x","from":{"id":555},"currency":"XTR","total_amount":5,"invoice_payload":"%s"}}`, o.ID)), &u)
	if e := a.dispatchUpdate(context.Background(), tgc, u); e != nil || !result {
		t.Fatal(e, result)
	}
	u.Checkout.From.ID = 666
	_ = a.dispatchUpdate(context.Background(), tgc, u)
	if result {
		t.Fatal("other buyer accepted")
	}
	u.Checkout.From.ID = 555
	u.Checkout.Amount = 1
	_ = a.dispatchUpdate(context.Background(), tgc, u)
	if result {
		t.Fatal("wrong amount accepted")
	}
}
func TestEngineExpiredSessionReloginSingleflight(t *testing.T) {
	var loginCount atomic.Int32
	var allowed atomic.Bool
	allowed.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/csrf-token":
			jsonReply(w, 200, map[string]any{"success": true, "obj": "csrf"})
		case "/login":
			loginCount.Add(1)
			allowed.Store(true)
			jsonReply(w, 200, map[string]bool{"success": true})
		default:
			if !allowed.Load() {
				// 3x-ui 3.8.5 hides unauthenticated API routes from non-XHR
				// callers with 404, even when Accept requests JSON.
				status := http.StatusNotFound
				if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
					status = http.StatusUnauthorized
				}
				w.WriteHeader(status)
				return
			}
			jsonReply(w, 200, map[string]any{"success": true, "obj": []Inbound{}})
		}
	}))
	defer srv.Close()
	e := newEngineClient(srv.URL)
	if err := e.login(context.Background(), "admin", "private", ""); err != nil {
		t.Fatal(err)
	}
	allowed.Store(false)
	var wg sync.WaitGroup
	errors := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := e.inbounds(context.Background()); errors <- err }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if loginCount.Load() != 2 || e.recoveries != 1 {
		t.Fatal("multiple relogins", loginCount.Load(), e.recoveries)
	}
}
func TestEngineUnknownWriteResultIsNotReplayed(t *testing.T) {
	var writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/csrf-token":
			jsonReply(w, 200, map[string]any{"success": true, "obj": "csrf"})
		case "/login":
			jsonReply(w, 200, map[string]bool{"success": true})
		default:
			writes.Add(1)
			w.Write([]byte(`not-json`))
		}
	}))
	defer srv.Close()
	e := newEngineClient(srv.URL)
	_ = e.login(context.Background(), "admin", "pass", "")
	if err := e.call(context.Background(), "POST", "/panel/api/clients/add", map[string]any{}, nil); err == nil || writes.Load() != 1 {
		t.Fatal("uncertain write replayed", err, writes.Load())
	}
}
func TestRenewReplaysAbsoluteTargets(t *testing.T) {
	a, c := salesFixture(t)
	quota := int64(5 << 30)
	expiry := time.Now().Add(10 * 24 * time.Hour).UnixMilli()
	puts := 0
	fail := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			puts++
			var v struct {
				Quota  int64 `json:"quota"`
				Expiry int64 `json:"expiry"`
			}
			json.NewDecoder(r.Body).Decode(&v)
			quota = v.Quota
			expiry = v.Expiry
			if fail {
				fail = false
				w.WriteHeader(503)
				return
			}
		}
		jsonReply(w, 200, SoldService{Email: "aria-000000000001", Name: "test", Quota: quota, Expiry: expiry, Enabled: true})
	}))
	defer server.Close()
	a.panelHTTP = server.Client()
	_ = a.salesTxn(func(s *SalesData) error {
		s.Nodes = []PanelNode{{ID: "n-test", Name: "test", Type: "aria", URL: server.URL, Token: "test", Enabled: true}}
		s.Plans[0].NodeID = "n-test"
		s.Services = []SoldService{{ID: "s-test", CustomerID: c.ID, NodeID: "n-test", Email: "aria-000000000001", Quota: quota, Expiry: expiry}}
		return nil
	})
	o, err := a.newOrder(BuyRequest{CustomerID: c.ID, PlanID: "p-test", ServiceID: "s-test", Kind: "renew", Method: "manual", Idempotency: "renew-order"})
	if err != nil {
		t.Fatal(err)
	}
	_ = a.submitReceipt(c.ID, o.ID, "reference", "", "")
	_, err = a.approveOrder(context.Background(), o.ID)
	if err == nil {
		t.Fatal("unknown write not surfaced")
	}
	firstQuota, firstExpiry := quota, expiry
	if _, err = a.fulfillOrder(context.Background(), o.ID); err != nil {
		t.Fatal(err)
	}
	if puts != 2 || quota != firstQuota || expiry != firstExpiry || len(a.salesSnapshot().Services) != 1 {
		t.Fatal("renew double extended", puts, quota, firstQuota)
	}
}
func TestXUIWSOutputUsesActualNetworkAndFlow(t *testing.T) {
	n := PanelNode{PublicURL: "https://vpn.example", Type: "3x-ui"}
	p := newProvision("test", 1, 30, []string{"vless-ws"})
	for _, protocol := range []string{"vless", "vmess"} {
		ib := Inbound{ID: 1, Port: 2443, Protocol: protocol, Stream: json.RawMessage(`{"network":"ws","security":"tls","tlsSettings":{"serverName":"vpn.example"},"wsSettings":{"path":"/real-path"}}`), Settings: json.RawMessage(marshalString(map[string]any{"clients": []any{inputClient(p)}}))}
		service, e := xuiService(n, []Inbound{ib}, p.Email)
		if e != nil || len(service.Links) != 1 {
			t.Fatal(e)
		}
		if protocol == "vless" {
			u, _ := url.Parse(service.Links[0])
			if u.Query().Get("path") != "/real-path" || u.Query().Get("type") != "ws" || u.Port() != "2443" {
				t.Fatal(u)
			}
		}
	}
	n.Edge = true
	ib := Inbound{ID: 1, Protocol: "vless", Port: 2443, Stream: json.RawMessage(`{"network":"tcp","security":"none"}`), Settings: json.RawMessage(marshalString(map[string]any{"clients": []any{inputClient(p)}}))}
	if _, err := xuiService(n, []Inbound{ib}, p.Email); err == nil {
		t.Fatal("raw TCP behind edge accepted")
	}
}

// A connection can fail after the engine has committed a credential rotation.
// Retrying after a full wrapper restart must reuse that same private target.
func TestAPIRevocationRecoversUnknownCommitAfterRestart(t *testing.T) {
	a := testApp(t)
	p := newProvision("rotation-test", 1, 30, []string{"vless-ws"})
	acc := APIAccount{Username: "rotation_user", KeyID: "key-test", Input: p}
	if err := a.salesTxn(func(s *SalesData) error { s.Accounts = append(s.Accounts, acc); return nil }); err != nil {
		t.Fatal(err)
	}
	ib := managedInbound(t)
	client := inputClient(p)
	writes := 0
	var targets []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panel/api/inbounds/list":
			current := ib
			current.Settings = json.RawMessage(marshalString(map[string]any{"clients": []any{client}}))
			jsonReply(w, 200, map[string]any{"success": true, "obj": []Inbound{current}})
		case "/panel/api/clients/update/" + p.Email:
			var next map[string]any
			if json.NewDecoder(r.Body).Decode(&next) != nil {
				t.Fatal("invalid engine update")
			}
			client = next
			writes++
			targets = append(targets, textField(next, "subId"))
			if writes == 1 {
				w.WriteHeader(503)
				return
			}
			jsonReply(w, 200, map[string]any{"success": true})
		default:
			jsonReply(w, 200, map[string]any{"success": true, "obj": map[string]any{}})
		}
	}))
	defer srv.Close()
	a.service = newEngineClient(srv.URL)
	first := httptest.NewRecorder()
	a.integrationSpecial(first, httptest.NewRequest("POST", "/api/user/rotation_user/revoke_sub", nil), acc, "revoke_sub")
	if first.Code != 503 {
		t.Fatal("ambiguous rotation must stay pending", first.Code)
	}
	pending := a.salesSnapshot().Accounts[0]
	if pending.Input.SubID != p.SubID || pending.Pending == nil || pending.Pending.SubID == p.SubID {
		t.Fatal("unconfirmed credentials published")
	}
	b, err := NewApp(a.cfg, a.base)
	if err != nil {
		t.Fatal(err)
	}
	b.service = newEngineClient(srv.URL)
	persisted, ok := b.apiAccount(IntegrationKey{ID: acc.KeyID}, acc.Username)
	if !ok || persisted.Pending == nil {
		t.Fatal("rotation lost after restart")
	}
	second := httptest.NewRecorder()
	b.integrationSpecial(second, httptest.NewRequest("POST", "/api/user/rotation_user/revoke_sub", nil), persisted, "revoke_sub")
	if second.Code != 200 {
		t.Fatal("rotation did not recover", second.Code, second.Body.String())
	}
	finished := b.salesSnapshot().Accounts[0]
	if writes != 2 || targets[0] != targets[1] || finished.Pending != nil || finished.Input.SubID != targets[0] {
		t.Fatal("rotation target replayed incorrectly")
	}
}
