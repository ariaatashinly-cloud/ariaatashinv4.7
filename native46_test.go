//go:build integration

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
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

	"github.com/miekg/dns"
)

func startRelay46(t *testing.T, ctx context.Context, provider, origin string, root []byte) string {
	t.Helper()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root}), 0600)
	module := filepath.Join(dir, "relay.mjs")
	_ = os.WriteFile(module, []byte(relaySource(provider, origin)), 0600)
	var cmd *exec.Cmd
	switch provider {
	case "vercel":
		ws := os.Getenv("ARIA_TEST_WS_MODULE")
		if ws == "" {
			t.Skip("set ARIA_TEST_WS_MODULE")
		}
		_ = os.Mkdir(filepath.Join(dir, "node_modules"), 0700)
		if e := os.Symlink(ws, filepath.Join(dir, "node_modules", "ws")); e != nil {
			t.Fatal(e)
		}
		boot := filepath.Join(dir, "boot.mjs")
		_ = os.WriteFile(boot, []byte(`import server from './relay.mjs';server.listen(0,'127.0.0.1',()=>console.log(server.address().port));`), 0600)
		cmd = exec.CommandContext(ctx, "node", boot)
	case "deno":
		bin := os.Getenv("ARIA_TEST_DENO")
		if bin == "" {
			t.Skip("set ARIA_TEST_DENO")
		}
		module = filepath.Join(dir, "relay.ts")
		source := strings.Replace(relaySource(provider, origin), "Deno.serve(async(req)=>{", `Deno.serve({hostname:"127.0.0.1",port:0,onListen:({port})=>console.log(port)},async(req)=>{`, 1)
		_ = os.WriteFile(module, []byte(source), 0600)
		cmd = exec.CommandContext(ctx, bin, "run", "--allow-net", "--cert", ca, module)
	case "cloudflare-worker":
		boot := filepath.Join(dir, "boot.mjs")
		// Exercise the Worker fetch handler and binary relay using Node's real
		// fetch/Request/Response runtime. The hosted Cloudflare account is not used.
		_ = os.WriteFile(boot, []byte(`import http from 'node:http';import worker from './relay.mjs';const nativeFetch=globalThis.fetch;globalThis.fetch=(url,init={})=>nativeFetch(url,{...init,...(init.body?{duplex:'half'}:{})});const server=http.createServer(async(req,res)=>{try{const chunks=[];for await(const c of req)chunks.push(c);const request=new Request('http://relay.invalid'+req.url,{method:req.method,headers:req.headers,body:req.method==='POST'?Buffer.concat(chunks):undefined});const response=await worker.fetch(request);res.writeHead(response.status,Object.fromEntries(response.headers));res.end(Buffer.from(await response.arrayBuffer()));}catch(e){console.error(e);res.writeHead(500);res.end();}});server.listen(0,'127.0.0.1',()=>console.log(server.address().port));`), 0600)
		cmd = exec.CommandContext(ctx, "node", boot)
	}
	cmd.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca)
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stderr = os.Stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, e := bufio.NewReader(stdout).ReadString('\n')
	if e != nil {
		t.Fatal(e)
	}
	port, e := strconv.Atoi(strings.TrimSpace(line))
	if e != nil {
		t.Fatal(line, e)
	}
	return "http://127.0.0.1:" + strconv.Itoa(port)
}
func TestNativePrivateDNSAcrossProviderRelays(t *testing.T) {
	for _, provider := range []string{"vercel", "deno", "cloudflare-worker"} {
		t.Run(provider, func(t *testing.T) {
			a, c, _ := dnsFixture46(t)
			cert, root := relayCertificate45(t)
			origin := httptest.NewUnstartedServer(a)
			origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
			origin.StartTLS()
			defer origin.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			relay := startRelay46(t, ctx, provider, origin.URL, root)
			q := new(dns.Msg)
			q.SetQuestion("game.example.", dns.TypeA)
			wire, _ := q.Pack()
			cl := &http.Client{Timeout: 5 * time.Second}
			for _, method := range []string{"POST", "GET"} {
				uri := relay + "/dns-query/" + c.Key
				var body io.Reader
				if method == "GET" {
					uri += "?dns=" + base64.RawURLEncoding.EncodeToString(wire)
				} else {
					body = bytes.NewReader(wire)
				}
				req, _ := http.NewRequest(method, uri, body)
				req.Header.Set("Content-Type", "application/dns-message")
				req.Header.Set("Cookie", "atia_session=private")
				resp, e := cl.Do(req)
				if e != nil {
					t.Fatal(e)
				}
				b, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				answer := new(dns.Msg)
				if resp.StatusCode != 200 || answer.Unpack(b) != nil || len(answer.Answer) != 1 {
					t.Fatal(method, resp.StatusCode, string(b))
				}
			}
			resp, e := cl.Get(relay + "/api/dns/backup")
			if e != nil {
				t.Fatal(e)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != 404 {
				t.Fatal("management API forwarded")
			}
		})
	}
}

func TestLiveHTTPSDNS46(t *testing.T) {
	if os.Getenv("ARIA_TEST_LIVE_DNS") != "true" {
		t.Skip("set ARIA_TEST_LIVE_DNS=true for public HTTPS DNS checks")
	}
	a := automaticApp(t)
	q := new(dns.Msg)
	q.SetQuestion("example.com.", dns.TypeA)
	for _, name := range []string{"cloudflare", "google", "quad9"} {
		t.Run(name, func(t *testing.T) {
			s := a.dns.dataSettings()
			s.Upstreams = []string{name}
			s.Cache = false
			s.TimeoutMS = 8000
			answer, e := a.resolveDoH(context.Background(), q, s)
			if e != nil || answer.Rcode != dns.RcodeSuccess || len(answer.Answer) == 0 {
				t.Fatal("public HTTPS resolver failed", e, answer)
			}
		})
	}
}
func TestNativeGamingExportAndUDPOverVLESSWebSocket(t *testing.T) {
	for _, provider := range []string{"origin", "vercel", "deno", "selected-exit"} {
		t.Run(provider, func(t *testing.T) { nativeGameTraffic46(t, provider) })
	}
}
func nativeGameTraffic46(t *testing.T, provider string) {
	bin := os.Getenv("ARIA_TEST_SING_BOX")
	if bin == "" {
		t.Skip("set ARIA_TEST_SING_BOX")
	}
	a, ctx := nativeTestApp(t)
	allowNativeTestLoopback(t, ctx, a.service)
	cert, root := relayCertificate45(t)
	origin := httptest.NewUnstartedServer(a)
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	origin.StartTLS()
	defer origin.Close()
	a.mu.Lock()
	a.state.Settings.PublicURL = origin.URL
	a.mu.Unlock()
	w := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/users", strings.NewReader(`{"name":"UDP game","count":1,"quotaGB":1,"days":1,"profiles":["vless-ws"]}`)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	a.createUsers(w, request, a.service)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	u := a.snapshot().Users[0]
	c := DNSClient{ID: "dns-native", Key: strings.Repeat("b", 48), Name: "Game DNS", Enabled: true, Quota: 1000, Presets: []string{"steam"}}
	a.dns.data.Clients = []DNSClient{c}
	a.dns.data.Settings.Enabled = true
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root}), 0600)
	var config map[string]any
	selectedQuery := ""
	if provider == "selected-exit" {
		var profile Profile
		for _, p := range a.snapshot().Profiles {
			if p.Key == "vless-ws" {
				profile = p
			}
		}
		extra := singBoxOutboundWithEndpoint(profile, u.Raw, origin.URL, LinkEndpoint{})
		delete(extra, "tag")
		id := "gx-" + strings.Repeat("a", 16)
		a.gameExits = []GameExit{{ID: id, Name: "خروجی منتخب واقعی", Enabled: true, Outbound: extra}}
		selectedQuery = "&extras=" + id + "&exit=" + id
	}
	for _, mode := range []string{"games", "full"} {
		w = httptest.NewRecorder()
		a.gamingExport(w, httptest.NewRequest("GET", "/api/gaming/export?email="+url.QueryEscape(u.Email)+"&profile=vless-ws&dns="+c.ID+"&mode="+mode+selectedQuery, nil))
		var result struct {
			Config map[string]any `json:"config"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		config = result.Config
		b, _ := json.Marshal(config)
		p := filepath.Join(dir, "game-"+mode+".json")
		_ = os.WriteFile(p, b, 0600)
		if data, e := exec.CommandContext(ctx, bin, "check", "-c", p).CombinedOutput(); e != nil {
			t.Fatal("real sing-box rejected TUN export", mode, e, string(data))
		}
	}
	// The exported outbound is unchanged except for trusting the fixture's CA.
	out := config["outbounds"].([]any)[0].(map[string]any)
	for _, o := range config["outbounds"].([]any) {
		v := o.(map[string]any)
		if tls, ok := v["tls"].(map[string]any); ok {
			tls["certificate_path"] = ca
		}
	}
	if provider == "selected-exit" {
		out["server"] = "unreachable-origin.example"
	}

	if provider != "origin" && provider != "selected-exit" {
		relay := startRelay46(t, ctx, provider, origin.URL, root)
		uri, _ := url.Parse(relay)
		port, _ := strconv.Atoi(uri.Port())
		// Hosted providers terminate HTTPS before this HTTP runtime. Use the local
		// runtime listener directly while still validating TLS to the real origin.
		out["server"] = uri.Hostname()
		out["server_port"] = port
		delete(out, "tls")
	}

	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	clientConfig := map[string]any{"log": map[string]string{"level": "warn"}, "inbounds": []any{map[string]any{"type": "mixed", "listen": "127.0.0.1", "listen_port": port}}, "outbounds": config["outbounds"], "dns": map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "bootstrap"}}}, "route": map[string]any{"final": "game-route", "default_domain_resolver": "bootstrap"}}
	b, _ := json.Marshal(clientConfig)
	p := filepath.Join(dir, "udp-client.json")
	_ = os.WriteFile(p, b, 0600)
	var log bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "run", "-c", p)
	cmd.Stdout = &log
	cmd.Stderr = &log
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	var control net.Conn
	for i := 0; i < 60; i++ {
		control, e = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if e == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if e != nil {
		t.Fatal("SOCKS listener unavailable", e)
	}
	defer control.Close()
	_ = control.SetDeadline(time.Now().Add(8 * time.Second))
	_, _ = control.Write([]byte{5, 1, 0})
	hello := make([]byte, 2)
	if _, e = io.ReadFull(control, hello); e != nil || hello[1] != 0 {
		t.Fatal(e, hello)
	}
	_, _ = control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	header := make([]byte, 4)
	if _, e = io.ReadFull(control, header); e != nil || header[1] != 0 {
		t.Fatal(e, header)
	}
	n := 4
	if header[3] == 4 {
		n = 16
	}
	addrBytes := make([]byte, n+2)
	if _, e = io.ReadFull(control, addrBytes); e != nil {
		t.Fatal(e)
	}
	proxyUDP := &net.UDPAddr{IP: net.IP(addrBytes[:n]), Port: int(binary.BigEndian.Uint16(addrBytes[n:]))}
	if proxyUDP.IP.IsUnspecified() {
		proxyUDP.IP = net.ParseIP("127.0.0.1")
	}
	echo, e := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if e != nil {
		t.Fatal(e)
	}
	defer echo.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, e := echo.ReadFromUDP(buf)
			if e != nil {
				return
			}
			_, _ = echo.WriteToUDP(buf[:n], addr)
		}
	}()
	udp, e := net.DialUDP("udp", nil, proxyUDP)
	if e != nil {
		t.Fatal(e)
	}
	defer udp.Close()
	_ = udp.SetDeadline(time.Now().Add(8 * time.Second))
	packet := []byte{0, 0, 0, 1, 127, 0, 0, 1}
	packet = binary.BigEndian.AppendUint16(packet, uint16(echo.LocalAddr().(*net.UDPAddr).Port))
	packet = append(packet, []byte("aria-real-udp-game")...)
	if _, e = udp.Write(packet); e != nil {
		t.Fatal(e)
	}
	buf := make([]byte, 2048)
	n, e = udp.Read(buf)
	if e != nil || !bytes.HasSuffix(buf[:n], []byte("aria-real-udp-game")) {
		t.Fatal("actual XUDP traffic failed", e)
	}
}
