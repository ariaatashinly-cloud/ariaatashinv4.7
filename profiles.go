package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Profile struct {
	Tuning     ManualTuning `json:"tuning,omitempty"`
	Manual     bool         `json:"manual,omitempty"`
	Endpoint   LinkEndpoint `json:"endpoint,omitempty"`
	XHTTPMode  string       `json:"xhttpMode,omitempty"`
	Method     string       `json:"method,omitempty"`
	Direct     bool         `json:"direct"`
	UDP        bool         `json:"udp"`
	Security   string       `json:"security"`
	Available  bool         `json:"available"`
	Reason     string       `json:"reason"`
	ServerName string       `json:"serverName,omitempty"`
	PublicKey  string       `json:"publicKey,omitempty"`
	ShortID    string       `json:"shortId,omitempty"`
	Key        string       `json:"key"`
	Name       string       `json:"name"`
	Protocol   string       `json:"protocol"`
	Network    string       `json:"network"`
	Port       int          `json:"internalPort"`
	Path       string       `json:"path"`
	InboundID  int          `json:"inboundId"`
	Ready      bool         `json:"ready"`
}

var profileTemplates = []Profile{
	{Key: "vless-ws", Name: "VLESS · WebSocket", Protocol: "vless", Network: "ws", Port: 20910},
	{Key: "vless-xhttp", Name: "VLESS · XHTTP", Protocol: "vless", Network: "xhttp", Port: 20911},
	{Key: "vmess-ws", Name: "VMess · WebSocket", Protocol: "vmess", Network: "ws", Port: 20912},
	{Key: "trojan-ws", Name: "Trojan · WebSocket", Protocol: "trojan", Network: "ws", Port: 20913},
	{Key: "vmess-xhttp", Name: "VMess · XHTTP", Protocol: "vmess", Network: "xhttp", Port: 20914},
	{Key: "trojan-xhttp", Name: "Trojan · XHTTP", Protocol: "trojan", Network: "xhttp", Port: 20915},
	{Key: "shadowsocks-ws", Name: "Shadowsocks · WebSocket", Protocol: "shadowsocks", Network: "ws", Port: 20916, Method: defaultShadowsocksMethod},
	{Key: "shadowsocks-xhttp", Name: "Shadowsocks · XHTTP", Protocol: "shadowsocks", Network: "xhttp", Port: 20917, Method: defaultShadowsocksMethod},
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("OS random unavailable")
	}
	return hex.EncodeToString(b)
}
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("OS random unavailable")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func validatePublicURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("use an absolute http(s) origin without a path, query or credentials")
	}
	if strings.ContainsAny(u.Hostname(), " \t\r\n\\/@,;\"'<>") || strings.HasSuffix(u.Host, ":") {
		return nil, fmt.Errorf("invalid hostname")
	}
	if net.ParseIP(u.Hostname()) == nil {
		name := strings.TrimSuffix(u.Hostname(), ".")
		if len(name) > 253 {
			return nil, fmt.Errorf("invalid hostname")
		}
		for _, label := range strings.Split(name, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return nil, fmt.Errorf("invalid hostname")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
					return nil, fmt.Errorf("invalid hostname")
				}
			}
		}
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid public port")
		}
	}
	u.Path = ""
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

