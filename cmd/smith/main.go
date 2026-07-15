// smith — thin convention + trust gate over mise (see toolsmith-spec.md).
//
// Phase 0 surface: init, add, list/status, run, promote (minimal), demote,
// verify. `new` and `publish` arrive in Phases 1–2.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"smith/internal/attest"
	"smith/internal/config"
	"smith/internal/index"
	"smith/internal/misex"
	"smith/internal/provenance"
	"smith/internal/reviewtui"
	"smith/internal/sandbox"
	"smith/internal/scaffold"
	"smith/internal/sign"
	"smith/internal/store"
	"smith/internal/transport"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	paths, err := config.Resolve()
	if err != nil {
		fatal(err)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "init":
		err = cmdInit(paths, args)
	case "add":
		err = cmdAdd(paths, args)
	case "list", "status":
		err = cmdList(paths, args)
	case "run":
		err = cmdRun(paths, args)
	case "review":
		err = cmdReview(paths, args)
	case "promote":
		err = cmdPromote(paths, args)
	case "demote":
		err = cmdDemote(paths, args)
	case "verify":
		err = cmdVerify(paths, args)
	case "new":
		err = cmdNew(paths, args)
	case "register":
		err = cmdRegister(paths, args)
	case "publish":
		err = cmdPublish(paths, args)
	case "fetch":
		err = cmdFetch(paths, args)
	case "sync":
		err = cmdSync(paths, args)
	case "gc":
		err = cmdGC(paths, args)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "smith: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "smith: error:", err)
	os.Exit(1)
}

func usage() {
	fmt.Print(`smith — agent tool store with a trust gate (thin wrapper over mise)

Usage:
  smith init                        set up SMITH_HOME + activate trusted store on PATH here
  smith add <name>[@<version>]      install external tool via mise -> Tier 2 quarantine
  smith list [--json]               list tools with version, tier, origin
  smith status [--json]             alias of list
  smith run <name>[@<ver>] [args]   run a tool (Tier 0/2 forced into sandbox)
  smith review <name>[@<ver>] [--plain|--edit|--paths] [--editor <cmd>]   interactive source review (TUI); comment lines -> agent report
  smith promote <name>[@<ver>]      human-gated: quarantine -> trusted (Tier 1, on PATH)
  smith demote <name>[@<ver>] --reason <why>   trusted -> quarantine, off PATH now
  smith verify [--json]             re-check sha256 pins + provenance chain + PATH hygiene
  smith new <name> [--runtime bash|python] [--desc <text>]
                                    scaffold an agent-friendly CLI draft (cli-creator)
  smith register <name>             sandbox-run the draft's self-test; pass -> Tier 0 + promotion request
  smith publish                     sign the manifest set + commit/push the index repo
  smith fetch <name>[@<ver>]        install a tool on this machine from the index (checksum + signature verified)
  smith sync                        pull the index repo + reconcile: tools demoted upstream come off PATH now
  smith gc [--older-than 30d] [--yes]  remove unpromoted Tier 0 versions + stale drafts (dry-run unless --yes)

Tiers: 0 = authored (quarantined)  1 = trusted (PATH)  2 = external (quarantined)
SMITH_HOME: ` + defaultHomeHint() + `
`)
}

func defaultHomeHint() string {
	if h := os.Getenv("SMITH_HOME"); h != "" {
		return h + " (from $SMITH_HOME)"
	}
	return "~/.toolsmith"
}

