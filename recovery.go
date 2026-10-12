package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type CoreStatus struct {
	State       string `json:"state"`
	LastSuccess int64  `json:"lastSuccess"`
	LastError   string `json:"lastError"`
	Recoveries  uint64 `json:"recoveries"`
}

func (a *App) coreStatus() CoreStatus {
	a.mu.RLock()
	e := a.service
	s := a.state
	note := a.coreNote
	a.mu.RUnlock()
	n := uint64(0)
	if e != nil {
		e.authMu.RLock()
		n = e.recoveries
		e.authMu.RUnlock()
	}
	status := "recovering"
	if s.Ready {
		status = "ready"
		note = ""
	}
	return CoreStatus{status, s.SyncedAt, note, n}
}
func (a *App) coreFailed() {
	a.mu.Lock()
	a.state.Ready = false
	a.coreNote = "ارتباط داخلی هسته در حال بازیابی است؛ داده‌ها حذف نشده‌اند."
	a.mu.Unlock()
}
func (a *App) reconnect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := a.refresh(ctx); err != nil {
		w.Header().Set("Retry-After", "5")
		apiError(w, 503, "هسته پاسخ نمی‌دهد؛ بازیابی خودکار ادامه دارد. بخش عیب‌یابی و لاگ میزبان را بررسی کن.")
		return
	}
	jsonReply(w, 200, a.runtimeSnapshot())
}

// Keep the HTTP wrapper and browser sessions alive if the native management
// process exits. Backoff prevents a failed binary from entering a tight loop.
func (a *App) superviseCore(ctx context.Context, env []string) {
	backoff := time.Second
	for ctx.Err() == nil {
		cmd := exec.Command(filepath.Join(a.cfg.EngineDir, "x-ui"))
		cmd.Env = env
		cmd.Dir = a.cfg.DataDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		started := time.Now()
		if err := cmd.Start(); err == nil {
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-ctx.Done():
				_ = cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					_ = cmd.Process.Kill()
					<-done
				}
				return
			case <-a.coreRestart:
				log.Print("TLS certificate renewed; reloading the native core")
				_ = cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					_ = cmd.Process.Kill()
					<-done
				}
			case <-done:
				log.Print("Native core exited; restarting with backoff")
			}
		} else {
			log.Print("Native core could not start; retrying with backoff")
		}
		a.coreFailed()
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}
