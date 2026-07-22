// smith — agent tools as git repos, adopted per project behind a trust gate.
//
// A tool is a git repo (source + manifest + selftest + SKILL.md). You author
// one with `smith new`, discover others with `smith search`, and adopt one
// into a project with `smith use` — which vendors its source under .smith/ and
// pins it, a reviewable commit like any dependency. `smith install` realizes
// that lockfile onto the project's PATH. Adoption (the PR) is the trust gate;
// until then a tool only runs sandboxed.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/term"

	"smith/internal/config"
	"smith/internal/reviewtui"
	"smith/internal/sandbox"
	"smith/internal/scaffold"
)

// version is set at build time via -ldflags "-X main.version=..." (see Makefile).
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if a := os.Args[1]; a == "version" || a == "--version" || a == "-v" {
		fmt.Println("smith " + version)
		return
	}
	paths, err := config.Resolve()
	if err != nil {
		fatal(err)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "new":
		err = cmdNew(paths, args)
	case "publish":
		err = cmdPublish(paths, args)
	case "use":
		err = cmdUse(paths, args)
	case "install":
		err = cmdInstall(paths, args)
	case "upgrade":
		err = cmdUpgrade(paths, args)
	case "diff":
		err = cmdDiff(paths, args)
	case "search":
		err = cmdSearch(paths, args)
	case "review":
		err = cmdReview(paths, args)
	case "list", "status":
		err = cmdList(paths, args)
	case "allowlist":
		err = cmdAllowlist(paths, args)
	case "migrate":
		err = cmdMigrate(paths, args)
	case "doctor":
		err = cmdDoctor(paths, args)
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
	fmt.Print(`smith — agent tools as git repos, adopted per project behind a trust gate

Usage:
  smith new <name> [--runtime bash|python|go|python-uv] [--desc <text>]
                                 scaffold a tool (a future git repo)
  smith publish (--public|--private) [--repo owner/name] [--tag v<x>]
                                 from a tool dir: git + create/push repo, tag,
                                 topic-tag smith-tool (make it adoptable)
  smith search [query]           discover tool repos (GitHub topic: smith-tool)
  smith use <repo|path>[@<ver>]  adopt a tool into this project: sandboxed
                                 self-test, vendor into .smith/tools/, pin in
                                 .smith/tools.yaml (a reviewable commit)
  smith review <repo|path|name> [--plain|--edit|--paths] [--editor <cmd>]
                                 read a tool's source (interactive TUI) before adopting
  smith install                  realize .smith/tools.yaml onto the project PATH
  smith upgrade [tool][@<ver>]   re-pin adopted tool(s) to a newer version (no
                                 arg = all → latest); self-tested, then install
  smith diff <tool>[@<ver>]      preview the source changes an upgrade would make
  smith list                     list tools adopted in this project
  smith allowlist                show the org adoption policy (if any)
  smith migrate                  clean up retired v1 files in SMITH_HOME
                                 (backs them up first; safe to re-run)
  smith doctor [--fix] [--json]  diagnose machine + project health; --fix repairs
                                 safe issues (activation, gitignore, v1 cruft)
  smith version                  print version

Trust is per-project: a tool vendored + pinned in the reviewed .smith/tools.yaml
is trusted for that project (on PATH via mise). Until adopted, tools run only
sandboxed. SMITH_HOME (` + defaultHomeHint() + `) holds generated sandbox profiles + config.yaml.
`)
}

func defaultHomeHint() string {
	if h := os.Getenv("SMITH_HOME"); h != "" {
		return h
	}
	return "~/.toolsmith"
}

func splitSpec(s string) (name, version string) {
	if i := strings.LastIndex(s, "@"); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// copyPlain copies a file, creating parent dirs, at 0644.
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

// ---------------------------------------------------------------- new

func cmdNew(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	runtime := fs.String("runtime", "bash", "tool runtime: bash | python | go | python-uv")
	desc := fs.String("desc", "", "one-line description (SKILL.md trigger)")
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
		return fmt.Errorf("usage: smith new <name> [--runtime bash|python|go|python-uv] [--desc <text>]")
	}
	if strings.ContainsAny(name, "/@ ") {
		return fmt.Errorf("tool name must be a plain identifier (no /, @, spaces)")
	}
	d := *desc
	if d == "" {
		d = "TODO: describe " + name
	}
	dir := name // scaffold into ./<name>, ready to become a git repo
	if err := scaffold.Create(dir, scaffold.Manifest{
		Name: name, Version: "0.1.0", Description: d, Runtime: *runtime,
	}); err != nil {
		return err
	}
	fmt.Printf("scaffolded tool: %s/\n", dir)
	fmt.Printf(`next steps:
  1. implement %s/%s and its selftest.sh; sharpen SKILL.md's trigger
  2. make it a repo:  cd %s && git init && git add -A && git commit -m "%s v0.1.0" && git tag v0.1.0
  3. publish it (own repo / org repo / embed in a project) and topic-tag it "smith-tool"
  4. adopt it:  smith use <repo-or-path>@v0.1.0   (from a project)
`, dir, name, dir, name)
	return nil
}

