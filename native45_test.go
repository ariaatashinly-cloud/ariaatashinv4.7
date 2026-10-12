//go:build integration

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
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

func TestNativeVercelRelayCarriesXrayTraffic(t *testing.T) { nativeRelayTraffic45(t, "vercel") }
func TestNativeDenoRelayCarriesXrayTraffic(t *testing.T)   { nativeRelayTraffic45(t, "deno") }
func nativeRelayTraffic45(t *testing.T, provider string) {
	ws := os.Getenv("ARIA_TEST_WS_MODULE")
	if provider == "vercel" && ws == "" {
		t.Skip("set ARIA_TEST_WS_MODULE to installed ws package")
	}
	deno := os.Getenv("ARIA_TEST_DENO")
	if provider == "deno" && deno == "" {
		t.Skip("set ARIA_TEST_DENO to a Deno runtime")
	}
	a, ctx := nativeTestApp(t)
	allowNativeTestLoopback(t, ctx, a.service)
	certPair, rootCert := relayCertificate45(t)
	origin := httptest.NewUnstartedServer(a)
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{certPair}}
	origin.StartTLS()
	defer origin.Close()
	a.mu.Lock()
	a.state.Settings.PublicURL = origin.URL
	a.mu.Unlock()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/users", strings.NewReader(`{"name":"Relay client","count":1,"quotaGB":1,"days":1,"profiles":["vless-ws"]}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	a.createUsers(w, r, a.service)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	u := a.snapshot().Users[0]
	var profile Profile
	for _, p := range a.snapshot().Profiles {
		if p.Key == "vless-ws" {
			profile = p
		}
	}
	dir := t.TempDir()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootCert})
	ca := filepath.Join(dir, "ca.pem")
	_ = os.WriteFile(ca, cert, 0600)
	module := filepath.Join(dir, "relay.mjs")
	_ = os.WriteFile(module, []byte(relaySource("vercel", origin.URL)), 0600)
	_ = os.Mkdir(filepath.Join(dir, "node_modules"), 0700)
	if e := os.Symlink(ws, filepath.Join(dir, "node_modules", "ws")); provider == "vercel" && e != nil {
		t.Fatal(e)
	}
	boot := filepath.Join(dir, "boot.mjs")
	_ = os.WriteFile(boot, []byte(`import server from './relay.mjs';server.listen(0,'127.0.0.1',()=>console.log(server.address().port));`), 0600)
	node := exec.CommandContext(ctx, "node", boot)
	if provider == "deno" {
		module = filepath.Join(dir, "relay.ts")
		source := strings.Replace(relaySource("deno", origin.URL), "Deno.serve(async(req)=>{", `Deno.serve({hostname:"127.0.0.1",port:0,onListen:({port})=>console.log(port)},async(req)=>{`, 1)
		_ = os.WriteFile(module, []byte(source), 0600)
		node = exec.CommandContext(ctx, deno, "run", "--allow-net", "--cert", ca, module)
	}
	node.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca, "DENO_CERT="+ca)
	stdout, e := node.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	node.Stderr = os.Stderr
	if e := node.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = node.Process.Kill(); _ = node.Wait() }()
	buf := make([]byte, 64)
	n, e := stdout.Read(buf)
	if e != nil {
		t.Fatal(e)
	}
	relayPort, e := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if e != nil {
		t.Fatal(e)
	}
	relayOrigin := "http://127.0.0.1:" + strconv.Itoa(relayPort)
	resp, e := http.Get(relayOrigin + "/api/state")
	if e != nil {
		t.Fatal(e)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("relay exposed admin API")
	}
	resp, e = http.Get(relayOrigin + "/live")
	if e != nil {
		t.Fatal(e)
	}
	var live struct {
		Identity string `json:"identity"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&live)
	_ = resp.Body.Close()
	if live.Identity != a.edgeSnapshot().Identity {
		t.Fatal("wrong origin identity", resp.StatusCode, live.Identity, provider)
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	socks := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	out := xrayCloudOutbound(profile, u.Raw, relayOrigin, LinkEndpoint{})
	config := map[string]any{"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": false}}}, "outbounds": []any{out}}
	b, _ := json.Marshal(config)
	clientFile := filepath.Join(dir, "xray-client.json")
	_ = os.WriteFile(clientFile, b, 0600)
	client := exec.CommandContext(ctx, filepath.Join(a.cfg.EngineDir, "bin", "xray-linux-amd64"), "run", "-config", clientFile)
	if e := client.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = client.Process.Kill(); _ = client.Wait() }()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "actual-relay-xray-response") }))
	defer target.Close()
	proxy, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", socks))
	cl := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	var got string
	for i := 0; i < 35; i++ {
		resp, e = cl.Get(target.URL)
		if e == nil {
			data, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			got = string(data)
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if got != "actual-relay-xray-response" {
		t.Fatal("native proxy relay failed", e, got)
	}
	a.geoMu.Lock()
	a.geo.Exit = GeoInfo{Code: "DE", Country: "Germany", Flag: "🇩🇪"}
	a.geoMu.Unlock()
	// Generated failover config must be accepted by the actual Xray parser.
	a.edges.Routes = []EdgeRoute{{ID: "relay-local", Name: "Relay", Enabled: true, Verified: true, Networks: []string{"ws"}, Endpoint: LinkEndpoint{Address: "127.0.0.1", Port: relayPort, Host: "localhost", SNI: "localhost"}}}
	w = httptest.NewRecorder()
	a.edgeExport(w, httptest.NewRequest("GET", "/api/edges/export?email="+url.QueryEscape(u.Email), nil))
	var exported struct {
		Config any `json:"xrayConfig"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &exported) != nil {
		t.Fatal(w.Body.String())
	}
	b, _ = json.Marshal(exported.Config)
	_ = os.WriteFile(clientFile, b, 0600)
	if data, e := exec.CommandContext(context.Background(), filepath.Join(a.cfg.EngineDir, "bin", "xray-linux-amd64"), "run", "-test", "-config", clientFile).CombinedOutput(); e != nil {
		t.Fatal("export config rejected", e, string(data))
	}
}

func relayCertificate45(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	rootKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Aria Relay Test Root"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	rootDER, e := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	if e != nil {
		t.Fatal(e)
	}
	leafKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Aria Relay Test Leaf"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	leafDER, e := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, rootKey)
	if e != nil {
		t.Fatal(e)
	}
	return tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}, rootDER
}
