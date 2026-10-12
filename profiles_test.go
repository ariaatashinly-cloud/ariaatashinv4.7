package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublicURLValidation(t *testing.T) {
	valid := []string{"https://yalah.velixir.run", "http://localhost:8080/", "https://[::1]:443"}
	for _, s := range valid {
		if _, err := validatePublicURL(s); err != nil {
			t.Errorf("valid URL %q: %v", s, err)
		}
	}
	invalid := []string{"", "yalah.velixir.run", "javascript:alert(1)", "https://x.example/panel", "https://user:pass@x.example", "https://x.example/?q=1", "https://x.example/#secret", "https://x.example:0", "https://x.example:99999", "https://x.example:abc"}
	for _, s := range invalid {
		if _, err := validatePublicURL(s); err == nil {
			t.Errorf("accepted unsafe URL %q", s)
		}
	}
}

func TestShareLinks(t *testing.T) {
	c := map[string]any{"id": "12345678-abcd-4abc-8abc-123456789abc", "password": "secret-password", "comment": "گوشی آریا"}
	for _, tpl := range profileTemplates {
		t.Run(tpl.Key, func(t *testing.T) {
			p := tpl
			p.Path = "/connect/test-safe/"
			link := shareLink(p, c, "https://yalah.velixir.run")
			if p.Protocol == "vmess" {
				b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
				if err != nil {
					t.Fatal(err)
				}
				var v map[string]any
				if err = json.Unmarshal(b, &v); err != nil {
					t.Fatal(err)
				}
				if v["add"] != "yalah.velixir.run" || v["port"] != "443" || v["tls"] != "tls" || v["path"] != p.Path {
					t.Fatal(v)
				}
				return
			}
			u, err := url.Parse(link)
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			if u.Hostname() != "yalah.velixir.run" || u.Port() != "443" || q.Get("sni") != "yalah.velixir.run" || q.Get("host") != "yalah.velixir.run" || q.Get("path") != p.Path || q.Get("security") != "tls" || q.Get("type") != p.Network {
				t.Fatal(link)
			}
			if p.Network == "ws" && q.Get("alpn") != "http/1.1" {
				t.Fatal("WebSocket must advertise HTTP/1.1")
			}
			if p.Network == "xhttp" && q.Get("mode") != "auto" {
				t.Fatal("XHTTP mode missing")
			}
			if p.Protocol == "trojan" && u.User.Username() != "secret-password" {
				t.Fatal("Trojan must use password, not UUID")
			}
		})
	}
}

func TestPublicInternalPortSeparation(t *testing.T) {
	p := profileTemplates[0]
	p.Port = 20910
	p.Path = "/connect/test-safe/"
	u, _ := url.Parse(shareLink(p, map[string]any{"id": newUUID()}, "http://localhost:18080"))
	if u.Port() != "18080" || u.Query().Get("security") != "none" {
		t.Fatal(u)
	}
	if strings.Contains(u.String(), "20910") {
		t.Fatal("internal port leaked to client link")
	}
}

func TestRandomIdentity(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		s := newUUID()
		if len(s) != 36 || s[14] != '4' || !strings.ContainsRune("89ab", rune(s[19])) || seen[s] {
			t.Fatal("UUID invalid or duplicate")
		}
		seen[s] = true
		if len(randomToken(16)) != 32 {
			t.Fatal("token size")
		}
	}
}

func TestCreateValidation(t *testing.T) {
	r := CreateRequest{Name: "آریا", Count: 1, QuotaGB: 50, Days: 30, Profiles: []string{"vless-ws"}}
	if err := validateCreate(r); err != nil {
		t.Fatal(err)
	}
	bad := []CreateRequest{{Name: "", Count: 1, Profiles: r.Profiles}, {Name: "a", Count: 0, Profiles: r.Profiles}, {Name: "a", Count: 51, Profiles: r.Profiles}, {Name: "a", Count: 1, QuotaGB: -1, Profiles: r.Profiles}, {Name: "a", Count: 1, Days: 4000, Profiles: r.Profiles}, {Name: "a", Count: 1, Profiles: []string{"reality"}}, {Name: "a", Count: 1, Profiles: []string{"vless-ws", "vless-ws"}}, {Name: "a\n", Count: 1, Profiles: r.Profiles}}
	for _, c := range bad {
		if validateCreate(c) == nil {
			t.Fatal("accepted invalid request", c)
		}
	}
}

func TestUserStatus(t *testing.T) {
	u := User{Enabled: true, Quota: 10}
	if userStatus(u) != "active" {
		t.Fatal(u)
	}
	u.Down = 10
	if userStatus(u) != "limited" {
		t.Fatal(u)
	}
	u.Expiry = time.Now().Add(-time.Second).UnixMilli()
	if userStatus(u) != "expired" {
		t.Fatal(u)
	}
	u.Enabled = false
	if userStatus(u) != "disabled" {
		t.Fatal(u)
	}
}

func TestNestedJSON(t *testing.T) {
	for _, b := range []string{`{"network":"ws"}`, `"{\"network\":\"ws\"}"`} {
		var out map[string]any
		if err := nestedJSON([]byte(b), &out); err != nil || out["network"] != "ws" {
			t.Fatal(err, out)
		}
	}
}

