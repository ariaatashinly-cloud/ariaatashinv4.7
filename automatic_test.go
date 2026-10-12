package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func automaticApp(t *testing.T) *App {
	t.Helper()
	ib := managedInbound(t)
	ib.Settings = json.RawMessage(marshalString(map[string]any{"clients": []any{map[string]any{"id": newUUID(), "password": randomToken(20), "email": "atia-000000000001", "subId": randomToken(20), "comment": "تست خودکار", "enable": true}}}))
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var obj any
		success := true
		switch r.URL.Path {
		case "/csrf-token":
			obj = "engine-csrf"
		case "/login":
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			success = input["username"] == "admin" && input["password"] == "Test-only-password-123"
		case "/panel/api/inbounds/list":
			obj = []Inbound{ib}
		case "/panel/api/server/status":
			obj = map[string]any{"xray": map[string]string{"state": "running"}}
		default:
			success = false
		}
		jsonReply(w, 200, map[string]any{"success": success, "obj": obj})
	}))
	t.Cleanup(engine.Close)
	a, err := NewApp(Config{DataDir: t.TempDir(), AdminUser: "admin", AdminPassword: "Test-only-password-123", Port: 8080}, engine.URL)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func browserRequest(method, path, origin, body string) *http.Request {
	r := httptest.NewRequest(method, "http://backend.internal:8080"+path, strings.NewReader(body))
	r.Header.Set("X-Requested-With", "atiaatashin")
	r.Header.Set("X-ATIA-Origin", origin)
	if method != "GET" {
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func autoLogin(t *testing.T, a *App, origin string) (*http.Cookie, string) {
	t.Helper()
	w := httptest.NewRecorder()
	a.ServeHTTP(w, browserRequest("POST", "/api/login", origin, `{"username":"admin","password":"Test-only-password-123"}`))
	if w.Code != 200 {
		t.Fatalf("automatic login: %d %s", w.Code, w.Body.String())
	}
	var reply struct{ CSRF string }
	_ = json.Unmarshal(w.Body.Bytes(), &reply)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || reply.CSRF == "" {
		t.Fatal("missing session")
	}
	return cookies[0], reply.CSRF
}

func TestAutomaticLoginBehindRewrittenProxyHost(t *testing.T) {
	a := automaticApp(t)
	cookie, _ := autoLogin(t, a, "https://first.deplexo.app")
	if !cookie.Secure || !cookie.HttpOnly {
		t.Fatal("HTTPS browser must get a secure session despite plain HTTP backend")
	}
	s := a.snapshot()
	if !s.Settings.AutomaticDomain || s.Settings.PublicURL != "https://first.deplexo.app" || len(s.Users) != 1 {
		t.Fatalf("auto origin not learned after authentication: %+v", s.Settings)
	}
	u, _ := url.Parse(s.Users[0].Links[0].Link)
	if u.Host != "first.deplexo.app:443" || u.Query().Get("sni") != "first.deplexo.app" || u.Query().Get("host") != "first.deplexo.app" || u.Query().Get("security") != "tls" {
		t.Fatal("incorrect edge endpoint", u)
	}
	oldKey, oldPath := u.User.Username(), u.Query().Get("path")
	autoLogin(t, a, "https://new.example.org:8443")
	s = a.snapshot()
	u, _ = url.Parse(s.Users[0].Links[0].Link)
	if u.Host != "new.example.org:8443" || u.User.Username() != oldKey || u.Query().Get("path") != oldPath || !strings.HasPrefix(s.Users[0].Subscription, "https://new.example.org:8443/") {
		t.Fatal("migration must change domain/port while preserving credentials and paths", u)
	}
	b, err := NewApp(a.cfg, a.base)
	if err != nil || b.snapshot().Settings.PublicURL != s.Settings.PublicURL || !b.snapshot().Settings.AutomaticDomain {
		t.Fatal("last authenticated origin must survive a process restart", err)
	}
}

func TestAnonymousAndFailedLoginsCannotPoisonDomain(t *testing.T) {
	a := automaticApp(t)
	autoLogin(t, a, "https://real.example")
	for _, route := range []string{"/", "/health", "/api/state"} {
		r := browserRequest("GET", route, "https://evil.example", "")
		r.Header.Set("X-Forwarded-Host", "evil.example")
		r.Header.Set("X-Forwarded-Proto", "https")
		a.ServeHTTP(httptest.NewRecorder(), r)
	}
	r := browserRequest("POST", "/api/login", "https://evil.example", `{"username":"admin","password":"wrong"}`)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 401 || a.snapshot().Settings.PublicURL != "https://real.example" {
		t.Fatal("unauthenticated request changed domain", w.Code)
	}
}

func TestAutomaticSessionOriginFence(t *testing.T) {
	a := automaticApp(t)
	cookie, csrf := autoLogin(t, a, "https://real.example")
	for _, method := range []string{"GET", "POST"} {
		r := browserRequest(method, "/api/state", "https://evil.example", "{}")
		r.AddCookie(cookie)
		r.Header.Set("X-ATIA-CSRF", csrf)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 403 || a.snapshot().Settings.PublicURL != "https://real.example" {
			t.Fatal("session accepted a foreign origin", w.Code)
		}
	}
	r := browserRequest("POST", "/api/logout", "https://real.example", "{}")
	r.Header.Set("Origin", "https://evil.example")
	r.AddCookie(cookie)
	r.Header.Set("X-ATIA-CSRF", csrf)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("inconsistent browser headers accepted")
	}
}

func TestAutomaticDomainCanBeRestoredAfterManualOverride(t *testing.T) {
	a := automaticApp(t)
	cookie, csrf := autoLogin(t, a, "https://real.example")
	for _, input := range []string{`{"publicURL":"https://custom.example","automaticDomain":false}`, `{"automaticDomain":true}`} {
		r := browserRequest("POST", "/api/settings", "https://real.example", input)
		r.AddCookie(cookie)
		r.Header.Set("X-ATIA-CSRF", csrf)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if !a.snapshot().Settings.AutomaticDomain || a.snapshot().Settings.PublicURL != "https://real.example" {
		t.Fatal("automatic mode did not use actual browser origin")
	}
}

func TestLegacySettingsMigrateAndExplicitEnvironmentWins(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "atia-settings.json"), []byte(`{"settings":{"publicURL":"https://old.example","brand":"atiaatashin"},"audit":[]}`), 0600)
	a, err := NewApp(Config{DataDir: dir}, "http://127.0.0.1:20530")
	if err != nil || !a.snapshot().Settings.AutomaticDomain {
		t.Fatal("v1 settings must migrate to automatic mode", err)
	}
	b, err := NewApp(Config{DataDir: dir, PublicURL: "https://chosen.example/"}, a.base)
	if err != nil || b.snapshot().Settings.AutomaticDomain || b.snapshot().Settings.PublicURL != "https://chosen.example" {
		t.Fatal("explicit override must win over old persisted domain", err)
	}
}

