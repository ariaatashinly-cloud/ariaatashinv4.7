package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCanonicalEnvAndBrand(t *testing.T) {
	t.Setenv("ATIA_ADMIN_PASSWORD", "Legacy-password-123")
	t.Setenv("ARIA_ADMIN_PASSWORD", "Canonical-password-123")
	t.Setenv("ARIA_MODE", "vps")
	t.Setenv("ARIA_DATA_DIR", t.TempDir())
	t.Setenv("ARIA_BIND", "127.0.0.1")
	c, e := loadConfig()
	if e != nil || c.AdminPassword != "Canonical-password-123" || c.Mode != "vps" {
		t.Fatal(c, e)
	}
	a, e := NewApp(c, "http://127.0.0.1:20530")
	if e != nil {
		t.Fatal(e)
	}
	if a.snapshot().Brand != "ariaatashin" || a.snapshot().Settings.Brand != "ariaatashin" {
		t.Fatal("old brand")
	}
}
func TestProviderDetectionAndCloudFence(t *testing.T) {
	for _, c := range []struct{ host, want string }{{"https://x.de.deplexo.com", "deplexo"}, {"https://x.velixir.run", "velixir"}, {"https://x.pxxl.app", "pxxl"}, {"https://deplexo.com.attacker.example", "auto"}} {
		if v := detectPreset(c.host, "cloud"); v != c.want {
			t.Fatal(c, v)
		}
	}
	a := testApp(t)
	list := a.profileCatalog()
	if len(list) != 18 {
		t.Fatal(len(list))
	}
	for _, p := range list {
		if p.Direct && p.Available {
			t.Fatal("cloud direct enabled")
		}
	}
	if _, e := a.ensureProfile(context.Background(), nil, "tuic"); e == nil {
		t.Fatal("cloud QUIC allowed")
	}
}
func TestVPSIdentitySurvivesAndStaysPrivate(t *testing.T) {
	c := Config{DataDir: t.TempDir(), Mode: "vps", Port: 8080}
	a, e := NewApp(c, "http://127.0.0.1:20530")
	if e != nil {
		t.Fatal(e)
	}
	v := a.vpsSnapshot()
	b, e := NewApp(c, a.base)
	if e != nil || b.vpsSnapshot().PrivateKey != v.PrivateKey {
		t.Fatal("identity changed", e)
	}
	fi, e := os.Stat(filepath.Join(c.DataDir, "aria-vps.json"))
	if e != nil || fi.Mode().Perm() != 0600 {
		t.Fatal(fi, e)
	}
	data, _ := json.Marshal(a.runtimeSnapshot())
	if strings.Contains(string(data), v.PrivateKey) || strings.Contains(string(data), "privateKey") {
		t.Fatal("private key exposed")
	}
	v.PortBase = 8080
	if validateVPS(v, 8080) == nil {
		t.Fatal("panel port conflict allowed")
	}
	for _, p := range a.profileCatalog() {
		if p.Security == "tls" && p.Available {
			t.Fatal("TLS without cert enabled")
		}
	}
}
func TestSelfSignedTLSRefused(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	pair := srv.TLS.Certificates[0]
	key, e := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if e != nil {
		t.Fatal(e)
	}
	d := t.TempDir()
	cf, kf := filepath.Join(d, "cert.pem"), filepath.Join(d, "key.pem")
	os.WriteFile(cf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]}), 0600)
	os.WriteFile(kf, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600)
	v := VPSConfig{ServerName: "example.com", CertFile: cf, KeyFile: kf}
	if v.certificateError() == nil {
		t.Fatal("untrusted self-signed certificate accepted")
	}
}
func TestDirectLinksAndExportsUseCorrectCredentials(t *testing.T) {
	a, e := NewApp(Config{DataDir: t.TempDir(), Mode: "vps", Port: 8080}, "http://127.0.0.1:20530")
	if e != nil {
		t.Fatal(e)
	}
	c := map[string]any{"id": newUUID(), "password": randomToken(20), "auth": randomToken(20), "email": "aria-000000000001", "comment": "آریا"}
	for _, p := range a.profileCatalog() {
		if !p.Direct {
			p.Path = "/connect/test-12345678/"
		}
		if p.Network == "grpc" {
			p.Path = "/connect/grpc-test-12345678/"
		}
		p.ServerName = "vpn.example.com"
		link := shareLink(p, c, "https://panel.example")
		if link == "" {
			t.Fatal(p.Key)
		}
		o := singBoxOutbound(p, c, "https://panel.example")
		if p.Network == "xhttp" || (!p.Direct && p.Protocol == "shadowsocks") {
			if o != nil {
				t.Fatal("unsupported web transport export")
			}
			continue
		}
		if o == nil {
			t.Fatal(p.Key)
		}
		if p.Protocol == "hysteria" {
			u, e := url.Parse(link)
			if e != nil || u.User.Username() != textField(c, "auth") || o["password"] != c["auth"] {
				t.Fatal("Hysteria uses wrong credential", link)
			}
		}
		if p.Protocol == "tuic" {
			u, _ := url.Parse(link)
			pw, _ := u.User.Password()
			if pw != c["password"] {
				t.Fatal("TUIC password")
			}
		}
	}
}
func TestDirectInboundIsNeverHTTPForwarded(t *testing.T) {
	a := testApp(t)
	v := a.vpsSnapshot()
	ib := Inbound{ID: 5, Remark: "aria:vless-reality", Listen: "0.0.0.0", Port: 10443, Protocol: "vless", Enable: true, Settings: json.RawMessage(marshalString(map[string]any{"clients": []any{}})), Stream: json.RawMessage(marshalString(map[string]any{"network": "tcp", "security": "reality", "realitySettings": map[string]any{"privateKey": v.PrivateKey, "serverNames": []string{v.RealityTarget}, "shortIds": []string{v.ShortID}}}))}
	a.publish([]Inbound{ib}, json.RawMessage("{}"))
	if len(a.routes) != 0 {
		t.Fatal("direct inbound proxied")
	}
}
func TestUDPProbeRequiresBoundSocket(t *testing.T) {
	c, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	p := c.LocalAddr().(*net.UDPAddr).Port
	if !udpSocketBound(p) {
		t.Fatal("bound socket missing")
	}
	c.Close()
	if udpSocketBound(p) {
		t.Fatal("UDP connect falsely healthy")
	}
}