// ---------------------------------------------------------------- list

func cmdList(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lock, err := loadProjLock()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(lock.Tools))
	for n := range lock.Tools {
		names = append(names, n)
	}
	sort.Strings(names)
	if *asJSON {
		return jsonPrint(lock)
	}
	if len(names) == 0 {
		fmt.Println("no tools adopted in this project; try `smith use <repo>` or `smith search`")
		return nil
	}
	installed := func(n string) string {
		if _, err := os.Lstat(filepath.Join(smithDir, "bin", n)); err == nil {
			return "on PATH"
		}
		return "not installed (run `smith install`)"
	}
	fmt.Printf("%-16s %-10s %-10s %s\n", "TOOL", "VERSION", "RUNTIME", "STATUS")
	for _, n := range names {
		t := lock.Tools[n]
		fmt.Printf("%-16s %-10s %-10s %s\n", n, t.Version, t.Runtime, installed(n))
	}
	return nil
}

// ---------------------------------------------------------------- allowlist

func cmdAllowlist(p config.Paths, args []string) error {
	fc, err := p.LoadFileConfig()
	if err != nil {
		return err
	}
	pats, err := allowlistPatterns(fc)
	if err != nil {
		return err
	}
	cfg := filepath.Join(p.Home, "config.yaml")
	if len(pats) == 0 {
		fmt.Printf("no allowlist configured — any tool repo may be adopted.\n")
		fmt.Printf("to restrict, add to %s:\n  allowlist:\n    repos: [\"github.com/your-org/*\"]\n", cfg)
		return nil
	}
	fmt.Printf("adoption allowlist (from %s):\n", cfg)
	for _, pat := range pats {
		fmt.Printf("  %s\n", pat)
	}
	fmt.Println("only sources matching a pattern can be `smith use`d on this machine.")
	return nil
}

// ---------------------------------------------------------------- review

func cmdReview(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	inline := fs.Bool("inline", false, "force the interactive viewer (default on a TTY)")
	plain := fs.Bool("plain", false, "dump source to stdout (no TUI); default when piped")
	edit := fs.Bool("edit", false, "open the source in an editor")
	editor := fs.String("editor", "", "editor command (overrides $VISUAL/$EDITOR/config); implies --edit")
	paths := fs.Bool("paths", false, "print file paths only")
	var arg string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		arg, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if arg == "" && fs.NArg() == 1 {
		arg = fs.Arg(0)
	}
	if arg == "" {
		return fmt.Errorf("usage: smith review <repo|path|adopted-name> [--plain|--edit|--paths] [--editor <cmd>]")
	}

	// Resolve to a bundle dir: an adopted tool, a local dir, or a clone.
	base, cleanup, label, err := resolveReviewTarget(arg)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return err
	}
	m, merr := scaffold.LoadManifest(base)
	entrypoint := ""
	if merr == nil {
		entrypoint = m.Entrypoint
		label = fmt.Sprintf("%s@%s (%s)", m.Name, m.Version, m.Runtime)
	}
	fmt.Printf("== reviewing %s ==\n  source: %s\n", label, base)

	all, err := bundleFiles(base)
	if err != nil {
		return err
	}
	files := orderForReview(entrypoint, all)
	fmt.Printf("\nbundle (%d files):\n", len(files))
	for _, f := range files {
		fmt.Printf("  %s\n", f)
	}

	fc, _ := p.LoadFileConfig()
	ed := *editor
	for _, cand := range []string{os.Getenv("VISUAL"), os.Getenv("EDITOR"), fc.Editor} {
		if ed == "" {
			ed = cand
		}
	}

	tty := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	def := fc.Review.Default
	if def == "" {
		def = "tui"
	}
	mode := ""
	switch {
	case *edit || *editor != "":
		mode = "edit"
	case *paths:
		mode = "paths"
	case *plain:
		mode = "plain"
	case *inline:
		mode = "tui"
	case tty:
		mode = def
	default:
		mode = "plain"
	}
	if mode == "tui" && !tty {
		mode = "plain"
	}
	if mode != "tui" && mode != "plain" && mode != "edit" && mode != "paths" {
		return fmt.Errorf("config review.default %q is not one of tui|plain|edit|paths", def)
	}

	switch mode {
	case "paths":
		for _, f := range files {
			fmt.Println(filepath.Join(base, f))
		}
	case "edit":
		return openInEditor(ed, base, files)
	case "plain":
		return printInline(base, files)
	case "tui":
		return runReviewTUI(m.Name, m.Version, base, files, fc)
	}
	return nil
}

