package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type feature45Transport func(*http.Request) (*http.Response, error)

func (f feature45Transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reply45(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
func TestCountryLookupCacheAndLinks(t *testing.T) {
	a := automaticApp(t)
	a.geoHTTP = &http.Client{Transport: feature45Transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "ipwho.is" {
			t.Fatal(r.URL)
		}
		return reply45(`{"success":true,"ip":"8.8.8.8","country":"United States","country_code":"US"}`), nil
	})}
	if e := a.refreshGeo(context.Background()); e != nil {
		t.Fatal(e)
	}
	g := a.exitLocation()
	if g.Flag != "🇺🇸" || g.Code != "US" {
		t.Fatal(g)
	}
	p := profileTemplates[0]
	link := shareLinkWithEndpoint(p, map[string]any{"id": newUUID(), "comment": "مشتری"}, "https://panel.example", LinkEndpoint{})
	located := labelShareLink(link, g)
	if located == link || labelShareLink(located, g) != located {
		t.Fatal("country label missing or duplicated")
	}
	if changed := labelShareLink(located, GeoInfo{Code: "DE", Country: "Germany", Flag: "🇩🇪"}); strings.Contains(changed, "United") || !strings.Contains(changed, "Germany") {
		t.Fatal("old location duplicated", changed)
	}
	p.Protocol = "vmess"
	v := labelShareLink(shareLinkWithEndpoint(p, map[string]any{"id": newUUID()}, "https://panel.example", LinkEndpoint{}), g)
	b, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, "vmess://"))
	if e != nil || !bytes.Contains(b, []byte(g.Flag)) {
		t.Fatal("VMess country name lost")
	}
	a.geoHTTP = &http.Client{Transport: feature45Transport(func(*http.Request) (*http.Response, error) { return reply45(`{"success":false}`), nil })}
	if a.refreshGeo(context.Background()) == nil || a.exitLocation().Code != "US" {
		t.Fatal("lookup failure erased cached country")
	}
	a.loadGeo()
	if a.exitLocation().Code != "US" {
		t.Fatal("cache did not persist")
	}
	if countryFlag("<>") != "🌐" {
		t.Fatal("invalid country accepted")
	}
}
func TestRelayAdaptersAndCredentialsArePrivate(t *testing.T) {
	for _, provider := range []string{"vercel", "deno", "cloudflare-worker"} {
		t.Run(provider, func(t *testing.T) {
			a := automaticApp(t)
			v := EdgeRoute{ID: "relay-" + strings.Repeat("a", 24), Name: "Route", Provider: provider, Resource: "aria-test-route", Origin: "https://origin.example"}
			c := RelayCredential{ID: v.ID, Token: "PRIVATE_PROVIDER_TOKEN", Account: strings.Repeat("a", 32), Team: "team-test"}
			a.edges.Routes = []EdgeRoute{v}
			a.edges.Credentials = []RelayCredential{c}
			calls := []string{}
			a.edgeHTTP = &http.Client{Transport: feature45Transport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer "+c.Token {
					t.Fatal("authorization missing")
				}
				calls = append(calls, r.Method+" "+r.URL.Path)
				if strings.HasSuffix(r.URL.Path, "/projects") {
					return reply45(`{"id":"prj_test"}`), nil
				}
				if strings.HasSuffix(r.URL.Path, "/deployments") {
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["target"] != "production" {
						t.Fatal("not production")
					}
					b, _ := json.Marshal(body["files"])
					if !bytes.Contains(b, []byte("https://origin.example")) || bytes.Contains(b, []byte(c.Token)) {
						t.Fatal("relay source leaked management token or lost origin")
					}
					return reply45(`{"id":"dpl_test","readyState":"QUEUED"}`), nil
				}
				if r.URL.Path == "/v2/apps" {
					return reply45(`{"id":"app-test"}`), nil
				}
				if strings.HasSuffix(r.URL.Path, "/deploy") {
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					assets := body["assets"].(map[string]any)
					if assets["main.ts"].(map[string]any)["kind"] != "file" || body["production"] != true {
						t.Fatal("wrong new Deno API payload")
					}
					return reply45(`{"id":"rev-test","status":"queued"}`), nil
				}
				if r.Method == "PUT" {
					mr, e := r.MultipartReader()
					if e != nil {
						t.Fatal(e)
					}
					types := map[string]string{}
					for {
						part, e := mr.NextPart()
						if e == io.EOF {
							break
						}
						if e != nil {
							t.Fatal(e)
						}
						types[part.FormName()] = part.Header.Get("Content-Type")
					}
					if types["relay.js"] != "application/javascript+module" || types["metadata"] != "application/json" {
						t.Fatal(types)
					}
					return reply45(`{"success":true}`), nil
				}
				if strings.HasSuffix(r.URL.Path, "/subdomain") && r.Method == "GET" {
					return reply45(`{"success":true,"result":{"subdomain":"test-account"}}`), nil
				}
				return reply45(`{"success":true,"result":{}}`), nil
			})}
			var e error
			switch provider {
			case "vercel":
				e = a.deployVercel(context.Background(), &v, c)
			case "deno":
				e = a.deployDeno(context.Background(), &v, c)
			default:
				e = a.deployWorker(context.Background(), &v, c)
			}
			if e != nil {
				t.Fatal(e)
			}
			if v.ResourceID == "" || len(calls) < 2 {
				t.Fatal("no actual deployment steps")
			}
			if provider == "cloudflare-worker" && v.Endpoint.Address != "aria-test-route.test-account.workers.dev" {
				t.Fatal(v.Endpoint)
			}
			b, _ := json.Marshal(a.edgePublic())
			if bytes.Contains(b, []byte(c.Token)) {
				t.Fatal("provider token exposed")
			}
		})
	}
}
func TestVerifiedRouteExportUsesCorrectTransportAndCountry(t *testing.T) {
	a := automaticApp(t)
	a.geo.Exit = GeoInfo{Code: "DE", Country: "Germany", Flag: "🇩🇪"}
	raw := map[string]any{"id": newUUID(), "email": "u", "comment": "User"}
	ps := append([]Profile{}, profileTemplates...)
	p := ps[0]
	a.mu.Lock()
	a.state.Profiles = ps
	a.state.Users = []User{{Email: "u", Name: "User", Raw: raw, Links: []Connection{{p.Name, p.Key, shareLink(p, raw, "https://panel.example")}}}}
	a.mu.Unlock()
	a.edges.Routes = []EdgeRoute{{ID: "relay-a", Name: "Edge", Provider: "vercel", Enabled: true, Verified: true, Networks: []string{"ws"}, Endpoint: LinkEndpoint{Address: "edge.example", Host: "edge.example", SNI: "edge.example", Port: 443}}}
	r := httptest.NewRequest("GET", "/api/edges/export?email=u&route=relay-a", nil)
	w := httptest.NewRecorder()
	a.edgeExport(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Links  []string `json:"links"`
		Routes int      `json:"routes"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Routes != 1 || len(out.Links) != 1 || !strings.Contains(out.Links[0], "edge.example") || !strings.Contains(out.Links[0], "Germany") {
		t.Fatal(out)
	}
	a.edges.Routes[0].Verified = false
	w = httptest.NewRecorder()
	a.edgeExport(w, r)
	if w.Code != 409 {
		t.Fatal("unverified route became usable")
	}
}
func TestTelegramTextAndQRDeliveryOwnershipAndRestart(t *testing.T) {
	a := automaticApp(t)
	a.telegram = TelegramConfig{Token: "123456:PRIVATE_BOT_TOKEN", ChatID: 900, Enabled: true}
	a.sales.Customers = []SalesCustomer{{ID: "customer", TelegramID: 100}}
	v := SoldService{ID: "s-test", Name: "مشتری", CustomerID: "customer", NodeID: "local", Subscription: "https://panel.example/subscription/secret", Links: []string{"vless://uuid@vpn.example:443?type=ws#User"}}
	a.sales.Services = []SoldService{v}
	queueConnectionDelivery(&a.sales, "test-delivery", 100, v.ID, "", len(v.Links))
	if len(a.sales.Notices) != 4 {
		t.Fatal("text + QR for subscription and config not queued")
	}
	methods := []string{}
	a.telegramHTTP = &http.Client{Transport: feature45Transport(func(r *http.Request) (*http.Response, error) {
		method := filepath.Base(r.URL.Path)
		methods = append(methods, method)
		if method == "sendPhoto" {
			mr, e := r.MultipartReader()
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for {
				p, e := mr.NextPart()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				if p.FormName() == "photo" {
					im, e := png.Decode(p)
					if e != nil || im.Bounds().Dx() != 800 {
						t.Fatal("not a real QR card PNG", e)
					}
					found = true
				}
			}
			if !found {
				t.Fatal("photo missing")
			}
		}
		return reply45(`{"ok":true}`), nil
	})}
	for _, n := range a.sales.Notices {
		target := a.telegram
		target.ChatID = n.ChatID
		if e := a.tgRichSend(context.Background(), target, n); e != nil {
			t.Fatal(e)
		}
	}
	if strings.Join(methods, ",") != "sendMessage,sendPhoto,sendMessage,sendPhoto" {
		t.Fatal(methods)
	}
	n := a.sales.Notices[0]
	n.ChatID = 101
	if a.tgRichSend(context.Background(), a.telegram, n) == nil {
		t.Fatal("another customer received credentials")
	}
	if e := a.salesTxn(func(s *SalesData) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if e := a.loadSales(); e != nil {
		t.Fatal(e)
	}
	if len(a.sales.Notices) != 4 || a.sales.Notices[0].ConnectionIndex != -1 {
		t.Fatal("delivery state lost on restart")
	}
	if v.Links[0] != a.sales.Services[0].Links[0] {
		t.Fatal("delivery mutated saved links")
	}
}
func TestQRCardArtifact(t *testing.T) {
	payload := "vless://a1a1a1a1-a1a1-4a1a-a1a1-a1a1a1a1a1a1@vpn.example:443?security=tls&type=ws&path=%2Fconnect%2Ftest%2F&sni=vpn.example#🇩🇪%20Germany%20·%20مشتری"
	b, e := qrCard(payload, "VLESS / WS")
	if e != nil {
		t.Fatal(e)
	}
	im, e := png.Decode(bytes.NewReader(b))
	if e != nil || im.Bounds().Dy() != 1010 {
		t.Fatal("bad PNG", e)
	}
	if dir := os.Getenv("ARIA_QR_TEST_OUTPUT"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "qr45.png"), b, 0600)
		_ = os.WriteFile(filepath.Join(dir, "qr45.txt"), []byte(payload), 0600)
	}
	if _, e := qrCard(strings.Repeat("vless", 3000), "LONG"); e == nil {
		t.Fatal("oversize QR silently accepted")
	}
}

func TestWebsocketProbeRejectsHTTPOnlyAndChecksAccept(t *testing.T) {
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer a.Close()
	if probeWebSocket(context.Background(), a.Client(), a.URL, "localhost") == nil {
		t.Fatal("HTTP-only relay accepted as WebSocket")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		w.Header().Set("Upgrade", "websocket")
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Sec-WebSocket-Accept", base64.StdEncoding.EncodeToString(h[:]))
		w.WriteHeader(101)
	}))
	defer server.Close()
	if e := probeWebSocket(context.Background(), server.Client(), server.URL, "localhost"); e != nil {
		t.Fatal(e)
	}
}

func TestCDNTelegramDeliveryUsesRegisteredOwnerAndCurrentRoute(t *testing.T) {
	a := automaticApp(t)
	a.telegram = TelegramConfig{Token: "123456:PRIVATE_BOT_TOKEN", Enabled: true, ChatID: 900}
	raw := map[string]any{"id": newUUID(), "email": "u"}
	p := profileTemplates[0]
	a.state.Users = []User{{Email: "u", Name: "User", Raw: raw, Links: []Connection{{p.Name, p.Key, shareLink(p, raw, "https://origin.example")}}}}
	a.state.Profiles = profileTemplates
	a.edges.Routes = []EdgeRoute{{ID: "route-a", Name: "CDN", Enabled: true, Verified: true, Networks: []string{"ws"}, Endpoint: LinkEndpoint{Address: "cdn.example", Port: 443, Host: "cdn.example", SNI: "cdn.example"}}}
	a.sales.Customers = []SalesCustomer{{ID: "c-a", TelegramID: 100}}
	a.sales.Services = []SoldService{{ID: "s-a", Email: "u", CustomerID: "c-a"}}
	r := httptest.NewRequest("POST", "/api/edges/delivery", strings.NewReader(`{"email":"u","route":"route-a","id":"aaaaaaaaaaaaaaaaaaaaaaaa"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.queueEdgeDelivery(w, r)
	if w.Code != 200 || len(a.sales.Notices) != 2 {
		t.Fatal(w.Code, w.Body.String(), len(a.sales.Notices))
	}
	n := a.sales.Notices[0]
	if n.ChatID != 100 || n.ConnectionService != "s-a" || n.ConnectionEmail != "" || n.ConnectionIndex != 0 {
		t.Fatal("not bound to customer", n)
	}
	a.telegramHTTP = &http.Client{Transport: feature45Transport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["chat_id"] != float64(100) || !strings.Contains(body["text"].(string), "cdn.example") {
			t.Fatal("wrong recipient or URI", body)
		}
		return reply45(`{"ok":true}`), nil
	})}
	target := a.telegram
	target.ChatID = 100
	if e := a.connectionNotice(context.Background(), target, n); e != nil {
		t.Fatal(e)
	}
	a.edges.Routes[0].Enabled = false
	if a.connectionNotice(context.Background(), target, n) == nil {
		t.Fatal("revoked route delivered")
	}
}

