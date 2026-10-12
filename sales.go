package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ShopSettings struct {
	Enabled         bool     `json:"enabled"`
	Name            string   `json:"name"`
	Support         string   `json:"support"`
	PaymentNote     string   `json:"paymentNote"`
	CardNumber      string   `json:"cardNumber"`
	CardHolder      string   `json:"cardHolder"`
	CompatEnabled   bool     `json:"compatEnabled"`
	BankName        string   `json:"bankName"`
	AccountNumber   string   `json:"accountNumber"`
	IBAN            string   `json:"iban"`
	Welcome         string   `json:"welcome"`
	TrialEnabled    bool     `json:"trialEnabled"`
	TrialQuotaGB    float64  `json:"trialQuotaGB"`
	TrialDays       int      `json:"trialDays"`
	TrialProfiles   []string `json:"trialProfiles"`
	ReminderEnabled bool     `json:"reminderEnabled"`
}
type SalesPlan struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Price       int64    `json:"price"`
	Stars       int64    `json:"stars"`
	QuotaGB     float64  `json:"quotaGB"`
	Days        int      `json:"days"`
	Profiles    []string `json:"profiles"`
	NodeID      string   `json:"nodeId"`
	Enabled     bool     `json:"enabled"`
}
type PanelNode struct {
	ID                    string              `json:"id"`
	Name                  string              `json:"name"`
	Type                  string              `json:"type"`
	URL                   string              `json:"url"`
	PublicURL             string              `json:"publicURL"`
	Token                 string              `json:"token,omitempty"`
	Username              string              `json:"username,omitempty"`
	Password              string              `json:"password,omitempty"`
	InboundIDs            []int               `json:"inboundIds"`
	Tags                  map[string][]string `json:"tags"`
	Edge                  bool                `json:"edge"`
	Enabled               bool                `json:"enabled"`
	LastTest              int64               `json:"lastTest"`
	LastOK                bool                `json:"lastOK"`
	LastError             string              `json:"lastError"`
	CredentialsConfigured bool                `json:"credentialsConfigured"`
}
type SalesCustomer struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	TelegramID   int64  `json:"telegramId"`
	Balance      int64  `json:"balance"`
	Blocked      bool   `json:"blocked"`
	Created      int64  `json:"created"`
	ReceiptOrder string `json:"receiptOrder,omitempty"`
	TrialOrder   string `json:"trialOrder,omitempty"`
}
type ProvisionInput struct {
	OperationID string   `json:"operationId"`
	Email       string   `json:"email"`
	UUID        string   `json:"uuid"`
	Password    string   `json:"password"`
	Auth        string   `json:"auth"`
	SubID       string   `json:"subId"`
	Name        string   `json:"name"`
	Quota       int64    `json:"quota"`
	Expiry      int64    `json:"expiry"`
	Profiles    []string `json:"profiles"`
}
type SalesOrder struct {
	ID            string         `json:"id"`
	CustomerID    string         `json:"customerId"`
	Kind          string         `json:"kind"`
	PlanID        string         `json:"planId"`
	ServiceID     string         `json:"serviceId"`
	Plan          SalesPlan      `json:"plan"`
	Amount        int64          `json:"amount"`
	Currency      string         `json:"currency"`
	Method        string         `json:"method"`
	Status        string         `json:"status"`
	Receipt       string         `json:"receipt"`
	ReceiptFile   string         `json:"receiptFile,omitempty"`
	TelegramPhoto string         `json:"telegramPhoto,omitempty"`
	PaymentID     string         `json:"paymentId,omitempty"`
	Idempotency   string         `json:"idempotency,omitempty"`
	Created       int64          `json:"created"`
	Updated       int64          `json:"updated"`
	Error         string         `json:"error"`
	Provision     ProvisionInput `json:"provision"`
	DesiredReady  bool           `json:"desiredReady"`
	Activated     bool           `json:"activated"`
	Coupon        string         `json:"coupon,omitempty"`
}
type SoldService struct {
	ID              string   `json:"id"`
	OrderID         string   `json:"orderId"`
	CustomerID      string   `json:"customerId"`
	PlanID          string   `json:"planId"`
	NodeID          string   `json:"nodeId"`
	Email           string   `json:"email"`
	Name            string   `json:"name"`
	Subscription    string   `json:"subscription"`
	Links           []string `json:"links"`
	Quota           int64    `json:"quota"`
	Expiry          int64    `json:"expiry"`
	Used            int64    `json:"used"`
	Enabled         bool     `json:"enabled"`
	Created         int64    `json:"created"`
	Updated         int64    `json:"updated"`
	Reminders       []string `json:"reminders,omitempty"`
	ReminderChecked int64    `json:"reminderChecked,omitempty"`
}
type WalletEntry struct {
	ID         string `json:"id"`
	CustomerID string `json:"customerId"`
	Delta      int64  `json:"delta"`
	OrderID    string `json:"orderId"`
	Reason     string `json:"reason"`
	Created    int64  `json:"created"`
}
type SalesTicket struct {
	ID         string `json:"id"`
	CustomerID string `json:"customerId"`
	Text       string `json:"text"`
	Reply      string `json:"reply"`
	Status     string `json:"status"`
	Created    int64  `json:"created"`
}
type Coupon struct {
	Code    string `json:"code"`
	Percent int64  `json:"percent"`
	Limit   int    `json:"limit"`
	Used    int    `json:"used"`
	Expires int64  `json:"expires"`
	Enabled bool   `json:"enabled"`
}
type IntegrationKey struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Hash    string   `json:"hash,omitempty"`
	Scopes  []string `json:"scopes"`
	Expires int64    `json:"expires"`
	Revoked bool     `json:"revoked"`
	Created int64    `json:"created"`
}
type PortalSession struct {
	Hash       string `json:"hash"`
	CustomerID string `json:"customerId"`
	Expires    int64  `json:"expires"`
	Magic      bool   `json:"magic"`
}
type APIAccount struct {
	Username string          `json:"username"`
	KeyID    string          `json:"keyId"`
	Input    ProvisionInput  `json:"input"`
	Pending  *ProvisionInput `json:"pendingRotation,omitempty"`
}
type SalesNotice struct {
	ConnectionRoute   string        `json:"connectionRoute,omitempty"`
	ConnectionService string        `json:"connectionService,omitempty"`
	ConnectionEmail   string        `json:"connectionEmail,omitempty"`
	ConnectionIndex   int           `json:"connectionIndex,omitempty"`
	ConnectionKind    string        `json:"connectionKind,omitempty"`
	ReceiptOrder      string        `json:"receiptOrder,omitempty"`
	Buttons           [][]BotButton `json:"buttons,omitempty"`
	DocumentService   string        `json:"documentService,omitempty"`
	ID                string        `json:"id"`
	ChatID            int64         `json:"chatId"`
	Text              string        `json:"text"`
	Sent              bool          `json:"sent"`
	Attempts          int           `json:"attempts"`
	LastAttempt       int64         `json:"lastAttempt"`
}
type SalesData struct {
	Version   int              `json:"version"`
	Settings  ShopSettings     `json:"settings"`
	Plans     []SalesPlan      `json:"plans"`
	Nodes     []PanelNode      `json:"nodes"`
	Customers []SalesCustomer  `json:"customers"`
	Orders    []SalesOrder     `json:"orders"`
	Services  []SoldService    `json:"services"`
	Ledger    []WalletEntry    `json:"ledger"`
	Tickets   []SalesTicket    `json:"tickets"`
	Coupons   []Coupon         `json:"coupons"`
	Keys      []IntegrationKey `json:"keys"`
	Sessions  []PortalSession  `json:"sessions"`
	Accounts  []APIAccount     `json:"accounts"`
	Notices   []SalesNotice    `json:"notices"`
	Gateways  GatewaySettings  `json:"gateways"`
	Payments  []PaymentAttempt `json:"payments"`
	BotFlows  []BotFlow        `json:"botFlows"`
}

