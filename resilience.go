package main

import (
	"context"
	"net/http"
	"reflect"
	"sync"
	"time"
)

func (a *App) resilienceAPI(w http.ResponseWriter, r *http.Request) {
	s := a.snapshot()
	edges := a.edgeSnapshot()
	verified := 0
	for _, v := range edges.Routes {
		if v.Enabled && v.Verified {
			verified++
		}
	}
	recipes := []map[string]any{
		{"name": "WS از چند ورودی مستقل", "key": "vless-ws", "available": true, "detail": "ورودی Vercel، Deno یا Worker را دپلوی و تأیید کن؛ برای هر مشتری خروجی همان مسیر یا فایل انتخاب خودکار بگیر. پورت HTTPS 443 و SNI دارای گواهی معتبر لازم است."},
		{"name": "XHTTP و دانلود جدا", "key": "vless-xhttp", "available": true, "detail": "برای CDN متصل مستقیم به مبدأ، packet-up و دو ورودی تأییدشده را در فرم دستی انتخاب کن. در خروجی relay خودکار این نسخه فقط WS آزموده شده است."},
		{"name": "VLESS Reality / Vision", "key": "vless-reality", "available": a.cfg.Mode == "vps", "detail": "TCP مستقیم VPS؛ به مقصد TLS سازگار، کلید معتبر و پورت عمومی نیاز دارد. ورودی HTTP CDN عمومی جایگزین این TCP مستقیم نیست."},
		{"name": "Hysteria2 / TUIC", "key": "sing-box", "available": a.cfg.Mode == "vps", "detail": "QUIC روی UDP برای شبکه‌ای که UDP را عبور می‌دهد. اگر اپراتور UDP را ببندد، یک اتصال TCP/WS آماده داشته باش."},
	}
	jsonReply(w, 200, map[string]any{"location": a.exitLocation(), "mode": envMode(a.cfg.Mode), "originReady": s.Ready, "verifiedRoutes": verified, "routes": edges.Routes, "recipes": recipes, "note": "آزمون‌های پنل از شبکهٔ سرور انجام می‌شوند. فایل انتخاب خودکار، مسیرها را از شبکهٔ خودِ کلاینت بررسی می‌کند. چند CDN متصل به یک مبدأ، خرابی همان مبدأ را پوشش نمی‌دهند."})
}

// Keep verification fresh instead of trusting a route forever after one success.
func (a *App) edgeMonitorLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s := a.edgeSnapshot()
			slots := make(chan struct{}, 3)
			var wg sync.WaitGroup
			for _, route := range s.Routes {
				if !route.Enabled || route.Endpoint.Host == "" {
					continue
				}
				wg.Add(1)
				go func(v EdgeRoute) {
					defer wg.Done()
					select {
					case slots <- struct{}{}:
						defer func() { <-slots }()
					case <-ctx.Done():
						return
					}
					c, cancel := context.WithTimeout(ctx, 12*time.Second)
					report, e := a.probeEdgeDetailed(c, v)
					cancel()
					if ctx.Err() != nil {
						return
					}
					// Probe outside the lock; retain edits and newer manual tests.
					a.edgeTxnMu.Lock()
					defer a.edgeTxnMu.Unlock()
					current := a.edgeSnapshot()
					for i := range current.Routes {
						n := &current.Routes[i]
						if n.ID != v.ID || !n.Enabled || n.Endpoint != v.Endpoint || !reflect.DeepEqual(n.Networks, v.Networks) || n.LastTest > report.At {
							continue
						}
						n.Probe = &report
						n.Verified = e == nil
						n.LatencyMS = report.MS
						n.LastTest = time.Now().UnixMilli()
						if e != nil {
							n.Note = e.Error()
						} else {
							n.Note = "TLS و مسیر به مبدأ تأیید شد؛ کیفیت اتصال را در کلاینت آزمون کن"
						}
						_ = a.edgeSave(current)
						break
					}
				}(route)
			}
			wg.Wait()
		}
	}
}
