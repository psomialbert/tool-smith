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
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"smith/internal/config"
	"smith/internal/sandbox"
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

// metaParts are the non-source bundle files every tool carries.
var metaParts = []string{"manifest.yaml", "selftest.sh", "SKILL.md"}

// sourceFiles lists the files that make up a tool's *reviewable source* — what
// gets vendored (committed) and hashed. For compiled/wrapped runtimes this is
// the source, not the built artifact: go vendors main.go (built at install),
// python-uv vendors the script + its hash-lock (deps warmed at install).
func sourceFiles(runtime, entrypoint, name string) []string {
	var src []string
	switch runtime {
	case "go":
		src = []string{"main.go"}
	case "python-uv":
		src = []string{name + ".py", name + ".py.lock"}
	default:
		src = []string{entrypoint}
	}
	return append(src, metaParts...)
}

// bundleHash hashes a set of files deterministically: sorted "<rel>\0<sha>\n".
func bundleHash(dir string, files []string) (string, error) {
	fs := append([]string(nil), files...)
	sort.Strings(fs)
	h := sha256.New()
	for _, f := range fs {
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

// buildGoTool compiles dir/main.go into outAbs, offline (GOPROXY=off) and
// without cgo. The Go toolchain is trusted; review reads the vendored main.go.
func buildGoTool(dir, outAbs string) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go toolchain not found on PATH")
	}
	cmd := exec.Command("go", "build", "-trimpath", "-o", outAbs, "main.go")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOPROXY=off", "GOFLAGS=-mod=mod")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

// uvLockScript runs `uv lock --script` if the lockfile is absent, so the
// vendored source carries a hash-locked dependency tree.
func uvLockScript(dir, scriptRel string) error {
	if _, err := os.Stat(filepath.Join(dir, scriptRel+".lock")); err == nil {
		return nil
	}
	cmd := exec.Command("uv", "lock", "--script", scriptRel)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

// warmUVCache populates cacheAbs by resolving+installing the script's locked
// deps, in a NETWORK-allowed sandbox with a sanitized env and writes confined
// to workdir — so dep download/build can't reach secrets. cacheAbs must sit
// under workdir (the sandbox's writable CWD).
func warmUVCache(p config.Paths, workdir, scriptAbs, cacheAbs string) error {
	argv, err := sandbox.Command(p.SandboxProfiles, workdir, sandbox.Perms{Network: true},
		[]string{"uv", "run", "--script", scriptAbs, "--version"})
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = workdir
	env, err := sandbox.Env(nil)
	if err != nil {
		return err
	}
	cmd.Env = append(env, "UV_CACHE_DIR="+cacheAbs)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

// pythonUVLauncher runs a PEP 723 script offline from a tool-local cache. It
// mirrors the cache to a writable temp first (a sandboxed run has the cache
// read-only, and uv writes to its cache at run). Absolute paths are baked in
// at generation time (regenerated each install / per self-test).
func pythonUVLauncher(scriptAbs, cacheAbs string) string {
	return "#!/bin/bash\nset -e\n" +
		"cache=\"$(mktemp -d)/c\"\n" +
		"cp -R " + shellQuote(cacheAbs) + " \"$cache\"\n" +
		"export UV_CACHE_DIR=\"$cache\"\n" +
		"exec uv run --offline --script " + shellQuote(scriptAbs) + " \"$@\"\n"
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

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

// allowlistPatterns returns the org's permitted-source patterns: config
// allowlist.repos plus lines from allowlist.file. Empty = no policy.
func allowlistPatterns(fc config.FileConfig) ([]string, error) {
	pats := append([]string(nil), fc.Allowlist.Repos...)
	if fc.Allowlist.File != "" {
		raw, err := os.ReadFile(fc.Allowlist.File)
		if err != nil {
			return nil, fmt.Errorf("reading allowlist file %s: %w", fc.Allowlist.File, err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				pats = append(pats, line)
			}
		}
	}
	return pats, nil
}

// allowlistPermits reports whether source (or its expanded URL) matches any
// pattern. Matching is glob (path.Match) against the raw arg, the expanded
// URL, and the scheme/.git-stripped form, so "github.com/acme/*" matches
// "github:acme/x", "https://github.com/acme/x", and ".../acme/x.git".
func allowlistPermits(pats []string, source, expanded string) bool {
	cands := map[string]bool{}
	for _, c := range []string{source, expanded} {
		cands[c] = true
		c = strings.TrimSuffix(c, ".git")
		cands[c] = true
		if i := strings.Index(c, "://"); i >= 0 {
			cands[c[i+3:]] = true
		}
		cands[strings.TrimPrefix(c, "github:")] = true
	}
	for _, p := range pats {
		for c := range cands {
			if p == c {
				return true
			}
			if ok, _ := path.Match(p, c); ok {
				return true
			}
		}
	}
	return false
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

	// Org policy: refuse (before cloning) sources the allowlist doesn't permit.
	fc, _ := p.LoadFileConfig()
	if pats, perr := allowlistPatterns(fc); perr != nil {
		return perr
	} else if len(pats) > 0 && !allowlistPermits(pats, source, clonedURL) {
		return fmt.Errorf("adoption blocked: %q matches no allowlist pattern.\n  allowed: %s\n  (policy in %s)",
			source, strings.Join(pats, ", "), filepath.Join(p.Home, "config.yaml"))
	}

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
	switch m.Runtime {
	case "bash", "python", "go", "python-uv":
	default:
		return fmt.Errorf("runtime %q not yet supported by `smith use` (bash/python/go/python-uv)", m.Runtime)
	}

	// Compiled/wrapped runtimes materialize a runnable before the self-test.
	entry := filepath.Join(clone, m.Entrypoint)
	switch m.Runtime {
	case "go":
		if err := buildGoTool(clone, entry); err != nil {
			return fmt.Errorf("go build failed — not adopted: %w", err)
		}
	case "python-uv":
		if _, err := exec.LookPath("uv"); err != nil {
			return fmt.Errorf("uv not found on PATH (required for python-uv tools)")
		}
		script := m.Name + ".py"
		if err := uvLockScript(clone, script); err != nil {
			return fmt.Errorf("uv lock failed — not adopted: %w", err)
		}
		cacheAbs, _ := filepath.Abs(filepath.Join(clone, "uv-cache"))
		scriptAbs, _ := filepath.Abs(filepath.Join(clone, script))
		if err := warmUVCache(p, clone, scriptAbs, cacheAbs); err != nil {
			return fmt.Errorf("warming uv cache failed — not adopted: %w", err)
		}
		// Self-test runs the offline launcher (mirrors cache, no network).
		entry = filepath.Join(clone, ".smith-launch")
		if err := os.WriteFile(entry, []byte(pythonUVLauncher(scriptAbs, cacheAbs)), 0o755); err != nil {
			return err
		}
	}
	if err := os.Chmod(entry, 0o755); err != nil {
		return fmt.Errorf("entrypoint %s: %w", m.Entrypoint, err)
	}

	// Sandboxed self-test before vendoring: unreviewed code stays contained
	// at adopt time; a failing self-test blocks adoption.
	fmt.Fprintf(os.Stderr, "[smith] self-testing %s@%s (sandboxed) before adopting...\n", m.Name, version)
	if err := runSelfTest(p, clone, entry); err != nil {
		return fmt.Errorf("self-test FAILED — not adopted: %w", err)
	}

	// Vendor the reviewable SOURCE (not built artifacts) into the project.
	src := sourceFiles(m.Runtime, m.Entrypoint, m.Name)
	dst := filepath.Join(smithDir, "tools", m.Name)
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, f := range src {
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
	sum, err := bundleHash(dst, src)
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
	absBinEarly, err := filepath.Abs(binDir)
	if err != nil {
		return err
	}
	for _, name := range names {
		t := lock.Tools[name]
		dir := filepath.Join(smithDir, "tools", name)
		// Integrity: vendored source must match the pinned hash (catches a
		// tampered/edited vendored tree vs the reviewed lockfile).
		src := sourceFiles(t.Runtime, t.Entrypoint, name)
		sum, herr := bundleHash(dir, src)
		if herr != nil {
			return fmt.Errorf("%s: %w", name, herr)
		}
		if sum != t.Sha256 {
			return fmt.Errorf("%s: vendored source sha mismatch (pinned %s, got %s) — review %s and re-adopt", name, t.Sha256[:12], sum[:12], dir)
		}
		link := filepath.Join(binDir, name)
		_ = os.Remove(link)
		// Materialize the runnable per runtime.
		switch t.Runtime {
		case "go":
			// Build the vendored source locally (per-platform, git-ignored).
			if err := buildGoTool(dir, filepath.Join(absBinEarly, name)); err != nil {
				return fmt.Errorf("%s: go build failed: %w", name, err)
			}
		case "python-uv":
			// Warm a git-ignored tool-local cache from the hash-locked deps,
			// then write an offline launcher onto the bin.
			if _, err := exec.LookPath("uv"); err != nil {
				return fmt.Errorf("%s: uv not found on PATH", name)
			}
			scriptAbs, _ := filepath.Abs(filepath.Join(dir, name+".py"))
			cacheAbs, _ := filepath.Abs(filepath.Join(smithDir, "cache", name))
			if err := os.MkdirAll(cacheAbs, 0o755); err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			if err := warmUVCache(p, cwd, scriptAbs, cacheAbs); err != nil {
				return fmt.Errorf("%s: warming uv cache failed: %w", name, err)
			}
			if err := os.WriteFile(link, []byte(pythonUVLauncher(scriptAbs, cacheAbs)), 0o755); err != nil {
				return err
			}
		default: // bash / python — the entrypoint IS the script
			if err := os.Chmod(filepath.Join(dir, t.Entrypoint), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(filepath.Join("..", "tools", name, t.Entrypoint), link); err != nil {
				return err
			}
		}
	}
	if err := ensureActivation(".", absBinEarly); err != nil {
		return err
	}
	// .smith/bin is generated; keep it out of the repo (the vendored source
	// and lockfile are what's committed/reviewed).
	ensureProjectGitignore()
	if err := exec.Command("mise", "trust").Run(); err != nil {
		fmt.Fprintln(os.Stderr, "[smith] note: `mise trust` failed; run it in this dir")
	}

	// Adopted tools are trusted for this project, so export their SKILL.md so
	// agent sessions discover them (config skills.export: always|never).
	fc, _ := p.LoadFileConfig()
	if fc.Skills.Export != "never" {
		for _, name := range names {
			src := filepath.Join(smithDir, "tools", name, "SKILL.md")
			if _, err := os.Stat(src); err != nil {
				continue
			}
			dst := filepath.Join(fc.SkillsDir(), name, "SKILL.md")
			if err := copyPlain(src, dst); err != nil {
				fmt.Fprintf(os.Stderr, "[smith] skill export failed for %s: %v\n", name, err)
			}
		}
	}

	fmt.Printf("installed %d tool(s) on PATH for this project (via %s)\n", len(names), binDir)
	fmt.Printf("  %s\n", strings.Join(names, ", "))
	fmt.Println("activation needs mise in your shell (`mise activate`) or `mise exec`")
	return nil
}

// jsonPrint writes v as indented JSON to stdout.
func jsonPrint(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func ensureProjectGitignore() {
	gi := ".gitignore"
	raw, _ := os.ReadFile(gi)
	content := string(raw)
	has := func(want string) bool {
		for _, l := range strings.Split(content, "\n") {
			if strings.TrimSpace(l) == want {
				return true
			}
		}
		return false
	}
	// .smith/bin (symlinks + built binaries) and .smith/cache (warmed uv
	// caches) are generated per machine; the vendored source + lockfile are
	// what's committed.
	for _, want := range []string{smithDir + "/bin/", smithDir + "/cache/"} {
		if !has(want) {
			if content != "" && !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			content += want + "\n"
		}
	}
	if content != string(raw) {
		_ = os.WriteFile(gi, []byte(content), 0o644)
	}
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