func TestOptionalDomainEnvironment(t *testing.T) {
	t.Setenv("ATIA_PUBLIC_URL", "")
	t.Setenv("ATIA_ADMIN_USER", "admin")
	t.Setenv("ATIA_ADMIN_PASSWORD", "Test-only-password-123")
	t.Setenv("ATIA_DATA_DIR", t.TempDir())
	t.Setenv("PORT", "3000")
	t.Setenv("ATIA_PERSISTENT_STORAGE", "")
	c, err := loadConfig()
	if err != nil || c.PublicURL != "" || c.Port != 3000 {
		t.Fatal("host-supplied PORT and no domain should work", err)
	}
}

func TestPersistentMountRecognition(t *testing.T) {
	root := "1 1 0:1 / / rw - overlay overlay rw\n"
	for _, test := range []struct {
		dir, entry string
		want       bool
	}{
		{"/data/atia", "2 1 0:2 /vol /data rw,noexec - ext4 /dev/disk rw", true},
		{"/data/atia", "2 1 0:2 / /data rw,noexec - tmpfs tmpfs rw", false},
		{"/data/tmp/atia", "2 1 0:2 /vol /data rw - ext4 /dev/disk rw\n3 2 0:3 / /data/tmp rw - tmpfs tmpfs rw", false},
		{"/data-other/atia", "2 1 0:2 /vol /data rw - ext4 /dev/disk rw", false},
		{"/data/atia", "", false},
	} {
		if got := dataMountInfo(test.dir, root+test.entry); got != test.want {
			t.Fatalf("mount=%q data=%s got=%t", test.entry, test.dir, got)
		}
	}
}

func TestReadOnlyEngineUsesSeparateWritableConfig(t *testing.T) {
	engine, data := t.TempDir(), t.TempDir()
	_ = os.MkdirAll(filepath.Join(engine, "bin"), 0755)
	for _, name := range []string{"xray-linux-amd64", "geoip.dat", "geosite.dat"} {
		_ = os.WriteFile(filepath.Join(engine, "bin", name), []byte("image asset"), 0444)
	}
	_ = os.Chmod(filepath.Join(engine, "bin"), 0555)
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(engine, "bin"), 0755) })
	bin, err := prepareRuntimeBin(engine, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "config.json"), []byte(`{"kept":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareRuntimeBin(engine, data); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(bin, "config.json"))
	if err != nil || string(b) != `{"kept":true}` {
		t.Fatal("runtime setup overwrote persistent config", err)
	}
	link, err := os.Readlink(filepath.Join(bin, "xray-linux-amd64"))
	if err != nil || link != filepath.Join(engine, "bin", "xray-linux-amd64") {
		t.Fatal("binary must resolve to image filesystem", link, err)
	}
	if _, err := os.Stat(filepath.Join(engine, "bin", "config.json")); !os.IsNotExist(err) {
		t.Fatal("config written into read-only image")
	}
}

func TestUnsafeAutomaticOriginsRejected(t *testing.T) {
	a := automaticApp(t)
	for _, origin := range []string{"https://good.example,evil.example", "https://user:pass@good.example", "https://good.example/panel", "https://good.example:", "https://good.example?", "null"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, browserRequest("POST", "/api/login", origin, `{}`))
		if w.Code != 403 {
			t.Fatal("unsafe browser origin", origin, w.Code)
		}
	}
	// A missing reported origin still works for same-host CLI clients.
	r := httptest.NewRequest("GET", "http://localhost:8080/api/state", nil)
	origin, err := a.requestOrigin(r)
	if err != nil || origin != "http://localhost:8080" {
		t.Fatal(origin, err)
	}
}