// resolveReviewTarget turns an argument into a bundle dir. Returns a cleanup
// func for temporary clones.
func resolveReviewTarget(arg string) (base string, cleanup func(), label string, err error) {
	if adopted := filepath.Join(smithDir, "tools", arg); dirHasManifest(adopted) {
		return adopted, nil, "adopted:" + arg, nil
	}
	if dirHasManifest(arg) {
		return arg, nil, arg, nil
	}
	// Treat as a repo to clone.
	tmp, terr := os.MkdirTemp("", "smith-review-")
	if terr != nil {
		return "", nil, "", terr
	}
	cleanup = func() { os.RemoveAll(tmp) }
	clone := filepath.Join(tmp, "repo")
	src := arg
	ver := ""
	if !strings.Contains(arg, "://") && !strings.Contains(arg, "git@") {
		if at := strings.LastIndex(arg, "@"); at > 0 {
			src, ver = arg[:at], arg[at+1:]
		}
	}
	cc := exec.Command("git", "clone", "--quiet", expandSource(src), clone)
	cc.Stderr = os.Stderr
	if cerr := cc.Run(); cerr != nil {
		return "", cleanup, "", fmt.Errorf("cloning %s: %w", src, cerr)
	}
	if ver != "" {
		co := exec.Command("git", "-C", clone, "checkout", "--quiet", ver)
		co.Stderr = os.Stderr
		if cerr := co.Run(); cerr != nil {
			return "", cleanup, "", fmt.Errorf("checkout %s: %w", ver, cerr)
		}
	}
	return clone, cleanup, arg, nil
}

func dirHasManifest(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, "manifest.yaml"))
	return err == nil && !fi.IsDir()
}

func runReviewTUI(name, version, base string, files []string, fc config.FileConfig) error {
	rf, err := loadReviewFiles(base, files)
	if err != nil {
		return err
	}
	theme := reviewtui.ThemeByName(fc.Review.Theme)
	if fc.Review.Color == "never" {
		theme = reviewtui.ThemeByName("mono")
	}
	m := reviewtui.New(name, version, rf)
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

func loadReviewFiles(base string, files []string) ([]reviewtui.File, error) {
	var rf []reviewtui.File
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(base, rel))
		if err != nil {
			return nil, err
		}
		if isBinary(data) {
			sum := sha256.Sum256(data)
			rf = append(rf, reviewtui.File{Name: rel, Lang: "generic",
				Lines: []string{fmt.Sprintf("(binary file, %d bytes, sha256=%s)", len(data), hex.EncodeToString(sum[:])[:16])}})
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

// orderForReview puts source first: the entrypoint, then selftest.sh, then the
// rest — so the reviewer reads what runs before the metadata.
func orderForReview(entrypoint string, all []string) []string {
	rank := func(f string) int {
		switch {
		case entrypoint != "" && f == entrypoint:
			return 0
		case strings.HasPrefix(f, "bin/") || f == "selftest.sh":
			return 1
		case f == "SKILL.md" || f == "manifest.yaml":
			return 3
		default:
			return 2
		}
	}
	out := append([]string(nil), all...)
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := rank(out[i]), rank(out[j]); ri != rj {
			return ri < rj
		}
		return out[i] < out[j]
	})
	return out
}

