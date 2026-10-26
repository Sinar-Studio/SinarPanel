package main

import (
	"context"
	"crypto/tls"
	"embed"
	"flag"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

type App struct {
	cfg      *Config
	sessions *SessionStore
	limiter  *limiter
	totp     *totpStore
	loginSem chan struct{}
	updateMu sync.Mutex // serialisasi cek / jalankan update panel
	siteMu   sync.Mutex // serialisasi operasi Apache/certbot
	selfExe  string
	hostname string
}

func cmdServe(args []string) {
	fset := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fset.String("config", defaultConfigPath, "path file config")
	_ = fset.Parse(args)

	log.SetFlags(0) // journald sudah memberi timestamp
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if os.Geteuid() != 0 {
		log.Printf("PERINGATAN: tidak berjalan sebagai root — kelola Apache/certbot tidak akan berfungsi")
	}
	if os.Getenv("PATH") == "" {
		os.Setenv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	}
	syscall.Umask(0o022)

	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("executable: %v", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		log.Fatalf("executable: %v", err)
	}
	host, _ := os.Hostname()

	app := &App{
		cfg:      cfg,
		sessions: newSessionStore(time.Duration(cfg.SessionIdleMinutes)*time.Minute, time.Duration(cfg.SessionMaxHours)*time.Hour),
		limiter:  newLimiter(),
		totp:     newTOTPStore(filepath.Join(cfg.DataDir, "2fa")),
		loginSem: make(chan struct{}, 4),
		selfExe:  exe,
		hostname: host,
	}

	if err := ensureSelfSigned(cfg.TLSCert, cfg.TLSKey); err != nil {
		log.Fatalf("tls: %v", err)
	}
	cm, err := newCertManager(cfg.TLSCert, cfg.TLSKey)
	if err != nil {
		log.Fatalf("tls: %v", err)
	}
	if fp, err := certFingerprint(cfg.TLSCert); err == nil {
		log.Printf("Fingerprint sertifikat panel: %s", fp)
	}

	srv := &http.Server{
		Handler:           app.securityHeaders(app.ipFilter(app.routes())),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		TLSConfig: &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: cm.GetCertificate,
		},
		ErrorLog: log.New(filteredLog{}, "", 0),
	}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("SinarPanel %s berjalan di https://%s", version, cfg.Listen)

	go func() {
		if err := srv.ServeTLS(ln, "", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("serve: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("berhenti...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// Bot scanner sering memicu "TLS handshake error" — jangan penuhi log.
type filteredLog struct{}

func (filteredLog) Write(p []byte) (int, error) {
	if !strings.Contains(string(p), "TLS handshake error") {
		log.Print(strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, a.requireAuth(h)) }

	mux.HandleFunc("POST /api/login", a.handleLogin)
	api("POST /api/logout", a.handleLogout)
	api("GET /api/me", a.handleMe)
	api("POST /api/2fa/setup", a.handle2FASetup)
	api("POST /api/2fa/enable", a.handle2FAEnable)
	api("POST /api/2fa/disable", a.handle2FADisable)

	api("GET /api/system", a.handleSystem)
	api("POST /api/apache/{action}", a.handleApacheAction)
	api("POST /api/certbot/install", a.handleCertbotInstall)

	api("GET /api/update", a.handleUpdateStatus)
	api("POST /api/update/check", a.handleUpdateCheck)
	api("POST /api/update/run", a.handleUpdateRun)

	api("GET /api/sites", a.handleSitesList)
	api("POST /api/sites", a.handleSiteCreate)
	api("GET /api/sites/{name}/config", a.handleSiteConfigGet)
	api("PUT /api/sites/{name}/config", a.handleSiteConfigPut)
	api("POST /api/sites/{name}/enable", a.handleSiteEnable)
	api("POST /api/sites/{name}/disable", a.handleSiteDisable)
	api("PUT /api/sites/{name}/aliases", a.handleSiteAliases)
	api("POST /api/sites/{name}/ssl", a.handleSiteSSL)
	api("GET /api/sites/{name}/dns", a.handleSiteDNS)
	api("DELETE /api/sites/{name}", a.handleSiteDelete)

	api("GET /api/fs/list", a.handleFSList)
	api("GET /api/fs/read", a.handleFSRead)
	api("GET /api/fs/download", a.handleFSDownload)
	api("POST /api/fs/write", a.handleFSWrite)
	api("POST /api/fs/upload", a.handleFSUpload)
	api("POST /api/fs/mkdir", a.handleFSSimple("mkdir"))
	api("POST /api/fs/touch", a.handleFSSimple("touch"))
	api("POST /api/fs/rename", a.handleFSSimple("rename"))
	api("POST /api/fs/copy", a.handleFSSimple("copy"))
	api("POST /api/fs/delete", a.handleFSSimple("delete"))
	api("POST /api/fs/chmod", a.handleFSSimple("chmod"))
	api("POST /api/fs/extract", a.handleFSSimple("extract"))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "endpoint tidak ada")
	})

	sub, _ := fs.Sub(webFS, "web")
	static := http.FileServer(http.FS(sub))
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", static))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		data, _ := fs.ReadFile(sub, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})
	return mux
}

func (a *App) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Strict-Transport-Security", "max-age=31536000")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (a *App) ipFilter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.cfg.ipAllowed(clientIP(r)) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
