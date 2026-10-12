package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"time"
)

//go:embed relays/*
var relayAssets embed.FS

type RelayCredential struct {
	ID      string `json:"id"`
	Token   string `json:"token"`
	Team    string `json:"team,omitempty"`
	Account string `json:"account,omitempty"`
}
type RelayDeployRequest struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Token    string `json:"token"`
	Team     string `json:"team"`
	Account  string `json:"account"`
}

func relaySource(provider, origin string) string {
	file := map[string]string{"vercel": "vercel.mjs", "deno": "deno.ts", "cloudflare-worker": "cloudflare.js"}[provider]
	b, _ := relayAssets.ReadFile("relays/" + file)
	quoted, _ := json.Marshal(origin)
	return strings.ReplaceAll(string(b), "__ORIGIN__", string(quoted))
}
func (a *App) providerClient() *http.Client {
	if a.edgeHTTP != nil {
		return a.edgeHTTP
	}
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: noRedirect}
}
func (a *App) providerCall(ctx context.Context, token, method, uri string, body any, out any) error {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, _ := http.NewRequestWithContext(ctx, method, uri, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return a.providerResponse(req, out)
}
func (a *App) providerResponse(req *http.Request, out any) error {
	resp, e := a.providerClient().Do(req)
	if e != nil {
		return fmt.Errorf("ارتباط API سرویس برقرار نشد")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("سرویس پاسخ %d داد؛ توکن، مجوز حساب و محدودیت طرح را بررسی کن", resp.StatusCode)
	}
	if out != nil && json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out) != nil {
		return fmt.Errorf("پاسخ API معتبر نیست")
	}
	return nil
}
func setRelayHost(v *EdgeRoute, host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if !validHostname(host) || net.ParseIP(host) != nil {
		return fmt.Errorf("دامنهٔ دپلوی هنوز آماده نیست")
	}
	v.Endpoint = LinkEndpoint{Address: host, Host: host, SNI: host, Port: 443}
	return nil
}
func (a *App) deployRelay(ctx context.Context, w http.ResponseWriter, r *http.Request, s *EdgeData) error {
	var in RelayDeployRequest
	if readBody(w, r, &in, 8192) != nil {
		return fmt.Errorf("اطلاعات اتصال معتبر نیست")
	}
	if !regexp.MustCompile(`^relay-[a-f0-9]{24}$`).MatchString(in.ID) || !safeLabel(in.Name, 64) || len(in.Token) < 20 || len(in.Token) > 1024 || strings.ContainsAny(in.Token, "\r\n\x00") {
		return fmt.Errorf("نام، توکن و شناسهٔ درخواست معتبر لازم است")
	}
	if in.Provider != "vercel" && in.Provider != "deno" && in.Provider != "cloudflare-worker" {
		return fmt.Errorf("سرویس پشتیبانی نمی‌شود")
	}
	if in.Provider == "deno" && !strings.HasPrefix(in.Token, "ddo_") {
		return fmt.Errorf("توکن سازمانی جدید Deno با پیشوند ddo_ لازم است")
	}
	if len(in.Team) > 100 || strings.ContainsAny(in.Team, "\r\n") {
		return fmt.Errorf("Team ID معتبر نیست")
	}
	if in.Provider == "cloudflare-worker" && !regexp.MustCompile(`^[a-fA-F0-9]{32}$`).MatchString(in.Account) {
		return fmt.Errorf("Account ID کلودفلر لازم است")
	}
	for _, v := range s.Routes {
		if v.ID == in.ID {
			return nil
		}
	}
	if len(s.Routes) >= 16 {
		return fmt.Errorf("حداکثر ۱۶ مسیر جایگزین")
	}
	origin := a.snapshot().Settings.PublicURL
	u, e := validatePublicURL(origin)
	if e != nil || u.Scheme != "https" {
		return fmt.Errorf("ابتدا آدرس HTTPS عمومی مبدأ را در پنل ثبت کن")
	}
	ips, e := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if e != nil || len(ips) == 0 {
		return fmt.Errorf("دامنهٔ مبدأ پاسخ DNS ندارد")
	}
	for _, ip := range ips {
		if blockedPanelIP(ip.IP) {
			return fmt.Errorf("مبدأ باید HTTPS عمومی باشد")
		}
	}
	v := EdgeRoute{ID: in.ID, Name: in.Name, Provider: in.Provider, DNSRelay: true, Resource: "aria-" + strings.TrimPrefix(in.ID, "relay-"), Enabled: true, Networks: []string{"ws"}, DeployState: "creating", Origin: origin, Note: "ساخت ورودی؛ پس از آماده‌شدن، آزمون و ساخت خروجی را اجرا کن"}
	cred := RelayCredential{ID: in.ID, Token: in.Token, Team: in.Team, Account: in.Account}
	s.Routes = append(s.Routes, v)
	s.Credentials = append(s.Credentials, cred)
	if e := a.edgeSave(*s); e != nil {
		return fmt.Errorf("اطلاعات اتصال ذخیره نشد")
	}
	i := len(s.Routes) - 1
	switch in.Provider {
	case "vercel":
		e = a.deployVercel(ctx, &s.Routes[i], cred)
	case "deno":
		e = a.deployDeno(ctx, &s.Routes[i], cred)
	case "cloudflare-worker":
		e = a.deployWorker(ctx, &s.Routes[i], cred)
	}
	if e != nil {
		s.Routes[i].DeployState = "error"
		s.Routes[i].Note = e.Error()
	} else {
		s.Routes[i].Note = "دپلوی ثبت شد؛ دریافت وضعیت و آزمون مسیر را اجرا کن"
	}
	if err := a.edgeSave(*s); err != nil {
		return fmt.Errorf("ثبت نتیجهٔ دپلوی انجام نشد")
	}
	return e
}
func teamQuery(team string) string {
	if team == "" {
		return ""
	}
	return "?teamId=" + url.QueryEscape(team)
}
func (a *App) deployVercel(ctx context.Context, v *EdgeRoute, c RelayCredential) error {
	var p struct {
		ID string `json:"id"`
	}
	if e := a.providerCall(ctx, c.Token, "POST", "https://api.vercel.com/v11/projects"+teamQuery(c.Team), map[string]any{"name": v.Resource, "framework": nil, "publicSource": false, "resourceConfig": map[string]any{"fluid": true}}, &p); e != nil {
		return e
	}
	if p.ID == "" {
		return fmt.Errorf("شناسهٔ پروژه برنگشت")
	}
	v.ResourceID = p.ID
	// Save the new resource ID before starting its first deployment.
	if e := a.persistRelayProgress(*v); e != nil {
		return e
	}
	config := map[string]any{"functions": map[string]any{"api/relay.mjs": map[string]any{"maxDuration": 300}}, "rewrites": []any{map[string]string{"source": "/:path*", "destination": "/api/relay?aria_path=:path*"}}}
	b, _ := json.Marshal(config)
	files := []any{map[string]any{"file": "api/relay.mjs", "data": relaySource("vercel", v.Origin)}, map[string]any{"file": "package.json", "data": `{"private":true,"type":"module","dependencies":{"ws":"8.22.0"}}`}, map[string]any{"file": "vercel.json", "data": string(b)}}
	var d struct {
		ID    string `json:"id"`
		URL   string `json:"url"`
		State string `json:"readyState"`
	}
	if e := a.providerCall(ctx, c.Token, "POST", "https://api.vercel.com/v13/deployments"+teamQuery(c.Team), map[string]any{"name": v.Resource, "project": p.ID, "target": "production", "files": files, "projectSettings": map[string]any{"framework": nil}}, &d); e != nil {
		return e
	}
	v.Deployment = d.ID
	v.DeployState = d.State
	if v.DeployState == "" {
		v.DeployState = "QUEUED"
	}
	return setRelayHost(v, v.Resource+".vercel.app")
}
func (a *App) deployDeno(ctx context.Context, v *EdgeRoute, c RelayCredential) error {
	config := map[string]any{"runtime": map[string]string{"type": "dynamic", "entrypoint": "main.ts"}}
	var p struct {
		ID string `json:"id"`
	}
	if e := a.providerCall(ctx, c.Token, "POST", "https://api.deno.com/v2/apps", map[string]any{"slug": v.Resource, "labels": map[string]string{"aria.identity": a.edgeSnapshot().Identity}, "config": config}, &p); e != nil {
		return e
	}
	if p.ID == "" {
		return fmt.Errorf("شناسهٔ برنامه برنگشت")
	}
	v.ResourceID = p.ID
	if e := a.persistRelayProgress(*v); e != nil {
		return e
	}
	var d denoRevision
	if e := a.providerCall(ctx, c.Token, "POST", "https://api.deno.com/v2/apps/"+url.PathEscape(p.ID)+"/deploy", map[string]any{"assets": map[string]any{"main.ts": map[string]string{"kind": "file", "content": relaySource("deno", v.Origin), "encoding": "utf-8"}}, "production": true, "config": config}, &d); e != nil {
		return e
	}
	v.Deployment = d.ID
	v.DeployState = d.Status
	for _, t := range d.Timelines {
		if len(t.Hostnames) > 0 && t.Context == "Production" {
			return setRelayHost(v, t.Hostnames[0])
		}
	}
	return nil
}