type tgTransport func(*http.Request) (*http.Response, error)

func (f tgTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTelegramPrivateAuthorizationAndRedaction(t *testing.T) {
	a := testApp(t)
	tgc := TelegramConfig{Token: "123456789:" + strings.Repeat("X", 35), ChatID: 12345, Enabled: true}
	m := tgMessage{Text: "/status", Date: time.Now().Unix()}
	m.Chat.ID = 12345
	m.Chat.Type = "private"
	m.From.ID = 12345
	if a.telegramCommand(tgc, m) == "" {
		t.Fatal("admin rejected")
	}
	m.From.ID = 2
	if a.telegramCommand(tgc, m) != "" {
		t.Fatal("wrong sender allowed")
	}
	m.From.ID = 12345
	m.Chat.Type = "group"
	if a.telegramCommand(tgc, m) != "" {
		t.Fatal("group allowed")
	}
	a.advancedMu.Lock()
	a.telegram = tgc
	a.advancedMu.Unlock()
	b, _ := json.Marshal(a.publicTelegram())
	if strings.Contains(string(b), tgc.Token) {
		t.Fatal("token exposed")
	}
	a.telegramHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.telegram.org" {
			t.Fatal("untrusted API destination")
		}
		if strings.HasSuffix(r.URL.Path, "sendMessage") {
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			if in["chat_id"] != float64(12345) || in["protect_content"] != true {
				t.Fatal(in)
			}
		}
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("{\"ok\":false}")), Header: http.Header{}}, nil
	})}
	err := a.tgSend(context.Background(), tgc, "test")
	if err == nil || strings.Contains(err.Error(), tgc.Token) {
		t.Fatal("error leaks token", err)
	}
}

func TestTelegramPollingPersistsOffsetAndOnlyRepliesToAdmin(t *testing.T) {
	a := testApp(t)
	tgc := TelegramConfig{Token: "123456789:" + strings.Repeat("Y", 35), ChatID: 12345, Enabled: true}
	a.telegram = tgc
	if e := atomicJSON(a.cfg.DataDir, "aria-telegram.json", tgc); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sends := make(chan map[string]any, 2)
	calls := 0
	a.telegramHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		var result any
		if strings.HasSuffix(r.URL.Path, "getUpdates") {
			calls++
			if calls > 1 {
				result = []any{}
			} else {
				good := tgMessage{Text: "/status", Date: time.Now().Unix()}
				good.Chat.ID = 12345
				good.Chat.Type = "private"
				good.From.ID = 12345
				bad := good
				bad.Chat.ID = 999
				bad.From.ID = 999
				result = []tgUpdate{{ID: 1, Message: bad}, {ID: 2, Message: good}}
			}
		} else {
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			sends <- in
			result = map[string]any{"message_id": 1}
			cancel()
		}
		b, _ := json.Marshal(map[string]any{"ok": true, "result": result})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b))), Header: http.Header{}}, nil
	})}
	done := make(chan struct{})
	go func() { a.telegramLoop(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("polling did not stop")
	}
	if len(sends) != 1 {
		t.Fatal("unauthorized chat received reply")
	}
	in := <-sends
	if in["chat_id"] != float64(12345) || in["protect_content"] != true {
		t.Fatal(in)
	}
	next, e := NewApp(a.cfg, a.base)
	if e != nil || next.telegramSnapshot().Offset != 3 {
		t.Fatal("offset not retained", e)
	}
	st, e := os.Stat(filepath.Join(a.cfg.DataDir, "aria-telegram.json"))
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("token file not private")
	}
}
