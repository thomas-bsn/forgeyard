// Package secrets encrypts sensitive settings (such as the Discord client secret) before they are stored,
// so a copy of the database alone does not reveal them.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// KeyEnv overrides the key file, e.g. to keep the key out of the data volume.
	KeyEnv  = "FORGEYARD_SECRET_KEY"
	keyFile = "secret.key"
	keyLen  = 32
	prefix  = "v1:"
)

// Box encrypts and decrypts values with AES-256-GCM.
type Box struct {
	aead cipher.AEAD
}

// LoadOrCreate returns a Box using the key from $FORGEYARD_SECRET_KEY, or from dataDir/secret.key,
// generating that file on first start.
func LoadOrCreate(dataDir string) (*Box, error) {
	if env := os.Getenv(KeyEnv); env != "" {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(env))
		if err != nil || len(key) != keyLen {
			return nil, fmt.Errorf("%s must be %d bytes encoded in base64", KeyEnv, keyLen)
		}
		return New(key)
	}

	path := filepath.Join(dataDir, keyFile)
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, keyLen)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		// O_EXCL: never overwrite a key that appeared meanwhile, it would make stored secrets unreadable.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		if _, err := f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n"); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		return New(key)
	}
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || len(key) != keyLen {
		return nil, fmt.Errorf("%s is corrupted", path)
	}
	return New(key)
}

// New returns a Box for a 32-byte key.
func New(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypt seals plaintext. label (the setting name) is authenticated, so a ciphertext copied
// into another setting fails to decrypt.
func (b *Box) Encrypt(plaintext, label string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plaintext), []byte(label))
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a value produced by Encrypt with the same label.
func (b *Box) Decrypt(value, label string) (string, error) {
	raw, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return "", errors.New("secret is not encrypted")
	}
	sealed, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(sealed) < b.aead.NonceSize() {
		return "", errors.New("malformed secret")
	}
	nonce, ct := sealed[:b.aead.NonceSize()], sealed[b.aead.NonceSize():]
	plain, err := b.aead.Open(nil, nonce, ct, []byte(label))
	if err != nil {
		return "", errors.New("secret cannot be decrypted: wrong key or tampered value")
	}
	return string(plain), nil
}
