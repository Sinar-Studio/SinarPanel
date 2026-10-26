package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const managedMarker = "# Managed by SinarPanel"

var (
	siteNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,200}$`)
	safePathRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
	urlPathRe  = regexp.MustCompile(`^[A-Za-z0-9._~/-]*$`)
)

type CertInfo struct {
	Path     string    `json:"path"`
	Issuer   string    `json:"issuer"`
	Names    []string  `json:"names"`
	NotAfter time.Time `json:"not_after"`
	DaysLeft int       `json:"days_left"`
}

type SiteInfo struct {
	Name         string    `json:"name"`
	ServerName   string    `json:"server_name"`
	Aliases      []string  `json:"aliases"`
	DocumentRoot string    `json:"document_root,omitempty"`
	ProxyTarget  string    `json:"proxy_target,omitempty"`
	Ports        []string  `json:"ports"`
	Enabled      bool      `json:"enabled"`
	Managed      bool      `json:"managed"`
	SSLConf      string    `json:"ssl_conf,omitempty"`
	SSLEnabled   bool      `json:"ssl_enabled"`
	Cert         *CertInfo `json:"cert,omitempty"`
	certFile     string
}

func (a *App) confPath(name string) string {
	return filepath.Join(a.cfg.ApacheSitesAvailable, name+".conf")
}

func (a *App) siteEnabled(name string) bool {
	_, err := os.Lstat(filepath.Join(a.cfg.ApacheSitesEnabled, name+".conf"))
	return err == nil
}

func parseVhost(content string, s *SiteInfo) {
	seenAlias := map[string]bool{}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, managedMarker) {
			s.Managed = true
			continue
		}
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Fields(line)
		switch strings.ToLower(f[0]) {
		case "<virtualhost":
			for _, addr := range f[1:] {
				addr = strings.TrimSuffix(addr, ">")
				if i := strings.LastIndex(addr, ":"); i >= 0 {
					s.Ports = appendUnique(s.Ports, addr[i+1:])
				}
			}
		case "servername":
			if len(f) > 1 && s.ServerName == "" {
				s.ServerName = strings.ToLower(f[1])
			}
		case "serveralias":
			for _, al := range f[1:] {
				al = strings.ToLower(al)
				if !seenAlias[al] {
					seenAlias[al] = true
					s.Aliases = append(s.Aliases, al)
				}
			}
		case "documentroot":
			if len(f) > 1 && s.DocumentRoot == "" {
				s.DocumentRoot = strings.Trim(f[1], `"`)
			}
		case "proxypass":
			if len(f) > 2 && f[1] == "/" && s.ProxyTarget == "" {
				s.ProxyTarget = f[2]
			}
		case "sslcertificatefile":
			if len(f) > 1 && s.certFile == "" {
				s.certFile = strings.Trim(f[1], `"`)
			}
		}
	}
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func readCert(path string) *CertInfo {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	b, _ := pem.Decode(data)
	if b == nil {
		return nil
	}
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		return nil
	}
	issuer := c.Issuer.Organization
	is := c.Issuer.CommonName
	if len(issuer) > 0 {
		is = issuer[0] + " " + is
	}
	return &CertInfo{
		Path: path, Issuer: is, Names: c.DNSNames, NotAfter: c.NotAfter,
		DaysLeft: int(time.Until(c.NotAfter).Hours() / 24),
	}
}

