package main

import (
	"context"
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

// Only server/endpoint addresses are looked up; customer IPs are never submitted.
type GeoInfo struct {
	IP      string `json:"ip"`
	Code    string `json:"code"`
	Country string `json:"country"`
	Flag    string `json:"flag"`
	Checked int64  `json:"checked"`
	Kind    string `json:"kind"`
	Note    string `json:"note,omitempty"`
}
type GeoData struct {
	Exit    GeoInfo            `json:"exit"`
	Entries map[string]GeoInfo `json:"entries"`
}

func (a *App) loadGeo() {
	a.geo = GeoData{Entries: map[string]GeoInfo{}}
	if b, e := os.ReadFile(filepath.Join(a.cfg.DataDir, "aria-location.json")); e == nil {
		_ = json.Unmarshal(b, &a.geo)
	}
	if a.geo.Entries == nil {
		a.geo.Entries = map[string]GeoInfo{}
	}
}
func countryFlag(code string) string {
	if !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(code) {
		return "🌐"
	}
	return string([]rune{0x1f1e6 + rune(code[0]-'A'), 0x1f1e6 + rune(code[1]-'A')})
}
func (a *App) exitLocation() GeoInfo { a.geoMu.RLock(); defer a.geoMu.RUnlock(); return a.geo.Exit }
func geoLabel(g GeoInfo) string {
	if g.Code == "" {
		return ""
	}
	return g.Flag + " " + g.Country
}
func (a *App) lookupGeo(ctx context.Context, host, kind string) (GeoInfo, error) {
	ip := ""
	if host != "" {
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil || len(ips) == 0 {
			return GeoInfo{}, fmt.Errorf("DNS آدرس پاسخ نداد")
		}
		for _, v := range ips {
			if blockedPanelIP(v.IP) {
				return GeoInfo{}, fmt.Errorf("IP خصوصی کشور عمومی ندارد")
			}
		}
		ip = ips[0].IP.String()
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://ipwho.is/"+ip+"?fields=success,ip,country,country_code", nil)
	cl := a.geoHTTP
	if cl == nil {
		cl = &http.Client{Timeout: 4 * time.Second, CheckRedirect: noRedirect}
	}
	resp, e := cl.Do(req)
	if e != nil {
		return GeoInfo{}, fmt.Errorf("تشخیص کشور در دسترس نیست")
	}
	defer resp.Body.Close()
	var v struct {
		Success bool   `json:"success"`
		IP      string `json:"ip"`
		Country string `json:"country"`
		Code    string `json:"country_code"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&v) != nil || !v.Success || net.ParseIP(v.IP) == nil || !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(v.Code) || len(v.Country) > 100 || blockedPanelIP(net.ParseIP(v.IP)) || (ip != "" && net.ParseIP(ip).Equal(net.ParseIP(v.IP)) == false) {
		return GeoInfo{}, fmt.Errorf("پاسخ کشور معتبر نیست")
	}
	return GeoInfo{IP: v.IP, Code: v.Code, Country: v.Country, Flag: countryFlag(v.Code), Checked: time.Now().UnixMilli(), Kind: kind}, nil
}
func (a *App) refreshGeo(ctx context.Context) error {
	g, e := a.lookupGeo(ctx, "", "exit")
	if e != nil {
		return e
	}
	a.geoMu.Lock()
	defer a.geoMu.Unlock()
	a.geo.Exit = g
	return atomicJSON(a.cfg.DataDir, "aria-location.json", a.geo)
}
func (a *App) locationAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		if e := a.refreshGeo(r.Context()); e != nil {
			apiError(w, 502, e.Error())
			return
		}
	} else if r.Method != "GET" {
		apiError(w, 405, "روش معتبر نیست")
		return
	}
	a.geoMu.RLock()
	defer a.geoMu.RUnlock()
	jsonReply(w, 200, map[string]any{"exit": a.geo.Exit, "entries": a.geo.Entries, "note": "خروجی: IP اینترنت همین سرور. کشور ورودی CDN محل IP ورودی است و ممکن است با خروجی متفاوت باشد؛ مکان‌یابی تقریبی است."})
}
func labelShareLink(link string, g GeoInfo) string {
	label := geoLabel(g)
	if label == "" {
		return link
	}
	if strings.HasPrefix(link, "vmess://") {
		b, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
		if e != nil {
			return link
		}
		var v map[string]any
		if json.Unmarshal(b, &v) != nil {
			return link
		}
		name := textField(v, "ps")
		if strings.HasPrefix(name, g.Flag+" ") {
			return link
		}
		v["ps"] = label + " · " + trimGeoPrefix(name)
		b, _ = json.Marshal(v)
		return "vmess://" + base64.StdEncoding.EncodeToString(b)
	}
	u, e := url.Parse(link)
	if e != nil || u.Host == "" || !strings.Contains(link, "://") || u.Scheme == "https" || u.Scheme == "http" {
		return link
	}
	if !strings.HasPrefix(u.Fragment, g.Flag+" ") {
		u.Fragment = label + " · " + trimGeoPrefix(u.Fragment)
	}
	return u.String()
}
func (a *App) locatedUser(u User) User {
	g := a.exitLocation()
	u.Location = g
	u.Links = append([]Connection{}, u.Links...)
	for i := range u.Links {
		u.Links[i].Link = labelShareLink(u.Links[i].Link, g)
		if geoLabel(g) != "" && !strings.HasPrefix(u.Links[i].Name, g.Flag+" ") {
			u.Links[i].Name = geoLabel(g) + " · " + u.Links[i].Name
		}
	}
	return u
}
func (a *App) locationLoop(ctx context.Context) {
	refresh := func() {
		if time.Now().UnixMilli()-a.exitLocation().Checked > int64(6*time.Hour/time.Millisecond) {
			_ = a.refreshGeo(ctx)
		}
	}
	refresh()
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refresh()
		}
	}
}

func trimGeoPrefix(name string) string {
	return regexp.MustCompile(`^[\x{1F1E6}-\x{1F1FF}]{2} [^·]{1,100} · `).ReplaceAllString(name, "")
}
