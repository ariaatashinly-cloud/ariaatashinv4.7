package main

import (
	"os"
	"strings"
	"syscall"
	"time"
)

type Metrics struct {
	CPU         float64 "json:\"cpu\""
	MemoryUsed  uint64  "json:\"memoryUsed\""
	MemoryTotal uint64  "json:\"memoryTotal\""
	DiskUsed    uint64  "json:\"diskUsed\""
	DiskTotal   uint64  "json:\"diskTotal\""
	UpBPS       float64 "json:\"upBPS\""
	DownBPS     float64 "json:\"downBPS\""
	Uptime      float64 "json:\"uptime\""
	Timestamp   int64   "json:\"timestamp\""
	Available   bool    "json:\"available\""
}
type procSample struct {
	total, idle, rx, tx float64
	t                   time.Time
}

func (a *App) sampleMetrics() Metrics {
	a.metricsMu.Lock()
	defer a.metricsMu.Unlock()
	now := time.Now()
	if now.Sub(a.metricPrev.t) < time.Second {
		return a.metrics
	}
	m := Metrics{Timestamp: now.UnixMilli()}
	cur := procSample{t: now}
	if b, e := os.ReadFile("/proc/stat"); e == nil {
		f := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])
		for i, v := range f[1:] {
			n := parseNumber(v)
			cur.total += n
			if i == 3 || i == 4 {
				cur.idle += n
			}
		}
		m.Available = true
	}
	if b, e := os.ReadFile("/proc/meminfo"); e == nil {
		vals := map[string]uint64{}
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) > 1 {
				vals[strings.TrimSuffix(f[0], ":")] = uint64(parseNumber(f[1])) * 1024
			}
		}
		m.MemoryTotal = vals["MemTotal"]
		m.MemoryUsed = m.MemoryTotal - vals["MemAvailable"]
	}
	if b, e := os.ReadFile("/proc/uptime"); e == nil {
		f := strings.Fields(string(b))
		if len(f) > 0 {
			m.Uptime = parseNumber(f[0])
		}
	}
	if b, e := os.ReadFile("/proc/net/dev"); e == nil {
		for _, l := range strings.Split(string(b), "\n") {
			p := strings.SplitN(l, ":", 2)
			if len(p) != 2 || strings.TrimSpace(p[0]) == "lo" {
				continue
			}
			f := strings.Fields(p[1])
			if len(f) >= 9 {
				cur.rx += parseNumber(f[0])
				cur.tx += parseNumber(f[8])
			}
		}
	}
	var disk syscall.Statfs_t
	if syscall.Statfs(a.cfg.DataDir, &disk) == nil {
		m.DiskTotal = disk.Blocks * uint64(disk.Bsize)
		m.DiskUsed = (disk.Blocks - disk.Bavail) * uint64(disk.Bsize)
	}
	prev := a.metricPrev
	dt := now.Sub(prev.t).Seconds()
	if !prev.t.IsZero() && dt > 0 {
		if d := cur.total - prev.total; d > 0 {
			m.CPU = 100 * (1 - (cur.idle-prev.idle)/d)
			if m.CPU < 0 {
				m.CPU = 0
			}
			if m.CPU > 100 {
				m.CPU = 100
			}
		}
		if cur.tx >= prev.tx {
			m.UpBPS = (cur.tx - prev.tx) / dt
		}
		if cur.rx >= prev.rx {
			m.DownBPS = (cur.rx - prev.rx) / dt
		}
	}
	a.metricPrev = cur
	a.metrics = m
	return m
}
