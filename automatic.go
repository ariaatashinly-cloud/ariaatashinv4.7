package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Browsers know the external origin even when a TLS proxy rewrites Host.
// The origin is learned only after admin authentication. Anonymous requests,
// health probes and untrusted Forwarded headers never change saved links.
func (a *App) requestOrigin(r *http.Request) (string, error) {
	reported := r.Header.Get("X-ARIA-Origin")
	if reported == "" {
		reported = r.Header.Get("X-ATIA-Origin")
	}
	origin := r.Header.Get("Origin")
	if reported != "" {
		u, err := validatePublicURL(reported)
		if err != nil {
			return "", err
		}
		if origin != "" {
			o, err := validatePublicURL(origin)
			if err != nil || o.String() != u.String() {
				return "", fmt.Errorf("Origin mismatch")
			}
		}
		return u.String(), nil
	}
	if origin != "" {
		u, err := validatePublicURL(origin)
		if err != nil {
			return "", err
		}
		// Compatibility for older clients with a manually configured domain.
		if !strings.EqualFold(u.Host, r.Host) && u.String() != a.cfg.PublicURL && u.String() != a.snapshot().Settings.PublicURL {
			return "", fmt.Errorf("Origin mismatch; use the bundled panel interface")
		}
		return u.String(), nil
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	u, err := validatePublicURL(scheme + "://" + r.Host)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (a *App) learnPublicOrigin(origin string) error {
	a.mutation.Lock()
	defer a.mutation.Unlock()
	a.syncing.Lock()
	defer a.syncing.Unlock()
	old := a.snapshot().Settings
	if !old.AutomaticDomain || old.PublicURL == origin {
		return nil
	}
	a.mu.Lock()
	a.state.Settings.PublicURL = origin
	all, engine := a.inbounds, a.state.Engine
	a.mu.Unlock()
	if err := a.persist(); err != nil {
		a.mu.Lock()
		a.state.Settings = old
		a.mu.Unlock()
		return err
	}
	a.publish(all, engine)
	return nil
}

func defaultDataDir() string {
	if s, err := os.Stat("/data"); err == nil && s.IsDir() {
		// Actually check write permission; root mode bits alone are insufficient.
		if f, err := os.CreateTemp("/data", ".atia-write-probe-*"); err == nil {
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
			// Preserve a v1 Docker database kept directly in /data.
			for _, name := range []string{"x-ui.db", "atia-settings.json"} {
				if s, err := os.Stat(filepath.Join("/data", name)); err == nil && s.Mode().IsRegular() {
					return "/data"
				}
			}
			if _, err := os.Stat("/data/atiaatashin-data/x-ui.db"); err == nil {
				return "/data/atiaatashin-data"
			}
			return "/data/ariaatashin-data"
		}
	}
	if _, err := os.Stat("/tmp/atiaatashin-data/x-ui.db"); err == nil {
		return "/tmp/atiaatashin-data"
	}
	return "/tmp/ariaatashin-data"
}

func hasDataMount(dir string) bool {
	b, err := os.ReadFile("/proc/self/mountinfo")
	return err == nil && dataMountInfo(dir, string(b))
}

func dataMountInfo(dir, info string) bool {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	best, durable := 0, false
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for _, line := range strings.Split(info, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		mount := unescape.Replace(fields[4])
		if !(dir == mount || strings.HasPrefix(dir, strings.TrimRight(mount, "/")+"/")) || len(mount) < best {
			continue
		}
		best, durable = len(mount), mount != "/"
		for i := 6; i+1 < len(fields); i++ {
			if fields[i] == "-" {
				switch fields[i+1] {
				case "tmpfs", "ramfs", "overlay", "proc", "sysfs", "devtmpfs":
					durable = false
				}
				break
			}
		}
	}
	return durable
}

// x-ui writes config.json in XUI_BIN_FOLDER. Keep this directory on the
// writable volume, but point executables and immutable geodata at the image.
// Symlinks resolve to the executable image filesystem, not a noexec volume.
func prepareRuntimeBin(engineDir, dataDir string) (string, error) {
	binDir := filepath.Join(dataDir, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		return "", err
	}
	engineDir, err := filepath.Abs(engineDir)
	if err != nil {
		return "", err
	}
	for _, name := range []string{"xray-linux-amd64", "geoip.dat", "geosite.dat", "tuic-server"} {
		target := filepath.Join(engineDir, "bin", name)
		if s, err := os.Stat(target); err != nil || !s.Mode().IsRegular() {
			if name == "tuic-server" {
				continue
			}
			return "", fmt.Errorf("packaged engine asset missing: %s", name)
		}
		link := filepath.Join(binDir, name)
		if old, err := os.Readlink(link); err == nil && old == target {
			continue
		}
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err := os.Symlink(target, link); err != nil {
			return "", err
		}
	}
	return binDir, nil
}