func (a *App) listSites() ([]*SiteInfo, error) {
	entries, err := os.ReadDir(a.cfg.ApacheSitesAvailable)
	if err != nil {
		return nil, err
	}
	byName := map[string]*SiteInfo{}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".conf") || !e.Type().IsRegular() {
			continue
		}
		name := strings.TrimSuffix(n, ".conf")
		data, err := readLimited(filepath.Join(a.cfg.ApacheSitesAvailable, n), 1<<20)
		if err != nil {
			continue
		}
		s := &SiteInfo{Name: name, Enabled: a.siteEnabled(name), Aliases: []string{}, Ports: []string{}}
		parseVhost(string(data), s)
		byName[name] = s
	}
	// Gabungkan "<nama>-le-ssl.conf" buatan certbot ke situs induknya.
	for name, s := range byName {
		base := strings.TrimSuffix(name, "-le-ssl")
		if base == name {
			continue
		}
		parent, ok := byName[base]
		if !ok {
			continue
		}
		parent.SSLConf = name
		parent.SSLEnabled = s.Enabled
		if s.certFile != "" {
			parent.certFile = s.certFile
		}
		parent.Ports = appendUnique(parent.Ports, "443")
		delete(byName, name)
	}
	out := make([]*SiteInfo, 0, len(byName))
	for _, s := range byName {
		if s.certFile == "" && s.ServerName != "" && validDomain(s.ServerName) {
			p := filepath.Join(a.cfg.LetsEncryptDir, "live", s.ServerName, "cert.pem")
			if _, err := os.Stat(p); err == nil {
				s.certFile = p
			}
		}
		if s.certFile != "" {
			s.Cert = readCert(s.certFile)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (a *App) findSite(name string) (*SiteInfo, error) {
	if !siteNameRe.MatchString(name) {
		return nil, errors.New("nama situs tidak valid")
	}
	sites, err := a.listSites()
	if err != nil {
		return nil, err
	}
	for _, s := range sites {
		if s.Name == name {
			return s, nil
		}
	}
	return nil, errors.New("situs tidak ditemukan")
}

func readLimited(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("file terlalu besar")
	}
	return data, nil
}

// ---- Perintah Apache ----

type opLog struct{ lines []string }

func (l *opLog) add(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}
func (l *opLog) out(title, output string) {
	if output != "" {
		l.lines = append(l.lines, "$ "+title, output)
	} else {
		l.lines = append(l.lines, "$ "+title)
	}
}

func (a *App) apacheCmd(ctx context.Context, l *opLog, name string, args ...string) error {
	out, err := runCmd(ctx, 60*time.Second, nil, name, args...)
	l.out(strings.Join(append([]string{name}, args...), " "), out)
	return err
}

func (a *App) configTest(ctx context.Context, l *opLog) error {
	return a.apacheCmd(ctx, l, "apache2ctl", "configtest")
}

func (a *App) reloadApache(ctx context.Context, l *opLog) error {
	return a.apacheCmd(ctx, l, "systemctl", "reload", a.cfg.ApacheService)
}

func (a *App) handleApacheAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	l := &opLog{}
	var err error
	a.siteMu.Lock()
	switch action {
	case "configtest":
		err = a.configTest(r.Context(), l)
	case "reload", "restart":
		if err = a.configTest(r.Context(), l); err == nil {
			err = a.apacheCmd(r.Context(), l, "systemctl", action, a.cfg.ApacheService)
		} else {
			l.add("Dibatalkan: konfigurasi Apache error.")
		}
	default:
		a.siteMu.Unlock()
		writeErr(w, http.StatusNotFound, "aksi tidak dikenal")
		return
	}
	a.siteMu.Unlock()
	a.audit(r, "apache-"+action, "", err)
	respondOp(w, l, err)
}

func respondOp(w http.ResponseWriter, l *opLog, err error) {
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "log": strings.Join(l.lines, "\n")})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": strings.Join(l.lines, "\n")})
}

// ---- List & buat situs ----

func (a *App) handleSitesList(w http.ResponseWriter, r *http.Request) {
	sites, err := a.listSites()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Tidak bisa membaca "+a.cfg.ApacheSitesAvailable+": "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": sites})
}

type createSiteReq struct {
	Domain       string   `json:"domain"`
	Aliases      []string `json:"aliases"`
	Type         string   `json:"type"`
	DocumentRoot string   `json:"document_root"`
	ProxyURL     string   `json:"proxy_url"`
	WebSocket    bool     `json:"websocket"`
	SSL          bool     `json:"ssl"`
	Email        string   `json:"email"`
}

func (a *App) validDocroot(p string) (string, error) {
	c := filepath.Clean(strings.TrimSpace(p))
	if !safePathRe.MatchString(c) {
		return "", errors.New("document root hanya boleh berisi huruf, angka, . _ - /")
	}
	if !strings.HasPrefix(c, a.cfg.WebrootBase+"/") {
		return "", fmt.Errorf("document root harus di dalam %s/", a.cfg.WebrootBase)
	}
	return c, nil
}

