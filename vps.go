package main

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var directTemplates = []Profile{
	{Key: "vless-reality", Name: "VLESS · REALITY / Vision", Protocol: "vless", Network: "tcp", Port: 10443, Direct: true, Security: "reality"},
	{Key: "vless-xhttp-reality", Name: "VLESS · XHTTP / REALITY", Protocol: "vless", Network: "xhttp", Port: 10444, Direct: true, Security: "reality"},
	{Key: "vless-tls", Name: "VLESS · TLS / Vision", Protocol: "vless", Network: "tcp", Port: 10445, Direct: true, Security: "tls"},
	{Key: "trojan-tls", Name: "Trojan · TLS", Protocol: "trojan", Network: "tcp", Port: 10446, Direct: true, Security: "tls"},
	{Key: "vmess-tls", Name: "VMess · TLS", Protocol: "vmess", Network: "tcp", Port: 10447, Direct: true, Security: "tls"},
	{Key: "vless-grpc", Name: "VLESS · gRPC / TLS", Protocol: "vless", Network: "grpc", Port: 10448, Direct: true, Security: "tls"},
	{Key: "trojan-grpc", Name: "Trojan · gRPC / TLS", Protocol: "trojan", Network: "grpc", Port: 10449, Direct: true, Security: "tls"},
	{Key: "shadowsocks", Name: "Shadowsocks · ChaCha20", Protocol: "shadowsocks", Network: "tcp", Port: 10450, Direct: true, Security: "none", UDP: true},
	{Key: "tuic", Name: "TUIC v5 · QUIC", Protocol: "tuic", Network: "quic", Port: 10451, Direct: true, Security: "tls", UDP: true},
	{Key: "hysteria2", Name: "Hysteria2 · QUIC", Protocol: "hysteria", Network: "hysteria", Port: 10452, Direct: true, Security: "tls", UDP: true},
}

type VPSConfig struct {
	ServerName    string "json:\"serverName\""
	PortBase      int    "json:\"portBase\""
	CertFile      string "json:\"certFile\""
	KeyFile       string "json:\"keyFile\""
	RealityTarget string "json:\"realityTarget\""
	PrivateKey    string "json:\"privateKey\""
	ShortID       string "json:\"shortId\""
}

