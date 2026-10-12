package main

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func normalizeDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '۰' && r <= '۹' {
			return '0' + r - '۰'
		}
		if r >= '٠' && r <= '٩' {
			return '0' + r - '٠'
		}
		return r
	}, s)
}
func bankDigits(s string) string {
	return strings.NewReplacer(" ", "", "-", "", "\u200c", "").Replace(normalizeDigits(strings.TrimSpace(s)))
}
func validIBAN(s string) bool {
	if !regexp.MustCompile(`^IR[0-9]{24}$`).MatchString(s) {
		return false
	}
	v := s[4:] + "1827" + s[2:4]
	mod := 0
	for _, r := range v {
		mod = (mod*10 + int(r-'0')) % 97
	}
	return mod == 1
}
func validateShopSettings(v *ShopSettings, a *App) error {
	v.CardNumber = bankDigits(v.CardNumber)
	v.AccountNumber = bankDigits(v.AccountNumber)
	v.IBAN = strings.ToUpper(bankDigits(v.IBAN))
	v.Name = strings.TrimSpace(v.Name)
	if !safeLabel(v.Name, 64) || len([]rune(v.PaymentNote)) > 2000 || len([]rune(v.Welcome)) > 1200 || len([]rune(v.Support)) > 120 || len([]rune(v.CardHolder)) > 120 || len([]rune(v.BankName)) > 64 {
		return fmt.Errorf("متن تنظیمات فروش بیش از حد بلند یا نام نامعتبر است")
	}
	if v.CardNumber != "" && !regexp.MustCompile(`^[0-9]{16}$`).MatchString(v.CardNumber) {
		return fmt.Errorf("شماره کارت باید ۱۶ رقم باشد")
	}
	if v.AccountNumber != "" && !regexp.MustCompile(`^[0-9]{5,30}$`).MatchString(v.AccountNumber) {
		return fmt.Errorf("شماره حساب باید ۵ تا ۳۰ رقم باشد")
	}
	if v.IBAN != "" && !validIBAN(v.IBAN) {
		return fmt.Errorf("شماره شبا با IR و ۲۴ رقم معتبر لازم است")
	}
	if v.TrialEnabled {
		if math.IsNaN(v.TrialQuotaGB) || math.IsInf(v.TrialQuotaGB, 0) || v.TrialQuotaGB <= 0 || v.TrialQuotaGB > 5 || v.TrialDays < 1 || v.TrialDays > 7 {
			return fmt.Errorf("تست: حجم بیشتر از صفر تا ۵ گیگ و اعتبار ۱ تا ۷ روز لازم است")
		}
		if err := a.validatePlan(SalesPlan{Name: "تست اتصال", Price: 1, QuotaGB: v.TrialQuotaGB, Days: v.TrialDays, Profiles: v.TrialProfiles, Enabled: true}); err != nil {
			return err
		}
	}
	return nil
}

// A persisted, unique trial order is reused across clicks, retries and restarts.
func (a *App) newTrial(ctx context.Context, customer string) (SalesOrder, error) {
	var id string
	err := a.salesTxn(func(s *SalesData) error {
		ci := customerIndex(s, customer)
		if ci < 0 || s.Customers[ci].Blocked || !s.Settings.Enabled || !s.Settings.TrialEnabled {
			return fmt.Errorf("سرویس تست فعال نیست یا مشتری غیرفعال است")
		}
		if old := s.Customers[ci].TrialOrder; old != "" {
			id = old
			return nil
		}
		if s.Customers[ci].TelegramID <= 0 {
			return fmt.Errorf("سرویس تست فقط برای حساب تلگرام فعال است")
		}
		p := SalesPlan{Name: "تست اتصال", QuotaGB: s.Settings.TrialQuotaGB, Days: s.Settings.TrialDays, Profiles: s.Settings.TrialProfiles}
		if p.QuotaGB <= 0 || p.QuotaGB > 5 || p.Days < 1 || p.Days > 7 || len(p.Profiles) == 0 {
			return fmt.Errorf("تنظیمات تست کامل نیست")
		}
		now := time.Now().UnixMilli()
		id = "o-" + randomToken(8)
		s.Orders = append(s.Orders, SalesOrder{ID: id, CustomerID: customer, Kind: "trial", Plan: p, Method: "trial", Status: "paid", Currency: "IRT", Provision: newProvision(p.Name, p.QuotaGB, p.Days, p.Profiles), Idempotency: "trial:" + customer, Created: now, Updated: now})
		s.Customers[ci].TrialOrder = id
		return nil
	})
	if err != nil {
		return SalesOrder{}, err
	}
	return a.fulfillOrder(ctx, id)
}
func serviceReminder(v SoldService, now int64) (string, string) {
	if v.Expiry > 0 {
		left := v.Expiry - now
		key := "expiry:" + strconv.FormatInt(v.Expiry, 10) + ":"
		if left <= 0 {
			return key + "expired", "اعتبار سرویس تمام شده است."
		}
		if left <= 24*60*60*1000 {
			return key + "24h", "کمتر از ۲۴ ساعت تا پایان اعتبار سرویس مانده است."
		}
		if left <= 3*24*60*60*1000 {
			return key + "3d", "کمتر از ۳ روز تا پایان اعتبار سرویس مانده است."
		}
	}
	if v.Quota > 0 {
		percent := float64(v.Used) / float64(v.Quota) * 100
		key := fmt.Sprintf("volume:%d:%d:", v.Quota, v.Expiry)
		if percent >= 95 {
			return key + "95", "بیش از ۹۵٪ حجم سرویس مصرف شده است."
		}
		if percent >= 80 {
			return key + "80", "بیش از ۸۰٪ حجم سرویس مصرف شده است."
		}
	}
	return "", ""
}
func (a *App) salesReminders(ctx context.Context) {
	s := a.salesSnapshot()
	if !s.Settings.Enabled || !s.Settings.ReminderEnabled {
		return
	}
	now := time.Now().UnixMilli()
	count := 0
	for _, v := range s.Services {
		if now-v.ReminderChecked < 5*60*1000 {
			continue
		}
		ci := customerIndex(&s, v.CustomerID)
		if ci < 0 || s.Customers[ci].Blocked || s.Customers[ci].TelegramID <= 0 {
			continue
		}
		count++
		_ = a.salesTxn(func(s *SalesData) error {
			if i := serviceIndex(s, v.ID); i >= 0 {
				s.Services[i].ReminderChecked = now
			}
			return nil
		})
		job, cancel := context.WithTimeout(ctx, 10*time.Second)
		live, err := a.syncSoldService(job, v.ID)
		cancel()
		if err == nil {
			key, text := serviceReminder(live, now)
			if key != "" {
				_ = a.salesTxn(func(s *SalesData) error {
					si := serviceIndex(s, v.ID)
					if si < 0 {
						return nil
					}
					for _, k := range s.Services[si].Reminders {
						if k == key {
							return nil
						}
					}
					s.Services[si].Reminders = append(s.Services[si].Reminders, key)
					if len(s.Services[si].Reminders) > 16 {
						s.Services[si].Reminders = s.Services[si].Reminders[len(s.Services[si].Reminders)-16:]
					}
					addRichNotice(s, "reminder:"+v.ID+":"+key, s.Customers[customerIndex(s, v.CustomerID)].TelegramID, "⏳ "+v.Name+"\n"+text, [][]BotButton{{botButton("تمدید سرویس", "renew:"+v.ID), botButton("سرویس من", "service:"+v.ID)}})
					return nil
				})
			}
		}
		if count >= 2 || ctx.Err() != nil {
			return
		}
	}
}
