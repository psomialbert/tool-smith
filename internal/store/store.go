// Package store manages the on-disk binary store. Layout:
//
//	store/trusted/<name>/<version>/...   Tier 1 only
//	store/trusted/bin/<tool>             symlink farm; the ONLY dir on PATH
//	store/quarantine/<name>/<version>/   Tier 0 and 2; never on PATH
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func QuarantineDir(storeQuarantine, name, version string) string {
	return filepath.Join(storeQuarantine, name, version)
}

func TrustedDir(storeTrusted, name, version string) string {
	return filepath.Join(storeTrusted, name, version)
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CopyExecutables copies regular executable files from srcDir into dstDir
// (flat), following symlinks, and returns their names. This is how external
// installs are pulled out of mise's store into quarantine.
func CopyExecutables(srcDir, dstDir string) ([]string, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}
	var copied []string
	for _, de := range entries {
		src := filepath.Join(srcDir, de.Name())
		info, err := os.Stat(src) // follows symlinks
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		if err := copyFile(src, filepath.Join(dstDir, de.Name()), info.Mode().Perm()); err != nil {
			return nil, err
		}
		copied = append(copied, de.Name())
	}
	return copied, nil
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// pinsFile is the per-machine record of an external tool's extracted-binary
// hashes, kept inside the (git-ignored) store rather than the signed index,
// because external bytes differ per platform and a fetch-only machine may not
// hold the signing key. Its authenticity chains back to the archive checksum
// verified against the signed mise.lock at fetch time.
const pinsFile = ".smithpins.json"

type LocalPin struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256"`
}

func WritePins(versionDir string, pins []LocalPin) error {
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(pins, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(versionDir, pinsFile), append(raw, '\n'), 0o644)
}

// ReadPins returns the local pins for a version dir, or (nil, false) if none
// were recorded (e.g. the tool was added natively with index pins instead).
func ReadPins(versionDir string) ([]LocalPin, bool) {
	raw, err := os.ReadFile(filepath.Join(versionDir, pinsFile))
	if err != nil {
		return nil, false
	}
	var pins []LocalPin
	if json.Unmarshal(raw, &pins) != nil {
		return nil, false
	}
	return pins, true
}

// Move relocates a tool's version dir between quarantine and trusted.
func Move(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("destination already exists: %s", dst)
	}
	return os.Rename(src, dst)
}

// LinkBin creates store/trusted/bin/<artifact> -> ../<name>/<version>/<artifact>.
// Refuses to overwrite a symlink owned by a different tool.
func LinkBin(trustedBin, storeTrusted, name, version string, artifacts []string) error {
	for _, a := range artifacts {
		link := filepath.Join(trustedBin, filepath.Base(a))
		target := filepath.Join(storeTrusted, name, version, a)
		if existing, err := os.Readlink(link); err == nil {
			if existing == target {
				continue
			}
			expected := filepath.Join(storeTrusted, name) + string(filepath.Separator)
			if !filepath.HasPrefix(existing, expected) {
				return fmt.Errorf("bin conflict: %s already points to %s", link, existing)
			}
			if err := os.Remove(link); err != nil {
				return err
			}
		}
		if err := os.Symlink(target, link); err != nil {
			return err
		}
	}
	return nil
}

// UnlinkVersion removes every trusted-bin symlink that points into a tool's
// version dir, regardless of the artifact list. Used by sync-driven
// revocation, where the index entry may already be gone.
func UnlinkVersion(trustedBin, storeTrusted, name, version string) error {
	entries, err := os.ReadDir(trustedBin)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	prefix := filepath.Join(storeTrusted, name, version) + string(filepath.Separator)
	for _, de := range entries {
		link := filepath.Join(trustedBin, de.Name())
		target, err := os.Readlink(link)
		if err != nil {
			continue
		}
		if strings.HasPrefix(target, prefix) {
			if err := os.Remove(link); err != nil {
				return err
			}
		}
	}
	return nil
}

// InstalledVersions lists version dirs present under a tool root
// (store/trusted or store/quarantine), skipping the bin symlink farm.
func InstalledVersions(storeRoot string) (map[string][]string, error) {
	out := map[string][]string{}
	names, err := os.ReadDir(storeRoot)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, nd := range names {
		if !nd.IsDir() || nd.Name() == "bin" {
			continue
		}
		versions, err := os.ReadDir(filepath.Join(storeRoot, nd.Name()))
		if err != nil {
			return nil, err
		}
		for _, vd := range versions {
			if vd.IsDir() {
				out[nd.Name()] = append(out[nd.Name()], vd.Name())
			}
		}
	}
	return out, nil
}

// UnlinkBin removes the symlinks for a tool's artifacts, immediately taking
// it off PATH (demotion requirement §7).
func UnlinkBin(trustedBin, storeTrusted, name, version string, artifacts []string) error {
	for _, a := range artifacts {
		link := filepath.Join(trustedBin, filepath.Base(a))
		target := filepath.Join(storeTrusted, name, version, a)
		existing, err := os.Readlink(link)
		if err != nil {
			continue // already gone or not ours
		}
		if existing == target {
			if err := os.Remove(link); err != nil {
				return err
			}
		}
	}
	return nil
}
