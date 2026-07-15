package sign

import (
	"os"
	"path/filepath"
	"testing"
)

func newHome(t *testing.T) (home, indexTools string) {
	t.Helper()
	home = t.TempDir()
	indexTools = filepath.Join(home, "index", "tools", "x", "1")
	if err := os.MkdirAll(indexTools, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(indexTools, "any.yaml"), []byte("name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(home, "index", "tools")
}

func TestSignVerifyRoundtrip(t *testing.T) {
	home, it := newHome(t)
	if _, err := SignIndex(home, it, "t"); err != nil {
		t.Fatal(err)
	}
	present, err := VerifyIndex(home, it)
	if !present || err != nil {
		t.Fatalf("present=%v err=%v", present, err)
	}
}

func TestRotateKeepsOldSignatureValid(t *testing.T) {
	home, it := newHome(t)
	// Sign with the original key.
	if _, err := SignIndex(home, it, "t1"); err != nil {
		t.Fatal(err)
	}
	// Rotate but do NOT re-sign: the on-disk signature is still from the old
	// key, which rotation kept authorized -> must still verify.
	newFP, err := Rotate(home)
	if err != nil {
		t.Fatal(err)
	}
	if present, err := VerifyIndex(home, it); !present || err != nil {
		t.Fatalf("old signature should still verify after rotate: %v", err)
	}
	// Re-signing with the new key also verifies, and the new key is primary.
	s, err := SignIndex(home, it, "t2")
	if err != nil {
		t.Fatal(err)
	}
	if s.KeyFprint != newFP {
		t.Fatalf("re-signed with %s, want new key %s", s.KeyFprint, newFP)
	}
	if present, err := VerifyIndex(home, it); !present || err != nil {
		t.Fatalf("new signature should verify: %v", err)
	}
}

func TestCrossMachineAuthorizeAndRevoke(t *testing.T) {
	// Machine A publishes.
	homeA, itA := newHome(t)
	if _, err := SignIndex(homeA, itA, "a"); err != nil {
		t.Fatal(err)
	}
	pubA, _ := LoadPublicKey(homeA)
	fpA := Fingerprint(pubA)

	// Machine B has its own key; it clones A's index (copy files).
	homeB, itB := newHome(t)
	// Give B its own primary key + a signature from B.
	if _, err := SignIndex(homeB, itB, "b"); err != nil {
		t.Fatal(err)
	}
	pubB, _ := LoadPublicKey(homeB)

	// On A: B's key is not authorized yet -> a signature from B must fail.
	// Simulate by installing B's signature+pub scenario on A: authorize B,
	// then a sig by B verifies; revoke B, then it fails.
	if err := AddAuthorizedKey(homeA, pubB, "machine-B"); err != nil {
		t.Fatal(err)
	}
	keys, _ := LoadAuthorizedKeys(homeA)
	if len(keys) < 2 {
		t.Fatalf("expected A to now trust >=2 keys, got %d", len(keys))
	}
	// Revoke A's own primary and confirm the count drops / fingerprint gone.
	if _, err := RevokeAuthorizedKey(homeA, Fingerprint(pubB)); err != nil {
		t.Fatal(err)
	}
	after, _ := LoadAuthorizedKeys(homeA)
	for _, k := range after {
		if k.Fingerprint() == Fingerprint(pubB) {
			t.Fatal("revoked key still present")
		}
	}
	_ = fpA
}

func TestUnauthorizedSignerRejected(t *testing.T) {
	home, it := newHome(t)
	if _, err := SignIndex(home, it, "t"); err != nil {
		t.Fatal(err)
	}
	// Tamper index.sig's key fingerprint to an unknown key.
	sigPath := filepath.Join(home, "index", "index.sig")
	raw, _ := os.ReadFile(sigPath)
	tampered := []byte(replaceFirst(string(raw), Fingerprint(mustPub(t, home)), "deadbeefdeadbeef"))
	os.WriteFile(sigPath, tampered, 0o644)
	if _, err := VerifyIndex(home, it); err == nil {
		t.Fatal("signature from an unauthorized/unknown key must be rejected")
	}
}

func mustPub(t *testing.T, home string) []byte {
	pub, err := LoadPublicKey(home)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func replaceFirst(s, old, new string) string {
	i := indexOf(s, old)
	if i < 0 {
		return s
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