func validProxyURL(s string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("target proxy harus seperti http://127.0.0.1:3000")
	}
	host := u.Hostname()
	if net.ParseIP(host) == nil && host != "localhost" && !validDomain(host) {
		return "", errors.New("host target proxy tidak valid")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("port target proxy tidak valid")
		}
	}
	path := u.EscapedPath()
	if !urlPathRe.MatchString(path) {
		return "", errors.New("path target proxy tidak valid")
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	hostPort := u.Host
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		hostPort = "[" + host + "]"
		if u.Port() != "" {
			hostPort += ":" + u.Port()
		}
	}
	return u.Scheme + "://" + hostPort + path, nil
}

func buildVhost(req *createSiteReq, docroot, proxy string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — dibuat %s\n", managedMarker, time.Now().Format("2006-01-02 15:04"))
	b.WriteString("<VirtualHost *:80>\n")
	fmt.Fprintf(&b, "    ServerName %s\n", req.Domain)
	if len(req.Aliases) > 0 {
		fmt.Fprintf(&b, "    ServerAlias %s\n", strings.Join(req.Aliases, " "))
	}
	if req.Type == "proxy" {
		b.WriteString("\n    ProxyPreserveHost On\n    ProxyRequests Off\n")
		if req.WebSocket {
			fmt.Fprintf(&b, "    ProxyPass / %s upgrade=websocket\n", proxy)
		} else {
			fmt.Fprintf(&b, "    ProxyPass / %s\n", proxy)
		}
		fmt.Fprintf(&b, "    ProxyPassReverse / %s\n", proxy)
		b.WriteString("    RequestHeader set X-Forwarded-Proto expr=%{REQUEST_SCHEME}\n")
	} else {
		fmt.Fprintf(&b, "    DocumentRoot %s\n\n", docroot)
		fmt.Fprintf(&b, "    <Directory %s>\n", docroot)
		b.WriteString("        Options -Indexes +FollowSymLinks\n        AllowOverride All\n        Require all granted\n    </Directory>\n")
	}
	fmt.Fprintf(&b, "\n    ErrorLog ${APACHE_LOG_DIR}/%s-error.log\n", req.Domain)
	fmt.Fprintf(&b, "    CustomLog ${APACHE_LOG_DIR}/%s-access.log combined\n", req.Domain)
	b.WriteString("</VirtualHost>\n")
	return b.String()
}

// mkdirOwned membuat docroot; hanya direktori yang baru dibuat yang di-chown
// ke user login. Mengembalikan direktori teratas yang dibuat (untuk rollback).
func mkdirOwned(path, base string, uid, gid uint32) (string, error) {
	rel, err := filepath.Rel(base, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("path di luar webroot")
	}
	if fi, err := os.Lstat(base); err != nil || !fi.IsDir() {
		if err := os.MkdirAll(base, 0o755); err != nil {
			return "", err
		}
	}
	cur, top := base, ""
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err == nil {
			if !fi.IsDir() {
				return top, fmt.Errorf("%s sudah ada dan bukan direktori", cur)
			}
			continue
		}
		if err := os.Mkdir(cur, 0o755); err != nil {
			return top, err
		}
		if top == "" {
			top = cur
		}
		if err := os.Lchown(cur, int(uid), int(gid)); err != nil {
			return top, err
		}
	}
	return top, nil
}

func writePlaceholder(docroot, domain string, uid, gid uint32) {
	p := filepath.Join(docroot, "index.html")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return
	}
	fmt.Fprintf(f, `<!doctype html><html lang="id"><head><meta charset="utf-8"><title>%s</title></head>
<body style="font-family:sans-serif;display:grid;place-items:center;height:100vh;margin:0;background:#0f172a;color:#e2e8f0">
<div style="text-align:center"><h1>%s</h1><p>Situs aktif. Ganti file ini lewat File Manager SinarPanel.</p></div></body></html>
`, domain, domain)
	f.Close()
	_ = os.Lchown(p, int(uid), int(gid))
}