func allTemplates() []Profile {
	return append(append([]Profile{}, profileTemplates...), directTemplates...)
}
func isDirectKey(key string) bool {
	for _, p := range directTemplates {
		if p.Key == key {
			return true
		}
	}
	return false
}
func clientFlow(keys []string) string {
	for _, k := range keys {
		if k == "vless-reality" || k == "vless-tls" {
			return "xtls-rprx-vision"
		}
	}
	return ""
}
func (v VPSConfig) publicKey() string {
	b, e := base64.RawURLEncoding.DecodeString(v.PrivateKey)
	if e != nil {
		return ""
	}
	key, e := ecdh.X25519().NewPrivateKey(b)
	if e != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
}
func validHostname(h string) bool {
	u, e := validatePublicURL("https://" + h)
	return e == nil && u.Hostname() == h && u.Port() == "" && net.ParseIP(h) == nil && strings.Contains(h, ".")
}
func validateVPS(v VPSConfig, port int) error {
	if v.PortBase < 1024 || v.PortBase > 65526 {
		return fmt.Errorf("پورت پایه باید بین ۱۰۲۴ و ۶۵۵۲۶ باشد")
	}
	for p := v.PortBase; p < v.PortBase+10; p++ {
		if p == port || p == panelPort {
			return fmt.Errorf("پورت پایه با پورت مدیریت تداخل دارد")
		}
	}
	if !validHostname(v.RealityTarget) {
		return fmt.Errorf("مقصد REALITY باید نام دامنهٔ معتبر با TLS 1.3 باشد")
	}
	if v.ServerName != "" && !validHostname(v.ServerName) {
		return fmt.Errorf("نام دامنهٔ TLS معتبر نیست")
	}
	if v.publicKey() == "" {
		return fmt.Errorf("کلید REALITY معتبر نیست")
	}
	if len(v.ShortID) < 8 || len(v.ShortID) > 16 || len(v.ShortID)%2 != 0 {
		return fmt.Errorf("Short ID معتبر نیست")
	}
	for _, c := range v.ShortID {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return fmt.Errorf("Short ID معتبر نیست")
		}
	}
	for _, p := range []string{v.CertFile, v.KeyFile} {
		if p != "" && (!filepath.IsAbs(p) || strings.ContainsAny(p, "\r\n\x00")) {
			return fmt.Errorf("مسیر گواهی باید مطلق باشد")
		}
	}
	return nil
}
func (v VPSConfig) certificateError() error {
	if v.ServerName == "" || v.CertFile == "" || v.KeyFile == "" {
		return fmt.Errorf("گواهی TLS تنظیم نشده است")
	}
	pair, e := tls.LoadX509KeyPair(v.CertFile, v.KeyFile)
	if e != nil {
		return fmt.Errorf("فایل گواهی یا کلید قابل خواندن نیست یا با هم سازگار نیستند")
	}
	leaf, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil {
		return e
	}
	inter := x509.NewCertPool()
	for _, b := range pair.Certificate[1:] {
		c, e := x509.ParseCertificate(b)
		if e == nil {
			inter.AddCert(c)
		}
	}
	_, e = leaf.Verify(x509.VerifyOptions{DNSName: v.ServerName, Intermediates: inter})
	if e != nil {
		return fmt.Errorf("گواهی TLS باید معتبر، دارای زنجیرهٔ مورد اعتماد و مطابق دامنه باشد")
	}
	return nil
}
func atomicJSON(dir, name string, v any) error {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".aria-private-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), filepath.Join(dir, name))
}
func (a *App) loadAdvanced() error {
	v := VPSConfig{PortBase: 10443, RealityTarget: "www.microsoft.com"}
	if b, e := os.ReadFile(filepath.Join(a.cfg.DataDir, "aria-vps.json")); e == nil {
		if e = json.Unmarshal(b, &v); e != nil {
			return fmt.Errorf("VPS settings invalid")
		}
	}
	if v.PrivateKey == "" {
		k, e := ecdh.X25519().GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		v.PrivateKey = base64.RawURLEncoding.EncodeToString(k.Bytes())
		v.ShortID = randomToken(8)
	}
	if s := envDefault("ATIA_TLS_SERVER_NAME", ""); s != "" {
		v.ServerName = strings.ToLower(s)
	}
	if s := envDefault("ATIA_TLS_CERT", ""); s != "" {
		v.CertFile = s
	}
	if s := envDefault("ATIA_TLS_KEY", ""); s != "" {
		v.KeyFile = s
	}
	if e := validateVPS(v, a.cfg.Port); e != nil {
		return e
	}
	a.vps = v
	if a.cfg.Mode == "vps" {
		if e := atomicJSON(a.cfg.DataDir, "aria-vps.json", v); e != nil {
			return e
		}
	}
	if b, e := os.ReadFile(filepath.Join(a.cfg.DataDir, "aria-telegram.json")); e == nil {
		if e = json.Unmarshal(b, &a.telegram); e != nil {
			return fmt.Errorf("Telegram settings invalid")
		}
	}
	if s := envDefault("ATIA_TELEGRAM_TOKEN", ""); s != "" {
		a.telegram.Token = s
	}
	if s := envDefault("ATIA_TELEGRAM_CHAT_ID", ""); s != "" {
		n, e := strconv.ParseInt(s, 10, 64)
		if e != nil || n <= 0 {
			return fmt.Errorf("ARIA_TELEGRAM_CHAT_ID must be a positive private chat id")
		}
		a.telegram.ChatID = n
	}
	if s := envDefault("ATIA_TELEGRAM_ENABLED", ""); s != "" {
		a.telegram.Enabled = s == "true"
	}
	return nil
}
func (a *App) vpsSnapshot() VPSConfig {
	a.advancedMu.RLock()
	defer a.advancedMu.RUnlock()
	return a.vps
}
func (a *App) publicVPS() any {
	v := a.vpsSnapshot()
	note := ""
	if e := v.certificateError(); e != nil {
		note = e.Error()
	}
	return map[string]any{"mode": envMode(a.cfg.Mode), "serverName": v.ServerName, "portBase": v.PortBase, "certFile": v.CertFile, "keyFile": v.KeyFile, "realityTarget": v.RealityTarget, "publicKey": v.publicKey(), "shortId": v.ShortID, "certificateReady": note == "", "certificateNote": note}
}
func (a *App) saveVPS(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Mode != "vps" {
		apiError(w, 409, "ARIA_MODE=vps فقط روی سرور با پورت مستقیم فعال شود")
		return
	}
	var in VPSConfig
	if readBody(w, r, &in, 8192) != nil {
		apiError(w, 400, "تنظیمات VPS معتبر نیست")
		return
	}
	a.mutation.Lock()
	defer a.mutation.Unlock()
	a.mu.RLock()
	active := false
	for _, ib := range a.inbounds {
		if p, ok := inboundProfile(ib); ok && p.Direct {
			active = true
		}
	}
	a.mu.RUnlock()
	if active {
		apiError(w, 409, "برای حفظ کانفیگ‌های موجود، ابتدا کاربران و اینباندهای مستقیم را منتقل کن؛ تنظیمات در حال استفاده قفل است")
		return
	}
	old := a.vpsSnapshot()
	in.PrivateKey = old.PrivateKey
	in.ShortID = old.ShortID
	in.ServerName = strings.ToLower(strings.TrimSpace(in.ServerName))
	in.RealityTarget = strings.ToLower(strings.TrimSpace(in.RealityTarget))
	if e := validateVPS(in, a.cfg.Port); e != nil {
		apiError(w, 400, e.Error())
		return
	}
	if in.CertFile != "" || in.KeyFile != "" {
		if e := in.certificateError(); e != nil {
			apiError(w, 400, e.Error())
			return
		}
	}
	if e := atomicJSON(a.cfg.DataDir, "aria-vps.json", in); e != nil {
		apiError(w, 500, "ذخیره انجام نشد")
		return
	}
	a.advancedMu.Lock()
	a.vps = in
	a.advancedMu.Unlock()
	a.mu.RLock()
	all, eng := a.inbounds, a.state.Engine
	a.mu.RUnlock()
	a.publish(all, eng)
	a.audit("تنظیمات سرور مجازی ذخیره شد", "settings")
	_ = a.persist()
	jsonReply(w, 200, a.runtimeSnapshot())
}
func (a *App) restoreVPS(b Backup) error {
	needed := false
	for _, u := range b.Users {
		for _, k := range u.Profiles {
			if isDirectKey(k) {
				needed = true
			}
		}
	}
	if !needed {
		return nil
	}
	if a.cfg.Mode != "vps" {
		return fmt.Errorf("این بکاپ الگوهای مستقیم دارد؛ روی VPS بازیابی کن")
	}
	if b.VPS == nil {
		return fmt.Errorf("هویت REALITY در بکاپ نیست")
	}
	v := *b.VPS
	old := a.vpsSnapshot()
	v.ServerName, v.CertFile, v.KeyFile = old.ServerName, old.CertFile, old.KeyFile
	if e := validateVPS(v, a.cfg.Port); e != nil {
		return e
	}
	a.mu.RLock()
	active := false
	for _, ib := range a.inbounds {
		if p, ok := inboundProfile(ib); ok && p.Direct {
			active = true
		}
	}
	a.mu.RUnlock()
	if active && (v.PrivateKey != old.PrivateKey || v.ShortID != old.ShortID || v.PortBase != old.PortBase || v.RealityTarget != old.RealityTarget) {
		return fmt.Errorf("بکاپ با هویت VPS فعلی تداخل دارد")
	}
	if e := atomicJSON(a.cfg.DataDir, "aria-vps.json", v); e != nil {
		return e
	}
	a.advancedMu.Lock()
	a.vps = v
	a.advancedMu.Unlock()
	return nil
}
func directInboundProfile(ib Inbound) (Profile, bool) {
	for _, t := range directTemplates {
		if ib.Remark != "aria:"+t.Key || ib.Protocol != t.Protocol || ib.Listen != "0.0.0.0" || ib.Port < 1024 || ib.Port > 65535 {
			continue
		}
		var stream, settings map[string]any
		if nestedJSON(ib.Stream, &stream) != nil || nestedJSON(ib.Settings, &settings) != nil {
			return Profile{}, false
		}
		if t.Protocol != "tuic" && (textField(stream, "network") != t.Network || textField(stream, "security") != t.Security) {
			return Profile{}, false
		}
		t.InboundID, t.Port, t.Ready, t.Available = ib.ID, ib.Port, ib.Enable, true
		if t.Network == "xhttp" {
			x, _ := stream["xhttpSettings"].(map[string]any)
			t.Path = textField(x, "path")
		}
		if t.Network == "grpc" {
			x, _ := stream["grpcSettings"].(map[string]any)
			t.Path = "/connect/" + textField(x, "serviceName") + "/"
		}
		if t.Security == "reality" {
			x, _ := stream["realitySettings"].(map[string]any)
			names, _ := x["serverNames"].([]any)
			if len(names) > 0 {
				t.ServerName, _ = names[0].(string)
			}
			v := VPSConfig{PrivateKey: textField(x, "privateKey")}
			t.PublicKey = v.publicKey()
			ids, _ := x["shortIds"].([]any)
			if len(ids) > 0 {
				t.ShortID, _ = ids[0].(string)
			}
		} else if t.Security == "tls" {
			if t.Protocol == "tuic" {
				x, _ := settings["server"].(map[string]any)
				t.ServerName = textField(x, "sni")
			} else {
				x, _ := stream["tlsSettings"].(map[string]any)
				t.ServerName = textField(x, "serverName")
			}
		}
		return t, true
	}
	return Profile{}, false
}
func (a *App) ensureDirectProfile(ctx context.Context, e *EngineClient, key, path string) (Profile, error) {
	var p Profile
	for _, t := range a.profileCatalog() {
		if t.Key == key {
			p = t
		}
	}
	if !p.Direct || !p.Available {
		return p, fmt.Errorf("این الگو قابل اجرا نیست: %s", p.Reason)
	}
	all, err := e.inbounds(ctx)
	if err != nil {
		return p, err
	}
	for _, ib := range all {
		if ready, ok := inboundProfile(ib); ok && ready.Key == key {
			if !ready.Ready {
				return ready, fmt.Errorf("اینباند خاموش است")
			}
			return ready, nil
		}
		if ib.Port == p.Port {
			return p, fmt.Errorf("پورت %d اشغال است", p.Port)
		}
	}
	v := a.vpsSnapshot()
	if (p.Network == "xhttp" || p.Network == "grpc") && path == "" {
		path = "/connect/" + p.Key + "-" + randomToken(6) + "/"
	}
	p.Path = path
	stream := map[string]any{"network": p.Network, "security": p.Security}
	settings := map[string]any{"clients": []any{}}
	if p.Protocol == "vless" {
		settings["decryption"] = "none"
		settings["fallbacks"] = []any{}
	}
	if p.Protocol == "shadowsocks" {
		settings["method"] = "chacha20-ietf-poly1305"
		settings["network"] = "tcp,udp"
		stream = map[string]any{"network": "tcp", "security": "none"}
	}
	if p.Network == "xhttp" {
		stream["xhttpSettings"] = map[string]any{"path": path, "mode": "auto"}
	}
	if p.Network == "grpc" {
		stream["grpcSettings"] = map[string]any{"serviceName": strings.TrimSuffix(strings.TrimPrefix(path, "/connect/"), "/"), "multiMode": false}
	}
	if p.Security == "reality" {
		stream["realitySettings"] = map[string]any{"show": false, "dest": v.RealityTarget + ":443", "xver": 0, "serverNames": []string{v.RealityTarget}, "privateKey": v.PrivateKey, "shortIds": []string{v.ShortID}, "settings": map[string]any{"publicKey": v.publicKey(), "fingerprint": "chrome", "serverName": v.RealityTarget, "spiderX": "/"}}
	}
	if p.Security == "tls" {
		stream["tlsSettings"] = map[string]any{"serverName": v.ServerName, "minVersion": "1.2", "maxVersion": "1.3", "alpn": []string{"h2", "http/1.1"}, "certificates": []any{map[string]any{"certificateFile": v.CertFile, "keyFile": v.KeyFile, "usage": "encipherment"}}}
	}
	if p.Protocol == "tuic" {
		settings["server"] = map[string]any{"certificate": v.CertFile, "private_key": v.KeyFile, "congestion_control": "bbr", "alpn": []string{"h3"}, "zero_rtt_handshake": false, "sni": v.ServerName, "udp_relay_mode": "native"}
		stream = map[string]any{"network": "quic", "security": "tls"}
	}
	if p.Protocol == "hysteria" {
		settings["version"] = 2
		stream["hysteriaSettings"] = map[string]any{"version": 2}
		x := stream["tlsSettings"].(map[string]any)
		x["minVersion"] = "1.3"
		x["alpn"] = []string{"h3"}
	}
	body := map[string]any{"remark": "aria:" + key, "enable": true, "listen": "0.0.0.0", "port": p.Port, "protocol": p.Protocol, "settings": marshalString(settings), "streamSettings": marshalString(stream), "sniffing": marshalString(map[string]any{"enabled": false}), "up": 0, "down": 0, "total": 0, "expiryTime": 0, "trafficReset": "never"}
	var added Inbound
	if err := e.call(ctx, "POST", "/panel/api/inbounds/add", body, &added); err != nil {
		return p, err
	}
	p.InboundID, p.Ready = added.ID, true
	return p, nil
}
func directShareLink(p Profile, c map[string]any, origin string) string {
	host, _, _ := publicEndpoint(origin)
	if host == "" {
		return ""
	}
	name := textField(c, "comment")
	if name == "" {
		name = textField(c, "email")
	}
	name += " · " + p.Name
	auth := textField(c, "id")
	q := url.Values{"security": {p.Security}, "type": {p.Network}}
	scheme := p.Protocol
	if p.Security == "tls" || p.Security == "reality" {
		q.Set("sni", p.ServerName)
		q.Set("fp", "chrome")
	}
	if p.Security == "reality" {
		q.Set("pbk", p.PublicKey)
		q.Set("sid", p.ShortID)
		q.Set("spx", "/")
	}
	if p.Protocol == "vless" {
		q.Set("encryption", "none")
		if p.Network == "tcp" {
			q.Set("flow", "xtls-rprx-vision")
		}
	}
	if p.Network == "xhttp" {
		q.Set("path", p.Path)
		q.Set("mode", "auto")
	}
	if p.Network == "grpc" {
		q.Set("serviceName", strings.TrimSuffix(strings.TrimPrefix(p.Path, "/connect/"), "/"))
		q.Set("alpn", "h2")
	}
	if p.Protocol == "trojan" {
		auth = textField(c, "password")
	}
	if p.Protocol == "vmess" {
		b, _ := json.Marshal(map[string]any{"v": "2", "ps": name, "add": host, "port": strconv.Itoa(p.Port), "id": auth, "aid": "0", "scy": "auto", "net": "tcp", "type": "none", "tls": "tls", "sni": p.ServerName, "fp": "chrome"})
		return "vmess://" + base64.StdEncoding.EncodeToString(b)
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(p.Port)), User: url.User(auth), RawQuery: q.Encode(), Fragment: name}
	switch p.Protocol {
	case "tuic":
		u.User = url.UserPassword(auth, textField(c, "password"))
		u.RawQuery = url.Values{"sni": {p.ServerName}, "alpn": {"h3"}, "congestion_control": {"bbr"}, "udp_relay_mode": {"native"}}.Encode()
	case "hysteria":
		u.Scheme = "hysteria2"
		u.User = url.User(textField(c, "auth"))
		u.RawQuery = url.Values{"sni": {p.ServerName}}.Encode()
	case "shadowsocks":
		u.Scheme = "ss"
		u.User = url.User(base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:" + textField(c, "password"))))
		u.RawQuery = ""
	}
	return u.String()
}
func singBoxOutbound(p Profile, c map[string]any, origin string) map[string]any {
	return singBoxOutboundWithEndpoint(p, c, origin, LinkEndpoint{})
}

