// Package sign implements index signing (§5.3): an ed25519 signature over a
// canonical digest of the whole manifest set, written to index/index.sig.
// Self-contained (stdlib crypto) — smith is both signer and verifier for the
// private index; there is no third-party interop requirement in v1.
//
// Key model: one operator keypair. The private key lives ONLY in
// $SMITH_HOME/keys/ (git-ignored, 0600) on machines allowed to publish; the
// public key is committed into the index repo (trust-on-first-clone — verify
// its fingerprint out-of-band when setting up a new machine).
package sign

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Signature struct {
	Digest    string `json:"digest"`    // sha256 hex of the canonical manifest set
	Sig       string `json:"sig"`       // hex ed25519 signature over Digest (as bytes of the hex string)
	KeyFprint string `json:"key_fprint"`// sha256 hex of the public key, first 16 hex chars
	SignedAt  string `json:"signed_at"`
}

func keyPaths(home string) (priv, pub string) {
	return filepath.Join(home, "keys", "smith.key"), filepath.Join(home, "index", "smith.pub")
}

func Fingerprint(pubKey ed25519.PublicKey) string {
	h := sha256.Sum256(pubKey)
	return hex.EncodeToString(h[:])[:16]
}

// EnsureKey loads the operator keypair, generating one on first use.
func EnsureKey(home string) (ed25519.PrivateKey, error) {
	privPath, pubPath := keyPaths(home)
	if raw, err := os.ReadFile(privPath); err == nil {
		seed, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("corrupt private key at %s", privPath)
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(privPath), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(privPath, []byte(hex.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(pubPath, []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "[smith] generated signing key; fingerprint %s\n  private: %s (keep out of git — never leaves this machine unless you copy it)\n  public:  %s (committed with the index)\n",
		Fingerprint(pub), privPath, pubPath)
	return priv, nil
}

func LoadPublicKey(home string) (ed25519.PublicKey, error) {
	_, pubPath := keyPaths(home)
	raw, err := os.ReadFile(pubPath)
	if err != nil {
		return nil, fmt.Errorf("no public key at %s (nothing published yet?): %w", pubPath, err)
	}
	pub, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("corrupt public key at %s", pubPath)
	}
	return ed25519.PublicKey(pub), nil
}

// ManifestDigest computes the canonical digest: sha256 over
// "relpath\x00sha256(file)\n" for every .yaml under indexTools, sorted by
// path. Index text is data — the digest binds bytes, it never interprets
// them. (The public key itself is TOFU: it authenticates the set but cannot
// authenticate itself; verify its fingerprint out-of-band on new machines.)
func ManifestDigest(home, indexTools string) (string, error) {
	type item struct{ rel, sum string }
	var items []item
	err := filepath.WalkDir(indexTools, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(indexTools, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		items = append(items, item{filepath.ToSlash(rel), hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].rel < items[j].rel })
	h := sha256.New()
	for _, it := range items {
		fmt.Fprintf(h, "%s\x00%s\n", it.rel, it.sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sigPath(home string) string { return filepath.Join(home, "index", "index.sig") }

// SignIndex signs the current manifest set and writes index/index.sig.
func SignIndex(home, indexTools, signedAt string) (Signature, error) {
	priv, err := EnsureKey(home)
	if err != nil {
		return Signature{}, err
	}
	digest, err := ManifestDigest(home, indexTools)
	if err != nil {
		return Signature{}, err
	}
	s := Signature{
		Digest:    digest,
		Sig:       hex.EncodeToString(ed25519.Sign(priv, []byte(digest))),
		KeyFprint: Fingerprint(priv.Public().(ed25519.PublicKey)),
		SignedAt:  signedAt,
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return Signature{}, err
	}
	return s, os.WriteFile(sigPath(home), append(raw, '\n'), 0o644)
}

// VerifyIndex checks index.sig against the current manifest set.
// Returns (signaturePresent, error). error==nil && present means valid.
func VerifyIndex(home, indexTools string) (bool, error) {
	raw, err := os.ReadFile(sigPath(home))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	var s Signature
	if err := json.Unmarshal(raw, &s); err != nil {
		return true, fmt.Errorf("corrupt index.sig: %w", err)
	}
	pub, err := LoadPublicKey(home)
	if err != nil {
		return true, err
	}
	if fp := Fingerprint(pub); fp != s.KeyFprint {
		return true, fmt.Errorf("index.sig key fingerprint %s does not match smith.pub %s", s.KeyFprint, fp)
	}
	sig, err := hex.DecodeString(s.Sig)
	if err != nil {
		return true, fmt.Errorf("corrupt signature encoding")
	}
	if !ed25519.Verify(pub, []byte(s.Digest), sig) {
		return true, fmt.Errorf("index.sig signature INVALID")
	}
	digest, err := ManifestDigest(home, indexTools)
	if err != nil {
		return true, err
	}
	if digest != s.Digest {
		return true, fmt.Errorf("manifest set changed since last publish (or tampered): signed=%s current=%s — review changes and `smith publish`", s.Digest[:12], digest[:12])
	}
	return true, nil
}