func (a *App) handleSiteCreate(w http.ResponseWriter, r *http.Request) {
	s := sessionFrom(r)
	var req createSiteReq
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if !validDomain(req.Domain) {
		writeErr(w, http.StatusBadRequest, "Domain tidak valid (contoh: blog.domainku.com)")
		return
	}
	aliases, err := normalizeAliases(req.Domain, req.Aliases)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Aliases = aliases

	var docroot, proxy string
	switch req.Type {
	case "static":
		if req.DocumentRoot == "" {
			req.DocumentRoot = filepath.Join(a.cfg.WebrootBase, req.Domain)
		}
		if docroot, err = a.validDocroot(req.DocumentRoot); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	case "proxy":
		if proxy, err = validProxyURL(req.ProxyURL); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "Tipe harus static atau proxy")
		return
	}
	email := strings.TrimSpace(req.Email)
	if email != "" && !validEmail(email) {
		writeErr(w, http.StatusBadRequest, "Email tidak valid")
		return
	}

	a.siteMu.Lock()
	defer a.siteMu.Unlock()
	ctx := r.Context()
	l := &opLog{}

	confPath := a.confPath(req.Domain)
	if _, err := os.Lstat(confPath); err == nil {
		writeErr(w, http.StatusConflict, "Config "+confPath+" sudah ada")
		return
	}
	if sites, err := a.listSites(); err == nil {
		if err := nameConflict(sites, "", append([]string{req.Domain}, req.Aliases...)); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
	}

	var createdTop string
	if req.Type == "static" {
		createdTop, err = mkdirOwned(docroot, a.cfg.WebrootBase, s.User.UID, s.User.GID)
		if err != nil {
			if createdTop != "" {
				os.RemoveAll(createdTop)
			}
			writeErr(w, http.StatusInternalServerError, "Gagal membuat document root: "+err.Error())
			return
		}
		writePlaceholder(docroot, req.Domain, s.User.UID, s.User.GID)
		l.add("Document root: %s", docroot)
	} else {
		if err := a.apacheCmd(ctx, l, "a2enmod", "-q", "proxy", "proxy_http", "headers"); err != nil {
			respondOp(w, l, err)
			return
		}
	}

	rollback := func() {
		_, _ = runCmd(context.Background(), 30*time.Second, nil, "a2dissite", "-q", req.Domain)
		os.Remove(confPath)
		if createdTop != "" {
			os.RemoveAll(createdTop)
		}
		l.add("Rollback: config dan folder baru dihapus, Apache tidak diubah.")
	}

	f, err := os.OpenFile(confPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		rollback()
		respondOp(w, l, fmt.Errorf("gagal menulis config: %v", err))
		return
	}
	_, err = f.WriteString(buildVhost(&req, docroot, proxy))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		rollback()
		respondOp(w, l, err)
		return
	}
	l.add("Config ditulis: %s", confPath)

	if err := a.apacheCmd(ctx, l, "a2ensite", "-q", req.Domain); err != nil {
		rollback()
		respondOp(w, l, err)
		return
	}
	if err := a.configTest(ctx, l); err != nil {
		rollback()
		respondOp(w, l, errors.New("configtest Apache gagal"))
		return
	}
	if err := a.reloadApache(ctx, l); err != nil {
		rollback()
		_ = a.reloadApache(context.Background(), l)
		respondOp(w, l, err)
		return
	}
	l.add("✓ Situs %s aktif (HTTP).", req.Domain)
	a.audit(r, "site-create", req.Domain, nil)

	var sslErr error
	if req.SSL {
		sslErr = a.runCertbot(ctx, l, append([]string{req.Domain}, req.Aliases...), req.Domain, email, true)
		a.audit(r, "site-ssl", req.Domain, sslErr)
	}
	resp := map[string]any{"ok": true, "log": strings.Join(l.lines, "\n")}
	if sslErr != nil {
		resp["ssl_error"] = "Situs berhasil dibuat, tapi SSL gagal: " + sslErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// normalizeAliases merapikan & memvalidasi daftar alias (tanpa duplikat dan
// tanpa domain utama).
func normalizeAliases(domain string, in []string) ([]string, error) {
	var aliases []string
	for _, al := range in {
		al = strings.ToLower(strings.TrimSpace(al))
		if al == "" || al == domain {
			continue
		}
		if !validDomain(al) {
			return nil, errors.New("Alias tidak valid: " + al)
		}
		aliases = appendUnique(aliases, al)
	}
	if len(aliases) > 20 {
		return nil, errors.New("Maksimal 20 alias")
	}
	return aliases, nil
}

// nameConflict mengecek apakah salah satu nama sudah dipakai situs lain
// (situs bernama skip diabaikan).
func nameConflict(sites []*SiteInfo, skip string, names []string) error {
	for _, st := range sites {
		if st.Name == skip {
			continue
		}
		for _, n := range names {
			if st.ServerName == n || contains(st.Aliases, n) {
				return fmt.Errorf("%s sudah dipakai oleh situs %s", n, st.Name)
			}
		}
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ---- Edit config ----

func (a *App) handleSiteConfigGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !siteNameRe.MatchString(name) {
		writeErr(w, http.StatusBadRequest, "nama tidak valid")
		return
	}
	p := a.confPath(name)
	if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
		writeErr(w, http.StatusNotFound, "config tidak ditemukan")
		return
	}
	data, err := readLimited(p, 1<<20)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "path": p, "content": string(data), "enabled": a.siteEnabled(name)})
}

