// Package transport moves authored-tool blobs (tar.gz of a store version
// dir) to and from shared storage so a tool promoted on machine A is
// fetchable on machine B (§5.3, Goal 1). Two backends:
//
//	github-release  private repo release assets via the gh CLI (recommended)
//	local-dir       any synced directory (Syncthing, iCloud, NFS, ...)
package transport

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Transport interface {
	// Push uploads the blob and returns a locator for the index entry.
	Push(blobPath, assetName string) (locator string, err error)
	// Fetch downloads the blob a locator points at into dstPath.
	Fetch(locator, dstPath string) error
	// String describes the destination for messages.
	String() string
}

// Config mirrors the transport section of $SMITH_HOME/config.yaml.
type Config struct {
	Kind string `yaml:"kind"`           // github-release | local-dir
	Repo string `yaml:"repo,omitempty"` // owner/name (github-release)
	Path string `yaml:"path,omitempty"` // directory (local-dir)
}

func New(c Config) (Transport, error) {
	switch c.Kind {
	case "local-dir":
		if c.Path == "" {
			return nil, fmt.Errorf("transport local-dir needs `path` in config.yaml")
		}
		return &localDir{dir: c.Path}, nil
	case "github-release":
		if c.Repo == "" || !strings.Contains(c.Repo, "/") {
			return nil, fmt.Errorf("transport github-release needs `repo: owner/name` in config.yaml")
		}
		if _, err := exec.LookPath("gh"); err != nil {
			return nil, fmt.Errorf("github-release transport requires the gh CLI")
		}
		return &ghRelease{repo: c.Repo}, nil
	case "":
		return nil, fmt.Errorf("no transport configured — set `transport:` in $SMITH_HOME/config.yaml (kind: github-release + repo, or kind: local-dir + path)")
	default:
		return nil, fmt.Errorf("unknown transport kind %q", c.Kind)
	}
}

// ---------------------------------------------------------------- local-dir

type localDir struct{ dir string }

func (l *localDir) String() string { return "local-dir:" + l.dir }

func (l *localDir) Push(blobPath, assetName string) (string, error) {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(l.dir, assetName)
	if err := copyFile(blobPath, dst); err != nil {
		return "", err
	}
	return "local-dir:" + assetName, nil
}

func (l *localDir) Fetch(locator, dstPath string) error {
	name, ok := strings.CutPrefix(locator, "local-dir:")
	if !ok {
		return fmt.Errorf("locator %q is not local-dir", locator)
	}
	return copyFile(filepath.Join(l.dir, filepath.Base(name)), dstPath)
}

// ---------------------------------------------------------------- github-release

// ghRelease stores blobs as assets on a single rolling release tagged
// `smith-blobs` in a private repo. Assets are content-named, so the tag is
// just a bucket.
type ghRelease struct{ repo string }

const ghTag = "smith-blobs"

func (g *ghRelease) String() string { return "github-release:" + g.repo }

func (g *ghRelease) ensureRelease() error {
	check := exec.Command("gh", "release", "view", ghTag, "--repo", g.repo)
	check.Stdout, check.Stderr = io.Discard, io.Discard
	if check.Run() == nil {
		return nil
	}
	create := exec.Command("gh", "release", "create", ghTag, "--repo", g.repo,
		"--title", "smith tool blobs", "--notes", "Content-addressed archives of promoted smith tools. Managed by `smith promote`/`smith fetch`.")
	create.Stdout, create.Stderr = os.Stderr, os.Stderr
	if err := create.Run(); err != nil {
		return fmt.Errorf("creating release %s in %s: %w", ghTag, g.repo, err)
	}
	return nil
}

func (g *ghRelease) Push(blobPath, assetName string) (string, error) {
	if err := g.ensureRelease(); err != nil {
		return "", err
	}
	// Upload under the asset name (gh uses the file's basename).
	staged := filepath.Join(filepath.Dir(blobPath), assetName)
	if staged != blobPath {
		if err := copyFile(blobPath, staged); err != nil {
			return "", err
		}
		defer os.Remove(staged)
	}
	up := exec.Command("gh", "release", "upload", ghTag, staged, "--repo", g.repo, "--clobber")
	up.Stdout, up.Stderr = os.Stderr, os.Stderr
	if err := up.Run(); err != nil {
		return "", fmt.Errorf("uploading %s: %w", assetName, err)
	}
	return fmt.Sprintf("github-release:%s#%s#%s", g.repo, ghTag, assetName), nil
}

func (g *ghRelease) Fetch(locator, dstPath string) error {
	rest, ok := strings.CutPrefix(locator, "github-release:")
	if !ok {
		return fmt.Errorf("locator %q is not github-release", locator)
	}
	parts := strings.Split(rest, "#")
	if len(parts) != 3 {
		return fmt.Errorf("malformed github-release locator %q", locator)
	}
	repo, tag, asset := parts[0], parts[1], parts[2]
	dir, err := os.MkdirTemp("", "smith-fetch-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	dl := exec.Command("gh", "release", "download", tag, "--repo", repo,
		"--pattern", asset, "--dir", dir)
	dl.Stdout, dl.Stderr = os.Stderr, os.Stderr
	if err := dl.Run(); err != nil {
		return fmt.Errorf("downloading %s from %s@%s: %w", asset, repo, tag, err)
	}
	return copyFile(filepath.Join(dir, asset), dstPath)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ---------------------------------------------------------------- blobs

// PackBlob creates a deterministic-enough tar.gz of a tool version dir. The
// archive is hashed after creation; determinism across machines is not
// required, only integrity of the pushed bytes.
func PackBlob(srcDir, dstPath string) error {
	f, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	err = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name: filepath.ToSlash(rel),
			Mode: int64(info.Mode().Perm()),
			Size: info.Size(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gw.Close()
}

// UnpackBlob extracts a blob into dstDir, refusing path traversal.
func UnpackBlob(blobPath, dstDir string) error {
	f, err := os.Open(blobPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("blob contains escaping path %q — refusing", hdr.Name)
		}
		dst := filepath.Join(dstDir, clean)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode).Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
}
