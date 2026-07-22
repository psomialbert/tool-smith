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
		if err := runGit("git", "push", "-u", "origin", "HEAD"); err != nil {
			return fmt.Errorf("git push: %w", err)
		}
	}

	if !gitTagExists(tag) {
		if err := runGit("git", "tag", tag); err != nil {
			return fmt.Errorf("git tag %s: %w", tag, err)
		}
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
	// Commit only if there's something staged (dirty tree or first commit).
	if exec.Command("git", "-C", dir, "diff", "--cached", "--quiet").Run() != nil {
		if err := runGit("git", "-C", dir, "commit", "-q", "-m", m.Name+" "+publishTag(m, "")); err != nil {
			return err
		}
	}
	return nil
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
	url := strings.TrimSpace(string(out))
	i := strings.Index(url, "github.com")
	if i < 0 {
		return ""
	}
	slug := url[i+len("github.com"):]
	slug = strings.TrimLeft(slug, ":/")
	return strings.TrimSuffix(slug, ".git")
}
