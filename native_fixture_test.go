//go:build integration

package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func nativeTestApp(t *testing.T) (*App, context.Context) {
	t.Helper()
	engine := os.Getenv("ARIA_TEST_ENGINE_DIR")
	if engine == "" {
		t.Skip("set ARIA_TEST_ENGINE_DIR to the pinned 3x-ui engine directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	bin, err := prepareRuntimeBin(engine, dir)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	cfg := Config{Port: 8080, DataDir: dir, EngineDir: engine, PublicURL: "https://native-recovery.example", AdminUser: "admin", AdminPassword: "Test-only-password-123", Mode: "cloud", Bind: "127.0.0.1"}
	env := append(os.Environ(), "XUI_DB_FOLDER="+dir, "XUI_LOG_FOLDER="+filepath.Join(dir, "logs"), "XUI_BIN_FOLDER="+bin, "XRAY_LOCATION_ASSET="+filepath.Join(engine, "bin"), "XUI_ENABLE_FAIL2BAN=false")
	setup := exec.CommandContext(ctx, filepath.Join(engine, "x-ui"), "setting", "-username", cfg.AdminUser, "-password", cfg.AdminPassword, "-port", strconv.Itoa(port), "-listenIP", "127.0.0.1", "-webBasePath", "/")
	setup.Env, setup.Dir = env, dir
	if err := setup.Run(); err != nil {
		t.Fatal("native initialization failed", err)
	}
	log, err := os.Create(filepath.Join(dir, "native-private.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cmd := exec.CommandContext(ctx, filepath.Join(engine, "x-ui"))
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = env, dir, log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
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
	a, err := NewApp(cfg, "http://127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		if a.refresh(ctx) == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !a.snapshot().Ready {
		t.Fatal("native core did not become ready")
	}
	return a, ctx
}