func splitSpec(s string) (name, version string) {
	if i := strings.LastIndex(s, "@"); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// ---------------------------------------------------------------- init

func cmdInit(p config.Paths, args []string) error {
	if err := p.EnsureLayout(); err != nil {
		return err
	}
	if _, err := sandbox.WriteDefaultProfile(p.SandboxProfiles); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := ensureActivation(cwd, p.TrustedBin); err != nil {
		return err
	}
	if err := misex.Trust(cwd); err != nil {
		return fmt.Errorf("mise trust failed: %w", err)
	}
	// SMITH_HOME is the index repo; make it one so publish can commit/push.
	if _, err := os.Stat(filepath.Join(p.Home, ".git")); os.IsNotExist(err) {
		if err := gitInHome(p, "init", "-q"); err != nil {
			fmt.Fprintln(os.Stderr, "[smith] warning: could not git-init SMITH_HOME; `smith publish` needs it")
		}
	}
	// Verify the two PATH invariants (§5.1) against what mise will activate.
	envPath, err := misex.EnvPath(cwd)
	if err != nil {
		return err
	}
	if !pathContains(envPath, p.TrustedBin) {
		return fmt.Errorf("activation check failed: %s not on mise-activated PATH", p.TrustedBin)
	}
	if pathTouchesQuarantine(envPath, p.StoreQuarantine) {
		return fmt.Errorf("INVARIANT VIOLATION: quarantine dir is on the activated PATH")
	}
	fmt.Printf("initialized: SMITH_HOME=%s\n", p.Home)
	fmt.Printf("trusted store on PATH:  %s\n", p.TrustedBin)
	fmt.Printf("quarantine off PATH:    %s (verified)\n", p.StoreQuarantine)
	fmt.Println("note: activation requires mise in your shell (`mise activate`) or `mise exec`")
	return nil
}

// ensureActivation adds env._.path = [trustedBin] via mise.local.toml, which
// mise merges with mise.toml. Handles the common shapes; refuses to guess on
// anything exotic rather than corrupting user config.
func ensureActivation(projectDir, trustedBin string) error {
	path := filepath.Join(projectDir, "mise.local.toml")
	pathLine := fmt.Sprintf("_.path = [%q]", trustedBin)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		content := "# managed by smith: puts the Toolsmith trusted store on PATH\n[env]\n" + pathLine + "\n"
		return os.WriteFile(path, []byte(content), 0o644)
	}
	if err != nil {
		return err
	}
	text := string(raw)
	if strings.Contains(text, trustedBin) {
		return nil // already activated
	}
	if strings.Contains(text, "_.path") {
		return fmt.Errorf("mise.local.toml already sets env._.path; add %q to it manually", trustedBin)
	}
	if i := strings.Index(text, "[env]"); i >= 0 {
		insertAt := i + len("[env]")
		text = text[:insertAt] + "\n" + pathLine + text[insertAt:]
	} else {
		if !strings.HasSuffix(text, "\n") && text != "" {
			text += "\n"
		}
		text += "\n[env]\n" + pathLine + "\n"
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func pathContains(pathVar, dir string) bool {
	for _, p := range filepath.SplitList(pathVar) {
		if p == dir {
			return true
		}
	}
	return false
}

func pathTouchesQuarantine(pathVar, quarantine string) bool {
	prefix := quarantine + string(filepath.Separator)
	for _, p := range filepath.SplitList(pathVar) {
		if p == quarantine || strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- add

func cmdAdd(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	allowUnverified := fs.Bool("allow-unverified", false,
		"accept a source with no attestation and no curated-registry checksums (TOFU pin only)")
	var spec0 string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		spec0, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if spec0 == "" && fs.NArg() == 1 {
		spec0 = fs.Arg(0)
	}
	if spec0 == "" {
		return fmt.Errorf("usage: smith add <name>[@<version>] [--allow-unverified]")
	}
	if err := p.EnsureLayout(); err != nil {
		return err
	}
	name, version := splitSpec(spec0)

	// Verification policy (§8 Phase 3): attestation when the source offers
	// it, curated-registry checksums as baseline, TOFU only with explicit
	// operator consent — decided BEFORE any resolution or download.
	backend := misex.Registry(name)
	level := attest.BackendLevel(backend)
	if level == attest.LevelTOFU && !*allowUnverified {
		return fmt.Errorf("%s resolves via %q: no attestation and no curated checksum registry.\n"+
			"Refusing (§7). To pin trust-on-first-use anyway: smith add %s --allow-unverified", name, backend, spec0)
	}

	if version == "" {
		v, err := misex.Latest(name)
		if err != nil || v == "" {
			return fmt.Errorf("could not resolve latest version for %q via mise: %v", name, err)
		}
		version = v
	}
	spec := name + "@" + version
	if _, err := index.Load(p.IndexTools, name, version); err == nil {
		return fmt.Errorf("%s already in index; `smith list` to inspect", spec)
	}

	fmt.Fprintf(os.Stderr, "resolving %s via mise (backend checksum verification applies)...\n", spec)
	if err := misex.Install(spec); err != nil {
		return fmt.Errorf("mise install failed — tool is NOT registered: %w", err)
	}
	verification := level
	if repo := attest.GitHubRepo(backend); repo != "" {
		if dlDir, err := misex.DownloadsDir(spec); err == nil {
			if res, ok := attest.VerifyArchives(dlDir, repo); ok {
				verification = res.Level
				fmt.Fprintf(os.Stderr, "[smith] attestation verified: %s (%s)\n", res.Detail, res.PredicateType)
			} else {
				fmt.Fprintf(os.Stderr, "[smith] no attestations for %s archives; verification level: %s\n", repo, level)
			}
		}
	}
	// mise's backend knows where the executables are inside the archive
	// (bin/, nested release dir, ...); ask it rather than guessing.
	binDirs, err := misex.BinPaths(spec)
	if err != nil || len(binDirs) == 0 {
		src, werr := misex.Where(spec)
		if werr != nil {
			return werr
		}
		binDirs = []string{src}
		if st, serr := os.Stat(filepath.Join(src, "bin")); serr == nil && st.IsDir() {
			binDirs = []string{filepath.Join(src, "bin")}
		}
	}
	qdir := store.QuarantineDir(p.StoreQuarantine, name, version)
	var names []string
	for _, d := range binDirs {
		copied, err := store.CopyExecutables(d, filepath.Join(qdir, "bin"))
		if err != nil {
			return err
		}
		names = append(names, copied...)
	}
	if len(names) == 0 {
		os.RemoveAll(qdir)
		return fmt.Errorf("no executables found in %s; not registering", strings.Join(binDirs, ", "))
	}
	var artifacts []index.Artifact
	for _, n := range names {
		sum, err := store.HashFile(filepath.Join(qdir, "bin", n))
		if err != nil {
			return err
		}
		artifacts = append(artifacts, index.Artifact{Path: filepath.Join("bin", n), Sha256: sum})
	}
	e := index.Entry{
		Name: name, Version: version, Platform: index.CurrentPlatform(), Tier: 2,
		Origin: "external", Source: "mise:" + spec,
		AddedAt:      nowUTC(),
		Artifacts:    artifacts,
		Verification: verification,
	}
	if err := index.Save(p.IndexTools, e); err != nil {
		return err
	}
	if err := provenance.Append(p.ProvenanceLog, provenance.Entry{
		Event: "add", Tool: name, Version: version,
		TierTo: provenance.Tier(2), Actor: config.Actor(), Origin: e.Source,
		Verification: verification,
	}); err != nil {
		return err
	}

	// Capture a signed cross-platform checksum record (mise.lock) so the
	// index carries integrity metadata for every configured platform, not
	// just this machine's. Best-effort: the tool is already registered with
	// smith's own pin; a lock failure is a warning, not a rollback.
	fc, _ := p.LoadFileConfig()
	lockPath := filepath.Join(p.IndexTools, name, version, "mise.lock")
	if err := misex.Lock(spec, lockPath, fc.Platforms); err != nil {
		fmt.Fprintf(os.Stderr, "[smith] warning: could not lock cross-platform checksums: %v\n", err)
	} else {
		plats := fc.Platforms
		if len(plats) == 0 {
			plats = []string{"this platform"}
		}
		fmt.Fprintf(os.Stderr, "[smith] locked checksums for: %s (index/tools/%s/%s/mise.lock)\n",
			strings.Join(plats, ", "), name, version)
	}

	fmt.Printf("added %s at Tier 2 (external quarantine)\n", spec)
	fmt.Printf("  store: %s\n", qdir)
	fmt.Printf("  verification: %s\n", verification)
	for _, a := range artifacts {
		fmt.Printf("  %s  sha256=%s\n", a.Path, a.Sha256)
	}
	fmt.Printf("run sandboxed:  smith run %s [args]\n", name)
	fmt.Printf("trust it:       smith promote %s   (interactive, human only)\n", spec)
	return nil
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// ---------------------------------------------------------------- new / register

func draftDir(p config.Paths, name string) string {
	return filepath.Join(p.Home, "drafts", name)
}

func cmdNew(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	runtime := fs.String("runtime", "bash", "tool runtime: bash | python (stdlib)")
	desc := fs.String("desc", "", "one-line description (becomes SKILL.md trigger stub)")
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if name == "" && fs.NArg() == 1 {
		name = fs.Arg(0)
	}
	if name == "" {
		return fmt.Errorf("usage: smith new <name> [--runtime bash|python] [--desc <text>]")
	}
	if strings.ContainsAny(name, "/@ ") {
		return fmt.Errorf("tool name must be a plain identifier (no /, @, spaces)")
	}
	if err := p.EnsureLayout(); err != nil {
		return err
	}
	d := *desc
	if d == "" {
		d = "TODO: describe " + name
	}
	dir := draftDir(p, name)
	if err := scaffold.Create(dir, scaffold.Manifest{
		Name: name, Version: "0.1.0", Description: d, Runtime: *runtime,
	}); err != nil {
		return err
	}
	fmt.Printf("draft scaffolded: %s\n", dir)
	fmt.Printf(`next steps (cli-creator flow):
  1. implement %s/%s (keep --json/--quiet/--help contract)
  2. replace the TODO block in selftest.sh with real assertions
  3. sharpen the trigger description in SKILL.md
  4. smith register %s   (runs the self-test sandboxed; pass -> Tier 0)
`, dir, name, name)
	return nil
}

func cmdRegister(p config.Paths, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: smith register <name>")
	}
	name := args[0]
	dir := draftDir(p, name)
	m, err := scaffold.LoadManifest(dir)
	if err != nil {
		return err
	}
	if m.Name != name {
		return fmt.Errorf("manifest name %q does not match draft %q", m.Name, name)
	}
	if _, err := index.Load(p.IndexTools, m.Name, m.Version); err == nil {
		return fmt.Errorf("%s@%s already registered; bump version in %s/manifest.yaml", m.Name, m.Version, dir)
	}
	entry := filepath.Join(dir, m.Entrypoint)
	if err := os.Chmod(entry, 0o755); err != nil {
		return fmt.Errorf("entrypoint %s: %w", entry, err)
	}

	// Self-test runs INSIDE the sandbox: it is executing unreviewed Tier-0
	// code. Failure blocks registration (§8 Phase 1).
	if err := runSelfTest(p, dir, entry); err != nil {
		return fmt.Errorf("self-test FAILED — not registered: %w", err)
	}

	// Copy the full review bundle into quarantine: the executable plus the
	// pinned docs a promotion review needs (source == executable for these
	// runtimes; selftest + manifest + SKILL.md ride along, hashed).
	qdir := store.QuarantineDir(p.StoreQuarantine, m.Name, m.Version)
	if _, err := os.Stat(qdir); err == nil {
		return fmt.Errorf("quarantine dir already exists: %s", qdir)
	}
	if _, err := store.CopyExecutables(dir, filepath.Join(qdir, "bin")); err != nil {
		return err
	}
	e := index.Entry{
		Name: m.Name, Version: m.Version, Platform: index.PlatformAny, Tier: 0,
		Origin: "authored:" + config.Actor(), Source: "local:" + dir,
		AddedAt: nowUTC(),
	}
	binArt, err := pinFile(qdir, filepath.Join("bin", m.Entrypoint))
	if err != nil {
		return err
	}
	e.Artifacts = []index.Artifact{binArt}
	for _, doc := range []string{"SKILL.md", "manifest.yaml"} {
		src := filepath.Join(dir, doc)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyPlain(src, filepath.Join(qdir, doc)); err != nil {
			return err
		}
		a, err := pinFile(qdir, doc)
		if err != nil {
			return err
		}
		e.Docs = append(e.Docs, a)
	}
	// selftest.sh was copied by CopyExecutables (it is executable); pin it as
	// a doc so review sees exactly what was tested, but never symlink it.
	if _, err := os.Stat(filepath.Join(qdir, "bin", "selftest.sh")); err == nil {
		a, err := pinFile(qdir, filepath.Join("bin", "selftest.sh"))
		if err != nil {
			return err
		}
		e.Docs = append(e.Docs, a)
	}
	if err := index.Save(p.IndexTools, e); err != nil {
		return err
	}
	if err := provenance.Append(p.ProvenanceLog, provenance.Entry{
		Event: "author", Tool: m.Name, Version: m.Version,
		TierTo: provenance.Tier(0), Actor: config.Actor(),
		Origin: e.Source, SelfTest: "pass",
	}); err != nil {
		return err
	}
	fmt.Printf("registered %s@%s at Tier 0 (authored, quarantined)\n", m.Name, m.Version)
	fmt.Printf("  store: %s\n  %s  sha256=%s\n", qdir, binArt.Path, binArt.Sha256)
	fmt.Printf(`
PROMOTION REQUEST — human action required
  tool:    %s@%s (Tier 0)
  review:  read %s and its selftest.sh — the self-test passed but was
           authored by the same agent that wrote the tool (§7)
  approve: smith promote %s@%s   (interactive terminal only)
until then: runnable only via `+"`smith run %s`"+` (sandboxed)
`, m.Name, m.Version, filepath.Join(qdir, "bin", m.Entrypoint), m.Name, m.Version, m.Name)
	return nil
}

// runSelfTest executes <draft>/selftest.sh <entrypoint> sandboxed, from a
// scratch workdir so tool/test writes land nowhere meaningful.
func runSelfTest(p config.Paths, dir, entry string) error {
	st := filepath.Join(dir, "selftest.sh")
	if _, err := os.Stat(st); err != nil {
		return fmt.Errorf("selftest.sh missing: a tool without a self-test cannot register")
	}
	work, err := os.MkdirTemp("", "smith-selftest-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	argv, err := sandbox.Command(p.SandboxProfiles, work, []string{"/bin/bash", st, entry})
	if err != nil {
		return err // no sandbox -> no self-test -> no registration
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = work
	cmd.Env, err = sandbox.Env(nil) // self-tests run unreviewed code: sanitized env too
	if err != nil {
		return err
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	fmt.Fprintf(os.Stderr, "[smith] running self-test sandboxed in %s\n", work)
	return cmd.Run()
}

func pinFile(base, rel string) (index.Artifact, error) {
	sum, err := store.HashFile(filepath.Join(base, rel))
	if err != nil {
		return index.Artifact{}, err
	}
	return index.Artifact{Path: rel, Sha256: sum}, nil
}

func copyPlain(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o644)
}

// ---------------------------------------------------------------- list

func cmdList(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	entries, err := index.List(p.IndexTools)
	if err != nil {
		return err
	}
	if *asJSON {
		if entries == nil {
			entries = []index.Entry{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	if len(entries) == 0 {
		fmt.Println("no tools in index; try `smith add <name>`")
		return nil
	}
	tierName := map[int]string{0: "0 authored", 1: "1 TRUSTED", 2: "2 external"}
	fmt.Printf("%-16s %-12s %-12s %-14s %-10s %s\n", "NAME", "VERSION", "PLATFORM", "TIER", "ORIGIN", "ADDED")
	for _, e := range entries {
		origin := e.Origin
		if i := strings.Index(origin, ":"); i > 0 {
			origin = origin[:i]
		}
		fmt.Printf("%-16s %-12s %-12s %-14s %-10s %s\n", e.Name, e.Version, e.Platform, tierName[e.Tier], origin, e.AddedAt)
	}
	return nil
}

// ---------------------------------------------------------------- run

func cmdRun(p config.Paths, args []string) error {
	// Leading --pass-env NAME[,NAME...] flags forward specific parent env
	// vars into the sandbox (ignored for Tier 1, which runs unsandboxed
	// with the full environment anyway).
	var passEnv []string
	for len(args) > 0 {
		if v, ok := strings.CutPrefix(args[0], "--pass-env="); ok {
			passEnv = append(passEnv, strings.Split(v, ",")...)
			args = args[1:]
		} else if args[0] == "--pass-env" && len(args) > 1 {
			passEnv = append(passEnv, strings.Split(args[1], ",")...)
			args = args[2:]
		} else {
			break
		}
	}
	if len(args) < 1 {
		return fmt.Errorf("usage: smith run [--pass-env NAME,...] <name>[@<version>] [args...]")
	}
	name, version := splitSpec(args[0])
	toolArgs := args[1:]
	e, err := index.Resolve(p.IndexTools, name, version)
	if err != nil {
		return err
	}
	var base string
	if e.Tier == 1 {
		base = store.TrustedDir(p.StoreTrusted, e.Name, e.Version)
	} else {
		base = store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)
	}
	art, err := pickArtifact(e, name)
	if err != nil {
		return err
	}
	bin := filepath.Join(base, art.Path)
	// Integrity gate before every execution: the pinned hash must match.
	sum, err := store.HashFile(bin)
	if err != nil {
		return fmt.Errorf("artifact missing or unreadable (store/index drift?): %w", err)
	}
	if sum != art.Sha256 {
		return fmt.Errorf("REFUSING TO RUN %s: sha256 mismatch\n  pinned: %s\n  actual: %s\nrun `smith verify` and investigate", bin, art.Sha256, sum)
	}
	argv := append([]string{bin}, toolArgs...)
	env := os.Environ()
	if e.Tier != 1 {
		wrapped, err := sandbox.Command(p.SandboxProfiles, "", argv)
		if err != nil {
			return err // never fall back to unsandboxed
		}
		if env, err = sandbox.Env(passEnv); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "[smith] tier %d tool — running sandboxed (no network, writes limited to CWD+TMP, env sanitized)\n", e.Tier)
		argv = wrapped
	}
	exe, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(exe, argv, env)
}

func pickArtifact(e index.Entry, name string) (index.Artifact, error) {
	if len(e.Artifacts) == 0 {
		return index.Artifact{}, fmt.Errorf("index entry %s@%s has no artifacts", e.Name, e.Version)
	}
	for _, a := range e.Artifacts {
		if filepath.Base(a.Path) == name {
			return a, nil
		}
	}
	if len(e.Artifacts) == 1 {
		return e.Artifacts[0], nil
	}
	var names []string
	for _, a := range e.Artifacts {
		names = append(names, filepath.Base(a.Path))
	}
	return index.Artifact{}, fmt.Errorf("%s@%s ships several binaries (%s); run one by its own name", e.Name, e.Version, strings.Join(names, ", "))
}

// ---------------------------------------------------------------- review

// cmdReview surfaces everything a human needs to make the promotion decision
// (§7): it re-checks the on-disk bytes against their pins, prints provenance,
// and shows the source — inline, in $VISUAL/$EDITOR, or as paths to open.
// Read-only; it never changes tiers.
func cmdReview(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	inline := fs.Bool("inline", false, "force the interactive viewer (default on a TTY)")
	plain := fs.Bool("plain", false, "dump source to stdout (no TUI); default when piped")
	edit := fs.Bool("edit", false, "open the bundle in an editor")
	editor := fs.String("editor", "", "editor command to use (overrides $VISUAL/$EDITOR/config); implies --edit")
	paths := fs.Bool("paths", false, "print file paths only")
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
		return fmt.Errorf("usage: smith review <name>[@<version>] [--plain|--edit|--paths] [--editor <cmd>]")
	}
	name, version := splitSpec(spec)
	e, err := index.Resolve(p.IndexTools, name, version)
	if err != nil {
		return err
	}
	base := store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)
	if e.Tier == 1 {
		base = store.TrustedDir(p.StoreTrusted, e.Name, e.Version)
	}

	tierName := map[int]string{0: "0 authored (pending review)", 1: "1 TRUSTED", 2: "2 external (pending review)"}
	fmt.Printf("== %s@%s ==\n", e.Name, e.Version)
	fmt.Printf("tier:         %s\n", tierName[e.Tier])
	fmt.Printf("origin:       %s\n", e.Origin)
	fmt.Printf("source:       %s\n", e.Source)
	fmt.Printf("added:        %s\n", e.AddedAt)
	if e.Verification != "" {
		fmt.Printf("verification: %s\n", e.Verification)
	}

	// Integrity: the bytes you're about to read must match the pins, or the
	// review is meaningless.
	fmt.Println("\nintegrity (pinned bytes vs on-disk):")
	allPinned := append(append([]index.Artifact{}, e.Artifacts...), e.Docs...)
	drift := false
	for _, a := range allPinned {
		sum, herr := store.HashFile(filepath.Join(base, a.Path))
		switch {
		case herr != nil:
			fmt.Printf("  MISSING  %s (%v)\n", a.Path, herr)
			drift = true
		case sum != a.Sha256:
			fmt.Printf("  MISMATCH %s\n", a.Path)
			drift = true
		default:
			fmt.Printf("  ok       %s\n", a.Path)
		}
	}
	if drift {
		fmt.Fprintln(os.Stderr, "\n[smith] WARNING: on-disk bytes differ from the pinned hashes — do NOT promote; investigate.")
	}

	// Provenance for this tool across all machines' logs.
	if entries := provenanceFor(p, e.Name, e.Version); len(entries) > 0 {
		fmt.Println("\nprovenance:")
		for _, pe := range entries {
			line := fmt.Sprintf("  %s  %-8s actor=%s", pe.Time, pe.Event, pe.Actor)
			if pe.SelfTest != "" {
				line += " self-test=" + pe.SelfTest
			}
			if pe.Reviewer != "" {
				line += " reviewer=" + pe.Reviewer
			}
			if pe.Reason != "" {
				line += " reason=" + pe.Reason
			}
			fmt.Println(line)
		}
	}

	all, err := bundleFiles(base)
	if err != nil {
		return err
	}
	files := orderForReview(e, all) // source (executables) first, docs last
	fmt.Printf("\nbundle (%d files) in %s:\n", len(files), base)
	for _, f := range files {
		fmt.Printf("  %s\n", f)
	}

	// Resolve the editor once: --editor flag > $VISUAL > $EDITOR >
	// config.yaml editor. Env must be *exported* to be visible here.
	fc, _ := p.LoadFileConfig()
	ed := *editor
	for _, cand := range []string{os.Getenv("VISUAL"), os.Getenv("EDITOR"), fc.Editor} {
		if ed == "" {
			ed = cand
		}
	}

	// Pick a mode. Explicit flags win. Default is the interactive TUI on a
	// real terminal; piped/non-TTY falls back to a plain dump so
	// `smith review x | less` and agents still work.
	tty := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	mode := ""
	switch {
	case *edit || *editor != "":
		mode = "edit"
	case *paths:
		mode = "paths"
	case *plain:
		mode = "plain"
	case *inline || tty:
		mode = "tui"
	default:
		mode = "plain"
	}
	if mode == "tui" && !tty {
		mode = "plain" // can't drive a TUI without a terminal
	}

	switch mode {
	case "paths":
		for _, f := range files {
			fmt.Println(filepath.Join(base, f))
		}
	case "edit":
		return openInEditor(ed, base, files)
	case "plain":
		if err := printInline(base, files); err != nil {
			return err
		}
	case "tui":
		if err := runReviewTUI(e, base, files, fc); err != nil {
			return err
		}
	}

	if e.Tier != 1 {
		fmt.Printf("\nif the source is sound: smith promote %s@%s  (interactive)\n", e.Name, e.Version)
	}
	return nil
}

// runReviewTUI loads the bundle into the interactive reviewer and prints the
// resulting comment report (agent-pasteable) on exit.
func runReviewTUI(e index.Entry, base string, files []string, fc config.FileConfig) error {
	rf, err := loadReviewFiles(base, files)
	if err != nil {
		return err
	}
	theme := reviewtui.ThemeByName(fc.Review.Theme)
	if fc.Review.Color == "never" {
		theme = reviewtui.ThemeByName("mono")
	}
	m := reviewtui.New(e.Name, e.Version, rf)
	if err := reviewtui.RunTerminal(m, theme); err != nil {
		return err
	}
	if report := m.Report(); report != "" {
		fmt.Print("\n" + report)
		fmt.Fprintf(os.Stderr, "\n[smith] %d comment(s) above — paste to the tool's author.\n", len(m.Comments))
	} else {
		fmt.Fprintln(os.Stderr, "[smith] no comments recorded.")
	}
	return nil
}

// loadReviewFiles reads a bundle into reviewer files: text is split into
// lines with a detected language; binary artifacts become a single note line
// (they are not human-readable in a source view).
func loadReviewFiles(base string, files []string) ([]reviewtui.File, error) {
	var rf []reviewtui.File
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(base, rel))
		if err != nil {
			return nil, err
		}
		if isBinary(data) {
			sum, _ := store.HashFile(filepath.Join(base, rel))
			rf = append(rf, reviewtui.File{Name: rel, Lang: "generic",
				Lines: []string{fmt.Sprintf("(binary file, %d bytes, sha256=%s)", len(data), sum)}})
			continue
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		first := ""
		if len(lines) > 0 {
			first = lines[0]
		}
		rf = append(rf, reviewtui.File{Name: rel, Lang: reviewtui.LangFor(rel, first), Lines: lines})
	}
	return rf, nil
}

// provenanceFor returns log entries mentioning a tool@version, oldest first,
// across every per-machine log.
func provenanceFor(p config.Paths, name, version string) []provenance.Entry {
	var out []provenance.Entry
	logs, _ := filepath.Glob(filepath.Join(filepath.Dir(p.ProvenanceLog), "*.jsonl"))
	for _, lg := range logs {
		raw, err := os.ReadFile(lg)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var pe provenance.Entry
			if json.Unmarshal([]byte(line), &pe) != nil {
				continue
			}
			if pe.Tool == name && (pe.Version == version || pe.Version == "") {
				out = append(out, pe)
			}
		}
	}
	return out
}

// orderForReview sorts bundle files source-first: executables (the entry
// point and any other artifacts) before docs (SKILL.md, selftest, manifest),
// so the reviewer reads what actually runs before the metadata. Any file not
// classified by the index entry is appended last.
func orderForReview(e index.Entry, all []string) []string {
	rank := map[string]int{}
	for _, a := range e.Artifacts {
		rank[a.Path] = 0
	}
	for _, d := range e.Docs {
		if _, ok := rank[d.Path]; !ok {
			if strings.HasPrefix(d.Path, "bin/") {
				rank[d.Path] = 1 // executable docs (e.g. selftest.sh) — still code
			} else {
				rank[d.Path] = 2 // metadata (SKILL.md, manifest.yaml)
			}
		}
	}
	ordered := append([]string(nil), all...)
	sort.SliceStable(ordered, func(i, j int) bool {
		ri, oki := rank[ordered[i]]
		rj, okj := rank[ordered[j]]
		if !oki {
			ri = 3
		}
		if !okj {
			rj = 3
		}
		if ri != rj {
			return ri < rj
		}
		return ordered[i] < ordered[j]
	})
	return ordered
}

// bundleFiles lists the regular files in a tool's store dir, relative paths.
func bundleFiles(base string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(base, path)
		if rerr != nil {
			return rerr
		}
		out = append(out, rel)
		return nil
	})
	return out, err
}

// printInline dumps each text file with a header; binary files are noted, not
// printed (an external Tier-2 binary is not human-readable).
func printInline(base string, files []string) error {
	for _, f := range files {
		full := filepath.Join(base, f)
		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		fmt.Printf("\n===== %s =====\n", f)
		if isBinary(data) {
			sum, _ := store.HashFile(full)
			fmt.Printf("(binary, %d bytes, sha256=%s — inspect with your own tools)\n", len(data), sum)
			continue
		}
		os.Stdout.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			fmt.Println()
		}
	}
	return nil
}

func isBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// openInEditor opens the bundle files with the resolved editor command. It
// never guesses an editor: with none resolved it prints the paths so the
// operator opens them in whatever they use.
func openInEditor(ed, base string, files []string) error {
	full := make([]string, len(files))
	for i, f := range files {
		full[i] = filepath.Join(base, f)
	}
	if strings.TrimSpace(ed) == "" {
		fmt.Fprintln(os.Stderr, "[smith] no editor resolved. Set one of:")
		fmt.Fprintln(os.Stderr, "  export EDITOR=nano        (must be EXPORTed — a bare EDITOR=... isn't inherited)")
		fmt.Fprintln(os.Stderr, "  editor: nano              in $SMITH_HOME/config.yaml")
		fmt.Fprintln(os.Stderr, "  smith review ... --editor nano")
		fmt.Fprintln(os.Stderr, "meanwhile, open these paths yourself:")
		for _, f := range full {
			fmt.Println(f)
		}
		return nil
	}
	// Open each file in its own editor session, advancing when you close it.
	// This works identically across editors — terminal editors (nano/vim)
	// would otherwise load extra files as hidden buffers you never see, and
	// exiting closes them all; sequential sessions make every file reviewable
	// no matter the editor.
	fields := strings.Fields(ed) // allow "code --wait" etc.
	for i, f := range full {
		fmt.Fprintf(os.Stderr, "[smith] [%d/%d] %s — close the editor to continue\n", i+1, len(full), files[i])
		argv := append(append([]string(nil), fields[1:]...), f)
		cmd := exec.Command(fields[0], argv...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("editor %q on %s: %w", ed, files[i], err)
		}
	}
	return nil
}