func (a *App) handleSiteConfigPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !siteNameRe.MatchString(name) {
		writeErr(w, http.StatusBadRequest, "nama tidak valid")
		return
	}
	if len(req.Content) > 512<<10 || !utf8.ValidString(req.Content) || strings.ContainsRune(req.Content, 0) {
		writeErr(w, http.StatusBadRequest, "isi config tidak valid / terlalu besar")
		return
	}
	a.siteMu.Lock()
	defer a.siteMu.Unlock()
	p := a.confPath(name)
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		writeErr(w, http.StatusNotFound, "config tidak ditemukan")
		return
	}
	old, err := readLimited(p, 1<<20)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	l := &opLog{}
	if err := writeFileAtomic(p, []byte(req.Content), fi.Mode().Perm()); err != nil {
		respondOp(w, l, err)
		return
	}
	l.add("Disimpan: %s", p)
	if a.siteEnabled(name) {
		if err := a.configTest(r.Context(), l); err != nil {
			_ = writeFileAtomic(p, old, fi.Mode().Perm())
			l.add("Config dikembalikan ke versi sebelumnya karena configtest gagal.")
			a.audit(r, "site-config", name, err)
			respondOp(w, l, errors.New("configtest gagal, perubahan dibatalkan"))
			return
		}
		if err := a.reloadApache(r.Context(), l); err != nil {
			respondOp(w, l, err)
			return
		}
	} else {
		l.add("Situs nonaktif — Apache tidak di-reload.")
	}
	a.audit(r, "site-config", name, nil)
	respondOp(w, l, nil)
}

// ---- Edit alias ----

// setServerAlias mengganti semua ServerAlias di blok <VirtualHost> yang
// ServerName-nya serverName dengan satu baris berisi aliases (tepat di bawah
// ServerName). Blok vhost lain tidak disentuh. ok=false jika tidak ada blok
// yang cocok.
func setServerAlias(content, serverName string, aliases []string) (out string, ok bool) {
	lines := strings.Split(content, "\n")
	res := make([]string, 0, len(lines)+1)
	start := -1
	for i, raw := range lines {
		f := strings.Fields(raw)
		key := ""
		if len(f) > 0 {
			key = strings.ToLower(f[0])
		}
		switch {
		case strings.HasPrefix(key, "<virtualhost"):
			if start >= 0 { // blok sebelumnya tidak ditutup
				res = append(res, lines[start:i]...)
			}
			start = i
			continue
		case key == "</virtualhost>" && start >= 0:
			block := lines[start : i+1]
			if nb, matched := rewriteAliasBlock(block, serverName, aliases); matched {
				block, ok = nb, true
			}
			res = append(res, block...)
			start = -1
			continue
		}
		if start < 0 {
			res = append(res, raw)
		}
	}
	if start >= 0 { // blok tidak ditutup — biarkan apa adanya
		res = append(res, lines[start:]...)
	}
	return strings.Join(res, "\n"), ok
}

