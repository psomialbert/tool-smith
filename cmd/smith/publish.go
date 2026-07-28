package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"smith/internal/config"
	"smith/internal/scaffold"
)

// cmdPublish turns the current directory (a smith tool) into a shareable repo:
// git init/commit → create/push the GitHub remote → tag the manifest version →
// topic-tag it smith-tool. Idempotent and resumable; each step is skipped if
// already done. Human-owned (like merging an adoption): agents author, humans
// publish.
func cmdPublish(p config.Paths, args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	public := fs.Bool("public", false, "create a public repo")
	private := fs.Bool("private", false, "create a private repo")
	repoFlag := fs.String("repo", "", "owner/name for the repo (default: <gh-user>/<tool>)")
	tagFlag := fs.String("tag", "", "version tag to push (default: v<manifest.version>)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *public == *private { // both or neither
		return fmt.Errorf("specify exactly one of --public or --private")
	}

	m, err := publishValidate(".")
	if err != nil {
		return err
	}
	tag := publishTag(m, *tagFlag)

	if err := ensureGitRepo(".", m); err != nil {
		return err
	}

	repo := *repoFlag
	if !gitHasRemote("origin") {
		if _, err := exec.LookPath("gh"); err != nil {
			return fmt.Errorf("no `origin` remote and `gh` not found — install gh (or set a remote) to create the repo")
		}
		if repo == "" {
			user, uerr := ghUser()
			if uerr != nil {
				return fmt.Errorf("resolving gh user (set --repo owner/name): %w", uerr)
			}
			repo = user + "/" + m.Name
		}
		vis := "--private"
		if *public {
			vis = "--public"
		}
		fmt.Fprintf(os.Stderr, "[smith] creating %s (%s) and pushing...\n", repo, strings.TrimPrefix(vis, "--"))
		if err := runGit("gh", "repo", "create", repo, vis, "--source=.", "--remote=origin", "--push"); err != nil {
			return fmt.Errorf("gh repo create: %w", err)
		}
	} else {
		// Existing origin: we don't create the repo, so the --public/--private
		// flag can't be applied at creation. Verify it rather than silently
		// ignore it — refuse to push if the repo's actual visibility disagrees.
		if slug := originGitHubSlug(); slug != "" {
			if _, err := exec.LookPath("gh"); err == nil {
				actual := ghRepoVisibility(slug)
				if msg := visibilityMismatch(*public, actual); msg != "" {
					return fmt.Errorf("%s — change it in repo settings (or `gh repo edit %s --visibility %s`) before publishing", msg, slug, visKeyword(*public))
				}
				if actual == "" {
					fmt.Fprintf(os.Stderr, "[smith] note: could not verify origin visibility; ensure %s is %s\n", slug, visKeyword(*public))
				}
			}
		}
		if err := runGit("git", "push", "-u", "origin", "HEAD"); err != nil {
			return fmt.Errorf("git push: %w", err)
		}
	}

	if !gitTagExists(tag) {
		if err := runGit("git", "tag", tag); err != nil {
			return fmt.Errorf("git tag %s: %w", tag, err)
		}
	} else if !tagAtHead(tag) {
		// The tag already exists but points at an older commit — a re-publish
		// where the version wasn't bumped. `git tag` won't move it and we won't
		// force it, so what gets pushed (and later adopted at this tag) is the
		// OLD code, not what was just committed. Refuse rather than print a
		// misleading "published" for stale bytes.
		return fmt.Errorf("tag %s already exists but does not point at HEAD — bump the version in manifest.yaml (or pass --tag), so adopters at %s get the code you just committed", tag, tag)
	}
	if err := runGit("git", "push", "origin", tag); err != nil {
		return fmt.Errorf("pushing tag %s: %w", tag, err)
	}

	// Topic-tag for discovery (best-effort: don't fail a successful push).
	if repo == "" {
		repo = originGitHubSlug()
	}
	if repo != "" {
		if _, err := exec.LookPath("gh"); err == nil {
			if err := runGit("gh", "repo", "edit", repo, "--add-topic", "smith-tool"); err != nil {
				fmt.Fprintf(os.Stderr, "[smith] note: could not add the smith-tool topic (%v); add it in repo settings for `smith search`\n", err)
			}
		}
	}

	fmt.Printf("published %s %s\n", m.Name, tag)
	if repo != "" {
		fmt.Printf("  adopt it:  smith use github:%s@%s\n", repo, tag)
	} else {
		fmt.Printf("  adopt it:  smith use <origin-url>@%s\n", tag)
	}
	return nil
}

// publishValidate confirms dir is a well-formed smith tool.
func publishValidate(dir string) (scaffold.Manifest, error) {
	m, err := scaffold.LoadManifest(dir)
	if err != nil {
		return scaffold.Manifest{}, fmt.Errorf("not a smith tool dir (%w) — run `smith publish` from a tool's root", err)
	}
	for _, f := range []string{"selftest.sh", "SKILL.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			return scaffold.Manifest{}, fmt.Errorf("missing %s — a publishable tool needs a self-test and a SKILL.md", f)
		}
	}
	return m, nil
}

// publishTag derives the version tag: override wins; else v<manifest.version>
// (a leading v is kept, not doubled).
func publishTag(m scaffold.Manifest, override string) string {
	if override != "" {
		return override
	}
	if strings.HasPrefix(m.Version, "v") {
		return m.Version
	}
	return "v" + m.Version
}

