package main

import (
	"net/http"
	"net/url"
	"strings"
)

type HostingPreset struct {
	Key         string   "json:\"key\""
	Name        string   "json:\"name\""
	Link        string   "json:\"link\""
	Description string   "json:\"description\""
	Steps       []string "json:\"steps\""
}

var hostingPresets = []HostingPreset{
	{"auto", "خودکار", "", "آدرس مرورگر مدیر مبنای تمام لینک‌هاست؛ بدون واردکردن دستی Host و SNI.", []string{"یک Replica، PORT میزبان و Volume واقعی داده را نگه دار.", "کانفیگ را از داخل پنل کپی یا با QR وارد کن."}},
	{"deplexo", "Deplexo", "https://deplexo.com", "هشت الگوی HTTP روی دامنهٔ عمومی؛ داده روی /data.", []string{"مخزن GitHub، شاخهٔ main و Dockerfile ریشه را انتخاب کن.", "پورت داخلی 8080 و یک Replica؛ ورود نخست admin / admin؛ رمز از تنظیمات پنل قابل تغییر است.", "اجرا از /app، داده از /data؛ باینری را روی Volume noexec کپی نکن."}},
	{"velixir", "Velixir", "https://velixir.net", "الگوهای HTTP؛ دیسک محلی موقت است، بکاپ مستقل لازم است.", []string{"دستور Deploy یا workflow همان برنامه را استفاده کن؛ Dockerfile یا Linux amd64 با امکان اجرای باینری لازم است.", "پورت داخلی با PORT برابر باشد. Min و Max replicas هر دو 1.", "قبل از Redeploy بکاپ بگیر؛ پلن پولی به‌تنهایی دیسک پایدار نمی‌دهد."}},
	{"pxxl", "Pxxl", "https://pxxl.app", "Docker Web Service و WebSocket طبق امکانات پلن؛ نیازمند بررسی فضای پایدار.", []string{"GitHub را وصل و مخزن را به‌عنوان Docker Web Service انتخاب کن.", "PORT=8080 و ورود اولیه admin / admin؛ تغییر رمز داخل پنل؛ یک Replica.", "Domains → Settings → WebSocket Support روشن؛ Save Controls و Resync Proxy.", "برای حفظ SQLite باید filesystem Volume واقعی داشته باشی؛ Object Storage جای آن نیست."}},
	{"koyeb", "Koyeb · Frankfurt", "https://www.koyeb.com", "پلن Free در فرانکفورت؛ ۵۱۲ مگابایت رم، خواب پس از یک ساعت، بدون Volume دائمی.", []string{"Web Service از GitHub و Dockerfile ریشه؛ منطقه Frankfurt و Instance نوع Free.", "پورت HTTP داخلی 8080، مسیر / و یک Instance؛ ورود اولیه admin / admin؛ تغییر رمز داخل پنل.", "Health check روی /health؛ از Volume دائمی در پلن Free پشتیبانی نمی‌شود.", "قبل از Restart یا Redeploy بکاپ بگیر؛ محدودیت ترافیک و مقررات میزبان را بررسی کن."}},
	{"render", "Render · Frankfurt", "https://render.com", "وب‌سرویس Free در فرانکفورت؛ خواب پس از ۱۵ دقیقه و حذف SQLite هنگام خواب یا بازسازی.", []string{"New Web Service از همین مخزن؛ Runtime نوع Docker، منطقه Frankfurt و Instance نوع Free.", "PORT=8080، یک Instance و ورود اولیه admin / admin؛ تغییر رمز داخل پنل؛ Health check روی /health.", "پلن Free دیسک دائمی ندارد؛ به‌علت SQLite برای سرویس پایدار مناسب نیست.", "ترافیک خروجی زیاد ممکن است موجب تعلیق شود؛ بکاپ مستقل لازم است."}},
	{"vps", "سرور مجازی", "", "هجده الگو: هشت وب و ده مستقیم با پورت مستقیم TCP/UDP و TLS معتبر.", []string{"سرور Linux amd64، Docker Compose و دامنهٔ متصل به IP سرور.", "راهنمای INSTALL-VPS-FA.md و install-vps.sh را اجرا کن.", "پورت‌های TCP/UDP هر الگوی انتخاب‌شده را در فایروال باز کن."}},
}

