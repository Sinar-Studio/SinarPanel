package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Update panel dari git lewat sinarpanel-update (dipasang oleh install.sh).
// Update dijalankan sebagai unit transient systemd supaya tidak ikut mati
// ketika install.sh me-restart service sinarpanel.
const (
	updaterPath   = "/usr/local/sbin/sinarpanel-update"
	updateSrc     = "/opt/sinarpanel/src"
	updateUnit    = "sinarpanel-update"
	updateLogPath = "/var/log/sinarpanel-update.log"
	installedFile = "/etc/sinarpanel/installed-commit"
)

var (
	commitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
	ansiRe   = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
)

type commitInfo struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}

func installedCommit() string {
	c := readFirstLine(installedFile)
	if !commitRe.MatchString(c) {
		return ""
	}
	return c
}

func updaterInstalled() bool {
	fi, err := os.Stat(updaterPath)
	return err == nil && fi.Mode().IsRegular()
}

// updateState: "active"/"activating" (sedang jalan), "failed", atau "inactive".
func updateState() string {
	out, _ := runCmd(context.Background(), 10*time.Second, nil, "systemctl", "is-active", updateUnit+".service")
	if f := strings.Fields(out); len(f) > 0 {
		return f[0]
	}
	return "inactive"
}

func updateRunning(state string) bool {
	return state == "active" || state == "activating" || state == "reloading"
}

// readUpdateLog mengembalikan maksimal 64 KB terakhir log update, tanpa kode warna.
func readUpdateLog() (string, time.Time) {
	f, err := os.Open(updateLogPath)
	if err != nil {
		return "", time.Time{}
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", time.Time{}
	}
	const max = 64 << 10
	if fi.Size() > max {
		_, _ = f.Seek(-max, io.SeekEnd)
	}
	data, _ := io.ReadAll(io.LimitReader(f, max))
	return ansiRe.ReplaceAllString(string(data), ""), fi.ModTime()
}

func gitOut(args ...string) (string, error) {
	return runCmd(context.Background(), 30*time.Second, []string{"GIT_TERMINAL_PROMPT=0"}, "git", append([]string{"-C", updateSrc}, args...)...)
}

// GET /api/update — status murah (tanpa akses jaringan), aman dipanggil berulang.
func (a *App) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	state := updateState()
	log, mtime := readUpdateLog()
	resp := map[string]any{
		"updater":   updaterInstalled(),
		"installed": installedCommit(),
		"state":     state,
		"running":   updateRunning(state),
		"log":       log,
	}
	if !mtime.IsZero() {
		resp["log_time"] = mtime.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}

// POST /api/update/check — fetch dari git lalu bandingkan dengan commit terpasang.
func (a *App) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !updaterInstalled() {
		writeErr(w, http.StatusNotFound, "sinarpanel-update belum terpasang — jalankan install.sh versi terbaru sekali dari SSH")
		return
	}
	if updateRunning(updateState()) {
		writeErr(w, http.StatusConflict, "Update sedang berjalan")
		return
	}
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	out, err := runCmd(r.Context(), 3*time.Minute, []string{"GIT_TERMINAL_PROMPT=0"}, updaterPath, "--check", "-y")
	out = ansiRe.ReplaceAllString(out, "")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "Gagal mengecek update", "log": out})
		return
	}
	branch, err := gitOut("symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || branch == "" {
		branch = "main"
	}
	repo, _ := gitOut("remote", "get-url", "origin")
	latest, err := gitOut("rev-parse", "refs/remotes/origin/"+branch)
	if err != nil || !commitRe.MatchString(latest) {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "Tidak bisa membaca commit terbaru", "log": out})
		return
	}
	installed := installedCommit()
	commits := []commitInfo{}
	if installed != latest {
		rng := []string{"-n", "1", latest}
		if installed != "" {
			if _, err := gitOut("cat-file", "-e", installed+"^{commit}"); err == nil {
				rng = []string{"-n", "50", installed + ".." + latest}
			}
		}
		lg, _ := gitOut(append([]string{"--no-pager", "log", "--no-decorate", "--format=%H%x1f%s%x1f%an%x1f%cI"}, rng...)...)
		for _, line := range strings.Split(lg, "\n") {
			if f := strings.Split(line, "\x1f"); len(f) == 4 {
				commits = append(commits, commitInfo{Hash: f[0], Subject: f[1], Author: f[2], Date: f[3]})
			}
		}
	}
	a.audit(r, "update-check", latest, nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"installed": installed,
		"latest":    latest,
		"branch":    branch,
		"repo":      repo,
		"available": installed != latest,
		"commits":   commits,
	})
}

// POST /api/update/run — jalankan sinarpanel-update di luar cgroup panel.
func (a *App) handleUpdateRun(w http.ResponseWriter, r *http.Request) {
	if !updaterInstalled() {
		writeErr(w, http.StatusNotFound, "sinarpanel-update belum terpasang")
		return
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		writeErr(w, http.StatusInternalServerError, "systemd-run tidak ditemukan")
		return
	}
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	if updateRunning(updateState()) {
		writeErr(w, http.StatusConflict, "Update sedang berjalan")
		return
	}
	ctx := r.Context()
	// Unit gagal sebelumnya harus di-reset agar nama unit bisa dipakai lagi.
	_, _ = runCmd(ctx, 10*time.Second, nil, "systemctl", "reset-failed", updateUnit+".service")
	if err := os.WriteFile(updateLogPath, nil, 0o600); err != nil {
		writeErr(w, http.StatusInternalServerError, "Tidak bisa membuat log: "+err.Error())
		return
	}
	out, err := runCmd(ctx, 30*time.Second, nil, "systemd-run",
		"--unit="+updateUnit, "--description=SinarPanel update",
		"--property=StandardOutput=file:"+updateLogPath,
		"--property=StandardError=inherit",
		"--setenv=GIT_TERMINAL_PROMPT=0",
		updaterPath, "-y")
	a.audit(r, "update-run", "", err)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Gagal memulai update: "+strings.TrimSpace(err.Error()+" "+out))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