func singBoxOutboundWithEndpoint(p Profile, c map[string]any, origin string, defaults LinkEndpoint) map[string]any {
	if p.Network == "xhttp" || (p.Protocol == "shadowsocks" && !p.Direct) {
		// sing-box's Shadowsocks outbound has no generic WS transport.
		return nil
	}
	host, port, sec := publicEndpoint(origin)
	httpHost, tlsName := host, host
	if !p.Direct {
		host, port, httpHost, tlsName, sec = connectionEndpoint(origin, defaults, p.Endpoint)
	}
	pn, _ := strconv.Atoi(port)
	o := map[string]any{"type": p.Protocol, "tag": p.Key, "server": host, "server_port": pn}
	if p.Direct {
		sec = p.Security
		o["server_port"] = p.Port
	}
	if p.Protocol == "hysteria" {
		o["type"] = "hysteria2"
	}
	switch p.Protocol {
	case "vless":
		o["uuid"] = textField(c, "id")
		if p.Direct && p.Network == "tcp" {
			o["flow"] = "xtls-rprx-vision"
		}
	case "vmess":
		o["uuid"] = textField(c, "id")
		o["security"] = "auto"
		o["alter_id"] = 0
	case "trojan":
		o["password"] = textField(c, "password")
	case "shadowsocks":
		o["method"] = "chacha20-ietf-poly1305"
		o["password"] = textField(c, "password")
	case "tuic":
		o["uuid"] = textField(c, "id")
		o["password"] = textField(c, "password")
		o["congestion_control"] = "bbr"
		o["udp_relay_mode"] = "native"
	case "hysteria":
		o["password"] = textField(c, "auth")
	}
	if sec == "tls" || sec == "reality" {
		sni := tlsName
		if p.Direct {
			sni = p.ServerName
		}
		t := map[string]any{"enabled": true, "server_name": sni}
		if sec == "reality" {
			t["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
			t["reality"] = map[string]any{"enabled": true, "public_key": p.PublicKey, "short_id": p.ShortID}
		} else if p.Network == "ws" {
			t["alpn"] = []string{"http/1.1"}
		} else if p.Network == "grpc" {
			t["alpn"] = []string{"h2"}
		} else if p.UDP {
			t["alpn"] = []string{"h3"}
		}
		o["tls"] = t
	}
	if p.Network == "ws" {
		o["transport"] = map[string]any{"type": "ws", "path": p.Path, "headers": map[string]string{"Host": httpHost}}
	}
	if p.Network == "grpc" {
		o["transport"] = map[string]any{"type": "grpc", "service_name": strings.TrimSuffix(strings.TrimPrefix(p.Path, "/connect/"), "/")}
	}
	return o
}
func certFingerprint(v VPSConfig) [32]byte {
	b, _ := os.ReadFile(v.CertFile)
	k, _ := os.ReadFile(v.KeyFile)
	return sha256.Sum256(append(b, k...))
}
func (a *App) watchCertificate(ctx context.Context) {
	if a.cfg.Mode != "vps" || envDefault("ATIA_RELOAD_ON_CERT_CHANGE", "false") != "true" {
		return
	}
	var last [32]byte
	observed := false
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		v := a.vpsSnapshot()
		if v.certificateError() == nil {
			next := certFingerprint(v)
			if observed && last != next {
				select {
				case a.coreRestart <- struct{}{}:
				default:
				}
				return
			}
			last, observed = next, true
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func certificatePEM(data []byte) *x509.Certificate {
	b, _ := pem.Decode(data)
	if b == nil {
		return nil
	}
	c, _ := x509.ParseCertificate(b.Bytes)
	return c
}
