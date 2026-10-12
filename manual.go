package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Address is the dial target. Host and SNI keep HTTP routing and certificate
// verification independent of DNS; changing Address does not disable TLS.
type LinkEndpoint struct {
	Address string `json:"address,omitempty"`
	Port    int    `json:"port,omitempty"`
	Host    string `json:"host,omitempty"`
	SNI     string `json:"sni,omitempty"`
}

func normalizeEndpoint(e LinkEndpoint) (LinkEndpoint, error) {
	fields := []*string{&e.Address, &e.Host, &e.SNI}
	for _, field := range fields {
		*field = strings.TrimSpace(*field)
		if *field == "" {
			continue
		}
		if ip := net.ParseIP(*field); ip != nil {
			*field = ip.String()
			continue
		}
		if strings.ContainsAny(*field, ":/?#@[]\\") {
			return e, fmt.Errorf("Address، Host و SNI باید فقط آی‌پی یا نام دامنه باشند؛ بدون https یا پورت")
		}
		u, err := validatePublicURL("https://" + *field)
		if err != nil {
			return e, fmt.Errorf("آی‌پی یا نام دامنه معتبر نیست")
		}
		*field = u.Hostname()
	}
	if e.Port < 0 || e.Port > 65535 {
		return e, fmt.Errorf("پورت عمومی باید بین ۱ و ۶۵۵۳۵ باشد؛ صفر یعنی خودکار")
	}
	return e, nil
}

func connectionEndpoint(origin string, defaults, local LinkEndpoint) (address, port, host, sni, security string) {
	address, port, security = publicEndpoint(origin)
	host, sni = address, address
	for _, e := range []LinkEndpoint{defaults, local} {
		if e.Address != "" {
			address = e.Address
		}
		if e.Port != 0 {
			port = strconv.Itoa(e.Port)
		}
		if e.Host != "" {
			host = e.Host
		}
		if e.SNI != "" {
			sni = e.SNI
		}
	}
	return
}

func xrayCloudOutbound(p Profile, c map[string]any, origin string, defaults LinkEndpoint) map[string]any {
	if p.Direct || (p.Network != "ws" && p.Network != "xhttp") {
		return nil
	}
	address, port, host, sni, security := connectionEndpoint(origin, defaults, p.Endpoint)
	pn, _ := strconv.Atoi(port)
	id := textField(c, "id")
	if id == "" {
		id = textField(c, "uuid")
	}
	user := map[string]any{"id": id}
	if p.Protocol == "vless" {
		user["encryption"] = "none"
	} else {
		user["security"] = "auto"
	}
	settings := map[string]any{"vnext": []any{map[string]any{"address": address, "port": pn, "users": []any{user}}}}
	if p.Protocol == "trojan" {
		settings = map[string]any{"servers": []any{map[string]any{"address": address, "port": pn, "password": textField(c, "password")}}}
	} else if p.Protocol == "shadowsocks" {
		settings = map[string]any{"servers": []any{map[string]any{"address": address, "port": pn, "method": p.Method, "password": textField(c, "password")}}}
	}
	transport := map[string]any{"path": p.Path, "host": host}
	if p.Network == "xhttp" {
		mode := p.XHTTPMode
		if mode == "" {
			mode = "auto"
		}
		transport["mode"] = mode
	}
	stream := map[string]any{"network": p.Network, "security": security, p.Network + "Settings": transport}
	if security == "tls" {
		alpn := []string{"http/1.1"}
		if p.Network == "xhttp" {
			alpn = []string{"h2", "http/1.1"}
		}
		stream["tlsSettings"] = map[string]any{"serverName": sni, "fingerprint": "chrome", "alpn": alpn}
	}
	applyManualTuning(p, stream, true)
	out := map[string]any{"tag": p.Key, "protocol": p.Protocol, "settings": settings, "streamSettings": stream}
	if p.Tuning.MuxConcurrency > 0 {
		out["mux"] = map[string]any{"enabled": true, "concurrency": p.Tuning.MuxConcurrency}
	}
	return out
}

type ManualCreate struct {
	Tuning   ManualTuning `json:"tuning,omitempty"`
	Protocol string       `json:"protocol"`
	Network  string       `json:"network"`
	Path     string       `json:"path"`
	Mode     string       `json:"mode"`
	Method   string       `json:"method,omitempty"`
	Endpoint LinkEndpoint `json:"endpoint"`
	UUID     string       `json:"uuid,omitempty"`
	Password string       `json:"password,omitempty"`
}

