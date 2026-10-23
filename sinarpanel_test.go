package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidDomain(t *testing.T) {
	good := []string{"blog.example.com", "a.b.co", "xn--80ak6aa92e.com", "my-app.example.id"}
	bad := []string{"", "localhost", "-a.com", "a-.com", "a..com", "*.example.com", "Example.com", "1.2.3.4", "a.com\n", "a.com;rm", "a .com", strings.Repeat("a", 64) + ".com"}
	for _, d := range good {
		if !validDomain(d) {
			t.Errorf("harusnya valid: %q", d)
		}
	}
	for _, d := range bad {
		if validDomain(d) {
			t.Errorf("harusnya tidak valid: %q", d)
		}
	}
}

func TestValidProxyURL(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:3000":      "http://127.0.0.1:3000/",
		"http://localhost:8080/app":  "http://localhost:8080/app/",
		"https://[::1]:9000/":        "https://[::1]:9000/",
		"http://backend.internal.io": "http://backend.internal.io/",
	}
	for in, want := range cases {
		got, err := validProxyURL(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"ftp://x.com", "http://a b", "http://127.0.0.1:99999", "http://u:p@127.0.0.1", "http://127.0.0.1/?x=1", "http://127.0.0.1/a\"b", "http://127.0.0.1/a\nProxyPass"} {
		if _, err := validProxyURL(in); err == nil {
			t.Errorf("harusnya ditolak: %q", in)
		}
	}
}

func TestDocroot(t *testing.T) {
	a := &App{cfg: defaultConfig()}
	if _, err := a.validDocroot("/var/www/blog.example.com"); err != nil {
		t.Error(err)
	}
	for _, p := range []string{"/var/www", "/var/www/../etc", "/etc/x", "/var/www/a b", "/var/www/a\"b", "relative", "/var/wwwx/a"} {
		if _, err := a.validDocroot(p); err == nil {
			t.Errorf("harusnya ditolak: %q", p)
		}
	}
}

func TestBuildAndParseVhost(t *testing.T) {
	req := &createSiteReq{Domain: "blog.example.com", Aliases: []string{"www.blog.example.com"}, Type: "static"}
	conf := buildVhost(req, "/var/www/blog.example.com", "")
	var s SiteInfo
	parseVhost(conf, &s)
	if !s.Managed || s.ServerName != "blog.example.com" || s.DocumentRoot != "/var/www/blog.example.com" || len(s.Aliases) != 1 || s.Ports[0] != "80" {
		t.Fatalf("parse salah: %+v\n%s", s, conf)
	}
	req = &createSiteReq{Domain: "app.example.com", Type: "proxy", WebSocket: true}
	conf = buildVhost(req, "", "http://127.0.0.1:3000/")
	s = SiteInfo{}
	parseVhost(conf, &s)
	if s.ProxyTarget != "http://127.0.0.1:3000/" || !strings.Contains(conf, "upgrade=websocket") {
		t.Fatalf("proxy salah: %+v\n%s", s, conf)
	}
}

func TestTOTPVectors(t *testing.T) {
	key := []byte("12345678901234567890") // RFC 6238 lampiran B
	if c := totpCode(key, 59/30); c != "287082" {
		t.Errorf("t=59: %s", c)
	}
	if c := totpCode(key, 1111111109/30); c != "081804" {
		t.Errorf("t=1111111109: %s", c)
	}
}

