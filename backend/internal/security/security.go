package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
)

func Random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func HashToken(v string) string { h := sha256.Sum256([]byte(v)); return fmt.Sprintf("%x", h[:]) }
func Password(p string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	h := argon2.IDKey([]byte(p), salt, 3, 64*1024, 2, 32)
	return base64.RawStdEncoding.EncodeToString(salt) + "." + base64.RawStdEncoding.EncodeToString(h)
}
func VerifyPassword(encoded, p string) bool {
	parts := split(encoded, '.')
	if len(parts) != 2 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[0])
	want, e2 := base64.RawStdEncoding.DecodeString(parts[1])
	if e1 != nil || e2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(p), salt, 3, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}
func split(s string, sep byte) []string {
	for i := range s {
		if s[i] == sep {
			return []string{s[:i], s[i+1:]}
		}
	}
	return nil
}

type Vault struct{ key []byte }

func OpenVault(dir string) (*Vault, error) {
	p := filepath.Join(dir, "secret.key")
	b, e := os.ReadFile(p)
	if errors.Is(e, os.ErrNotExist) {
		b = make([]byte, 32)
		if _, e = rand.Read(b); e == nil {
			e = os.WriteFile(p, b, 0600)
		}
	}
	if e != nil {
		return nil, e
	}
	if len(b) != 32 {
		return nil, errors.New("invalid secret key")
	}
	return &Vault{b}, nil
}
func (v *Vault) Encrypt(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	b, e := aes.NewCipher(v.key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return "", e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return "", e
	}
	out := g.Seal(n, n, []byte(s), nil)
	return base64.RawStdEncoding.EncodeToString(out), nil
}
func (v *Vault) Decrypt(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	raw, e := base64.RawStdEncoding.DecodeString(s)
	if e != nil {
		return "", e
	}
	b, _ := aes.NewCipher(v.key)
	g, _ := cipher.NewGCM(b)
	if len(raw) < g.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	p, e := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], nil)
	return string(p), e
}
