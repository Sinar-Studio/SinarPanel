package main

import (
	"bufio"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func readFirstLine(p string) string {
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
}

func osPrettyName() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return runtime.GOOS
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return runtime.GOOS
}

func memInfo() map[string]uint64 {
	out := map[string]uint64{}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 {
			if v, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				out[strings.TrimSuffix(fields[0], ":")] = v * 1024
			}
		}
	}
	return out
}

func diskUsage(p string) map[string]uint64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err != nil {
		return nil
	}
	bs := uint64(st.Bsize)
	return map[string]uint64{"total": uint64(st.Blocks) * bs, "free": uint64(st.Bavail) * bs}
}

func serviceActive(name string) string {
	out, _ := exec.Command("systemctl", "is-active", name).Output()
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "unknown"
	}
	return s
}

func cmdVersion(name string, args ...string) string {
	if _, err := exec.LookPath(name); err != nil {
		return ""
	}
	out, _ := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

func (a *App) handleSystem(w http.ResponseWriter, r *http.Request) {
	var uptime float64
	if f := strings.Fields(readFirstLine("/proc/uptime")); len(f) > 0 {
		uptime, _ = strconv.ParseFloat(f[0], 64)
	}
	load := strings.Fields(readFirstLine("/proc/loadavg"))
	if len(load) > 3 {
		load = load[:3]
	}
	mem := memInfo()
	certbot := cmdVersion("certbot", "--version")
	apacheVer := cmdVersion("apache2", "-v")
	apacheVer = strings.TrimPrefix(apacheVer, "Server version: ")

	writeJSON(w, http.StatusOK, map[string]any{
		"hostname":     a.hostname,
		"os":           osPrettyName(),
		"kernel":       readFirstLine("/proc/sys/kernel/osrelease"),
		"arch":         runtime.GOARCH,
		"cpus":         runtime.NumCPU(),
		"uptime":       int64(uptime),
		"load":         load,
		"mem_total":    mem["MemTotal"],
		"mem_avail":    mem["MemAvailable"],
		"swap_total":   mem["SwapTotal"],
		"swap_free":    mem["SwapFree"],
		"disk":         diskUsage("/"),
		"apache":       serviceActive(a.cfg.ApacheService),
		"apache_ver":   apacheVer,
		"certbot":      certbot,
		"server_ips":   localIPs(),
		"time":         time.Now().Format(time.RFC3339),
		"panel_ver":    version,
		"session_user": sessionFrom(r).User.Name,
	})
}