// ---------------------------------------------------------------- promote

func cmdPromote(p config.Paths, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: smith promote <name>[@<version>]")
	}
	name, version := splitSpec(args[0])
	e, err := index.Resolve(p.IndexTools, name, version)
	if err != nil {
		return err
	}
	if e.Tier == 1 {
		return fmt.Errorf("%s@%s is already Tier 1", e.Name, e.Version)
	}

	// HARD GATE (§7): promotion is a human decision made interactively.
	// Agent sessions run without a TTY on stdin (pipes, /dev/null) and are
	// refused here. This is a guard against accidental agent promotion, not
	// a cryptographic proof of humanity — the real gate is the review.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("promotion requires an interactive terminal (human review). " +
			"If you are an agent: stop — ask the operator to run this command themselves")
	}

	fmt.Printf("Promotion review for %s@%s (tier %d -> 1)\n", e.Name, e.Version, e.Tier)
	fmt.Printf("  origin: %s\n  source: %s\n  added:  %s\n", e.Origin, e.Source, e.AddedAt)
	if e.Verification != "" {
		fmt.Printf("  verification at add time: %s\n", e.Verification)
	}
	qdir := store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)
	fmt.Printf("  store:  %s\n", qdir)
	for _, a := range e.Artifacts {
		fmt.Printf("  %s  sha256=%s\n", a.Path, a.Sha256)
	}
	fmt.Printf("\nReview means reading the tool source / provenance — a passing self-test is not sufficient (§7).\n")
	fmt.Printf("If you haven't: run `smith review %s@%s` in another shell first.\n", e.Name, e.Version)

	rd := bufio.NewReader(os.Stdin)
	fmt.Print("Reviewer name: ")
	reviewer, _ := rd.ReadString('\n')
	reviewer = strings.TrimSpace(reviewer)
	if reviewer == "" {
		return fmt.Errorf("aborted: reviewer required")
	}
	confirm := fmt.Sprintf("promote %s@%s", e.Name, e.Version)
	fmt.Printf("Type %q to confirm you reviewed it: ", confirm)
	typed, _ := rd.ReadString('\n')
	if strings.TrimSpace(typed) != confirm {
		return fmt.Errorf("aborted: confirmation did not match")
	}
	if err := promoteEntry(p, e, reviewer); err != nil {
		return err
	}
	fmt.Printf("promoted: %s@%s is now Tier 1 (trusted, on PATH via %s)\n", e.Name, e.Version, p.TrustedBin)
	return nil
}