func TestRelayDisablesClientEarlyDataWithoutMutatingServerProfile(t *testing.T) {
	p := profileTemplates[0]
	p.Tuning.MaxEarlyData = 4096
	e := EdgeRoute{ID: "relay", Name: "Relay", Networks: []string{"ws"}}
	q := profileForEdge(p, e)
	if q.Tuning.MaxEarlyData != 0 || p.Tuning.MaxEarlyData != 4096 {
		t.Fatal("relay early data compatibility was not isolated")
	}
}

func TestHTTP1TunnelRepliesBeforeStreamingUploadFinishes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).EnableFullDuplex()
		one := make([]byte, 1)
		if _, e := io.ReadFull(r.Body, one); e != nil {
			return
		}
		w.WriteHeader(200)
		_ = http.NewResponseController(w).Flush()
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer upstream.Close()
	address := strings.TrimPrefix(upstream.URL, "http://")
	_, portText, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	a := automaticApp(t)
	a.routes["/connect/duplex/"] = makeProxy(port)
	frontend := httptest.NewServer(a)
	defer frontend.Close()
	conn, e := net.DialTimeout("tcp", strings.TrimPrefix(frontend.URL, "http://"), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, e = io.WriteString(conn, "POST /connect/duplex/ HTTP/1.1\r\nHost: localhost\r\nTransfer-Encoding: chunked\r\n\r\n1\r\nx\r\n")
	if e != nil {
		t.Fatal(e)
	}
	response, e := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "POST"})
	if e != nil {
		t.Fatal("response was blocked waiting for upload to finish", e)
	}
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	_, _ = io.WriteString(conn, "0\r\n\r\n")
	_ = response.Body.Close()
}
