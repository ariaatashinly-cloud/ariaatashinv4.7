package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestConnectionAddressPreservesTLSAndHTTPIdentity(t *testing.T) {
	c := map[string]any{"id": newUUID(), "password": "test-password-123", "comment": "تست"}
	for _, address := range []string{"203.0.113.17", "2001:db8::17", "edge.example"} {
		for _, p := range profileTemplates {
			p.Path = "/connect/test-address/"
			link := shareLinkWithEndpoint(p, c, "https://panel.example", LinkEndpoint{Address: address, Port: 8443})
			if p.Protocol == "vmess" {
				b, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
				var v map[string]any
				if json.Unmarshal(b, &v) != nil || v["add"] != address || v["host"] != "panel.example" || v["sni"] != "panel.example" || v["port"] != "8443" || v["tls"] != "tls" {
					t.Fatal("VMess lost routing or TLS identity", string(b))
				}
				continue
			}
			u, err := url.Parse(link)
			if err != nil || u.Hostname() != address || u.Port() != "8443" || u.Query().Get("sni") != "panel.example" || u.Query().Get("host") != "panel.example" || u.Query().Get("security") != "tls" || u.Query().Get("allowInsecure") != "" {
				t.Fatal("dial address must not replace TLS identity", link, err)
			}
		}
	}
	p := profileTemplates[0]
	p.Path = "/connect/test-local-override/"
	p.Endpoint = LinkEndpoint{Address: "2001:db8::22", Host: "route.example", SNI: "cert.example", Port: 443}
	u, _ := url.Parse(shareLinkWithEndpoint(p, c, "https://panel.example", LinkEndpoint{Address: "203.0.113.1", Port: 8443, Host: "global.example"}))
	if u.Hostname() != "2001:db8::22" || u.Port() != "443" || u.Query().Get("host") != "route.example" || u.Query().Get("sni") != "cert.example" {
		t.Fatal("manual endpoint must override global defaults", u)
	}
	o := singBoxOutboundWithEndpoint(p, c, "https://panel.example", LinkEndpoint{Address: "203.0.113.1"})
	if o["server"] != "2001:db8::22" || o["tls"].(map[string]any)["server_name"] != "cert.example" || o["transport"].(map[string]any)["headers"].(map[string]string)["Host"] != "route.example" {
		t.Fatal("sing-box disagrees with share link", o)
	}
}

