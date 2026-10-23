package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TOTP (RFC 6238) — kompatibel dengan Google Authenticator, Authy, 2FAS, dll.

type totpRecord struct {
	Secret    string    `json:"secret"`
	CreatedAt time.Time `json:"created_at"`
}

type totpStore struct {
	dir  string
	mu   sync.Mutex
	last map[string]int64 // counter terakhir yang dipakai, cegah replay
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPStore(dir string) *totpStore {
	return &totpStore{dir: dir, last: map[string]int64{}}
}

func generateTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

func (t *totpStore) path(user string) (string, error) {
	if !usernameRe.MatchString(user) {
		return "", errors.New("username tidak valid")
	}
	return filepath.Join(t.dir, user+".json"), nil
}

func (t *totpStore) get(user string) (*totpRecord, error) {
	p, err := t.path(user)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec totpRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	if rec.Secret == "" {
		return nil, nil
	}
	return &rec, nil
}

func (t *totpStore) save(user string, rec *totpRecord) error {
	p, err := t.path(user)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(t.dir, 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(rec)
	return writeFileAtomic(p, data, 0o600)
}

func (t *totpStore) remove(user string) error {
	p, err := t.path(user)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func totpCode(key []byte, counter int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000)
}

// verify menerima kode di jendela ±30 detik dan menolak kode yang sudah dipakai.
func (t *totpStore) verify(user, secret, code string) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return false
	}
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return false
	}
	now := time.Now().Unix() / 30
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, d := range []int64{0, -1, 1} {
		c := now + d
		if subtle.ConstantTimeCompare([]byte(totpCode(key, c)), []byte(code)) == 1 {
			if c <= t.last[user] {
				return false
			}
			t.last[user] = c
			return true
		}
	}
	return false
}
