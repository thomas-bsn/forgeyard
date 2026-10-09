package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	dir := t.TempDir()
	box, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := box.Encrypt("hunter2", "discord_client_secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, "hunter2") {
		t.Fatal("ciphertext contains the plaintext")
	}

	// A restart reads the same key file back.
	again, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := again.Decrypt(enc, "discord_client_secret"); err != nil || got != "hunter2" {
		t.Fatalf("decrypt after reload: %q %v", got, err)
	}
	if _, err := again.Decrypt(enc, "other_setting"); err == nil {
		t.Fatal("decrypted under another label")
	}
	if _, err := again.Decrypt("hunter2", "discord_client_secret"); err == nil {
		t.Fatal("accepted a plaintext value")
	}

	info, err := os.Stat(filepath.Join(dir, keyFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file permissions: %o", perm)
	}
}

func TestOtherKeyCannotDecrypt(t *testing.T) {
	a, _ := LoadOrCreate(t.TempDir())
	b, _ := LoadOrCreate(t.TempDir())
	enc, _ := a.Encrypt("secret", "x")
	if _, err := b.Decrypt(enc, "x"); err == nil {
		t.Fatal("another key decrypted the value")
	}
}
