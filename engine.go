package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

var errUnauthorized = errors.New("engine session expired")

type EngineClient struct {
	base       string
	http       *http.Client
	csrf       string
	mu         sync.Mutex
	authMu     sync.RWMutex
	username   string
	password   string
	autoLogin  bool
	generation uint64
	recoveries uint64
}

func newEngineClient(base string) *EngineClient {
	jar, _ := cookiejar.New(nil)
	return &EngineClient{base: strings.TrimRight(base, "/"), http: &http.Client{Jar: jar, Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 8, IdleConnTimeout: 60 * time.Second}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

type engineResponse struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

func (e *EngineClient) call(ctx context.Context, method, path string, body any, out any) error {
	e.authMu.RLock()
	generation := e.generation
	err := e.rawCall(ctx, method, path, body, out)
	e.authMu.RUnlock()
	if !errors.Is(err, errUnauthorized) {
		return err
	}
	// Only an explicit authentication rejection is replayed. Network timeouts,
	// malformed JSON and business errors never replay a possibly committed write.
	e.authMu.Lock()
	if e.generation == generation {
		if !e.autoLogin || e.username == "" {
			e.authMu.Unlock()
			return errUnauthorized
		}
		if err = e.loginRaw(ctx, e.username, e.password, ""); err != nil {
			e.authMu.Unlock()
			return err
		}
		e.recoveries++
	}
	e.authMu.Unlock()
	e.authMu.RLock()
	defer e.authMu.RUnlock()
	return e.rawCall(ctx, method, path, body, out)
}

func (e *EngineClient) rawCall(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, e.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	// 3x-ui 3.8.5 returns 404 for anonymous non-XHR API calls. Request
	// its explicit 401 response so an expired session triggers re-login.
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	e.mu.Lock()
	token := e.csrf
	e.mu.Unlock()
	if token != "" {
		req.Header.Set("X-CSRF-Token", token)
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return fmt.Errorf("engine is unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return errUnauthorized
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("engine API HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return fmt.Errorf("engine response unavailable: %w", err)
	}
	if len(b) > 8<<20 {
		return fmt.Errorf("engine response too large")
	}
	trimmed := bytes.TrimSpace(b)
	html := strings.ToLower(string(trimmed))
	if bytes.HasPrefix(trimmed, []byte("<")) && strings.Contains(html, "login") && strings.Contains(html, "username") && strings.Contains(html, "password") && path != "/login" && path != "/csrf-token" {
		return errUnauthorized
	}
	var envelope engineResponse
	if err := json.Unmarshal(b, &envelope); err != nil {
		return fmt.Errorf("invalid engine response: %w", err)
	}
	if !envelope.Success {
		m := strings.ToLower(envelope.Msg)
		for _, marker := range []string{"not logged", "not login", "unauthorized", "session expired", "csrf", "未登录", "请登录"} {
			if strings.Contains(m, marker) {
				return errUnauthorized
			}
		}
		return fmt.Errorf("engine: %s", envelope.Msg)
	}
	if out != nil && len(envelope.Obj) > 0 && string(envelope.Obj) != "null" {
		return json.Unmarshal(envelope.Obj, out)
	}
	return nil
}

func (e *EngineClient) login(ctx context.Context, user, password, otp string) error {
	e.authMu.Lock()
	defer e.authMu.Unlock()
	return e.loginRaw(ctx, user, password, otp)
}

func (e *EngineClient) loginRaw(ctx context.Context, user, password, otp string) error {
	var token string
	if err := e.rawCall(ctx, "GET", "/csrf-token", nil, &token); err != nil {
		return err
	}
	e.mu.Lock()
	e.csrf = token
	e.mu.Unlock()
	if err := e.rawCall(ctx, "POST", "/login", map[string]string{"username": user, "password": password, "twoFactorCode": otp}, nil); err != nil {
		return err
	}
	if err := e.rawCall(ctx, "GET", "/csrf-token", nil, &token); err != nil {
		return err
	}
	e.mu.Lock()
	e.csrf = token
	e.mu.Unlock()
	e.username, e.password, e.autoLogin = user, password, otp == ""
	e.generation++
	return nil
}

type Inbound struct {
	ID       int             `json:"id"`
	Remark   string          `json:"remark"`
	Listen   string          `json:"listen"`
	Port     int             `json:"port"`
	Protocol string          `json:"protocol"`
	Enable   bool            `json:"enable"`
	Settings json.RawMessage `json:"settings"`
	Stream   json.RawMessage `json:"streamSettings"`
	Stats    []Traffic       `json:"clientStats"`
}

type Traffic struct {
	Email      string `json:"email"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	LastOnline int64  `json:"lastOnline"`
}

// Accept both legacy JSON-encoded strings and nested objects.
func nestedJSON(b json.RawMessage, out any) error {
	if len(b) == 0 || string(b) == "null" {
		b = []byte("{}")
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		b = []byte(s)
	}
	return json.Unmarshal(b, out)
}

func (e *EngineClient) inbounds(ctx context.Context) ([]Inbound, error) {
	var all []Inbound
	err := e.call(ctx, "GET", "/panel/api/inbounds/list", nil, &all)
	return all, err
}

func marshalString(v any) string { b, _ := json.Marshal(v); return string(b) }

func (e *EngineClient) clientCall(ctx context.Context, verb, email string, body any) error {
	return e.call(ctx, "POST", "/panel/api/clients/"+verb+"/"+url.PathEscape(email), body, nil)
}

func (e *EngineClient) clientEmails(ctx context.Context) (map[string]bool, error) {
	var clients []struct {
		Email string `json:"email"`
	}
	if err := e.call(ctx, "GET", "/panel/api/clients/list", nil, &clients); err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, c := range clients {
		known[c.Email] = true
	}
	return known, nil
}
