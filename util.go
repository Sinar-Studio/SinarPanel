package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return errors.New("Content-Type harus application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("body JSON tidak valid: %v", err)
	}
	return nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func auditf(format string, args ...any) {
	log.Printf("AUDIT "+format, args...)
}

func (a *App) audit(r *http.Request, action, target string, err error) {
	user := "-"
	if s := sessionFrom(r); s != nil {
		user = s.User.Name
	}
	result := "ok"
	if err != nil {
		result = "error: " + err.Error()
	}
	auditf("user=%q ip=%s action=%s target=%q result=%q", user, clientIP(r), action, target, result)
}

// limitedBuffer menyimpan output perintah maksimal n byte.
type limitedBuffer struct {
	buf bytes.Buffer
	n   int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.n - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
		} else {
			l.buf.Write(p)
		}
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string { return l.buf.String() }

// runCmd menjalankan program langsung (tanpa shell) dan mengembalikan output gabungan.
func runCmd(ctx context.Context, timeout time.Duration, env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = "/"
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out := &limitedBuffer{n: 1 << 20}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	s := strings.TrimSpace(out.String())
	if ctx.Err() == context.DeadlineExceeded {
		return s, fmt.Errorf("%s timeout setelah %s", name, timeout)
	}
	if err != nil {
		return s, fmt.Errorf("%s gagal: %v", name, err)
	}
	return s, nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".sp-tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,63}$`)

func validEmail(s string) bool { return len(s) <= 254 && emailRe.MatchString(s) }

func urlPathEscape(s string) string { return url.PathEscape(s) }

// validDomain: hostname huruf kecil, minimal 2 label, tanpa wildcard.
func validDomain(d string) bool {
	if len(d) < 3 || len(d) > 253 {
		return false
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	tld := labels[len(labels)-1]
	allDigit := true
	for i := 0; i < len(tld); i++ {
		if tld[i] < '0' || tld[i] > '9' {
			allDigit = false
		}
	}
	return !allDigit
}
