// Package attest performs external-tool attestation checks (§8 Phase 3).
// It verifies GitHub native attestations (SLSA provenance) over the exact
// downloaded archive via `gh attestation verify`. The deeper cosign/SLSA
// checks defined in aqua registry entries are mise's job (§2) — install
// cosign/slsa-verifier and mise-aqua applies them during `mise install`.
package attest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Levels, strongest first. The level is recorded in the index entry and
// shown at promotion review; policy in `smith add` refuses TOFU-only
// installs unless explicitly allowed.
const (
	LevelAttested = "gh-attestation" // SLSA provenance verified against source repo
	LevelRegistry = "aqua-registry"  // curated registry with checksum verification (mise)
	LevelChecksum = "mise-checksum"  // backend records a download checksum in mise.lock
	LevelTOFU     = "tofu"           // no upstream integrity; only smith's own first-use pin
)

// checksumBackends are the mise backends that record a download checksum in
// mise.lock (per mise's backend support matrix). Tools from these can be
// pin-verified on re-fetch, so they are allowed without --allow-unverified.
// Everything else (asdf/cargo/go/npm/pipx/vfox — version-only) stays TOFU.
var checksumBackends = map[string]bool{
	"ubi": true, "github": true, "gitlab": true, "http": true,
}

// GitHubRepo extracts owner/repo from a backend spec like "aqua:cli/cli" or
// "ubi:BurntSushi/ripgrep". Empty when the spec has no GitHub coordinates.
func GitHubRepo(backendSpec string) string {
	_, rest, ok := strings.Cut(backendSpec, ":")
	if !ok {
		return ""
	}
	rest = strings.TrimSuffix(rest, "/")
	if parts := strings.Split(rest, "/"); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return rest
	}
	return ""
}

// BackendLevel maps a backend spec to its baseline verification level.
func BackendLevel(backendSpec string) string {
	kind, _, _ := strings.Cut(backendSpec, ":")
	switch {
	case kind == "aqua":
		return LevelRegistry
	case checksumBackends[kind]:
		return LevelChecksum
	default:
		return LevelTOFU // asdf/cargo/go/npm/pipx/vfox: version-only, no integrity
	}
}

// Allowed reports whether a level clears the add-time policy without
// --allow-unverified: anything with upstream integrity does; bare TOFU
// does not.
func Allowed(level string) bool { return level != LevelTOFU }

type Result struct {
	Level         string `json:"level"`
	Detail        string `json:"detail,omitempty"`
	PredicateType string `json:"predicate_type,omitempty"`
}

// VerifyArchives attempts `gh attestation verify` on each archive in
// downloadDir against repo. First success wins. Returns found=false when
// nothing could be attested (no gh, no repo, no archives, no attestations) —
// callers fall back to the backend level; attestation absence is not an
// error because tampered and never-attested are indistinguishable here
// (digest lookup), and the checksum layer already covers mismatch.
func VerifyArchives(downloadDir, repo string) (Result, bool) {
	if repo == "" {
		return Result{}, false
	}
	if _, err := exec.LookPath("gh"); err != nil {
		fmt.Fprintln(os.Stderr, "[smith] gh CLI not found — skipping attestation check")
		return Result{}, false
	}
	entries, err := os.ReadDir(downloadDir)
	if err != nil {
		return Result{}, false
	}
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		archive := filepath.Join(downloadDir, de.Name())
		cmd := exec.Command("gh", "attestation", "verify", archive, "--repo", repo, "--format", "json")
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			continue
		}
		res := Result{
			Level:  LevelAttested,
			Detail: fmt.Sprintf("repo=%s archive=%s", repo, de.Name()),
		}
		var payload []struct {
			VerificationResult struct {
				Statement struct {
					PredicateType string `json:"predicateType"`
				} `json:"statement"`
			} `json:"verificationResult"`
		}
		if json.Unmarshal(out.Bytes(), &payload) == nil && len(payload) > 0 {
			res.PredicateType = payload[0].VerificationResult.Statement.PredicateType
		}
		return res, true
	}
	return Result{}, false
}
