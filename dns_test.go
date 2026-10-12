package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func dnsFixture46(t *testing.T) (*App, DNSClient, *atomic.Int64) {
	t.Helper()
	a := automaticApp(t)
	c := DNSClient{ID: "dns-test", Key: strings.Repeat("a", 48), Name: "Personal", Enabled: true, Quota: 100, Domains: []string{"game.example"}}
	a.dns.data.Settings.Enabled = true
	a.dns.data.Clients = []DNSClient{c}
	calls := new(atomic.Int64)
	a.dns.http = &http.Client{Transport: feature45Transport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		q := new(dns.Msg)
		if q.Unpack(b) != nil {
			t.Error("invalid upstream question")
		}
		if q.IsEdns0() != nil && len(q.IsEdns0().Option) > 0 {
			t.Error("ECS or other options leaked")
		}
		m := new(dns.Msg)
		m.SetReply(q)
		m.RecursionAvailable = true
		m.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("8.8.4.4")}}
		wire, _ := m.Pack()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/dns-message"}}, Body: io.NopCloser(bytes.NewReader(wire))}, nil
	})}
	return a, c, calls
}
func dohRequest46(a *App, c DNSClient, q *dns.Msg, method string) *httptest.ResponseRecorder {
	b, _ := q.Pack()
	var r *http.Request
	if method == "GET" {
		r = httptest.NewRequest("GET", "/dns-query/"+c.Key+"?dns="+base64.RawURLEncoding.EncodeToString(b), nil)
	} else {
		r = httptest.NewRequest(method, "/dns-query/"+c.Key, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/dns-message")
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func dnsPost46(a *App, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	a.dnsAPI(w, r)
	return w
}
func TestPrivateDNSWireCacheQuotaRevocation(t *testing.T) {
	a, c, calls := dnsFixture46(t)
	q := new(dns.Msg)
	q.SetQuestion("game.example.", dns.TypeA)
	q.SetEdns0(4096, true)
	q.IsEdns0().Option = append(q.IsEdns0().Option, &dns.EDNS0_SUBNET{Code: dns.EDNS0SUBNET, Family: 1, SourceNetmask: 24, Address: net.ParseIP("8.8.8.0")})
	for _, method := range []string{"POST", "GET"} {
		q.Id++
		w := dohRequest46(a, c, q, method)
		m := new(dns.Msg)
		if w.Code != 200 || m.Unpack(w.Body.Bytes()) != nil || m.Id != q.Id || len(m.Answer) != 1 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatal("TTL cache not reused", calls.Load())
	}
	encoded, _ := json.Marshal(a.dnsPublic())
	if bytes.Contains(encoded, []byte(c.Key)) {
		t.Fatal("key exposed in public state")
	}
	a.dns.mu.Lock()
	a.dns.data.Clients[0].Quota = 2
	a.dns.mu.Unlock()
	if w := dohRequest46(a, c, q, "POST"); w.Code != 429 {
		t.Fatal("daily quota bypass", w.Code)
	}
	if w := dnsPost46(a, "/api/dns/clients/"+c.ID+"/rotate", map[string]any{}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := dohRequest46(a, c, q, "GET"); w.Code != 404 {
		t.Fatal("old key remains valid", w.Code)
	}
	fresh, ok := a.dnsClientByID(c.ID)
	if !ok || fresh.Key == c.Key {
		t.Fatal("rotation failed")
	}
	a.flushDNS()
	b, err := NewApp(a.cfg, a.base)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := b.dnsClientByID(c.ID)
	if !ok || restored.Key != fresh.Key || restored.DailyQueries != 2 {
		t.Fatal("private state / quota lost")
	}
	unauth := httptest.NewRecorder()
	a.ServeHTTP(unauth, httptest.NewRequest("GET", "/api/dns/export?id="+c.ID, nil))
	if unauth.Code != 401 {
		t.Fatal("DNS secret API unprotected")
	}
}
func TestDNSValidationAndUpstreamFallback(t *testing.T) {
	a, c, _ := dnsFixture46(t)
	q := new(dns.Msg)
	q.SetQuestion("game.example.", dns.TypeA)
	for _, qt := range []uint16{dns.TypeANY, dns.TypeAXFR, dns.TypeIXFR} {
		bad := q.Copy()
		bad.Question[0].Qtype = qt
		if w := dohRequest46(a, c, bad, "POST"); w.Code != 400 {
			t.Fatal("unsafe DNS query allowed", qt, w.Code)
		}
	}
	bad := q.Copy()
	bad.Question = append(bad.Question, bad.Question[0])
	if dohRequest46(a, c, bad, "POST").Code != 400 {
		t.Fatal("multi-question query allowed")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/dns-query/"+c.Key, strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	a.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
	a.dns.http = &http.Client{Transport: feature45Transport(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		m := new(dns.Msg)
		_ = m.Unpack(b)
		answer := new(dns.Msg)
		answer.SetReply(m)
		if r.URL.Host == "cloudflare-dns.com" {
			answer.Rcode = dns.RcodeServerFailure
		}
		wire, _ := answer.Pack()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/dns-message"}}, Body: io.NopCloser(bytes.NewReader(wire))}, nil
	})}
	if w := dohRequest46(a, c, q, "POST"); w.Code != 200 {
		t.Fatal("SERVFAIL fallback failed", w.Code, w.Body.String())
	}
	s := a.dns.dataSettings()
	s.ClassicEnabled = true
	if w := dnsPost46(a, "/api/dns/settings", s); w.Code != 400 {
		t.Fatal("raw DNS enabled in cloud mode")
	}
	for _, ip := range []string{"127.0.0.1", "192.168.1.1", "203.0.113.1", "198.18.1.1", "100.64.0.1", "2001:db8::1", "::ffff:127.0.0.1", "fec0::1", "3fff::1", "100::1", "5f00::1"} {
		if publicNetworkIP(net.ParseIP(ip)) {
			t.Fatal("reserved IP accepted", ip)
		}
	}
	for _, cidr := range []string{"0.0.0.0/0", "8.0.0.0/8", "127.0.0.1/32"} {
		v := DNSClient{Name: "test", Quota: 100, CIDRs: []string{cidr}}
		if validateDNSClient(&v) == nil {
			t.Fatal("unrestricted source accepted", cidr)
		}
	}
}
func TestSmartDNSSyntheticAnswersAndDomainBoundary(t *testing.T) {
	a, c, calls := dnsFixture46(t)
	c.Smart = true
	c.CIDRs = []string{"8.8.8.8/32"}
	s := a.dns.dataSettings()
	s.SmartEnabled = true
	s.SmartIP = "1.1.1.1"
	a.dns.active = s
	for _, qt := range []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeHTTPS, dns.TypeSVCB} {
		q := new(dns.Msg)
		q.SetQuestion("login.game.example.", qt)
		answer, e := a.answerDNS(context.Background(), q, c, s)
		if e != nil || answer.AuthenticatedData {
			t.Fatal(e)
		}
		if qt == dns.TypeA {
			if len(answer.Answer) != 1 || answer.Answer[0].(*dns.A).A.String() != s.SmartIP {
				t.Fatal(answer)
			}
		} else if len(answer.Answer) != 0 {
			t.Fatal("IPv6/ECH synthetic route bypass")
		}
	}
	q := new(dns.Msg)
	q.SetQuestion("notgame.example.", dns.TypeA)
	_, e := a.answerDNS(context.Background(), q, c, s)
	if e != nil || calls.Load() != 1 {
		t.Fatal("suffix boundary or unrelated resolution failed")
	}
	a.dns.active.SmartEnabled = false
	q.SetQuestion("login.game.example.", dns.TypeA)
	if _, e := a.answerDNS(context.Background(), q, c, s); e == nil {
		t.Fatal("dead Smart relay still advertised")
	}
}
func TestClassicDNSUDPAndTCPWithClientACL(t *testing.T) {
	a, c, _ := dnsFixture46(t)
	a.cfg.Mode = "vps"
	c.CIDRs = []string{"127.0.0.1/32"}
	a.dns.data.Clients = []DNSClient{c}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := l.Addr().String()
	_ = l.Close()
	s := a.dns.dataSettings()
	s.ClassicEnabled = true
	s.DNSListen = addr
	a.dns.data.Settings = s
	stop, e := a.startDNSListeners(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	q := new(dns.Msg)
	q.SetQuestion("game.example.", dns.TypeA)
	for _, network := range []string{"udp", "tcp"} {
		client := &dns.Client{Net: network, Timeout: time.Second}
		answer, _, e := client.Exchange(q, addr)
		if e != nil || answer.Rcode != dns.RcodeSuccess || len(answer.Answer) != 1 {
			t.Fatal(network, e, answer)
		}
	}
	a.dns.mu.Lock()
	a.dns.data.Clients[0].Enabled = false
	a.dns.mu.Unlock()
	answer, _, e := (&dns.Client{Net: "udp", Timeout: time.Second}).Exchange(q, addr)
	if e != nil || answer.Rcode != dns.RcodeRefused {
		t.Fatal("unauthorized resolver access", e, answer)
	}
}
func TestSmartTLSKeepsOriginCertificateAndEncryptedTraffic(t *testing.T) {
	a, c, _ := dnsFixture46(t)
	a.cfg.Mode = "vps"
	c.Smart = true
	c.CIDRs = []string{"127.0.0.1/32"}
	a.dns.data.Clients = []DNSClient{c}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(46), DNSNames: []string{"login.game.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "login.game.example" {
			t.Error("Host changed")
		}
		_, _ = w.Write([]byte("encrypted-game-response"))
	}))
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	origin.StartTLS()
	defer origin.Close()
	originAddr := strings.TrimPrefix(origin.URL, "https://")
	a.dns.resolveIP = func(_ context.Context, name string) ([]net.IPAddr, error) {
		if name != "login.game.example" {
			t.Error(name)
		}
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	a.dns.dialTLS = func(ctx context.Context, addr string) (net.Conn, error) {
		if addr != "8.8.8.8:443" {
			t.Error(addr)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", originAddr)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	s := a.dns.dataSettings()
	s.SmartEnabled = true
	s.SmartIP = "1.1.1.1"
	s.PanelHost = "panel.example"
	a.dns.data.Settings = s
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.acceptSmartTLS(ctx, listener, s)
	pool := x509.NewCertPool()
	leaf, _ := x509.ParseCertificate(der)
	pool.AddCert(leaf)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "login.game.example"}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, e := client.Get("https://login.game.example/hello")
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "encrypted-game-response" || !bytes.Equal(resp.TLS.PeerCertificates[0].Raw, der) {
		t.Fatal("TLS terminated or game traffic not carried")
	}
	a.dns.mu.Lock()
	a.dns.data.Clients[0].Enabled = false
	a.dns.mu.Unlock()
	tr.CloseIdleConnections()
	if resp, e = client.Get("https://login.game.example/"); e == nil {
		_ = resp.Body.Close()
		t.Fatal("revoked client can still open game relay")
	}
}
func TestDNSRelayTemplateAndLegacyExport(t *testing.T) {
	a, c, _ := dnsFixture46(t)
	for _, provider := range []string{"vercel", "deno", "cloudflare-worker"} {
		source := relaySource(provider, "https://origin.example")
		if !strings.Contains(source, "dns-query") || strings.Contains(source, "__ORIGIN__") {
			t.Fatal(provider)
		}
	}
	a.mu.Lock()
	a.state.Settings.PublicURL = "https://panel.example"
	a.mu.Unlock()
	a.edges.Routes = []EdgeRoute{{ID: "old", Resource: "old-project", Enabled: true, Verified: true, Endpoint: LinkEndpoint{Host: "relay.example", Port: 443}}}
	if _, e := a.dnsURL(c, "old"); e == nil {
		t.Fatal("old deployment falsely claims DoH support")
	}
	a.edges.Routes[0].DNSRelay = true
	if link, e := a.dnsURL(c, "old"); e != nil || !strings.Contains(link, c.Key) {
		t.Fatal(e, link)
	}
	if _, e := NewApp(Config{DataDir: filepath.Join(t.TempDir(), "empty"), Mode: "cloud"}, "http://127.0.0.1:1"); e != nil {
		t.Fatal(e)
	}
}

func TestDNSAccountAPIRestoreAndSourceOwnership(t *testing.T) {
	a := automaticApp(t)
	input := map[string]any{"name": "Home", "enabled": true, "quota": 1000, "days": 7, "cidrs": []string{"8.8.8.8"}, "presets": []string{"steam"}, "domains": []string{}, "smart": false}
	w := dnsPost46(a, "/api/dns/clients", input)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	c := a.dns.data.Clients[0]
	if c.CIDRs[0] != "8.8.8.8/32" || len(c.Key) != 48 || bytes.Contains(w.Body.Bytes(), []byte(c.Key)) {
		t.Fatal("invalid normalization / secret redaction")
	}
	input["name"] = "Other"
	if dnsPost46(a, "/api/dns/clients", input).Code != 400 {
		t.Fatal("overlapping source ownership allowed")
	}
	input["id"] = c.ID
	input["name"] = "Renamed"
	if dnsPost46(a, "/api/dns/clients", input).Code != 200 || a.dns.data.Clients[0].Key != c.Key {
		t.Fatal("edit changed private key")
	}
	backup := a.dns.data
	b := automaticApp(t)
	if w := dnsPost46(b, "/api/dns/backup", backup); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if b.dns.data.Clients[0].Key != c.Key || b.dns.data.Settings.Enabled {
		t.Fatal("restore loses identity or unexpectedly opens service")
	}
	if w := dnsPost46(b, "/api/dns/backup", backup); w.Code != 200 || len(b.dns.data.Clients) != 1 {
		t.Fatal("restore not idempotent")
	}
}

func clientHelloFixture46(name string, ech bool) []byte {
	sni := []byte{0}
	sni = append(sni, byte(len(name)>>8), byte(len(name)))
	sni = append(sni, []byte(name)...)
	list := append([]byte{byte(len(sni) >> 8), byte(len(sni))}, sni...)
	ext := append([]byte{0, 0, byte(len(list) >> 8), byte(len(list))}, list...)
	if ech {
		ext = append(ext, 0xfe, 0x0d, 0, 0)
	}
	body := append([]byte{3, 3}, make([]byte, 32)...)
	body = append(body, 0, 0, 2, 0x13, 1, 1, 0, byte(len(ext)>>8), byte(len(ext)))
	body = append(body, ext...)
	hello := append([]byte{1, 0, byte(len(body) >> 8), byte(len(body))}, body...)
	return hello
}
func TestClientHelloFragmentationECHAndBounds(t *testing.T) {
	for _, ech := range []bool{false, true} {
		hello := clientHelloFixture46("game.example", ech)
		var records []byte
		for _, b := range [][]byte{hello[:12], hello[12:]} {
			records = append(records, 22, 3, 1, byte(len(b)>>8), byte(len(b)))
			records = append(records, b...)
		}
		raw, name, e := readTLSClientHello(bytes.NewReader(records))
		if ech {
			if e == nil {
				t.Fatal("ECH routed by untrusted outer name")
			}
		} else if e != nil || name != "game.example" || !bytes.Equal(raw, records) {
			t.Fatal("fragmented ClientHello lost", e, name)
		}
	}
	if _, _, e := readTLSClientHello(bytes.NewReader([]byte{22, 3, 1, 255, 255})); e == nil {
		t.Fatal("oversized record accepted")
	}
	if gameDomainMatches("evilgame.example", []string{"game.example"}) {
		t.Fatal("SNI whitelist suffix confusion")
	}
}
func FuzzTLSClientHello46(f *testing.F) {
	hello := clientHelloFixture46("game.example", false)
	record := append([]byte{22, 3, 1, 0, byte(len(hello))}, hello...)
	f.Add(record)
	f.Add([]byte{22, 3, 1, 255, 255})
	f.Fuzz(func(t *testing.T, b []byte) { _, _, _ = readTLSClientHello(bytes.NewReader(b)) })
}
func TestProbeRejectsNonPublicCandidateBeforeNetwork(t *testing.T) {
	a := automaticApp(t)
	report, e := a.probeEdgeDetailed(context.Background(), EdgeRoute{Endpoint: LinkEndpoint{Address: "127.0.0.1", Host: "panel.example", SNI: "panel.example", Port: 443}})
	if e == nil || report.Failure != "dns" || report.OK || len(report.Stages) != 1 {
		t.Fatal("unsafe probe allowed", report, e)
	}
	a.edges.Routes = []EdgeRoute{{ID: "edge-test", Endpoint: LinkEndpoint{Address: "1.1.1.1", Host: "panel.example", SNI: "panel.example", Port: 443}}}
	r := httptest.NewRequest("POST", "/api/resilience/probe", strings.NewReader(`{"route":"edge-test","ips":["203.0.113.1"]}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.candidateProbeAPI(w, r)
	if w.Code != 400 {
		t.Fatal("reserved candidate allowed", w.Code)
	}
}

func TestUnauthorizedDoHDoesNotWaitForUpload(t *testing.T) {
	a, _, _ := dnsFixture46(t)
	server := httptest.NewServer(a)
	defer server.Close()
	c, e := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = io.WriteString(c, "POST /dns-query/"+strings.Repeat("b", 48)+" HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/dns-message\r\nContent-Length: 1000\r\n\r\n")
	// No body is uploaded; the unknown key must be rejected immediately.
	resp, e := http.ReadResponse(bufio.NewReader(c), nil)
	if e != nil {
		t.Fatal("unknown DNS key waits for body", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 || !resp.Close {
		t.Fatal(resp.StatusCode, resp.Close)
	}
}