// pushBlob enforces the §5.3 source rule at promotion time: an authored
// tool's bytes must be on the shared transport before it can become Tier 1,
// or Goal 1 silently fails on the second machine.
func pushBlob(p config.Paths, e *index.Entry) error {
	fc, err := p.LoadFileConfig()
	if err != nil {
		return err
	}
	tr, err := transport.New(fc.Transport)
	if err != nil {
		return fmt.Errorf("authored tool cannot be promoted until its blob is pushed to shared storage: %w", err)
	}
	qdir := store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)
	tmp, err := os.MkdirTemp("", "smith-blob-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	blobPath := filepath.Join(tmp, "blob.tgz")
	if err := transport.PackBlob(qdir, blobPath); err != nil {
		return err
	}
	sum, err := store.HashFile(blobPath)
	if err != nil {
		return err
	}
	asset := fmt.Sprintf("%s-%s-%s-%s.tgz", e.Name, e.Version, e.Platform, sum[:12])
	locator, err := tr.Push(blobPath, asset)
	if err != nil {
		return fmt.Errorf("blob push failed — promotion refused: %w", err)
	}
	e.Blob = &index.Blob{Locator: locator, Sha256: sum}
	e.Source = locator
	fmt.Fprintf(os.Stderr, "[smith] blob pushed to %s (%s)\n", tr.String(), asset)
	return nil
}

