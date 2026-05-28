package agentllmconfig

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

const testKeyHex = "0011223344556677889900112233445566778899001122334455667788990011"

func TestNewEncrypter_Empty(t *testing.T) {
	e, err := NewEncrypter("")
	if err != nil {
		t.Fatalf("empty key should be no-op without error, got %v", err)
	}
	if e != nil {
		t.Fatalf("empty key should return nil encrypter, got %#v", e)
	}
}

func TestNewEncrypter_BadHex(t *testing.T) {
	if _, err := NewEncrypter("not-hex"); err == nil {
		t.Fatal("expected error for non-hex key")
	}
}

func TestNewEncrypter_WrongLength(t *testing.T) {
	if _, err := NewEncrypter(strings.Repeat("aa", 16)); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	e, err := NewEncrypter(testKeyHex)
	if err != nil {
		t.Fatalf("NewEncrypter: %v", err)
	}
	plain := []byte("sk-ant-api03-test-key-string")
	ct, nonce, err := e.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(ct, plain) {
		t.Fatal("ciphertext should differ from plaintext")
	}
	got, err := e.Decrypt(ct, nonce)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip mismatch: got %q want %q", got, plain)
	}
}

func TestEncrypt_NonceUnique(t *testing.T) {
	e, _ := NewEncrypter(testKeyHex)
	_, n1, _ := e.Encrypt([]byte("payload"))
	_, n2, _ := e.Encrypt([]byte("payload"))
	if bytes.Equal(n1, n2) {
		t.Fatal("nonce must be fresh per call")
	}
}

func TestEncrypt_NilReceiverRejects(t *testing.T) {
	var e *Encrypter
	if _, _, err := e.Encrypt([]byte("x")); err == nil {
		t.Fatal("nil receiver Encrypt should error")
	}
	if _, err := e.Decrypt([]byte("x"), []byte("n")); err == nil {
		t.Fatal("nil receiver Decrypt should error")
	}
}

func TestDecrypt_EmptyInputs(t *testing.T) {
	e, _ := NewEncrypter(testKeyHex)
	got, err := e.Decrypt(nil, nil)
	if err != nil {
		t.Fatalf("empty (nil, nil) should not error, got %v", err)
	}
	if got != nil {
		t.Fatalf("empty input should yield nil plaintext, got %v", got)
	}
}

func TestDecrypt_Tampered(t *testing.T) {
	e, _ := NewEncrypter(testKeyHex)
	ct, nonce, _ := e.Encrypt([]byte("secret"))
	ct[0] ^= 0x01
	if _, err := e.Decrypt(ct, nonce); err == nil {
		t.Fatal("tampered ciphertext should fail GCM verification")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	a, _ := NewEncrypter(testKeyHex)
	bKey := strings.Repeat("ff", 32)
	b, _ := NewEncrypter(hex.EncodeToString(mustDecodeHex(t, bKey)))
	ct, nonce, _ := a.Encrypt([]byte("secret"))
	if _, err := b.Decrypt(ct, nonce); err == nil {
		t.Fatal("decrypting with a different key should fail")
	}
}

func TestLast4(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"abc", ""},
		{"abcd", "abcd"},
		{"sk-ant-api03-XYZ-aBcD", "aBcD"},
	}
	for _, c := range cases {
		if got := Last4(c.in); got != c.want {
			t.Errorf("Last4(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex: %v", err)
	}
	return b
}
