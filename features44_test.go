package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccountChangePersistsAndKeepsNativeLogin(t *testing.T) {
	a := automaticApp(t)
	cookie, csrf := autoLogin(t, a, "https://account.example")
	r := browserRequest("POST", "/api/account", "https://account.example", `{"username":"owner","currentPassword":"Test-only-password-123","password":"New-private-password-44"}`)
	r.AddCookie(cookie)
	r.Header.Set("X-ARIA-CSRF", csrf)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, ok := a.getSession(r); ok {
		t.Fatal("old session survived")
	}
	if a.cfg.AdminPassword != "Test-only-password-123" {
		t.Fatal("native login changed")
	}
	b, e := os.ReadFile(filepath.Join(a.cfg.DataDir, "aria-admin.json"))
	if e != nil || strings.Contains(string(b), "New-private-password-44") {
		t.Fatal("password stored in plaintext")
	}
	next, e := NewApp(a.cfg, a.base)
	if e != nil || !next.validAccount("owner", "New-private-password-44") || next.validAccount("admin", "Test-only-password-123") {
		t.Fatal("persisted credentials lost")
	}
	w = httptest.NewRecorder()
	next.ServeHTTP(w, browserRequest("POST", "/api/login", "https://account.example", `{"username":"owner","password":"New-private-password-44"}`))
	if w.Code != 200 {
		t.Fatal("new browser login does not reach unchanged core", w.Code, w.Body.String())
	}
}
func TestDefaultLoginNeedsNoEnvironment(t *testing.T) {
	for _, k := range []string{"ARIA_ADMIN_USER", "ARIA_ADMIN_PASSWORD", "ATIA_ADMIN_USER", "ATIA_ADMIN_PASSWORD", "ARIA_PUBLIC_URL", "ATIA_PUBLIC_URL"} {
		t.Setenv(k, "")
	}
	c, e := loadConfig()
	if e != nil || c.AdminUser != "admin" || c.AdminPassword != "admin" {
		t.Fatal(c.AdminUser, e)
	}
}
func TestReceiptIsSentAsPhotoAndPrivatelyRendered(t *testing.T) {
	a := automaticApp(t)
	cookie, _ := autoLogin(t, a, "https://receipt.example")
	tg := TelegramConfig{Enabled: true, ChatID: 12345, Token: "123456:" + strings.Repeat("a", 35)}
	a.telegram = tg
	a.sales.Orders = []SalesOrder{{ID: "order-photo", TelegramPhoto: "telegram-file-id", Amount: 50000}}
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/xCkAAAAASUVORK5CYII=")
	sent := false
	a.telegramHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendPhoto"):
			var p map[string]any
			_ = json.NewDecoder(r.Body).Decode(&p)
			if p["photo"] != "telegram-file-id" || int64(p["chat_id"].(float64)) != 12345 {
				t.Fatal("wrong photo or recipient")
			}
			sent = true
			return fakeJSON(`{"ok":true,"result":{}}`), nil
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			return fakeJSON(`{"ok":true,"result":{"file_path":"photos/file_1.jpg"}}`), nil
		case strings.Contains(r.URL.Path, "/file/bot"):
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(png))), Header: http.Header{}}, nil
		}
		return nil, fmt.Errorf("unexpected Telegram operation")
	})}
	n := SalesNotice{ChatID: 12345, ReceiptOrder: "order-photo"}
	if e := a.tgRichSend(context.Background(), tg, n); e != nil || !sent {
		t.Fatal("photo was not sent", e)
	}
	n.ChatID = 999
	if a.tgRichSend(context.Background(), tg, n) == nil {
		t.Fatal("receipt leaked to another account")
	}
	r := browserRequest("GET", "/api/sales/receipt-image?id=order-photo", "https://receipt.example", "")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("receipt is public")
	}
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/") || w.Body.String() != string(png) || strings.Contains(w.Body.String(), tg.Token) {
		t.Fatal("private image did not render", w.Code, w.Body.String())
	}
}
func TestAdvancedManualRoundTripAndClientServerSeparation(t *testing.T) {
	m := ManualCreate{Protocol: "vless", Network: "xhttp", Mode: "packet-up", Path: "/connect/tuning-roundtrip/", Tuning: ManualTuning{Fingerprint: "firefox", KeepAliveIdle: 60, KeepAliveInterval: 30, TCPUserTimeoutMS: 25000, XHTTPMaxConnections: 2, PostMaxKB: 512, PostIntervalMS: 30, BufferedPosts: 30, DownloadEndpoint: LinkEndpoint{Address: "download.example", Port: 443, Host: "download.example", SNI: "download.example"}}}
	m, e := normalizeManual(m)
	if e != nil {
		t.Fatal(e)
	}
	s := m.spec("manual-abcdef012345")
	p := s.profile()
	round, e := specFromProfile(p)
	if e != nil || round != s {
		t.Fatal("tuning not preserved")
	}
	c := map[string]any{"id": newUUID(), "email": "test"}
	o := xrayCloudOutbound(p, c, "https://origin.example", LinkEndpoint{})
	stream := o["streamSettings"].(map[string]any)
	if stream["tlsSettings"].(map[string]any)["fingerprint"] != "firefox" {
		t.Fatal("fingerprint ignored")
	}
	extra := stream["xhttpSettings"].(map[string]any)["extra"].(map[string]any)
	if extra["downloadSettings"] == nil || extra["xmux"] == nil {
		t.Fatal("download split or xmux ignored")
	}
	server := map[string]any{"xhttpSettings": map[string]any{"path": p.Path, "mode": p.XHTTPMode}}
	applyManualTuning(p, server, false)
	se := server["xhttpSettings"].(map[string]any)["extra"].(map[string]any)
	if se["scMaxBufferedPosts"] != 30 || se["scMinPostsIntervalMs"] != nil || se["xmux"] != nil {
		t.Fatal("client-only settings emitted on server")
	}
	m.Network = "ws"
	if _, e := normalizeManual(m); e == nil {
		t.Fatal("incompatible fields accepted")
	}
}
func TestAdminMenuRequiresConfiguredNumericIdentity(t *testing.T) {
	a := automaticApp(t)
	tg := TelegramConfig{Enabled: true, ChatID: 12345, Token: "123456:" + strings.Repeat("a", 35)}
	a.telegram = tg
	u := tgUpdate{ID: 44}
	u.Message.Chat.Type = "private"
	u.Message.Chat.ID = 54321
	u.Message.From.ID = 54321
	u.Message.Date = time.Now().Unix()
	u.Message.Text = "/admin"
	if e := a.dispatchUpdate(context.Background(), tg, u); e != nil {
		t.Fatal(e)
	}
	if len(a.salesSnapshot().Notices) != 0 {
		t.Fatal("non-admin got management")
	}
	u.ID = 45
	u.Message.Chat.ID = tg.ChatID
	u.Message.From.ID = tg.ChatID
	if e := a.dispatchUpdate(context.Background(), tg, u); e != nil {
		t.Fatal(e)
	}
	rows := a.salesSnapshot().Notices
	if len(rows) != 1 || !strings.Contains(rows[0].Text, "مدیریت") || len(rows[0].Buttons) == 0 {
		t.Fatal("admin menu missing")
	}
}
func TestCloudflareAdapterAndRedaction(t *testing.T) {
	a := automaticApp(t)
	token := strings.Repeat("T", 40)
	zone := strings.Repeat("a", 32)
	created := false
	a.edgeHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.cloudflare.com" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatal("token not confined to Cloudflare")
		}
		switch {
		case r.URL.Path == "/client/v4/user/tokens/verify":
			return fakeJSON(`{"success":true,"result":{"status":"active"}}`), nil
		case r.URL.Path == "/client/v4/zones/"+zone:
			return fakeJSON(`{"success":true,"result":{"name":"example.com"}}`), nil
		case r.Method == "GET":
			return fakeJSON(`{"success":true,"result":[]}`), nil
		case r.Method == "POST":
			var p map[string]any
			_ = json.NewDecoder(r.Body).Decode(&p)
			if p["proxied"] != true || p["type"] != "CNAME" {
				t.Fatal("invalid CDN record")
			}
			created = true
			return fakeJSON(`{"success":true,"result":{}}`), nil
		}
		return nil, fmt.Errorf("unknown operation")
	})}
	s := a.edgeSnapshot()
	body := fmt.Sprintf(`{"token":"%s","zone":"%s","name":"CDN","hostname":"vpn.example.com","target":"service.host.example"}`, token, zone)
	r := httptest.NewRequest("POST", "/api/edges/cloudflare", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if e := a.cloudflareRoute(context.Background(), httptest.NewRecorder(), r, &s); e != nil || !created || len(s.Routes) != 1 {
		t.Fatal(e)
	}
	if e := a.edgeSave(s); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(a.edgePublic())
	if strings.Contains(string(b), token) {
		t.Fatal("CDN token exposed")
	}
	if validEdge(EdgeRoute{Name: "bad", Endpoint: LinkEndpoint{Address: "127.0.0.1", Host: "example.com", SNI: "example.com", Port: 443}}) == nil {
		t.Fatal("private probe target accepted")
	}
}
