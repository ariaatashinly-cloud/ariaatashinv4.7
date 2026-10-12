package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func dnsResponse47(r *http.Request, ip string) *http.Response {
	b, _ := io.ReadAll(r.Body)
	q := new(dns.Msg)
	_ = q.Unpack(b)
	answer := new(dns.Msg)
	answer.SetReply(q)
	answer.RecursionAvailable = true
	answer.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP(ip)}}
	wire, _ := answer.Pack()
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/dns-message"}}, Body: io.NopCloser(bytes.NewReader(wire))}
}
func TestAdaptiveDNSHedgeAndCanceledLoser(t *testing.T) {
	a, _, _ := dnsFixture46(t)
	s := a.dns.dataSettings()
	s.Cache = false
	s.HedgeMS = 50
	s.TimeoutMS = 1000
	s.ResolverMode = "adaptive"
	canceled := make(chan struct{}, 1)
	var third atomic.Int64
	a.dns.http = &http.Client{Transport: feature45Transport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "cloudflare-dns.com":
			select {
			case <-r.Context().Done():
				canceled <- struct{}{}
				return nil, r.Context().Err()
			case <-time.After(700 * time.Millisecond):
				return dnsResponse47(r, "9.9.9.9"), nil
			}
		case "dns.google":
			return dnsResponse47(r, "8.8.4.4"), nil
		default:
			third.Add(1)
			return dnsResponse47(r, "1.1.1.1"), nil
		}
	})}
	q := new(dns.Msg)
	q.SetQuestion("game.example.", dns.TypeA)
	start := time.Now()
	answer, e := a.resolveDoH(context.Background(), q, s)
	if e != nil || answer.Answer[0].(*dns.A).A.String() != "8.8.4.4" || time.Since(start) > 600*time.Millisecond {
		t.Fatal("slow primary stalls DNS", e, answer)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("hedge loser not canceled")
	}
	if third.Load() != 0 {
		t.Fatal("extra resolver queried after a fast winner")
	}
	if a.dns.upstreamOrder(s)[0] != "google" {
		t.Fatal("observed responsive resolver not preferred")
	}
	a.dns.mu.Lock()
	failed := a.dns.resolverStats["cloudflare"].Failures
	a.dns.mu.Unlock()
	if failed != 0 {
		t.Fatal("canceled loser penalized as failed")
	}
}
func TestDNSCoalescingPreservesIDsAndSurvivesOwnerCancellation(t *testing.T) {
	a, _, _ := dnsFixture46(t)
	s := a.dns.dataSettings()
	s.Cache = false
	s.ResolverMode = "ordered"
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	var calls atomic.Int64
	a.dns.http = &http.Client{Transport: feature45Transport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-gate:
			return dnsResponse47(r, "8.8.4.4"), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}
	q := new(dns.Msg)
	q.SetQuestion("game.example.", dns.TypeA)
	owner, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, e := a.resolveDoH(owner, q, s); first <- e }()
	<-started
	var wg sync.WaitGroup
	var bad atomic.Int64
	for i := 0; i < 19; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			copy := q.Copy()
			copy.Id = uint16(100 + i)
			copy.Question[0].Name = "GaMe.Example."
			answer, e := a.resolveDoH(context.Background(), copy, s)
			if e != nil || answer.Id != copy.Id || answer.Question[0].Name != copy.Question[0].Name {
				bad.Add(1)
			}
		}(i)
	}
	deadline := time.Now().Add(time.Second)
	for {
		a.dns.mu.Lock()
		f := a.dns.flights[resolverKey(q, s)]
		count := 0
		if f != nil {
			count = f.waiters
		}
		a.dns.mu.Unlock()
		if count == 20 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("requests not coalesced", count)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if e := <-first; e != context.Canceled {
		t.Fatal("caller context not honored", e)
	}
	close(gate)
	wg.Wait()
	if calls.Load() != 1 || bad.Load() != 0 {
		t.Fatal("shared query duplicated / IDs mixed", calls.Load(), bad.Load())
	}
}
func TestDNSCacheNamespacesTTLAndLRU(t *testing.T) {
	a, _, calls := dnsFixture46(t)
	s := a.dns.dataSettings()
	s.ResolverMode = "ordered"
	q := new(dns.Msg)
	q.SetQuestion("game.example.", dns.TypeA)
	if _, e := a.resolveDoH(context.Background(), q, s); e != nil {
		t.Fatal(e)
	}
	key := resolverKey(q, s)
	a.dns.mu.Lock()
	c := a.dns.cache[key]
	c.Added = time.Now().Add(-10 * time.Second)
	a.dns.cache[key] = c
	a.dns.mu.Unlock()
	answer, e := a.resolveDoH(context.Background(), q, s)
	if e != nil || answer.Answer[0].Header().Ttl > 50 {
		t.Fatal("cache TTL not aged", e)
	}
	other := s
	other.Upstreams = []string{"google"}
	_, e = a.resolveDoH(context.Background(), q, other)
	if e != nil || calls.Load() != 2 {
		t.Fatal("upstream settings reuse previous cache", e, calls.Load())
	}
	a.dns.mu.Lock()
	c = a.dns.cache[key]
	c.Expires = time.Now().Add(-time.Second)
	a.dns.cache[key] = c
	a.dns.mu.Unlock()
	_, e = a.resolveDoH(context.Background(), q, s)
	if e != nil || calls.Load() != 3 {
		t.Fatal("expired response served", e, calls.Load())
	}
	a.dns.mu.Lock()
	a.dns.cache = map[string]dnsCacheEntry{}
	for i := 0; i < 1024; i++ {
		a.dns.cache[fmt.Sprint(i)] = dnsCacheEntry{Message: answer.Copy(), Added: time.Now(), Expires: time.Now().Add(time.Minute), Accessed: time.Now().Add(-time.Hour)}
	}
	hot := a.dns.cache["0"]
	hot.Accessed = time.Now()
	a.dns.cache["0"] = hot
	a.dns.storeDNSCache("new", answer)
	_, hotRetained := a.dns.cache["0"]
	size := len(a.dns.cache)
	a.dns.mu.Unlock()
	if !hotRetained || size != 1024 {
		t.Fatal("hot response evicted / unbounded cache")
	}
}
func TestGameCatalogAndDirectPolicy(t *testing.T) {
	for _, id := range []string{"clash-of-clans", "rainbow-six", "call-of-duty", "cod-mobile", "battlefield", "marvel-rivals"} {
		p, ok := gamePreset(id)
		if !ok || len(p.Domains) == 0 || !strings.HasPrefix(p.Source, "https://") {
			t.Fatal("game profile missing", id)
		}
	}
	c := DNSClient{Presets: []string{"clash-of-clans", "cod-mobile"}, GameRoutes: map[string]string{"cod-mobile": "direct"}}
	proxy, direct, pkgs, _ := gameGroups(c)
	if len(proxy) == 0 || len(direct) == 0 || len(pkgs) != 1 || pkgs[0] != "com.supercell.clashofclans" {
		t.Fatal("direct game dragged into Android VPN")
	}
	c.GameRoutes["clash-of-clans"] = "direct"
	if len(smartGameDomains(c)) != 0 {
		t.Fatal("direct game still gets Smart rewrite")
	}
	v := DNSClient{Name: "test", Quota: 100, Presets: []string{"clash-of-clans"}, GameRoutes: map[string]string{"rainbow-six": "direct"}}
	if validateDNSClient(&v) == nil {
		t.Fatal("unselected game policy accepted")
	}
}
func gamePolicyFixture47(t *testing.T, mode, game, platform, dnsMode string) (map[string]any, error) {
	t.Helper()
	out := map[string]any{"type": "vless", "tag": "game-proxy", "server": "game.example", "server_port": 443, "uuid": newUUID()}
	config, e := gamingConfig(out, "https://dns.example/dns-query/"+strings.Repeat("a", 48), []string{"game.example"}, mode)
	if e != nil {
		return nil, e
	}
	a := automaticApp(t)
	r := httptest.NewRequest("GET", "/api/gaming/export?"+url.Values{"mode": {mode}, "platform": {platform}, "dnsMode": {dnsMode}}.Encode(), nil)
	if e = a.addGameExitChoices(config, r); e != nil {
		return nil, e
	}
	c := DNSClient{Presets: []string{game}}
	return config, applyGamePolicy(config, c, r)
}
func TestGameFullAppAndSplitDNSConfigurations(t *testing.T) {
	config, e := gamePolicyFixture47(t, "apps", "clash-of-clans", "android", "private")
	if e != nil {
		t.Fatal(e)
	}
	in := config["inbounds"].([]any)[0].(map[string]any)
	if in["include_package"].([]string)[0] != "com.supercell.clashofclans" || config["route"].(map[string]any)["final"] != "game-route" {
		t.Fatal("Android fixed IP traffic not routed")
	}
	if _, e := gamePolicyFixture47(t, "apps", "rainbow-six", "console", "private"); e == nil {
		t.Fatal("unsupported console per-process mode accepted")
	}
	config, e = gamePolicyFixture47(t, "games", "marvel-rivals", "windows", "split")
	if e != nil {
		t.Fatal(e)
	}
	if config["route"].(map[string]any)["final"] != "direct" || config["dns"].(map[string]any)["final"] != "local-game" {
		t.Fatal("unrelated traffic/DNS forced abroad")
	}
	config, e = gamePolicyFixture47(t, "full", "battlefield", "console", "private")
	if e != nil {
		t.Fatal(e)
	}
	if config["route"].(map[string]any)["final"] != "game-route" || config["dns"].(map[string]any)["final"] != "personal" {
		t.Fatal("full coverage route/DNS incomplete")
	}
	if _, e := gamePolicyFixture47(t, "full", "battlefield", "console", "split"); e == nil {
		t.Fatal("split DNS accepted for full tunnel")
	}
}
func TestOwnedGameExitImportsSecretsRestoreAndSelectors(t *testing.T) {
	a := automaticApp(t)
	secret := "game-private-password"
	uri := "trojan://" + secret + "@exit.example:443?type=ws&path=%2Fconnect%2Fx%2F&sni=exit.example&host=exit.example"
	b, _ := json.Marshal(map[string]any{"name": "Owned exit", "uri": uri})
	r := httptest.NewRequest("POST", "/api/gaming/exits", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.gamingAPI(w, r)
	if w.Code != 200 || bytes.Contains(w.Body.Bytes(), []byte(secret)) {
		t.Fatal("secret leaked or valid exit rejected", w.Code, w.Body.String())
	}
	v := a.gameExitSnapshot()[0]
	restored, e := NewApp(a.cfg, a.base)
	if e != nil || restored.gameExitSnapshot()[0].Outbound["password"] != secret {
		t.Fatal("private exit lost on restart", e)
	}
	config, _ := gamingConfig(map[string]any{"tag": "game-proxy", "type": "vless", "server": "origin.example", "server_port": 443, "uuid": newUUID()}, "https://dns.example/dns-query/"+strings.Repeat("a", 48), nil, "full")
	q := url.Values{"extras": {v.ID}, "exit": {"auto"}}
	if e := a.addGameExitChoices(config, httptest.NewRequest("GET", "/api/gaming/export?"+q.Encode(), nil)); e != nil {
		t.Fatal(e)
	}
	outs := config["outbounds"].([]any)
	auto := outs[len(outs)-2].(map[string]any)
	if auto["type"] != "urltest" || auto["interrupt_exist_connections"] != false {
		t.Fatal("game sessions forcibly interrupted")
	}
	for _, bad := range []string{"vless://" + newUUID() + "@127.0.0.1:443?security=tls", "trojan://secret@exit.example:443?allowInsecure=1", "vless://" + newUUID() + "@exit.example:443?type=xhttp"} {
		out, e := parseGameURI(bad)
		if e == nil {
			e = validateGameOutbound(out)
		}
		if e == nil {
			t.Fatal("unsafe/unsupported exit accepted", bad)
		}
	}
	private := cloneGameOutbound(v.Outbound)
	private["tls"].(map[string]any)["certificate_path"] = "/etc/passwd"
	if validateGameOutbound(private) == nil {
		t.Fatal("file-reading TLS option accepted")
	}
	unauth := httptest.NewRecorder()
	a.ServeHTTP(unauth, httptest.NewRequest("GET", "/api/gaming/exits/backup", nil))
	if unauth.Code != 401 {
		t.Fatal("private game backup unprotected")
	}
}