func testApp(t *testing.T) *App {
	t.Helper()
	a, err := NewApp(Config{DataDir: t.TempDir(), PublicURL: "https://panel.example", AdminUser: "admin", Port: 8080}, "http://127.0.0.1:20530")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func managedInbound(t *testing.T) Inbound {
	t.Helper()
	p := profileTemplates[0]
	return Inbound{ID: 1, Remark: "atia:" + p.Key, Port: p.Port, Listen: "127.0.0.1", Protocol: p.Protocol, Enable: true, Stream: json.RawMessage(marshalString(map[string]any{"network": "ws", "security": "none", "wsSettings": map[string]any{"path": "/connect/vless-ws-test123/"}})), Settings: json.RawMessage(`{"clients":[]}`)}
}

func TestManagedInboundGuard(t *testing.T) {
	ib := managedInbound(t)
	if _, ok := inboundProfile(ib); !ok {
		t.Fatal("valid managed inbound rejected")
	}
	ib.Listen = "0.0.0.0"
	if _, ok := inboundProfile(ib); ok {
		t.Fatal("nonloopback forwarded")
	}
	ib = managedInbound(t)
	ib.Remark = "native user config"
	if _, ok := inboundProfile(ib); ok {
		t.Fatal("unmanaged config forwarded")
	}
}

func TestSharedUsageNotDoubleCounted(t *testing.T) {
	a := testApp(t)
	ib := managedInbound(t)
	client := map[string]any{"id": newUUID(), "password": randomToken(20), "email": "atia-000000000001", "subId": randomToken(16), "comment": "آریا", "enable": true, "totalGB": float64(1000)}
	ib.Settings = json.RawMessage(marshalString(map[string]any{"clients": []any{client}}))
	ib.Stats = []Traffic{{Email: "atia-000000000001", Up: 5, Down: 10}}
	second := ib
	second.ID = 2
	a.publish([]Inbound{ib, second}, json.RawMessage(`{}`))
	s := a.snapshot()
	if len(s.Users) != 1 || s.Users[0].Up != 5 || s.Users[0].Down != 10 {
		t.Fatalf("shared counters counted twice: %+v", s.Users)
	}
}

func TestAPIProtected(t *testing.T) {
	a := testApp(t)
	for _, p := range []string{"/api/state", "/api/backup", "/api/diagnostics"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 401 {
			t.Fatal(p, w.Code)
		}
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{}`)))
	if w.Code != 403 {
		t.Fatal("login CSRF guard", w.Code)
	}
}

func TestCookieAndCSRFFencing(t *testing.T) {
	a := testApp(t)
	a.sessions["valid"] = session{CSRF: "private-csrf", Expires: time.Now().Add(time.Hour)}
	for _, test := range []struct {
		origin, csrf string
		code         int
	}{{"https://evil.example", "private-csrf", 403}, {"https://panel.example", "bad", 403}, {"https://panel.example", "private-csrf", 404}} {
		r := httptest.NewRequest("POST", "/api/does-not-exist", nil)
		r.AddCookie(&http.Cookie{Name: "atia_session", Value: "valid"})
		r.Header.Set("X-Requested-With", "atiaatashin")
		r.Header.Set("Origin", test.origin)
		r.Header.Set("X-ATIA-CSRF", test.csrf)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != test.code {
			t.Fatal(test, w.Code)
		}
	}
}

func TestSubscriptionExpiryAndRevocation(t *testing.T) {
	a := testApp(t)
	u := User{Email: "atia-000000000001", Enabled: true, SubID: randomToken(16), Status: "active", Links: []Connection{{Link: "vless://example"}}}
	a.state.Users = []User{u}
	a.state.Ready = true
	a.state.SyncedAt = time.Now().UnixMilli()
	path := "/subscription/" + u.SubID
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	body, _ := base64.StdEncoding.DecodeString(w.Body.String())
	if string(body) != "vless://example" {
		t.Fatal(string(body))
	}
	a.state.Users[0].Expiry = time.Now().Add(-time.Second).UnixMilli()
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 403 {
		t.Fatal("expired subscription served")
	}
	a.state.Users[0].SubID = randomToken(16)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 404 {
		t.Fatal("rotated token served")
	}
}

func TestSecurityHeadersAndLocalAssets(t *testing.T) {
	a := testApp(t)
	for _, p := range []string{"/", "/app.js", "/style.css", "/qr.js", "/vazirmatn.woff2"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 200 {
			t.Fatal("asset missing", p, w.Code)
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") {
			t.Fatal("CSP missing")
		}
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/panel/api/inbounds/list", nil))
	if w.Code != 404 {
		t.Fatal("native API must not be forwarded")
	}
}

func TestAtomicSettingsPersist(t *testing.T) {
	a := testApp(t)
	a.audit("real event", "create")
	if err := a.persist(); err != nil {
		t.Fatal(err)
	}
	b, err := NewApp(a.cfg, a.base)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.state.Audit) != 1 || b.state.Settings.PublicURL != a.state.Settings.PublicURL {
		t.Fatal("settings did not persist")
	}
	info, err := os.Stat(filepath.Join(a.cfg.DataDir, "atia-settings.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private file permissions missing")
	}
}

func TestArchivePathTraversalRefused(t *testing.T) {
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "x-ui/../../outside", Mode: 0644, Size: 1})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	if err := extractEngine(&out, t.TempDir()); err == nil {
		t.Fatal("unsafe archive accepted")
	}
}
