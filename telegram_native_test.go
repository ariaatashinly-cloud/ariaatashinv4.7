//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Run with a locally installed pinned 3x-ui engine. This test never contacts a
// real Telegram account or gateway; only the actual native core is exercised.
func TestNativeTelegramBuyReceiptApproveDeliveryAndRestart(t *testing.T) {
	engine := os.Getenv("ARIA_TEST_ENGINE_DIR")
	if engine == "" {
		t.Skip("set ARIA_TEST_ENGINE_DIR to the pinned engine directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	dir := t.TempDir()
	if e := os.MkdirAll(filepath.Join(dir, "logs"), 0700); e != nil {
		t.Fatal(e)
	}
	bin, e := prepareRuntimeBin(engine, dir)
	if e != nil {
		t.Fatal(e)
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	cfg := Config{Port: 8080, DataDir: dir, EngineDir: engine, PublicURL: "https://native-qa.example", AdminUser: "admin", AdminPassword: "private-native-QA-password-123", Mode: "cloud", Bind: "127.0.0.1"}
	env := append(os.Environ(), "XUI_DB_FOLDER="+dir, "XUI_LOG_FOLDER="+filepath.Join(dir, "logs"), "XUI_BIN_FOLDER="+bin, "XRAY_LOCATION_ASSET="+filepath.Join(engine, "bin"), "XUI_ENABLE_FAIL2BAN=false")
	setup := exec.CommandContext(ctx, filepath.Join(engine, "x-ui"), "setting", "-username", cfg.AdminUser, "-password", cfg.AdminPassword, "-port", strconv.Itoa(port), "-listenIP", "127.0.0.1", "-webBasePath", "/")
	setup.Env, setup.Dir = env, dir
	if e := setup.Run(); e != nil {
		t.Fatal("native initialization failed", e)
	}
	log, e := os.Create(filepath.Join(dir, "native-private.log"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.CommandContext(ctx, filepath.Join(engine, "x-ui"))
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = env, dir, log, log
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	a, e := NewApp(cfg, "http://127.0.0.1:"+strconv.Itoa(port))
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 60; i++ {
		if a.refresh(ctx) == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !a.snapshot().Ready {
		t.Fatal("native core was not ready")
	}
	tg := TelegramConfig{Token: "123456:" + strings.Repeat("q", 35), Enabled: true, ChatID: 999, Mode: "poll"}
	if e := a.storeTelegramSettings(tg, false); e != nil {
		t.Fatal(e)
	}
	if e := a.salesTxn(func(s *SalesData) error {
		s.Settings.Enabled = true
		s.Plans = []SalesPlan{{ID: "p-native", Name: "خرید داخل ربات", Price: 100000, QuotaGB: 10, Days: 30, Profiles: []string{"vless-ws", "vmess-ws"}, Enabled: true}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	var messages []map[string]any
	var configFiles []string
	var qrPhotos int
	a.telegramHTTP = &http.Client{Transport: tgTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "sendDocument") {
			if e := r.ParseMultipartForm(2 << 20); e != nil {
				t.Fatal(e)
			}
			file, _, e := r.FormFile("document")
			if e != nil {
				t.Fatal(e)
			}
			b, _ := io.ReadAll(file)
			_ = file.Close()
			configFiles = append(configFiles, string(b))
			if r.FormValue("chat_id") != "555" {
				t.Fatal("configuration sent to wrong customer")
			}
		} else if strings.HasSuffix(r.URL.Path, "sendPhoto") {
			if e := r.ParseMultipartForm(2 << 20); e != nil {
				t.Fatal(e)
			}
			f, _, e := r.FormFile("photo")
			if e != nil {
				t.Fatal(e)
			}
			im, e := png.Decode(f)
			_ = f.Close()
			if e != nil || im.Bounds().Dx() != 800 || r.FormValue("chat_id") != "555" {
				t.Fatal("invalid QR delivery", e)
			}
			qrPhotos++
		} else if strings.HasSuffix(r.URL.Path, "sendMessage") {
			var m map[string]any
			if e := json.NewDecoder(r.Body).Decode(&m); e != nil {
				t.Fatal(e)
			}
			messages = append(messages, m)
		} else if !strings.HasSuffix(r.URL.Path, "answerCallbackQuery") {
			t.Fatal("unexpected external operation", r.URL.Path)
		}
		return fakeJSON(`{"ok":true,"result":{"message_id":1}}`), nil
	})}
	next := int64(1)
	send := func(text string, chat int64, callback bool) {
		u := botText(text, chat)
		u.ID = next
		next++
		if callback {
			_ = json.Unmarshal([]byte(fmt.Sprintf(`{"id":"cb-%d","data":%q,"from":{"id":%d},"message":{"date":1,"chat":{"id":%d,"type":"private"}}}`, u.ID, text, chat, chat)), &u.Callback)
		}
		if e := a.processUpdate(ctx, a.telegramSnapshot(), u); e != nil {
			t.Fatal(e)
		}
		a.flushSalesNotices(ctx)
	}
	send("/start", 555, false)
	if len(messages) == 0 || !strings.Contains(fmt.Sprint(messages[0]["reply_markup"]), "خرید سرویس") {
		t.Fatal("customer home did not contain purchase button")
	}
	_ = a.salesTxn(func(s *SalesData) error { s.Plans[0].Price = 120000; return nil })
	send("ui:plans:0", 555, true)
	if !strings.Contains(fmt.Sprint(messages[len(messages)-1]), "120000") {
		t.Fatal("new panel price missing from bot")
	}
	send("ui:plan:p-native", 555, true)
	f := a.botFlow("tg-555")
	if f.Nonce == "" {
		t.Fatal("payment selection did not create flow")
	}
	send("ui:pay:"+f.Nonce+":manual", 555, true)
	s := a.salesSnapshot()
	if len(s.Orders) != 1 || s.Orders[0].Amount != 120000 {
		t.Fatal("bot purchase did not create correct order")
	}
	order := s.Orders[0].ID
	send("ui:receipt:"+order, 555, true)
	send("پیگیری پرداخت آزمایشی", 555, false)
	if a.salesSnapshot().Orders[0].Status != "receipt" {
		t.Fatal("receipt did not reach admin")
	}
	send("ui:admin-review:"+order, 999, true)
	send("ui:admin-approve:"+order, 999, true)
	s = a.salesSnapshot()
	if s.Orders[0].Status != "fulfilled" || len(s.Services) != 1 || len(configFiles) != 1 || qrPhotos != 3 {
		t.Fatal("native service or Telegram file was not delivered")
	}
	if !strings.Contains(configFiles[0], "vless://") || !strings.Contains(configFiles[0], "vmess://") || !strings.Contains(configFiles[0], "native-qa.example") {
		t.Fatal("configuration file incomplete or wrong endpoint")
	}
	send("ui:admin-approve:"+order, 999, true)
	if len(a.salesSnapshot().Services) != 1 || len(configFiles) != 1 {
		t.Fatal("repeat approval duplicated native service or delivery")
	}
	b, e := NewApp(cfg, a.base)
	if e != nil {
		t.Fatal(e)
	}
	if e := b.refresh(ctx); e != nil {
		t.Fatal(e)
	}
	if !b.telegramSnapshot().Enabled || len(b.salesSnapshot().Services) != 1 || b.salesSnapshot().Orders[0].Status != "fulfilled" || len(b.snapshot().Users) != 1 {
		t.Fatal("restart lost customer, service or bot settings")
	}
}