func validPreset(s string) bool {
	for _, p := range hostingPresets {
		if p.Key == s {
			return true
		}
	}
	return s == ""
}
func normalizePreset(s string) string {
	if s == "" {
		return "auto"
	}
	return s
}
func detectPreset(origin, mode string) string {
	if mode == "vps" {
		return "vps"
	}
	u, _ := url.Parse(origin)
	h := strings.ToLower(u.Hostname())
	for _, p := range []struct {
		key      string
		suffixes []string
	}{{"deplexo", []string{"deplexo.com", "deplexo.app"}}, {"velixir", []string{"velixir.run"}}, {"pxxl", []string{"pxxl.app", "pxxlspace.cv", "pxxl.space"}}, {"koyeb", []string{"koyeb.app"}}, {"render", []string{"onrender.com"}}} {
		for _, s := range p.suffixes {
			if h == s || strings.HasSuffix(h, "."+s) {
				return p.key
			}
		}
	}
	return "auto"
}
func csrfHeader(r *http.Request) string {
	if v := r.Header.Get("X-ARIA-CSRF"); v != "" {
		return v
	}
	return r.Header.Get("X-ATIA-CSRF")
}
func (a *App) runtimeSnapshot() any {
	s := a.snapshot()
	s.Users = append([]User{}, s.Users...)
	for i := range s.Users {
		s.Users[i] = a.locatedUser(s.Users[i])
	}
	selected := normalizePreset(s.Settings.HostPreset)
	detected := detectPreset(s.Settings.PublicURL, a.cfg.Mode)
	if selected == "auto" {
		selected = detected
	}
	return struct {
		Snapshot
		Location GeoInfo    `json:"location"`
		Core     CoreStatus `json:"core"`
		Hosting  any        "json:\"hosting\""
		VPS      any        "json:\"vps\""
		Telegram any        "json:\"telegram\""
		Metrics  Metrics    "json:\"metrics\""
	}{s, a.exitLocation(), a.coreStatus(), map[string]any{"selected": selected, "detected": detected, "presets": hostingPresets, "mode": envMode(a.cfg.Mode)}, a.publicVPS(), a.publicTelegram(), a.sampleMetrics()}
}
func envMode(s string) string {
	if s == "" {
		return "cloud"
	}
	return s
}
func (a *App) profileCatalog() []Profile {
	list := allTemplates()
	v := a.vpsSnapshot()
	certErr := v.certificateError()
	for i := range list {
		p := &list[i]
		p.Available = true
		if p.Direct {
			p.Port = v.PortBase + (p.Port - 10443)
			p.ServerName = v.ServerName
			if p.Security == "reality" {
				p.ServerName = v.RealityTarget
				p.PublicKey = v.publicKey()
				p.ShortID = v.ShortID
			}
			if a.cfg.Mode != "vps" {
				p.Available = false
				p.Reason = "پورت مستقیم روی سرور مجازی لازم است"
			} else if p.Security == "tls" && certErr != nil {
				p.Available = false
				p.Reason = "ابتدا دامنه و گواهی TLS معتبر VPS را تنظیم کن"
			}
		} else {
			p.Security = "edge"
		}
	}
	return list
}
func (a *App) clientExport(w http.ResponseWriter, r *http.Request) {
	u, ok := a.findUser(r.URL.Query().Get("email"))
	if !ok {
		apiError(w, 404, "کاربر یافت نشد")
		return
	}
	out := []any{}
	xray := []any{}
	settings := a.snapshot().Settings
	for _, l := range u.Links {
		for _, p := range a.snapshot().Profiles {
			if p.Key == l.Profile {
				if o := xrayCloudOutbound(p, u.Raw, settings.PublicURL, settings.Connection); o != nil {
					xray = append(xray, o)
				}
				if o := singBoxOutboundWithEndpoint(p, u.Raw, settings.PublicURL, settings.Connection); o != nil {
					out = append(out, o)
				}
			}
		}
	}
	jsonReply(w, 200, map[string]any{"brand": "ariaatashin", "name": u.Name, "subscription": u.Subscription, "links": u.Links, "singBox": out, "xray": xray, "note": "این‌ها قطعهٔ outbound هستند؛ XHTTP و Shadowsocks روی WS/XHTTP در خروجی Xray قرار دارند و در sing-box گنجانده نمی‌شوند. بعضی کلاینت‌ها پارامترهای لینک ss:// را نادیده می‌گیرند؛ برای Shadowsocks وب از Xray JSON استفاده کن."})
}
