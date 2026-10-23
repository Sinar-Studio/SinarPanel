//go:build !cgo || !(linux || darwin)

package main

import "errors"

var errPasswordExpired = errors.New("password kedaluwarsa")

// Build tanpa cgo tidak punya PAM, jadi semua login ditolak.
func pamAuthenticate(service, user, password, rhost string) error {
	return errors.New("binary ini di-build tanpa PAM (CGO_ENABLED=0); login tidak mungkin")
}