func rewriteAliasBlock(block []string, serverName string, aliases []string) ([]string, bool) {
	nameIdx := -1
	for i, raw := range block {
		if f := strings.Fields(raw); len(f) > 1 && strings.EqualFold(f[0], "ServerName") && strings.EqualFold(f[1], serverName) {
			nameIdx = i
			break
		}
	}
	if nameIdx < 0 {
		return block, false
	}
	out := make([]string, 0, len(block)+1)
	for i, raw := range block {
		if f := strings.Fields(raw); len(f) > 0 && strings.EqualFold(f[0], "ServerAlias") {
			continue
		}
		out = append(out, raw)
		if i == nameIdx && len(aliases) > 0 {
			indent := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
			out = append(out, indent+"ServerAlias "+strings.Join(aliases, " "))
		}
	}
	return out, true
}

func (a *App) handleSiteAliases(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Aliases  []string `json:"aliases"`
		SSL      bool     `json:"ssl"`
		Email    string   `json:"email"`
		Redirect bool     `json:"redirect"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email != "" && !validEmail(req.Email) {
		writeErr(w, http.StatusBadRequest, "Email tidak valid")
		return
	}
	a.siteMu.Lock()
	defer a.siteMu.Unlock()
	sites, err := a.listSites()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var site *SiteInfo
	for _, s := range sites {
		if s.Name == r.PathValue("name") {
			site = s
		}
	}
	if site == nil || !siteNameRe.MatchString(site.Name) {
		writeErr(w, http.StatusNotFound, "situs tidak ditemukan")
		return
	}
	if !validDomain(site.ServerName) {
		writeErr(w, http.StatusBadRequest, "Situs ini tidak punya ServerName yang valid")
		return
	}
	aliases, err := normalizeAliases(site.ServerName, req.Aliases)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := nameConflict(sites, site.Name, aliases); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if req.SSL && !site.Enabled {
		writeErr(w, http.StatusBadRequest, "Aktifkan situs dulu sebelum pasang SSL")
		return
	}

	// Tulis ServerAlias ke config HTTP & SSL; simpan isi lama untuk rollback.
	type saved struct {
		path string
		old  []byte
		perm os.FileMode
	}
	names := []string{site.Name}
	if site.SSLConf != "" {
		names = append(names, site.SSLConf)
	}
	var plan []saved
	var news []string
	for _, n := range names {
		p := a.confPath(n)
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() {
			writeErr(w, http.StatusNotFound, "config tidak ditemukan: "+p)
			return
		}
		old, err := readLimited(p, 1<<20)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		nc, ok := setServerAlias(string(old), site.ServerName, aliases)
		if !ok {
			writeErr(w, http.StatusBadRequest, "Tidak menemukan <VirtualHost> dengan ServerName "+site.ServerName+" di "+p)
			return
		}
		plan = append(plan, saved{p, old, fi.Mode().Perm()})
		news = append(news, nc)
	}

	ctx, l := r.Context(), &opLog{}
	rollback := func() {
		for _, s := range plan {
			_ = writeFileAtomic(s.path, s.old, s.perm)
		}
		l.add("Config dikembalikan ke versi sebelumnya.")
	}
	for i, s := range plan {
		if err := writeFileAtomic(s.path, []byte(news[i]), s.perm); err != nil {
			rollback()
			respondOp(w, l, err)
			return
		}
		l.add("Disimpan: %s", s.path)
	}
	if len(aliases) > 0 {
		l.add("ServerAlias: %s", strings.Join(aliases, " "))
	} else {
		l.add("Semua alias dihapus.")
	}
	if site.Enabled {
		if err := a.configTest(ctx, l); err != nil {
			rollback()
			a.audit(r, "site-aliases", site.Name, err)
			respondOp(w, l, errors.New("configtest gagal, perubahan dibatalkan"))
			return
		}
		if err := a.reloadApache(ctx, l); err != nil {
			rollback()
			_ = a.reloadApache(context.Background(), l)
			respondOp(w, l, err)
			return
		}
	} else {
		l.add("Situs nonaktif — Apache tidak di-reload.")
	}
	a.audit(r, "site-aliases", site.Name, nil)

	resp := map[string]any{"ok": true}
	if req.SSL {
		sslErr := a.runCertbot(ctx, l, append([]string{site.ServerName}, aliases...), site.ServerName, req.Email, req.Redirect)
		a.audit(r, "site-ssl", site.Name, sslErr)
		if sslErr != nil {
			resp["ssl_error"] = "Alias tersimpan, tapi SSL gagal: " + sslErr.Error()
		}
	}
	resp["log"] = strings.Join(l.lines, "\n")
	writeJSON(w, http.StatusOK, resp)
}

// ---- Aktif / nonaktif / hapus ----

func (a *App) handleSiteEnable(w http.ResponseWriter, r *http.Request)  { a.toggleSite(w, r, true) }
func (a *App) handleSiteDisable(w http.ResponseWriter, r *http.Request) { a.toggleSite(w, r, false) }

func (a *App) toggleSite(w http.ResponseWriter, r *http.Request, enable bool) {
	a.siteMu.Lock()
	defer a.siteMu.Unlock()
	site, err := a.findSite(r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	names := []string{site.Name}
	if site.SSLConf != "" {
		names = append(names, site.SSLConf)
	}
	ctx, l := r.Context(), &opLog{}
	cmd, undo := "a2ensite", "a2dissite"
	if !enable {
		cmd, undo = undo, cmd
	}
	for _, n := range names {
		if err := a.apacheCmd(ctx, l, cmd, "-q", n); err != nil {
			respondOp(w, l, err)
			return
		}
	}
	if err := a.configTest(ctx, l); err != nil {
		for _, n := range names {
			_ = a.apacheCmd(ctx, l, undo, "-q", n)
		}
		l.add("Dibatalkan karena configtest gagal.")
		respondOp(w, l, err)
		return
	}
	err = a.reloadApache(ctx, l)
	a.audit(r, cmd, site.Name, err)
	respondOp(w, l, err)
}

func (a *App) handleSiteDelete(w http.ResponseWriter, r *http.Request) {
	a.siteMu.Lock()
	defer a.siteMu.Unlock()
	site, err := a.findSite(r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if !site.Managed {
		writeErr(w, http.StatusForbidden, "Hanya situs buatan SinarPanel yang bisa dihapus dari panel (situs ini bisa dinonaktifkan)")
		return
	}
	ctx, l := r.Context(), &opLog{}
	names := []string{site.Name}
	if site.SSLConf != "" {
		names = append(names, site.SSLConf)
	}
	for _, n := range names {
		if a.siteEnabled(n) {
			if err := a.apacheCmd(ctx, l, "a2dissite", "-q", n); err != nil {
				respondOp(w, l, err)
				return
			}
		}
		if err := os.Remove(a.confPath(n)); err != nil {
			respondOp(w, l, err)
			return
		}
		l.add("Dihapus: %s", a.confPath(n))
	}
	if err := a.configTest(ctx, l); err == nil {
		_ = a.reloadApache(ctx, l)
	} else {
		l.add("PERINGATAN: configtest gagal setelah penghapusan — cek config Apache lain.")
	}
	if r.URL.Query().Get("delete_cert") == "1" && site.Cert != nil && validDomain(site.ServerName) {
		if _, err := exec.LookPath("certbot"); err == nil {
			_ = a.apacheCmd(ctx, l, "certbot", "delete", "--non-interactive", "--cert-name", site.ServerName)
		}
	}
	if site.DocumentRoot != "" {
		l.add("File di %s TIDAK dihapus (hapus manual lewat File Manager jika perlu).", site.DocumentRoot)
	}
	a.audit(r, "site-delete", site.Name, nil)
	respondOp(w, l, nil)
}

// ---- SSL / certbot ----

func (a *App) runCertbot(ctx context.Context, l *opLog, domains []string, certName, email string, redirect bool) error {
	if _, err := exec.LookPath("certbot"); err != nil {
		l.add("certbot belum terinstall. Install dari Dashboard → Install Certbot.")
		return errors.New("certbot belum terinstall")
	}
	args := []string{"--apache", "--non-interactive", "--agree-tos", "--cert-name", certName}
	if email == "" {
		email = a.cfg.CertbotEmail
	}
	if email != "" {
		args = append(args, "--email", email, "--no-eff-email")
	} else {
		args = append(args, "--register-unsafely-without-email")
	}
	if redirect {
		args = append(args, "--redirect")
	} else {
		args = append(args, "--no-redirect")
	}
	for _, d := range domains {
		args = append(args, "-d", d)
	}
	out, err := runCmd(ctx, 5*time.Minute, nil, "certbot", args...)
	l.out("certbot "+strings.Join(args, " "), out)
	if err != nil {
		return errors.New("certbot gagal — cek DNS sudah mengarah ke server & port 80 terbuka")
	}
	l.add("✓ SSL terpasang. Perpanjangan otomatis lewat timer certbot.")
	return nil
}

func (a *App) handleSiteSSL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Redirect bool   `json:"redirect"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email != "" && !validEmail(req.Email) {
		writeErr(w, http.StatusBadRequest, "Email tidak valid")
		return
	}
	a.siteMu.Lock()
	defer a.siteMu.Unlock()
	site, err := a.findSite(r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if !site.Enabled {
		writeErr(w, http.StatusBadRequest, "Aktifkan situs dulu sebelum pasang SSL")
		return
	}
	domains := []string{site.ServerName}
	for _, d := range append([]string{site.ServerName}, site.Aliases...) {
		if !validDomain(d) {
			writeErr(w, http.StatusBadRequest, "Domain/alias tidak didukung untuk SSL otomatis: "+d)
			return
		}
	}
	domains = append(domains, site.Aliases...)
	l := &opLog{}
	err = a.runCertbot(r.Context(), l, domains, site.ServerName, req.Email, req.Redirect)
	a.audit(r, "site-ssl", site.Name, err)
	respondOp(w, l, err)
}

func localIPs() []string {
	var out []string
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.IsGlobalUnicast() {
				out = append(out, ipn.IP.String())
			}
		}
	}
	return out
}

