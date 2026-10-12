//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNative47GameProfilesAppsDNSAndExitSelectors(t *testing.T) {
	bin := os.Getenv("ARIA_TEST_SING_BOX")
	if bin == "" {
		t.Skip("set ARIA_TEST_SING_BOX")
	}
	a := automaticApp(t)
	t.Setenv("ARIA_SING_BOX_BIN", bin)
	for _, uri := range []string{"vless://" + newUUID() + "@exit.example:443?security=tls&type=ws&path=%2Fconnect%2Fa%2F&sni=exit.example&host=exit.example", "trojan://private-pass@exit.example:443?security=tls", "hy2://private-pass@exit.example:443?sni=exit.example", "tuic://" + newUUID() + ":private-pass@exit.example:443?sni=exit.example", "anytls://private-pass@exit.example:443?sni=exit.example"} {
		out, e := parseGameURI(uri)
		if e != nil || validateGameOutbound(out) != nil {
			t.Fatal("import rejected", e)
		}
		valid, e := a.checkGameOutbound(context.Background(), out)
		if e != nil || !valid {
			t.Fatal("native exit checker failed", e)
		}
	}
	a.gameExits = []GameExit{{ID: "gx-" + strings.Repeat("a", 16), Name: "خروجی دوم", Enabled: true, Outbound: map[string]any{"type": "trojan", "server": "exit.example", "server_port": 443, "password": "private-pass", "tls": map[string]any{"enabled": true, "server_name": "exit.example"}}}}
	for _, p := range gamePresets {
		cases := []struct{ mode, platform, dnsMode string }{{"full", "console", "private"}, {"full", "console", "exit"}, {"games", "windows", "split"}}
		if len(p.Packages) > 0 {
			cases = append(cases, struct{ mode, platform, dnsMode string }{"apps", "android", "private"})
		}
		if len(p.Processes) > 0 {
			cases = append(cases, struct{ mode, platform, dnsMode string }{"apps", "windows", "split"})
		}
		for _, tc := range cases {
			t.Run(p.ID+"-"+tc.mode+"-"+tc.platform+"-"+tc.dnsMode, func(t *testing.T) {
				out := map[string]any{"type": "vless", "tag": "game-proxy", "server": "origin.example", "server_port": 443, "uuid": newUUID(), "packet_encoding": "xudp", "domain_resolver": "bootstrap", "tls": map[string]any{"enabled": true, "server_name": "origin.example"}}
				config, e := gamingConfig(out, "https://dns.example/dns-query/"+strings.Repeat("a", 48), p.Domains, tc.mode)
				if e != nil {
					t.Fatal(e)
				}
				query := url.Values{"mode": {tc.mode}, "platform": {tc.platform}, "dnsMode": {tc.dnsMode}, "extras": {a.gameExits[0].ID}, "exit": {"auto"}}
				r := httptest.NewRequest("GET", "/api/gaming/export?"+query.Encode(), nil)
				if e = a.addGameExitChoices(config, r); e != nil {
					t.Fatal(e)
				}
				if e = applyGamePolicy(config, DNSClient{Presets: []string{p.ID}}, r); e != nil {
					t.Fatal(e)
				}
				b, _ := json.Marshal(config)
				file := filepath.Join(t.TempDir(), "game.json")
				_ = os.WriteFile(file, b, 0600)
				if data, e := exec.Command(bin, "check", "-c", file).CombinedOutput(); e != nil {
					t.Fatal("native game config rejected", e, string(data))
				}
			})
		}
	}
}
