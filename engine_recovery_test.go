package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEngineMissingRouteDoesNotRelogin(t *testing.T) {
	var logins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/csrf-token":
			jsonReply(w, 200, map[string]any{"success": true, "obj": "csrf"})
		case "/login":
			logins.Add(1)
			jsonReply(w, 200, map[string]bool{"success": true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	e := newEngineClient(srv.URL)
	if err := e.login(context.Background(), "admin", "private", ""); err != nil {
		t.Fatal(err)
	}
	err := e.call(context.Background(), "POST", "/panel/api/missing", map[string]any{}, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || logins.Load() != 1 {
		t.Fatal("a genuinely missing route must not trigger login or replay", err, logins.Load())
	}
}

func TestEngineExpiredOTPSessionRequiresInteractiveLogin(t *testing.T) {
	var logins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/csrf-token":
			jsonReply(w, 200, map[string]any{"success": true, "obj": "csrf"})
		case "/login":
			logins.Add(1)
			jsonReply(w, 200, map[string]bool{"success": true})
		default:
			if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
				w.WriteHeader(http.StatusUnauthorized)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		}
	}))
	defer srv.Close()
	e := newEngineClient(srv.URL)
	if err := e.login(context.Background(), "admin", "private", "123456"); err != nil {
		t.Fatal(err)
	}
	_, err := e.inbounds(context.Background())
	if !errors.Is(err, errUnauthorized) || logins.Load() != 1 {
		t.Fatal("an expired OTP session must require a fresh interactive code", err, logins.Load())
	}
}
