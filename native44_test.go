//go:build integration

package main

import (
	"context"
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

func TestNativeAdvancedPreviewAndMetadata(t *testing.T) {
	a, ctx := nativeTestApp(t)
	for _, network := range []string{"ws", "xhttp"} {
		m := ManualCreate{Protocol: "vless", Network: network, Tuning: ManualTuning{KeepAliveIdle: 60, KeepAliveInterval: 30, TCPUserTimeoutMS: 20000, Fingerprint: "firefox"}}
		if network == "xhttp" {
			m.Mode = "packet-up"
			m.Tuning.XHTTPMaxConnections = 2
			m.Tuning.PostMaxKB = 512
			m.Tuning.PostIntervalMS = 30
			m.Tuning.BufferedPosts = 30
		}
		body, _ := json.Marshal(m)
		r := httptest.NewRequest("POST", "/api/manual/preview", strings.NewReader(string(body))).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.previewManual(w, r)
		if w.Code != 200 {
			t.Fatalf("native preview %s: %d %s", network, w.Code, w.Body.String())
		}
		var preview struct {
			Verified bool `json:"verified"`
			Client   any  `json:"client"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &preview)
		if !preview.Verified {
			t.Fatal("not checked by native binary")
		}
		clientFile := filepath.Join(t.TempDir(), "client.json")
		b, _ := json.Marshal(preview.Client)
		_ = os.WriteFile(clientFile, b, 0600)
		if out, e := exec.CommandContext(ctx, filepath.Join(a.cfg.EngineDir, "bin", "xray-linux-amd64"), "run", "-test", "-config", clientFile).CombinedOutput(); e != nil {
			t.Fatalf("client config invalid %s: %s", network, out)
		}
		create, _ := json.Marshal(CreateRequest{Name: "Advanced " + network, Count: 1, Days: 2, QuotaGB: 1, Manual: &m})
		r = httptest.NewRequest("POST", "/api/users", strings.NewReader(string(create))).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		a.createUsers(w, r, a.service)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		s := a.snapshot()
		found := false
		for _, p := range s.Profiles {
			if p.Manual && p.Network == network && p.Tuning.KeepAliveIdle == 60 {
				found = true
			}
		}
		if !found {
			t.Fatal("native round trip lost tuning", network)
		}
	}
}
func TestNativeSingBoxTunnelStatsAndQuota(t *testing.T) {
	bin := os.Getenv("ARIA_TEST_SING_BOX")
	engine := os.Getenv("ARIA_TEST_ENGINE_DIR")
	if bin == "" || engine == "" {
		t.Skip("native binaries required")
	}
	t.Setenv("ARIA_SING_BOX_BIN", bin)
	dir := t.TempDir()
	a, e := NewApp(Config{DataDir: dir, EngineDir: engine, AdminUser: "admin", AdminPassword: "admin", Mode: "cloud", Port: 8080}, "http://127.0.0.1:20530")
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(a)
	defer server.Close()
	a.mu.Lock()
	a.state.Settings.PublicURL = server.URL
	a.mu.Unlock()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("response-data-", 500))
	}))
	defer target.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { a.managedCoreLoop(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	r := httptest.NewRequest("POST", "/api/managed-cores/services", strings.NewReader(`{"name":"Native SB","kind":"vless-ws","quotaGB":0.000001,"days":1}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.managedAPI(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	v := a.coresSnapshot().Services[0]
	for i := 0; i < 60; i++ {
		a.coresMu.RLock()
		running := a.managedStatus.Running
		a.coresMu.RUnlock()
		if running {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	client := map[string]any{"inbounds": []any{map[string]any{"type": "mixed", "listen": "127.0.0.1", "listen_port": port}}, "outbounds": []any{a.coreClient(v)}, "route": map[string]string{"final": v.ID}}
	p := filepath.Join(dir, "client.json")
	b, _ := json.Marshal(client)
	_ = os.WriteFile(p, b, 0600)
	cmd := exec.CommandContext(ctx, bin, "run", "-c", p)
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	proxy, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	cl := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: 5 * time.Second}
	var resp *http.Response
	for i := 0; i < 30; i++ {
		resp, e = cl.Get(target.URL)
		if e == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if e != nil {
		t.Fatal("sing-box tunnel failed", e, a.managedCoreSummary())
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if len(data) < 6000 {
		t.Fatal("wrong tunneled response")
	}
	if e = a.collectCoreStats(ctx); e != nil {
		out, _ := exec.CommandContext(ctx, filepath.Join(engine, "bin", "xray-linux-amd64"), "api", "statsquery", "--server=127.0.0.1:"+strconv.Itoa(managedStatsPort), "-pattern", "user>>>").CombinedOutput()
		t.Fatal("native traffic stats failed", e, string(out))
	}
	live := a.coresSnapshot().Services[0]
	if live.Used < live.Quota || coreActive(live) {
		t.Fatal("quota not enforced by real traffic", live.Used, live.Quota)
	}
	if len(a.managedRoutes()) != 0 {
		t.Fatal("exhausted route still public")
	}
}
