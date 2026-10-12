package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const singBoxVersion = "1.14.3"
const managedStatsPort = 24099

type CoreService struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Kind       string       `json:"kind"`
	Port       int          `json:"port"`
	Path       string       `json:"path"`
	UUID       string       `json:"uuid,omitempty"`
	Password   string       `json:"password,omitempty"`
	Obfs       string       `json:"obfs,omitempty"`
	Endpoint   LinkEndpoint `json:"endpoint"`
	Quota      int64        `json:"quota"`
	Expiry     int64        `json:"expiry"`
	Used       int64        `json:"used"`
	Enabled    bool         `json:"enabled"`
	Congestion string       `json:"congestion"`
	UpMbps     int          `json:"upMbps"`
	DownMbps   int          `json:"downMbps"`
	LastUp     int64        `json:"lastUp,omitempty"`
	LastDown   int64        `json:"lastDown,omitempty"`
}
type ManagedCores struct {
	Enabled  bool          `json:"enabled"`
	Revision int64         `json:"revision"`
	Services []CoreService `json:"services"`
}
type ManagedStatus struct {
	Running      bool   `json:"running"`
	Note         string `json:"note"`
	LastStats    int64  `json:"lastStats"`
	RestartCount int    `json:"restartCount"`
}

func (a *App) singBoxBin() string { return envDefault("ARIA_SING_BOX_BIN", "/app/sing-box") }
func (a *App) loadManagedCores() error {
	a.coreWake = make(chan struct{}, 1)
	a.cores.Services = []CoreService{}
	b, e := os.ReadFile(filepath.Join(a.cfg.DataDir, "aria-managed-cores.json"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if json.Unmarshal(b, &a.cores) != nil {
		return fmt.Errorf("managed-core data invalid")
	}
	if len(a.cores.Services) > 200 {
		return fmt.Errorf("too many managed-core services")
	}
	return nil
}
func (a *App) coresSnapshot() ManagedCores {
	a.coresMu.RLock()
	defer a.coresMu.RUnlock()
	s := a.cores
	s.Services = append([]CoreService{}, s.Services...)
	return s
}
func (a *App) coreSave(s ManagedCores) error {
	if e := atomicJSON(a.cfg.DataDir, "aria-managed-cores.json", s); e != nil {
		return e
	}
	a.coresMu.Lock()
	a.cores = s
	a.coresMu.Unlock()
	select {
	case a.coreWake <- struct{}{}:
	default:
	}
	return nil
}
func coreActive(s CoreService) bool {
	return s.Enabled && (s.Expiry == 0 || s.Expiry > time.Now().UnixMilli()) && (s.Quota == 0 || s.Used < s.Quota)
}
func (a *App) managedCoreSummary() string {
	a.coresMu.RLock()
	defer a.coresMu.RUnlock()
	return fmt.Sprintf("sing-box %s · روشن: %t · سرویس: %d · %s", singBoxVersion, a.managedStatus.Running, len(a.cores.Services), a.managedStatus.Note)
}
func (a *App) managedPublic() any {
	s := a.coresSnapshot()
	a.coresMu.RLock()
	st := a.managedStatus
	a.coresMu.RUnlock()
	rows := []map[string]any{}
	for _, v := range s.Services {
		rows = append(rows, map[string]any{"id": v.ID, "name": v.Name, "kind": v.Kind, "port": v.Port, "quota": v.Quota, "expiry": v.Expiry, "used": v.Used, "enabled": v.Enabled, "active": coreActive(v)})
	}
	_, err := os.Stat(a.singBoxBin())
	return map[string]any{"version": singBoxVersion, "installed": err == nil, "enabled": s.Enabled, "status": st, "services": rows, "location": a.exitLocation(), "mode": envMode(a.cfg.Mode), "note": "تغییر تنظیمات sing-box اتصال‌های همین هسته را کوتاه قطع می‌کند؛ Xray مستقل باقی می‌ماند. سهمیه و انقضا هر ۱۰ ثانیه بررسی می‌شوند."}
}
func (a *App) coreConfig(s ManagedCores) map[string]any {
	inbounds := []any{}
	users := []string{}
	vps := a.vpsSnapshot()
	for _, v := range s.Services {
		if !coreActive(v) {
			continue
		}
		users = append(users, v.ID)
		kind := v.Kind
		web := strings.HasSuffix(kind, "-ws")
		typ := strings.TrimSuffix(kind, "-ws")
		inbound := map[string]any{"type": typ, "tag": v.ID, "listen": "127.0.0.1", "listen_port": v.Port}
		user := map[string]any{"name": v.ID, "password": v.Password}
		if typ == "vless" || typ == "tuic" {
			user["uuid"] = v.UUID
		}
		if typ == "vless" {
			delete(user, "password")
		}
		inbound["users"] = []any{user}
		if web {
			inbound["transport"] = map[string]any{"type": "ws", "path": v.Path}
		} else {
			inbound["listen"] = "0.0.0.0"
			inbound["tls"] = map[string]any{"enabled": true, "server_name": vps.ServerName, "certificate_path": vps.CertFile, "key_path": vps.KeyFile}
		}
		if typ == "tuic" {
			inbound["congestion_control"] = v.Congestion
			inbound["zero_rtt_handshake"] = false
		}
		if typ == "hysteria2" {
			if v.UpMbps > 0 && v.DownMbps > 0 {
				inbound["up_mbps"] = v.UpMbps
				inbound["down_mbps"] = v.DownMbps
			} else {
				inbound["ignore_client_bandwidth"] = true
			}
			if v.Obfs != "" {
				inbound["obfs"] = map[string]any{"type": "salamander", "password": v.Obfs}
			}
		}
		inbounds = append(inbounds, inbound)
	}
	return map[string]any{"log": map[string]any{"level": "warn", "timestamp": true}, "inbounds": inbounds, "outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}, "experimental": map[string]any{"v2ray_api": map[string]any{"listen": "127.0.0.1:" + strconv.Itoa(managedStatsPort), "stats": map[string]any{"enabled": true, "users": users}}}}
}
func (a *App) checkCore(ctx context.Context, s ManagedCores) error {
	bin := a.singBoxBin()
	if _, e := os.Stat(bin); e != nil {
		return fmt.Errorf("باینری مستقل sing-box در این نصب نیست؛ Dockerfile جدید را rebuild کن")
	}
	f, e := os.CreateTemp("", "aria-sb-check-*.json")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_ = f.Chmod(0600)
	e = json.NewEncoder(f).Encode(a.coreConfig(s))
	_ = f.Close()
	if e != nil {
		return e
	}
	c, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if exec.CommandContext(c, bin, "check", "-c", f.Name()).Run() != nil {
		return fmt.Errorf("sing-box کانفیگ را نپذیرفت؛ نسخه، گواهی و امکانات باینری را بررسی کن")
	}
	return nil
}
func (a *App) managedAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		if r.URL.Path == "/api/managed-cores/export" {
			a.exportManaged(w, r)
			return
		}
		jsonReply(w, 200, a.managedPublic())
		return
	}
	a.coreTxnMu.Lock()
	defer a.coreTxnMu.Unlock()
	s := a.coresSnapshot()
	switch r.URL.Path {
	case "/api/managed-cores/settings":
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if readBody(w, r, &in, 1024) != nil {
			apiError(w, 400, "تنظیمات معتبر نیست")
			return
		}
		s.Enabled = in.Enabled
	case "/api/managed-cores/services":
		var in struct {
			Name       string       `json:"name"`
			Kind       string       `json:"kind"`
			QuotaGB    float64      `json:"quotaGB"`
			Days       int          `json:"days"`
			Port       int          `json:"port"`
			Endpoint   LinkEndpoint `json:"endpoint"`
			Congestion string       `json:"congestion"`
			UpMbps     int          `json:"upMbps"`
			DownMbps   int          `json:"downMbps"`
			Obfs       bool         `json:"obfs"`
		}
		if readBody(w, r, &in, 4096) != nil || !safeLabel(in.Name, 64) || math.IsNaN(in.QuotaGB) || math.IsInf(in.QuotaGB, 0) || in.QuotaGB < 0 || in.QuotaGB > 1000000 || in.Days < 0 || in.Days > 3650 || len(s.Services) >= 200 {
			apiError(w, 400, "نام، سهمیه، مدت یا سقف سرویس معتبر نیست")
			return
		}
		switch in.Kind {
		case "vless-ws", "trojan-ws", "hysteria2", "tuic", "anytls":
		default:
			apiError(w, 400, "نوع هسته معتبر نیست")
			return
		}
		web := strings.HasSuffix(in.Kind, "-ws")
		if !web && (a.cfg.Mode != "vps" || a.vpsSnapshot().certificateError() != nil) {
			apiError(w, 409, "این پروتکل به VPS با پورت مستقیم و گواهی TLS معتبر نیاز دارد")
			return
		}
		if in.Congestion == "" {
			in.Congestion = "bbr"
		}
		if in.Congestion != "bbr" && in.Congestion != "cubic" && in.Congestion != "new_reno" {
			apiError(w, 400, "کنترل ازدحام معتبر نیست")
			return
		}
		if in.UpMbps < 0 || in.DownMbps < 0 || in.UpMbps > 10000 || in.DownMbps > 10000 || (in.UpMbps == 0) != (in.DownMbps == 0) {
			apiError(w, 400, "پهنای باند: هر دو صفر برای BBR خودکار، یا هر دو مثبت تا ۱۰۰۰۰ Mbps")
			return
		}
		endpoint, e := normalizeEndpoint(in.Endpoint)
		if e != nil {
			apiError(w, 400, e.Error())
			return
		}
		used := map[int]bool{a.cfg.Port: true, panelPort: true, managedStatsPort: true}
		a.mu.RLock()
		for _, ib := range a.inbounds {
			used[ib.Port] = true
		}
		a.mu.RUnlock()
		for _, v := range s.Services {
			used[v.Port] = true
		}
		port := in.Port
		if web {
			port = 23000
			for used[port] {
				port++
			}
		} else if port < 1024 || port > 65535 || used[port] {
			apiError(w, 400, "پورت مستقیم ۱۰۲۴ تا ۶۵۵۳۵ و بدون تداخل لازم است")
			return
		}
		v := CoreService{ID: "sb-" + randomToken(6), Name: strings.TrimSpace(in.Name), Kind: in.Kind, Port: port, UUID: newUUID(), Password: randomToken(20), Endpoint: endpoint, Enabled: true, Quota: int64(in.QuotaGB * (1 << 30)), Congestion: in.Congestion, UpMbps: in.UpMbps, DownMbps: in.DownMbps}
		v.Path = "/connect/" + v.ID + "/"
		if in.Days > 0 {
			v.Expiry = time.Now().Add(time.Duration(in.Days) * 24 * time.Hour).UnixMilli()
		}
		if in.Obfs && in.Kind == "hysteria2" {
			v.Obfs = randomToken(20)
		}
		s.Services = append(s.Services, v)
		s.Enabled = true
	default:
		p := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/managed-cores/services/"), "/")
		if len(p) != 2 {
			apiError(w, 404, "عملیات هسته پیدا نشد")
			return
		}
		found := false
		for i := range s.Services {
			if s.Services[i].ID != p[0] {
				continue
			}
			found = true
			switch p[1] {
			case "toggle":
				s.Services[i].Enabled = !s.Services[i].Enabled
			case "delete":
				s.Services = append(s.Services[:i], s.Services[i+1:]...)
			case "reset":
				s.Services[i].Used = 0
			case "edit":
				var in struct {
					Name    string  `json:"name"`
					QuotaGB float64 `json:"quotaGB"`
					Days    int     `json:"days"`
				}
				if readBody(w, r, &in, 2048) != nil || !safeLabel(in.Name, 64) || math.IsNaN(in.QuotaGB) || math.IsInf(in.QuotaGB, 0) || in.QuotaGB < 0 || in.QuotaGB > 1000000 || in.Days < 0 || in.Days > 3650 {
					apiError(w, 400, "ویرایش معتبر نیست")
					return
				}
				s.Services[i].Name = in.Name
				s.Services[i].Quota = int64(in.QuotaGB * (1 << 30))
				s.Services[i].Expiry = 0
				if in.Days > 0 {
					s.Services[i].Expiry = time.Now().Add(time.Duration(in.Days) * 24 * time.Hour).UnixMilli()
				}
			default:
				apiError(w, 404, "عملیات نامعتبر است")
				return
			}
			break
		}
		if !found {
			apiError(w, 404, "سرویس هسته پیدا نشد")
			return
		}
	}
	s.Revision++
	if s.Enabled {
		if e := a.checkCore(r.Context(), s); e != nil {
			apiError(w, 409, e.Error())
			return
		}
	}
	if e := a.coreSave(s); e != nil {
		apiError(w, 500, "ذخیرهٔ هسته انجام نشد")
		return
	}
	a.audit("تنظیمات هستهٔ مستقل sing-box تغییر کرد", "settings")
	_ = a.persist()
	jsonReply(w, 200, a.managedPublic())
}
func (a *App) managedRoutes() map[string]int {
	rows := map[string]int{}
	s := a.coresSnapshot()
	if s.Enabled {
		for _, v := range s.Services {
			if coreActive(v) && strings.HasSuffix(v.Kind, "-ws") {
				rows[v.Path] = v.Port
			}
		}
	}
	return rows
}
func (a *App) coreClient(v CoreService) map[string]any {
	settings := a.snapshot().Settings
	address, port, host, sni, security := connectionEndpoint(settings.PublicURL, settings.Connection, v.Endpoint)
	pn, _ := strconv.Atoi(port)
	typ := strings.TrimSuffix(v.Kind, "-ws")
	web := strings.HasSuffix(v.Kind, "-ws")
	if !web {
		address, _, _ = publicEndpoint(settings.PublicURL)
		pn = v.Port
		sni = a.vpsSnapshot().ServerName
		security = "tls"
		if v.Endpoint.Address != "" {
			address = v.Endpoint.Address
		}
		if v.Endpoint.Port > 0 {
			pn = v.Endpoint.Port
		}
		if v.Endpoint.SNI != "" {
			sni = v.Endpoint.SNI
		}
	}
	o := map[string]any{"type": typ, "tag": v.ID, "server": address, "server_port": pn}
	if typ == "vless" || typ == "tuic" {
		o["uuid"] = v.UUID
	}
	if typ != "vless" {
		o["password"] = v.Password
	}
	if web {
		o["transport"] = map[string]any{"type": "ws", "path": v.Path, "headers": map[string]string{"Host": host}}
	}
	if security == "tls" {
		o["tls"] = map[string]any{"enabled": true, "server_name": sni}
	}
	if typ == "tuic" {
		o["congestion_control"] = v.Congestion
	}
	if typ == "hysteria2" {
		if v.UpMbps > 0 {
			o["up_mbps"] = v.UpMbps
			o["down_mbps"] = v.DownMbps
		}
		if v.Obfs != "" {
			o["obfs"] = map[string]any{"type": "salamander", "password": v.Obfs}
		}
	}
	return o
}
func (a *App) exportManaged(w http.ResponseWriter, r *http.Request) {
	for _, v := range a.coresSnapshot().Services {
		if v.ID == r.URL.Query().Get("id") {
			out := a.coreClient(v)
			tag := v.ID
			if label := geoLabel(a.exitLocation()); label != "" {
				tag += " · " + label
			}
			out["tag"] = tag
			config := map[string]any{"log": map[string]any{"level": "warn"}, "inbounds": []any{map[string]any{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 10808}}, "outbounds": []any{out}, "route": map[string]any{"final": tag}}
			jsonReply(w, 200, map[string]any{"config": config, "location": a.exitLocation(), "name": v.Name, "active": coreActive(v), "note": "فایل کامل sing-box؛ SOCKS/HTTP محلی روی 10808. برای VPN کل دستگاه از کلاینت دارای TUN استفاده کن."})
			return
		}
	}
	apiError(w, 404, "سرویس هسته پیدا نشد")
}
func (a *App) setCoreStatus(running bool, note string) {
	a.coresMu.Lock()
	a.managedStatus.Running = running
	a.managedStatus.Note = note
	a.coresMu.Unlock()
}
func (a *App) collectCoreStats(ctx context.Context) error {
	bin := filepath.Join(a.cfg.EngineDir, "bin", "xray-linux-amd64")
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, e := exec.CommandContext(c, bin, "api", "statsquery", "--server=127.0.0.1:"+strconv.Itoa(managedStatsPort), "-pattern", "user>>>").Output()
	if e != nil {
		return fmt.Errorf("آمار هسته در دسترس نیست")
	}
	var data struct {
		Stat []map[string]any `json:"stat"`
	}
	if json.Unmarshal(out, &data) != nil {
		return fmt.Errorf("پاسخ آمار معتبر نیست")
	}
	a.coreTxnMu.Lock()
	defer a.coreTxnMu.Unlock()
	s := a.coresSnapshot()
	changed := false
	for i := range s.Services {
		v := &s.Services[i]
		active := coreActive(*v)
		for _, row := range data.Stat {
			key := textField(row, "name")
			n := numField(row, "value")
			if key == "user>>>"+v.ID+">>>traffic>>>uplink" {
				if n >= v.LastUp {
					v.Used += n - v.LastUp
				}
				v.LastUp = n
				changed = true
			}
			if key == "user>>>"+v.ID+">>>traffic>>>downlink" {
				if n >= v.LastDown {
					v.Used += n - v.LastDown
				}
				v.LastDown = n
				changed = true
			}
		}
		if active != coreActive(*v) {
			s.Revision++
			changed = true
		}
	}
	if changed {
		if e := a.coreSave(s); e != nil {
			return e
		}
	}
	a.coresMu.Lock()
	a.managedStatus.LastStats = time.Now().UnixMilli()
	a.coresMu.Unlock()
	return nil
}
func (a *App) managedCoreLoop(ctx context.Context) {
	var cmd *exec.Cmd
	var done chan error
	revision := int64(-1)
	lastStats := time.Time{}
	retry := time.Time{}
	stop := func() {
		if cmd != nil {
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
			}
			cmd = nil
			done = nil
		}
		a.setCoreStatus(false, "هسته متوقف است")
	}
	defer stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.coreWake:
		case <-ticker.C:
		}
		s := a.coresSnapshot()
		active := false
		for _, v := range s.Services {
			if coreActive(v) {
				active = true
			}
		}
		if !s.Enabled || !active {
			stop()
			revision = s.Revision
			continue
		}
		if cmd != nil {
			select {
			case <-done:
				cmd = nil
				done = nil
				a.setCoreStatus(false, "هسته خارج شد؛ تلاش مجدد خودکار")
				retry = time.Now().Add(5 * time.Second)
			default:
			}
		}
		if (cmd == nil || revision != s.Revision) && time.Now().After(retry) {
			if cmd != nil {
				_ = a.collectCoreStats(ctx)
			}
			stop()
			s = a.coresSnapshot()
			if e := a.checkCore(ctx, s); e != nil {
				a.setCoreStatus(false, e.Error())
				retry = time.Now().Add(15 * time.Second)
				continue
			}
			configRevision := s.Revision
			configPath := filepath.Join(a.cfg.DataDir, "aria-sing-box-runtime.json")
			if atomicJSON(a.cfg.DataDir, "aria-sing-box-runtime.json", a.coreConfig(s)) != nil {
				a.setCoreStatus(false, "فایل هسته ذخیره نشد")
				retry = time.Now().Add(15 * time.Second)
				continue
			}
			a.coreTxnMu.Lock()
			s = a.coresSnapshot()
			for i := range s.Services {
				s.Services[i].LastUp = 0
				s.Services[i].LastDown = 0
			}
			_ = a.coreSave(s)
			a.coreTxnMu.Unlock()
			cmd = exec.CommandContext(ctx, a.singBoxBin(), "run", "-c", configPath)
			cmd.Dir = filepath.Dir(a.singBoxBin())
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if cmd.Start() != nil {
				cmd = nil
				a.setCoreStatus(false, "اجرای sing-box انجام نشد")
				retry = time.Now().Add(15 * time.Second)
				continue
			}
			done = make(chan error, 1)
			running := cmd
			finished := done
			go func() { finished <- running.Wait() }()
			revision = configRevision
			lastStats = time.Now()
			a.setCoreStatus(true, "sing-box مستقل اجرا شده؛ در انتظار آمار")
			a.coresMu.Lock()
			a.managedStatus.RestartCount++
			a.coresMu.Unlock()
		}
		if cmd != nil && time.Since(lastStats) > 10*time.Second {
			lastStats = time.Now()
			if e := a.collectCoreStats(ctx); e != nil {
				stop()
				a.setCoreStatus(false, "آمار قابل دریافت نیست؛ هسته برای حفظ سهمیه متوقف شد")
				retry = time.Now().Add(15 * time.Second)
			} else {
				a.setCoreStatus(true, "هسته و آمار در حال اجرا هستند")
			}
		}
		// Expiry can change independently of configuration revision.
		if cmd != nil {
			expired := false
			for _, v := range s.Services {
				if v.Enabled && v.Expiry > 0 && v.Expiry < time.Now().UnixMilli() {
					expired = true
				}
			}
			if expired {
				a.coreTxnMu.Lock()
				next := a.coresSnapshot()
				for i := range next.Services {
					if next.Services[i].Enabled && next.Services[i].Expiry > 0 && next.Services[i].Expiry < time.Now().UnixMilli() {
						next.Services[i].Enabled = false
						next.Revision++
					}
				}
				_ = a.coreSave(next)
				a.coreTxnMu.Unlock()
			}
		}
	}
}
