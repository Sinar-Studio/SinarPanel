package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ensureSelfSigned membuat sertifikat self-signed jika cert/key belum ada.
func ensureSelfSigned(certPath, keyPath string) error {
	_, errC := os.Stat(certPath)
	_, errK := os.Stat(keyPath)
	if errC == nil && errK == nil {
		return nil
	}
	if errC == nil || errK == nil {
		return fmt.Errorf("hanya salah satu dari %s / %s yang ada", certPath, keyPath)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "SinarPanel " + host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	if host != "" {
		tmpl.DNSNames = append(tmpl.DNSNames, host)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.IsGlobalUnicast() {
				tmpl.IPAddresses = append(tmpl.IPAddresses, ipn.IP)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	for _, d := range []string{filepath.Dir(certPath), filepath.Dir(keyPath)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	if err := writeFileAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	return writeFileAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func certFingerprint(certPath string) (string, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", err
	}
	b, _ := pem.Decode(data)
	if b == nil {
		return "", errors.New("cert PEM tidak valid")
	}
	sum := sha256.Sum256(b.Bytes)
	parts := make([]string, len(sum))
	for i, c := range sum {
		parts[i] = fmt.Sprintf("%02X", c)
	}
	return "SHA256 " + strings.Join(parts, ":"), nil
}

// certManager me-reload cert otomatis jika file berubah (mis. renew Let's Encrypt).
type certManager struct {
	certPath, keyPath string
	mu                sync.Mutex
	cert              *tls.Certificate
	mod               time.Time
	checked           time.Time
}

func newCertManager(certPath, keyPath string) (*certManager, error) {
	m := &certManager{certPath: certPath, keyPath: keyPath}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *certManager) modTime() time.Time {
	var t time.Time
	for _, p := range []string{m.certPath, m.keyPath} {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(t) {
			t = fi.ModTime()
		}
	}
	return t
}

func (m *certManager) load() error {
	c, err := tls.LoadX509KeyPair(m.certPath, m.keyPath)
	if err != nil {
		return err
	}
	m.cert = &c
	m.mod = m.modTime()
	return nil
}

func (m *certManager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Since(m.checked) > time.Minute {
		m.checked = time.Now()
		if mt := m.modTime(); mt.After(m.mod) {
			_ = m.load() // kalau gagal, tetap pakai cert lama
		}
	}
	return m.cert, nil
}
