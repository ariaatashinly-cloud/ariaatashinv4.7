package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var integrationScopes = []string{"clients:read", "clients:write", "sales:read", "orders:write", "payments:approve"}

func hasScope(k IntegrationKey, scope string) bool {
	for _, s := range k.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}
func (a *App) createIntegrationKey(w http.ResponseWriter, r *http.Request) (any, error) {
	var in IntegrationKey
	if readBody(w, r, &in, 4096) != nil || !safeLabel(in.Name, 64) || len(in.Scopes) < 1 || len(in.Scopes) > len(integrationScopes) {
		return nil, fmt.Errorf("نام و دسترسی‌های کلید معتبر نیست")
	}
	seen := map[string]bool{}
	for _, s := range in.Scopes {
		valid := false
		for _, v := range integrationScopes {
			if v == s {
				valid = true
			}
		}
		if !valid || seen[s] {
			return nil, fmt.Errorf("دسترسی نامعتبر یا تکراری")
		}
		seen[s] = true
	}
	if in.Expires == 0 {
		in.Expires = time.Now().Add(90 * 24 * time.Hour).UnixMilli()
	}
	if in.Expires <= time.Now().UnixMilli() || in.Expires > time.Now().Add(366*24*time.Hour).UnixMilli() {
		return nil, fmt.Errorf("انقضا باید در یک سال آینده باشد")
	}
	in.ID = "k-" + randomToken(6)
	in.Created = time.Now().UnixMilli()
	in.Revoked = false
	token := "aria_pk_" + in.ID + "." + randomToken(32)
	in.Hash = hashToken(token)
	if err := a.salesTxn(func(s *SalesData) error {
		if len(s.Keys) >= 100 {
			return fmt.Errorf("سقف کلیدها پر است")
		}
		s.Keys = append(s.Keys, in)
		return nil
	}); err != nil {
		return nil, err
	}
	in.Hash = ""
	return map[string]any{"key": token, "metadata": in, "note": "این کلید فقط همین بار نمایش داده می‌شود؛ در URL یا مخزن عمومی نگذار."}, nil
}
func (a *App) integrationKey(token string) (IntegrationKey, bool) {
	if len(token) > 200 || !strings.HasPrefix(token, "aria_pk_k-") {
		return IntegrationKey{}, false
	}
	hash := hashToken(token)
	s := a.salesSnapshot()
	for _, k := range s.Keys {
		if !k.Revoked && k.Expires > time.Now().UnixMilli() && subtle.ConstantTimeCompare([]byte(hash), []byte(k.Hash)) == 1 {
			return k, true
		}
	}
	return IntegrationKey{}, false
}
func (a *App) integrationLimit(key string, max int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.attempts[key]
	if t.Until.Before(time.Now()) {
		t = attempt{Until: time.Now().Add(time.Minute)}
	}
	t.Count++
	a.attempts[key] = t
	return t.Count <= max
}
func (a *App) isCompatRoute(path string) bool {
	return path == "/api/system" || path == "/api/inbounds" || path == "/api/admin/token" || path == "/api/user" || strings.HasPrefix(path, "/api/user/")
}
func (a *App) integrationAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	compat := a.isCompatRoute(r.URL.Path)
	if compat && !a.salesSnapshot().Settings.CompatEnabled {
		apiError(w, 404, "رابط Marzban فعال نیست؛ مدیر باید آن را در فروش فعال کند")
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.URL.Path == "/api/admin/token" && r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if r.ParseForm() != nil {
			apiError(w, 400, "فرم معتبر نیست")
			return
		}
		if r.Form.Get("username") != "api" {
			apiError(w, 401, "نام کاربری API باید api باشد")
			return
		}
		token = r.Form.Get("password")
	}
	k, ok := a.integrationKey(token)
	if !ok {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if !a.integrationLimit("api-invalid:"+host, 100) {
			w.Header().Set("Retry-After", "60")
			apiError(w, 429, "تلاش احراز هویت زیاد است")
			return
		}
		apiError(w, 401, "API Key معتبر و فعال لازم است")
		return
	}
	if !a.integrationLimit("api:"+k.ID, 120) {
		w.Header().Set("Retry-After", "60")
		apiError(w, 429, "سقف API در دقیقه پر شد")
		return
	}
	if r.URL.Path == "/api/admin/token" && r.Method == "POST" {
		jsonReply(w, 200, map[string]any{"access_token": token, "token_type": "bearer"})
		return
	}
	scope := "clients:read"
	if r.Method != "GET" {
		scope = "clients:write"
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/sales") {
		scope = "sales:read"
	}
	if r.URL.Path == "/api/v1/orders" {
		scope = "orders:write"
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/payments/") {
		scope = "payments:approve"
	}
	if !hasScope(k, scope) {
		apiError(w, 403, "این کلید دسترسی "+scope+" ندارد")
		return
	}
	if r.URL.Path == "/api/v1/info" && r.Method == "GET" {
		jsonReply(w, 200, map[string]any{"name": "ariaatashin", "version": "4.0.0", "ready": a.snapshot().Ready, "profiles": a.profileCatalog(), "capabilities": []string{"create", "read", "update", "delete", "reset", "revoke", "idempotency"}})
		return
	}
	if r.URL.Path == "/api/system" && r.Method == "GET" {
		s := a.snapshot()
		jsonReply(w, 200, map[string]any{"version": "ariaatashin-4.0.0", "total_user": len(s.Users), "users_active": len(s.Users), "ready": s.Ready})
		return
	}
	if r.URL.Path == "/api/inbounds" && r.Method == "GET" {
		out := map[string][]map[string]any{}
		for _, p := range a.profileCatalog() {
			if p.Available {
				out[p.Protocol] = append(out[p.Protocol], map[string]any{"tag": "aria-" + p.Key, "protocol": p.Protocol, "network": p.Network, "port": p.Port})
			}
		}
		jsonReply(w, 200, out)
		return
	}
	if r.URL.Path == "/api/v1/sales" && r.Method == "GET" {
		s := a.salesSnapshot()
		jsonReply(w, 200, map[string]any{"settings": s.Settings, "plans": s.Plans})
		return
	}
	if r.URL.Path == "/api/v1/orders" && r.Method == "POST" {
		var in BuyRequest
		if readBody(w, r, &in, 4096) != nil {
			apiError(w, 400, "سفارش معتبر نیست")
			return
		}
		if in.Idempotency == "" {
			in.Idempotency = r.Header.Get("Idempotency-Key")
		}
		o, err := a.newOrder(in)
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		o.Provision = ProvisionInput{}
		jsonReply(w, 200, o)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/payments/") && r.Method == "POST" {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/payments/")
		var v struct {
			PaymentID string `json:"paymentId"`
			Amount    int64  `json:"amount"`
		}
		if readBody(w, r, &v, 4096) != nil || !safeLabel(v.PaymentID, 128) {
			apiError(w, 400, "شمارهٔ تراکنش لازم است")
			return
		}
		err := a.confirmPayment(id, v.PaymentID, v.Amount, "api")
		if err != nil {
			apiError(w, 409, err.Error())
			return
		}
		o, err := a.fulfillOrder(r.Context(), id)
		if err != nil {
			apiError(w, 503, err.Error())
			return
		}
		o.Provision = ProvisionInput{}
		jsonReply(w, 200, o)
		return
	}
	if compat {
		a.compatUsers(w, r, k)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/clients")
	if r.URL.Path == "/api/v1/clients" && r.Method == "POST" {
		var p ProvisionInput
		if readBody(w, r, &p, 8192) != nil || !validProvision(p) {
			apiError(w, 400, "ProvisionInput معتبر و کامل لازم است؛ مستندات API را ببین")
			return
		}
		out, err := a.createAPIAccount(r.Context(), k, p.Email, p)
		if err != nil {
			apiError(w, 409, err.Error())
			return
		}
		jsonReply(w, 200, out)
		return
	}
	if r.URL.Path == "/api/v1/clients" && r.Method == "GET" {
		out := []SoldService{}
		for _, acc := range a.salesSnapshot().Accounts {
			if acc.KeyID == k.ID {
				if u, ok := a.findUser(acc.Input.Email); ok {
					out = append(out, serviceFromUser(u))
				}
			}
		}
		jsonReply(w, 200, out)
		return
	}
	if strings.HasPrefix(path, "/") {
		name := strings.TrimPrefix(path, "/")
		acc, ok := a.apiAccount(k, name)
		if !ok {
			apiError(w, 404, "سرویس این کلید یافت نشد")
			return
		}
		a.apiAccountAction(w, r, k, acc, false)
		return
	}
	apiError(w, 404, "مسیر API یافت نشد")
}
func (a *App) apiAccount(k IntegrationKey, name string) (APIAccount, bool) {
	for _, acc := range a.salesSnapshot().Accounts {
		if acc.KeyID == k.ID && (acc.Username == name || acc.Input.Email == name) {
			return acc, true
		}
	}
	return APIAccount{}, false
}
func (a *App) createAPIAccount(ctx context.Context, k IntegrationKey, username string, p ProvisionInput) (SoldService, error) {
	if err := a.salesTxn(func(s *SalesData) error {
		for _, acc := range s.Accounts {
			if acc.Input.Email == p.Email || acc.KeyID == k.ID && acc.Username == username {
				if acc.KeyID != k.ID || acc.Input.UUID != p.UUID || acc.Input.SubID != p.SubID || acc.Input.OperationID != p.OperationID {
					return fmt.Errorf("شناسهٔ درخواست با سرویس قبلی مطابقت ندارد")
				}
				return nil
			}
		}
		s.Accounts = append(s.Accounts, APIAccount{Username: username, KeyID: k.ID, Input: p})
		return nil
	}); err != nil {
		return SoldService{}, err
	}
	return a.provisionLocal(ctx, p)
}
func (a *App) compatUsers(w http.ResponseWriter, r *http.Request, k IntegrationKey) {
	if r.URL.Path == "/api/user" && r.Method == "POST" {
		var in struct {
			Username      string                     `json:"username"`
			DataLimit     int64                      `json:"data_limit"`
			Expire        int64                      `json:"expire"`
			Note          string                     `json:"note"`
			Inbounds      map[string][]string        `json:"inbounds"`
			Proxies       map[string]json.RawMessage `json:"proxies"`
			ResetStrategy string                     `json:"data_limit_reset_strategy"`
			Status        string                     `json:"status"`
			OnHold        json.RawMessage            `json:"on_hold_expire_duration"`
		}
		if readBody(w, r, &in, 16384) != nil || !regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`).MatchString(in.Username) || in.DataLimit < 0 || in.DataLimit > 1e6*(1<<30) || in.Expire < 0 || in.Expire > time.Now().Add(3660*24*time.Hour).Unix() {
			apiError(w, 400, "درخواست Marzban کلاسیک معتبر نیست")
			return
		}
		if in.ResetStrategy != "" && in.ResetStrategy != "no_reset" || in.Status != "" && in.Status != "active" || len(in.OnHold) > 0 {
			apiError(w, 400, "تنها شروع اعتبار فوری و no_reset پشتیبانی می‌شود")
			return
		}

		var p ProvisionInput
		if acc, ok := a.apiAccount(k, in.Username); ok {
			p = acc.Input
			if p.Quota != in.DataLimit || p.Expiry/1000 != in.Expire {
				apiError(w, 409, "این نام قبلاً با سهمیه/اعتبار دیگری ثبت شده است؛ از PUT استفاده کن")
				return
			}
		} else {
			profiles := []string{}
			selected := map[string]bool{}
			for _, tags := range in.Inbounds {
				for _, tag := range tags {
					key := strings.TrimPrefix(tag, "aria-")
					if key == tag {
						apiError(w, 400, "تگ‌ها را از /api/inbounds انتخاب کن")
						return
					}
					selected[key] = true
				}
			}
			for _, tpl := range a.profileCatalog() {
				if !tpl.Available {
					continue
				}
				if len(selected) > 0 && selected[tpl.Key] || len(selected) == 0 && len(in.Proxies) > 0 && in.Proxies[tpl.Protocol] != nil || len(selected) == 0 && len(in.Proxies) == 0 && tpl.Key == "vless-ws" {
					profiles = append(profiles, tpl.Key)
				}
			}
			name := in.Note
			if !safeLabel(name, 64) {
				name = in.Username
			}
			p = newProvision(name, float64(in.DataLimit)/(1<<30), 0, profiles)
			p.Expiry = in.Expire * 1000
			p.Email = "aria-" + hashToken(k.ID + ":" + in.Username)[:12]
			if !validProvision(p) {
				apiError(w, 400, "پروتکل/تگ قابل ساخت انتخاب نشده است")
				return
			}
		}
		_, err := a.createAPIAccount(r.Context(), k, in.Username, p)
		if err != nil {
			apiError(w, 503, err.Error())
			return
		}
		u, ok := a.findUser(p.Email)
		if !ok {
			apiError(w, 503, "ساخت هنوز تأیید نشده است")
			return
		}
		jsonReply(w, 200, compatUser(in.Username, u, p))
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/user/")
	parts := strings.Split(rest, "/")
	if len(parts) > 2 || parts[0] == r.URL.Path {
		apiError(w, 404, "مسیر یافت نشد")
		return
	}
	acc, ok := a.apiAccount(k, parts[0])
	if !ok {
		apiError(w, 404, "سرویس این کلید یافت نشد")
		return
	}
	if len(parts) == 2 && r.Method == "POST" {
		if parts[1] != "reset" && parts[1] != "revoke_sub" {
			apiError(w, 404, "عملیات پشتیبانی نمی‌شود")
			return
		}
		a.integrationSpecial(w, r, acc, parts[1])
		return
	}
	a.apiAccountAction(w, r, k, acc, true)
}
func compatUser(username string, u User, p ProvisionInput) any {
	links := []string{}
	proxies := map[string]any{}
	for _, l := range u.Links {
		links = append(links, l.Link)
	}
	for _, key := range p.Profiles {
		protocol := strings.Split(key, "-")[0]
		if protocol == "vless" || protocol == "vmess" {
			proxies[protocol] = map[string]string{"id": textField(u.Raw, "id")}
		} else if protocol == "trojan" || protocol == "shadowsocks" {
			proxies[protocol] = map[string]string{"password": textField(u.Raw, "password")}
		}
	}
	return map[string]any{"username": username, "status": userStatus(u), "data_limit": u.Quota, "used_traffic": u.Up + u.Down, "expire": u.Expiry / 1000, "subscription_url": u.Subscription, "links": links, "note": u.Name, "proxies": proxies, "data_limit_reset_strategy": "no_reset"}
}
func (a *App) apiAccountAction(w http.ResponseWriter, r *http.Request, k IntegrationKey, acc APIAccount, compat bool) {
	if err := a.refresh(r.Context()); err != nil {
		apiError(w, 503, "هسته در حال بازیابی است")
		return
	}
	u, exists := a.findUser(acc.Input.Email)
	if r.Method == "GET" {
		if !exists {
			apiError(w, 404, "سرویس در هسته یافت نشد")
			return
		}
		if compat {
			jsonReply(w, 200, compatUser(acc.Username, u, acc.Input))
		} else {
			jsonReply(w, 200, serviceFromUser(u))
		}
		return
	}
	if r.Method == "DELETE" {
		a.mutation.Lock()
		if exists {
			a.mu.RLock()
			e := a.service
			a.mu.RUnlock()
			if err := e.clientCall(r.Context(), "del", u.Email, nil); err != nil {
				a.mutation.Unlock()
				apiError(w, 503, "حذف هنوز تأیید نشد")
				return
			}
			_ = a.refresh(r.Context())
		}
		a.mutation.Unlock()
		err := a.salesTxn(func(s *SalesData) error {
			out := []APIAccount{}
			for _, v := range s.Accounts {
				if v.KeyID != k.ID || v.Username != acc.Username {
					out = append(out, v)
				}
			}
			s.Accounts = out
			return nil
		})
		if err != nil {
			apiError(w, 500, err.Error())
			return
		}
		jsonReply(w, 200, map[string]bool{"ok": true})
		return
	}
	if !exists {
		apiError(w, 404, "سرویس یافت نشد")
		return
	}
	if (compat && r.Method != "PUT") || (!compat && r.Method != "POST") {
		apiError(w, 405, "روش پشتیبانی نمی‌شود")
		return
	}
	quota, expiry, enabled := u.Quota, u.Expiry, u.Enabled
	if compat {
		var in struct {
			Limit         *int64  `json:"data_limit"`
			Expire        *int64  `json:"expire"`
			Status        *string `json:"status"`
			ResetStrategy string  `json:"data_limit_reset_strategy"`
		}
		if readBody(w, r, &in, 8192) != nil {
			apiError(w, 400, "فرم معتبر نیست")
			return
		}
		if in.ResetStrategy != "" && in.ResetStrategy != "no_reset" {
			apiError(w, 400, "فقط no_reset پشتیبانی می‌شود")
			return
		}
		if in.Limit != nil {
			quota = *in.Limit
		}
		if in.Expire != nil {
			expiry = *in.Expire * 1000
		}
		if in.Status != nil {
			if *in.Status != "active" && *in.Status != "disabled" {
				apiError(w, 400, "فقط active/disabled قابل تنظیم است")
				return
			}
			enabled = *in.Status == "active"
		}
	} else {
		var in struct {
			Quota   *int64 `json:"quota"`
			Expiry  *int64 `json:"expiry"`
			Enabled *bool  `json:"enabled"`
		}
		if readBody(w, r, &in, 8192) != nil {
			apiError(w, 400, "فرم معتبر نیست")
			return
		}
		if in.Quota != nil {
			quota = *in.Quota
		}
		if in.Expiry != nil {
			expiry = *in.Expiry
		}
		if in.Enabled != nil {
			enabled = *in.Enabled
		}
	}
	if quota < 0 || quota > 1e6*(1<<30) || expiry < 0 || expiry > time.Now().Add(3660*24*time.Hour).UnixMilli() {
		apiError(w, 400, "حجم/انقضا نامعتبر است")
		return
	}
	if err := a.updateRemoteService(r.Context(), "", u.Email, quota, expiry, enabled); err != nil {
		apiError(w, 503, "تغییر هنوز در هسته تأیید نشد")
		return
	}
	if u, ok := a.findUser(u.Email); ok {
		if compat {
			jsonReply(w, 200, compatUser(acc.Username, u, acc.Input))
		} else {
			jsonReply(w, 200, serviceFromUser(u))
		}
	} else {
		apiError(w, 503, "همگام‌سازی کامل نیست")
	}
}
func (a *App) integrationSpecial(w http.ResponseWriter, r *http.Request, acc APIAccount, action string) {
	a.mutation.Lock()
	defer a.mutation.Unlock()
	if err := a.refresh(r.Context()); err != nil {
		apiError(w, 503, "هسته آماده نیست")
		return
	}
	u, ok := a.findUser(acc.Input.Email)
	if !ok {
		apiError(w, 404, "سرویس یافت نشد")
		return
	}
	a.mu.RLock()
	e := a.service
	a.mu.RUnlock()
	var err error
	if action == "reset" {
		err = e.clientCall(r.Context(), "resetTraffic", u.Email, nil)
		if err == nil && !u.Enabled {
			c := map[string]any{}
			for k, v := range u.Raw {
				c[k] = v
			}
			c["enable"] = false
			err = e.clientCall(r.Context(), "update", u.Email, c)
		}
	} else {
		var p ProvisionInput
		if err = a.salesTxn(func(s *SalesData) error {
			for i := range s.Accounts {
				if s.Accounts[i].KeyID == acc.KeyID && s.Accounts[i].Username == acc.Username {
					entry := &s.Accounts[i]
					if entry.Pending == nil {
						target := entry.Input
						target.UUID = newUUID()
						target.Password = randomToken(20)
						target.Auth = randomToken(20)
						target.SubID = randomToken(16)
						entry.Pending = &target
					}
					p = *entry.Pending
					return nil
				}
			}
			return fmt.Errorf("سرویس یافت نشد")
		}); err == nil {
			c := map[string]any{}
			for k, v := range u.Raw {
				c[k] = v
			}
			c["id"] = p.UUID
			c["password"] = p.Password
			c["auth"] = p.Auth
			c["subId"] = p.SubID
			err = e.clientCall(r.Context(), "update", u.Email, c)
			if err == nil {
				err = a.refresh(r.Context())
			}
			if err == nil {
				updated, exists := a.findUser(u.Email)
				if !exists || updated.SubID != p.SubID {
					err = fmt.Errorf("rotation not confirmed")
				}
			}
			if err == nil {
				err = a.salesTxn(func(s *SalesData) error {
					for i := range s.Accounts {
						entry := &s.Accounts[i]
						if entry.KeyID == acc.KeyID && entry.Username == acc.Username && entry.Pending != nil && entry.Pending.SubID == p.SubID {
							entry.Input, entry.Pending = p, nil
							return nil
						}
					}
					return fmt.Errorf("rotation state changed")
				})
			}
			acc.Input = p
		}
	}
	if err != nil {
		apiError(w, 503, "عملیات هنوز در هسته تأیید نشد")
		return
	}
	_ = a.refresh(r.Context())
	u, _ = a.findUser(u.Email)
	jsonReply(w, 200, compatUser(acc.Username, u, acc.Input))
}

// This privileged bridge is called only by an authenticated payment backend.
// Customer redirects and customer-supplied receipts never reach this method.
func (a *App) confirmPayment(id, paymentID string, amount int64, method string) error {
	return a.salesTxn(func(s *SalesData) error { return confirmPaymentTxn(s, id, paymentID, amount, method) })
}
func confirmPaymentTxn(s *SalesData, id, paymentID string, amount int64, method string) error {
	i := orderIndex(s, id)
	if i < 0 {
		return fmt.Errorf("سفارش یافت نشد")
	}
	o := &s.Orders[i]
	if o.Amount != amount || o.Status == "cancelled" {
		return fmt.Errorf("مبلغ یا وضعیت سفارش مطابقت ندارد")
	}
	if method == "stars" {
		if o.Method != "stars" || o.Currency != "XTR" {
			return fmt.Errorf("سفارش Stars نیست")
		}
	} else if gatewayMethod(method) {
		if o.Currency != "IRT" || o.Method != method {
			return fmt.Errorf("روش درگاه با سفارش مطابقت ندارد")
		}
	} else {
		if o.Currency != "IRT" || o.Method != "manual" {
			return fmt.Errorf("این سفارش برای درگاه تومانی نیست")
		}
	}
	for _, v := range s.Orders {
		if v.PaymentID == paymentID && v.ID != id {
			return fmt.Errorf("تراکنش قبلاً برای سفارش دیگری ثبت شده است")
		}
	}
	if o.PaymentID != "" {
		if o.PaymentID == paymentID {
			return nil
		}
		return fmt.Errorf("سفارش با تراکنش دیگری پرداخت شده است")
	}
	if o.Status != "pending" && o.Status != "receipt" {
		return fmt.Errorf("سفارش منتظر پرداخت نیست")
	}
	o.PaymentID = paymentID
	o.Status = "paid"
	o.Updated = time.Now().UnixMilli()
	return nil
}
