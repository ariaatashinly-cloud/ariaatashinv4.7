package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
)

func queueReceiptPhoto(s *SalesData, id string, chat int64, order string) {
	addNotice(s, id, chat, "عکس رسید")
	for i := range s.Notices {
		if s.Notices[i].ID == id && !s.Notices[i].Sent {
			s.Notices[i].ReceiptOrder = order
			break
		}
	}
}
func (a *App) sendReceiptPhoto(ctx context.Context, t TelegramConfig, n SalesNotice) error {
	admin := a.telegramSnapshot()
	s := a.salesSnapshot()
	i := orderIndex(&s, n.ReceiptOrder)
	if n.ChatID != admin.ChatID || i < 0 || s.Orders[i].TelegramPhoto == "" {
		return fmt.Errorf("عکس رسید مدیر پیدا نشد")
	}
	o := s.Orders[i]
	return a.tgCall(ctx, t, "sendPhoto", map[string]any{"chat_id": n.ChatID, "photo": o.TelegramPhoto, "caption": "🧾 رسید سفارش " + o.ID + " · " + strconv.FormatInt(o.Amount, 10) + " تومان", "protect_content": true, "reply_markup": map[string]any{"inline_keyboard": [][]BotButton{{botButton("بررسی و تأیید / رد", "admin-review:"+o.ID)}}}}, nil)
}
func (a *App) receiptImage(w http.ResponseWriter, r *http.Request) {
	s := a.salesSnapshot()
	i := orderIndex(&s, r.URL.Query().Get("id"))
	if i < 0 || s.Orders[i].TelegramPhoto == "" {
		apiError(w, 404, "عکس تلگرام برای این سفارش وجود ندارد")
		return
	}
	t := a.telegramSnapshot()
	if t.Token == "" {
		apiError(w, 409, "توکن همان ربات فرستندهٔ عکس لازم است")
		return
	}
	var f struct {
		Path string `json:"file_path"`
	}
	if a.tgCall(r.Context(), t, "getFile", map[string]string{"file_id": s.Orders[i].TelegramPhoto}, &f) != nil || !regexp.MustCompile(`^[A-Za-z0-9_-]+/[A-Za-z0-9_.-]+$`).MatchString(f.Path) {
		apiError(w, 502, "دریافت عکس از تلگرام انجام نشد؛ دوباره امتحان کن")
		return
	}
	req, e := http.NewRequestWithContext(r.Context(), "GET", "https://api.telegram.org/file/bot"+t.Token+"/"+f.Path, nil)
	if e != nil {
		apiError(w, 502, "دریافت عکس آماده نشد")
		return
	}
	cl := a.telegramHTTP
	if cl == nil {
		cl = &http.Client{Timeout: 25_000_000_000, CheckRedirect: noRedirect}
	}
	resp, e := cl.Do(req)
	if e != nil {
		apiError(w, 502, "عکس تلگرام در دسترس نیست")
		return
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, 10<<20+1))
	if e != nil || resp.StatusCode != 200 || len(data) > 10<<20 {
		apiError(w, 502, "عکس قابل دریافت نیست")
		return
	}
	mime := http.DetectContentType(data)
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" {
		apiError(w, 502, "پاسخ دریافت‌شده تصویر معتبر نیست")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
