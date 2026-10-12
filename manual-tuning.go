package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type ManualTuning struct {
	DownloadEndpoint    LinkEndpoint `json:"downloadEndpoint,omitempty"`
	BufferedPosts       int          `json:"bufferedPosts,omitempty"`
	Fingerprint         string       `json:"fingerprint,omitempty"`
	ALPN                string       `json:"alpn,omitempty"`
	TCPUserTimeoutMS    int          `json:"tcpUserTimeoutMS,omitempty"`
	KeepAliveIdle       int          `json:"keepAliveIdle,omitempty"`
	KeepAliveInterval   int          `json:"keepAliveInterval,omitempty"`
	MuxConcurrency      int          `json:"muxConcurrency,omitempty"`
	MaxEarlyData        int          `json:"maxEarlyData,omitempty"`
	XHTTPMaxConnections int          `json:"xhttpMaxConnections,omitempty"`
	PostMaxKB           int          `json:"postMaxKB,omitempty"`
	PostIntervalMS      int          `json:"postIntervalMS,omitempty"`
	NoGRPCHeader        bool         `json:"noGRPCHeader,omitempty"`
	Sniffing            string       `json:"sniffing,omitempty"`
}

func validateTuning(t ManualTuning, network string) error {
	if t.BufferedPosts < 0 || t.BufferedPosts > 256 {
		return fmt.Errorf("حداکثر POST بافرشده: صفر تا ۲۵۶")
	}
	if network != "xhttp" && (t.DownloadEndpoint != (LinkEndpoint{}) || t.BufferedPosts != 0) {
		return fmt.Errorf("مسیر دانلود جدا و بافر POST فقط برای XHTTP هستند")
	}
	if t.TCPUserTimeoutMS < 0 || t.TCPUserTimeoutMS > 300000 || (t.TCPUserTimeoutMS > 0 && t.TCPUserTimeoutMS < 1000) {
		return fmt.Errorf("TCP User Timeout: صفر یا ۱۰۰۰ تا ۳۰۰۰۰۰ میلی‌ثانیه")
	}
	switch t.Fingerprint {
	case "", "chrome", "firefox", "safari", "edge", "randomized":
	default:
		return fmt.Errorf("Fingerprint معتبر نیست")
	}
	switch t.ALPN {
	case "", "auto", "http/1.1", "h2", "h2,http/1.1":
	default:
		return fmt.Errorf("ALPN معتبر نیست")
	}
	if network == "ws" && t.ALPN != "" && t.ALPN != "auto" && t.ALPN != "http/1.1" {
		return fmt.Errorf("WebSocket این پنل از HTTP/1.1 استفاده می‌کند")
	}
	if t.KeepAliveIdle < 0 || t.KeepAliveIdle > 600 || t.KeepAliveInterval < 0 || t.KeepAliveInterval > 300 || (t.KeepAliveIdle > 0 && t.KeepAliveIdle < 10) || (t.KeepAliveInterval > 0 && t.KeepAliveInterval < 5) {
		return fmt.Errorf("Keepalive Idle: صفر یا ۱۰ تا ۶۰۰؛ Interval: صفر یا ۵ تا ۳۰۰ ثانیه")
	}
	if t.MuxConcurrency < 0 || t.MuxConcurrency > 32 || (network != "ws" && t.MuxConcurrency != 0) {
		return fmt.Errorf("Mux کلاسیک فقط روی WS، صفر=خاموش؛ حداکثر ۳۲")
	}
	if t.MaxEarlyData < 0 || t.MaxEarlyData > 4096 || (network != "ws" && t.MaxEarlyData != 0) {
		return fmt.Errorf("Early Data فقط روی WS، از صفر تا ۴۰۹۶ بایت")
	}
	if t.XHTTPMaxConnections < 0 || t.XHTTPMaxConnections > 16 || t.PostMaxKB < 0 || t.PostMaxKB > 16384 || t.PostIntervalMS < 0 || t.PostIntervalMS > 1000 {
		return fmt.Errorf("پارامترهای XHTTP خارج از بازه‌اند")
	}
	if network != "xhttp" && (t.XHTTPMaxConnections != 0 || t.PostMaxKB != 0 || t.PostIntervalMS != 0 || t.NoGRPCHeader) {
		return fmt.Errorf("تنظیمات XHTTP فقط برای همان انتقال هستند")
	}
	if t.Sniffing != "" && t.Sniffing != "route" && t.Sniffing != "off" {
		return fmt.Errorf("حالت Sniffing معتبر نیست")
	}
	return nil
}
func applyManualTuning(p Profile, stream map[string]any, client bool) {
	t := p.Tuning
	sock := map[string]any{}
	if t.TCPUserTimeoutMS > 0 {
		sock["tcpUserTimeout"] = t.TCPUserTimeoutMS
	}
	if t.KeepAliveIdle > 0 {
		sock["tcpKeepAliveIdle"] = t.KeepAliveIdle
	}
	if t.KeepAliveInterval > 0 {
		sock["tcpKeepAliveInterval"] = t.KeepAliveInterval
	}
	if len(sock) > 0 {
		stream["sockopt"] = sock
	}
	if p.Network == "ws" && t.MaxEarlyData > 0 {
		ws, _ := stream["wsSettings"].(map[string]any)
		if ws != nil {
			ws["maxEarlyData"] = t.MaxEarlyData
			ws["earlyDataHeaderName"] = "Sec-WebSocket-Protocol"
		}
	}
	if p.Network == "xhttp" {
		x, _ := stream["xhttpSettings"].(map[string]any)
		if x == nil {
			return
		}
		extra := map[string]any{}
		if t.PostMaxKB > 0 {
			extra["scMaxEachPostBytes"] = t.PostMaxKB * 1024
		}
		if client && t.PostIntervalMS > 0 {
			extra["scMinPostsIntervalMs"] = t.PostIntervalMS
		}
		if client && t.NoGRPCHeader {
			extra["noGRPCHeader"] = true
		}
		if client && t.XHTTPMaxConnections > 0 {
			extra["xmux"] = map[string]any{"maxConnections": t.XHTTPMaxConnections}
		}
		if !client && t.BufferedPosts > 0 {
			extra["scMaxBufferedPosts"] = t.BufferedPosts
		}
		if client && t.DownloadEndpoint != (LinkEndpoint{}) {
			// Both paths must arrive at this same inbound; credentials and path
			// are shared, but TLS identities can differ between CDN entrances.
			e := t.DownloadEndpoint
			tls, _ := stream["tlsSettings"].(map[string]any)
			downTLS := map[string]any{}
			for k, v := range tls {
				downTLS[k] = v
			}
			if e.SNI != "" {
				downTLS["serverName"] = e.SNI
			}
			port := e.Port
			if port == 0 {
				port = 443
			}
			downHost := e.Host
			if downHost == "" {
				downHost, _ = x["host"].(string)
			}
			download := map[string]any{"address": e.Address, "port": port, "network": "xhttp", "security": "tls", "tlsSettings": downTLS, "xhttpSettings": map[string]any{"path": p.Path, "host": downHost, "mode": p.XHTTPMode}}
			extra["downloadSettings"] = download
		}
		if len(extra) > 0 {
			x["extra"] = extra
		}
	}
	if client {
		tls, _ := stream["tlsSettings"].(map[string]any)
		if tls != nil {
			if t.Fingerprint != "" {
				tls["fingerprint"] = t.Fingerprint
			}
			switch t.ALPN {
			case "h2":
				tls["alpn"] = []string{"h2"}
			case "http/1.1":
				tls["alpn"] = []string{"http/1.1"}
			case "h2,http/1.1":
				tls["alpn"] = []string{"h2", "http/1.1"}
			}
		}
	}
}
func (a *App) previewManual(w http.ResponseWriter, r *http.Request) {
	var m ManualCreate
	if readBody(w, r, &m, 8192) != nil {
		apiError(w, 400, "فرم دستی معتبر نیست")
		return
	}
	m, e := normalizeManual(m)
	if e != nil {
		apiError(w, 400, e.Error())
		return
	}
	if m.Path == "" {
		m.Path = "/connect/preview-config-01/"
	}
	p := m.spec("manual-000000000000").profile()
	c := map[string]any{"id": m.UUID, "password": m.Password, "email": "preview", "comment": "پیش‌نمایش"}
	if c["id"] == "" {
		c["id"] = "00000000-0000-4000-8000-000000000001"
	}
	if c["password"] == "" {
		c["password"] = "preview-only-change-me"
	}
	settings := a.snapshot().Settings
	outbound := xrayCloudOutbound(p, c, settings.PublicURL, settings.Connection)
	client := map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": []any{map[string]any{"tag": "socks-in", "listen": "127.0.0.1", "port": 10808, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": false}}}, "outbounds": []any{outbound}}
	stream := map[string]any{"network": p.Network, "security": "none", p.Network + "Settings": map[string]any{"path": p.Path}}
	if p.Network == "xhttp" {
		stream["xhttpSettings"].(map[string]any)["mode"] = p.XHTTPMode
	}
	applyManualTuning(p, stream, false)
	nativeSettings := map[string]any{"clients": []any{c}, "decryption": "none"}
	if p.Protocol == "shadowsocks" {
		nativeSettings["method"] = p.Method
		nativeSettings["network"] = "tcp"
	}
	server := map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": 20990, "protocol": p.Protocol, "settings": nativeSettings, "streamSettings": stream}}, "outbounds": []any{map[string]any{"protocol": "freedom", "tag": "direct"}}}
	verified := false
	note := "مقادیر اعتبارسنجی شدند؛ باینری Xray برای بررسی نحو در دسترس نیست."
	bin := filepath.Join(a.cfg.EngineDir, "bin", "xray-linux-amd64")
	if _, e := os.Stat(bin); e == nil {
		b, _ := json.Marshal(server)
		f, e := os.CreateTemp("", "aria-preview-*.json")
		if e == nil {
			name := f.Name()
			defer os.Remove(name)
			_ = f.Chmod(0600)
			_, e = f.Write(b)
			_ = f.Close()
			if e == nil {
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				err := exec.CommandContext(ctx, bin, "run", "-test", "-config", name).Run()
				cancel()
				verified = err == nil
				if !verified {
					apiError(w, 400, "Xray این ترکیب را نپذیرفت؛ حالت انتقال و نسخهٔ هسته را بررسی کن")
					return
				}
				note = "نحو کانفیگ با باینری واقعی Xray تأیید شد؛ این آزمون دسترسی شبکهٔ ایران را نمی‌سنجد."
			}
		}
	}
	jsonReply(w, 200, map[string]any{"client": client, "server": server, "verified": verified, "note": note})
}