func TestConnectionSettingsPersistAndSurvivePartialSaves(t *testing.T) {
	a := automaticApp(t)
	cookie, csrf := autoLogin(t, a, "https://panel.example")
	save := func(body string) *httptest.ResponseRecorder {
		r := browserRequest("POST", "/api/settings", "https://panel.example", body)
		r.AddCookie(cookie)
		r.Header.Set("X-ATIA-CSRF", csrf)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	w := save(`{"publicURL":"https://panel.example","automaticDomain":true,"hostPreset":"auto","connection":{"address":"203.0.113.9"}}`)
	if w.Code != 200 || a.snapshot().Settings.Connection.Address != "203.0.113.9" {
		t.Fatal("connection settings were not decoded or saved", w.Code, w.Body.String())
	}
	u, _ := url.Parse(a.snapshot().Users[0].Links[0].Link)
	if u.Hostname() != "203.0.113.9" || u.Query().Get("sni") != "panel.example" {
		t.Fatal("existing subscription links did not update", u)
	}
	w = save(`{"publicURL":"https://panel.example","automaticDomain":true,"hostPreset":"auto"}`)
	if w.Code != 200 || a.snapshot().Settings.Connection.Address != "203.0.113.9" {
		t.Fatal("host/domain save erased the endpoint override", w.Code, w.Body.String())
	}
	loaded, err := NewApp(a.cfg, a.base)
	if err != nil || loaded.snapshot().Settings.Connection.Address != "203.0.113.9" {
		t.Fatal("connection override did not survive restart", err)
	}
	for _, value := range []string{`{"address":"https://bad.example"}`, `{"host":"bad\r\nHost: attacker"}`, `{"port":65536}`, `{"sni":"good.example@bad.example"}`} {
		w = save(`{"publicURL":"https://panel.example","hostPreset":"auto","connection":` + value + `}`)
		if w.Code != 400 || a.snapshot().Settings.Connection.Address != "203.0.113.9" {
			t.Fatal("invalid endpoint changed saved settings", w.Code, value)
		}
	}
	w = save(`{"publicURL":"https://panel.example","automaticDomain":true,"hostPreset":"auto","connection":{}}`)
	if w.Code != 200 || a.snapshot().Settings.Connection != (LinkEndpoint{}) {
		t.Fatal("explicit reset did not clear endpoint", w.Code)
	}
}

func TestManualValidationAndNativeMetadataGuard(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan", "shadowsocks"} {
		for _, network := range []string{"ws", "xhttp"} {
			m, err := normalizeManual(ManualCreate{Protocol: protocol, Network: network, Path: "/connect/manual-test-01/", Endpoint: LinkEndpoint{Address: "203.0.113.9"}})
			if err != nil {
				t.Fatal(err)
			}
			spec := m.spec("manual-abcdef123456")
			stream := map[string]any{"network": network, "security": "none", network + "Settings": map[string]any{"path": m.Path, "mode": m.Mode}}
			ib := Inbound{Remark: manualRemark(spec), Protocol: protocol, Enable: true, Port: 20950, ID: 5, Listen: "127.0.0.1", Stream: json.RawMessage(marshalString(stream)), Settings: json.RawMessage(`{"clients":[]}`)}
			if protocol == "shadowsocks" {
				ib.Settings = json.RawMessage(marshalString(map[string]any{"clients": []any{}, "method": m.Method, "network": "tcp"}))
			}
			p, ok := inboundProfile(ib)
			if !ok || !p.Manual || p.Endpoint != m.Endpoint || p.XHTTPMode != m.Mode {
				t.Fatal("native metadata was lost", p)
			}
			a := testApp(t)
			a.publish([]Inbound{ib}, json.RawMessage(`{}`))
			if len(a.snapshot().Profiles) != len(allTemplates())+1 || a.routes[m.Path] == nil {
				t.Fatal("manual profile or public route missing")
			}
			ib.Listen = "0.0.0.0"
			if _, ok = inboundProfile(ib); ok {
				t.Fatal("non-loopback inbound accepted")
			}
			ib.Listen = "127.0.0.1"
			stream[network+"Settings"] = map[string]any{"path": "/connect/different-path/", "mode": m.Mode}
			ib.Stream = json.RawMessage(marshalString(stream))
			if _, ok = inboundProfile(ib); ok {
				t.Fatal("mismatched path accepted")
			}
		}
	}
	bad := []ManualCreate{
		{Protocol: "vless", Network: "tcp"}, {Protocol: "unknown", Network: "ws"},
		{Protocol: "vless", Network: "ws", Path: "/api/settings"}, {Protocol: "vless", Network: "ws", Path: "/connect/../"},
		{Protocol: "vless", Network: "ws", Path: "/connect/short/"}, {Protocol: "vless", Network: "xhttp", Mode: "invalid"},
		{Protocol: "vless", Network: "ws", Mode: "auto"}, {Protocol: "vless", Network: "ws", UUID: "bad-id"},
		{Protocol: "trojan", Network: "ws", UUID: newUUID()}, {Protocol: "trojan", Network: "ws", Password: "short"},
		{Protocol: "trojan", Network: "ws", Password: "unsafe password 123"}, {Protocol: "vless", Network: "ws", Password: "valid-password-123"},
		{Protocol: "shadowsocks", Network: "ws", UUID: newUUID()}, {Protocol: "shadowsocks", Network: "ws", Password: "short"},
		{Protocol: "shadowsocks", Network: "ws", Method: "none"}, {Protocol: "shadowsocks", Network: "ws", Method: "2022-blake3-aes-256-gcm"},
		{Protocol: "vless", Network: "ws", Method: "aes-128-gcm"},
	}
	for _, m := range bad {
		if _, err := normalizeManual(m); err == nil {
			t.Fatal("accepted invalid manual input", m)
		}
	}
	good := ManualCreate{Protocol: "vless", Network: "ws"}
	if validateCreate(CreateRequest{Name: "manual", Count: 1, Manual: &good}) != nil {
		t.Fatal("valid manual request rejected")
	}
	if validateCreate(CreateRequest{Name: "manual", Count: 2, Manual: &good}) == nil || validateCreate(CreateRequest{Name: "manual", Count: 1, Manual: &good, Profiles: []string{"vless-ws"}}) == nil {
		t.Fatal("mixed or bulk manual request accepted")
	}
}