// promoteEntry performs the promotion mutations after the human gate has
// passed. Kept separate so the mechanics are testable without weakening the
// TTY gate above.
func promoteEntry(p config.Paths, e index.Entry, reviewer string) error {
	if strings.HasPrefix(e.Source, "local:") {
		if err := pushBlob(p, &e); err != nil {
			return err
		}
	}
	qdir := store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)
	tdir := store.TrustedDir(p.StoreTrusted, e.Name, e.Version)
	if err := store.Move(qdir, tdir); err != nil {
		return err
	}
	var names []string
	for _, a := range e.Artifacts {
		names = append(names, a.Path)
	}
	if err := store.LinkBin(p.TrustedBin, p.StoreTrusted, e.Name, e.Version, names); err != nil {
		return err
	}
	from := e.Tier
	e.Tier = 1
	if err := index.Save(p.IndexTools, e); err != nil {
		return err
	}
	return provenance.Append(p.ProvenanceLog, provenance.Entry{
		Event: "promote", Tool: e.Name, Version: e.Version,
		TierFrom: provenance.Tier(from), TierTo: provenance.Tier(1),
		Actor: config.Actor(), Reviewer: reviewer,
	})
}

// ---------------------------------------------------------------- demote

func cmdDemote(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("demote", flag.ContinueOnError)
	reason := fs.String("reason", "", "why trust is being revoked (required, recorded in provenance)")
	// Accept `smith demote <spec> --reason ...` (stdlib flag stops at the
	// first positional, so pull the spec out before parsing).
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
	if spec == "" || *reason == "" {
		return fmt.Errorf("usage: smith demote <name>[@<version>] --reason <why>")
	}
	name, version := splitSpec(spec)
	e, err := index.Resolve(p.IndexTools, name, version)
	if err != nil {
		return err
	}
	if e.Tier != 1 {
		return fmt.Errorf("%s@%s is not Tier 1 (tier %d)", e.Name, e.Version, e.Tier)
	}
	var names []string
	for _, a := range e.Artifacts {
		names = append(names, a.Path)
	}
	// Off PATH first — revocation must be immediate even if the move fails.
	if err := store.UnlinkBin(p.TrustedBin, p.StoreTrusted, e.Name, e.Version, names); err != nil {
		return err
	}
	tdir := store.TrustedDir(p.StoreTrusted, e.Name, e.Version)
	qdir := store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)
	if err := store.Move(tdir, qdir); err != nil {
		return err
	}
	e.Tier = 2
	if err := index.Save(p.IndexTools, e); err != nil {
		return err
	}
	if err := provenance.Append(p.ProvenanceLog, provenance.Entry{
		Event: "demote", Tool: e.Name, Version: e.Version,
		TierFrom: provenance.Tier(1), TierTo: provenance.Tier(2),
		Actor: config.Actor(), Reason: *reason,
	}); err != nil {
		return err
	}
	fmt.Printf("demoted: %s@%s -> Tier 2 quarantine, removed from PATH\n", e.Name, e.Version)
	return nil
}

// ---------------------------------------------------------------- publish

// cmdPublish signs the manifest set and commits (and pushes, if a remote is
// configured) the index repo. Separate, explicit human step after promote —
// nothing auto-publishes (§4, §7).
func cmdPublish(p config.Paths, args []string) error {
	entries, err := index.ListAll(p.IndexTools)
	if err != nil {
		return err
	}
	// The signed index is the trust vehicle across machines: refuse to sign
	// Tier 1 authored entries whose blob never made it to shared storage.
	for _, e := range entries {
		if e.Tier == 1 && strings.HasPrefix(e.Source, "local:") {
			return fmt.Errorf("%s@%s is Tier 1 with a machine-local source — re-promote after configuring a transport", e.Name, e.Version)
		}
	}
	// Nothing to publish if no manifest changed since the last commit (the
	// signature's own timestamp would otherwise make index/ always dirty).
	if out, _ := gitOutput(p, "status", "--porcelain", "index/tools"); strings.TrimSpace(out) == "" {
		if _, statErr := os.Stat(filepath.Join(p.Home, "index", "index.sig")); statErr == nil {
			fmt.Println("no manifest changes since last publish; nothing to do")
			return nil
		}
	}
	s, err := sign.SignIndex(p.Home, p.IndexTools, nowUTC())
	if err != nil {
		return err
	}
	fmt.Printf("signed manifest set: digest %s… key %s\n", s.Digest[:12], s.KeyFprint)

	if err := provenance.Append(p.ProvenanceLog, provenance.Entry{
		Event: "publish", Actor: config.Actor(),
		Origin: "digest:" + s.Digest[:16] + " key:" + s.KeyFprint,
	}); err != nil {
		return err
	}
	if err := gitInHome(p, "add", "index", "provenance"); err != nil {
		return err
	}
	if err := gitInHome(p, "commit", "-m", "smith publish "+nowUTC()); err != nil {
		return err
	}
	if out, _ := gitOutput(p, "remote"); strings.TrimSpace(out) != "" {
		if err := gitInHome(p, "push", "-u", "origin", "HEAD"); err != nil {
			return fmt.Errorf("committed locally but push failed: %w", err)
		}
		fmt.Println("index committed and pushed")
	} else {
		fmt.Println("index committed (no git remote configured — add one to sync machines)")
	}
	return nil
}

