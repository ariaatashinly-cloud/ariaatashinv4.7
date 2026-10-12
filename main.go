package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	engineVersion = "3.8.5"
	releaseURL    = "https://github.com/MHSanaei/3x-ui/releases/download/v3.8.5/x-ui-linux-amd64.tar.gz"
	releaseSHA256 = "6a85c110a04a727613c933c54ae602b8d37dab8876c6e20a6d46623010dd9d3c"
	panelPort     = 20530
)

type Config struct {
	Port          int
	DataDir       string
	EngineDir     string
	PublicURL     string
	AdminUser     string
	AdminPassword string
	Persistent    bool
	Mode          string
	Bind          string
}

func envDefault(key, fallback string) string {
	if strings.HasPrefix(key, "ATIA_") {
		if v := os.Getenv("ARIA_" + strings.TrimPrefix(key, "ATIA_")); v != "" {
			return v
		}
	}
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func loadConfig() (Config, error) {
	c := Config{DataDir: envDefault("ATIA_DATA_DIR", defaultDataDir()), EngineDir: envDefault("ATIA_ENGINE_DIR", "/tmp/ariaatashin-engine"), AdminUser: envDefault("ATIA_ADMIN_USER", "admin"), AdminPassword: envDefault("ATIA_ADMIN_PASSWORD", "admin"), PublicURL: strings.TrimSpace(envDefault("ATIA_PUBLIC_URL", ""))}
	c.Mode = envDefault("ATIA_MODE", "cloud")
	c.Bind = envDefault("ATIA_BIND", "")
	if c.Mode != "cloud" && c.Mode != "vps" {
		return c, fmt.Errorf("ARIA_MODE must be cloud or vps")
	}
	if c.Bind != "" && net.ParseIP(c.Bind) == nil {
		return c, fmt.Errorf("ARIA_BIND must be an IP address")
	}
	c.Persistent = hasDataMount(c.DataDir)
	if value := envDefault("ATIA_PERSISTENT_STORAGE", ""); value != "" {
		if value != "true" && value != "false" {
			return c, fmt.Errorf("ATIA_PERSISTENT_STORAGE must be true or false")
		}
		c.Persistent = value == "true"
	}
	var err error
	c.Port, err = strconv.Atoi(envDefault("PORT", "8080"))
	if err != nil || c.Port < 1 || c.Port > 65535 || c.Port == panelPort {
		return c, fmt.Errorf("PORT must be a valid port other than %d", panelPort)
	}
	if len(c.AdminPassword) > 256 {
		return c, fmt.Errorf("administrator password is too long")
	}
	if c.PublicURL != "" {
		if _, err := validatePublicURL(c.PublicURL); err != nil {
			return c, fmt.Errorf("ATIA_PUBLIC_URL is optional; remove it for automatic detection, or use a valid http(s) origin: %w", err)
		}
	}
	if len(c.AdminUser) > 64 || strings.TrimSpace(c.AdminUser) == "" {
		return c, fmt.Errorf("invalid ATIA_ADMIN_USER")
	}
	if c.DataDir, err = filepath.Abs(c.DataDir); err != nil {
		return c, err
	}
	if c.EngineDir, err = filepath.Abs(c.EngineDir); err != nil {
		return c, err
	}
	return c, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		log.Fatal(err)
	}
	if err := ensureEngine(ctx, cfg.EngineDir); err != nil {
		log.Fatalf("engine setup failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "logs"), 0700); err != nil {
		log.Fatal(err)
	}
	binDir, err := prepareRuntimeBin(cfg.EngineDir, cfg.DataDir)
	if err != nil {
		log.Fatalf("runtime setup failed: %v", err)
	}
	engineEnv := append(os.Environ(), "XUI_DB_FOLDER="+cfg.DataDir, "XUI_LOG_FOLDER="+filepath.Join(cfg.DataDir, "logs"), "XUI_BIN_FOLDER="+binDir, "XRAY_LOCATION_ASSET="+filepath.Join(cfg.EngineDir, "bin"), "XUI_ENABLE_FAIL2BAN=false")
	// Some engine CLI versions print credentials: never log its setup output.
	setup := exec.CommandContext(ctx, filepath.Join(cfg.EngineDir, "x-ui"), "setting", "-username", cfg.AdminUser, "-password", cfg.AdminPassword, "-port", strconv.Itoa(panelPort), "-listenIP", "127.0.0.1", "-webBasePath", "/")
	setup.Env, setup.Dir = engineEnv, cfg.DataDir
	if err := setup.Run(); err != nil {
		log.Fatalf("engine credential initialization failed: %v", err)
	}
	if err := isolateNativeEntrypoints(cfg); err != nil {
		log.Fatal(err)
	}
	app, err := NewApp(cfg, "http://127.0.0.1:"+strconv.Itoa(panelPort))
	if err != nil {
		log.Fatal(err)
	}
	coreStopped := make(chan struct{})
	go func() { defer close(coreStopped); app.superviseCore(ctx, engineEnv) }()
	go app.syncLoop(ctx)
	go app.salesLoop(ctx)
	go app.telegramLoop(ctx)
	go app.managedCoreLoop(ctx)
	go app.locationLoop(ctx)
	go app.edgeMonitorLoop(ctx)
	go app.dnsLoop(ctx)
	go app.dnsResolverMonitor(ctx)
	go app.watchCertificate(ctx)
	server := &http.Server{Addr: cfg.Bind + ":" + strconv.Itoa(cfg.Port), Handler: app, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server failed: %v", err)
			stop()
		}
	}()
	log.Printf("ariaatashin v4.7.0 listening on :%d; data=%s; separate data mount=%t; keep replicas at 1", cfg.Port, cfg.DataDir, cfg.Persistent)
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	app.flushDNS()
	select {
	case <-coreStopped:
	case <-shutdown.Done():
	}
}

func ensureEngine(ctx context.Context, dir string) error {
	if info, err := os.Stat(filepath.Join(dir, "x-ui")); err == nil && !info.IsDir() {
		if core, err := os.Stat(filepath.Join(dir, "bin", "xray-linux-amd64")); err == nil && !core.IsDir() {
			return nil
		}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	client := &http.Client{Timeout: 150 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("engine download HTTP %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dir), "atia-release-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, 140<<20))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if n >= 140<<20 || hex.EncodeToString(hash.Sum(nil)) != releaseSHA256 {
		return fmt.Errorf("engine checksum mismatch; download refused")
	}
	f, err := os.Open(tmp.Name())
	if err != nil {
		return err
	}
	defer f.Close()
	return extractEngine(f, dir)
}

func extractEngine(src io.Reader, dir string) error {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var size int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(h.Name, "./")
		if name == "x-ui" || name == "x-ui/" {
			continue
		}
		if !strings.HasPrefix(name, "x-ui/") {
			return fmt.Errorf("unexpected engine archive entry")
		}
		name = strings.TrimPrefix(name, "x-ui/")
		clean := filepath.Clean(name)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("unsafe archive path")
		}
		path := filepath.Join(dir, clean)
		size += h.Size
		if h.Size < 0 || size > 650<<20 {
			return fmt.Errorf("engine archive is too large")
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			mode := os.FileMode(0644)
			if h.Mode&0111 != 0 {
				mode = 0755
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("links and special files are refused in engine archives")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "x-ui")); err != nil {
		return fmt.Errorf("engine binary missing")
	}
	return nil
}