type denoRevision struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Timelines []struct {
		Context   string   `json:"context"`
		Hostnames []string `json:"hostnames"`
	} `json:"timelines"`
}

func (a *App) persistRelayProgress(v EdgeRoute) error {
	s := a.edgeSnapshot()
	for i := range s.Routes {
		if s.Routes[i].ID == v.ID {
			s.Routes[i] = v
		}
	}
	return a.edgeSave(s)
}
func (a *App) deployWorker(ctx context.Context, v *EdgeRoute, c RelayCredential) error {
	base := "/accounts/" + c.Account + "/workers"
	var sub struct {
		Subdomain string `json:"subdomain"`
	}
	if e := a.cfCall(ctx, c.Token, "GET", base+"/subdomain", nil, &sub); e != nil {
		return e
	}
	if sub.Subdomain == "" {
		return fmt.Errorf("ابتدا زیردامنهٔ workers.dev را در حساب فعال کن")
	}
	var buf bytes.Buffer
	m := multipart.NewWriter(&buf)
	meta, _ := json.Marshal(map[string]any{"main_module": "relay.js", "compatibility_date": "2026-10-01"})
	mh := textproto.MIMEHeader{}
	mh.Set("Content-Disposition", `form-data; name="metadata"`)
	mh.Set("Content-Type", "application/json")
	mp, _ := m.CreatePart(mh)
	_, _ = mp.Write(meta)
	sh := textproto.MIMEHeader{}
	sh.Set("Content-Disposition", `form-data; name="relay.js"; filename="relay.js"`)
	sh.Set("Content-Type", "application/javascript+module")
	p, _ := m.CreatePart(sh)
	_, _ = p.Write([]byte(relaySource("cloudflare-worker", v.Origin)))
	_ = m.Close()
	req, _ := http.NewRequestWithContext(ctx, "PUT", "https://api.cloudflare.com/client/v4"+base+"/scripts/"+v.Resource, &buf)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", m.FormDataContentType())
	var out struct {
		Success bool `json:"success"`
	}
	if e := a.providerResponse(req, &out); e != nil {
		return e
	}
	if !out.Success {
		return fmt.Errorf("آپلود Worker پذیرفته نشد")
	}
	v.ResourceID = v.Resource
	if e := a.persistRelayProgress(*v); e != nil {
		return e
	}
	if e := a.cfCall(ctx, c.Token, "POST", base+"/scripts/"+v.Resource+"/subdomain", map[string]bool{"enabled": true, "previews_enabled": false}, nil); e != nil {
		return e
	}
	v.DeployState = "ready"
	return setRelayHost(v, v.Resource+"."+sub.Subdomain+".workers.dev")
}
func (a *App) relayStatus(ctx context.Context, v *EdgeRoute, s EdgeData) error {
	var c RelayCredential
	for _, cred := range s.Credentials {
		if cred.ID == v.ID {
			c = cred
			break
		}
	}
	if c.Token == "" {
		return nil
	}
	if v.Provider == "cloudflare-worker" {
		return nil
	}
	if v.Deployment == "" && (v.Provider == "vercel" || v.Provider == "deno") {
		return fmt.Errorf("دپلوی ثبت نشده؛ وضعیت پروژهٔ تازه را در حساب سرویس بررسی کن")
	}
	switch v.Provider {
	case "vercel":
		var d struct {
			State string `json:"readyState"`
		}
		if e := a.providerCall(ctx, c.Token, "GET", "https://api.vercel.com/v13/deployments/"+url.PathEscape(v.Deployment)+teamQuery(c.Team), nil, &d); e != nil {
			return e
		}
		v.DeployState = d.State
		if d.State != "READY" {
			return fmt.Errorf("Vercel هنوز آماده نیست: %s", d.State)
		}
	case "deno":
		var d denoRevision
		if e := a.providerCall(ctx, c.Token, "GET", "https://api.deno.com/v2/revisions/"+url.PathEscape(v.Deployment), nil, &d); e != nil {
			return e
		}
		v.DeployState = d.Status
		if d.Status != "succeeded" {
			return fmt.Errorf("Deno هنوز آماده نیست: %s", d.Status)
		}
		for _, t := range d.Timelines {
			if t.Context == "Production" && len(t.Hostnames) > 0 {
				return setRelayHost(v, t.Hostnames[0])
			}
		}
		return fmt.Errorf("دامنهٔ Production هنوز برنگشته است")
	}
	return nil
}
func edgeSupports(v EdgeRoute, p Profile) bool {
	if len(v.Networks) == 0 {
		return true
	}
	for _, n := range v.Networks {
		if n == p.Network {
			return true
		}
	}
	return false
}

func profileForEdge(p Profile, v EdgeRoute) Profile {
	if v.ID != "origin" {
		p.Endpoint = v.Endpoint
		p.Name += " · " + v.Name
	}
	if len(v.Networks) > 0 {
		p.Tuning.MaxEarlyData = 0
	}
	return p
}
