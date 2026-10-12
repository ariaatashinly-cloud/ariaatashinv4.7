package main

import (
	"fmt"
	"net/http"
	"net/url"
	"time"
)

func (a *App) gamingExport(w http.ResponseWriter, r *http.Request) {
	u, ok := a.findUser(r.URL.Query().Get("email"))
	if !ok {
		apiError(w, 404, "کاربر Xray پیدا نشد")
		return
	}
	if !u.Enabled || (u.Expiry > 0 && u.Expiry < time.Now().UnixMilli()) || (u.Quota > 0 && u.Up+u.Down >= u.Quota) {
		apiError(w, 409, "حساب اتصال باید فعال و دارای اعتبار باشد")
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode != "full" && mode != "games" && mode != "apps" {
		apiError(w, 400, "حالت دامنه، برنامه یا تونل کامل لازم است")
		return
	}
	c, ok := a.dnsClientByID(r.URL.Query().Get("dns"))
	if !ok || !dnsClientActive(c) || !a.dns.dataSettings().Enabled {
		apiError(w, 409, "یک حساب DNS شخصی فعال انتخاب کن")
		return
	}
	if c.Smart {
		apiError(w, 409, "برای تونل، حساب DNS را با Smart DNS خاموش بساز؛ رلهٔ Smart و تونل را همزمان روی یک دامنه فعال نکن")
		return
	}
	if game := r.URL.Query().Get("game"); game != "" {
		if _, ok := gamePreset(game); !ok {
			apiError(w, 400, "بازی پیدا نشد")
			return
		}
		c.Presets = []string{game}
	}
	domains := clientGameDomains(c)
	if mode == "games" && len(domains) == 0 {
		apiError(w, 409, "ابتدا دامنه یا بستهٔ بازی را برای DNS انتخاب کن")
		return
	}
	var p Profile
	found := false
	for _, link := range u.Links {
		for _, profile := range a.snapshot().Profiles {
			if profile.Key == link.Profile && profile.Key == r.URL.Query().Get("profile") {
				p = profile
				found = true
			}
		}
	}
	if !found {
		apiError(w, 404, "این پروفایل برای همین کاربر نیست")
		return
	}
	if edgeID := r.URL.Query().Get("route"); edgeID != "" {
		found = false
		for _, e := range a.edgeSnapshot().Routes {
			if e.ID == edgeID && e.Enabled && e.Verified && edgeSupports(e, p) {
				p = profileForEdge(p, e)
				found = true
				break
			}
		}
		if !found {
			apiError(w, 409, "مسیر فعال، تأییدشده و سازگار با پروفایل لازم است")
			return
		}
	}
	if p.Tuning.MuxConcurrency != 0 || p.Tuning.MaxEarlyData != 0 {
		apiError(w, 409, "برای این خروجی، WS با Mux و EarlyData صفر بساز")
		return
	}
	settings := a.snapshot().Settings
	out := singBoxOutboundWithEndpoint(p, u.Raw, settings.PublicURL, settings.Connection)
	if out == nil {
		apiError(w, 409, "این انتقال در sing-box پشتیبانی نمی‌شود؛ VLESS WS یا اتصال مستقیم سازگار انتخاب کن")
		return
	}
	out["tag"] = "game-proxy"
	out["domain_resolver"] = "bootstrap"
	if p.Protocol == "vless" || p.Protocol == "vmess" {
		out["packet_encoding"] = "xudp"
	}
	doh, e := a.dnsURL(c, r.URL.Query().Get("dnsRoute"))
	if e != nil {
		apiError(w, 409, e.Error())
		return
	}
	config, e := gamingConfig(out, doh, domains, mode)
	if e != nil {
		apiError(w, 400, e.Error())
		return
	}
	if err := a.addGameExitChoices(config, r); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if err := applyGamePolicy(config, c, r); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if r.URL.Query().Get("dnsMode") == "exit" {
		doh = "https://cloudflare-dns.com/dns-query"
	}
	jsonReply(w, 200, map[string]any{"config": config, "selectedGame": r.URL.Query().Get("game"), "location": a.exitLocation(), "mode": mode, "domains": domains, "dnsURL": doh, "note": "خروجی sing-box 1.14 با TUN است؛ در کلاینت سازگار اجازهٔ VPN/TUN بده. UDP در VLESS/VMess با XUDP روی تونل عبور می‌کند. حالت برنامه برای IP ثابت هم قواعد برنامه را به کار می‌گیرد؛ کنسول/iOS یا بازی بدون نام برنامه به تونل کامل نیاز دارد. بازی مستقیم از مسیر معمولی می‌رود. URLTest کیفیت HTTP را می‌سنجد، نه پذیرش حساب یا UDP مسابقه. تأخیر و پذیرش منطقه تابع مسیر و بازی است."})
}
func gamingConfig(out map[string]any, doh string, domains []string, mode string) (map[string]any, error) {
	u, e := url.Parse(doh)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, fmt.Errorf("HTTPS DNS required")
	}
	port := 443
	if u.Port() != "" {
		port, e = parsePort(u.Port())
		if e != nil {
			return nil, e
		}
	}
	dnsServer := map[string]any{"type": "https", "tag": "personal", "server": u.Hostname(), "server_port": port, "path": u.EscapedPath(), "tls": map[string]any{"enabled": true, "server_name": u.Hostname()}, "domain_resolver": "bootstrap", "detour": "game-proxy"}
	rules := []any{map[string]any{"action": "sniff"}, map[string]any{"protocol": "dns", "action": "hijack-dns"}, map[string]any{"ip_is_private": true, "action": "route", "outbound": "direct"}}
	final := "game-proxy"
	if mode == "games" {
		rules = append(rules, map[string]any{"domain_suffix": domains, "action": "route", "outbound": "game-proxy"})
		final = "direct"
	}
	return map[string]any{
		"log":       map[string]any{"level": "warn", "timestamp": true},
		"dns":       map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "bootstrap"}, dnsServer}, "final": "personal", "strategy": "ipv4_only", "independent_cache": true},
		"inbounds":  []any{map[string]any{"type": "tun", "tag": "game-tun", "interface_name": "aria-game", "address": []string{"172.19.0.1/30"}, "mtu": 1400, "auto_route": true, "strict_route": true, "stack": "system"}},
		"outbounds": []any{out, map[string]any{"type": "direct", "tag": "direct", "domain_resolver": "bootstrap"}},
		"route":     map[string]any{"auto_detect_interface": true, "default_domain_resolver": "personal", "rules": rules, "final": final},
	}, nil
}
