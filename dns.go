package main

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// Public endpoints are fixed presets: credentials cannot turn the resolver into
// an HTTP proxy. Client query names are never stored in the persistent state.
var dohUpstreams = map[string]string{
	"cloudflare": "https://cloudflare-dns.com/dns-query",
	"google":     "https://dns.google/dns-query",
	"quad9":      "https://dns.quad9.net/dns-query",
}

type DNSSettings struct {
	ResolverMode   string   `json:"resolverMode"`
	HedgeMS        int      `json:"hedgeMS"`
	Enabled        bool     `json:"enabled"`
	Cache          bool     `json:"cache"`
	Upstreams      []string `json:"upstreams"`
	TimeoutMS      int      `json:"timeoutMS"`
	ClassicEnabled bool     `json:"classicEnabled"`
	DNSListen      string   `json:"dnsListen"`
	SmartEnabled   bool     `json:"smartEnabled"`
	TLSListen      string   `json:"tlsListen"`
	SmartIP        string   `json:"smartIP"`
	PanelHost      string   `json:"panelHost"`
}
type DNSClient struct {
	GameRoutes   map[string]string `json:"gameRoutes,omitempty"`
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Key          string            `json:"key,omitempty"`
	Enabled      bool              `json:"enabled"`
	Expires      int64             `json:"expires"`
	Quota        int64             `json:"quota"`
	CIDRs        []string          `json:"cidrs"`
	Presets      []string          `json:"presets"`
	Domains      []string          `json:"domains"`
	Smart        bool              `json:"smart"`
	Day          string            `json:"day"`
	DailyQueries int64             `json:"dailyQueries"`
	Queries      int64             `json:"queries"`
}
type DNSData struct {
	Settings DNSSettings `json:"settings"`
	Clients  []DNSClient `json:"clients"`
}
type dnsCacheEntry struct {
	Message  *dns.Msg
	Added    time.Time
	Expires  time.Time
	Accessed time.Time
}
type DNSRuntime struct {
	mu            sync.Mutex
	data          DNSData
	cache         map[string]dnsCacheEntry
	flights       map[string]*dnsFlight
	resolverStats map[string]DNSResolverStat
	fetchSlots    chan struct{}
	coalesced     uint64
	busy          chan struct{}
	wake          chan struct{}
	resolverWake  chan struct{}
	http          *http.Client // tests may inject a transport; production uses pinned HTTPS presets
	dirty         bool
	hits          uint64
	failures      uint64
	status        string
	active        DNSSettings
	// These fixture hooks are never configurable through the API or disk.
	resolveIP func(context.Context, string) ([]net.IPAddr, error)
	dialTLS   func(context.Context, string) (net.Conn, error)
}