func (a *App) handleSiteDNS(w http.ResponseWriter, r *http.Request) {
	site, err := a.findSite(r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	type res struct {
		Domain string   `json:"domain"`
		IPs    []string `json:"ips"`
		Error  string   `json:"error,omitempty"`
	}
	var results []res
	for _, d := range append([]string{site.ServerName}, site.Aliases...) {
		if !validDomain(d) {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		ips, err := net.DefaultResolver.LookupHost(ctx, d)
		cancel()
		rr := res{Domain: d, IPs: ips}
		if err != nil {
			rr.Error = "tidak resolve"
		}
		if rr.IPs == nil {
			rr.IPs = []string{}
		}
		results = append(results, rr)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "server_ips": localIPs()})
}

func (a *App) handleCertbotInstall(w http.ResponseWriter, r *http.Request) {
	a.siteMu.Lock()
	defer a.siteMu.Unlock()
	l := &opLog{}
	if _, err := exec.LookPath("apt-get"); err != nil {
		respondOp(w, l, errors.New("apt-get tidak ada — install certbot manual"))
		return
	}
	env := []string{"DEBIAN_FRONTEND=noninteractive"}
	out, err := runCmd(r.Context(), 10*time.Minute, env, "apt-get", "update", "-q")
	l.out("apt-get update", lastLines(out, 15))
	if err == nil {
		out, err = runCmd(r.Context(), 10*time.Minute, env, "apt-get", "install", "-y", "-q", "certbot", "python3-certbot-apache")
		l.out("apt-get install -y certbot python3-certbot-apache", lastLines(out, 25))
	}
	a.audit(r, "certbot-install", "", err)
	respondOp(w, l, err)
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append([]string{"..."}, lines[len(lines)-n:]...)
	}
	return strings.Join(lines, "\n")
}
