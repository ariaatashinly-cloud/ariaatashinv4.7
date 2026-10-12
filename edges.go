package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type EdgeRoute struct {
	DNSRelay    bool         `json:"dnsRelay,omitempty"`
	Probe       *EdgeProbe   `json:"probe,omitempty"`
	Resource    string       `json:"resource,omitempty"`
	ResourceID  string       `json:"resourceId,omitempty"`
	Deployment  string       `json:"deployment,omitempty"`
	DeployState string       `json:"deployState,omitempty"`
	Origin      string       `json:"origin,omitempty"`
	Networks    []string     `json:"networks,omitempty"`
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Provider    string       `json:"provider"`
	Endpoint    LinkEndpoint `json:"endpoint"`
	Enabled     bool         `json:"enabled"`
	LastTest    int64        `json:"lastTest"`
	Verified    bool         `json:"verified"`
	LatencyMS   int64        `json:"latencyMS"`
	Note        string       `json:"note"`
}
type EdgeData struct {
	Credentials []RelayCredential `json:"credentials,omitempty"`
	Identity    string            `json:"identity"`
	Routes      []EdgeRoute       `json:"routes"`
	CFToken     string            `json:"cfToken,omitempty"`
	CFZone      string            `json:"cfZone,omitempty"`
}

func (a *App) loadEdges() error {
	a.edges.Routes = []EdgeRoute{}
	b, e := os.ReadFile(filepath.Join(a.cfg.DataDir, "aria-edges.json"))
	if e == nil {
		if json.Unmarshal(b, &a.edges) != nil {
			return fmt.Errorf("edge data invalid")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if a.edges.Identity == "" {
		a.edges.Identity = randomToken(16)
		return atomicJSON(a.cfg.DataDir, "aria-edges.json", a.edges)
	}
	return nil
}
func (a *App) edgeSnapshot() EdgeData {
	a.edgesMu.RLock()
	defer a.edgesMu.RUnlock()
	s := a.edges
	s.Routes = append([]EdgeRoute{}, s.Routes...)
	s.Credentials = append([]RelayCredential{}, s.Credentials...)
	return s
}
func (a *App) edgeSave(s EdgeData) error {
	if e := atomicJSON(a.cfg.DataDir, "aria-edges.json", s); e != nil {
		return e
	}
	a.edgesMu.Lock()
	a.edges = s
	a.edgesMu.Unlock()
	return nil
}
func (a *App) edgePublic() any {
	s := a.edgeSnapshot()
	return map[string]any{"routes": s.Routes, "cloudflareConfigured": s.CFToken != "", "zoneId": s.CFZone, "note": "آزمون از شبکه همین سرور است؛ دسترسی اپراتور ایران و تونل را باید در کلاینت آزمود."}
}
func validEdge(v EdgeRoute) error {
	if !safeLabel(v.Name, 64) {
		return fmt.Errorf("نام مسیر معتبر نیست")
	}
	e, err := normalizeEndpoint(v.Endpoint)
	if err != nil {
		return err
	}
	if e.Host == "" || e.SNI == "" || e.Port == 0 || net.ParseIP(e.Host) != nil || net.ParseIP(e.SNI) != nil || e.Address == "" {
		return fmt.Errorf("Address ورودی، Host و SNI دامنه و پورت HTTPS لازم است")
	}
	if ip := net.ParseIP(e.Address); ip != nil && blockedPanelIP(ip) {
		return fmt.Errorf("ورودی CDN باید عمومی باشد")
	}
	return nil
}
func (a *App) probeEdge(ctx context.Context, v EdgeRoute) (int64, error) {
	report, err := a.probeEdgeDetailed(ctx, v)
	return report.MS, err
}
func probeWebSocket(ctx context.Context, cl *http.Client, uri, host string) error {
	key := base64.StdEncoding.EncodeToString([]byte(randomToken(8)))
	req, e := http.NewRequestWithContext(ctx, "GET", uri, nil)
	if e != nil {
		return fmt.Errorf("مسیر WebSocket معتبر نیست")
	}
	req.Host = host
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	resp, e := cl.Do(req)
	if e != nil {
		return fmt.Errorf("WebSocket ورودی پاسخ نداد؛ قابلیت WS و محدودیت سرویس را بررسی کن")
	}
	defer resp.Body.Close()
	hash := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	want := base64.StdEncoding.EncodeToString(hash[:])
	if resp.StatusCode != 101 || !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") || resp.Header.Get("Sec-WebSocket-Accept") != want {
		return fmt.Errorf("HTTP آماده است اما WebSocket تأیید نشد؛ تنظیم سرویس و مسیر WS را بررسی کن")
	}
	return nil
}
func (a *App) edgesAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/edges/delivery" {
		a.queueEdgeDelivery(w, r)
		return
	}
	if r.Method == "GET" {
		if r.URL.Path == "/api/edges/export" {
			a.edgeExport(w, r)
			return
		}
		jsonReply(w, 200, a.edgePublic())
		return
	}
	a.edgeTxnMu.Lock()
	defer a.edgeTxnMu.Unlock()
	s := a.edgeSnapshot()
	switch r.URL.Path {
	case "/api/edges/routes":
		var v EdgeRoute
		if readBody(w, r, &v, 4096) != nil {
			apiError(w, 400, "مسیر معتبر نیست")
			return
		}
		v.Provider = "manual"
		if e := validEdge(v); e != nil {
			apiError(w, 400, e.Error())
			return
		}
		v.Endpoint, _ = normalizeEndpoint(v.Endpoint)
		if v.ID == "" {
			v.ID = "edge-" + randomToken(6)
		}
		v.Verified = false
		v.LastTest = 0
		found := false
		for i := range s.Routes {
			if s.Routes[i].ID == v.ID {
				s.Routes[i] = v
				found = true
				break
			}
		}
		if !found {
			if len(s.Routes) >= 16 {
				apiError(w, 400, "حداکثر ۱۶ مسیر جایگزین")
				return
			}
			s.Routes = append(s.Routes, v)
		}
	case "/api/edges/cloudflare":
		if e := a.cloudflareRoute(r.Context(), w, r, &s); e != nil {
			apiError(w, 409, e.Error())
			return
		}
	case "/api/edges/deploy":
		if e := a.deployRelay(r.Context(), w, r, &s); e != nil {
			apiError(w, 409, e.Error())
			return
		}
	default:
		p := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/edges/routes/"), "/")
		if len(p) != 2 {
			apiError(w, 404, "مسیر پیدا نشد")
			return
		}
		found := false
		for i := range s.Routes {
			if s.Routes[i].ID != p[0] {
				continue
			}
			found = true
			switch p[1] {
			case "test":
				if e := a.relayStatus(r.Context(), &s.Routes[i], s); e != nil {
					s.Routes[i].Note = e.Error()
					s.Routes[i].Verified = false
					s.Routes[i].LastTest = time.Now().UnixMilli()
					break
				}
				report, e := a.probeEdgeDetailed(r.Context(), s.Routes[i])
				s.Routes[i].Probe = &report
				ms := report.MS
				s.Routes[i].Verified = e == nil
				s.Routes[i].LastTest = time.Now().UnixMilli()
				s.Routes[i].LatencyMS = ms
				if e != nil {
					s.Routes[i].Note = e.Error()
				} else {
					s.Routes[i].Note = "TLS و مسیر به همین پنل تأیید شد؛ آزمون اپراتور و ترافیک در کلاینت لازم است"
				}
			case "toggle":
				s.Routes[i].Enabled = !s.Routes[i].Enabled
			case "delete":
				s.Routes = append(s.Routes[:i], s.Routes[i+1:]...)
				creds := []RelayCredential{}
				for _, c := range s.Credentials {
					if c.ID != p[0] {
						creds = append(creds, c)
					}
				}
				s.Credentials = creds
			default:
				apiError(w, 404, "عملیات معتبر نیست")
				return
			}
			break
		}
		if !found {
			apiError(w, 404, "مسیر پیدا نشد")
			return
		}
	}
	if e := a.edgeSave(s); e != nil {
		apiError(w, 500, "مسیر ذخیره نشد")
		return
	}
	a.audit("مسیرهای جایگزین تغییر کردند", "settings")
	_ = a.persist()
	jsonReply(w, 200, a.edgePublic())
}
func (a *App) cfCall(ctx context.Context, token, method, path string, body any, out any) error {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, e := http.NewRequestWithContext(ctx, method, "https://api.cloudflare.com/client/v4"+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	cl := a.edgeHTTP
	if cl == nil {
		cl = &http.Client{Timeout: 15 * time.Second, CheckRedirect: noRedirect}
	}
	resp, e := cl.Do(req)
	if e != nil {
		return fmt.Errorf("ارتباط Cloudflare API برقرار نشد")
	}
	defer resp.Body.Close()
	var v struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v) != nil || resp.StatusCode < 200 || resp.StatusCode > 299 || !v.Success {
		return fmt.Errorf("Cloudflare نپذیرفت؛ توکن و مجوز Zone/DNS را بررسی کن")
	}
	if out != nil {
		return json.Unmarshal(v.Result, out)
	}
	return nil
}
func (a *App) cloudflareRoute(ctx context.Context, w http.ResponseWriter, r *http.Request, s *EdgeData) error {
	var in struct {
		Token    string `json:"token"`
		Zone     string `json:"zone"`
		Name     string `json:"name"`
		Hostname string `json:"hostname"`
		Target   string `json:"target"`
	}
	if readBody(w, r, &in, 4096) != nil {
		return fmt.Errorf("فرم کلودفلر معتبر نیست")
	}
	in.Token = strings.TrimSpace(in.Token)
	if in.Token == "" {
		in.Token = s.CFToken
	}
	if in.Zone == "" {
		in.Zone = s.CFZone
	}
	if len(in.Token) < 20 || len(in.Token) > 512 || !regexp.MustCompile(`^[a-fA-F0-9]{32}$`).MatchString(in.Zone) || !validHostname(in.Hostname) || !validHostname(in.Target) || !safeLabel(in.Name, 64) {
		return fmt.Errorf("توکن، Zone ID، نام و دامنه مبدأ معتبر لازم است")
	}
	var check struct {
		Status string `json:"status"`
	}
	if e := a.cfCall(ctx, in.Token, "GET", "/user/tokens/verify", nil, &check); e != nil {
		return e
	}
	if check.Status != "active" {
		return fmt.Errorf("توکن فعال نیست")
	}
	var zone struct {
		Name string `json:"name"`
	}
	if e := a.cfCall(ctx, in.Token, "GET", "/zones/"+in.Zone, nil, &zone); e != nil {
		return e
	}
	if in.Hostname != zone.Name && !strings.HasSuffix(in.Hostname, "."+zone.Name) {
		return fmt.Errorf("دامنه به این Zone تعلق ندارد")
	}
	if len(s.Routes) >= 16 {
		return fmt.Errorf("سقف مسیرها پر است")
	}
	var records []struct {
		Content string `json:"content"`
		Proxied bool   `json:"proxied"`
	}
	if e := a.cfCall(ctx, in.Token, "GET", "/zones/"+in.Zone+"/dns_records?name="+url.QueryEscape(in.Hostname), nil, &records); e != nil {
		return e
	}
	if len(records) > 0 {
		if len(records) != 1 || records[0].Content != in.Target || !records[0].Proxied {
			return fmt.Errorf("رکورد دیگری با این نام وجود دارد؛ نام تازه انتخاب کن")
		}
	} else {
		if e := a.cfCall(ctx, in.Token, "POST", "/zones/"+in.Zone+"/dns_records", map[string]any{"type": "CNAME", "name": in.Hostname, "content": in.Target, "proxied": true, "ttl": 1, "comment": "ariaatashin:" + s.Identity}, nil); e != nil {
			return e
		}
	}
	s.CFToken = in.Token
	s.CFZone = in.Zone
	v := EdgeRoute{ID: "edge-" + randomToken(6), Name: in.Name, Provider: "cloudflare", Endpoint: LinkEndpoint{Address: in.Hostname, Host: in.Hostname, SNI: in.Hostname, Port: 443}, Enabled: true, Note: "رکورد متصل شد؛ دامنه را در هاست مبدأ ثبت و آزمون مسیر را اجرا کن"}
	for i := range s.Routes {
		if s.Routes[i].Endpoint.Host == in.Hostname {
			s.Routes[i] = v
			return nil
		}
	}
	s.Routes = append(s.Routes, v)
	return nil
}
func (a *App) edgeExport(w http.ResponseWriter, r *http.Request) {
	u, ok := a.findUser(r.URL.Query().Get("email"))
	if !ok {
		apiError(w, 404, "کاربر Xray پیدا نشد")
		return
	}
	settings := a.snapshot().Settings
	edges := []EdgeRoute{{ID: "origin", Name: "مبدأ", Enabled: true, Verified: true}}
	selected := r.URL.Query().Get("route")
	if selected != "" {
		edges = nil
	}
	for _, e := range a.edgeSnapshot().Routes {
		if e.Enabled && e.Verified && (selected == "" || selected == e.ID) {
			edges = append(edges, e)
		}
	}
	if len(edges) == 0 {
		apiError(w, 409, "مسیر فعال و تأییدشده پیدا نشد؛ ابتدا دریافت وضعیت و آزمون را اجرا کن")
		return
	}
	xo := []any{}
	so := []any{}
	tags := []string{}
	links := []string{}
	for _, link := range u.Links {
		for _, profile := range a.snapshot().Profiles {
			if profile.Key != link.Profile || profile.Direct {
				continue
			}
			for _, edge := range edges {
				if !edgeSupports(edge, profile) {
					continue
				}
				p := profileForEdge(profile, edge)
				tag := "edge-" + edge.ID + "-" + p.Key
				if label := geoLabel(a.exitLocation()); label != "" {
					tag += " · " + label
				}
				if o := xrayCloudOutbound(p, u.Raw, settings.PublicURL, settings.Connection); o != nil {
					o["tag"] = tag
					xo = append(xo, o)
					link := shareLinkWithEndpoint(p, u.Raw, settings.PublicURL, settings.Connection)
					links = append(links, labelShareLink(link, a.exitLocation()))
				}
				if p.Tuning.MuxConcurrency == 0 && p.Tuning.MaxEarlyData == 0 {
					if o := singBoxOutboundWithEndpoint(p, u.Raw, settings.PublicURL, settings.Connection); o != nil {
						o["tag"] = tag
						so = append(so, o)
						tags = append(tags, tag)
					}
				}
			}
		}
	}
	if len(xo) == 0 {
		apiError(w, 409, "این کاربر اتصال وب ندارد")
		return
	}
	xtags := []string{}
	for _, o := range xo {
		xtags = append(xtags, o.(map[string]any)["tag"].(string))
	}
	xconfig := map[string]any{"log": map[string]any{"loglevel": "warning"}, "inbounds": []any{map[string]any{"tag": "socks-in", "listen": "127.0.0.1", "port": 10808, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": false}}}, "outbounds": xo, "observatory": map[string]any{"subjectSelector": []string{"edge-"}, "probeURL": "https://cp.cloudflare.com/generate_204", "probeInterval": "30s", "enableConcurrency": true}, "routing": map[string]any{"balancers": []any{map[string]any{"tag": "auto", "selector": xtags, "strategy": map[string]string{"type": "leastPing"}}}, "rules": []any{map[string]any{"type": "field", "inboundTag": []string{"socks-in"}, "balancerTag": "auto"}}}}
	var sconfig any
	if len(so) > 0 {
		so = append(so, map[string]any{"type": "urltest", "tag": "auto", "outbounds": tags, "url": "https://cp.cloudflare.com/generate_204", "interval": "1m", "tolerance": 50})
		sconfig = map[string]any{"log": map[string]any{"level": "warn"}, "inbounds": []any{map[string]any{"type": "mixed", "listen": "127.0.0.1", "listen_port": 10808}}, "outbounds": so, "route": map[string]string{"final": "auto"}}
	}
	jsonReply(w, 200, map[string]any{"xrayConfig": xconfig, "singBoxConfig": sconfig, "links": links, "routes": len(edges), "location": a.exitLocation(), "note": "انتخاب مسیر از دید کلاینت؛ اتصال موجود هنگام قطعی ممکن است به اتصال مجدد نیاز داشته باشد. EarlyData در ورودی توکنی خاموش است. XHTTP فقط در Xray است."})
}