func TestTOTPReplay(t *testing.T) {
	st := newTOTPStore(t.TempDir())
	secret := generateTOTPSecret()
	key, _ := b32.DecodeString(secret)
	code := totpCode(key, time.Now().Unix()/30)
	if !st.verify("bob", secret, code) {
		t.Fatal("kode valid ditolak")
	}
	if st.verify("bob", secret, code) {
		t.Fatal("replay kode diterima")
	}
	if st.verify("bob", secret, "000000x") {
		t.Fatal("kode sampah diterima")
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter()
	for i := 0; i < 5; i++ {
		l.fail("ip:1.2.3.4", 5, time.Minute, time.Minute)
	}
	if l.blocked("ip:1.2.3.4") == 0 {
		t.Fatal("harusnya terkunci")
	}
	if l.blocked("ip:5.6.7.8") != 0 {
		t.Fatal("IP lain ikut terkunci")
	}
}

func TestSafeJoinAndExtract(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out")
	for _, n := range []string{"../evil", "/etc/passwd", "a/../../evil", "..\\evil"} {
		if _, ok := safeJoin(dest, n); ok {
			t.Errorf("zip-slip lolos: %q", n)
		}
	}
	zp := filepath.Join(dir, "x.zip")
	f, _ := os.Create(zp)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("site/index.html")
	w.Write([]byte("hi"))
	w, _ = zw.Create("../../escape.txt")
	w.Write([]byte("bad"))
	zw.Close()
	f.Close()
	res, err := fsExtract(zp, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	st := res.(*extractStats)
	if st.Files != 1 || st.Unsafe != 1 {
		t.Fatalf("stats salah: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Fatal("file lolos keluar dest")
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "site/index.html")); string(b) != "hi" {
		t.Fatal("isi salah")
	}
}

func TestProtectedPaths(t *testing.T) {
	for _, p := range []string{"/", "/etc", "/home", "/var/www", "/usr/bin", "/root/.ssh"} {
		if checkNotProtected(p) == nil {
			t.Errorf("harusnya dilindungi: %s", p)
		}
	}
	if checkNotProtected("/var/www/blog") != nil {
		t.Error("/var/www/blog harusnya boleh")
	}
}

func helperRoundtrip(t *testing.T, req fsReq, payload ...string) fsHeader {
	t.Helper()
	line, _ := json.Marshal(req)
	in := bufio.NewReader(strings.NewReader(string(line) + "\n" + strings.Join(payload, "")))
	in.ReadBytes('\n')
	var buf bytes.Buffer
	out := bufio.NewWriter(&buf)
	_ = helperDispatch(&req, in, out)
	out.Flush()
	var h fsHeader
	if err := json.Unmarshal([]byte(strings.SplitN(buf.String(), "\n", 2)[0]), &h); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHelperOps(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "a.txt")
	if h := helperRoundtrip(t, fsReq{Op: "write", Path: p, Max: 1 << 20}, "halo"); !h.OK {
		t.Fatal(h.Error)
	}
	if h := helperRoundtrip(t, fsReq{Op: "read", Path: p, Max: 1 << 20}); !h.OK || !strings.Contains(string(h.Data), "halo") {
		t.Fatal("read gagal", h)
	}
	if h := helperRoundtrip(t, fsReq{Op: "upload", Path: p, Max: 1 << 20}, "x"); h.OK || !strings.HasPrefix(h.Error, "Sudah ada") {
		t.Fatal("upload tanpa overwrite harusnya gagal", h)
	}
	if h := helperRoundtrip(t, fsReq{Op: "upload", Path: p, Max: 1 << 20, Overwrite: true}, "baru"); !h.OK {
		t.Fatal(h.Error)
	}
	if b, _ := os.ReadFile(p); string(b) != "baru" {
		t.Fatal("overwrite gagal")
	}
	if h := helperRoundtrip(t, fsReq{Op: "upload", Path: filepath.Join(d, "big"), Max: 3}, "12345"); h.OK {
		t.Fatal("batas ukuran tidak berlaku")
	}
	if h := helperRoundtrip(t, fsReq{Op: "chmod", Path: p, Mode: "600"}); !h.OK {
		t.Fatal(h.Error)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatal("chmod tidak berlaku")
	}
	if h := helperRoundtrip(t, fsReq{Op: "copy", Path: d, Dest: filepath.Join(d, "sub")}); h.OK {
		t.Fatal("copy ke dalam diri sendiri harusnya ditolak")
	}
	if h := helperRoundtrip(t, fsReq{Op: "list", Path: d}); !h.OK || !strings.Contains(string(h.Data), "a.txt") {
		t.Fatal("list gagal", h)
	}
	if h := helperRoundtrip(t, fsReq{Op: "read", Path: "relative/path"}); h.OK {
		t.Fatal("path relatif harusnya ditolak")
	}
	entries, _ := os.ReadDir(d)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".sinarpanel-upload") {
			t.Fatal("file temp upload tertinggal")
		}
	}
}

func TestAuthMiddleware(t *testing.T) {
	cfg := defaultConfig()
	a := &App{cfg: cfg, sessions: newSessionStore(time.Hour, time.Hour), limiter: newLimiter(), totp: newTOTPStore(t.TempDir())}
	tok, sess := a.sessions.Create(User{Name: "bob"}, "192.0.2.1")
	h := a.securityHeaders(a.routes())

	do := func(method, path string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://panel:8443"+path, strings.NewReader(`{}`))
		r.RemoteAddr = "192.0.2.1:5555"
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if w := do("GET", "/api/me", nil); w.Code != 200 {
		t.Fatalf("me: %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/api/fs/mkdir", nil); w.Code != 403 {
		t.Fatalf("POST tanpa CSRF harusnya 403, dapat %d", w.Code)
	}
	if w := do("POST", "/api/fs/mkdir", map[string]string{"X-CSRF-Token": sess.CSRF, "Origin": "https://evil.com"}); w.Code != 403 {
		t.Fatalf("Origin asing harusnya 403, dapat %d", w.Code)
	}
	if w := do("POST", "/api/logout", map[string]string{"X-CSRF-Token": sess.CSRF, "Origin": "https://panel:8443"}); w.Code != 200 {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/me", nil); w.Code != 401 {
		t.Fatalf("setelah logout harusnya 401, dapat %d", w.Code)
	}
	w := do("GET", "/", nil)
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") || w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("security header hilang")
	}

	// Sesi terikat IP
	tok2, _ := a.sessions.Create(User{Name: "bob"}, "192.0.2.1")
	if a.sessions.Get(tok2, "198.51.100.9", true) != nil {
		t.Fatal("sesi bisa dipakai dari IP lain")
	}
}
