//go:build integration

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestNativeSessionExpiryKeepsBrowserAndExistingConfig(t *testing.T) {
	a, ctx := nativeTestApp(t)
	cfg := a.cfg
	cookie, csrf := autoLogin(t, a, cfg.PublicURL)
	request := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := browserRequest("POST", path, cfg.PublicURL, body).WithContext(ctx)
		r.AddCookie(cookie)
		r.Header.Set("X-ATIA-CSRF", csrf)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	create := `{"name":"native recovery test","count":1,"quotaGB":1,"days":7,"profiles":["vless-ws"]}`
	w := request("/api/users", create)
	if w.Code != http.StatusOK || len(a.snapshot().Users) != 1 {
		t.Fatal("initial config creation failed", w.Code, w.Body.String())
	}
	old := a.snapshot().Users[0]
	e := a.service
	u, _ := url.Parse(a.base)
	expire := func() {
		// Expiring the native cookie accelerates the six-hour default boundary.
		// The wrapper's browser session and the running Xray process stay intact.
		e.http.Jar.SetCookies(u, []*http.Cookie{{Name: "3x-ui", Path: "/", MaxAge: -1}})
	}
	expire()
	bare, _ := http.NewRequestWithContext(ctx, "GET", a.base+"/panel/api/inbounds/list", nil)
	bare.Header.Set("Accept", "application/json")
	resp, err := e.http.Do(bare)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal("pinned 3x-ui must hide an anonymous non-XHR API request with 404", resp.StatusCode)
	}
	if err := a.refresh(ctx); err != nil {
		t.Fatal("background sync failed to recover the expired native session", err)
	}
	if !a.snapshot().Ready || a.coreStatus().Recoveries != 1 {
		t.Fatal("native session was not recovered exactly once")
	}
	expire()
	a.coreFailed()
	w = request("/api/core/reconnect", `{}`)
	if w.Code != http.StatusOK || !a.snapshot().Ready || a.coreStatus().Recoveries != 2 {
		t.Fatal("original browser could not reconnect after a second expiration", w.Code, w.Body.String())
	}
	w = request("/api/users", create)
	var created struct {
		Created []string `json:"created"`
		Warning string   `json:"warning"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != http.StatusOK || len(created.Created) != 1 || created.Warning != "" || len(a.snapshot().Users) != 2 {
		t.Fatal("original browser could not create a new config after recovery", w.Code, w.Body.String(), err)
	}
	current, ok := a.findUser(old.Email)
	if !ok || current.Subscription != old.Subscription || marshalString(current.Links) != marshalString(old.Links) || current.Quota != old.Quota || current.Expiry != old.Expiry {
		t.Fatal("recovery changed or removed an existing config")
	}
}
