package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

func clientGameDomains(c DNSClient) []string {
	out := append([]string{}, c.Domains...)
	for _, id := range c.Presets {
		for _, p := range gamePresets {
			if p.ID == id {
				out = append(out, p.Domains...)
			}
		}
	}
	return uniqueGameValues(out)
}
func gameDomainMatches(name string, domains []string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	for _, d := range domains {
		if name == d || strings.HasSuffix(name, "."+d) {
			return true
		}
	}
	return false
}
func (a *App) dnsPublic() any {
	d := a.dns
	d.mu.Lock()
	defer d.mu.Unlock()
	clients := append([]DNSClient{}, d.data.Clients...)
	stats := map[string]DNSResolverStat{}
	for k, v := range d.resolverStats {
		stats[k] = v
	}
	for i := range clients {
		clients[i].Key = ""
		if clients[i].Day != time.Now().UTC().Format("2006-01-02") {
			clients[i].DailyQueries = 0
		}
	}
	return map[string]any{"settings": d.data.Settings, "clients": clients, "presets": gamePresets, "status": d.status, "mode": a.cfg.Mode, "cacheEntries": len(d.cache), "cacheHits": d.hits, "coalesced": d.coalesced, "resolvers": stats, "failures": d.failures, "active": d.active, "note": "DoH در هاست وب اجرا می‌شود. DNS با پورت ۵۳ و رلهٔ HTTPS به VPS با پورت عمومی نیاز دارند. Smart DNS فقط دامنه‌های منتخب و TLS بدون ECH را عبور می‌دهد؛ UDP بازی به تونل نیاز دارد."}
}
func (a *App) dnsURL(c DNSClient, route string) (string, error) {
	origin := a.snapshot().Settings.PublicURL
	if route != "" {
		found := false
		for _, e := range a.edgeSnapshot().Routes {
			if e.ID != route {
				continue
			}
			found = true
			if !e.Enabled || !e.Verified {
				return "", fmt.Errorf("مسیر باید فعال و تأییدشده باشد")
			}
			if e.Resource != "" && !e.DNSRelay {
				return "", fmt.Errorf("ورودی قدیمی DNS ندارد؛ با قالب نسخهٔ ۴٫۶ یک ورودی تازه بساز")
			}
			origin = "https://" + net.JoinHostPort(e.Endpoint.Host, fmt.Sprint(e.Endpoint.Port))
		}
		if !found {
			return "", fmt.Errorf("مسیر پیدا نشد")
		}
	}
	u, e := url.Parse(origin)
	if e != nil || u.Host == "" || u.Scheme != "https" {
		return "", fmt.Errorf("برای DNS شخصی ابتدا دامنهٔ HTTPS عمومی پنل را تنظیم کن")
	}
	u.Path = "/dns-query/" + c.Key
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
func (a *App) dnsClientByID(id string) (DNSClient, bool) {
	a.dns.mu.Lock()
	defer a.dns.mu.Unlock()
	for _, c := range a.dns.data.Clients {
		if c.ID == id {
			return c, true
		}
	}
	return DNSClient{}, false
}
func (a *App) dnsAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/dns")
	if path == "" && r.Method == "GET" {
		jsonReply(w, 200, a.dnsPublic())
		return
	}
	if path == "/export" && r.Method == "GET" {
		c, ok := a.dnsClientByID(r.URL.Query().Get("id"))
		if !ok {
			apiError(w, 404, "کاربر DNS پیدا نشد")
			return
		}
		u, e := a.dnsURL(c, r.URL.Query().Get("route"))
		if e != nil {
			apiError(w, 409, e.Error())
			return
		}
		jsonReply(w, 200, map[string]any{"url": u, "name": c.Name, "classicIP": a.dns.dataSettings().SmartIP, "note": "این لینک کلید خصوصی DNS است. Android Private DNS سیستم به hostname سرویس DoT نیاز دارد؛ این URL را در کلاینت سازگار با DoH وارد کن."})
		return
	}
	if path == "/backup" && r.Method == "GET" {
		a.dns.mu.Lock()
		copyBytes, e := json.Marshal(a.dns.data)
		a.dns.mu.Unlock()
		if e != nil {
			apiError(w, 500, "بکاپ آماده نشد")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="aria-dns-private-backup.json"`)
		_, _ = w.Write(copyBytes)
		return
	}
	if r.Method != "POST" {
		apiError(w, 405, "عملیات معتبر نیست")
		return
	}
	if path == "/test" {
		var in struct {
			Name string `json:"name"`
		}
		if readBody(w, r, &in, 1024) != nil || !validHostname(in.Name) {
			apiError(w, 400, "دامنهٔ آزمون معتبر لازم است")
			return
		}
		results := a.benchmarkDNS(r.Context(), in.Name, a.dns.dataSettings())
		jsonReply(w, 200, map[string]any{"results": results, "note": "سه نمونه و میانهٔ زمان پاسخ از شبکهٔ همین سرور؛ این اندازه‌گیری پینگ UDP مسابقه یا رفع محدودیت حساب را تأیید نمی‌کند. در حالت adaptive، نتیجه در انتخاب resolver به کار می‌رود."})
		return
	}
	d := a.dns
	d.mu.Lock()
	data := d.data
	data.Clients = append([]DNSClient{}, data.Clients...)
	var err error
	switch path {
	case "/settings":
		var s DNSSettings
		if readBody(w, r, &s, 4096) != nil {
			err = fmt.Errorf("تنظیمات DNS معتبر نیست")
			break
		}
		err = validateDNSSettings(s, a.cfg)
		if err == nil {
			data.Settings = s
			d.cache = map[string]dnsCacheEntry{}
		}
	case "/clients":
		var in struct {
			ID         string            `json:"id"`
			Name       string            `json:"name"`
			Enabled    bool              `json:"enabled"`
			Days       int               `json:"days"`
			Quota      int64             `json:"quota"`
			CIDRs      []string          `json:"cidrs"`
			Presets    []string          `json:"presets"`
			Domains    []string          `json:"domains"`
			Smart      bool              `json:"smart"`
			GameRoutes map[string]string `json:"gameRoutes"`
		}
		if readBody(w, r, &in, 12288) != nil {
			err = fmt.Errorf("فرم DNS معتبر نیست")
			break
		}
		c := DNSClient{ID: in.ID, Name: strings.TrimSpace(in.Name), Enabled: in.Enabled, Quota: in.Quota, CIDRs: in.CIDRs, Presets: in.Presets, Domains: in.Domains, Smart: in.Smart, GameRoutes: in.GameRoutes}
		if in.Days < 0 || in.Days > 3650 {
			err = fmt.Errorf("اعتبار باید ۰ تا ۳۶۵۰ روز باشد")
			break
		}
		if in.Days > 0 {
			c.Expires = time.Now().Add(time.Duration(in.Days) * 24 * time.Hour).Unix()
		}
		err = validateDNSClient(&c)
		if err != nil {
			break
		}
		found := false
		for i, old := range data.Clients {
			if old.ID == c.ID {
				c.Key = old.Key
				c.Day = old.Day
				c.DailyQueries = old.DailyQueries
				c.Queries = old.Queries
				data.Clients[i] = c
				found = true
				break
			}
		}
		if !found {
			if c.ID != "" {
				err = fmt.Errorf("کاربر قبلی پیدا نشد")
				break
			}
			if len(data.Clients) >= 200 {
				err = fmt.Errorf("حداکثر ۲۰۰ کاربر DNS")
				break
			}
			c.ID = "dns-" + randomToken(8)
			c.Key = randomToken(24)
			data.Clients = append(data.Clients, c)
		}
		err = validateDNSOwnership(data.Clients)
	case "/backup":
		var imported DNSData
		if readBody(w, r, &imported, 1<<20) != nil || len(imported.Clients) > 200 {
			err = fmt.Errorf("بکاپ DNS معتبر نیست")
			break
		}
		for _, c := range imported.Clients {
			if !regexp.MustCompile(`^dns-[a-f0-9]{16}$`).MatchString(c.ID) || !regexp.MustCompile(`^[a-f0-9]{48}$`).MatchString(c.Key) {
				err = fmt.Errorf("هویت بکاپ معتبر نیست")
				break
			}
			if e := validateDNSClient(&c); e != nil {
				err = e
				break
			}
			found := false
			for _, existing := range data.Clients {
				if existing.ID == c.ID {
					found = true
					break
				}
				if existing.Key == c.Key {
					err = fmt.Errorf("کلید تکراری در بکاپ")
					break
				}
			}
			if err != nil {
				break
			}
			if !found {
				data.Clients = append(data.Clients, c)
			}
		}
		if len(data.Clients) > 200 {
			err = fmt.Errorf("سقف حساب‌های DNS پر است")
		}
		if err == nil {
			err = validateDNSOwnership(data.Clients)
		}
	case "/cache/clear":
		d.cache = map[string]dnsCacheEntry{}
	default:
		parts := strings.Split(strings.TrimPrefix(path, "/clients/"), "/")
		if len(parts) != 2 {
			err = fmt.Errorf("عملیات پیدا نشد")
			break
		}
		found := false
		for i := range data.Clients {
			if data.Clients[i].ID != parts[0] {
				continue
			}
			found = true
			switch parts[1] {
			case "rotate":
				data.Clients[i].Key = randomToken(24)
			case "toggle":
				data.Clients[i].Enabled = !data.Clients[i].Enabled
			case "delete":
				data.Clients = append(data.Clients[:i], data.Clients[i+1:]...)
			default:
				err = fmt.Errorf("عملیات پیدا نشد")
			}
			break
		}
		if !found {
			err = fmt.Errorf("کاربر DNS پیدا نشد")
		}
	}
	if err == nil {
		err = atomicJSON(a.cfg.DataDir, "aria-dns.json", data)
		if err == nil {
			d.data = data
			d.dirty = false
		}
	}
	d.mu.Unlock()
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	select {
	case d.wake <- struct{}{}:
	default:
	}
	if path == "/settings" {
		select {
		case d.resolverWake <- struct{}{}:
		default:
		}
	}
	jsonReply(w, 200, a.dnsPublic())
}
func (d *DNSRuntime) dataSettings() DNSSettings {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.data.Settings
}
func validateDNSClient(c *DNSClient) error {
	if !safeLabel(c.Name, 64) || c.Quota < 100 || c.Quota > 1000000 || len(c.CIDRs) > 16 || len(c.Domains) > 64 || len(c.Presets) > len(gamePresets) {
		return fmt.Errorf("نام، سهمیهٔ روزانه ۱۰۰ تا ۱۰۰۰۰۰۰ و حداکثر ۱۶ IP و ۶۴ دامنه لازم است")
	}
	for i, s := range c.CIDRs {
		if ip := net.ParseIP(s); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			s = fmt.Sprint(ip) + fmt.Sprintf("/%d", bits)
		}
		_, n, e := net.ParseCIDR(s)
		if e != nil || !publicNetworkIP(n.IP) {
			return fmt.Errorf("IP عمومی مشتری یا CIDR معتبر لازم است")
		}
		ones, bits := n.Mask.Size()
		if (bits == 32 && ones < 24) || (bits == 128 && ones < 56) {
			return fmt.Errorf("محدودهٔ بزرگ مجاز نیست؛ IP مشتری را با /32 یا /128 وارد کن")
		}
		c.CIDRs[i] = n.String()
	}
	seen := map[string]bool{}
	for _, id := range c.Presets {
		valid := false
		for _, p := range gamePresets {
			if p.ID == id {
				valid = true
			}
		}
		if !valid || seen[id] {
			return fmt.Errorf("بستهٔ بازی معتبر نیست")
		}
		seen[id] = true
	}
	for i, s := range c.Domains {
		s = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(s), "*."), "."))
		if !validHostname(s) || net.ParseIP(s) != nil || !strings.Contains(s, ".") {
			return fmt.Errorf("دامنهٔ اختصاصی معتبر لازم است")
		}
		c.Domains[i] = s
	}
	for id, policy := range c.GameRoutes {
		_, ok := gamePreset(id)
		if !ok || (policy != "proxy" && policy != "direct") {
			return fmt.Errorf("مسیر بازی معتبر نیست")
		}
		found := false
		for _, p := range c.Presets {
			if p == id {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("ابتدا بازی را انتخاب کن؛ سپس مسیر آن را تعیین کن")
		}
	}
	if c.Smart && (len(c.CIDRs) == 0 || len(smartGameDomains(*c)) == 0) {
		return fmt.Errorf("برای Smart DNS باید IP مشتری و دامنهٔ بازی ثبت شود")
	}
	return nil
}

// Direct port DNS has no token, so every source network has one owner.
func validateDNSOwnership(clients []DNSClient) error {
	for i, c := range clients {
		for _, other := range clients[i+1:] {
			for _, s := range c.CIDRs {
				_, n, e := net.ParseCIDR(s)
				if e != nil {
					return fmt.Errorf("IP معتبر نیست")
				}
				for _, t := range other.CIDRs {
					_, m, e := net.ParseCIDR(t)
					if e != nil {
						return fmt.Errorf("IP معتبر نیست")
					}
					if n.Contains(m.IP) || m.Contains(n.IP) {
						return fmt.Errorf("این IP یا محدوده قبلاً برای کاربر دیگری ثبت شده است")
					}
				}
			}
		}
	}
	return nil
}

// Retain aggregate quotas across restart; flush every 30 s, not on each packet.
func (a *App) flushDNS() {
	a.dns.mu.Lock()
	defer a.dns.mu.Unlock()
	if a.dns.dirty {
		if atomicJSON(a.cfg.DataDir, "aria-dns.json", a.dns.data) == nil {
			a.dns.dirty = false
		}
	}
}
