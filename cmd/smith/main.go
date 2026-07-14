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
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"smith/internal/config"
	"smith/internal/index"
	"smith/internal/misex"
	"smith/internal/provenance"
	"smith/internal/sandbox"
	"smith/internal/scaffold"
	"smith/internal/store"
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
		err = fmt.Errorf("`smith publish` (signed index) lands in Phase 2")
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
  smith promote <name>[@<ver>]      human-gated: quarantine -> trusted (Tier 1, on PATH)
  smith demote <name>[@<ver>] --reason <why>   trusted -> quarantine, off PATH now
  smith verify [--json]             re-check sha256 pins + provenance chain + PATH hygiene
  smith new <name> [--runtime bash|python] [--desc <text>]
                                    scaffold an agent-friendly CLI draft (cli-creator)
  smith register <name>             sandbox-run the draft's self-test; pass -> Tier 0 + promotion request
  smith publish <name>@<ver>        (Phase 2) sign + push index entry

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
	if len(args) != 1 {
		return fmt.Errorf("usage: smith add <name>[@<version>]")
	}
	if err := p.EnsureLayout(); err != nil {
		return err
	}
	name, version := splitSpec(args[0])
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
		Name: name, Version: version, Tier: 2,
		Origin: "external", Source: "mise:" + spec,
		AddedAt:   nowUTC(),
		Artifacts: artifacts,
	}
	if err := index.Save(p.IndexTools, e); err != nil {
		return err
	}
	if err := provenance.Append(p.ProvenanceLog, provenance.Entry{
		Event: "add", Tool: name, Version: version,
		TierTo: provenance.Tier(2), Actor: config.Actor(), Origin: e.Source,
	}); err != nil {
		return err
	}
	fmt.Printf("added %s at Tier 2 (external quarantine)\n", spec)
	fmt.Printf("  store: %s\n", qdir)
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
		Name: m.Name, Version: m.Version, Tier: 0,
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
	fmt.Printf("%-20s %-14s %-12s %-10s %s\n", "NAME", "VERSION", "TIER", "ORIGIN", "ADDED")
	for _, e := range entries {
		fmt.Printf("%-20s %-14s %-12s %-10s %s\n", e.Name, e.Version, tierName[e.Tier], e.Origin, e.AddedAt)
	}
	return nil
}

// ---------------------------------------------------------------- run

func cmdRun(p config.Paths, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: smith run <name>[@<version>] [args...]")
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
	if e.Tier != 1 {
		wrapped, err := sandbox.Command(p.SandboxProfiles, "", argv)
		if err != nil {
			return err // never fall back to unsandboxed
		}
		fmt.Fprintf(os.Stderr, "[smith] tier %d tool — running sandboxed (no network, writes limited to CWD+TMP)\n", e.Tier)
		argv = wrapped
	}
	exe, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(exe, argv, os.Environ())
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
	qdir := store.QuarantineDir(p.StoreQuarantine, e.Name, e.Version)
	fmt.Printf("  store:  %s\n", qdir)
	for _, a := range e.Artifacts {
		fmt.Printf("  %s  sha256=%s\n", a.Path, a.Sha256)
	}
	fmt.Println("\nReview means reading the tool source / provenance — a passing self-test is not sufficient (§7).")

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

// promoteEntry performs the promotion mutations after the human gate has
// passed. Kept separate so the mechanics are testable without weakening the
// TTY gate above.
func promoteEntry(p config.Paths, e index.Entry, reviewer string) error {
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

	n, err := provenance.VerifyChain(p.ProvenanceLog)
	if err != nil {
		add(verifyResult{Check: "provenance-chain", OK: false, Detail: err.Error()})
	} else {
		add(verifyResult{Check: "provenance-chain", OK: true, Detail: fmt.Sprintf("%d entries", n)})
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