func bundleFiles(base string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.Contains(path, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			return nil
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

func printInline(base string, files []string) error {
	for _, f := range files {
		full := filepath.Join(base, f)
		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		fmt.Printf("\n===== %s =====\n", f)
		if isBinary(data) {
			sum := sha256.Sum256(data)
			fmt.Printf("(binary, %d bytes, sha256=%s)\n", len(data), hex.EncodeToString(sum[:])[:16])
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

// openInEditor opens files one at a time (advancing when each closes), which
// works the same across editors. Resolves --editor/$VISUAL/$EDITOR/config.
func openInEditor(ed, base string, files []string) error {
	full := make([]string, len(files))
	for i, f := range files {
		full[i] = filepath.Join(base, f)
	}
	if strings.TrimSpace(ed) == "" {
		fmt.Fprintln(os.Stderr, "[smith] no editor resolved. Set one of:")
		fmt.Fprintln(os.Stderr, "  export EDITOR=nano        (must be EXPORTed)")
		fmt.Fprintln(os.Stderr, "  editor: nano              in $SMITH_HOME/config.yaml")
		fmt.Fprintln(os.Stderr, "  smith review ... --editor nano")
		for _, f := range full {
			fmt.Println(f)
		}
		return nil
	}
	fields := strings.Fields(ed)
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

// ---------------------------------------------------------------- shared helpers (adopt.go)

// activationState classifies mise.local.toml without mutating it:
//
//	ok      – already activates binDir
//	missing – no file (create one)
//	stale   – a smith-managed _.path pointing elsewhere (heal it): a v1
//	          ~/.toolsmith store path, or a committed foreign .smith/bin
//	foreign – a user-authored _.path (do not touch)
//	inject  – file exists with no _.path (add one under [env])
func activationState(projectDir, binDir string) string {
	raw, err := os.ReadFile(filepath.Join(projectDir, "mise.local.toml"))
	if os.IsNotExist(err) {
		return "missing"
	}
	if err != nil {
		return "foreign" // unreadable: refuse to touch
	}
	text := string(raw)
	if strings.Contains(text, binDir) {
		return "ok"
	}
	if _, ok := healStaleActivation(strings.Split(text, "\n"), ""); ok {
		return "stale"
	}
	if strings.Contains(text, "_.path") {
		return "foreign"
	}
	return "inject"
}

// ensureActivation makes the project's mise.local.toml put binDir on PATH.
// Detection lives in activationState so `smith doctor` can diagnose without
// mutating; this applies the fix for each state.
func ensureActivation(projectDir, binDir string) error {
	pathLine := fmt.Sprintf("_.path = [%q]", binDir)
	path := filepath.Join(projectDir, "mise.local.toml")
	switch activationState(projectDir, binDir) {
	case "ok":
		return nil
	case "missing":
		return os.WriteFile(path, []byte("# managed by smith: puts adopted tools on PATH\n[env]\n"+pathLine+"\n"), 0o644)
	case "stale":
		raw, _ := os.ReadFile(path)
		lines, _ := healStaleActivation(strings.Split(string(raw), "\n"), pathLine)
		fmt.Fprintf(os.Stderr, "[smith] healed stale activation in mise.local.toml → %s\n", binDir)
		return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
	case "foreign":
		return fmt.Errorf("mise.local.toml already sets env._.path; add %q to it manually", binDir)
	default: // inject
		raw, _ := os.ReadFile(path)
		text := string(raw)
		if i := strings.Index(text, "[env]"); i >= 0 {
			at := i + len("[env]")
			text = text[:at] + "\n" + pathLine + text[at:]
		} else {
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += "\n"
			}
			text += "\n[env]\n" + pathLine + "\n"
		}
		return os.WriteFile(path, []byte(text), 0o644)
	}
}

// healStaleActivation rewrites the `_.path` line of a smith-managed
// mise.local.toml to newLine, preserving the rest of the file. A file is
// "ours" iff it carries the `managed by smith` header — which both the v1
// (`.toolsmith` store) and v2 (`.smith/bin`) writers emit — so this heals both
// a v1 leftover and a committed file bearing another machine's absolute path.
// A genuinely user-authored file (no smith header) is left untouched, so we
// never clobber someone's own `_.path`. Returns (lines, true) if it rewrote.
func healStaleActivation(lines []string, newLine string) ([]string, bool) {
	managed := false
	for _, ln := range lines {
		if strings.Contains(ln, "managed by smith") {
			managed = true
			break
		}
	}
	if !managed {
		return lines, false
	}
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "_.path") {
			lines[i] = newLine
			return lines, true
		}
	}
	return lines, false
}

// runSelfTest executes <dir>/selftest.sh <entry> sandboxed, from a scratch
// workdir, with a strict (no-network) profile and sanitized env. Unreviewed
// code stays contained while it's being adopted.
func runSelfTest(p config.Paths, dir, entry string) error {
	st := filepath.Join(dir, "selftest.sh")
	if _, err := os.Stat(st); err != nil {
		return fmt.Errorf("selftest.sh missing: a tool without a self-test cannot be adopted")
	}
	work, err := os.MkdirTemp("", "smith-selftest-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	argv, err := sandbox.Command(p.SandboxProfiles, work, sandbox.Perms{}, []string{"/bin/bash", st, entry})
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = work
	cmd.Env, err = sandbox.Env(nil)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	fmt.Fprintf(os.Stderr, "[smith] running self-test sandboxed in %s\n", work)
	return cmd.Run()
}
