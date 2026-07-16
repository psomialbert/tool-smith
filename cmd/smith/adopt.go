package main

// smith v2 adoption model: a tool is a git repo (source + manifest +
// selftest + SKILL.md). You *adopt* it into a project by vendoring its source
// under .smith/tools/<name>/ and pinning it in .smith/tools.yaml — a
// reviewable commit, like any dependency. `smith install` realizes that
// lockfile onto the project's PATH. Discovery is a GitHub topic (smith-tool)
// via `smith search`.
//
// Trust in this model is per-project: a tool vendored+pinned in the (reviewed)
// lockfile is trusted for that project. `smith use` runs the tool's self-test
// sandboxed before vendoring, so unreviewed code is still contained at adopt
// time; the human reviews the vendored source in the PR.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"smith/internal/config"
	"smith/internal/scaffold"
)

const (
	smithDir     = ".smith"
	lockFileName = "tools.yaml" // under .smith/
)

// ProjTool is one adopted tool pinned in the project lockfile.
type ProjTool struct {
	Source     string `yaml:"source"`
	Version    string `yaml:"version"`
	Sha256     string `yaml:"sha256"` // hash of the vendored bundle
	Entrypoint string `yaml:"entrypoint"`
	Runtime    string `yaml:"runtime"`
}

// ProjLock is .smith/tools.yaml — the project's adopted-tools manifest.
type ProjLock struct {
	Tools map[string]ProjTool `yaml:"tools"`
}

func lockPath() string { return filepath.Join(smithDir, lockFileName) }

func loadProjLock() (ProjLock, error) {
	var l ProjLock
	raw, err := os.ReadFile(lockPath())
	if os.IsNotExist(err) {
		l.Tools = map[string]ProjTool{}
		return l, nil
	}
	if err != nil {
		return l, err
	}
	if err := yaml.Unmarshal(raw, &l); err != nil {
		return l, fmt.Errorf("parsing %s: %w", lockPath(), err)
	}
	if l.Tools == nil {
		l.Tools = map[string]ProjTool{}
	}
	return l, nil
}

func saveProjLock(l ProjLock) error {
	if err := os.MkdirAll(smithDir, 0o755); err != nil {
		return err
	}
	raw, err := yaml.Marshal(l)
	if err != nil {
		return err
	}
	return os.WriteFile(lockPath(), raw, 0o644)
}

// bundleFilesFor lists the tool-bundle files (that carry integrity), relative
// to a bundle dir. Only these are vendored + hashed.
var bundleParts = []string{"manifest.yaml", "selftest.sh", "SKILL.md"}

