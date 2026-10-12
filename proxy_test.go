package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func proxyForTest(t *testing.T, target string) http.Handler {
	t.Helper()
	u, _ := url.Parse(target)
	port, _ := strconv.Atoi(u.Port())
	return makeProxy(port)
}

func TestProxyStripsAdminSecrets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, k := range []string{"Cookie", "Authorization", "X-ATIA-CSRF", "X-CSRF-Token"} {
			if r.Header.Get(k) != "" {
				t.Errorf("secret %s forwarded", k)
			}
		}
		if r.URL.Path != "/connect/test-path/extra" {
			t.Error("path changed", r.URL.Path)
		}
		io.WriteString(w, "success")
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(proxyForTest(t, upstream.URL))
	defer proxy.Close()
	req, _ := http.NewRequest("GET", proxy.URL+"/connect/test-path/extra", nil)
	for _, k := range []string{"Cookie", "Authorization", "X-ATIA-CSRF", "X-CSRF-Token"} {
		req.Header.Set(k, "admin-secret")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "success" {
		t.Fatal(string(b))
	}
}

func TestWebSocketBidirectionalPayload(t *testing.T) {
	// A loopback-only upgrade fixture echoes a masked WebSocket frame exactly.
	// It checks payload forwarding, not merely a successful 101 handshake.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			t.Error("upgrade missing")
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		rw.Flush()
		buf := make([]byte, 8)
		if _, err := io.ReadFull(rw, buf); err != nil {
			t.Error(err)
			return
		}
		if _, err := conn.Write(buf); err != nil {
			t.Error(err)
		}
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(proxyForTest(t, upstream.URL))
	defer proxy.Close()
	u, _ := url.Parse(proxy.URL)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(conn, "GET /connect/socket/ HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n", u.Host)
	reader := bufio.NewReader(conn)
	line, _ := reader.ReadString('\n')
	if !strings.Contains(line, "101") {
		t.Fatal(line)
	}
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	frame := []byte{0x81, 0x82, 1, 2, 3, 4, 0x40, 0x43}
	if _, err := conn.Write(frame); err != nil {
		t.Fatal(err)
	}
	result := make([]byte, len(frame))
	if _, err := io.ReadFull(reader, result); err != nil {
		t.Fatal(err)
	}
	if string(result) != string(frame) {
		t.Fatal("frame corrupted")
	}
}

func TestHTTPStreamingPayload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		w.Write(b)
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()
	proxy := httptest.NewServer(proxyForTest(t, upstream.URL))
	defer proxy.Close()
	data := strings.Repeat("xhttp-fixture-", 1000)
	res, err := http.Post(proxy.URL+"/connect/stream/session/1", "application/octet-stream", strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || string(body) != data {
		t.Fatal("HTTP streaming payload failed", err)
	}
}
