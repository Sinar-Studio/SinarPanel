package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

type Config struct {
	Listen string `json:"listen"`

	// Siapa yang boleh login. User harus bisa auth via PAM (password user Linux)
	// DAN masuk salah satu grup/daftar berikut.
	AllowedGroups []string `json:"allowed_groups"`
	AllowedUsers  []string `json:"allowed_users"`
	AllowRoot     bool     `json:"allow_root"`

	// Kosong = semua IP boleh. Isi CIDR/IP untuk membatasi akses panel.
	AllowedIPs []string `json:"allowed_ips"`

	Require2FA         bool `json:"require_2fa"`
	SessionIdleMinutes int  `json:"session_idle_minutes"`
	SessionMaxHours    int  `json:"session_max_hours"`
	BindSessionIP      bool `json:"bind_session_ip"`

	WebrootBase  string `json:"webroot_base"`
	CertbotEmail string `json:"certbot_email"`
	MaxUploadMB  int64  `json:"max_upload_mb"`
	MaxEditMB    int64  `json:"max_edit_mb"`

	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`
	DataDir string `json:"data_dir"`

	PAMService string `json:"pam_service"`

	ApacheSitesAvailable string `json:"apache_sites_available"`
	ApacheSitesEnabled   string `json:"apache_sites_enabled"`
	ApacheService        string `json:"apache_service"`
	LetsEncryptDir       string `json:"letsencrypt_dir"`

	allowedNets []*net.IPNet
}

func defaultConfig() *Config {
	return &Config{
		Listen:               ":8443",
		AllowedGroups:        []string{"sudo", "wheel", "admin"},
		AllowedUsers:         []string{},
		AllowRoot:            false,
		AllowedIPs:           []string{},
		Require2FA:           false,
		SessionIdleMinutes:   30,
		SessionMaxHours:      12,
		BindSessionIP:        true,
		WebrootBase:          "/var/www",
		MaxUploadMB:          1024,
		MaxEditMB:            5,
		TLSCert:              "/etc/sinarpanel/tls/cert.pem",
		TLSKey:               "/etc/sinarpanel/tls/key.pem",
		DataDir:              "/etc/sinarpanel",
		PAMService:           "sinarpanel",
		ApacheSitesAvailable: "/etc/apache2/sites-available",
		ApacheSitesEnabled:   "/etc/apache2/sites-enabled",
		ApacheService:        "apache2",
		LetsEncryptDir:       "/etc/letsencrypt",
	}
}

func loadConfig(path string) (*Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("config %s tidak ada, memakai default", path)
			return cfg, cfg.validate()
		}
		return nil, err
	}
	if err := checkConfigPerms(path); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, cfg.validate()
}

// Config menentukan siapa yang boleh jadi "root" lewat panel, jadi tolak start
// kalau file bisa diubah oleh user lain.
func checkConfigPerms(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s bisa ditulis group/others (mode %o) — jalankan: chmod 600 %s", path, fi.Mode().Perm(), path)
	}
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 {
			return fmt.Errorf("%s harus dimiliki root", path)
		}
	}
	return nil
}

func (c *Config) validate() error {
	if c.Listen == "" {
		return errors.New("listen kosong")
	}
	if c.SessionIdleMinutes <= 0 || c.SessionIdleMinutes > 24*60 {
		return errors.New("session_idle_minutes harus 1..1440")
	}
	if c.SessionMaxHours <= 0 || c.SessionMaxHours > 24*30 {
		return errors.New("session_max_hours harus 1..720")
	}
	if c.MaxUploadMB <= 0 {
		c.MaxUploadMB = 1024
	}
	if c.MaxEditMB <= 0 || c.MaxEditMB > 50 {
		c.MaxEditMB = 5
	}
	for _, p := range []*string{&c.WebrootBase, &c.TLSCert, &c.TLSKey, &c.DataDir, &c.ApacheSitesAvailable, &c.ApacheSitesEnabled, &c.LetsEncryptDir} {
		if !filepath.IsAbs(*p) {
			return fmt.Errorf("path %q harus absolut", *p)
		}
		*p = filepath.Clean(*p)
	}
	if c.WebrootBase == "/" {
		return errors.New("webroot_base tidak boleh /")
	}
	if c.CertbotEmail != "" && !validEmail(c.CertbotEmail) {
		return errors.New("certbot_email tidak valid")
	}
	c.allowedNets = nil
	for _, s := range c.AllowedIPs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
				s += "/32"
			} else {
				s += "/128"
			}
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return fmt.Errorf("allowed_ips: %q tidak valid", s)
		}
		c.allowedNets = append(c.allowedNets, n)
	}
	return nil
}

func (c *Config) ipAllowed(ipStr string) bool {
	if len(c.allowedNets) == 0 {
		return true
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, n := range c.allowedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
