package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"smith/internal/config"
)

// verifyAllowlistSig checks that <file> has a valid detached ed25519 signature
// at <file>.sig against the base64 public key. Any problem (bad key, missing or
// invalid signature) is an error — callers fail closed.
func verifyAllowlistSig(file, pubB64 string) error {
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid allowlist public_key (want base64 of a 32-byte ed25519 key)")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	sigRaw, err := os.ReadFile(file + ".sig")
	if err != nil {
		return fmt.Errorf("missing signature %s.sig (sign it with `smith allowlist sign`)", file)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigRaw)))
	if err != nil {
		return fmt.Errorf("malformed signature %s.sig", file)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), data, sig) {
		return fmt.Errorf("signature does not match %s (tampered, or wrong key)", file)
	}
	return nil
}

// allowlistKeygen generates an ed25519 keypair: the private key to a file, the
// public key printed for pasting into config.yaml.
func allowlistKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fs.String("out", "smith-allowlist.key", "private key output path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("%s already exists — refusing to overwrite a private key", *out)
	}
	if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Printf("private key → %s  (keep it secret; do NOT commit it)\n", *out)
	fmt.Printf("public key — put this in $SMITH_HOME/config.yaml:\n\n  allowlist:\n    public_key: %s\n\n", base64.StdEncoding.EncodeToString(pub))
	return nil
}

// allowlistSign writes a detached ed25519 signature <file>.sig using the private key.
func allowlistSign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	key := fs.String("key", "smith-allowlist.key", "private key path")
	// Accept the <file> positional before OR after flags.
	var file string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		file, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if file == "" && fs.NArg() == 1 {
		file = fs.Arg(0)
	}
	if file == "" {
		return fmt.Errorf("usage: smith allowlist sign <file> [--key <private-key>]")
	}
	privRaw, err := os.ReadFile(*key)
	if err != nil {
		return fmt.Errorf("reading private key %s (run `smith allowlist keygen`): %w", *key, err)
	}
	priv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(privRaw)))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("%s is not a valid smith allowlist private key", *key)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(ed25519.PrivateKey(priv), data)
	if err := os.WriteFile(file+".sig", []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("signed %s → %s.sig\n", file, file)
	return nil
}

// allowlistVerify reports the signature status of the configured allowlist.
func allowlistVerify(p config.Paths) error {
	fc, err := p.LoadFileConfig()
	if err != nil {
		return err
	}
	if fc.Allowlist.File == "" || fc.Allowlist.PublicKey == "" {
		fmt.Println("no signed allowlist configured (set allowlist.file + allowlist.public_key)")
		return nil
	}
	if err := verifyAllowlistSig(fc.Allowlist.File, fc.Allowlist.PublicKey); err != nil {
		return fmt.Errorf("allowlist signature INVALID: %w", err)
	}
	fmt.Printf("allowlist signature OK: %s verifies against the configured public_key\n", fc.Allowlist.File)
	return nil
}