func gitInHome(p config.Paths, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = p.Home
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

func gitOutput(p config.Paths, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = p.Home
	out, err := cmd.Output()
	return string(out), err
}

// ---------------------------------------------------------------- fetch

// cmdFetch materializes a tool on this machine from the index alone.
// Tier 1 + valid index signature -> installed trusted, on PATH.
// Anything else -> quarantine, sandbox-only, promote locally to trust.
func cmdFetch(p config.Paths, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: smith fetch <name>[@<version>]")
	}
	if err := p.EnsureLayout(); err != nil {
		return err
	}
	name, version := splitSpec(args[0])
	e, err := index.Resolve(p.IndexTools, name, version)
	if err != nil {
		return err
	}
	base := store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)
	if e.Tier == 1 {
		base = store.TrustedDir(p.StoreTrusted, e.Name, e.Version)
	}
	if _, err := os.Stat(base); err == nil {
		return fmt.Errorf("%s@%s already installed at %s", e.Name, e.Version, base)
	}

	trustable := e.Tier == 1
	if trustable {
		present, verr := sign.VerifyIndex(p.Home, p.IndexTools)
		if !present {
			trustable = false
			fmt.Fprintln(os.Stderr, "[smith] index is unsigned — Tier 1 entry will be installed to QUARANTINE; promote locally after review")
		} else if verr != nil {
			return fmt.Errorf("index signature check failed — refusing to fetch: %w", verr)
		}
	}

	switch {
	case e.Blob != nil:
		tmp, err := os.MkdirTemp("", "smith-fetch-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		blobPath := filepath.Join(tmp, "blob.tgz")
		fc, err := p.LoadFileConfig()
		if err != nil {
			return err
		}
		tr, err := transport.New(fc.Transport)
		if err != nil {
			return err
		}
		if err := tr.Fetch(e.Blob.Locator, blobPath); err != nil {
			return err
		}
		sum, err := store.HashFile(blobPath)
		if err != nil {
			return err
		}
		if sum != e.Blob.Sha256 {
			return fmt.Errorf("blob sha256 MISMATCH for %s@%s: pinned=%s got=%s — refusing", e.Name, e.Version, e.Blob.Sha256, sum)
		}
		dst := base
		if !trustable {
			dst = store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)
		}
		if err := transport.UnpackBlob(blobPath, dst); err != nil {
			return err
		}
	case strings.HasPrefix(e.Source, "mise:"):
		spec := strings.TrimPrefix(e.Source, "mise:")
		if err := misex.Install(spec); err != nil {
			return err
		}
		binDirs, err := misex.BinPaths(spec)
		if err != nil || len(binDirs) == 0 {
			return fmt.Errorf("mise bin-paths failed for %s: %v", spec, err)
		}
		dst := base
		if !trustable {
			dst = store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)
		}
		for _, d := range binDirs {
			if _, err := store.CopyExecutables(d, filepath.Join(dst, "bin")); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("entry %s@%s has no fetchable source (%s)", e.Name, e.Version, e.Source)
	}

	// Verify every pinned artifact of what we just materialized.
	dst := base
	if !trustable {
		dst = store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)
	}
	for _, a := range append(append([]index.Artifact{}, e.Artifacts...), e.Docs...) {
		sum, err := store.HashFile(filepath.Join(dst, a.Path))
		if err != nil {
			return fmt.Errorf("pinned file missing after fetch: %s: %w", a.Path, err)
		}
		if sum != a.Sha256 {
			os.RemoveAll(dst)
			return fmt.Errorf("sha256 MISMATCH on %s after fetch — removed; refusing", a.Path)
		}
	}
	tier := e.Tier
	if e.Tier == 1 && trustable {
		var names []string
		for _, a := range e.Artifacts {
			names = append(names, a.Path)
		}
		if err := store.LinkBin(p.TrustedBin, p.StoreTrusted, e.Name, e.Version, names); err != nil {
			return err
		}
	} else if e.Tier == 1 {
		tier = 2 // unsigned index: treat as external quarantine locally
		e.Tier = tier
		if err := index.Save(p.IndexTools, e); err != nil {
			return err
		}
	}
	if err := provenance.Append(p.ProvenanceLog, provenance.Entry{
		Event: "fetch", Tool: e.Name, Version: e.Version,
		TierTo: provenance.Tier(tier), Actor: config.Actor(), Origin: e.Source,
	}); err != nil {
		return err
	}
	fmt.Printf("fetched %s@%s -> tier %d (%s)\n", e.Name, e.Version, tier, dst)
	return nil
}

// ---------------------------------------------------------------- sync

// cmdSync pulls the index repo and reconciles the local store with it.
// Its load-bearing job is propagating revocation (§7): a tool demoted on
// another machine must come off THIS machine's PATH as soon as we sync,
// even though its binary is still sitting in the local trusted store.
func cmdSync(p config.Paths, args []string) error {
	if err := p.EnsureLayout(); err != nil {
		return err
	}
	if out, _ := gitOutput(p, "remote"); strings.TrimSpace(out) != "" {
		// Commit this machine's local provenance before pulling so the pull
		// has a clean tree. Each machine writes only its own <hostname>.jsonl,
		// so a merge across distinct machines never conflicts.
		if st, _ := gitOutput(p, "status", "--porcelain", "provenance"); strings.TrimSpace(st) != "" {
			_ = gitInHome(p, "add", "provenance")
			_ = gitInHome(p, "commit", "-m", "smith sync: local provenance "+nowUTC())
		}
		fmt.Fprintln(os.Stderr, "[smith] pulling index...")
		if err := gitInHome(p, "pull", "--no-rebase", "--no-edit"); err != nil {
			return fmt.Errorf("git pull failed (resolve manually in %s): %w", p.Home, err)
		}
	} else {
		fmt.Fprintln(os.Stderr, "[smith] no git remote; reconciling against local index only")
	}

	// Signature is advisory for revocation (removing trust is always safe),
	// but report it so the operator knows the index's integrity state.
	if present, err := sign.VerifyIndex(p.Home, p.IndexTools); present && err != nil {
		fmt.Fprintf(os.Stderr, "[smith] WARNING: index signature invalid after pull: %v\n", err)
	}

	installed, err := store.InstalledVersions(p.StoreTrusted)
	if err != nil {
		return err
	}
	revoked := 0
	for name, versions := range installed {
		for _, version := range versions {
			e, err := index.Load(p.IndexTools, name, version)
			demoted := err != nil || e.Tier != 1
			if !demoted {
				continue
			}
			reason := "revoked upstream (tier changed)"
			if err != nil {
				reason = "revoked upstream (entry removed from index)"
			}
			if uerr := store.UnlinkVersion(p.TrustedBin, p.StoreTrusted, name, version); uerr != nil {
				return uerr
			}
			if merr := store.Move(store.TrustedDir(p.StoreTrusted, name, version),
				store.QuarantineDir(p.StoreQuarantine, name, version)); merr != nil {
				return merr
			}
			if aerr := provenance.Append(p.ProvenanceLog, provenance.Entry{
				Event: "demote", Tool: name, Version: version,
				TierFrom: provenance.Tier(1), TierTo: provenance.Tier(2),
				Actor: config.Actor(), Reason: "sync: " + reason,
			}); aerr != nil {
				return aerr
			}
			fmt.Printf("REVOKED %s@%s — off PATH, moved to quarantine (%s)\n", name, version, reason)
			revoked++
		}
	}

	// Report Tier 1 entries not installed here (fetch candidates) — sync
	// never auto-fetches; that stays an explicit `smith fetch`.
	available := 0
	all, err := index.List(p.IndexTools)
	if err != nil {
		return err
	}
	for _, e := range all {
		if e.Tier != 1 {
			continue
		}
		if _, err := os.Stat(store.TrustedDir(p.StoreTrusted, e.Name, e.Version)); os.IsNotExist(err) {
			if available == 0 {
				fmt.Println("\ntrusted tools in the index not installed here:")
			}
			fmt.Printf("  %s@%s   (smith fetch %s)\n", e.Name, e.Version, e.Name)
			available++
		}
	}
	fmt.Printf("\nsync complete: %d revoked, %d available to fetch\n", revoked, available)
	return nil
}