func TestShadowsocksCloudCredentialsTransportAndExports(t *testing.T) {
	client := map[string]any{"password": "Test+password:/!123", "id": newUUID()}
	for _, method := range []string{defaultShadowsocksMethod, "aes-128-gcm", "aes-256-gcm"} {
		for _, network := range []string{"ws", "xhttp"} {
			m, err := normalizeManual(ManualCreate{Protocol: "shadowsocks", Network: network, Method: method, Path: "/connect/ss-transport-test/"})
			if err != nil {
				t.Fatal(err)
			}
			p := m.spec("manual-abcdef123456").profile()
			endpoint := LinkEndpoint{Address: "2001:db8::25", Port: 8443, Host: "route.example", SNI: "certificate.example"}
			link, err := url.Parse(shareLinkWithEndpoint(p, client, "https://panel.example", endpoint))
			if err != nil {
				t.Fatal(err)
			}
			credential, err := base64.RawURLEncoding.DecodeString(link.User.Username())
			if err != nil || link.Scheme != "ss" || string(credential) != method+":"+textField(client, "password") || link.Query().Get("type") != network || link.Query().Get("path") != p.Path || link.Hostname() != endpoint.Address {
				t.Fatal("Shadowsocks credential or transport changed", link)
			}
			outbound := xrayCloudOutbound(p, client, "https://panel.example", endpoint)
			server := outbound["settings"].(map[string]any)["servers"].([]any)[0].(map[string]any)
			stream := outbound["streamSettings"].(map[string]any)
			if server["method"] != method || server["password"] != client["password"] || server["address"] != endpoint.Address || stream["network"] != network || stream["tlsSettings"].(map[string]any)["serverName"] != endpoint.SNI || stream[network+"Settings"].(map[string]any)["host"] != endpoint.Host {
				t.Fatal("Xray does not match Shadowsocks link", outbound)
			}
			if singBoxOutboundWithEndpoint(p, client, "https://panel.example", endpoint) != nil {
				t.Fatal("unsupported Shadowsocks web transport exported to sing-box")
			}
		}
	}
}

func TestShadowsocksCloudNativeMetadataRejectsMismatch(t *testing.T) {
	for _, tpl := range profileTemplates {
		if tpl.Protocol != "shadowsocks" {
			continue
		}
		stream := map[string]any{"network": tpl.Network, "security": "none", tpl.Network + "Settings": map[string]any{"path": "/connect/ss-native-guard/", "mode": "auto"}}
		ib := Inbound{Remark: "aria:" + tpl.Key, Protocol: "shadowsocks", Port: tpl.Port, Listen: "127.0.0.1", Stream: json.RawMessage(marshalString(stream))}
		for _, settings := range []map[string]any{{"method": "none", "network": "tcp"}, {"method": defaultShadowsocksMethod, "network": "tcp,udp"}, {"method": "aes-256-gcm", "network": "tcp"}} {
			ib.Settings = json.RawMessage(marshalString(settings))
			if _, ok := inboundProfile(ib); ok {
				t.Fatal("mismatched Shadowsocks settings accepted", settings)
			}
		}
		ib.Settings = json.RawMessage(marshalString(map[string]any{"method": defaultShadowsocksMethod, "network": "tcp"}))
		if _, ok := inboundProfile(ib); !ok {
			t.Fatal("valid Shadowsocks cloud inbound rejected")
		}
	}
}
