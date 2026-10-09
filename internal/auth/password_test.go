package auth

import (
	"testing"
	"time"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword("correct horse battery", hash)
	if err != nil || !ok {
		t.Fatalf("correct password rejected: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong password", hash)
	if err != nil || ok {
		t.Fatalf("wrong password accepted: ok=%v err=%v", ok, err)
	}
	if _, err := VerifyPassword("x", "not-a-hash"); err == nil {
		t.Fatal("malformed hash accepted")
	}
}

func TestLoginLimiter(t *testing.T) {
	l := NewLoginLimiter(2, time.Minute)
	l.Fail("1.2.3.4")
	if !l.Allowed("1.2.3.4") {
		t.Fatal("blocked after one failure")
	}
	l.Fail("1.2.3.4")
	if l.Allowed("1.2.3.4") {
		t.Fatal("allowed after max failures")
	}
	if !l.Allowed("5.6.7.8") {
		t.Fatal("other key blocked")
	}
	l.Reset("1.2.3.4")
	if !l.Allowed("1.2.3.4") {
		t.Fatal("blocked after reset")
	}
}