// bundleHash hashes a tool bundle deterministically: sorted "<rel>\0<sha>\n".
// It covers the entrypoint plus the standard bundle parts that exist.
func bundleHash(dir, entrypoint string) (string, error) {
	files := append([]string{entrypoint}, bundleParts...)
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		fh := sha256.Sum256(data)
		fmt.Fprintf(h, "%s\x00%s\n", f, hex.EncodeToString(fh[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// expandSource turns a shorthand into a cloneable git URL. Local paths and
// explicit URLs pass through; "owner/repo" and "github:owner/repo" become
// GitHub HTTPS.
func expandSource(s string) string {
	if strings.HasPrefix(s, "github:") {
		return "https://github.com/" + strings.TrimPrefix(s, "github:")
	}
	if strings.Contains(s, "://") || strings.HasPrefix(s, "git@") ||
		strings.HasPrefix(s, ".") || strings.HasPrefix(s, "/") {
		return s
	}
	// bare owner/repo (exactly one slash, no scheme) -> GitHub
	if strings.Count(s, "/") == 1 {
		return "https://github.com/" + s
	}
	return s
}

// ---------------------------------------------------------------- smith use

func cmdUse(p config.Paths, args []string) error {
	var version string
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--version" && i+1 < len(args) {
			version = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: smith use <repo|path>[@<version>] [--version <v>]")
	}
	source := rest[0]
	// Allow trailing @ref when the source isn't a scheme/auth URL.
	if version == "" && !strings.Contains(source, "://") && !strings.Contains(source, "git@") {
		if at := strings.LastIndex(source, "@"); at > 0 {
			version, source = source[at+1:], source[:at]
		}
	}
	clonedURL := expandSource(source)

	tmp, err := os.MkdirTemp("", "smith-use-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	clone := filepath.Join(tmp, "repo")
	cc := exec.Command("git", "clone", "--quiet", clonedURL, clone)
	cc.Stdout, cc.Stderr = os.Stderr, os.Stderr
	if err := cc.Run(); err != nil {
		return fmt.Errorf("cloning %s: %w", clonedURL, err)
	}
	if version != "" {
		co := exec.Command("git", "-C", clone, "checkout", "--quiet", version)
		co.Stdout, co.Stderr = os.Stderr, os.Stderr
		if err := co.Run(); err != nil {
			return fmt.Errorf("checkout %s: %w", version, err)
		}
	} else {
		out, _ := exec.Command("git", "-C", clone, "describe", "--tags", "--always").Output()
		version = strings.TrimSpace(string(out))
	}

	m, err := scaffold.LoadManifest(clone)
	if err != nil {
		return fmt.Errorf("%s is not a smith tool (no valid manifest.yaml): %w", source, err)
	}
	if m.Runtime != "bash" && m.Runtime != "python" {
		return fmt.Errorf("runtime %q not yet supported by `smith use` (bash/python only for now)", m.Runtime)
	}
	entry := filepath.Join(clone, m.Entrypoint)
	if err := os.Chmod(entry, 0o755); err != nil {
		return fmt.Errorf("entrypoint %s: %w", m.Entrypoint, err)
	}

	// Sandboxed self-test before vendoring: unreviewed code stays contained
	// at adopt time; a failing self-test blocks adoption.
	fmt.Fprintf(os.Stderr, "[smith] self-testing %s@%s (sandboxed) before adopting...\n", m.Name, version)
	if err := runSelfTest(p, clone, entry); err != nil {
		return fmt.Errorf("self-test FAILED — not adopted: %w", err)
	}

	// Vendor the bundle into the project.
	dst := filepath.Join(smithDir, "tools", m.Name)
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, f := range append([]string{m.Entrypoint}, bundleParts...) {
		raw, rerr := os.ReadFile(filepath.Join(clone, f))
		if os.IsNotExist(rerr) {
			continue
		}
		if rerr != nil {
			return rerr
		}
		mode := os.FileMode(0o644)
		if f == m.Entrypoint || f == "selftest.sh" {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dst, f), raw, mode); err != nil {
			return err
		}
	}
	sum, err := bundleHash(dst, m.Entrypoint)
	if err != nil {
		return err
	}

	lock, err := loadProjLock()
	if err != nil {
		return err
	}
	lock.Tools[m.Name] = ProjTool{
		Source: clonedURL, Version: version, Sha256: sum,
		Entrypoint: m.Entrypoint, Runtime: m.Runtime,
	}
	if err := saveProjLock(lock); err != nil {
		return err
	}

	fmt.Printf("adopted %s@%s into %s/\n", m.Name, version, smithDir)
	fmt.Printf("  source: %s\n  vendored: %s  (sha256=%s)\n", clonedURL, dst, sum[:12])
	fmt.Printf(`
This is a reviewable change — the tool's source is now in your project:
  git add %s && git commit    # the adoption decision, for your team to review
then run `+"`smith install`"+` to put it on PATH for this project.
`, smithDir)
	return nil
}

// ---------------------------------------------------------------- smith install

func cmdInstall(p config.Paths, args []string) error {
	lock, err := loadProjLock()
	if err != nil {
		return err
	}
	if len(lock.Tools) == 0 {
		return fmt.Errorf("no adopted tools (%s missing/empty) — `smith use <repo>` to adopt one", lockPath())
	}
	binDir := filepath.Join(smithDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	names := make([]string, 0, len(lock.Tools))
	for n := range lock.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		t := lock.Tools[name]
		dir := filepath.Join(smithDir, "tools", name)
		// Integrity: vendored bytes must match the pinned hash (catches a
		// tampered/edited vendored tree vs the reviewed lockfile).
		sum, herr := bundleHash(dir, t.Entrypoint)
		if herr != nil {
			return fmt.Errorf("%s: %w", name, herr)
		}
		if sum != t.Sha256 {
			return fmt.Errorf("%s: vendored source sha mismatch (pinned %s, got %s) — review %s and re-adopt", name, t.Sha256[:12], sum[:12], dir)
		}
		if err := os.Chmod(filepath.Join(dir, t.Entrypoint), 0o755); err != nil {
			return err
		}
		link := filepath.Join(binDir, name)
		target := filepath.Join("..", "tools", name, t.Entrypoint) // relative
		_ = os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			return err
		}
	}
	absBin, err := filepath.Abs(binDir)
	if err != nil {
		return err
	}
	if err := ensureActivation(".", absBin); err != nil {
		return err
	}
	// .smith/bin is generated; keep it out of the repo (the vendored source
	// and lockfile are what's committed/reviewed).
	ensureProjectGitignore()
	if err := exec.Command("mise", "trust").Run(); err != nil {
		fmt.Fprintln(os.Stderr, "[smith] note: `mise trust` failed; run it in this dir")
	}
	fmt.Printf("installed %d tool(s) on PATH for this project (via %s)\n", len(names), binDir)
	fmt.Printf("  %s\n", strings.Join(names, ", "))
	fmt.Println("activation needs mise in your shell (`mise activate`) or `mise exec`")
	return nil
}

func ensureProjectGitignore() {
	gi := ".gitignore"
	want := smithDir + "/bin/"
	raw, _ := os.ReadFile(gi)
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == want {
			return
		}
	}
	content := string(raw)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += want + "\n"
	_ = os.WriteFile(gi, []byte(content), 0o644)
}

// ---------------------------------------------------------------- smith search

func cmdSearch(p config.Paths, args []string) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("smith search needs the gh CLI")
	}
	query := strings.Join(args, " ")
	ghArgs := []string{"search", "repos", "--topic", "smith-tool",
		"--limit", "30", "--json", "fullName,description,url"}
	if query != "" {
		ghArgs = append(ghArgs, query)
	}
	cmd := exec.Command("gh", ghArgs...)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("gh search failed: %w", err)
	}
	var repos []struct {
		FullName    string `json:"fullName"`
		Description string `json:"description"`
		URL         string `json:"url"`
	}
	if err := json.Unmarshal(out, &repos); err != nil {
		return err
	}
	if len(repos) == 0 {
		fmt.Println("no tools found (repos tagged with the `smith-tool` GitHub topic)")
		fmt.Println("adopt one with: smith use <owner/repo>[@<version>]")
		return nil
	}
	for _, r := range repos {
		fmt.Printf("%-40s %s\n", r.FullName, r.Description)
		fmt.Printf("  adopt: smith use %s\n", r.FullName)
	}
	fmt.Println("\nfound ≠ trusted — `smith use` vendors + self-tests; you review the diff before committing.")
	return nil
}