// ensureGitRepo makes dir a git repo with the working tree committed.
func ensureGitRepo(dir string, m scaffold.Manifest) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		if err := runGit("git", "-C", dir, "init", "-q"); err != nil {
			return err
		}
	}
	if err := runGit("git", "-C", dir, "add", "-A"); err != nil {
		return err
	}
	// Refuse to commit anything that looks like a secret: `git add -A` would
	// otherwise sweep a stray private key (e.g. the keygen default
	// smith-allowlist.key) into a repo that may be pushed public.
	staged, err := stagedFiles(dir)
	if err != nil {
		return err
	}
	if bad := sensitiveStagedPaths(staged); len(bad) > 0 {
		return fmt.Errorf("refusing to publish: these staged files look like secrets: %s — remove them or add them to .gitignore before publishing", strings.Join(bad, ", "))
	}
	// Commit only if there's something staged (dirty tree or first commit).
	if exec.Command("git", "-C", dir, "diff", "--cached", "--quiet").Run() != nil {
		if err := runGit("git", "-C", dir, "commit", "-q", "-m", m.Name+" "+publishTag(m, "")); err != nil {
			return err
		}
	}
	return nil
}

// sensitiveStagedPaths returns the staged paths whose basename looks like a
// secret that must never be committed to a (possibly public) repo: the keygen
// default private key, any private-key/cert file, dotenv files, and SSH keys.
func sensitiveStagedPaths(staged []string) []string {
	var bad []string
	for _, p := range staged {
		base := filepath.Base(p)
		switch {
		case strings.HasSuffix(base, ".key"), strings.HasSuffix(base, ".pem"),
			strings.HasSuffix(base, ".p12"), strings.HasSuffix(base, ".pfx"),
			base == ".env" || strings.HasPrefix(base, ".env."),
			base == "id_rsa" || base == "id_dsa" || base == "id_ecdsa" || base == "id_ed25519":
			bad = append(bad, p)
		}
	}
	return bad
}

// stagedFiles lists paths staged for commit in dir (respects .gitignore, since
// git add -A already ran).
func stagedFiles(dir string) ([]string, error) {
	out, err := exec.Command("git", "-C", dir, "diff", "--cached", "--name-only", "-z").Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// visibilityMismatch reports why an existing repo's visibility disagrees with
// the requested --public/--private, or "" if it matches (or can't be
// determined). actual is gh's visibility string (public/private/internal, any
// case); internal counts as neither public nor private.
func visibilityMismatch(wantPublic bool, actual string) string {
	a := strings.ToLower(strings.TrimSpace(actual))
	if a == "" {
		return "" // unknown — caller warns rather than blocks
	}
	if wantPublic && a != "public" {
		return "origin repo is " + a + " but --public was requested"
	}
	if !wantPublic && a != "private" {
		return "origin repo is " + a + " but --private was requested"
	}
	return ""
}

// ghRepoVisibility returns the repo's visibility via gh, or "" if it can't be
// determined (gh missing, not authenticated, repo not found).
func ghRepoVisibility(slug string) string {
	out, err := exec.Command("gh", "repo", "view", slug, "--json", "visibility", "--jq", ".visibility").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func visKeyword(public bool) string {
	if public {
		return "public"
	}
	return "private"
}

// runGit runs a command, streaming stderr, returning an error on non-zero exit.
func runGit(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stderr = os.Stderr
	return c.Run()
}

func gitHasRemote(name string) bool {
	return exec.Command("git", "remote", "get-url", name).Run() == nil
}

func gitTagExists(tag string) bool {
	return exec.Command("git", "rev-parse", "-q", "--verify", "refs/tags/"+tag).Run() == nil
}

// tagAtHead reports whether tag resolves to the same commit as HEAD (so a
// re-publish at an unchanged version is distinguishable from a stale tag).
func tagAtHead(tag string) bool {
	tagRev, err := exec.Command("git", "rev-list", "-n", "1", "refs/tags/"+tag).Output()
	if err != nil {
		return false
	}
	headRev, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(tagRev)) == strings.TrimSpace(string(headRev))
}

func ghUser() (string, error) {
	out, err := exec.Command("gh", "api", "user", "--jq", ".login").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// originGitHubSlug returns owner/name if origin points at github.com, else "".
func originGitHubSlug() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return gitHubSlugFromURL(strings.TrimSpace(string(out)))
}

// gitHubSlugFromURL extracts owner/name from a github.com remote URL, or "".
// "github.com" must appear as the HOST — at the start, right after "@"
// (scp form), or right after "//" (scheme separator) — and be followed by
// ":" or "/". A single "/" before it is a path segment, so a non-github host
// with "github.com" in its PATH (https://evil.com/github.com/acme/x) or a
// look-alike host (mygithub.company.com) is not mistaken for github.com.
func gitHubSlugFromURL(url string) string {
	const host = "github.com"
	for off := 0; ; {
		rel := strings.Index(url[off:], host)
		if rel < 0 {
			return ""
		}
		i := off + rel
		before := i == 0 || url[i-1] == '@' || (i >= 2 && url[i-1] == '/' && url[i-2] == '/')
		end := i + len(host)
		after := end < len(url) && (url[end] == ':' || url[end] == '/')
		if before && after {
			slug := strings.TrimLeft(url[end:], ":/")
			return strings.TrimSuffix(slug, ".git")
		}
		off = i + 1
	}
}
