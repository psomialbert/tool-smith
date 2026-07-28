package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smith/internal/config"
)

// pubFromKeyFile derives the base64 public key from a generated private key file.
func pubFromKeyFile(t *testing.T, keyPath string) string {
	t.Helper()
	privB64, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(privB64)))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		t.Fatalf("bad private key file: %v", err)
	}
	return base64.StdEncoding.EncodeToString(ed25519.PrivateKey(priv).Public().(ed25519.PublicKey))
}

func TestAllowlistSignVerifyRoundtrip(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "k.key")
	if err := allowlistKeygen([]string{"--out", keyPath}); err != nil {
		t.Fatal(err)
	}
	pub := pubFromKeyFile(t, keyPath)

	al := filepath.Join(dir, "allow.txt")
	os.WriteFile(al, []byte("github.com/acme/*\n"), 0o644)
	if err := allowlistSign([]string{"--key", keyPath, al}); err != nil {
		t.Fatal(err)
	}
	if err := verifyAllowlistSig(al, pub); err != nil {
		t.Fatalf("freshly signed file should verify: %v", err)
	}
	// Tamper → verification fails.
	os.WriteFile(al, []byte("github.com/evil/*\n"), 0o644)
	if err := verifyAllowlistSig(al, pub); err == nil {
		t.Fatal("a tampered allowlist file must fail verification")
	}
}

func TestAllowlistPatternsFailClosed(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "k.key")
	if err := allowlistKeygen([]string{"--out", keyPath}); err != nil {
		t.Fatal(err)
	}
	pub := pubFromKeyFile(t, keyPath)
	al := filepath.Join(dir, "allow.txt")
	os.WriteFile(al, []byte("github.com/acme/*\n"), 0o644)

	fc := config.FileConfig{Allowlist: config.AllowlistConfig{File: al, PublicKey: pub}}

	// public_key set but no signature yet → fail closed.
	if _, err := allowlistPatterns(fc); err == nil {
		t.Fatal("unsigned file with public_key configured must fail closed")
	}
	// Sign → patterns load.
	if err := allowlistSign([]string{"--key", keyPath, al}); err != nil {
		t.Fatal(err)
	}
	pats, err := allowlistPatterns(fc)
	if err != nil {
		t.Fatalf("signed file should load: %v", err)
	}
	if len(pats) != 1 || pats[0] != "github.com/acme/*" {
		t.Fatalf("patterns wrong: %v", pats)
	}
	// Tamper after signing → fail closed again.
	os.WriteFile(al, []byte("github.com/evil/*\n"), 0o644)
	if _, err := allowlistPatterns(fc); err == nil {
		t.Fatal("a tampered signed allowlist must fail closed")
	}
}

func TestAllowlistSignFlagAfterPositional(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "k.key")
	if err := allowlistKeygen([]string{"--out", keyPath}); err != nil {
		t.Fatal(err)
	}
	al := filepath.Join(dir, "allow.txt")
	os.WriteFile(al, []byte("github.com/acme/*\n"), 0o644)
	// Flag AFTER the positional — how people actually type it.
	if err := allowlistSign([]string{al, "--key", keyPath}); err != nil {
		t.Fatalf("sign must accept <file> before --key: %v", err)
	}
	if err := verifyAllowlistSig(al, pubFromKeyFile(t, keyPath)); err != nil {
		t.Fatalf("signature should verify: %v", err)
	}
}

func TestUnsignedAllowlistStillWorksWithoutPublicKey(t *testing.T) {
	dir := t.TempDir()
	al := filepath.Join(dir, "allow.txt")
	os.WriteFile(al, []byte("github.com/acme/*\n"), 0o644)
	// No public_key → signing not required (backward compatible).
	fc := config.FileConfig{Allowlist: config.AllowlistConfig{File: al}}
	pats, err := allowlistPatterns(fc)
	if err != nil || len(pats) != 1 {
		t.Fatalf("unsigned allowlist without public_key must work: %v %v", pats, err)
	}
}

// TestKeygenRefusesDanglingSymlink guards the stat-then-write TOCTOU: a dangling
// symlink at --out passed the os.Stat existence check (stat follows the link and
// errors "not found"), then os.WriteFile followed it and dropped the private key
// at the link target. O_EXCL must refuse instead.
func TestKeygenRefusesDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "out.key")
	target := filepath.Join(dir, "evil-target")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := allowlistKeygen([]string{"--out", link}); err == nil {
		t.Fatal("keygen must refuse to write a private key through a dangling symlink")
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("keygen followed the symlink and wrote the key at the target")
	}
	// A plain existing file is still refused.
	plain := filepath.Join(dir, "exists.key")
	os.WriteFile(plain, []byte("x"), 0o600)
	if err := allowlistKeygen([]string{"--out", plain}); err == nil {
		t.Fatal("keygen must refuse to overwrite an existing file")
	}
}
