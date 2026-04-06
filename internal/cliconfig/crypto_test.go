package cliconfig

import (
	"testing"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := deriveKey("test-machine-id")
	plaintext := []byte(`{"tokens":{"ctx":{"access_token":"secret123"}}}`)

	encrypted, err := encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if string(encrypted) == string(plaintext) {
		t.Fatal("encrypted should differ from plaintext")
	}

	decrypted, err := decrypt(encrypted, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDecrypt_WrongKey(t *testing.T) {
	key1 := deriveKey("machine-1")
	key2 := deriveKey("machine-2")
	plaintext := []byte("secret data")

	encrypted, err := encrypt(plaintext, key1)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	_, err = decrypt(encrypted, key2)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestDeriveKey_Deterministic(t *testing.T) {
	k1 := deriveKey("same-input")
	k2 := deriveKey("same-input")
	if string(k1) != string(k2) {
		t.Fatal("same input should produce same key")
	}
}
