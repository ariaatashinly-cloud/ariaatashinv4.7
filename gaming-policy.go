package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

func gameExtraNames(raw string, packages bool) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 16 {
		return nil, fmt.Errorf("حداکثر ۱۶ نام برنامه")
	}
	pattern := `^[\p{L}\p{N} ._()\-]{1,128}$`
	if packages {
		pattern = `^[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)+$`
	}
	for i, v := range parts {
		v = strings.TrimSpace(v)
		if len(v) > 128 || !regexp.MustCompile(pattern).MatchString(v) {
			return nil, fmt.Errorf("نام برنامه یا package معتبر لازم است")
		}
		parts[i] = v
	}
	return uniqueGameValues(parts), nil
}
func applyGamePolicy(config map[string]any, c DNSClient, r *http.Request) error {
	mode := r.URL.Query().Get("mode")
	platform := r.URL.Query().Get("platform")
	proxy, direct, packages, processes := gameGroups(c)
	extra, e := gameExtraNames(r.URL.Query().Get("processes"), false)
	if e != nil {
		return e
	}
	processes = uniqueGameValues(append(processes, extra...))
	extra, e = gameExtraNames(r.URL.Query().Get("packages"), true)
	if e != nil {
		return e
	}
	packages = uniqueGameValues(append(packages, extra...))
	if mode == "apps" {
		if platform != "android" && platform != "windows" && platform != "linux" && platform != "macos" {
			return fmt.Errorf("فقط برنامه برای Android یا سیستم دارای نام فرایند است؛ برای کنسول/iOS تونل کامل را انتخاب کن")
		}
		if platform == "android" && len(packages) == 0 {
			return fmt.Errorf("package واقعی بازی لازم است؛ یا تونل کامل را انتخاب کن")
		}
		if platform != "android" && len(processes) == 0 {
			return fmt.Errorf("نام exe/فرایند بازی را وارد کن؛ برای عبور بدون نام برنامه، تونل کامل موجود است")
		}
	}
	dnsMode := r.URL.Query().Get("dnsMode")
	if dnsMode == "" {
		dnsMode = "private"
	}
	if dnsMode != "private" && dnsMode != "split" && dnsMode != "exit" {
		return fmt.Errorf("حالت DNS معتبر نیست")
	}
	if mode == "full" && dnsMode == "split" {
		return fmt.Errorf("در تونل کامل DNS خصوصی را انتخاب کن")
	}
	inbound := config["inbounds"].([]any)[0].(map[string]any)
	route := config["route"].(map[string]any)
	rules := []any{map[string]any{"action": "sniff"}, map[string]any{"protocol": "dns", "action": "hijack-dns"}, map[string]any{"ip_is_private": true, "action": "route", "outbound": "direct"}}
	dnsRules := []any{}
	if mode == "apps" && platform == "android" {
		inbound["include_package"] = packages
		route["final"] = "game-route"
	} else {
		if mode == "apps" {
			rules = append(rules, map[string]any{"process_name": processes, "action": "route", "outbound": "game-route"})
			dnsRules = append(dnsRules, map[string]any{"process_name": processes, "action": "route", "server": "personal"})
		}
		if mode == "full" {
			route["final"] = "game-route"
		} else {
			route["final"] = "direct"
		}
	}
	if mode != "full" {
		if len(direct) > 0 {
			rules = append(rules, map[string]any{"domain_suffix": direct, "action": "route", "outbound": "direct"})
		}
		if len(proxy) > 0 {
			rules = append(rules, map[string]any{"domain_suffix": proxy, "action": "route", "outbound": "game-route"})
			dnsRules = append(dnsRules, map[string]any{"domain_suffix": proxy, "action": "route", "server": "personal"})
		}
	}
	if len(direct) > 0 && mode != "full" {
		dnsRules = append(dnsRules, map[string]any{"domain_suffix": direct, "action": "route", "server": "local-game"})
	}
	route["rules"] = rules
	dnsConfig := config["dns"].(map[string]any)
	if dnsMode == "exit" {
		// A second exit can resolve names even while the panel's origin is down.
		server := dnsConfig["servers"].([]any)[1].(map[string]any)
		server["server"] = "cloudflare-dns.com"
		server["server_port"] = 443
		server["path"] = "/dns-query"
		server["tls"] = map[string]any{"enabled": true, "server_name": "cloudflare-dns.com"}
	}
	dnsConfig["servers"] = append(dnsConfig["servers"].([]any), map[string]string{"type": "local", "tag": "local-game"})
	dnsConfig["rules"] = dnsRules
	if dnsMode == "split" && !(mode == "apps" && platform == "android") {
		dnsConfig["final"] = "local-game"
	} else {
		dnsConfig["final"] = "personal"
	}
	if mode == "full" {
		dnsConfig["rules"] = []any{}
	}
	return nil
}
func (a *App) addGameExitChoices(config map[string]any, r *http.Request) error {
	selected := r.URL.Query().Get("exit")
	if selected == "" {
		selected = "origin"
	}
	ids := []string{}
	if raw := r.URL.Query().Get("extras"); raw != "" {
		ids = strings.Split(raw, ",")
	}
	if selected != "origin" && selected != "auto" {
		has := false
		for _, id := range ids {
			if id == selected {
				has = true
			}
		}
		if !has {
			ids = append(ids, selected)
		}
	}
	if len(ids) > 8 {
		return fmt.Errorf("حداکثر ۸ خروجی مستقل")
	}
	outs := config["outbounds"].([]any)
	tags := []string{"game-proxy"}
	chosen := "game-proxy"
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return fmt.Errorf("خروجی تکراری است")
		}
		seen[id] = true
		found := false
		for _, v := range a.gameExitSnapshot() {
			if v.ID != id {
				continue
			}
			if !v.Enabled {
				return fmt.Errorf("خروجی انتخاب‌شده خاموش است")
			}
			found = true
			out := cloneGameOutbound(v.Outbound)
			tag := "exit-" + v.ID + " · " + v.Name
			out["tag"] = tag
			out["domain_resolver"] = "bootstrap"
			outs = append(outs, out)
			tags = append(tags, tag)
			if selected == id {
				chosen = tag
			}
			break
		}
		if !found {
			return fmt.Errorf("خروجی مستقل پیدا نشد")
		}
	}
	if selected == "auto" {
		if len(tags) < 2 {
			return fmt.Errorf("آزمون خودکار حداقل دو خروجی لازم دارد")
		}
		outs = append(outs, map[string]any{"type": "urltest", "tag": "game-auto", "outbounds": tags, "url": "https://cp.cloudflare.com/generate_204", "interval": "5m", "tolerance": 80, "interrupt_exist_connections": false})
		tags = append(tags, "game-auto")
		chosen = "game-auto"
	}
	outs = append(outs, map[string]any{"type": "selector", "tag": "game-route", "outbounds": tags, "default": chosen, "interrupt_exist_connections": false})
	config["outbounds"] = outs
	config["dns"].(map[string]any)["servers"].([]any)[1].(map[string]any)["detour"] = "game-route"
	return nil
}
