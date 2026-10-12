package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// These are credentials for administrator-owned additional exit servers.
// A CDN entry for the same origin is not an independent exit server.
type GameExit struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Enabled         bool           `json:"enabled"`
	NativeValidated bool           `json:"nativeValidated"`
	Outbound        map[string]any `json:"outbound"`
}

func (a *App) loadGameExits() error {
	a.gameExits = []GameExit{}
	b, e := os.ReadFile(filepath.Join(a.cfg.DataDir, "aria-game-exits.json"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if json.Unmarshal(b, &a.gameExits) != nil || len(a.gameExits) > 8 {
		return fmt.Errorf("game exits state invalid")
	}
	for _, v := range a.gameExits {
		if !regexp.MustCompile(`^gx-[a-f0-9]{16}$`).MatchString(v.ID) || validateGameOutbound(v.Outbound) != nil {
			return fmt.Errorf("game exit invalid")
		}
	}
	return nil
}
func (a *App) gameExitSnapshot() []GameExit {
	a.gameMu.Lock()
	defer a.gameMu.Unlock()
	return append([]GameExit{}, a.gameExits...)
}
func (a *App) gameExitsPublic() any {
	out := []any{}
	for _, v := range a.gameExitSnapshot() {
		out = append(out, map[string]any{"id": v.ID, "name": v.Name, "enabled": v.Enabled, "type": v.Outbound["type"], "server": v.Outbound["server"], "port": v.Outbound["server_port"], "nativeValidated": v.NativeValidated})
	}
	return map[string]any{"exits": out, "note": "اتصال‌های سرورهای مستقل خودت را وارد کن؛ این کار سرور نمی‌خرد یا ایجاد نمی‌کند. اعتبار ساختار با اتصال واقعی یا پذیرش IP در بازی یکسان نیست."}
}
func (a *App) gamingAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/gaming/catalog" && r.Method == "GET" {
		jsonReply(w, 200, map[string]any{"games": gamePresets})
		return
	}
	if r.URL.Path == "/api/gaming/exits" && r.Method == "GET" {
		jsonReply(w, 200, a.gameExitsPublic())
		return
	}
	if r.URL.Path == "/api/gaming/exits/backup" && r.Method == "GET" {
		jsonReply(w, 200, map[string]any{"version": 1, "exits": a.gameExitSnapshot()})
		return
	}
	if r.Method != "POST" {
		apiError(w, 405, "عملیات معتبر نیست")
		return
	}
	a.gameMu.Lock()
	defer a.gameMu.Unlock()
	list := append([]GameExit{}, a.gameExits...)
	if r.URL.Path == "/api/gaming/exits" {
		var in struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			URI  string `json:"uri"`
		}
		if readBody(w, r, &in, 16384) != nil || !safeLabel(in.Name, 64) {
			apiError(w, 400, "نام و لینک اتصال معتبر لازم است")
			return
		}
		out, e := parseGameURI(strings.TrimSpace(in.URI))
		if e != nil {
			apiError(w, 400, e.Error())
			return
		}
		if e = validateGameOutbound(out); e != nil {
			apiError(w, 400, e.Error())
			return
		}
		validated, e := a.checkGameOutbound(r.Context(), out)
		if e != nil {
			apiError(w, 400, e.Error())
			return
		}
		found := false
		for i, v := range list {
			if v.ID == in.ID {
				list[i] = GameExit{v.ID, in.Name, v.Enabled, validated, out}
				found = true
				break
			}
		}
		if !found {
			if in.ID != "" {
				apiError(w, 404, "خروجی پیدا نشد")
				return
			}
			if len(list) >= 8 {
				apiError(w, 400, "حداکثر ۸ سرور خروجی")
				return
			}
			list = append(list, GameExit{"gx-" + randomToken(8), in.Name, true, validated, out})
		}
	} else if r.URL.Path == "/api/gaming/exits/backup" {
		var in struct {
			Version int        `json:"version"`
			Exits   []GameExit `json:"exits"`
		}
		if readBody(w, r, &in, 1<<20) != nil || in.Version != 1 || len(in.Exits) > 8 {
			apiError(w, 400, "بکاپ خروجی معتبر نیست")
			return
		}
		for _, v := range in.Exits {
			if !safeLabel(v.Name, 64) || !regexp.MustCompile(`^gx-[a-f0-9]{16}$`).MatchString(v.ID) {
				apiError(w, 400, "هویت خروجی معتبر نیست")
				return
			}
			if e := validateGameOutbound(v.Outbound); e != nil {
				apiError(w, 400, e.Error())
				return
			}
			found := false
			for _, old := range list {
				if old.ID == v.ID {
					found = true
				}
			}
			if found {
				continue
			}
			ok, e := a.checkGameOutbound(r.Context(), v.Outbound)
			if e != nil {
				apiError(w, 400, e.Error())
				return
			}
			v.NativeValidated = ok
			list = append(list, v)
		}
		if len(list) > 8 {
			apiError(w, 400, "سقف خروجی‌ها پر است")
			return
		}
	} else {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/gaming/exits/"), "/")
		if len(parts) != 2 {
			apiError(w, 404, "خروجی پیدا نشد")
			return
		}
		found := false
		for i, v := range list {
			if v.ID != parts[0] {
				continue
			}
			found = true
			switch parts[1] {
			case "delete":
				list = append(list[:i], list[i+1:]...)
			case "toggle":
				list[i].Enabled = !list[i].Enabled
			default:
				apiError(w, 404, "عملیات معتبر نیست")
				return
			}
			break
		}
		if !found {
			apiError(w, 404, "خروجی پیدا نشد")
			return
		}
	}
	if e := atomicJSON(a.cfg.DataDir, "aria-game-exits.json", list); e != nil {
		apiError(w, 500, "خروجی ذخیره نشد")
		return
	}
	a.gameExits = list
	// Do not call the lock-taking snapshot method while holding gameMu.
	out := []any{}
	for _, v := range list {
		out = append(out, map[string]any{"id": v.ID, "name": v.Name, "enabled": v.Enabled, "type": v.Outbound["type"], "server": v.Outbound["server"], "port": v.Outbound["server_port"], "nativeValidated": v.NativeValidated})
	}
	jsonReply(w, 200, map[string]any{"exits": out})
}
func (a *App) checkGameOutbound(ctx context.Context, out map[string]any) (bool, error) {
	if _, e := os.Stat(a.singBoxBin()); e != nil {
		return false, nil
	}
	dir, e := os.MkdirTemp(a.cfg.DataDir, ".game-check-")
	if e != nil {
		return false, e
	}
	defer os.RemoveAll(dir)
	copyOut := cloneGameOutbound(out)
	copyOut["tag"] = "test-exit"
	copyOut["domain_resolver"] = "bootstrap"
	config := map[string]any{"dns": map[string]any{"servers": []any{map[string]string{"type": "local", "tag": "bootstrap"}}}, "outbounds": []any{copyOut}, "route": map[string]string{"default_domain_resolver": "bootstrap"}}
	b, _ := json.Marshal(config)
	path := filepath.Join(dir, "client.json")
	if os.WriteFile(path, b, 0600) != nil {
		return false, fmt.Errorf("بررسی خروجی آماده نشد")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if exec.CommandContext(ctx, a.singBoxBin(), "check", "-c", path).Run() != nil {
		return false, fmt.Errorf("sing-box ساختار این اتصال را نپذیرفت؛ لینک و نوع انتقال را بررسی کن")
	}
	return true, nil
}
func cloneGameOutbound(v map[string]any) map[string]any {
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}
func parseGameURI(raw string) (map[string]any, error) {
	if len(raw) > 12288 {
		return nil, fmt.Errorf("لینک بیش از حد بزرگ است")
	}
	if strings.HasPrefix(raw, "vmess://") {
		encoded := strings.TrimPrefix(raw, "vmess://")
		var b []byte
		var e error
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			b, e = enc.DecodeString(encoded)
			if e == nil {
				break
			}
		}
		var v map[string]any
		if e != nil || json.Unmarshal(b, &v) != nil {
			return nil, fmt.Errorf("VMess معتبر نیست")
		}
		port, e := strconv.Atoi(fmt.Sprint(v["port"]))
		if e != nil {
			return nil, fmt.Errorf("پورت VMess معتبر نیست")
		}
		out := map[string]any{"type": "vmess", "server": fmt.Sprint(v["add"]), "server_port": port, "uuid": fmt.Sprint(v["id"]), "security": "auto", "alter_id": 0, "packet_encoding": "xudp"}
		if aid := fmt.Sprint(v["aid"]); aid != "0" && aid != "<nil>" && aid != "" {
			return nil, fmt.Errorf("VMess باید alterId صفر باشد")
		}
		if v["tls"] == "tls" {
			sni, _ := v["sni"].(string)
			if sni == "" {
				sni = fmt.Sprint(v["add"])
			}
			out["tls"] = map[string]any{"enabled": true, "server_name": sni}
		}
		network, _ := v["net"].(string)
		if network == "ws" {
			out["transport"] = map[string]any{"type": "ws", "path": v["path"], "headers": map[string]any{"Host": v["host"]}}
		} else if network != "" && network != "tcp" {
			return nil, fmt.Errorf("برای VMess این فرم، TCP یا WS لازم است")
		}
		return out, nil
	}
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || u.User == nil {
		return nil, fmt.Errorf("لینک اتصال معتبر لازم است")
	}
	q := u.Query()
	if q.Get("insecure") == "1" || q.Get("allowInsecure") == "1" {
		return nil, fmt.Errorf("گواهی باید معتبر باشد؛ insecure پذیرفته نمی‌شود")
	}
	port := 443
	if u.Port() != "" {
		port, e = parsePort(u.Port())
		if e != nil {
			return nil, e
		}
	}
	typ := u.Scheme
	if typ == "hy2" {
		typ = "hysteria2"
	}
	if typ != "vless" && typ != "trojan" && typ != "hysteria2" && typ != "tuic" && typ != "anytls" {
		return nil, fmt.Errorf("لینک VLESS، VMess، Trojan، Hysteria2، TUIC یا AnyTLS لازم است")
	}
	out := map[string]any{"type": typ, "server": u.Hostname(), "server_port": port}
	if typ == "vless" || typ == "tuic" {
		out["uuid"] = u.User.Username()
	} else {
		out["password"] = u.User.Username()
	}
	if typ == "tuic" {
		password, _ := u.User.Password()
		out["password"] = password
		out["congestion_control"] = "bbr"
		out["udp_relay_mode"] = "native"
	}
	if typ == "vless" {
		out["packet_encoding"] = "xudp"
		if flow := q.Get("flow"); flow != "" {
			if flow != "xtls-rprx-vision" {
				return nil, fmt.Errorf("flow پشتیبانی نمی‌شود")
			}
			out["flow"] = flow
		}
	}
	security := q.Get("security")
	if typ != "vless" && security == "" {
		security = "tls"
	}
	if security == "tls" || security == "reality" {
		sni := q.Get("sni")
		if sni == "" {
			sni = u.Hostname()
		}
		tls := map[string]any{"enabled": true, "server_name": sni}
		if security == "reality" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
			tls["reality"] = map[string]any{"enabled": true, "public_key": q.Get("pbk"), "short_id": q.Get("sid")}
		}
		out["tls"] = tls
	} else if security != "" && security != "none" {
		return nil, fmt.Errorf("نوع امنیت پشتیبانی نمی‌شود")
	}
	network := q.Get("type")
	if typ == "vless" || typ == "trojan" {
		switch network {
		case "", "tcp":
		case "ws":
			host := q.Get("host")
			if host == "" {
				host = u.Hostname()
			}
			path := q.Get("path")
			if path == "" {
				path = "/"
			}
			out["transport"] = map[string]any{"type": "ws", "path": path, "headers": map[string]any{"Host": host}}
		case "grpc":
			out["transport"] = map[string]any{"type": "grpc", "service_name": q.Get("serviceName")}
		default:
			return nil, fmt.Errorf("این انتقال در خروجی بازی sing-box پشتیبانی نمی‌شود")
		}
	}
	if typ == "hysteria2" && q.Get("obfs") != "" {
		if q.Get("obfs") != "salamander" {
			return nil, fmt.Errorf("obfs پشتیبانی نمی‌شود")
		}
		out["obfs"] = map[string]any{"type": "salamander", "password": q.Get("obfs-password")}
	}
	return out, nil
}
func gameKeysOnly(v map[string]any, allowed string) bool {
	set := map[string]bool{}
	for _, k := range strings.Fields(allowed) {
		set[k] = true
	}
	for k := range v {
		if !set[k] {
			return false
		}
	}
	return true
}
func validateGameOutbound(v map[string]any) error {
	bad := fmt.Errorf("ساختار اتصال یا گزینهٔ ناامن/ناشناخته پذیرفته نشد")
	if !gameKeysOnly(v, "type server server_port uuid password security alter_id flow tls transport packet_encoding congestion_control udp_relay_mode obfs") {
		return bad
	}
	typ, _ := v["type"].(string)
	switch typ {
	case "vless", "vmess", "trojan", "hysteria2", "tuic", "anytls":
	default:
		return bad
	}
	host, _ := v["server"].(string)
	if ip := net.ParseIP(host); ip != nil {
		if !publicNetworkIP(ip) {
			return fmt.Errorf("سرور خروجی باید IP عمومی باشد")
		}
	} else if !validHostname(host) {
		return fmt.Errorf("دامنهٔ سرور خروجی معتبر نیست")
	}
	port, e := parsePort(fmt.Sprint(v["server_port"]))
	if e != nil || port < 1 {
		return bad
	}
	if typ == "vless" || typ == "vmess" || typ == "tuic" {
		id, _ := v["uuid"].(string)
		if !regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`).MatchString(id) {
			return fmt.Errorf("UUID معتبر لازم است")
		}
	}
	if typ != "vless" && typ != "vmess" {
		password, _ := v["password"].(string)
		if password == "" || len(password) > 512 {
			return fmt.Errorf("رمز اتصال معتبر لازم است")
		}
	}
	for _, field := range []string{"tls", "transport", "obfs"} {
		if _, exists := v[field]; exists {
			if _, ok := v[field].(map[string]any); !ok {
				return bad
			}
		}
	}
	if t, ok := v["tls"].(map[string]any); ok {
		if !gameKeysOnly(t, "enabled server_name alpn utls reality") || t["enabled"] != true {
			return bad
		}
		if sni, _ := t["server_name"].(string); sni == "" || len(sni) > 253 || strings.ContainsAny(sni, "/\r\n\x00") {
			return bad
		}
		if u, ok := t["utls"].(map[string]any); ok && !gameKeysOnly(u, "enabled fingerprint") {
			return bad
		}
		for _, field := range []string{"utls", "reality"} {
			if _, exists := t[field]; exists {
				if _, ok := t[field].(map[string]any); !ok {
					return bad
				}
			}
		}
		if r, ok := t["reality"].(map[string]any); ok && !gameKeysOnly(r, "enabled public_key short_id") {
			return bad
		}
	}
	if t, ok := v["transport"].(map[string]any); ok {
		if t["type"] == "ws" {
			if !gameKeysOnly(t, "type path headers") {
				return bad
			}
			if h, ok := t["headers"].(map[string]any); ok && !gameKeysOnly(h, "Host") {
				return bad
			}
		} else if t["type"] == "grpc" {
			if !gameKeysOnly(t, "type service_name") {
				return bad
			}
		} else {
			return bad
		}
	}
	if o, ok := v["obfs"].(map[string]any); ok && (!gameKeysOnly(o, "type password") || o["type"] != "salamander") {
		return bad
	}
	return nil
}
