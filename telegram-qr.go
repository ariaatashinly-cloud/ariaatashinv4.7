package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func qrCard(payload, kind string) ([]byte, error) {
	qr, e := qrcode.New(payload, qrcode.Medium)
	if e != nil {
		return nil, fmt.Errorf("لینک برای QR خیلی بلند است؛ فایل یا اشتراک را دریافت کن")
	}
	// Keep the four-module quiet zone and integer scaling. Decoration never touches modules.
	matrix := qr.Bitmap()
	n := len(matrix)
	scale := 640 / n
	if scale < 3 {
		return nil, fmt.Errorf("QR بیش از حد متراکم است؛ فایل یا اشتراک را دریافت کن")
	}
	img := image.NewRGBA(image.Rect(0, 0, 800, 1010))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{13, 20, 34, 255}}, image.Point{}, draw.Src)
	for x := 0; x < 800; x++ {
		c := color.RGBA{uint8(32 + x/15), uint8(105 + x/12), 220, 255}
		draw.Draw(img, image.Rect(x, 0, x+1, 7), &image.Uniform{c}, image.Point{}, draw.Src)
	}
	qrTitle(img, "ARIAATASHIN", 58, 58, 4, color.RGBA{239, 245, 255, 255})
	qrTitle(img, kind, 60, 133, 2, color.RGBA{120, 177, 249, 255})
	rect := image.Rect(55, 200, 745, 890)
	draw.Draw(img, rect, &image.Uniform{color.White}, image.Point{}, draw.Src)
	side := n * scale
	ox := (800 - side) / 2
	oy := 200 + (690-side)/2
	for y, row := range matrix {
		for x, dark := range row {
			if dark {
				draw.Draw(img, image.Rect(ox+x*scale, oy+y*scale, ox+(x+1)*scale, oy+(y+1)*scale), &image.Uniform{color.RGBA{9, 15, 26, 255}}, image.Point{}, draw.Src)
			}
		}
	}
	qrTitle(img, "SCAN TO CONNECT", 60, 930, 2, color.RGBA{234, 243, 255, 255})
	qrTitle(img, "PRIVATE CONNECTION  /  KEEP THIS CODE SAFE", 60, 976, 1, color.RGBA{139, 162, 190, 255})
	var b bytes.Buffer
	if png.Encode(&b, img) != nil {
		return nil, fmt.Errorf("تصویر QR آماده نشد")
	}
	return b.Bytes(), nil
}
func qrTitle(dst draw.Image, text string, x, y, scale int, c color.Color) {
	tmp := image.NewRGBA(image.Rect(0, 0, len(text)*7+2, 16))
	d := font.Drawer{Dst: tmp, Src: &image.Uniform{c}, Face: basicfont.Face7x13, Dot: fixed.P(0, 13)}
	d.DrawString(text)
	for yy := 0; yy < 16; yy++ {
		for xx := 0; xx < tmp.Bounds().Dx(); xx++ {
			r, g, b, a := tmp.At(xx, yy).RGBA()
			if a > 0 {
				draw.Draw(dst, image.Rect(x+xx*scale, y+yy*scale, x+(xx+1)*scale, y+(yy+1)*scale), &image.Uniform{color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}}, image.Point{}, draw.Over)
			}
		}
	}
}
func (a *App) tgQR(ctx context.Context, t TelegramConfig, b []byte, caption string) error {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("chat_id", strconv.FormatInt(t.ChatID, 10))
	_ = writer.WriteField("caption", caption)
	_ = writer.WriteField("protect_content", "false")
	p, e := writer.CreateFormFile("photo", "ariaatashin-connection.png")
	if e != nil {
		return e
	}
	_, _ = p.Write(b)
	_ = writer.Close()
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.telegram.org/bot"+t.Token+"/sendPhoto", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	cl := a.telegramHTTP
	if cl == nil {
		cl = &http.Client{Timeout: 20 * time.Second, CheckRedirect: noRedirect}
	}
	resp, e := cl.Do(req)
	if e != nil {
		return fmt.Errorf("ارسال QR تأیید نشد")
	}
	defer resp.Body.Close()
	var out struct {
		OK bool `json:"ok"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&out) != nil || !out.OK {
		return fmt.Errorf("تلگرام QR را نپذیرفت")
	}
	return nil
}
func queueConnectionDelivery(s *SalesData, key string, chat int64, service, email string, links int, route ...string) {
	start := -1
	if len(route) > 0 {
		start = 0
	}
	for i := start; i < links; i++ {
		for _, kind := range []string{"text", "qr"} {
			id := fmt.Sprintf("%s:%s:%d", key, kind, i)
			addNotice(s, id, chat, "اتصال آماده")
			for j := range s.Notices {
				if s.Notices[j].ID == id {
					s.Notices[j].ConnectionService = service
					s.Notices[j].ConnectionEmail = email
					s.Notices[j].ConnectionIndex = i
					s.Notices[j].ConnectionKind = kind
					if len(route) > 0 {
						s.Notices[j].ConnectionRoute = route[0]
					}
				}
			}
		}
	}
}
func (a *App) connectionNotice(ctx context.Context, t TelegramConfig, n SalesNotice) error {
	var name, sub string
	var links []string
	var label string
	localEmail := n.ConnectionEmail
	if n.ConnectionEmail != "" {
		if n.ChatID != a.telegramSnapshot().ChatID {
			return fmt.Errorf("تحویل مدیر مجاز نیست")
		}
		u, ok := a.findUser(n.ConnectionEmail)
		if !ok {
			return fmt.Errorf("کاربر اتصال پیدا نشد")
		}
		name = u.Name
		sub = u.Subscription
		label = geoLabel(u.Location)
		for _, l := range u.Links {
			links = append(links, l.Link)
		}
	} else {
		s := a.salesSnapshot()
		si := serviceIndex(&s, n.ConnectionService)
		if si < 0 {
			return fmt.Errorf("سرویس اتصال پیدا نشد")
		}
		v := s.Services[si]
		ci := customerIndex(&s, v.CustomerID)
		if ci < 0 || s.Customers[ci].TelegramID != n.ChatID {
			return fmt.Errorf("مالک اتصال مطابقت ندارد")
		}
		localEmail = v.Email
		name = v.Name
		sub = v.Subscription
		links = append([]string{}, v.Links...)
		// Local services use this server's exit; remote panel links retain their own location.
		if v.NodeID == "" || v.NodeID == "local" {
			label = geoLabel(a.exitLocation())
			if u, ok := a.findUser(v.Email); ok {
				links = []string{}
				for _, l := range u.Links {
					links = append(links, l.Link)
				}
			}
			for i := range links {
				links[i] = labelShareLink(links[i], a.exitLocation())
			}
		}
	}
	if n.ConnectionRoute != "" {
		edge, ok := a.verifiedEdge(n.ConnectionRoute)
		if !ok {
			return fmt.Errorf("مسیر تحویل دیگر فعال و تأییدشده نیست")
		}
		u, ok := a.findUser(localEmail)
		if !ok {
			return fmt.Errorf("کاربر مسیر پیدا نشد")
		}
		links = a.linksForEdge(u, edge)
		sub = ""
	}
	payload := sub
	kind := "SUBSCRIPTION"
	title := "اشتراک همهٔ اتصال‌ها"
	if n.ConnectionIndex >= 0 {
		if n.ConnectionIndex >= len(links) {
			return nil
		}
		payload = links[n.ConnectionIndex]
		kind = "CONFIG " + strconv.Itoa(n.ConnectionIndex+1)
		title = "کانفیگ " + strconv.Itoa(n.ConnectionIndex+1)
	}
	if payload == "" {
		return nil
	}
	if len([]rune(name)) > 100 {
		name = string([]rune(name)[:100])
	}
	caption := "📱 " + name + " · " + title
	if label != "" {
		caption += "\n" + label
	}
	if n.ConnectionKind == "qr" {
		// Oversize custom URIs remain available as text/document. Subscription has its own QR.
		data, e := qrCard(payload, kind)
		if e != nil {
			return nil
		}
		return a.tgQR(ctx, t, data, caption+"\nبا برنامهٔ اتصال اسکن کن.")
	}
	if len([]rune(caption+"\n\n"+payload)) > 3900 {
		return a.tgCall(ctx, t, "sendMessage", map[string]any{"chat_id": t.ChatID, "text": caption + "\nلینک بلند است؛ فایل کامل و QR اشتراک را دریافت کن."}, nil)
	}
	return a.tgCall(ctx, t, "sendMessage", map[string]any{"chat_id": t.ChatID, "text": caption + "\n\n" + payload, "link_preview_options": map[string]bool{"is_disabled": true}}, nil)
}
func (a *App) queueAdminConnections(email, key string) error {
	u, ok := a.findUser(email)
	if !ok {
		return fmt.Errorf("کاربر پیدا نشد")
	}
	return a.salesTxn(func(s *SalesData) error {
		queueConnectionDelivery(s, key, a.telegramSnapshot().ChatID, "", email, len(u.Links))
		return nil
	})
}