func (a *App) loadDNS() error {
	d := DNSData{Settings: DNSSettings{ResolverMode: "adaptive", HedgeMS: 180, Cache: true, Upstreams: []string{"cloudflare", "google", "quad9"}, TimeoutMS: 2500, DNSListen: "0.0.0.0:53", TLSListen: "0.0.0.0:443"}, Clients: []DNSClient{}}
	b, err := os.ReadFile(filepath.Join(a.cfg.DataDir, "aria-dns.json"))
	if err == nil {
		if json.Unmarshal(b, &d) != nil {
			return fmt.Errorf("DNS state invalid")
		}
		if err := validateDNSSettings(d.Settings, a.cfg); err != nil {
			return fmt.Errorf("saved DNS settings: %w", err)
		}
		for _, c := range d.Clients {
			if !regexp.MustCompile(`^[a-f0-9]{48}$`).MatchString(c.Key) {
				return fmt.Errorf("saved DNS key invalid")
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	a.dns = &DNSRuntime{data: d, cache: map[string]dnsCacheEntry{}, busy: make(chan struct{}, 64), wake: make(chan struct{}, 1), http: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second, MaxIdleConns: 12, MaxIdleConnsPerHost: 4, IdleConnTimeout: 90 * time.Second}, CheckRedirect: noRedirect, Timeout: 9 * time.Second}, status: "DoH آمادهٔ فعال‌سازی؛ سرویس مستقیم خاموش"}
	a.dns.flights = map[string]*dnsFlight{}
	a.dns.resolverStats = map[string]DNSResolverStat{}
	a.dns.fetchSlots = make(chan struct{}, 32)
	a.dns.resolverWake = make(chan struct{}, 1)
	return nil
}
func validateDNSSettings(s DNSSettings, cfg Config) error {
	if s.ResolverMode != "" && s.ResolverMode != "adaptive" && s.ResolverMode != "ordered" {
		return fmt.Errorf("حالت resolver معتبر نیست")
	}
	if s.HedgeMS != 0 && (s.HedgeMS < 50 || s.HedgeMS > 1500) {
		return fmt.Errorf("مهلت مسیر جایگزین باید ۵۰ تا ۱۵۰۰ میلی‌ثانیه باشد")
	}
	if len(s.Upstreams) < 1 || len(s.Upstreams) > 3 || s.TimeoutMS < 500 || s.TimeoutMS > 8000 {
		return fmt.Errorf("۱ تا ۳ resolver و مهلت ۵۰۰ تا ۸۰۰۰ میلی‌ثانیه لازم است")
	}
	seen := map[string]bool{}
	for _, p := range s.Upstreams {
		if dohUpstreams[p] == "" || seen[p] {
			return fmt.Errorf("resolver تکراری یا نامعتبر")
		}
		seen[p] = true
	}
	if (s.ClassicEnabled || s.SmartEnabled) && cfg.Mode != "vps" {
		return fmt.Errorf("DNS با پورت مستقیم و رلهٔ TLS به حالت VPS نیاز دارند؛ در هاست وب DoH را فعال کن")
	}
	if (s.ClassicEnabled || s.SmartEnabled) && !s.Enabled {
		return fmt.Errorf("ابتدا DNS شخصی را فعال کن")
	}
	for _, addr := range []string{s.DNSListen, s.TLSListen} {
		host, port, e := net.SplitHostPort(addr)
		if e != nil || net.ParseIP(host) == nil || port == "" {
			return fmt.Errorf("آدرس شنود باید IP:port باشد")
		}
		p, e := parsePort(port)
		if e != nil || p == cfg.Port || p == panelPort || p == 7443 || p == 24099 {
			return fmt.Errorf("پورت شنود معتبر و جدا از پنل لازم است")
		}
	}
	if s.SmartEnabled {
		ip := net.ParseIP(s.SmartIP)
		if ip == nil || ip.To4() == nil || !publicNetworkIP(ip) {
			return fmt.Errorf("IPv4 عمومی همین VPS برای Smart DNS لازم است")
		}
		if !validHostname(s.PanelHost) || net.ParseIP(s.PanelHost) != nil {
			return fmt.Errorf("دامنهٔ پنل برای عبور TLS به Caddy لازم است")
		}
		if s.DNSListen == s.TLSListen {
			return fmt.Errorf("پورت DNS و رله باید جدا باشند")
		}
	}
	return nil
}
func parsePort(s string) (int, error) {
	var n int
	if _, e := fmt.Sscanf(s, "%d", &n); e != nil || n < 1 || n > 65535 || fmt.Sprint(n) != s {
		return 0, fmt.Errorf("invalid port")
	}
	return n, nil
}

// Also reject special-use and documentation networks, not just RFC1918.
func publicNetworkIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || blockedPanelIP(ip) {
		return false
	}
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2001:2::/48", "2001:10::/28", "2002::/16", "3fff::/20", "5f00::/16", "100::/64", "100:0:0:1::/64", "fec0::/10", "64:ff9b::/96", "64:ff9b:1::/48"} {
		_, n, _ := net.ParseCIDR(s)
		if n.Contains(ip) {
			return false
		}
	}
	return true
}
func dnsClientActive(c DNSClient) bool {
	return c.Enabled && (c.Expires == 0 || c.Expires > time.Now().Unix())
}
func clientMatchesIP(c DNSClient, ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, s := range c.CIDRs {
		_, n, e := net.ParseCIDR(s)
		if e == nil && n.Contains(ip) {
			return true
		}
	}
	return false
}
func (a *App) reserveDNS(key string, ip net.IP) (DNSClient, DNSSettings, int) {
	d := a.dns
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.data.Settings
	if !s.Enabled {
		return DNSClient{}, s, 503
	}
	for i := range d.data.Clients {
		c := &d.data.Clients[i]
		match := key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(c.Key)) == 1
		if key == "" {
			match = clientMatchesIP(*c, ip)
		}
		if !match {
			continue
		}
		if !dnsClientActive(*c) {
			return DNSClient{}, s, 403
		}
		day := time.Now().UTC().Format("2006-01-02")
		if c.Day != day {
			c.Day = day
			c.DailyQueries = 0
		}
		if c.DailyQueries >= c.Quota {
			return DNSClient{}, s, 429
		}
		c.DailyQueries++
		c.Queries++
		d.dirty = true
		return *c, s, 200
	}
	return DNSClient{}, s, 404
}
func sanitizeDNSQuery(raw []byte) (*dns.Msg, error) {
	if len(raw) < 12 || len(raw) > 4096 {
		return nil, fmt.Errorf("DNS message size")
	}
	q := new(dns.Msg)
	if q.Unpack(raw) != nil || q.Response || q.Opcode != dns.OpcodeQuery || len(q.Question) != 1 || len(q.Answer) != 0 || len(q.Ns) != 0 {
		return nil, fmt.Errorf("one DNS question required")
	}
	v := q.Question[0]
	if v.Qclass != dns.ClassINET || v.Qtype == dns.TypeAXFR || v.Qtype == dns.TypeIXFR || v.Qtype == dns.TypeANY {
		return nil, fmt.Errorf("unsupported DNS question")
	}
	if _, ok := dns.IsDomainName(v.Name); len(v.Name) > 254 || !ok {
		return nil, fmt.Errorf("invalid DNS name")
	}
	// Build a fresh query; do not transmit ECS, padding, cookies or extra records.
	clean := new(dns.Msg)
	clean.SetQuestion(v.Name, v.Qtype)
	clean.Id = q.Id
	clean.CheckingDisabled = q.CheckingDisabled
	if opt := q.IsEdns0(); opt != nil {
		clean.SetEdns0(1232, opt.Do())
	}
	return clean, nil
}
func (a *App) serveDoH(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	key := strings.TrimPrefix(r.URL.Path, "/dns-query/")
	if !regexp.MustCompile(`^[a-f0-9]{48}$`).MatchString(key) {
		dohError(w, r, "not found", 404)
		return
	}
	// Reject invalid keys before accepting a body; do not charge quota until a
	// valid DNS question has been parsed.
	d := a.dns
	d.mu.Lock()
	status := 404
	if !d.data.Settings.Enabled {
		status = 503
	} else {
		for _, c := range d.data.Clients {
			if subtle.ConstantTimeCompare([]byte(key), []byte(c.Key)) == 1 {
				status = 200
				if !dnsClientActive(c) {
					status = 403
				}
				break
			}
		}
	}
	d.mu.Unlock()
	if status != 200 {
		dohError(w, r, "DNS access unavailable", status)
		return
	}
	var raw []byte
	var e error
	switch r.Method {
	case "GET":
		encoded := r.URL.Query().Get("dns")
		if len(encoded) > 5500 {
			dohError(w, r, "DNS query too large", 413)
			return
		}
		raw, e = base64.RawURLEncoding.DecodeString(encoded)
	case "POST":
		if r.ContentLength > 4096 {
			dohError(w, r, "DNS query too large", 413)
			return
		}
		ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if ct != "application/dns-message" {
			dohError(w, r, "application/dns-message required", 415)
			return
		}
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(5 * time.Second))
		raw, e = io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		_ = controller.SetReadDeadline(time.Time{})
	default:
		w.Header().Set("Allow", "GET, POST")
		dohError(w, r, "method not allowed", 405)
		return
	}
	if e != nil {
		dohError(w, r, "invalid DNS request", 400)
		return
	}
	q, e := sanitizeDNSQuery(raw)
	if e != nil {
		dohError(w, r, e.Error(), 400)
		return
	}
	c, s, status := a.reserveDNS(key, nil)
	if status != 200 {
		dohError(w, r, "DNS access unavailable", status)
		return
	}
	select {
	case a.dns.busy <- struct{}{}:
		defer func() { <-a.dns.busy }()
	default:
		dohError(w, r, "DNS busy", 429)
		return
	}
	answer, e := a.answerDNS(r.Context(), q, c, s)
	if e != nil {
		a.dns.mu.Lock()
		a.dns.failures++
		a.dns.mu.Unlock()
		dohError(w, r, "DNS upstream unavailable", 502)
		return
	}
	b, e := answer.Pack()
	if e != nil {
		dohError(w, r, "DNS response invalid", 502)
		return
	}
	w.Header().Set("Content-Type", "application/dns-message")
	w.WriteHeader(200)
	_, _ = w.Write(b)
}
func (a *App) answerDNS(ctx context.Context, q *dns.Msg, c DNSClient, s DNSSettings) (*dns.Msg, error) {
	if s.SmartEnabled && c.Smart && gameDomainMatches(q.Question[0].Name, smartGameDomains(c)) {
		a.dns.mu.Lock()
		ready := a.dns.active.SmartEnabled
		a.dns.mu.Unlock()
		if !ready {
			return nil, fmt.Errorf("Smart relay not listening")
		}
		if len(c.CIDRs) == 0 {
			return nil, fmt.Errorf("Smart DNS needs registered source IP")
		}
		answer := new(dns.Msg)
		answer.SetReply(q)
		answer.RecursionAvailable = true
		v := q.Question[0]
		switch v.Qtype {
		case dns.TypeA:
			answer.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: v.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 30}, A: net.ParseIP(s.SmartIP).To4()}}
			return answer, nil
		case dns.TypeAAAA, dns.TypeHTTPS, dns.TypeSVCB:
			return answer, nil
		}
	}
	return a.resolveDoH(ctx, q, s)
}
func dnsCacheKey(q *dns.Msg) string {
	b := q.Copy()
	b.Id = 0
	b.Question[0].Name = strings.ToLower(b.Question[0].Name)
	wire, _ := b.Pack()
	return string(wire)
}

func dohError(w http.ResponseWriter, r *http.Request, message string, status int) {
	// Do not wait for an unauthorized or oversized HTTP/1 upload to finish.
	if r.Method == http.MethodPost && r.ProtoMajor == 1 {
		w.Header().Set("Connection", "close")
	}
	http.Error(w, message, status)
}
