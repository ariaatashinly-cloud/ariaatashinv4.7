package main

import (
	"fmt"
	"net/http"
	"regexp"
)

func validateCoreBackup(s ManagedCores) error {
	if len(s.Services) > 200 {
		return fmt.Errorf("سقف سرویس‌های بکاپ بیشتر از ۲۰۰ است")
	}
	ids := map[string]bool{}
	ports := map[int]bool{}
	for _, v := range s.Services {
		if !regexp.MustCompile(`^sb-[a-f0-9]{12}$`).MatchString(v.ID) || ids[v.ID] || ports[v.Port] || !safeLabel(v.Name, 64) || v.Port < 1024 || v.Port > 65535 || v.Port == panelPort || v.Port == managedStatsPort || !uuidPattern.MatchString(v.UUID) || !validClientPassword(v.Password) || v.Quota < 0 || v.Quota > 1000000*(1<<30) || v.Used < 0 || v.Used > 1<<60 || v.Expiry < 0 || v.Path != "/connect/"+v.ID+"/" {
			return fmt.Errorf("رکورد هسته در بکاپ معتبر نیست")
		}
		switch v.Kind {
		case "vless-ws", "trojan-ws", "tuic", "hysteria2", "anytls":
		default:
			return fmt.Errorf("پروتکل بکاپ معتبر نیست")
		}
		if _, e := normalizeEndpoint(v.Endpoint); e != nil {
			return e
		}
		ids[v.ID] = true
		ports[v.Port] = true
	}
	return nil
}
func (a *App) managedBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		s := a.coresSnapshot()
		w.Header().Set("Content-Disposition", `attachment; filename="ariaatashin-managed-cores-backup.json"`)
		jsonReply(w, 200, map[string]any{"brand": "ariaatashin", "version": 1, "cores": s})
		return
	}
	var in struct {
		Brand   string       `json:"brand"`
		Version int          `json:"version"`
		Cores   ManagedCores `json:"cores"`
	}
	if readBody(w, r, &in, 1<<20) != nil || in.Brand != "ariaatashin" || in.Version != 1 {
		apiError(w, 400, "بکاپ هسته معتبر نیست")
		return
	}
	if e := validateCoreBackup(in.Cores); e != nil {
		apiError(w, 400, e.Error())
		return
	}
	a.coreTxnMu.Lock()
	defer a.coreTxnMu.Unlock()
	current := a.coresSnapshot()
	if len(current.Services) > 0 {
		apiError(w, 409, "برای حفظ حساب‌های فعلی، بکاپ هسته روی مقصد خالی بازیابی می‌شود")
		return
	}
	for i := range in.Cores.Services {
		v := &in.Cores.Services[i]
		if v.Port == a.cfg.Port {
			apiError(w, 409, "پورت بکاپ با پورت این برنامه تداخل دارد")
			return
		}
		if v.Kind != "vless-ws" && v.Kind != "trojan-ws" && (a.cfg.Mode != "vps" || a.vpsSnapshot().certificateError() != nil) {
			apiError(w, 409, "بکاپ مستقیم به VPS و گواهی تنظیم‌شده نیاز دارد")
			return
		}
		a.mu.RLock()
		conflict := false
		for _, ib := range a.inbounds {
			if ib.Port == v.Port {
				conflict = true
			}
		}
		a.mu.RUnlock()
		if conflict {
			apiError(w, 409, "پورت بکاپ با اینباند فعلی تداخل دارد")
			return
		}
		v.LastUp = 0
		v.LastDown = 0
	}
	in.Cores.Revision = current.Revision + 1
	if e := a.checkCore(r.Context(), in.Cores); e != nil {
		apiError(w, 409, e.Error())
		return
	}
	if e := a.coreSave(in.Cores); e != nil {
		apiError(w, 500, "بکاپ ذخیره نشد")
		return
	}
	jsonReply(w, 200, a.managedPublic())
}
