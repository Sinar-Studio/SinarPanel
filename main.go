// SinarPanel — panel VPS ringan untuk mengelola subdomain Apache, SSL (certbot)
// dan file. Semua logika ada di satu binary tanpa dependency pihak ketiga.
package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "1.0.0"

const defaultConfigPath = "/etc/sinarpanel/config.json"

func usage() {
	fmt.Fprintf(os.Stderr, `SinarPanel %s

Pemakaian:
  sinarpanel serve [-config %s]
  sinarpanel cert-fingerprint [-config %s]
  sinarpanel version
`, version, defaultConfigPath, defaultConfigPath)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		cmdServe(os.Args[2:])
	case "fs-helper":
		// Dipanggil internal oleh panel dengan UID/GID user yang login.
		os.Exit(runFSHelper())
	case "cert-fingerprint":
		cmdFingerprint(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
}

func cmdFingerprint(args []string) {
	fs := flag.NewFlagSet("cert-fingerprint", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "path file config")
	_ = fs.Parse(args)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	if err := ensureSelfSigned(cfg.TLSCert, cfg.TLSKey); err != nil {
		fmt.Fprintln(os.Stderr, "tls:", err)
		os.Exit(1)
	}
	fp, err := certFingerprint(cfg.TLSCert)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tls:", err)
		os.Exit(1)
	}
	fmt.Println(fp)
}
