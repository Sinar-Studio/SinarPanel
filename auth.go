package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "__Host-sinarpanel"

var usernameRe = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}$`)

type User struct {
	Name   string
	UID    uint32
	GID    uint32
	Groups []uint32
	Home   string
	Shell  string
}

type Session struct {
	mu            sync.Mutex
	CSRF          string
	User          User
	IP            string
	Created       time.Time
	LastSeen      time.Time
	Needs2FASetup bool
	pendingTOTP   string
}

type SessionStore struct {
	mu   sync.Mutex
	m    map[[32]byte]*Session
	idle time.Duration
	max  time.Duration
}

const maxSessions = 200

func newSessionStore(idle, max time.Duration) *SessionStore {
	s := &SessionStore{m: map[[32]byte]*Session{}, idle: idle, max: max}
	go func() {
		for range time.Tick(time.Minute) {
			s.gc()
		}
	}()
	return s
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Token disimpan dalam bentuk hash supaya isi memori tidak langsung bisa dipakai.
func tokenKey(tok string) [32]byte { return sha256.Sum256([]byte(tok)) }

func (s *SessionStore) Create(u User, ip string) (string, *Session) {
	tok := randomToken(32)
	now := time.Now()
	sess := &Session{CSRF: randomToken(32), User: u, IP: ip, Created: now, LastSeen: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.m) >= maxSessions {
		var oldestK [32]byte
		var oldest time.Time
		for k, v := range s.m {
			if oldest.IsZero() || v.LastSeen.Before(oldest) {
				oldest, oldestK = v.LastSeen, k
			}
		}
		delete(s.m, oldestK)
	}
	s.m[tokenKey(tok)] = sess
	return tok, sess
}

func (s *SessionStore) Get(tok, ip string, bindIP bool) *Session {
	if tok == "" || len(tok) > 100 {
		return nil
	}
	k := tokenKey(tok)
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[k]
	if !ok {
		return nil
	}
	now := time.Now()
	if now.Sub(sess.LastSeen) > s.idle || now.Sub(sess.Created) > s.max {
		delete(s.m, k)
		return nil
	}
	if bindIP && sess.IP != ip {
		return nil
	}
	sess.LastSeen = now
	return sess
}

func (s *SessionStore) Delete(tok string) {
	s.mu.Lock()
	delete(s.m, tokenKey(tok))
	s.mu.Unlock()
}

func (s *SessionStore) DeleteUserExcept(name string, keep *Session) {
	s.mu.Lock()
	for k, v := range s.m {
		if v.User.Name == name && v != keep {
			delete(s.m, k)
		}
	}
	s.mu.Unlock()
}

func (s *SessionStore) gc() {
	now := time.Now()
	s.mu.Lock()
	for k, v := range s.m {
		if now.Sub(v.LastSeen) > s.idle || now.Sub(v.Created) > s.max {
			delete(s.m, k)
		}
	}
	s.mu.Unlock()
}

// ---- Rate limit login ----

type limitEntry struct {
	fails       int
	first       time.Time
	lockedUntil time.Time
}

type limiter struct {
	mu sync.Mutex
	m  map[string]*limitEntry
}

func newLimiter() *limiter { return &limiter{m: map[string]*limitEntry{}} }

func (l *limiter) blocked(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.m[key]
	if !ok {
		return 0
	}
	if d := time.Until(e.lockedUntil); d > 0 {
		return d
	}
	return 0
}

func (l *limiter) fail(key string, maxFails int, window, lock time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.m) > 20000 {
		for k, v := range l.m {
			if now.Sub(v.first) > window && now.After(v.lockedUntil) {
				delete(l.m, k)
			}
		}
	}
	e, ok := l.m[key]
	if !ok || now.Sub(e.first) > window {
		e = &limitEntry{first: now}
		l.m[key] = e
	}
	e.fails++
	if e.fails >= maxFails {
		e.lockedUntil = now.Add(lock)
		e.fails = 0
		e.first = now
	}
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.m, key)
	l.mu.Unlock()
}

// ---- User lookup & otorisasi ----

func lookupPasswd(name string) (User, error) {
	var line string
	if out, err := exec.Command("getent", "passwd", name).Output(); err == nil {
		line = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	} else {
		f, err := os.Open("/etc/passwd")
		if err != nil {
			return User{}, err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), name+":") {
				line = sc.Text()
				break
			}
		}
	}
	p := strings.Split(line, ":")
	if len(p) != 7 || p[0] != name {
		return User{}, errors.New("user tidak ditemukan")
	}
	uid, err1 := strconv.ParseUint(p[2], 10, 32)
	gid, err2 := strconv.ParseUint(p[3], 10, 32)
	if err1 != nil || err2 != nil {
		return User{}, errors.New("passwd rusak")
	}
	return User{Name: name, UID: uint32(uid), GID: uint32(gid), Home: p[5], Shell: p[6]}, nil
}

func (a *App) authorize(name string) (User, error) {
	u, err := lookupPasswd(name)
	if err != nil {
		return User{}, err
	}
	if u.Shell == "" || strings.HasSuffix(u.Shell, "nologin") || strings.HasSuffix(u.Shell, "/false") {
		return User{}, errors.New("user tidak punya shell login")
	}
	if u.UID == 0 {
		if !a.cfg.AllowRoot {
			return User{}, errors.New("login root dimatikan (allow_root=false)")
		}
	}

	allowed := u.UID == 0 && a.cfg.AllowRoot
	for _, n := range a.cfg.AllowedUsers {
		if n == name {
			allowed = true
		}
	}
	ou, err := user.Lookup(name)
	if err != nil {
		return User{}, err
	}
	gids, err := ou.GroupIds()
	if err != nil {
		return User{}, err
	}
	for _, g := range gids {
		id, err := strconv.ParseUint(g, 10, 32)
		if err != nil {
			continue
		}
		u.Groups = append(u.Groups, uint32(id))
		if allowed {
			continue
		}
		if grp, err := user.LookupGroupId(g); err == nil {
			for _, ag := range a.cfg.AllowedGroups {
				if grp.Name == ag {
					allowed = true
				}
			}
		}
	}
	if !allowed {
		return User{}, fmt.Errorf("user %s tidak ada di allowed_users / allowed_groups", name)
	}
	return u, nil
}

// ---- Handlers ----

type ctxKey int

const sessKey ctxKey = 1

func sessionFrom(r *http.Request) *Session {
	s, _ := r.Context().Value(sessKey).(*Session)
	return s
}

// Path yang tetap bisa diakses walau user wajib setup 2FA dulu.
var allowedDuring2FASetup = map[string]bool{
	"/api/me": true, "/api/logout": true, "/api/2fa/setup": true, "/api/2fa/enable": true,
}

func (a *App) requireAuth(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		var sess *Session
		if err == nil {
			sess = a.sessions.Get(c.Value, clientIP(r), a.cfg.BindSessionIP)
		}
		if sess == nil {
			writeErr(w, http.StatusUnauthorized, "Belum login atau sesi sudah berakhir")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !sameOrigin(r) {
				writeErr(w, http.StatusForbidden, "Origin request tidak valid")
				return
			}
			tok := r.Header.Get("X-CSRF-Token")
			if subtle.ConstantTimeCompare([]byte(tok), []byte(sess.CSRF)) != 1 {
				writeErr(w, http.StatusForbidden, "CSRF token tidak valid")
				return
			}
		}
		sess.mu.Lock()
		needs := sess.Needs2FASetup
		sess.mu.Unlock()
		if needs && !allowedDuring2FASetup[r.URL.Path] {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "Aktifkan 2FA dulu sebelum memakai panel", "code": "2fa_setup_required"})
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), sessKey, sess)))
	})
}

func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" && o != "https://"+r.Host {
		return false
	}
	return true
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeErr(w, http.StatusForbidden, "Origin request tidak valid")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		OTP      string `json:"otp"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ip := clientIP(r)
	name := strings.TrimSpace(req.Username)
	ipKey, userKey := "ip:"+ip, "user:"+name

	for _, k := range []string{ipKey, userKey} {
		if d := a.limiter.blocked(k); d > 0 {
			auditf("login-blocked user=%q ip=%s", name, ip)
			writeErr(w, http.StatusTooManyRequests, fmt.Sprintf("Terlalu banyak percobaan gagal. Coba lagi dalam %d menit.", int(d.Minutes())+1))
			return
		}
	}

	fail := func(reason string, msg string) {
		auditf("login-failed user=%q ip=%s reason=%q", name, ip, reason)
		a.limiter.fail(ipKey, 5, 15*time.Minute, 15*time.Minute)
		if usernameRe.MatchString(name) {
			a.limiter.fail(userKey, 10, 15*time.Minute, 15*time.Minute)
		}
		writeErr(w, http.StatusUnauthorized, msg)
	}
	const generic = "Login gagal: username, password, atau kode 2FA salah."

	if !usernameRe.MatchString(name) || req.Password == "" || len(req.Password) > 1024 || strings.ContainsRune(req.Password, 0) {
		time.Sleep(time.Second)
		fail("input tidak valid", generic)
		return
	}

	// Batasi PAM paralel (hash password mahal CPU).
	select {
	case a.loginSem <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	err := pamAuthenticate(a.cfg.PAMService, name, req.Password, ip)
	<-a.loginSem
	if err != nil {
		if errors.Is(err, errPasswordExpired) {
			fail(err.Error(), "Password kedaluwarsa — ganti dulu lewat SSH dengan perintah passwd.")
			return
		}
		fail(err.Error(), generic)
		return
	}
	u, err := a.authorize(name)
	if err != nil {
		fail(err.Error(), generic)
		return
	}

	rec, err := a.totp.get(name)
	if err != nil {
		fail("baca 2fa: "+err.Error(), generic)
		return
	}
	if rec != nil {
		if !a.totp.verify(name, rec.Secret, req.OTP) {
			fail("kode 2FA salah/kosong", generic)
			return
		}
	}

	a.limiter.reset(ipKey)
	a.limiter.reset(userKey)
	tok, sess := a.sessions.Create(u, ip)
	if rec == nil && a.cfg.Require2FA {
		sess.Needs2FASetup = true
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/",
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	auditf("login-ok user=%q ip=%s", name, ip)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	a.audit(r, "logout", "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	s := sessionFrom(r)
	s.mu.Lock()
	needs := s.Needs2FASetup
	s.mu.Unlock()
	rec, _ := a.totp.get(s.User.Name)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":            s.User.Name,
		"uid":             s.User.UID,
		"home":            s.User.Home,
		"csrf":            s.CSRF,
		"hostname":        a.hostname,
		"version":         version,
		"has_2fa":         rec != nil,
		"require_2fa":     a.cfg.Require2FA,
		"needs_2fa_setup": needs,
		"webroot_base":    a.cfg.WebrootBase,
		"certbot_email":   a.cfg.CertbotEmail,
		"max_upload_mb":   a.cfg.MaxUploadMB,
		"max_edit_mb":     a.cfg.MaxEditMB,
		"idle_minutes":    a.cfg.SessionIdleMinutes,
	})
}

