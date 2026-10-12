package main

import (
	"fmt"
	"net/http"
	"regexp"
)

func (a *App) verifiedEdge(id string) (EdgeRoute, bool) {
	for _, v := range a.edgeSnapshot().Routes {
		if v.ID == id && v.Enabled && v.Verified {
			return v, true
		}
	}
	return EdgeRoute{}, false
}
func (a *App) linksForEdge(u User, v EdgeRoute) []string {
	s := a.snapshot()
	links := []string{}
	for _, l := range u.Links {
		for _, p := range s.Profiles {
			if p.Key == l.Profile && !p.Direct && edgeSupports(v, p) {
				p = profileForEdge(p, v)
				links = append(links, labelShareLink(shareLinkWithEndpoint(p, u.Raw, s.Settings.PublicURL, s.Settings.Connection), a.exitLocation()))
			}
		}
	}
	return links
}
func (a *App) queueEdgeDelivery(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Route string `json:"route"`
		ID    string `json:"id"`
	}
	if r.Method != "POST" || readBody(w, r, &in, 2048) != nil || !regexp.MustCompile(`^[a-f0-9]{24}$`).MatchString(in.ID) {
		apiError(w, 400, "درخواست تحویل معتبر نیست")
		return
	}
	edge, ok := a.verifiedEdge(in.Route)
	if !ok {
		apiError(w, 409, "مسیر فعال و تأییدشده لازم است")
		return
	}
	u, ok := a.findUser(in.Email)
	if !ok {
		apiError(w, 404, "کاربر پیدا نشد")
		return
	}
	links := a.linksForEdge(u, edge)
	if len(links) == 0 {
		apiError(w, 409, "این کاربر اتصال سازگار با مسیر ندارد")
		return
	}
	t := a.telegramSnapshot()
	if !t.Enabled || t.ChatID <= 0 || t.Token == "" {
		apiError(w, 409, "ابتدا ربات و شناسهٔ مدیر را فعال کن")
		return
	}
	chat := t.ChatID
	service := ""
	email := u.Email
	destination := "مدیر"
	s := a.salesSnapshot()
	for _, v := range s.Services {
		if v.Email == u.Email && (v.NodeID == "" || v.NodeID == "local") {
			ci := customerIndex(&s, v.CustomerID)
			if ci >= 0 && s.Customers[ci].TelegramID > 0 && !s.Customers[ci].Blocked {
				chat = s.Customers[ci].TelegramID
				service = v.ID
				email = ""
				destination = "صاحب ثبت‌شدهٔ سرویس"
				break
			}
		}
	}
	e := a.salesTxn(func(s *SalesData) error {
		queueConnectionDelivery(s, "edge-delivery:"+in.ID, chat, service, email, len(links), edge.ID)
		return nil
	})
	if e != nil {
		apiError(w, 500, "تحویل ذخیره نشد")
		return
	}
	jsonReply(w, 200, map[string]any{"queued": true, "destination": destination, "configs": len(links), "note": fmt.Sprintf("متن و QR %d کانفیگ برای %s در صف ارسال است", len(links), destination)})
}