// ---------------------------------------------------------------- gc

func cmdGC(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	olderThan := fs.String("older-than", "30d", "minimum age of a Tier 0 version before it is eligible (e.g. 7d, 24h)")
	apply := fs.Bool("yes", false, "actually delete (default is a dry run)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cutoff, err := parseAge(*olderThan)
	if err != nil {
		return err
	}
	now := time.Now().UTC()

	all, err := index.ListAll(p.IndexTools)
	if err != nil {
		return err
	}
	var victims []index.Entry
	for _, e := range all {
		if e.Tier != 0 {
			continue // only unpromoted authored tools are GC-eligible
		}
		added, perr := time.Parse(time.RFC3339, e.AddedAt)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "[smith] skip %s@%s: unparseable added_at %q\n", e.Name, e.Version, e.AddedAt)
			continue
		}
		if now.Sub(added) >= cutoff {
			victims = append(victims, e)
		}
	}

	// Stale drafts: a draft whose version is not registered in the index.
	var staleDrafts []string
	if dirs, derr := os.ReadDir(filepath.Join(p.Home, "drafts")); derr == nil {
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			m, merr := scaffold.LoadManifest(draftDir(p, d.Name()))
			if merr != nil {
				continue
			}
			if _, lerr := index.Load(p.IndexTools, m.Name, m.Version); lerr != nil {
				staleDrafts = append(staleDrafts, d.Name())
			}
		}
	}

	if len(victims) == 0 && len(staleDrafts) == 0 {
		fmt.Printf("nothing to collect (Tier 0 older than %s, unregistered drafts)\n", *olderThan)
		return nil
	}
	verb := "would remove"
	if *apply {
		verb = "removing"
	}
	for _, e := range victims {
		fmt.Printf("%s Tier 0 %s@%s (added %s)\n", verb, e.Name, e.Version, e.AddedAt)
		if *apply {
			if rerr := os.RemoveAll(store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)); rerr != nil {
				return rerr
			}
			if rerr := os.RemoveAll(filepath.Join(p.IndexTools, e.Name, e.Version)); rerr != nil {
				return rerr
			}
			// Prune the now-empty tool dir.
			os.Remove(filepath.Join(p.IndexTools, e.Name))
			if aerr := provenance.Append(p.ProvenanceLog, provenance.Entry{
				Event: "gc", Tool: e.Name, Version: e.Version,
				TierFrom: provenance.Tier(0), Actor: config.Actor(),
				Reason: "gc: unpromoted Tier 0 older than " + *olderThan,
			}); aerr != nil {
				return aerr
			}
		}
	}
	for _, name := range staleDrafts {
		fmt.Printf("%s stale draft %s\n", verb, name)
		if *apply {
			if rerr := os.RemoveAll(draftDir(p, name)); rerr != nil {
				return rerr
			}
		}
	}
	if !*apply {
		fmt.Println("\ndry run — re-run with --yes to delete")
	}
	return nil
}

// parseAge parses durations like "30d", "24h", "90m" into a time.Duration.
func parseAge(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || days < 0 {
			return 0, fmt.Errorf("invalid age %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid age %q (use 30d, 24h, ...)", s)
	}
	return d, nil
}

// ---------------------------------------------------------------- verify

type verifyResult struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
	Check   string `json:"check"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail,omitempty"`
}

func cmdVerify(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var results []verifyResult
	add := func(r verifyResult) { results = append(results, r) }

	// Every per-machine provenance chain must verify.
	logs, _ := filepath.Glob(filepath.Join(filepath.Dir(p.ProvenanceLog), "*.jsonl"))
	for _, lg := range logs {
		n, err := provenance.VerifyChain(lg)
		label := "provenance-chain:" + strings.TrimSuffix(filepath.Base(lg), ".jsonl")
		if err != nil {
			add(verifyResult{Check: label, OK: false, Detail: err.Error()})
		} else {
			add(verifyResult{Check: label, OK: true, Detail: fmt.Sprintf("%d entries", n)})
		}
	}

	// Index signature over the manifest set (present only after first publish).
	if present, err := sign.VerifyIndex(p.Home, p.IndexTools); present {
		if err != nil {
			add(verifyResult{Check: "index-signature", OK: false, Detail: err.Error()})
		} else {
			add(verifyResult{Check: "index-signature", OK: true})
		}
	} else {
		add(verifyResult{Check: "index-signature", OK: true, Detail: "not yet published (unsigned)"})
	}

	entries, err := index.List(p.IndexTools)
	if err != nil {
		return err
	}
	for _, e := range entries {
		base := store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)
		if e.Tier == 1 {
			base = store.TrustedDir(p.StoreTrusted, e.Name, e.Version)
		}
		for _, a := range append(append([]index.Artifact{}, e.Artifacts...), e.Docs...) {
			path := filepath.Join(base, a.Path)
			sum, err := store.HashFile(path)
			switch {
			case err != nil:
				add(verifyResult{Tool: e.Name, Version: e.Version, Check: "sha256:" + a.Path, OK: false, Detail: "missing: " + path})
			case sum != a.Sha256:
				add(verifyResult{Tool: e.Name, Version: e.Version, Check: "sha256:" + a.Path, OK: false, Detail: "MISMATCH pinned=" + a.Sha256 + " actual=" + sum})
			default:
				add(verifyResult{Tool: e.Name, Version: e.Version, Check: "sha256:" + a.Path, OK: true})
			}
		}
	}

	// PATH hygiene: quarantine must not be reachable from the live PATH, and
	// every trusted-bin symlink must resolve into the trusted store.
	if pathTouchesQuarantine(os.Getenv("PATH"), p.StoreQuarantine) {
		add(verifyResult{Check: "path-hygiene", OK: false, Detail: "quarantine dir is on PATH"})
	} else {
		add(verifyResult{Check: "path-hygiene", OK: true})
	}
	if links, err := os.ReadDir(p.TrustedBin); err == nil {
		for _, l := range links {
			full := filepath.Join(p.TrustedBin, l.Name())
			target, err := filepath.EvalSymlinks(full)
			trustedRoot, _ := filepath.EvalSymlinks(p.StoreTrusted)
			if err != nil || !strings.HasPrefix(target, trustedRoot+string(filepath.Separator)) {
				add(verifyResult{Check: "trusted-bin:" + l.Name(), OK: false, Detail: "symlink escapes trusted store"})
			} else {
				add(verifyResult{Check: "trusted-bin:" + l.Name(), OK: true})
			}
		}
	}

	failed := 0
	for _, r := range results {
		if !r.OK {
			failed++
		}
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return err
		}
	} else {
		for _, r := range results {
			mark := "ok  "
			if !r.OK {
				mark = "FAIL"
			}
			id := r.Check
			if r.Tool != "" {
				id = r.Tool + "@" + r.Version + " " + r.Check
			}
			fmt.Printf("%s  %s", mark, id)
			if r.Detail != "" {
				fmt.Printf("  (%s)", r.Detail)
			}
			fmt.Println()
		}
		fmt.Printf("%d checks, %d failed\n", len(results), failed)
	}
	if failed > 0 {
		os.Exit(1)
	}
	return nil
}
