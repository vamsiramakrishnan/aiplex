package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/vamsiramakrishnan/aiplex/internal/capability"
)

func TestLocalSigner_GenerateAndPersist(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "local.key")

	s1, err := NewLocalSigner(keyPath, "")
	if err != nil {
		t.Fatalf("first NewLocalSigner: %v", err)
	}
	pub1 := s1.PublicKey()

	// Re-loading the same path returns the same key.
	s2, err := NewLocalSigner(keyPath, "")
	if err != nil {
		t.Fatalf("second NewLocalSigner: %v", err)
	}
	if string(pub1) != string(s2.PublicKey()) {
		t.Errorf("public key changed across loads")
	}
	if s1.KeyID() != s2.KeyID() {
		t.Errorf("kid changed across loads: %s vs %s", s1.KeyID(), s2.KeyID())
	}
}

func TestLocalSigner_MintAndVerify(t *testing.T) {
	s, err := NewLocalSigner(filepath.Join(t.TempDir(), "k"), "aiplex://test")
	if err != nil {
		t.Fatalf("NewLocalSigner: %v", err)
	}

	caps := capability.CapSet{
		{URI: "cap://tool/search@v1", Actions: []string{"call"}},
		{URI: "cap://memory/notes@v1", Actions: []string{"read", "write"}},
	}
	tok, err := s.Mint("alice@local", caps, "spiffe://local/sa/tutor", time.Hour)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	claims, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "alice@local" {
		t.Errorf("Subject = %q", claims.Subject)
	}
	if len(claims.Caps) != 2 {
		t.Errorf("Caps = %d entries", len(claims.Caps))
	}
	if claims.Act["sub"] != "spiffe://local/sa/tutor" {
		t.Errorf("Act = %v", claims.Act)
	}
}

func TestLocalSigner_RejectsTamperedToken(t *testing.T) {
	s, _ := NewLocalSigner(filepath.Join(t.TempDir(), "k"), "")
	tok, _ := s.Mint("alice@local", nil, "", time.Hour)

	// Flip the last character of the signature.
	tampered := tok[:len(tok)-1] + "A"
	if tampered == tok {
		tampered = tok[:len(tok)-1] + "B"
	}
	if _, err := s.Verify(tampered); err == nil {
		t.Error("expected verify to reject tampered token")
	}
}

func TestLocalSigner_RejectsExpired(t *testing.T) {
	s, _ := NewLocalSigner(filepath.Join(t.TempDir(), "k"), "")
	tok, _ := s.Mint("alice@local", nil, "", -time.Minute) // already expired
	if _, err := s.Verify(tok); err == nil {
		t.Error("expected verify to reject expired token")
	}
}

func TestLocalSigner_RejectsForeignIssuer(t *testing.T) {
	s1, _ := NewLocalSigner(filepath.Join(t.TempDir(), "k1"), "aiplex://node-a")
	s2, _ := NewLocalSigner(filepath.Join(t.TempDir(), "k2"), "aiplex://node-b")
	tok, _ := s1.Mint("alice@local", nil, "", time.Hour)
	if _, err := s2.Verify(tok); err == nil {
		t.Error("expected node-b to reject node-a's token")
	}
}
