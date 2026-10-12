package main

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var errPanelMissing = errors.New("remote account missing")

func privatePanelsAllowed() bool { return envDefault("ARIA_ALLOW_PRIVATE_PANELS", "") == "true" }
func cgnIP(ip net.IP) bool {
	v := ip.To4()
	return v != nil && v[0] == 100 && v[1] >= 64 && v[1] <= 127
}
func blockedPanelIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.ParseIP("100.100.100.200")) || cgnIP(ip)
}
func (a *App) validateNode(n PanelNode, credentials bool) error {
	if !safeLabel(n.Name, 64) || n.Type != "aria" && n.Type != "3x-ui" && n.Type != "marzban" {
		return fmt.Errorf("نوع پنل باید aria، 3x-ui 3.8.5 یا Marzban کلاسیک باشد")
	}
	u, e := url.Parse(n.URL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("آدرس پایهٔ پنل معتبر نیست؛ مسیر پنل مجاز است")
	}
	if u.Scheme != "https" && !(privatePanelsAllowed() && u.Scheme == "http") {
		return fmt.Errorf("اتصال پنل مقصد باید HTTPS با گواهی معتبر باشد")
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || h == "metadata.google.internal" || h == "metadata" {
		if !privatePanelsAllowed() {
			return fmt.Errorf("آدرس داخلی مجاز نیست")
		}
	}
	if ip := net.ParseIP(h); ip != nil && blockedPanelIP(ip) && !privatePanelsAllowed() {
		return fmt.Errorf("آدرس خصوصی مجاز نیست؛ برای شبکهٔ تحت مالکیت، ARIA_ALLOW_PRIVATE_PANELS را تنظیم کن")
	}
	if n.PublicURL != "" {
		if _, e = validatePublicURL(n.PublicURL); e != nil {
			return e
		}
	}
	if len(n.Token) > 2000 || len(n.Password) > 256 || len(n.Username) > 64 || len(n.InboundIDs) > 14 || len(n.Tags) > 4 {
		return fmt.Errorf("اطلاعات اتصال بیش از حد طولانی است")
	}
	for _, id := range n.InboundIDs {
		if id <= 0 {
			return fmt.Errorf("شمارهٔ اینباند باید مثبت باشد")
		}
	}
	for protocol, tags := range n.Tags {
		if protocol != "vless" && protocol != "vmess" && protocol != "trojan" && protocol != "shadowsocks" || len(tags) > 30 {
			return fmt.Errorf("تگ اینباند معتبر نیست")
		}
		for _, tag := range tags {
			if !safeLabel(tag, 128) {
				return fmt.Errorf("تگ معتبر نیست")
			}
		}
	}
	if credentials {
		if n.Type == "aria" && n.Token == "" {
			return fmt.Errorf("API Key پنل aria لازم است")
		}
		if n.Type == "3x-ui" && (n.Username == "" || n.Password == "" || len(n.InboundIDs) == 0) {
			return fmt.Errorf("نام کاربری، رمز و ID اینباندهای 3x-ui لازم است")
		}
		if n.Type == "marzban" && n.Token == "" && (n.Username == "" || n.Password == "") {
			return fmt.Errorf("توکن یا نام کاربری و رمز Marzban لازم است")
		}
	}
	return nil
}
func (a *App) savePanelNode(n PanelNode) error {
	return a.salesTxn(func(s *SalesData) error {
		i := nodeIndex(s, n.ID)
		if n.ID != "" && i < 0 {
			return fmt.Errorf("پنل یافت نشد")
		}
		if i >= 0 {
			old := s.Nodes[i]
			if old.URL == n.URL && old.Type == n.Type {
				if n.Token == "" {
					n.Token = old.Token
				}
				if n.Password == "" {
					n.Password = old.Password
				}
			}
			for _, service := range s.Services {
				if service.NodeID == n.ID && (old.URL != n.URL || old.Type != n.Type || old.PublicURL != n.PublicURL || old.Edge != n.Edge || marshalString(old.InboundIDs) != marshalString(n.InboundIDs) || marshalString(old.Tags) != marshalString(n.Tags)) {
					return fmt.Errorf("مقصد پنل دارای سرویس قابل جابه‌جایی نیست؛ یک پنل تازه ثبت کن")
				}
			}
		}
		n.URL = strings.TrimRight(strings.TrimSpace(n.URL), "/")
		if err := a.validateNode(n, n.Enabled); err != nil {
			return err
		}
		n.LastError = ""
		n.LastTest = 0
		n.LastOK = false
		if n.ID == "" {
			if len(s.Nodes) >= 50 {
				return fmt.Errorf("سقف پنل‌ها پر است")
			}
			n.ID = "n-" + randomToken(6)
			s.Nodes = append(s.Nodes, n)
		} else {
			s.Nodes[i] = n
		}
		return nil
	})
}
func (a *App) node(id string) (PanelNode, error) {
	s := a.salesSnapshot()
	i := nodeIndex(&s, id)
	if i < 0 {
		return PanelNode{}, fmt.Errorf("پنل مقصد یافت نشد")
	}
	n := s.Nodes[i]
	if !n.Enabled {
		return n, fmt.Errorf("پنل مقصد غیرفعال است")
	}
	return n, nil
}
func (a *App) nodeClient() *http.Client {
	if a.panelHTTP != nil {
		return a.panelHTTP
	}
	d := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("DNS مقصد پاسخ نداد")
		}
		for _, ip := range ips {
			if blockedPanelIP(ip.IP) && !privatePanelsAllowed() {
				return nil, fmt.Errorf("DNS به آدرس خصوصی/محلی اشاره می‌کند")
			}
		}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	return &http.Client{Timeout: 20 * time.Second, Transport: tr, CheckRedirect: noRedirect}
}
func remoteRequest(ctx context.Context, cl *http.Client, n PanelNode, method, path string, body any, headers map[string]string, out any) error {
	var rd io.Reader
	switch v := body.(type) {
	case url.Values:
		rd = strings.NewReader(v.Encode())
	case nil:
	default:
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		rd = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, n.URL+path, rd)
	if e != nil {
		return fmt.Errorf("درخواست پنل معتبر نیست")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		if _, ok := body.(url.Values); ok {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, e := cl.Do(req)
	if e != nil {
		return fmt.Errorf("ارتباط امن با پنل مقصد برقرار نشد")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return errPanelMissing
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("پنل مقصد HTTP %d؛ جزئیات را در لاگ همان پنل بررسی کن", resp.StatusCode)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out) != nil {
		return fmt.Errorf("پاسخ JSON پنل مقصد معتبر نیست")
	}
	return nil
}
func (a *App) marzAuth(ctx context.Context, n PanelNode) (map[string]string, error) {
	token := n.Token
	if token == "" {
		var auth struct {
			Token string `json:"access_token"`
		}
		err := remoteRequest(ctx, a.nodeClient(), n, "POST", "/api/admin/token", url.Values{"username": {n.Username}, "password": {n.Password}}, nil, &auth)
		if err != nil {
			return nil, err
		}
		if auth.Token == "" {
			return nil, fmt.Errorf("پنل توکن ورود نداد")
		}
		token = auth.Token
	}
	return map[string]string{"Authorization": "Bearer " + token}, nil
}
func (a *App) remoteEngine(ctx context.Context, n PanelNode) (*EngineClient, error) {
	e := newEngineClient(n.URL)
	e.http.Transport = a.nodeClient().Transport
	if e.http.Transport == nil {
		e.http.Transport = http.DefaultTransport
	}
	if err := e.login(ctx, n.Username, n.Password, ""); err != nil {
		return nil, fmt.Errorf("ورود امن 3x-ui انجام نشد؛ نسخهٔ 3.8.5 و مسیر کامل را بررسی کن")
	}
	return e, nil
}
func (a *App) testPanelNode(ctx context.Context, id string) (any, error) {
	n, err := a.node(id)
	if err != nil {
		return nil, err
	}
	var resources any
	started := time.Now()
	switch n.Type {
	case "aria":
		var r struct {
			Profiles []Profile `json:"profiles"`
		}
		err = remoteRequest(ctx, a.nodeClient(), n, "GET", "/api/v1/info", nil, map[string]string{"Authorization": "Bearer " + n.Token}, &r)
		resources = r.Profiles
	case "marzban":
		var h map[string]string
		h, err = a.marzAuth(ctx, n)
		if err == nil {
			var r map[string]any
			err = remoteRequest(ctx, a.nodeClient(), n, "GET", "/api/inbounds", nil, h, &r)
			resources = r
		}
	case "3x-ui":
		var e *EngineClient
		e, err = a.remoteEngine(ctx, n)
		if err == nil {
			var all []Inbound
			all, err = e.inbounds(ctx)
			rows := []map[string]any{}
			for _, ib := range all {
				rows = append(rows, map[string]any{"id": ib.ID, "name": ib.Remark, "protocol": ib.Protocol, "port": ib.Port, "enabled": ib.Enable})
			}
			resources = rows
		}
	}
	note := ""
	if err != nil {
		note = err.Error()
	}
	_ = a.salesTxn(func(s *SalesData) error {
		i := nodeIndex(s, id)
		if i >= 0 && s.Nodes[i].URL == n.URL {
			s.Nodes[i].LastTest = time.Now().UnixMilli()
			s.Nodes[i].LastOK = err == nil
			s.Nodes[i].LastError = note
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "ms": time.Since(started).Milliseconds(), "resources": resources}, nil
}
func inputClient(p ProvisionInput) map[string]any {
	return map[string]any{"id": p.UUID, "password": p.Password, "auth": p.Auth, "email": p.Email, "subId": p.SubID, "enable": true, "security": "auto", "flow": clientFlow(p.Profiles), "totalGB": p.Quota, "expiryTime": p.Expiry, "limitIp": 0, "comment": p.Name, "tgId": 0, "reset": 0, "trafficReset": "never"}
}
func serviceFromUser(u User) SoldService {
	links := []string{}
	for _, l := range u.Links {
		if l.Link != "" {
			links = append(links, l.Link)
		}
	}
	return SoldService{Email: u.Email, Name: u.Name, Subscription: u.Subscription, Links: links, Quota: u.Quota, Expiry: u.Expiry, Used: u.Up + u.Down, Enabled: u.Enabled, Created: time.Now().UnixMilli(), Updated: time.Now().UnixMilli()}
}
func (a *App) provisionLocal(ctx context.Context, p ProvisionInput) (SoldService, error) {
	a.mutation.Lock()
	defer a.mutation.Unlock()
	if err := a.refresh(ctx); err != nil {
		return SoldService{}, fmt.Errorf("ارتباط داخلی هسته آماده نیست")
	}
	a.mu.RLock()
	e := a.service
	a.mu.RUnlock()
	if u, ok := a.findUser(p.Email); ok {
		if textField(u.Raw, "id") != p.UUID || u.SubID != p.SubID {
			return SoldService{}, fmt.Errorf("شناسهٔ سرویس با سفارش مطابقت ندارد")
		}
		return serviceFromUser(u), nil
	}
	ids := []int{}
	for _, key := range p.Profiles {
		ib, err := a.ensureProfile(ctx, e, key)
		if err != nil {
			return SoldService{}, err
		}
		ids = append(ids, ib.InboundID)
	}
	if err := e.call(ctx, "POST", "/panel/api/clients/add", map[string]any{"client": inputClient(p), "inboundIds": ids}, nil); err != nil {
		return SoldService{}, err
	}
	if err := a.refresh(ctx); err != nil {
		return SoldService{}, err
	}
	u, ok := a.findUser(p.Email)
	if !ok {
		return SoldService{}, fmt.Errorf("ساخت سرویس هنوز در هسته تأیید نشد")
	}
	return serviceFromUser(u), nil
}
func (a *App) provisionPlan(ctx context.Context, plan SalesPlan, p ProvisionInput) (SoldService, error) {
	if plan.NodeID == "" || plan.NodeID == "local" {
		return a.provisionLocal(ctx, p)
	}
	n, err := a.node(plan.NodeID)
	if err != nil {
		return SoldService{}, err
	}
	switch n.Type {
	case "aria":
		var s SoldService
		err = remoteRequest(ctx, a.nodeClient(), n, "POST", "/api/v1/clients", p, map[string]string{"Authorization": "Bearer " + n.Token, "Idempotency-Key": p.OperationID}, &s)
		return s, err
	case "marzban":
		return a.provisionMarzban(ctx, n, p)
	case "3x-ui":
		return a.provisionXUI(ctx, n, p)
	}
	return SoldService{}, fmt.Errorf("نوع مقصد پشتیبانی نمی‌شود")
}

type MarzUser struct {
	Username     string   `json:"username"`
	Note         string   `json:"note"`
	Status       string   `json:"status"`
	DataLimit    int64    `json:"data_limit"`
	Used         int64    `json:"used_traffic"`
	Expire       int64    `json:"expire"`
	Subscription string   `json:"subscription_url"`
	Links        []string `json:"links"`
}

func marzService(n PanelNode, u MarzUser) (SoldService, error) {
	sub := u.Subscription
	if sub != "" {
		base, _ := url.Parse(n.URL + "/")
		p, e := url.Parse(sub)
		if e != nil {
			return SoldService{}, fmt.Errorf("اشتراک مقصد معتبر نیست")
		}
		p = base.ResolveReference(p)
		if p.Scheme != "https" && !(privatePanelsAllowed() && p.Scheme == "http") {
			return SoldService{}, fmt.Errorf("اشتراک مقصد TLS ندارد")
		}
		sub = p.String()
	}
	if len(u.Links) == 0 && sub == "" {
		return SoldService{}, fmt.Errorf("پنل مقصد لینک آماده نداد")
	}
	return SoldService{Email: u.Username, Name: u.Note, Links: u.Links, Subscription: sub, Quota: u.DataLimit, Expiry: u.Expire * 1000, Used: u.Used, Enabled: u.Status == "active", Created: time.Now().UnixMilli(), Updated: time.Now().UnixMilli()}, nil
}
func (a *App) provisionMarzban(ctx context.Context, n PanelNode, p ProvisionInput) (SoldService, error) {
	h, err := a.marzAuth(ctx, n)
	if err != nil {
		return SoldService{}, err
	}
	username := strings.ReplaceAll(p.Email, "-", "_")
	var u MarzUser
	err = remoteRequest(ctx, a.nodeClient(), n, "GET", "/api/user/"+url.PathEscape(username), nil, h, &u)
	if err == nil {
		if u.Username != username || u.Note != "aria-order:"+p.OperationID {
			return SoldService{}, fmt.Errorf("شناسهٔ موجود به این سفارش تعلق ندارد")
		}
		return marzService(n, u)
	}
	if !errors.Is(err, errPanelMissing) {
		return SoldService{}, err
	}
	protocols := map[string]any{}
	for _, key := range p.Profiles {
		protocol := strings.Split(key, "-")[0]
		switch protocol {
		case "vless", "vmess":
			protocols[protocol] = map[string]string{"id": p.UUID}
		case "trojan", "shadowsocks":
			protocols[protocol] = map[string]string{"password": p.Password}
		default:
			return SoldService{}, fmt.Errorf("Marzban کلاسیک این پروتکل را از این رابط پشتیبانی نمی‌کند")
		}
	}
	body := map[string]any{"username": username, "status": "active", "proxies": protocols, "data_limit": p.Quota, "data_limit_reset_strategy": "no_reset", "expire": p.Expiry / 1000, "note": "aria-order:" + p.OperationID}
	if len(n.Tags) > 0 {
		body["inbounds"] = n.Tags
	}
	if err = remoteRequest(ctx, a.nodeClient(), n, "POST", "/api/user", body, h, &u); err != nil {
		return SoldService{}, err
	}
	if u.Username != username {
		return SoldService{}, fmt.Errorf("نام ساخته‌شده مطابقت ندارد")
	}
	return marzService(n, u)
}
func (a *App) provisionXUI(ctx context.Context, n PanelNode, p ProvisionInput) (SoldService, error) {
	e, err := a.remoteEngine(ctx, n)
	if err != nil {
		return SoldService{}, err
	}
	all, err := e.inbounds(ctx)
	if err != nil {
		return SoldService{}, err
	}
	for _, ib := range all {
		var st struct {
			Clients []map[string]any `json:"clients"`
		}
		_ = nestedJSON(ib.Settings, &st)
		for _, c := range st.Clients {
			if textField(c, "email") == p.Email {
				if textField(c, "id") != p.UUID || textField(c, "subId") != p.SubID {
					return SoldService{}, fmt.Errorf("شناسهٔ موجود با سفارش مطابقت ندارد")
				}
				return xuiService(n, all, p.Email)
			}
		}
	}
	for _, id := range n.InboundIDs {
		found := false
		for _, ib := range all {
			if ib.ID == id && ib.Enable {

				// Verify the output can be generated before creating a paid service.
				check := ib
				check.Settings = json.RawMessage(marshalString(map[string]any{"clients": []any{inputClient(p)}}))
				if _, err := xuiService(n, []Inbound{check}, p.Email); err != nil {
					return SoldService{}, err
				}
				found = true
			}
		}
		if !found {
			return SoldService{}, fmt.Errorf("اینباند انتخاب‌شده خاموش یا حذف شده است")
		}
	}
	if err = e.call(ctx, "POST", "/panel/api/clients/add", map[string]any{"client": inputClient(p), "inboundIds": n.InboundIDs}, nil); err != nil {
		return SoldService{}, err
	}
	all, err = e.inbounds(ctx)
	if err != nil {
		return SoldService{}, err
	}
	return xuiService(n, all, p.Email)
}
func xuiService(n PanelNode, all []Inbound, email string) (SoldService, error) {
	s := SoldService{Email: email, Links: []string{}, Created: time.Now().UnixMilli(), Updated: time.Now().UnixMilli()}
	origin := n.PublicURL
	if origin == "" {
		u, _ := url.Parse(n.URL)
		origin = u.Scheme + "://" + u.Host
	}
	for _, ib := range all {
		var settings struct {
			Clients []map[string]any `json:"clients"`
		}
		_ = nestedJSON(ib.Settings, &settings)
		for _, c := range settings.Clients {
			if textField(c, "email") != email {
				continue
			}
			s.Name = textField(c, "comment")
			s.Quota = numField(c, "totalGB")
			s.Expiry = numField(c, "expiryTime")
			s.Enabled = boolField(c, "enable")
			var stream map[string]any
			_ = nestedJSON(ib.Stream, &stream)
			network := textField(stream, "network")
			security := textField(stream, "security")
			if security == "" {
				security = "none"
			}
			p := Profile{Name: ib.Remark, Protocol: ib.Protocol, Network: network, Security: security, Port: ib.Port, Direct: !n.Edge}
			ns, _ := stream[network+"Settings"].(map[string]any)
			p.Path = textField(ns, "path")
			if network == "grpc" {
				p.Path = textField(ns, "serviceName")
			}
			tls, _ := stream["tlsSettings"].(map[string]any)
			p.ServerName = textField(tls, "serverName")
			if security == "reality" {
				re, _ := stream["realitySettings"].(map[string]any)
				opts, _ := re["settings"].(map[string]any)
				p.PublicKey = textField(opts, "publicKey")
				if p.PublicKey == "" {
					if b, e := base64.RawURLEncoding.DecodeString(textField(re, "privateKey")); e == nil {
						if k, e := ecdh.X25519().NewPrivateKey(b); e == nil {
							p.PublicKey = base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes())
						}
					}
				}
				if names, ok := re["serverNames"].([]any); ok && len(names) > 0 {
					p.ServerName, _ = names[0].(string)
				}
				if ids, ok := re["shortIds"].([]any); ok && len(ids) > 0 {
					p.ShortID, _ = ids[0].(string)
				}
			}
			if ib.Protocol != "vless" && ib.Protocol != "vmess" && ib.Protocol != "trojan" && ib.Protocol != "shadowsocks" {
				return s, fmt.Errorf("اینباند مقصد برای خروجی خودکار پشتیبانی نمی‌شود")
			}

			if network != "tcp" && network != "ws" && network != "grpc" && network != "xhttp" {
				return s, fmt.Errorf("انتقال مقصد برای خروجی خودکار پشتیبانی نمی‌شود")
			}
			if n.Edge && (network != "ws" && network != "xhttp" || ib.Protocol == "shadowsocks") {
				return s, fmt.Errorf("مقصد پشت HTTPS تنها WS/XHTTP را می‌پذیرد")
			}
			if security != "none" && security != "tls" && security != "reality" {
				return s, fmt.Errorf("امنیت مقصد پشتیبانی نمی‌شود")
			}
			if ib.Protocol == "vmess" && security == "reality" {
				return s, fmt.Errorf("VMess با REALITY برای خروجی خودکار پشتیبانی نمی‌شود")
			}
			link := shareLink(p, c, origin)

			if !n.Edge && ib.Protocol == "vmess" {
				host, _, _ := publicEndpoint(origin)
				tlsValue := ""
				if security == "tls" {
					tlsValue = "tls"
				}
				data := map[string]any{"v": "2", "ps": s.Name + " · " + ib.Remark, "add": host, "port": strconv.Itoa(ib.Port), "id": textField(c, "id"), "aid": "0", "scy": "auto", "net": network, "type": "none", "tls": tlsValue, "sni": p.ServerName, "fp": "chrome", "path": p.Path}
				headers, _ := ns["headers"].(map[string]any)
				h := textField(headers, "Host")
				if h == "" {
					h = host
				}
				data["host"] = h
				b, _ := json.Marshal(data)
				link = "vmess://" + base64.StdEncoding.EncodeToString(b)
			}
			if ib.Protocol == "shadowsocks" {
				var st map[string]any
				_ = nestedJSON(ib.Settings, &st)
				method := textField(st, "method")
				if method != "chacha20-ietf-poly1305" && method != "aes-128-gcm" && method != "aes-256-gcm" {
					return s, fmt.Errorf("فقط روش‌های کلاسیک Shadowsocks برای مقصد پشتیبانی می‌شوند")
				}
				host, _, _ := publicEndpoint(origin)
				link = "ss://" + base64.RawURLEncoding.EncodeToString([]byte(method+":"+textField(c, "password"))) + "@" + net.JoinHostPort(host, strconv.Itoa(ib.Port)) + "#" + url.PathEscape(s.Name+" · "+ib.Remark)
			}
			if !n.Edge && (ib.Protocol == "vless" || ib.Protocol == "trojan") {
				u, _ := url.Parse(link)
				q := u.Query()
				if ib.Protocol == "vless" && network == "tcp" {
					if flow := textField(c, "flow"); flow != "" {
						q.Set("flow", flow)
					} else {
						q.Del("flow")
					}
				}
				if network == "grpc" {
					q.Set("serviceName", p.Path)
				}
				u.RawQuery = q.Encode()
				link = u.String()
			}
			if network == "ws" && !n.Edge && ib.Protocol != "vmess" {
				u, _ := url.Parse(link)
				q := u.Query()
				q.Set("path", p.Path)
				h, _, _ := publicEndpoint(origin)
				q.Set("host", h)
				u.RawQuery = q.Encode()
				link = u.String()
			}
			s.Links = append(s.Links, link)
			for _, tr := range ib.Stats {
				if tr.Email == email && tr.Up+tr.Down > s.Used {
					s.Used = tr.Up + tr.Down
				}
			}
		}
	}
	if len(s.Links) == 0 {
		return s, fmt.Errorf("سرویس هنوز در اینباندها دیده نمی‌شود")
	}
	return s, nil
}
func (a *App) getRemoteService(ctx context.Context, nodeID, email string) (SoldService, error) {
	if nodeID == "" || nodeID == "local" {
		if err := a.refresh(ctx); err != nil {
			return SoldService{}, err
		}
		u, ok := a.findUser(email)
		if !ok {
			return SoldService{}, errPanelMissing
		}
		return serviceFromUser(u), nil
	}
	n, err := a.node(nodeID)
	if err != nil {
		return SoldService{}, err
	}
	switch n.Type {
	case "aria":
		var s SoldService
		err = remoteRequest(ctx, a.nodeClient(), n, "GET", "/api/v1/clients/"+url.PathEscape(email), nil, map[string]string{"Authorization": "Bearer " + n.Token}, &s)
		return s, err
	case "marzban":
		h, err := a.marzAuth(ctx, n)
		if err != nil {
			return SoldService{}, err
		}
		var u MarzUser
		err = remoteRequest(ctx, a.nodeClient(), n, "GET", "/api/user/"+url.PathEscape(email), nil, h, &u)
		if err != nil {
			return SoldService{}, err
		}
		return marzService(n, u)
	case "3x-ui":
		e, err := a.remoteEngine(ctx, n)
		if err != nil {
			return SoldService{}, err
		}
		all, err := e.inbounds(ctx)
		if err != nil {
			return SoldService{}, err
		}
		return xuiService(n, all, email)
	}
	return SoldService{}, fmt.Errorf("نوع مقصد نامعتبر است")
}
func (a *App) updateRemoteService(ctx context.Context, nodeID, email string, quota, expiry int64, enabled bool) error {
	if nodeID == "" || nodeID == "local" {
		a.mutation.Lock()
		defer a.mutation.Unlock()
		if err := a.refresh(ctx); err != nil {
			return err
		}
		u, ok := a.findUser(email)
		if !ok {
			return errPanelMissing
		}
		c := map[string]any{}
		for k, v := range u.Raw {
			c[k] = v
		}
		c["totalGB"] = quota
		c["expiryTime"] = expiry
		c["enable"] = enabled
		a.mu.RLock()
		e := a.service
		a.mu.RUnlock()
		if err := e.clientCall(ctx, "update", email, c); err != nil {
			return err
		}
		return a.refresh(ctx)
	}
	n, err := a.node(nodeID)
	if err != nil {
		return err
	}
	switch n.Type {
	case "aria":
		return remoteRequest(ctx, a.nodeClient(), n, "POST", "/api/v1/clients/"+url.PathEscape(email), map[string]any{"quota": quota, "expiry": expiry, "enabled": enabled}, map[string]string{"Authorization": "Bearer " + n.Token}, nil)
	case "marzban":
		h, err := a.marzAuth(ctx, n)
		if err != nil {
			return err
		}
		status := "disabled"
		if enabled {
			status = "active"
		}
		return remoteRequest(ctx, a.nodeClient(), n, "PUT", "/api/user/"+url.PathEscape(email), map[string]any{"data_limit": quota, "expire": expiry / 1000, "status": status}, h, nil)
	case "3x-ui":
		e, err := a.remoteEngine(ctx, n)
		if err != nil {
			return err
		}
		all, err := e.inbounds(ctx)
		if err != nil {
			return err
		}
		for _, ib := range all {
			var st struct {
				Clients []map[string]any `json:"clients"`
			}
			_ = nestedJSON(ib.Settings, &st)
			for _, c := range st.Clients {
				if textField(c, "email") == email {
					c["totalGB"] = quota
					c["expiryTime"] = expiry
					c["enable"] = enabled
					return e.clientCall(ctx, "update", email, c)
				}
			}
		}
		return errPanelMissing
	}
	return fmt.Errorf("نوع مقصد نامعتبر است")
}
func (a *App) renewSoldService(ctx context.Context, o SalesOrder) (SoldService, error) {
	s := a.salesSnapshot()
	si := serviceIndex(&s, o.ServiceID)
	if si < 0 {
		return SoldService{}, fmt.Errorf("سرویس تمدید یافت نشد")
	}
	saved := s.Services[si]
	desired := o.Provision
	if !o.DesiredReady {
		live, err := a.getRemoteService(ctx, saved.NodeID, saved.Email)
		if err != nil {
			return SoldService{}, err
		}
		desired.Quota = live.Quota + int64(o.Plan.QuotaGB*(1<<30))
		if live.Quota == 0 || o.Plan.QuotaGB == 0 {
			desired.Quota = 0
		}
		desired.Expiry = live.Expiry
		if desired.Expiry < time.Now().UnixMilli() {
			desired.Expiry = time.Now().UnixMilli()
		}
		desired.Expiry += int64(o.Plan.Days) * int64(24*time.Hour/time.Millisecond)
		if o.Plan.Days == 0 || live.Expiry == 0 {
			desired.Expiry = 0
		}
		if desired.Quota < 0 || desired.Quota > 1e6*(1<<30) {
			return SoldService{}, fmt.Errorf("سقف سهمیه پر شده است")
		}
		if err = a.salesTxn(func(s *SalesData) error {
			i := orderIndex(s, o.ID)
			s.Orders[i].Provision = desired
			s.Orders[i].DesiredReady = true
			return nil
		}); err != nil {
			return SoldService{}, err
		}
	}
	if err := a.updateRemoteService(ctx, saved.NodeID, saved.Email, desired.Quota, desired.Expiry, true); err != nil {
		return SoldService{}, err
	}
	updated, err := a.getRemoteService(ctx, saved.NodeID, saved.Email)
	if err != nil {
		return SoldService{}, err
	}
	if updated.Quota != desired.Quota || updated.Expiry/1000 != desired.Expiry/1000 {
		return SoldService{}, fmt.Errorf("تمدید هنوز در مقصد تأیید نشد")
	}
	updated.ID = saved.ID
	updated.Created = saved.Created
	return updated, nil
}
func (a *App) syncSoldService(ctx context.Context, id string) (SoldService, error) {
	a.fulfillMu.Lock()
	defer a.fulfillMu.Unlock()
	return a.syncSoldServiceLocked(ctx, id)
}
func (a *App) syncSoldServiceLocked(ctx context.Context, id string) (SoldService, error) {
	s := a.salesSnapshot()
	i := serviceIndex(&s, id)
	if i < 0 {
		return SoldService{}, fmt.Errorf("سرویس یافت نشد")
	}
	old := s.Services[i]
	live, err := a.getRemoteService(ctx, old.NodeID, old.Email)
	if err != nil {
		return old, err
	}
	live.ID = old.ID
	live.OrderID = old.OrderID
	live.CustomerID = old.CustomerID
	live.PlanID = old.PlanID
	live.NodeID = old.NodeID
	live.Created = old.Created
	live.Reminders = old.Reminders
	live.ReminderChecked = old.ReminderChecked
	err = a.salesTxn(func(s *SalesData) error { i := serviceIndex(s, id); s.Services[i] = live; return nil })
	return live, err
}
func (a *App) toggleSoldService(ctx context.Context, id string) error {
	a.fulfillMu.Lock()
	defer a.fulfillMu.Unlock()
	s := a.salesSnapshot()
	i := serviceIndex(&s, id)
	if i < 0 {
		return fmt.Errorf("سرویس یافت نشد")
	}
	live, err := a.getRemoteService(ctx, s.Services[i].NodeID, s.Services[i].Email)
	if err != nil {
		return err
	}
	if err = a.updateRemoteService(ctx, s.Services[i].NodeID, live.Email, live.Quota, live.Expiry, !live.Enabled); err != nil {
		return err
	}
	_, err = a.syncSoldServiceLocked(ctx, id)
	return err
}
func validProvision(p ProvisionInput) bool {
	return regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(p.OperationID) && regexp.MustCompile(`^aria-[a-f0-9]{12}$`).MatchString(p.Email) && regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`).MatchString(p.UUID) && regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(p.SubID) && regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(p.Password) && regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(p.Auth) && p.Quota >= 0 && p.Quota <= 1e6*(1<<30) && p.Expiry >= 0 && p.Expiry < time.Now().Add(3660*24*time.Hour).UnixMilli() && validateCreate(CreateRequest{Name: p.Name, Count: 1, QuotaGB: float64(p.Quota) / (1 << 30), Profiles: p.Profiles}) == nil
}