func cloneSales(s SalesData) SalesData {
	b, _ := json.Marshal(s)
	var n SalesData
	_ = json.Unmarshal(b, &n)
	return n
}
func (a *App) salesSnapshot() SalesData {
	a.salesMu.Lock()
	defer a.salesMu.Unlock()
	return cloneSales(a.sales)
}
func (a *App) salesTxn(fn func(*SalesData) error) error {
	a.salesMu.Lock()
	defer a.salesMu.Unlock()
	next := cloneSales(a.sales)
	if err := fn(&next); err != nil {
		return err
	}
	if err := atomicJSON(a.cfg.DataDir, "aria-commerce.json", next); err != nil {
		return fmt.Errorf("ذخیرهٔ دادهٔ فروش انجام نشد؛ Volume و دسترسی نوشتن را بررسی کن")
	}
	a.sales = next
	return nil
}
func (a *App) loadSales() error {
	a.sales = SalesData{Version: 1, Settings: ShopSettings{Name: "ariaatashin", PaymentNote: "پس از پرداخت، رسید را ثبت کن؛ سرویس پس از تأیید مدیر تحویل می‌شود."}}
	b, err := os.ReadFile(filepath.Join(a.cfg.DataDir, "aria-commerce.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if json.Unmarshal(b, &a.sales) != nil || a.sales.Version != 1 {
		return fmt.Errorf("commerce data invalid; restore a verified private backup")
	}
	return nil
}
func customerIndex(s *SalesData, id string) int {
	for i := range s.Customers {
		if s.Customers[i].ID == id {
			return i
		}
	}
	return -1
}
func orderIndex(s *SalesData, id string) int {
	for i := range s.Orders {
		if s.Orders[i].ID == id {
			return i
		}
	}
	return -1
}
func serviceIndex(s *SalesData, id string) int {
	for i := range s.Services {
		if s.Services[i].ID == id {
			return i
		}
	}
	return -1
}
func planIndex(s *SalesData, id string) int {
	for i := range s.Plans {
		if s.Plans[i].ID == id {
			return i
		}
	}
	return -1
}
func nodeIndex(s *SalesData, id string) int {
	for i := range s.Nodes {
		if s.Nodes[i].ID == id {
			return i
		}
	}
	return -1
}
func addNotice(s *SalesData, id string, chat int64, text string) {
	if chat <= 0 {
		return
	}
	for _, n := range s.Notices {
		if n.ID == id {
			return
		}
	}
	s.Notices = append(s.Notices, SalesNotice{ID: id, ChatID: chat, Text: text})
}
func newProvision(name string, gb float64, days int, profiles []string) ProvisionInput {
	id := randomToken(16)
	expiry := int64(0)
	if days > 0 {
		expiry = time.Now().Add(time.Duration(days) * 24 * time.Hour).UnixMilli()
	}
	return ProvisionInput{id, "aria-" + id[:12], newUUID(), randomToken(20), randomToken(20), randomToken(16), name, int64(gb * (1 << 30)), expiry, profiles}
}
func hashToken(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func safeLabel(s string, max int) bool {
	return strings.TrimSpace(s) != "" && len([]rune(s)) <= max && !strings.ContainsAny(s, "\r\n\x00")
}
func (a *App) validatePlan(p SalesPlan) error {
	if !safeLabel(p.Name, 64) || len([]rune(p.Description)) > 1000 || p.Price < 1 || p.Price > 1e12 || p.Stars < 0 || p.Stars > 100000 {
		return fmt.Errorf("نام و قیمت پلن معتبر نیست")
	}
	if err := validateCreate(CreateRequest{Name: p.Name, Count: 1, QuotaGB: p.QuotaGB, Days: p.Days, Profiles: p.Profiles}); err != nil {
		return err
	}
	if p.NodeID == "" || p.NodeID == "local" {
		if p.Enabled {
			for _, key := range p.Profiles {
				for _, tpl := range a.profileCatalog() {
					if tpl.Key == key && !tpl.Available {
						return fmt.Errorf("الگوی %s روی این میزبان فعال نیست", key)
					}
				}
			}
		}
		return nil
	}
	s := a.salesSnapshot()
	i := nodeIndex(&s, p.NodeID)
	if i < 0 {
		return fmt.Errorf("پنل مقصد یافت نشد")
	}
	return nil
}
func (a *App) publicSales() any {
	s := a.salesSnapshot()
	for i := range s.Nodes {
		n := &s.Nodes[i]
		n.CredentialsConfigured = n.Token != "" || n.Username != "" && n.Password != ""
		n.Token = ""
		n.Password = ""
	}
	for i := range s.Keys {
		s.Keys[i].Hash = ""
	}
	for i := range s.Orders {
		s.Orders[i].Provision = ProvisionInput{}
		s.Orders[i].Idempotency = ""
	}
	income := int64(0)
	pending := 0
	for _, o := range s.Orders {
		if o.Status == "fulfilled" && o.Kind != "topup" && o.Currency == "IRT" {
			income += o.Amount
		}
		if o.Status == "receipt" {
			pending++
		}
	}
	// The UI never receives portal credentials, API hashes or the notification queue.
	return map[string]any{"settings": s.Settings, "plans": s.Plans, "nodes": s.Nodes, "customers": s.Customers, "orders": lastOrders(s.Orders, 300), "services": s.Services, "ledger": s.Ledger, "tickets": s.Tickets, "coupons": s.Coupons, "keys": s.Keys, "stats": map[string]any{"income": income, "pending": pending, "customers": len(s.Customers), "services": len(s.Services)}, "nodeTypes": []string{"aria", "3x-ui", "marzban"}, "scopes": integrationScopes, "gateways": publicGateways(s), "paymentMethods": paymentMethods(s), "paymentAttempts": paymentSummaries(s)}
}
func lastOrders(o []SalesOrder, n int) []SalesOrder {
	if len(o) > n {
		o = o[len(o)-n:]
	}
	sort.Slice(o, func(i, j int) bool { return o[i].Created > o[j].Created })
	return o
}
func (a *App) createCustomer(name string, tg int64) (SalesCustomer, error) {
	if !safeLabel(name, 64) || tg < 0 {
		return SalesCustomer{}, fmt.Errorf("نام مشتری معتبر نیست")
	}
	id := "c-" + randomToken(10)
	if tg > 0 {
		id = "tg-" + strconv.FormatInt(tg, 10)
	}
	var out SalesCustomer
	err := a.salesTxn(func(s *SalesData) error {
		i := customerIndex(s, id)
		if i >= 0 {
			out = s.Customers[i]
			return nil
		}
		if len(s.Customers) >= 50000 {
			return fmt.Errorf("سقف مشتریان این نمونه پر شده است")
		}
		out = SalesCustomer{ID: id, Name: name, TelegramID: tg, Created: time.Now().UnixMilli()}
		s.Customers = append(s.Customers, out)
		return nil
	})
	return out, err
}

type BuyRequest struct {
	CustomerID     string `json:"customerId"`
	PlanID         string `json:"planId"`
	ServiceID      string `json:"serviceId"`
	Kind           string `json:"kind"`
	Method         string `json:"method"`
	Amount         int64  `json:"amount"`
	Idempotency    string `json:"idempotency"`
	Coupon         string `json:"coupon"`
	ExpectedAmount *int64 `json:"expectedAmount,omitempty"`
}

func (a *App) newOrder(req BuyRequest) (SalesOrder, error) {
	var out SalesOrder
	err := a.salesTxn(func(s *SalesData) error {
		if !s.Settings.Enabled {
			return fmt.Errorf("فروشگاه فعلاً غیرفعال است")
		}
		ci := customerIndex(s, req.CustomerID)
		if ci < 0 || s.Customers[ci].Blocked {
			return fmt.Errorf("مشتری فعال یافت نشد")
		}
		if len(req.Idempotency) < 8 || len(req.Idempotency) > 120 {
			return fmt.Errorf("شناسهٔ یکتای درخواست لازم است")
		}
		for _, o := range s.Orders {
			if o.CustomerID == req.CustomerID && o.Idempotency == req.Idempotency {
				if o.PlanID != req.PlanID || o.ServiceID != req.ServiceID || o.Kind != req.Kind || o.Method != req.Method || o.Coupon != strings.ToUpper(strings.TrimSpace(req.Coupon)) || req.Kind == "topup" && o.Amount != req.Amount {
					return fmt.Errorf("این شناسه قبلاً برای سفارش دیگری استفاده شده است")
				}
				out = o
				return nil
			}
		}
		if len(s.Orders) >= 100000 {
			return fmt.Errorf("سقف سفارش‌های این نمونه پر شده است")
		}
		pending := 0
		for _, o := range s.Orders {
			if o.CustomerID == req.CustomerID && (o.Status == "pending" || o.Status == "receipt") {
				pending++
			}
		}
		if pending >= 10 {
			return fmt.Errorf("اول سفارش‌های باز را تعیین تکلیف کن")
		}
		if req.Method != "manual" && req.Method != "wallet" && req.Method != "stars" && !gatewayMethod(req.Method) {
			return fmt.Errorf("روش پرداخت معتبر نیست")
		}
		out = SalesOrder{ID: "o-" + randomToken(8), CustomerID: req.CustomerID, PlanID: req.PlanID, Kind: req.Kind, Method: req.Method, Status: "pending", Currency: "IRT", Idempotency: req.Idempotency, Created: time.Now().UnixMilli(), Updated: time.Now().UnixMilli()}
		if req.Kind == "topup" {
			if (req.Method != "manual" && !gatewayMethod(req.Method)) || req.Amount < 1000 || req.Amount > 1e9 {
				return fmt.Errorf("شارژ کیف پول با مبلغ ۱۰۰۰ تا یک میلیارد تومان و رسید یا درگاه انجام می‌شود")
			}
			out.Amount = req.Amount
		} else {
			if req.Kind != "buy" && req.Kind != "renew" {
				return fmt.Errorf("نوع سفارش معتبر نیست")
			}
			pi := planIndex(s, req.PlanID)
			if pi < 0 || !s.Plans[pi].Enabled {
				return fmt.Errorf("پلن فعال یافت نشد")
			}
			p := s.Plans[pi]
			out.Plan = p
			out.Amount = p.Price
			out.Provision = newProvision(p.Name, p.QuotaGB, p.Days, p.Profiles)
			if p.NodeID != "" && p.NodeID != "local" {
				ni := nodeIndex(s, p.NodeID)
				if ni < 0 || !s.Nodes[ni].Enabled {
					return fmt.Errorf("پنل مقصد غیرفعال است")
				}
			}
			if req.Kind == "renew" {
				si := serviceIndex(s, req.ServiceID)
				if si < 0 || s.Services[si].CustomerID != req.CustomerID {
					return fmt.Errorf("سرویس مشتری یافت نشد")
				}
				if s.Services[si].NodeID != p.NodeID {
					return fmt.Errorf("تمدید باید روی همان پنل مقصد انجام شود")
				}
				for _, o := range s.Orders {
					if o.ServiceID == req.ServiceID && o.Status != "fulfilled" && o.Status != "cancelled" {
						return fmt.Errorf("سفارش دیگری برای این سرویس باز است")
					}
				}
				out.ServiceID = req.ServiceID
			}
			if req.Coupon != "" {
				if req.Method == "stars" {
					return fmt.Errorf("کد تخفیف برای پرداخت تومانی است")
				}
				code := strings.ToUpper(strings.TrimSpace(req.Coupon))
				found := false
				for i := range s.Coupons {
					c := &s.Coupons[i]
					if c.Code == code && c.Enabled && (c.Expires == 0 || c.Expires > time.Now().UnixMilli()) && (c.Limit == 0 || c.Used < c.Limit) {
						out.Amount -= out.Amount * c.Percent / 100
						out.Coupon = code
						c.Used++
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("کد تخفیف فعال یافت نشد")
				}
			}
			if req.Method == "stars" {
				if p.Stars <= 0 {
					return fmt.Errorf("پرداخت Stars برای این پلن فعال نیست")
				}
				out.Currency = "XTR"
				out.Amount = p.Stars
			}
		}
		if gatewayMethod(req.Method) {
			g := gatewayCredential(*s, req.Method)
			if !g.Enabled || g.Merchant == "" || out.Amount < 1000 || out.Amount > 1000000000 {
				return fmt.Errorf("درگاه فعال نیست یا مبلغ بیرون از محدوده ۱۰۰۰ تا یک میلیارد تومان است")
			}
		}
		if req.ExpectedAmount != nil && out.Currency == "IRT" && out.Amount != *req.ExpectedAmount {
			return fmt.Errorf("قیمت یا تخفیف تغییر کرده؛ پلن را دوباره انتخاب کن")
		}
		if req.Method == "wallet" {
			if s.Customers[ci].Balance < out.Amount {
				return fmt.Errorf("موجودی کیف پول کافی نیست")
			}
			s.Customers[ci].Balance -= out.Amount
			s.Ledger = append(s.Ledger, WalletEntry{ID: "l-" + randomToken(8), CustomerID: req.CustomerID, Delta: -out.Amount, OrderID: out.ID, Reason: "خرید از کیف پول", Created: out.Created})
			out.Status = "paid"
		}
		s.Orders = append(s.Orders, out)
		return nil
	})
	return out, err
}
func (a *App) submitReceipt(customer, id, text, file, photo string) error {
	if len([]rune(text)) > 1000 {
		return fmt.Errorf("متن رسید بیش از حد طولانی است")
	}
	if strings.TrimSpace(text) == "" && file == "" && photo == "" {
		return fmt.Errorf("شمارهٔ پیگیری یا عکس رسید لازم است")
	}
	t := a.telegramSnapshot()
	return a.salesTxn(func(s *SalesData) error {
		ci := customerIndex(s, customer)
		i := orderIndex(s, id)
		if ci < 0 || s.Customers[ci].Blocked || i < 0 || s.Orders[i].CustomerID != customer {
			return fmt.Errorf("سفارش مشتری یافت نشد")
		}
		o := &s.Orders[i]
		if o.Method != "manual" || (o.Status != "pending" && o.Status != "receipt") {
			return fmt.Errorf("این سفارش رسید دستی نمی‌پذیرد")
		}
		o.Receipt = text
		o.ReceiptFile = file
		o.TelegramPhoto = photo
		o.Status = "receipt"
		o.Updated = time.Now().UnixMilli()
		s.Customers[ci].ReceiptOrder = ""
		addRichNotice(s, "receipt:"+id, t.ChatID, "🧾 رسید تازه\nسفارش "+id+"\n"+s.Customers[ci].Name+"\nمبلغ: "+strconv.FormatInt(o.Amount, 10)+" تومان\nبررسی در پنل فروش یا /approve "+id, [][]BotButton{{botButton("بررسی رسید", "admin-review:"+id)}})
		if photo != "" {
			queueReceiptPhoto(s, "receipt-photo:"+id+":"+strconv.FormatInt(o.Updated, 10), t.ChatID, id)
		}
		return nil
	})
}
func (a *App) approveOrder(ctx context.Context, id string) (SalesOrder, error) {
	err := a.salesTxn(func(s *SalesData) error {
		i := orderIndex(s, id)
		if i < 0 {
			return fmt.Errorf("سفارش یافت نشد")
		}
		o := &s.Orders[i]
		if o.Currency != "IRT" || o.Method != "manual" {
			return fmt.Errorf("این سفارش با تأیید دستی پرداخت نمی‌شود")
		}
		if o.Status == "fulfilled" || o.Status == "paid" || o.Status == "provisioning" || o.Status == "uncertain" || o.Status == "failed" {
			return nil
		}
		if o.Status != "receipt" {
			return fmt.Errorf("ابتدا رسید مشتری را بررسی کن")
		}
		o.Status = "paid"
		o.Updated = time.Now().UnixMilli()
		return nil
	})
	if err != nil {
		return SalesOrder{}, err
	}
	return a.fulfillOrder(ctx, id)
}
func (a *App) cancelOrder(customer, id string, admin bool) error {
	return a.salesTxn(func(s *SalesData) error {
		i := orderIndex(s, id)
		if i < 0 || (!admin && s.Orders[i].CustomerID != customer) {
			return fmt.Errorf("سفارش یافت نشد")
		}
		o := &s.Orders[i]
		if o.Status == "cancelled" {
			return nil
		}
		if o.Status != "pending" && o.Status != "receipt" {
			return fmt.Errorf("سفارش پرداخت‌شده را نمی‌توان لغو کرد؛ ابتدا وضعیت سرویس بررسی شود")
		}
		if gatewayMethod(o.Method) {
			for _, p := range s.Payments {
				if p.OrderID == id && p.Status != "failed" {
					return fmt.Errorf("این سفارش لینک درگاه دارد؛ ابتدا وضعیت پرداخت باید مشخص شود")
				}
			}
		}
		o.Status = "cancelled"
		o.Updated = time.Now().UnixMilli()
		if o.Coupon != "" {
			for j := range s.Coupons {
				if s.Coupons[j].Code == o.Coupon && s.Coupons[j].Used > 0 {
					s.Coupons[j].Used--
				}
			}
		}
		ci := customerIndex(s, o.CustomerID)
		if ci >= 0 {
			addNotice(s, "cancel:"+id, s.Customers[ci].TelegramID, "سفارش "+id+" لغو/رد شد.")
		}
		return nil
	})
}
func (a *App) fulfillOrder(ctx context.Context, id string) (SalesOrder, error) {
	a.fulfillMu.Lock()
	defer a.fulfillMu.Unlock()
	s := a.salesSnapshot()
	i := orderIndex(&s, id)
	if i < 0 {
		return SalesOrder{}, fmt.Errorf("سفارش یافت نشد")
	}
	o := s.Orders[i]
	if o.Status == "fulfilled" {
		return o, nil
	}
	if o.Status != "paid" && o.Status != "provisioning" && o.Status != "uncertain" && o.Status != "failed" {
		return o, fmt.Errorf("پرداخت سفارش تأیید نشده است")
	}
	if o.Kind == "topup" {
		err := a.salesTxn(func(s *SalesData) error {
			oi := orderIndex(s, id)
			ci := customerIndex(s, o.CustomerID)
			if oi < 0 || ci < 0 {
				return fmt.Errorf("سفارش یا مشتری یافت نشد")
			}
			if s.Orders[oi].Status == "fulfilled" {
				return nil
			}
			if s.Customers[ci].Balance+o.Amount > 1e12 {
				return fmt.Errorf("سقف کیف پول پر شده است")
			}
			s.Customers[ci].Balance += o.Amount
			s.Ledger = append(s.Ledger, WalletEntry{ID: "l-" + randomToken(8), CustomerID: o.CustomerID, Delta: o.Amount, OrderID: o.ID, Reason: "شارژ تأییدشده", Created: time.Now().UnixMilli()})
			s.Orders[oi].Status = "fulfilled"
			s.Orders[oi].Updated = time.Now().UnixMilli()
			addNotice(s, "delivery:"+id, s.Customers[ci].TelegramID, "✅ کیف پول شارژ شد.\nسفارش "+id+"\n"+strconv.FormatInt(o.Amount, 10)+" تومان")
			o = s.Orders[oi]
			return nil
		})
		return o, err
	}
	if err := a.salesTxn(func(s *SalesData) error {
		oi := orderIndex(s, id)
		if (o.Kind == "buy" || o.Kind == "trial") && !s.Orders[oi].Activated {
			if o.Plan.Days > 0 {
				s.Orders[oi].Provision.Expiry = time.Now().Add(time.Duration(o.Plan.Days) * 24 * time.Hour).UnixMilli()
			}
			s.Orders[oi].Activated = true
		}
		s.Orders[oi].Status = "provisioning"
		s.Orders[oi].Updated = time.Now().UnixMilli()
		o = s.Orders[oi]
		return nil
	}); err != nil {
		return o, err
	}
	var service SoldService
	var err error
	if o.Kind == "renew" {
		service, err = a.renewSoldService(ctx, o)
	} else {
		service, err = a.provisionPlan(ctx, o.Plan, o.Provision)
	}
	if err != nil {
		msg := "ساخت/بازیابی سرویس کامل نشد؛ اعتبار سفارش محفوظ است. اتصال پنل مقصد را بررسی و بازیابی را اجرا کن."
		_ = a.salesTxn(func(s *SalesData) error {
			oi := orderIndex(s, id)
			s.Orders[oi].Status = "uncertain"
			s.Orders[oi].Error = msg
			s.Orders[oi].Updated = time.Now().UnixMilli()
			o = s.Orders[oi]
			return nil
		})
		return o, fmt.Errorf("%s (%s)", msg, err.Error())
	}
	service.CustomerID = o.CustomerID
	service.PlanID = o.PlanID
	service.OrderID = o.ID
	if service.ID == "" {
		service.ID = "s-" + o.Provision.OperationID
	}
	service.NodeID = o.Plan.NodeID
	err = a.salesTxn(func(s *SalesData) error {
		oi := orderIndex(s, id)
		ci := customerIndex(s, o.CustomerID)
		if oi < 0 || ci < 0 {
			return fmt.Errorf("سفارش/مشتری یافت نشد")
		}
		si := serviceIndex(s, service.ID)
		if si < 0 {
			s.Services = append(s.Services, service)
		} else {
			s.Services[si] = service
		}
		s.Orders[oi].Status = "fulfilled"
		s.Orders[oi].Error = ""
		s.Orders[oi].ServiceID = service.ID
		s.Orders[oi].Updated = time.Now().UnixMilli()
		queueServiceDelivery(s, s.Orders[oi], service)
		o = s.Orders[oi]
		return nil
	})
	if err == nil {
		a.audit("سفارش فروش "+id+" تحویل شد", "sale")
		_ = a.persist()
	}
	return o, err
}
func (a *App) salesLoop(ctx context.Context) {
	for {
		a.reconcilePayments(ctx)
		a.salesReminders(ctx)
		s := a.salesSnapshot()
		for _, o := range s.Orders {
			if o.Status == "paid" || o.Status == "provisioning" || o.Status == "uncertain" && time.Now().UnixMilli()-o.Updated > 60000 {
				job, cancel := context.WithTimeout(ctx, 30*time.Second)
				_, _ = a.fulfillOrder(job, o.ID)
				cancel()
				if ctx.Err() != nil {
					return
				}
			}
		}
		a.flushSalesNotices(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}
func (a *App) salesAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sales")
	if path == "" && r.Method == "GET" {
		jsonReply(w, 200, a.publicSales())
		return
	}
	if path == "/backup" && r.Method == "GET" {
		s := a.salesSnapshot()
		s.Sessions = nil
		s.Notices = nil
		s.BotFlows = nil
		s.Payments = nil
		s.Accounts = nil
		s.Gateways = GatewaySettings{}
		for i := range s.Nodes {
			s.Nodes[i].Token = ""
			s.Nodes[i].Password = ""
		}
		s.Keys = nil
		w.Header().Set("Content-Disposition", `attachment; filename="ariaatashin-commerce-private.json"`)
		jsonReply(w, 200, s)
		return
	}
	if path == "/gateways" && r.Method == "GET" {
		jsonReply(w, 200, publicGateways(a.salesSnapshot()))
		return
	}
	if path == "/restore" && r.Method == "POST" {
		a.restoreCommerce(w, r)
		return
	}
	if strings.HasPrefix(path, "/receipts/") && r.Method == "GET" {
		a.adminReceipt(w, r, strings.TrimPrefix(path, "/receipts/"))
		return
	}
	if r.Method != "POST" {
		apiError(w, 405, "این مسیر POST لازم دارد")
		return
	}
	var err error
	var result any
	switch path {
	case "/gateways":
		var v GatewaySettings
		if readBody(w, r, &v, 4096) != nil {
			apiError(w, 400, "تنظیمات درگاه معتبر نیست")
			return
		}
		err = a.saveGateways(v)
	case "/settings":
		var v ShopSettings
		if readBody(w, r, &v, 8192) != nil {
			apiError(w, 400, "تنظیمات فروش معتبر نیست")
			return
		}
		if err = validateShopSettings(&v, a); err == nil {
			err = a.salesTxn(func(s *SalesData) error { s.Settings = v; return nil })
		}
	case "/plans":
		var v SalesPlan
		if readBody(w, r, &v, 8192) != nil {
			apiError(w, 400, "پلن معتبر نیست")
			return
		}
		if v.NodeID == "local" {
			v.NodeID = ""
		}
		if err = a.validatePlan(v); err == nil {
			err = a.salesTxn(func(s *SalesData) error {
				if v.ID == "" {
					if len(s.Plans) >= 100 {
						return fmt.Errorf("سقف پلن‌ها پر است")
					}
					v.ID = "p-" + randomToken(6)
					s.Plans = append(s.Plans, v)
				} else {
					i := planIndex(s, v.ID)
					if i < 0 {
						return fmt.Errorf("پلن یافت نشد")
					}
					s.Plans[i] = v
				}
				return nil
			})
		}
	case "/nodes":
		var v PanelNode
		if readBody(w, r, &v, 16384) != nil {
			apiError(w, 400, "فرم پنل مقصد معتبر نیست")
			return
		}
		err = a.savePanelNode(v)
	case "/customers":
		var v SalesCustomer
		if readBody(w, r, &v, 4096) != nil {
			apiError(w, 400, "مشتری معتبر نیست")
			return
		}
		if v.ID == "" {
			result, err = a.createCustomer(v.Name, v.TelegramID)
		} else {
			err = a.salesTxn(func(s *SalesData) error {
				i := customerIndex(s, v.ID)
				if i < 0 {
					return fmt.Errorf("مشتری یافت نشد")
				}
				if !safeLabel(v.Name, 64) {
					return fmt.Errorf("نام معتبر نیست")
				}
				s.Customers[i].Name = v.Name
				s.Customers[i].Blocked = v.Blocked
				return nil
			})
		}
	case "/orders":
		var v BuyRequest
		if readBody(w, r, &v, 4096) != nil {
			apiError(w, 400, "سفارش معتبر نیست")
			return
		}
		result, err = a.newOrder(v)
		if err == nil {
			o := result.(SalesOrder)
			if o.Status == "paid" {
				result, err = a.fulfillOrder(r.Context(), o.ID)
			}
		}
	case "/coupons":
		var v Coupon
		if readBody(w, r, &v, 4096) != nil || !regexp.MustCompile(`^[A-Z0-9_-]{3,32}$`).MatchString(strings.ToUpper(v.Code)) || v.Percent < 1 || v.Percent > 90 || v.Limit < 0 || v.Limit > 100000 || v.Expires < 0 {
			apiError(w, 400, "کد تخفیف معتبر نیست")
			return
		}
		v.Code = strings.ToUpper(v.Code)
		err = a.salesTxn(func(s *SalesData) error {
			for i, c := range s.Coupons {
				if c.Code == v.Code {
					v.Used = c.Used
					s.Coupons[i] = v
					return nil
				}
			}
			if len(s.Coupons) >= 500 {
				return fmt.Errorf("سقف کدها پر است")
			}
			v.Used = 0
			s.Coupons = append(s.Coupons, v)
			return nil
		})
	case "/keys":
		result, err = a.createIntegrationKey(w, r)
		if err == nil {
			jsonReply(w, 200, result)
			return
		}
	default:
		p := strings.Split(strings.Trim(path, "/"), "/")
		if len(p) != 3 {
			apiError(w, 404, "عملیات فروش یافت نشد")
			return
		}
		kind, id, verb := p[0], p[1], p[2]
		switch kind {
		case "orders":
			switch verb {
			case "check-payment":
				snapshot := a.salesSnapshot()
				oi := orderIndex(&snapshot, id)
				if oi < 0 {
					err = fmt.Errorf("سفارش پیدا نشد")
				} else {
					result, err = a.customerPaymentCheck(r.Context(), snapshot.Orders[oi].CustomerID, id)
				}
			case "recover-payment":
				var input struct {
					Authority string `json:"authority"`
				}
				if readBody(w, r, &input, 1024) != nil {
					apiError(w, 400, "شناسه معتبر نیست")
					return
				}
				result, err = a.recoverPayment(r.Context(), id, input.Authority)
			case "approve":
				result, err = a.approveOrder(r.Context(), id)
			case "retry":
				result, err = a.fulfillOrder(r.Context(), id)
			case "reject":
				err = a.cancelOrder("", id, true)
			case "receipt":
				var v struct {
					Receipt string `json:"receipt"`
				}
				if readBody(w, r, &v, 4096) != nil {
					apiError(w, 400, "رسید معتبر نیست")
					return
				}
				s := a.salesSnapshot()
				oi := orderIndex(&s, id)
				if oi < 0 {
					err = fmt.Errorf("سفارش یافت نشد")
				} else {
					err = a.submitReceipt(s.Orders[oi].CustomerID, id, v.Receipt, "", "")
				}
			}
		case "customers":
			if verb == "link" {
				var link string
				link, err = a.portalLink(id)
				result = map[string]string{"url": link}
			}
		case "nodes":
			if verb == "test" {
				result, err = a.testPanelNode(r.Context(), id)
			}
		case "keys":
			if verb == "revoke" {
				err = a.salesTxn(func(s *SalesData) error {
					for i := range s.Keys {
						if s.Keys[i].ID == id {
							s.Keys[i].Revoked = true
							return nil
						}
					}
					return fmt.Errorf("کلید یافت نشد")
				})
			}
		case "tickets":
			if verb == "reply" {
				var v struct {
					Reply string `json:"reply"`
				}
				if readBody(w, r, &v, 8192) != nil || len([]rune(v.Reply)) < 1 || len([]rune(v.Reply)) > 2000 {
					apiError(w, 400, "پاسخ معتبر نیست")
					return
				}
				err = a.salesTxn(func(s *SalesData) error {
					for i := range s.Tickets {
						t := &s.Tickets[i]
						if t.ID == id {
							t.Reply = v.Reply
							t.Status = "answered"
							ci := customerIndex(s, t.CustomerID)
							if ci >= 0 {
								addNotice(s, "ticket:"+id+":"+randomToken(4), s.Customers[ci].TelegramID, "پاسخ پشتیبانی\n"+v.Reply)
							}
							return nil
						}
					}
					return fmt.Errorf("تیکت یافت نشد")
				})
			}
		case "services":
			switch verb {
			case "sync":
				result, err = a.syncSoldService(r.Context(), id)
			case "toggle":
				err = a.toggleSoldService(r.Context(), id)
			}
		}
	}
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	jsonReply(w, 200, map[string]any{"ok": true, "result": result, "sales": a.publicSales()})
}
func (a *App) restoreCommerce(w http.ResponseWriter, r *http.Request) {
	var incoming SalesData
	if readBody(w, r, &incoming, 16<<20) != nil || incoming.Version != 1 || len(incoming.Orders) > 100000 || len(incoming.Customers) > 50000 {
		apiError(w, 400, "بکاپ فروش معتبر نیست")
		return
	}
	current := a.salesSnapshot()
	if len(current.Customers) > 0 || len(current.Orders) > 0 || len(current.Services) > 0 {
		apiError(w, 409, "بازیابی فروش فقط روی فروشگاه خالی انجام می‌شود؛ تاریخچهٔ مالی بازنویسی نمی‌شود")
		return
	}
	// Credentials and access keys are intentionally not imported. Each remote
	// endpoint must be configured and re-authorized in the destination instance.
	incoming.Keys = nil
	incoming.Sessions = nil
	incoming.Notices = nil
	incoming.Accounts = nil
	incoming.BotFlows = nil
	incoming.Payments = nil
	incoming.Gateways = GatewaySettings{}
	ids := map[string]bool{}
	for _, c := range incoming.Customers {
		if !safeLabel(c.ID, 64) || ids[c.ID] || c.Balance < 0 || c.Balance > 1e12 || c.TelegramID < 0 {
			apiError(w, 400, "مشتری نامعتبر در بکاپ")
			return
		}
		ids[c.ID] = true
	}
	for i := range incoming.Nodes {
		n := &incoming.Nodes[i]
		n.Token = ""
		n.Password = ""
		n.Enabled = false
		if err := a.validateNode(*n, false); err != nil {
			apiError(w, 400, "پنل نامعتبر در بکاپ")
			return
		}
	}
	for _, p := range incoming.Plans {
		if validateCreate(CreateRequest{Name: p.Name, Count: 1, QuotaGB: p.QuotaGB, Days: p.Days, Profiles: p.Profiles}) != nil || p.Price < 1 || p.Price > 1e12 {
			apiError(w, 400, "پلن نامعتبر در بکاپ")
			return
		}
	}
	for i := range incoming.Orders {
		o := &incoming.Orders[i]
		if !ids[o.CustomerID] || o.Amount < 1 || o.Amount > 1e12 || !regexp.MustCompile(`^o-[a-f0-9]{16}$`).MatchString(o.ID) {
			apiError(w, 400, "سفارش نامعتبر در بکاپ")
			return
		}
		o.ReceiptFile = ""
		o.TelegramPhoto = ""
		if o.Status == "paid" || o.Status == "provisioning" || o.Status == "uncertain" {
			o.Status = "failed"
			o.Error = "پس از بازیابی، اتصال مقصد را بررسی و سفارش را دستی بازیابی کن."
		}
	}
	if err := a.salesTxn(func(s *SalesData) error { *s = incoming; return nil }); err != nil {
		apiError(w, 500, err.Error())
		return
	}
	jsonReply(w, 200, map[string]any{"ok": true, "sales": a.publicSales()})
}
