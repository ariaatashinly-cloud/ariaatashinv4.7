//go:build integration

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNativeManualProfilesTrafficRestartAndBackup(t *testing.T) {
	a, ctx := nativeTestApp(t)
	public := httptest.NewServer(a)
	t.Cleanup(public.Close)
	a.cfg.PublicURL = public.URL
	a.mu.Lock()
	a.state.Settings.PublicURL = public.URL
	a.mu.Unlock()
	cookie, csrf := autoLogin(t, a, public.URL)
	allowNativeTestLoopback(t, ctx, a.service)
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw := ""
		if body != nil {
			raw = marshalString(body)
		}
		r := browserRequest(method, path, public.URL, raw).WithContext(ctx)
		r.AddCookie(cookie)
		r.Header.Set("X-ATIA-CSRF", csrf)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	cases := []ManualCreate{}
	for _, protocol := range []string{"vless", "vmess", "trojan", "shadowsocks"} {
		methods := []string{""}
		if protocol == "shadowsocks" {
			methods = []string{defaultShadowsocksMethod, "aes-128-gcm", "aes-256-gcm"}
		}
		for _, method := range methods {
			for _, network := range []string{"ws", "xhttp"} {
				m := ManualCreate{Protocol: protocol, Network: network, Method: method, Path: "/connect/manual-" + protocol + "-" + network + "-" + method + "test/", Endpoint: LinkEndpoint{Host: "route.example"}}
				if network == "xhttp" {
					m.Mode = map[string]string{"vless": "auto", "vmess": "packet-up", "trojan": "stream-up", "shadowsocks": "packet-up"}[protocol]
				}
				if protocol == "trojan" || protocol == "shadowsocks" {
					m.Password = "Test+password!" + protocol + "-" + method + "-" + network
				} else {
					m.UUID = newUUID()
				}
				cases = append(cases, m)
			}
		}
	}
	for _, m := range cases {
		w := request("POST", "/api/users", CreateRequest{Name: m.Protocol + " " + m.Network, Count: 1, QuotaGB: 1, Days: 7, Manual: &m})
		var result struct {
			Created []string
			Warning string
		}
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != 200 || len(result.Created) != 1 || result.Warning != "" {
			t.Fatal("native manual creation failed", m.Protocol, m.Network, m.Method, w.Code, w.Body.String())
		}
		u, ok := a.findUser(result.Created[0])
		if !ok || len(u.Links) != 1 {
			t.Fatal("manual user links missing")
		}
		var profile Profile
		for _, p := range a.snapshot().Profiles {
			if p.Key == u.Links[0].Profile {
				profile = p
			}
		}
		if !profile.Manual || !profile.Ready || profile.Path != m.Path || profile.Network != m.Network || profile.Method != m.Method {
			t.Fatal("native manual profile missing or changed")
		}
		probeManualTraffic(t, ctx, a.cfg.EngineDir, profile, u.Raw, public.URL)
		w = request("POST", "/api/users", CreateRequest{Name: "duplicate secret", Count: 1, Manual: &m})
		if w.Code != 400 {
			t.Fatal("duplicate manual credential was accepted", w.Code)
		}
	}
	wantedUsers := len(cases) + 1
	// Existing manual profiles can also be reused from the normal catalog.
	key := a.snapshot().Users[0].Links[0].Profile
	w := request("POST", "/api/users", CreateRequest{Name: "reuse manual", Count: 1, Profiles: []string{key}})
	if w.Code != 200 || len(a.snapshot().Users) != wantedUsers {
		t.Fatal("reuse of a manual template failed", w.Code, w.Body.String())
	}
	settings := a.snapshot().Settings
	settings.Connection = LinkEndpoint{Address: "203.0.113.42", Port: 8443}
	w = request("POST", "/api/settings", settings)
	if w.Code != 200 {
		t.Fatal("native endpoint save failed", w.Code)
	}
	u := a.snapshot().Users[0]
	w = request("GET", "/subscription/"+u.SubID, nil)
	decoded, err := base64.StdEncoding.DecodeString(w.Body.String())
	if err != nil || w.Code != 200 || !strings.Contains(string(decoded), "203.0.113.42") && !strings.HasPrefix(string(decoded), "vmess://") {
		t.Fatal("subscription did not include updated endpoint")
	}
	w = request("GET", "/api/backup", nil)
	var backup Backup
	if json.Unmarshal(w.Body.Bytes(), &backup) != nil || w.Code != 200 || len(backup.Users) != wantedUsers {
		t.Fatal("native manual backup failed", w.Code)
	}
	before := a.snapshot()
	loaded, err := NewApp(a.cfg, a.base)
	if err != nil {
		t.Fatal(err)
	}
	if err = loaded.refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if len(loaded.snapshot().Users) != wantedUsers || marshalString(loaded.snapshot().Users) != marshalString(before.Users) {
		t.Fatal("native manual configs changed after wrapper restart")
	}
	for _, user := range before.Users {
		if err := a.service.clientCall(ctx, "del", user.Email, nil); err != nil {
			t.Fatal(err)
		}
	}
	all, err := a.service.inbounds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ib := range all {
		if _, ok := manualInboundProfile(ib); ok {
			if err = a.service.call(ctx, "POST", "/panel/api/inbounds/del/"+strconv.Itoa(ib.ID), nil, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = a.refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if len(a.snapshot().Users) != 0 {
		t.Fatal("test database was not cleared")
	}
	settings.Connection = LinkEndpoint{}
	w = request("POST", "/api/settings", settings)
	if w.Code != 200 {
		t.Fatal("destination endpoint reset failed")
	}
	w = request("POST", "/api/restore", backup)
	var restored struct {
		Restored int
		Warning  string
	}
	if json.Unmarshal(w.Body.Bytes(), &restored) != nil || w.Code != 200 || restored.Restored != wantedUsers || restored.Warning != "" {
		t.Fatal("manual backup restore failed", w.Code, w.Body.String())
	}
	for _, original := range before.Users {
		current, ok := a.findUser(original.Email)
		if !ok || marshalString(current.Links) != marshalString(original.Links) || current.SubID != original.SubID {
			t.Fatal("manual restore changed identity, path or endpoint")
		}
	}
}

func TestNativeEightCloudProfilesAndExistingConnections(t *testing.T) {
	a, ctx := nativeTestApp(t)
	public := httptest.NewServer(a)
	t.Cleanup(public.Close)
	a.cfg.PublicURL = public.URL
	a.mu.Lock()
	a.state.Settings.PublicURL = public.URL
	a.mu.Unlock()
	cookie, csrf := autoLogin(t, a, public.URL)
	allowNativeTestLoopback(t, ctx, a.service)
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw := ""
		if body != nil {
			raw = marshalString(body)
		}
		r := browserRequest(method, path, public.URL, raw).WithContext(ctx)
		r.AddCookie(cookie)
		r.Header.Set("X-ATIA-CSRF", csrf)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	create := func(name string, keys []string) User {
		t.Helper()
		w := request("POST", "/api/users", CreateRequest{Name: name, Count: 1, QuotaGB: 1, Days: 7, Profiles: keys})
		var result struct {
			Created []string
			Warning string
		}
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || w.Code != 200 || len(result.Created) != 1 || result.Warning != "" {
			t.Fatal("native cloud creation failed", w.Code, w.Body.String())
		}
		u, ok := a.findUser(result.Created[0])
		if !ok || len(u.Links) != len(keys) {
			t.Fatal("cloud links missing")
		}
		return u
	}
	old := create("original four profiles", []string{"vless-ws", "vless-xhttp", "vmess-ws", "trojan-ws"})
	keys := []string{}
	for _, p := range profileTemplates {
		keys = append(keys, p.Key)
	}
	if len(keys) != 8 {
		t.Fatal("eight cloud profiles required")
	}
	u := create("all eight profiles", keys)
	for _, p := range a.snapshot().Profiles {
		if p.Direct || p.Manual {
			continue
		}
		if !p.Ready {
			t.Fatal("cloud profile was not enabled", p.Key)
		}
		probeManualTraffic(t, ctx, a.cfg.EngineDir, p, u.Raw, public.URL)
	}
	current, ok := a.findUser(old.Email)
	if !ok || marshalString(current.Links) != marshalString(old.Links) || current.SubID != old.SubID {
		t.Fatal("adding protocols changed existing connections")
	}
	w := request("GET", "/api/client-export?email="+u.Email, nil)
	var exported struct {
		Xray    []json.RawMessage
		SingBox []json.RawMessage
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &exported) != nil || len(exported.Xray) != 8 || len(exported.SingBox) != 3 {
		t.Fatal("cloud export included unsupported transports or missed profiles", w.Code, w.Body.String())
	}
	// Removing a shared user must not remove the existing user's inbounds.
	w = request("POST", "/api/users/"+u.Email+"/rotate", map[string]any{})
	if w.Code != 200 {
		t.Fatal("cloud credential rotation failed", w.Code, w.Body.String())
	}
	rotated, ok := a.findUser(u.Email)
	if !ok || textField(rotated.Raw, "password") == textField(u.Raw, "password") {
		t.Fatal("Shadowsocks password did not rotate")
	}
	for _, p := range a.snapshot().Profiles {
		if p.Protocol == "shadowsocks" && !p.Direct {
			probeManualTraffic(t, ctx, a.cfg.EngineDir, p, rotated.Raw, public.URL)
		}
	}
	loaded, err := NewApp(a.cfg, a.base)
	if err != nil || loaded.refresh(ctx) != nil {
		t.Fatal("cloud panel reload failed", err)
	}
	current, ok = loaded.findUser(old.Email)
	if !ok || marshalString(current.Links) != marshalString(old.Links) {
		t.Fatal("existing links changed after panel reload")
	}
}

func allowNativeTestLoopback(t *testing.T, ctx context.Context, e *EngineClient) {
	t.Helper()
	// The official default template blocks private destinations. Add a
	// fixture-only outbound for our loopback echo server, leaving that block
	// and every production setting outside this temporary database intact.
	var raw json.RawMessage
	if err := e.call(ctx, "POST", "/panel/api/xray/", map[string]any{}, &raw); err != nil {
		t.Fatal(err)
	}
	var template map[string]any
	if err := nestedJSON(raw, &template); err != nil {
		t.Fatal(err)
	}
	if wrapped, ok := template["xraySetting"]; ok {
		if err := nestedJSON(json.RawMessage(marshalString(wrapped)), &template); err != nil {
			t.Fatal(err)
		}
	}
	if template == nil {
		t.Fatal("native test template missing")
	}
	outbounds, _ := template["outbounds"].([]any)
	template["outbounds"] = append(outbounds, map[string]any{"protocol": "freedom", "tag": "test-loopback-only", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}, map[string]any{"action": "block"}}}})
	routing, _ := template["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	probeRule := map[string]any{"type": "field", "ip": []string{"127.0.0.1/32"}, "outboundTag": "test-loopback-only"}
	if len(rules) == 0 {
		t.Fatal("native API route missing")
	}
	routing["rules"] = append([]any{rules[0], probeRule}, rules[1:]...)
	form := url.Values{"xraySetting": {marshalString(template)}}
	r, _ := http.NewRequestWithContext(ctx, "POST", e.base+"/panel/api/xray/update", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	e.mu.Lock()
	token := e.csrf
	e.mu.Unlock()
	r.Header.Set("X-CSRF-Token", token)
	resp, err := e.http.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var envelope engineResponse
	if json.NewDecoder(resp.Body).Decode(&envelope) != nil || !envelope.Success {
		t.Fatal("loopback test outbound could not be configured", resp.StatusCode, envelope.Msg)
	}
}

func probeManualTraffic(t *testing.T, ctx context.Context, engine string, p Profile, client map[string]any, origin string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	socksPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	config := map[string]any{"log": map[string]string{"loglevel": "warning"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socksPort, "protocol": "socks", "settings": map[string]any{"auth": "noauth"}}}, "outbounds": []any{xrayCloudOutbound(p, client, origin, LinkEndpoint{})}}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "client.json")
	if err = os.WriteFile(configPath, []byte(marshalString(config)), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "private-client.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.CommandContext(ctx, filepath.Join(engine, "bin", "xray-linux-amd64"), "run", "-config", configPath)
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	payload := strings.Repeat("manual-data-check-", 64)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { body, _ := io.ReadAll(r.Body); _, _ = w.Write(body) }))
	defer target.Close()
	socksURL, _ := url.Parse("socks5://127.0.0.1:" + strconv.Itoa(socksPort))
	tr := &http.Transport{Proxy: http.ProxyURL(socksURL), DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	httpClient := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	var lastErr error
	for i := 0; i < 25; i++ {
		resp, err := httpClient.Post(target.URL, "application/octet-stream", strings.NewReader(payload))
		lastErr = err
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == 200 && string(body) == payload {
				return
			}
		}
		if ctx.Err() != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "private-client.log"))
	t.Log("client test diagnostics", string(b))
	t.Fatal("real Xray traffic did not cross the manual route", p.Protocol, p.Network, lastErr)
}
