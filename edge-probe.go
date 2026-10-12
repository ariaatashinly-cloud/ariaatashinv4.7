package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ProbeStage struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	MS     int64  `json:"ms"`
	Detail string `json:"detail"`
}
type EdgeProbe struct {
	OK          bool         `json:"ok"`
	At          int64        `json:"at"`
	MS          int64        `json:"ms"`
	IP          string       `json:"ip,omitempty"`
	CertExpires int64        `json:"certExpires,omitempty"`
	Failure     string       `json:"failure,omitempty"`
	Stages      []ProbeStage `json:"stages"`
}

func (a *App) probeEdgeDetailed(parent context.Context, v EdgeRoute) (report EdgeProbe, err error) {
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	start := time.Now()
	report.At = start.UnixMilli()
	report.Stages = []ProbeStage{}
	stage := "dns"
	stageStart := time.Now()
	fail := func(detail string) error {
		report.Failure = stage
		report.Stages = append(report.Stages, ProbeStage{stage, false, time.Since(stageStart).Milliseconds(), detail})
		return fmt.Errorf("%s: %s", stage, detail)
	}
	defer func() { report.MS = time.Since(start).Milliseconds(); report.OK = err == nil }()
	ips, e := net.DefaultResolver.LookupIPAddr(ctx, v.Endpoint.Address)
	if e != nil || len(ips) == 0 {
		err = fail("ورودی پاسخ DNS ندارد؛ Address را بررسی کن")
		return
	}
	for _, ip := range ips {
		if !publicNetworkIP(ip.IP) {
			err = fail("IP خصوصی یا رزروشده مجاز نیست")
			return
		}
	}
	report.Stages = append(report.Stages, ProbeStage{stage, true, time.Since(stageStart).Milliseconds(), fmt.Sprintf("%d آدرس عمومی", len(ips))})
	stage = "connect"
	stageStart = time.Now()
	port := strconv.Itoa(v.Endpoint.Port)
	var traceMu sync.Mutex
	actualIP := ""
	tlsStarted := false
	tlsStart := time.Now()
	tlsMS := int64(0)
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		traceMu.Lock()
		actualIP, _, _ = net.SplitHostPort(info.Conn.RemoteAddr().String())
		traceMu.Unlock()
	}, TLSHandshakeStart: func() { traceMu.Lock(); tlsStarted = true; tlsStart = time.Now(); traceMu.Unlock() }, TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
		traceMu.Lock()
		tlsMS = time.Since(tlsStart).Milliseconds()
		traceMu.Unlock()
	}}
	tr := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{ServerName: v.Endpoint.SNI, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var last error
		for _, ip := range ips {
			c, e := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return c, nil
			}
			last = e
		}
		return nil, last
	}, TLSHandshakeTimeout: 5 * time.Second}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Transport: tr, Timeout: 8 * time.Second, CheckRedirect: noRedirect}
	req, e := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", "https://"+net.JoinHostPort(v.Endpoint.SNI, port)+"/live", nil)
	if e != nil {
		err = fail("آدرس HTTPS معتبر نیست")
		return
	}
	req.Host = v.Endpoint.Host
	resp, e := cl.Do(req)
	traceMu.Lock()
	started, ms, connectedIP := tlsStarted, tlsMS, actualIP
	traceMu.Unlock()
	if e != nil {
		if started {
			stage = "tls"
		}
		err = fail("ارتباط برقرار نشد؛ پورت، SNI، گواهی و اتصال مبدأ را بررسی کن")
		return
	}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		report.CertExpires = resp.TLS.PeerCertificates[0].NotAfter.UnixMilli()
	}
	report.IP = connectedIP
	report.Stages = append(report.Stages, ProbeStage{"tls", true, ms, "گواهی و SNI معتبر هستند"})
	stage = "origin"
	stageStart = time.Now()
	var live struct {
		Alive    bool   `json:"alive"`
		Identity string `json:"identity"`
	}
	e = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&live)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || e != nil || !live.Alive || live.Identity != a.edgeSnapshot().Identity {
		err = fail("پاسخ متعلق به همین پنل نیست؛ مبدأ، Host و ثبت دامنه را بررسی کن")
		return
	}
	report.Stages = append(report.Stages, ProbeStage{stage, true, time.Since(stageStart).Milliseconds(), "هویت همین پنل تأیید شد"})
	if len(v.Networks) > 0 {
		stage = "websocket"
		stageStart = time.Now()
		path := ""
		for _, p := range a.snapshot().Profiles {
			if p.Ready && !p.Direct && p.Network == "ws" {
				path = p.Path
				break
			}
		}
		if path == "" {
			for _, c := range a.coresSnapshot().Services {
				if coreActive(c) && (c.Kind == "vless-ws" || c.Kind == "trojan-ws") {
					path = c.Path
					break
				}
			}
		}
		if path == "" {
			err = fail("ابتدا اتصال WS بساز؛ سپس مسیر را آزمایش کن")
			return
		}
		if e = probeWebSocket(ctx, cl, "https://"+net.JoinHostPort(v.Endpoint.SNI, port)+path, v.Endpoint.Host); e != nil {
			err = fail(e.Error())
			return
		}
		report.Stages = append(report.Stages, ProbeStage{stage, true, time.Since(stageStart).Milliseconds(), "پاسخ ارتقای واقعی 101 تأیید شد"})
	}
	return
}

// Candidate IPs are explicitly supplied by the administrator and pinned to an
// existing hostname. This is bounded diagnostic work, not an Internet scan.
func (a *App) candidateProbeAPI(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Route string   `json:"route"`
		IPs   []string `json:"ips"`
	}
	if readBody(w, r, &in, 4096) != nil || len(in.IPs) == 0 || len(in.IPs) > 8 {
		apiError(w, 400, "شناسهٔ مسیر و ۱ تا ۸ IP عمومی لازم است")
		return
	}
	var route EdgeRoute
	found := false
	for _, e := range a.edgeSnapshot().Routes {
		if e.ID == in.Route {
			route = e
			found = true
			break
		}
	}
	if !found {
		apiError(w, 404, "ابتدا مسیر با Host و SNI دامنهٔ خودت ثبت کن")
		return
	}
	seen := map[string]bool{}
	for _, s := range in.IPs {
		ip := net.ParseIP(strings.TrimSpace(s))
		if !publicNetworkIP(ip) || seen[ip.String()] {
			apiError(w, 400, "IP عمومی، معتبر و غیرتکراری لازم است")
			return
		}
		seen[ip.String()] = true
	}
	results := make([]any, len(in.IPs))
	slots := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i, ip := range in.IPs {
		wg.Add(1)
		go func(i int, ip string) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-r.Context().Done():
				return
			}
			v := route
			v.Endpoint.Address = strings.TrimSpace(ip)
			probe, _ := a.probeEdgeDetailed(r.Context(), v)
			results[i] = map[string]any{"address": v.Endpoint.Address, "probe": probe}
		}(i, ip)
	}
	wg.Wait()
	jsonReply(w, 200, map[string]any{"results": results, "note": "نتیجه از شبکهٔ همین سرور است. IP موفق باید از شبکهٔ اپراتور مشتری هم آزموده شود؛ این آزمون IP را سفید یا رفع مسدودی نمی‌کند."})
}
