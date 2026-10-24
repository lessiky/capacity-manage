package aesgcm

import (
	"strings"
	"testing"
)

const testKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := New("short"); err == nil {
		t.Fatal("expected error for non-hex/short key")
	}
	if _, err := New(strings.Repeat("ab", 31)); err == nil {
		t.Fatal("expected error for 31-byte key")
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	c, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}
	plain := "oracle://scott:tiger@db:1521/svc"
	enc, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "enc.v1.") {
		t.Fatalf("missing enc.v1. prefix: %s", enc)
	}
	got, err := c.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != plain {
		t.Fatalf("roundtrip mismatch: %q", got)
	}
	enc2, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if enc == enc2 {
		t.Fatal("expected different ciphertext (random nonce) for same plaintext")
	}
}

func TestDecryptPlaintextPassthrough(t *testing.T) {
	c, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.Decrypt("plain-password"); err != nil || got != "plain-password" {
		t.Fatalf("legacy plaintext passthrough failed: %q %v", got, err)
	}
	if got, err := c.Decrypt(""); err != nil || got != "" {
		t.Fatalf("empty passthrough failed: %q %v", got, err)
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	c1, _ := New(testKey)
	enc, err := c1.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := New(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Decrypt(enc); err == nil {
		t.Fatal("expected error when decrypting with a different key")
	}
}