func publicEndpoint(raw string) (host, port, security string) {
	u, _ := validatePublicURL(raw)
	if u == nil {
		return "", "", ""
	}
	host = u.Hostname()
	port = u.Port()
	security = "none"
	if u.Scheme == "https" {
		security = "tls"
	}
	if port == "" {
		if security == "tls" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return
}

type User struct {
	Location     GeoInfo        `json:"location"`
	Email        string         `json:"email"`
	Name         string         `json:"name"`
	Enabled      bool           `json:"enabled"`
	Status       string         `json:"status"`
	Quota        int64          `json:"quota"`
	Expiry       int64          `json:"expiry"`
	Up           int64          `json:"up"`
	Down         int64          `json:"down"`
	LastOnline   int64          `json:"lastOnline"`
	SubID        string         `json:"subId"`
	Subscription string         `json:"subscription"`
	Links        []Connection   `json:"links"`
	Raw          map[string]any `json:"-"`
}
type Connection struct {
	Name    string `json:"name"`
	Profile string `json:"profile"`
	Link    string `json:"link"`
}

func textField(m map[string]any, k string) string { v, _ := m[k].(string); return v }
func numField(m map[string]any, k string) int64   { v, _ := m[k].(float64); return int64(v) }
func boolField(m map[string]any, k string) bool   { v, _ := m[k].(bool); return v }

func shareLink(p Profile, c map[string]any, origin string) string {
	return shareLinkWithEndpoint(p, c, origin, LinkEndpoint{})
}

func shareLinkWithEndpoint(p Profile, c map[string]any, origin string, defaults LinkEndpoint) string {
	if p.Direct {
		return directShareLink(p, c, origin)
	}
	address, port, host, sni, security := connectionEndpoint(origin, defaults, p.Endpoint)
	name := textField(c, "comment")
	if name == "" {
		name = textField(c, "email")
	}
	name += " · " + p.Name
	id := textField(c, "id")
	if id == "" {
		id = textField(c, "uuid")
	}
	if p.Protocol == "vmess" {
		v := map[string]any{"v": "2", "ps": name, "add": address, "port": port, "id": id, "aid": "0", "scy": "auto", "net": p.Network, "type": "none", "host": host, "path": p.Path, "tls": "", "sni": sni, "alpn": "http/1.1", "fp": "chrome"}
		if p.Network == "xhttp" {
			mode := p.XHTTPMode
			if mode == "" {
				mode = "auto"
			}
			v["mode"], v["alpn"] = mode, "h2,http/1.1"
		}
		if security == "tls" {
			v["tls"] = "tls"
		}
		b, _ := json.Marshal(v)
		return "vmess://" + base64.StdEncoding.EncodeToString(b)
	}
	q := url.Values{"security": {security}, "type": {p.Network}, "path": {p.Path}, "host": {host}}
	if p.Protocol == "vless" {
		q.Set("encryption", "none")
	}
	if security == "tls" {
		q.Set("sni", sni)
		q.Set("fp", "chrome")
		if p.Network == "ws" {
			q.Set("alpn", "http/1.1")
		} else {
			q.Set("alpn", "h2,http/1.1")
		}
	}
	if p.Network == "xhttp" {
		mode := p.XHTTPMode
		if mode == "" {
			mode = "auto"
		}
		q.Set("mode", mode)
	}
	if p.Protocol == "trojan" {
		id = textField(c, "password")
	}
	scheme := p.Protocol
	if p.Protocol == "shadowsocks" {
		// WS/XHTTP parameters extend SIP002. Some clients discard them; the
		// matching Xray outbound is the authoritative transport configuration.
		scheme = "ss"
		id = base64.RawURLEncoding.EncodeToString([]byte(p.Method + ":" + textField(c, "password")))
	}
	u := url.URL{Scheme: scheme, User: url.User(id), Host: net.JoinHostPort(address, port), RawQuery: q.Encode(), Fragment: name}
	return u.String()
}

func inboundProfile(ib Inbound) (Profile, bool) {
	if strings.HasPrefix(ib.Remark, "aria:manual:") {
		return manualInboundProfile(ib)
	}
	if p, ok := directInboundProfile(ib); ok {
		return p, true
	}
	for _, tpl := range profileTemplates {
		if ib.Remark != "atia:"+tpl.Key && ib.Remark != "aria:"+tpl.Key {
			continue
		}
		var stream map[string]any
		if nestedJSON(ib.Stream, &stream) != nil || textField(stream, "network") != tpl.Network || textField(stream, "security") != "none" || ib.Protocol != tpl.Protocol || ib.Port < 20800 || ib.Port > 30000 || (ib.Listen != "127.0.0.1" && ib.Listen != "localhost") {
			return Profile{}, false
		}
		settings, _ := stream[tpl.Network+"Settings"].(map[string]any)
		tpl.Path = textField(settings, "path")
		if !strings.HasPrefix(tpl.Path, "/connect/") || !strings.HasSuffix(tpl.Path, "/") || strings.Contains(tpl.Path, "..") {
			return Profile{}, false
		}
		if tpl.Network == "xhttp" {
			tpl.XHTTPMode = textField(settings, "mode")
		}
		if tpl.Protocol == "shadowsocks" && !cloudShadowsocksSettingsMatch(ib, tpl.Method) {
			return Profile{}, false
		}
		tpl.Port, tpl.InboundID, tpl.Ready = ib.Port, ib.ID, ib.Enable
		return tpl, true
	}
	return Profile{}, false
}

func userStatus(u User) string {
	if !u.Enabled {
		return "disabled"
	}
	if u.Expiry > 0 && u.Expiry < time.Now().UnixMilli() {
		return "expired"
	}
	if u.Quota > 0 && u.Up+u.Down >= u.Quota {
		return "limited"
	}
	return "active"
}

type CreateRequest struct {
	OperationID     string        `json:"operationId,omitempty"`
	RequestUUID     string        `json:"requestUUID,omitempty"`
	RequestPassword string        `json:"requestPassword,omitempty"`
	Manual          *ManualCreate `json:"manual,omitempty"`
	Name            string        `json:"name"`
	Count           int           `json:"count"`
	QuotaGB         float64       `json:"quotaGB"`
	Days            int           `json:"days"`
	Profiles        []string      `json:"profiles"`
}

func validateCreate(req CreateRequest) error {
	if req.OperationID != "" && (!regexp.MustCompile(`^[a-f0-9]{16}$`).MatchString(req.OperationID) || req.Count != 1 || req.Manual != nil || !uuidPattern.MatchString(req.RequestUUID) || !validClientPassword(req.RequestPassword)) {
		return fmt.Errorf("شناسه عملیات ساخت معتبر نیست")
	}
	if len([]rune(strings.TrimSpace(req.Name))) < 1 || len([]rune(req.Name)) > 64 {
		return fmt.Errorf("نام باید بین ۱ تا ۶۴ کاراکتر باشد")
	}
	if strings.ContainsAny(req.Name, "\r\n\x00") {
		return fmt.Errorf("نام معتبر نیست")
	}
	if req.Count < 1 || req.Count > 50 {
		return fmt.Errorf("تعداد باید بین ۱ و ۵۰ باشد")
	}
	if math.IsNaN(req.QuotaGB) || math.IsInf(req.QuotaGB, 0) || req.QuotaGB < 0 || req.QuotaGB > 1e6 || req.Days < 0 || req.Days > 3650 {
		return fmt.Errorf("حجم یا مدت اعتبار نامعتبر است")
	}
	if req.Manual != nil {
		if req.Count != 1 || len(req.Profiles) != 0 {
			return fmt.Errorf("ساخت دستی برای یک کاربر است؛ الگوهای آماده را هم‌زمان انتخاب نکن")
		}
		_, err := normalizeManual(*req.Manual)
		return err
	}
	if len(req.Profiles) < 1 || len(req.Profiles) > 64 {
		return fmt.Errorf("حداقل یک نوع اتصال انتخاب کن")
	}
	seen := map[string]bool{}
	for _, key := range req.Profiles {
		found := manualKeyPattern.MatchString(key)
		for _, p := range allTemplates() {
			if key == p.Key {
				found = true
			}
		}
		if !found || seen[key] {
			return fmt.Errorf("نوع اتصال نامعتبر یا تکراری است")
		}
		seen[key] = true
	}
	return nil
}
