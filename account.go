package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Browser credentials are independent of the private native-core login.
// Changing the browser password must not interrupt background provisioning.
type AdminAccount struct {
	Username string `json:"username"`
	Salt     string `json:"salt"`
	Hash     string `json:"hash"`
	Rounds   int    `json:"rounds"`
}

func passwordKey(password, salt string, rounds int) string {
	h := hmac.New(sha256.New, []byte(password))
	h.Write([]byte(salt))
	h.Write([]byte{0, 0, 0, 1})
	u := h.Sum(nil)
	out := append([]byte{}, u...)
	for i := 1; i < rounds; i++ {
		h.Reset()
		h.Write(u)
		u = h.Sum(nil)
		for j := range out {
			out[j] ^= u[j]
		}
	}
	return hex.EncodeToString(out)
}
func (a *App) loadAccount() error {
	b, e := os.ReadFile(filepath.Join(a.cfg.DataDir, "aria-admin.json"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	var v AdminAccount
	if json.Unmarshal(b, &v) != nil || !safeLabel(v.Username, 64) || len(v.Salt) != 48 || len(v.Hash) != 64 || v.Rounds < 100000 || v.Rounds > 500000 {
		return fmt.Errorf("administrator account file invalid")
	}
	a.account = v
	return nil
}
func (a *App) accountUsername() string {
	a.accountMu.RLock()
	defer a.accountMu.RUnlock()
	if a.account.Username != "" {
		return a.account.Username
	}
	return a.cfg.AdminUser
}
func (a *App) validAccount(username, password string) bool {
	if len(password) < 1 || len(password) > 256 {
		return false
	}
	a.accountMu.RLock()
	v := a.account
	a.accountMu.RUnlock()
	if v.Username == "" {
		return subtle.ConstantTimeCompare([]byte(username), []byte(a.cfg.AdminUser)) == 1 && subtle.ConstantTimeCompare([]byte(password), []byte(a.cfg.AdminPassword)) == 1
	}
	if username != v.Username {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(v.Hash), []byte(passwordKey(password, v.Salt, v.Rounds))) == 1
}
func (a *App) changeAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username        string `json:"username"`
		Password        string `json:"password"`
		CurrentPassword string `json:"currentPassword"`
	}
	if readBody(w, r, &in, 2048) != nil {
		apiError(w, 400, "فرم حساب معتبر نیست")
		return
	}
	a.mutation.Lock()
	defer a.mutation.Unlock()
	in.Username = strings.TrimSpace(in.Username)
	if !safeLabel(in.Username, 64) || len(in.Password) < 1 || len(in.Password) > 256 {
		apiError(w, 400, "نام کاربری ۱ تا ۶۴ نویسه و رمز ۱ تا ۲۵۶ نویسه لازم است")
		return
	}
	if !a.validAccount(a.accountUsername(), in.CurrentPassword) {
		apiError(w, 403, "رمز فعلی درست نیست")
		return
	}
	v := AdminAccount{Username: in.Username, Salt: randomToken(24), Rounds: 120000}
	v.Hash = passwordKey(in.Password, v.Salt, v.Rounds)
	if atomicJSON(a.cfg.DataDir, "aria-admin.json", v) != nil {
		apiError(w, 500, "اطلاعات ورود ذخیره نشد")
		return
	}
	a.accountMu.Lock()
	a.account = v
	a.accountMu.Unlock()
	a.mu.Lock()
	a.sessions = map[string]session{}
	a.mu.Unlock()
	a.audit("اطلاعات ورود مدیر تغییر کرد؛ نشست‌های قبلی بسته شدند", "settings")
	_ = a.persist()
	jsonReply(w, 200, map[string]bool{"ok": true, "loginRequired": true})
}