// Only public connection metadata goes in the native remark. Client secrets
// stay in 3x-ui's clients table and in the existing private backup format.
type manualSpec struct {
	Tuning   ManualTuning `json:"tuning,omitempty"`
	Key      string       `json:"key"`
	Protocol string       `json:"protocol"`
	Network  string       `json:"network"`
	Path     string       `json:"path"`
	Mode     string       `json:"mode,omitempty"`
	Method   string       `json:"method,omitempty"`
	Endpoint LinkEndpoint `json:"endpoint,omitempty"`
}

var manualKeyPattern = regexp.MustCompile(`^manual-[a-f0-9]{12}$`)
var connectPathPattern = regexp.MustCompile(`^/connect/[a-z0-9-]{8,80}/$`)
var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

const defaultShadowsocksMethod = "chacha20-ietf-poly1305"

func validShadowsocksMethod(method string) bool {
	return method == defaultShadowsocksMethod || method == "aes-128-gcm" || method == "aes-256-gcm"
}

func cloudShadowsocksSettingsMatch(ib Inbound, method string) bool {
	var settings map[string]any
	return validShadowsocksMethod(method) && nestedJSON(ib.Settings, &settings) == nil &&
		textField(settings, "method") == method && textField(settings, "network") == "tcp"
}

func validClientPassword(s string) bool {
	if len(s) < 12 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func normalizeManual(m ManualCreate) (ManualCreate, error) {
	var tuneErr error
	m.Tuning.DownloadEndpoint, tuneErr = normalizeEndpoint(m.Tuning.DownloadEndpoint)
	if tuneErr != nil {
		return m, tuneErr
	}
	if m.Tuning.DownloadEndpoint != (LinkEndpoint{}) && m.Tuning.DownloadEndpoint.Address == "" {
		return m, fmt.Errorf("Address برای دانلود جدا لازم است")
	}
	m.Protocol = strings.ToLower(strings.TrimSpace(m.Protocol))
	m.Network = strings.ToLower(strings.TrimSpace(m.Network))
	m.Path = strings.TrimSpace(m.Path)
	m.Mode = strings.TrimSpace(m.Mode)
	m.Method = strings.ToLower(strings.TrimSpace(m.Method))
	m.UUID = strings.ToLower(strings.TrimSpace(m.UUID))
	if m.Protocol != "vless" && m.Protocol != "vmess" && m.Protocol != "trojan" && m.Protocol != "shadowsocks" {
		return m, fmt.Errorf("در ساخت دستی وب، VLESS، VMess، Trojan یا Shadowsocks را انتخاب کن")
	}
	if m.Network != "ws" && m.Network != "xhttp" {
		return m, fmt.Errorf("انتقال دستی وب باید WebSocket یا XHTTP باشد")
	}
	if err := validateTuning(m.Tuning, m.Network); err != nil {
		return m, err
	}
	if m.Path != "" && !connectPathPattern.MatchString(m.Path) {
		return m, fmt.Errorf("Path باید مانند /connect/my-config-01/ باشد: ۸ تا ۸۰ حرف کوچک، عدد یا خط تیره")
	}
	if m.Network == "xhttp" {
		if m.Mode == "" {
			m.Mode = "auto"
		}
		if m.Mode != "auto" && m.Mode != "packet-up" && m.Mode != "stream-up" {
			return m, fmt.Errorf("حالت XHTTP باید auto، packet-up یا stream-up باشد")
		}
		if m.Mode == "packet-up" && m.Tuning.NoGRPCHeader {
			return m, fmt.Errorf("No gRPC Header برای XHTTP جریان است؛ در packet-up خاموش بگذار")
		}
	} else if m.Mode != "" {
		return m, fmt.Errorf("Mode فقط برای XHTTP است")
	}
	passwordProtocol := m.Protocol == "trojan" || m.Protocol == "shadowsocks"
	if m.Protocol == "shadowsocks" {
		if m.Method == "" {
			m.Method = defaultShadowsocksMethod
		}
		if !validShadowsocksMethod(m.Method) {
			return m, fmt.Errorf("روش Shadowsocks باید chacha20-ietf-poly1305، aes-128-gcm یا aes-256-gcm باشد")
		}
	} else if m.Method != "" {
		return m, fmt.Errorf("Method فقط برای Shadowsocks است")
	}
	if m.UUID != "" && (passwordProtocol || !uuidPattern.MatchString(m.UUID)) {
		return m, fmt.Errorf("UUID معتبر فقط برای VLESS یا VMess وارد کن")
	}
	if m.Password != "" && (!passwordProtocol || !validClientPassword(m.Password)) {
		return m, fmt.Errorf("رمز دلخواه برای Trojan یا Shadowsocks است: ۱۲ تا ۱۲۸ نویسهٔ انگلیسی قابل چاپ، بدون فاصله")
	}
	var err error
	m.Endpoint, err = normalizeEndpoint(m.Endpoint)
	return m, err
}

func (m ManualCreate) spec(key string) manualSpec {
	return manualSpec{Tuning: m.Tuning, Key: key, Protocol: m.Protocol, Network: m.Network, Path: m.Path, Mode: m.Mode, Method: m.Method, Endpoint: m.Endpoint}
}

func (s manualSpec) profile() Profile {
	network := "WebSocket"
	if s.Network == "xhttp" {
		network = "XHTTP"
	}
	protocol := strings.ToUpper(s.Protocol)
	if s.Protocol == "vmess" {
		protocol = "VMess"
	} else if s.Protocol == "trojan" {
		protocol = "Trojan"
	} else if s.Protocol == "shadowsocks" {
		protocol = "Shadowsocks"
	}
	return Profile{Tuning: s.Tuning, Manual: true, Endpoint: s.Endpoint, XHTTPMode: s.Mode, Method: s.Method, Key: s.Key, Name: protocol + " · " + network + " · دستی", Protocol: s.Protocol, Network: s.Network, Path: s.Path, Port: 20910, Security: "none", Available: true}
}

func specFromProfile(p Profile) (manualSpec, error) {
	m, err := normalizeManual(ManualCreate{Tuning: p.Tuning, Protocol: p.Protocol, Network: p.Network, Path: p.Path, Mode: p.XHTTPMode, Method: p.Method, Endpoint: p.Endpoint})
	if err != nil || !p.Manual || p.Direct || !manualKeyPattern.MatchString(p.Key) || m.Path == "" {
		return manualSpec{}, fmt.Errorf("الگوی دستی معتبر نیست")
	}
	return m.spec(p.Key), nil
}

func manualRemark(s manualSpec) string {
	b, _ := json.Marshal(s)
	return "aria:manual:" + base64.RawURLEncoding.EncodeToString(b)
}

func manualInboundProfile(ib Inbound) (Profile, bool) {
	if !strings.HasPrefix(ib.Remark, "aria:manual:") || len(ib.Remark) > 4096 {
		return Profile{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ib.Remark, "aria:manual:"))
	var spec manualSpec
	if err != nil || json.Unmarshal(b, &spec) != nil {
		return Profile{}, false
	}
	p := spec.profile()
	canonical, err := specFromProfile(p)
	if err != nil || canonical != spec {
		return Profile{}, false
	}
	var stream map[string]any
	if nestedJSON(ib.Stream, &stream) != nil || textField(stream, "network") != p.Network || textField(stream, "security") != "none" || ib.Protocol != p.Protocol || ib.Port < 20800 || ib.Port > 30000 || (ib.Listen != "127.0.0.1" && ib.Listen != "localhost") {
		return Profile{}, false
	}
	transport, _ := stream[p.Network+"Settings"].(map[string]any)
	if textField(transport, "path") != p.Path || (p.Network == "xhttp" && textField(transport, "mode") != p.XHTTPMode) {
		return Profile{}, false
	}
	if p.Protocol == "shadowsocks" && !cloudShadowsocksSettingsMatch(ib, p.Method) {
		return Profile{}, false
	}
	p.Port, p.InboundID, p.Ready = ib.Port, ib.ID, ib.Enable
	return p, true
}

func (a *App) addCloudProfile(ctx context.Context, e *EngineClient, p Profile, all []Inbound) (Profile, error) {
	used := map[int]bool{panelPort: true, a.cfg.Port: true}
	for _, ib := range all {
		used[ib.Port] = true
		if other, ok := inboundProfile(ib); ok && !other.Direct && other.Path == p.Path {
			return p, fmt.Errorf("این Path قبلاً برای اتصال دیگری استفاده شده است")
		}
	}
	for used[p.Port] {
		p.Port++
		if p.Port > 30000 {
			return p, fmt.Errorf("no internal port available")
		}
	}
	stream := map[string]any{"network": p.Network, "security": "none", p.Network + "Settings": map[string]any{"path": p.Path, "headers": map[string]any{}}}
	if p.Network == "xhttp" {
		mode := p.XHTTPMode
		if mode == "" {
			mode = "auto"
		}
		stream["xhttpSettings"] = map[string]any{"path": p.Path, "mode": mode}
	}
	applyManualTuning(p, stream, false)
	remark := "aria:" + p.Key
	if p.Manual {
		spec, err := specFromProfile(p)
		if err != nil {
			return p, err
		}
		remark = manualRemark(spec)
	}
	settings := map[string]any{"clients": []any{}, "decryption": "none", "fallbacks": []any{}}
	if p.Protocol == "shadowsocks" {
		if !validShadowsocksMethod(p.Method) {
			return p, fmt.Errorf("روش Shadowsocks معتبر نیست")
		}
		// Web hosts expose an HTTP origin. Keep native UDP off this inbound.
		settings = map[string]any{"clients": []any{}, "method": p.Method, "network": "tcp"}
	}
	host, _, _ := publicEndpoint(a.snapshot().Settings.PublicURL)
	body := map[string]any{"remark": remark, "enable": true, "listen": "127.0.0.1", "port": p.Port, "protocol": p.Protocol, "settings": marshalString(settings), "streamSettings": marshalString(stream), "sniffing": marshalString(map[string]any{"enabled": true, "destOverride": []string{"http", "tls"}, "routeOnly": false}), "up": 0, "down": 0, "total": 0, "expiryTime": 0, "trafficReset": "never", "shareAddrStrategy": "custom", "shareAddr": host}
	if p.Tuning.Sniffing != "" {
		body["sniffing"] = marshalString(map[string]any{"enabled": p.Tuning.Sniffing != "off", "destOverride": []string{"http", "tls"}, "routeOnly": true})
	}
	var added Inbound
	if err := e.call(ctx, "POST", "/panel/api/inbounds/add", body, &added); err != nil {
		return p, err
	}
	p.InboundID, p.Ready = added.ID, true
	return p, nil
}

func (a *App) ensureManualProfile(ctx context.Context, e *EngineClient, spec manualSpec) (Profile, error) {
	if spec.Key == "" {
		spec.Key = "manual-" + randomToken(6)
	}
	if spec.Path == "" {
		spec.Path = "/connect/" + spec.Key + "/"
	}
	p := spec.profile()
	canonical, err := specFromProfile(p)
	if err != nil || canonical != spec {
		return p, fmt.Errorf("الگوی دستی معتبر نیست")
	}
	all, err := e.inbounds(ctx)
	if err != nil {
		return p, err
	}
	for _, ib := range all {
		other, ok := inboundProfile(ib)
		if !ok || other.Direct || (other.Key != p.Key && other.Path != p.Path) {
			continue
		}
		existing, err := specFromProfile(other)
		// A repeated manual form can add a client to the same inbound. During
		// restore a saved key must identify exactly the same connection.
		if other.Key != p.Key {
			existing.Key = spec.Key
		}
		if err != nil || existing != spec {
			return p, fmt.Errorf("این Path یا کلید برای تنظیمات دیگری رزرو شده است")
		}
		if !other.Ready {
			return p, fmt.Errorf("اینباند دستی خاموش است")
		}
		return other, nil
	}
	return a.addCloudProfile(ctx, e, p, all)
}

func manualCredentialAvailable(ctx context.Context, e *EngineClient, m ManualCreate) error {
	if m.UUID == "" && m.Password == "" {
		return nil
	}
	all, err := e.inbounds(ctx)
	if err != nil {
		return err
	}
	for _, ib := range all {
		var settings struct {
			Clients []map[string]any `json:"clients"`
		}
		if err := nestedJSON(ib.Settings, &settings); err != nil {
			return err
		}
		for _, c := range settings.Clients {
			if (m.UUID != "" && strings.EqualFold(textField(c, "id"), m.UUID)) || (m.Password != "" && (ib.Protocol == "trojan" || ib.Protocol == "shadowsocks") && textField(c, "password") == m.Password) {
				return fmt.Errorf("این UUID یا رمز قبلاً استفاده شده است؛ برای کاربر جدید یک مقدار متفاوت وارد کن")
			}
		}
	}
	return nil
}
