package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"smith/internal/config"
)

var semverRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// parseSemver parses a strict vX.Y.Z (optional leading v) tag.
func parseSemver(t string) ([3]int, bool) {
	var v [3]int
	if !semverRe.MatchString(t) {
		return v, false
	}
	for i, s := range strings.Split(strings.TrimPrefix(t, "v"), ".") {
		n, err := strconv.Atoi(s)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// semverLess reports whether tag a sorts before tag b (numeric per component).
// A non-semver tag sorts before a semver one.
func semverLess(a, b string) bool {
	pa, oka := parseSemver(a)
	pb, okb := parseSemver(b)
	if oka != okb {
		return !oka
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

// latestTag returns the highest vX.Y.Z semver tag advertised by the remote (or
// local repo path). Pre-release / non-semver tags are ignored.
func latestTag(clonedURL string) (string, error) {
	out, err := exec.Command("git", "ls-remote", "--tags", clonedURL).Output()
	if err != nil {
		return "", fmt.Errorf("listing tags for %s: %w", clonedURL, err)
	}
	best := ""
	for _, line := range strings.Split(string(out), "\n") {
		i := strings.Index(line, "refs/tags/")
		if i < 0 {
			continue
		}
		tag := strings.TrimSpace(strings.TrimSuffix(line[i+len("refs/tags/"):], "^{}"))
		if _, ok := parseSemver(tag); !ok {
			continue
		}
		if best == "" || semverLess(best, tag) {
			best = tag
		}
	}
	if best == "" {
		return "", fmt.Errorf("no semver (vX.Y.Z) tags on %s — pin an explicit @<version>", clonedURL)
	}
	return best, nil
}

func cmdUpgrade(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	// Accept an optional tool[@ver] positional before OR after flags.
	var spec string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		spec, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if spec == "" && fs.NArg() == 1 {
		spec = fs.Arg(0)
	}
	lock, err := loadProjLock()
	if err != nil {
		return err
	}
	if len(lock.Tools) == 0 {
		return fmt.Errorf("no adopted tools (%s missing/empty) — `smith use <repo>` to adopt one", lockPath())
	}

	var targetName, targetVer string
	if spec != "" {
		targetName, targetVer = splitSpec(spec)
	}
	var names []string
	if targetName != "" {
		if _, ok := lock.Tools[targetName]; !ok {
			return fmt.Errorf("%q is not adopted in this project (see `smith list`)", targetName)
		}
		names = []string{targetName}
	} else {
		names = sortedToolNames(lock)
	}

	fc, ferr := p.LoadFileConfig()
	if ferr != nil {
		return fmt.Errorf("reading %s (fix or remove it): %w", filepath.Join(p.Home, "config.yaml"), ferr)
	}
	pats, perr := allowlistPatterns(fc)
	if perr != nil {
		return perr
	}

	type upEntry struct{ Tool, From, To string }
	type failEntry struct{ Tool, Error string }
	var upgraded []upEntry
	var uptodate []string
	var failed []failEntry
	var skippedExternal []string

	for _, name := range names {
		t := lock.Tools[name]
		if t.isMise() {
			// External tool: pinned in mise.toml, no vendored source to re-clone.
			// Skip it in the bulk sweep; if explicitly targeted, say how.
			if targetName != "" {
				return fmt.Errorf("%q is an external (mise) tool — upgrade it with `mise use %s@<ver>`, then `smith install`", name, strings.TrimPrefix(t.Source, "mise:"))
			}
			skippedExternal = append(skippedExternal, name)
			continue
		}
		want := targetVer
		if want == "" {
			latest, lerr := latestTag(t.Source)
			if lerr != nil {
				failed = append(failed, failEntry{name, lerr.Error()})
				continue
			}
			want = latest
		}
		if want == t.Version {
			uptodate = append(uptodate, name)
			continue
		}
		if len(pats) > 0 && !allowlistPermits(pats, t.Source, t.Source) {
			failed = append(failed, failEntry{name, "blocked by allowlist"})
			continue
		}
		// adoptAtVersion only writes the pin on success, so a failed self-test
		// (or clone/checkout) leaves the old pin + vendored source untouched.
		if _, aerr := adoptAtVersion(p, t.Source, t.Source, want); aerr != nil {
			failed = append(failed, failEntry{name, aerr.Error()})
			continue
		}
		upgraded = append(upgraded, upEntry{name, t.Version, want})
	}

	if *asJSON {
		if err := jsonPrint(map[string]any{
			"upgraded": upgraded, "uptodate": uptodate, "failed": failed,
			"skipped_external": skippedExternal,
		}); err != nil {
			return err
		}
		// Machine-readable mode must still exit nonzero on failure, so CI that
		// consumes the JSON doesn't stay green on a failed upgrade.
		if len(failed) > 0 {
			return fmt.Errorf("%d tool(s) failed to upgrade", len(failed))
		}
		return nil
	}
	for _, u := range upgraded {
		note := ""
		if semverLess(u.To, u.From) {
			note = "  (downgrade)"
		}
		fmt.Printf("upgraded %s: %s → %s%s\n", u.Tool, u.From, u.To, note)
	}
	for _, n := range uptodate {
		fmt.Printf("up to date: %s (%s)\n", n, lock.Tools[n].Version)
	}
	for _, n := range skippedExternal {
		fmt.Printf("skipped (external, via mise): %s — `mise upgrade %s`\n", n, strings.TrimPrefix(lock.Tools[n].Source, "mise:"))
	}
	for _, f := range failed {
		fmt.Fprintf(os.Stderr, "failed: %s — %s\n", f.Tool, f.Error)
	}
	if len(upgraded) > 0 {
		fmt.Println("\nreview the diff (`git diff .smith`), then `smith install`")
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d tool(s) failed to upgrade", len(failed))
	}
	return nil
}

func cmdDiff(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	stat := fs.Bool("stat", false, "summary (files changed)")
	asJSON := fs.Bool("json", false, "machine-readable numstat")
	// Accept the tool[@ver] positional before OR after flags.
	var spec string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		spec, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if spec == "" && fs.NArg() == 1 {
		spec = fs.Arg(0)
	}
	if spec == "" {
		return fmt.Errorf("usage: smith diff <tool>[@<version>] [--stat|--json]")
	}
	name, ver := splitSpec(spec)
	lock, err := loadProjLock()
	if err != nil {
		return err
	}
	t, ok := lock.Tools[name]
	if !ok {
		return fmt.Errorf("%q is not adopted in this project (see `smith list`)", name)
	}
	if t.isMise() {
		return fmt.Errorf("%q is an external (mise) tool — no source is vendored to diff", name)
	}
	if ver == "" {
		latest, lerr := latestTag(t.Source)
		if lerr != nil {
			return lerr
		}
		ver = latest
	}
	vendored := filepath.Join(smithDir, "tools", name)
	if _, err := os.Stat(vendored); err != nil {
		return fmt.Errorf("no vendored source at %s", vendored)
	}

	// Clone the target and mirror just the reviewable source files, so the diff
	// is apples-to-apples with the vendored tree (no repo noise).
	tmp, err := os.MkdirTemp("", "smith-diff-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	clone := filepath.Join(tmp, "repo")
	if err := gitClone(t.Source, clone); err != nil {
		return err
	}
	co := exec.Command("git", "-C", clone, "checkout", "--quiet", "--end-of-options", ver)
	co.Stderr = os.Stderr
	if err := co.Run(); err != nil {
		return fmt.Errorf("checkout %s: %w", ver, err)
	}
	target := filepath.Join(tmp, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	for _, f := range sourceFiles(t.Runtime, t.Entrypoint, name) {
		raw, rerr := os.ReadFile(filepath.Join(clone, f))
		if os.IsNotExist(rerr) {
			continue
		}
		if rerr != nil {
			return rerr
		}
		// Match the modes adoptAtVersion vendors with, so an unchanged file
		// isn't a spurious mode-only diff (entrypoint + selftest are 0755).
		mode := os.FileMode(0o644)
		if f == t.Entrypoint || f == "selftest.sh" {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(target, f), raw, mode); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "[smith] diff %s: %s → %s\n", name, t.Version, ver)
	gitArgs := []string{"diff", "--no-index"}
	switch {
	case *asJSON:
		gitArgs = append(gitArgs, "--numstat")
	case *stat:
		gitArgs = append(gitArgs, "--stat")
	}
	gitArgs = append(gitArgs, vendored, target)
	// `git diff --no-index` exits 1 when there are differences — that's not an
	// error for us, and stdout still carries the diff.
	out, _ := exec.Command("git", gitArgs...).Output()
	if *asJSON {
		return diffNumstatJSON(string(out), name, t.Version, ver)
	}
	if len(out) == 0 {
		fmt.Printf("%s: no source changes between %s and %s\n", name, t.Version, ver)
		return nil
	}
	fmt.Print(string(out))
	return nil
}

// diffNumstatJSON turns `git diff --numstat` output into a structured summary.
func diffNumstatJSON(numstat, name, from, to string) error {
	type change struct {
		File    string `json:"file"`
		Added   int    `json:"added"`
		Deleted int    `json:"deleted"`
	}
	var changes []change
	for _, line := range strings.Split(strings.TrimSpace(numstat), "\n") {
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		add, _ := strconv.Atoi(f[0])
		del, _ := strconv.Atoi(f[1])
		changes = append(changes, change{File: filepath.Base(f[len(f)-1]), Added: add, Deleted: del})
	}
	return jsonPrint(map[string]any{"tool": name, "from": from, "to": to, "changes": changes})
}
