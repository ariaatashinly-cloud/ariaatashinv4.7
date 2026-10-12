package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func isolateNativeEntrypoints(c Config) error {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		if c.Mode == "vps" {
			return fmt.Errorf("sqlite3 is required to isolate native subscription listeners; use the supplied Docker image")
		}
		return nil
	}
	sql := "BEGIN; DELETE FROM settings WHERE key IN ('subEnable','subJsonEnable','subClashEnable','subListen'); INSERT INTO settings(key,value) VALUES ('subEnable','false'),('subJsonEnable','false'),('subClashEnable','false'),('subListen','127.0.0.1'); COMMIT;"
	cmd := exec.Command(bin, filepath.Join(c.DataDir, "x-ui.db"), sql)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("could not isolate native subscription listeners")
	}
	return nil
}
func udpSocketBound(port int) bool {
	want := fmt.Sprintf("%04X", port)
	for _, file := range []string{"/proc/net/udp", "/proc/net/udp6"} {
		b, e := os.ReadFile(file)
		if e != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 3 {
				continue
			}
			parts := strings.Split(f[1], ":")
			if len(parts) == 2 && strings.EqualFold(parts[1], want) {
				return true
			}
		}
	}
	return false
}
func parseNumber(s string) float64                        { v, _ := strconv.ParseFloat(s, 64); return v }
func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