func (a *App) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	s := sessionFrom(r)
	if rec, _ := a.totp.get(s.User.Name); rec != nil {
		writeErr(w, http.StatusBadRequest, "2FA sudah aktif")
		return
	}
	secret := generateTOTPSecret()
	s.mu.Lock()
	s.pendingTOTP = secret
	s.mu.Unlock()
	label := "SinarPanel:" + s.User.Name + "@" + a.hostname
	uri := "otpauth://totp/" + urlPathEscape(label) + "?secret=" + secret + "&issuer=SinarPanel&algorithm=SHA1&digits=6&period=30"
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret, "uri": uri})
}

func (a *App) handle2FAEnable(w http.ResponseWriter, r *http.Request) {
	s := sessionFrom(r)
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	secret := s.pendingTOTP
	s.mu.Unlock()
	if secret == "" {
		writeErr(w, http.StatusBadRequest, "Mulai setup 2FA dulu")
		return
	}
	if !a.totp.verify(s.User.Name, secret, req.Code) {
		writeErr(w, http.StatusBadRequest, "Kode salah. Pastikan jam server & HP sinkron.")
		return
	}
	if err := a.totp.save(s.User.Name, &totpRecord{Secret: secret, CreatedAt: time.Now()}); err != nil {
		writeErr(w, http.StatusInternalServerError, "Gagal menyimpan 2FA: "+err.Error())
		return
	}
	s.mu.Lock()
	s.pendingTOTP = ""
	s.Needs2FASetup = false
	s.mu.Unlock()
	// Sesi lain milik user ini dibuat sebelum 2FA aktif — putuskan.
	a.sessions.DeleteUserExcept(s.User.Name, s)
	a.audit(r, "2fa-enable", s.User.Name, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	s := sessionFrom(r)
	if a.cfg.Require2FA {
		writeErr(w, http.StatusBadRequest, "2FA diwajibkan oleh config (require_2fa)")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rec, err := a.totp.get(s.User.Name)
	if err != nil || rec == nil {
		writeErr(w, http.StatusBadRequest, "2FA belum aktif")
		return
	}
	if !a.totp.verify(s.User.Name, rec.Secret, req.Code) {
		writeErr(w, http.StatusBadRequest, "Kode 2FA salah")
		return
	}
	if err := a.totp.remove(s.User.Name); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.audit(r, "2fa-disable", s.User.Name, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
