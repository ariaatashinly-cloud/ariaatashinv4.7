package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTelegramActivationEnablesSalesPreservesUpdatesAndSurvivesRestart(t *testing.T) {
	a, c := salesFixture(t)
	token := "123456:" + strings.Repeat("a", 35)
	a.telegram = TelegramConfig{Token: token, ChatID: 999, Mode: "poll", Offset: 52, Seen: []int64{50, 51}}
	_ = a.salesTxn(func(s *SalesData) error { s.Settings.Enabled = false; return nil })
	a.telegramHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "getMe"):
			return fakeJSON(`{"ok":true,"result":{"username":"aria_test_bot","is_bot":true}}`), nil
		case strings.HasSuffix(r.URL.Path, "getWebhookInfo"):
			return fakeJSON(`{"ok":true,"result":{"url":""}}`), nil
		}
		t.Fatalf("activation unexpectedly sent or consumed messages: %s", r.URL.Path)
		return nil, nil
	})}
	w := httptest.NewRecorder()
	a.activateTelegram(w, browserRequest("POST", "/api/telegram/activate", "https://panel.example", `{"chatId":999}`))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	tg := a.telegramSnapshot()
	if !tg.Enabled || tg.Offset != 52 || len(tg.Seen) != 2 || !a.salesSnapshot().Settings.Enabled {
		t.Fatal("activation lost updates or left sales disabled")
	}
	if strings.Contains(w.Body.String(), token) {
		t.Fatal("private token leaked")
	}
	// A fresh plan list reads the price saved in the panel, with no redeploy.
	_ = a.salesTxn(func(s *SalesData) error { s.Plans[0].Price = 87654; return nil })
	u := botText("/plans", c.TelegramID)
	u.ID = 53
	if e := a.processUpdate(context.Background(), tg, u); e != nil {
		t.Fatal(e)
	}
	s := a.salesSnapshot()
	last := s.Notices[len(s.Notices)-1]
	if len(last.Buttons) == 0 || !strings.Contains(last.Buttons[0][0].Text, "87654") {
		t.Fatal("panel price did not reach Telegram menu")
	}
	b, e := NewApp(a.cfg, a.base)
	if e != nil {
		t.Fatal(e)
	}
	if !b.telegramSnapshot().Enabled || !b.salesSnapshot().Settings.Enabled || b.telegramSnapshot().Offset != 54 {
		t.Fatal("restart lost bot configuration")
	}
}

func TestTelegramActivationRequiresExplicitWebhookReplacement(t *testing.T) {
	a, _ := salesFixture(t)
	token := "123456:" + strings.Repeat("b", 35)
	deleted := 0
	a.telegramHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "getMe") {
			return fakeJSON(`{"ok":true,"result":{"username":"aria_test_bot","is_bot":true}}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "getWebhookInfo") {
			return fakeJSON(`{"ok":true,"result":{"url":"https://old.example/private-hook"}}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "deleteWebhook") {
			deleted++
			var body map[string]bool
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["drop_pending_updates"] {
				t.Fatal("pending purchases were dropped")
			}
			return fakeJSON(`{"ok":true,"result":true}`), nil
		}
		t.Fatal("unexpected Telegram call")
		return nil, nil
	})}
	for _, replace := range []bool{false, true} {
		w := httptest.NewRecorder()
		a.activateTelegram(w, browserRequest("POST", "/api/telegram/activate", "https://panel.example", fmt.Sprintf(`{"token":"%s","chatId":999,"replaceWebhook":%t}`, token, replace)))
		if !replace {
			if w.Code != 409 || deleted != 0 || a.telegramSnapshot().Enabled {
				t.Fatal("webhook replaced silently")
			}
		}
		if replace && (w.Code != 200 || deleted != 1 || a.telegramSnapshot().Mode != "poll") {
			t.Fatal("confirmed replacement failed", w.Code, w.Body.String())
		}
	}
}

func TestTelegramActivationFailureCannotEnableSales(t *testing.T) {
	a, _ := salesFixture(t)
	_ = a.salesTxn(func(s *SalesData) error { s.Settings.Enabled = false; return nil })
	token := "123456:" + strings.Repeat("c", 35)
	a.telegramHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		res := fakeJSON(`{"ok":false,"error_code":401,"description":"secret-token-must-not-leak"}`)
		res.StatusCode = 401
		return res, nil
	})}
	w := httptest.NewRecorder()
	a.activateTelegram(w, browserRequest("POST", "/api/telegram/activate", "https://panel.example", fmt.Sprintf(`{"token":"%s","chatId":999}`, token)))
	if w.Code != 502 || a.telegramSnapshot().Enabled || a.salesSnapshot().Settings.Enabled || strings.Contains(w.Body.String(), "secret-token") {
		t.Fatal("failed authentication enabled sale or leaked details")
	}
}

func TestOldTelegramMenuStillAcceptsNewPurchaseClick(t *testing.T) {
	a, c := salesFixture(t)
	tg := TelegramConfig{Token: "123456:" + strings.Repeat("d", 35), ChatID: 999, Enabled: true}
	a.telegram = tg
	a.telegramHTTP = &http.Client{Transport: tgTransport(func(*http.Request) (*http.Response, error) { return fakeJSON(`{"ok":true,"result":true}`), nil })}
	u := botText("", c.TelegramID)
	u.ID = 9
	_ = json.Unmarshal([]byte(`{"id":"fresh-click","data":"ui:plans:0","from":{"id":555},"message":{"date":1,"chat":{"id":555,"type":"private"}}}`), &u.Callback)
	if e := a.processUpdate(context.Background(), tg, u); e != nil {
		t.Fatal(e)
	}
	s := a.salesSnapshot()
	if len(s.Notices) != 1 || len(s.Notices[0].Buttons) == 0 || !strings.Contains(s.Notices[0].Buttons[0][0].Text, "پلن تست") {
		t.Fatal("new click on an old menu was ignored")
	}
	_ = a.processUpdate(context.Background(), tg, u)
	if len(a.salesSnapshot().Notices) != 1 {
		t.Fatal("callback replay duplicated response")
	}
}

func TestDisabledShopRespondsAndHealthRedactsToken(t *testing.T) {
	a, c := salesFixture(t)
	a.telegram = TelegramConfig{Token: "123456:" + strings.Repeat("e", 35), ChatID: 999, Enabled: true}
	_ = a.salesTxn(func(s *SalesData) error { s.Settings.Enabled = false; return nil })
	u := botText("/start", c.TelegramID)
	u.ID = 6
	if e := a.processUpdate(context.Background(), a.telegramSnapshot(), u); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(a.salesSnapshot().Notices[0].Text, "غیرفعال") {
		t.Fatal("disabled shop remained silent")
	}
	a.recordTelegramPoll(fmt.Errorf("دریافت پیام تداخل دارد"))
	b, _ := json.Marshal(a.publicTelegram())
	if strings.Contains(string(b), a.telegramSnapshot().Token) || !strings.Contains(string(b), "تداخل") {
		t.Fatal("health missed error or leaked token")
	}
	a.recordTelegramPoll(nil)
	a.recordTelegramDelivery(nil)
	status := a.telegramPublicStatus()["health"].(TelegramHealth)
	if status.PollError != "" || status.LastPoll < time.Now().Add(-time.Minute).UnixMilli() || status.LastDelivery == 0 {
		t.Fatal("health failed to recover")
	}
}
