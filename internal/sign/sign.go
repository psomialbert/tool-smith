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
	Digest    string `json:"digest"`     // sha256 hex of the canonical manifest set
	Sig       string `json:"sig"`        // hex ed25519 signature over Digest (as bytes of the hex string)
	KeyFprint string `json:"key_fprint"` // sha256 hex of the public key, first 16 hex chars
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

func authorizedKeysPath(home string) string {
	return filepath.Join(home, "index", "authorized_keys")
}

// ParsePublicKeyArg accepts a hex ed25519 public key directly or a path to a
// file containing one (e.g. another machine's index/smith.pub).
func ParsePublicKeyArg(arg string) (ed25519.PublicKey, error) {
	s := strings.TrimSpace(arg)
	if raw, err := os.ReadFile(arg); err == nil {
		s = strings.TrimSpace(string(raw))
	}
	pub, err := hex.DecodeString(s)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("not a valid ed25519 public key (hex or file)")
	}
	return ed25519.PublicKey(pub), nil
}

// AuthorizedKey is a trusted signing key: its public key and an optional
// human label (machine name, "rotated 2026-07", ...).
type AuthorizedKey struct {
	Pub   ed25519.PublicKey
	Label string
}

func (a AuthorizedKey) Fingerprint() string { return Fingerprint(a.Pub) }

// LoadAuthorizedKeys returns every key allowed to sign the index: the entries
// in index/authorized_keys plus the primary smith.pub (so a single-key setup
// with no authorized_keys file still verifies). This is the trust set that
// lets a second machine publish and others verify it, and keeps an old key
// valid through a rotation window.
func LoadAuthorizedKeys(home string) ([]AuthorizedKey, error) {
	var keys []AuthorizedKey
	seen := map[string]bool{}
	addKey := func(pub ed25519.PublicKey, label string) {
		fp := Fingerprint(pub)
		if seen[fp] {
			return
		}
		seen[fp] = true
		keys = append(keys, AuthorizedKey{Pub: pub, Label: label})
	}
	if raw, err := os.ReadFile(authorizedKeysPath(home)); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			pub, err := hex.DecodeString(fields[0])
			if err != nil || len(pub) != ed25519.PublicKeySize {
				return nil, fmt.Errorf("corrupt entry in authorized_keys: %q", line)
			}
			label := ""
			if len(fields) > 1 {
				label = strings.Join(fields[1:], " ")
			}
			addKey(ed25519.PublicKey(pub), label)
		}
	}
	if pub, err := LoadPublicKey(home); err == nil {
		addKey(pub, "primary (smith.pub)")
	}
	return keys, nil
}

// AddAuthorizedKey appends a pubkey (with optional label) to
// index/authorized_keys if not already in the file. Dedup is against the
// file only (not the smith.pub fallback), so the outgoing key can be
// persisted during a rotation even while it still matches smith.pub.
func AddAuthorizedKey(home string, pub ed25519.PublicKey, label string) error {
	if raw, err := os.ReadFile(authorizedKeysPath(home)); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if b, derr := hex.DecodeString(strings.Fields(line)[0]); derr == nil &&
				Fingerprint(ed25519.PublicKey(b)) == Fingerprint(pub) {
				return nil
			}
		}
	}
	path := authorizedKeysPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line := hex.EncodeToString(pub)
	if label != "" {
		line += "  " + label
	}
	_, err = f.WriteString(line + "\n")
	return err
}

// RevokeAuthorizedKey removes keys whose fingerprint matches fp from
// authorized_keys. Returns how many were removed.
func RevokeAuthorizedKey(home, fp string) (int, error) {
	raw, err := os.ReadFile(authorizedKeysPath(home))
	if err != nil {
		return 0, err
	}
	var kept []string
	removed := 0
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			if line != "" {
				kept = append(kept, line)
			}
			continue
		}
		pub, derr := hex.DecodeString(strings.Fields(t)[0])
		if derr == nil && Fingerprint(ed25519.PublicKey(pub)) == fp {
			removed++
			continue
		}
		kept = append(kept, line)
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, os.WriteFile(authorizedKeysPath(home), []byte(strings.Join(kept, "\n")+"\n"), 0o644)
}

// Rotate generates a fresh signing keypair, keeps the outgoing public key
// authorized (so already-published signatures still verify during the
// window), installs the new key as primary, authorizes it, and returns its
// fingerprint. The caller re-signs (SignIndex) with the new key.
func Rotate(home string) (string, error) {
	privPath, pubPath := keyPaths(home)
	// Keep the outgoing key in the trust set before replacing it.
	if oldPub, err := LoadPublicKey(home); err == nil {
		if err := AddAuthorizedKey(home, oldPub, "rotated-out"); err != nil {
			return "", err
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(privPath), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(privPath, []byte(hex.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(pubPath, []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := AddAuthorizedKey(home, pub, "current"); err != nil {
		return "", err
	}
	return Fingerprint(pub), nil
}

// ManifestDigest computes the canonical digest: sha256 over
// "relpath\x00sha256(file)\n" for every regular file under indexTools
// (manifests and the per-tool mise.lock checksum records), sorted by path.
// Index content is data — the digest binds bytes, it never interprets them.
// (The public key itself is TOFU: it authenticates the set but cannot
// authenticate itself; verify its fingerprint out-of-band on new machines.)
func ManifestDigest(home, indexTools string) (string, error) {
	type item struct{ rel, sum string }
	var items []item
	err := filepath.WalkDir(indexTools, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
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
	// NOTE: authorized_keys is deliberately NOT in the digest. Folding it in
	// would invalidate every prior signature whenever a key is added or
	// rotated (breaking the rotation window), and it wouldn't stop an
	// attacker with index-repo write access anyway — they could add their key
	// AND re-sign. The trust set's integrity rests on the git repo's access
	// control plus the TOFU smith.pub root verified out-of-band on clone.
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
	// The signing key must be in the authorized set (primary smith.pub plus
	// any authorized_keys — other machines, rotated-out keys in their window).
	authorized, err := LoadAuthorizedKeys(home)
	if err != nil {
		return true, err
	}
	var pub ed25519.PublicKey
	for _, k := range authorized {
		if k.Fingerprint() == s.KeyFprint {
			pub = k.Pub
			break
		}
	}
	if pub == nil {
		return true, fmt.Errorf("index.sig signed by key %s, which is not authorized (check index/authorized_keys)", s.KeyFprint)
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
