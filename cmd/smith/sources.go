package main

// Multi-file tool support. A tool's manifest may declare `sources:` to ship more
// than one source file; without it a tool keeps the legacy single-file set and a
// byte-identical bundle hash, because changing that hash would make `smith
// install` and `smith verify` refuse every tool already adopted anywhere.
//
// Everything here fails closed. The resolved list decides what gets vendored,
// hashed, re-verified, and materialized, so a path that slips through
// unvalidated is a path that lands in someone's repo outside the hash.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// maxSourceFiles / maxSourceBytes bound the review surface. The adoption PR
	// is the trust gate, so a tool nobody can read is a tool nobody can vouch
	// for. Generous enough that no honest tool hits them; tight enough that an
	// accidentally vendored dependency tree fails loudly instead of landing in
	// someone's project.
	maxSourceFiles = 200
	maxSourceBytes = 2 << 20 // 2 MiB
)

// resolveSources expands a manifest's `sources:` entries into a sorted list of
// relative file paths under dir, with the meta files appended. It returns an
// error rather than skipping anything: the list is author-written, so a typo
// must fail loudly instead of silently shipping a tool missing a file.
//
// Rejects, in every case with a message naming the offending path:
//   - absolute paths and any ".." segment (an entry is a path inside the repo)
//   - symlinks, as an entry or found while walking (a symlink in a tool repo can
//     point outside it, and the vendor copy would follow it, pulling arbitrary
//     host files into the project)
//   - a listed entry that does not exist
//   - an empty result, or one exceeding the file-count / byte caps
func resolveSources(dir string, sources []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	total := int64(0)

	add := func(rel string, size int64) error {
		rel = filepath.ToSlash(rel)
		if seen[rel] {
			return nil
		}
		seen[rel] = true
		out = append(out, rel)
		total += size
		if len(out) > maxSourceFiles {
			return fmt.Errorf("tool has more than %d source files — trim `sources:` in manifest.yaml; the adoption PR is the review gate, so the source has to stay readable", maxSourceFiles)
		}
		if total > maxSourceBytes {
			return fmt.Errorf("tool source exceeds %d bytes — trim `sources:` in manifest.yaml (vendoring a dependency tree or large fixtures is the usual cause)", maxSourceBytes)
		}
		return nil
	}

	for _, entry := range sources {
		clean, err := safeSourceEntry(entry)
		if err != nil {
			return nil, err
		}
		abs := filepath.Join(dir, clean)
		fi, err := os.Lstat(abs)
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("manifest `sources:` lists %q, which does not exist in the tool repo", entry)
		}
		if err != nil {
			return nil, err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			return nil, fmt.Errorf("manifest `sources:` entry %q is a symlink; refusing (it could point outside the tool repo)", entry)
		case fi.IsDir():
			if err := walkSourceDir(dir, clean, add); err != nil {
				return nil, err
			}
		default:
			if !fi.Mode().IsRegular() {
				return nil, fmt.Errorf("manifest `sources:` entry %q is not a regular file", entry)
			}
			if err := add(clean, fi.Size()); err != nil {
				return nil, err
			}
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("manifest `sources:` resolved to no files — a tool with no source is a packaging mistake")
	}
	// A multi-file Go tool is built as a package (`go build .`), which needs a
	// main module — unlike the legacy single-file `go build main.go`. go.mod and
	// go.sum are auto-included rather than left to the author to remember,
	// because they must be inside the hashed bundle: go.mod pins the module path
	// and Go version, so an unhashed one could change how the tool builds after
	// review.
	for _, f := range []string{"go.mod", "go.sum"} {
		if seen[f] {
			continue
		}
		fi, err := os.Lstat(filepath.Join(dir, f))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, f)
		seen[f] = true
	}
	// Meta files are part of every bundle, exactly as in the legacy list.
	for _, m := range metaParts {
		if seen[m] {
			continue
		}
		out = append(out, m)
	}
	sort.Strings(out)
	return out, nil
}

// safeSourceEntry validates one `sources:` entry and returns it cleaned.
func safeSourceEntry(entry string) (string, error) {
	if strings.TrimSpace(entry) == "" {
		return "", fmt.Errorf("manifest `sources:` contains an empty entry")
	}
	if filepath.IsAbs(entry) || strings.HasPrefix(entry, "/") {
		return "", fmt.Errorf("manifest `sources:` entry %q is an absolute path; entries are relative to the tool repo", entry)
	}
	clean := filepath.Clean(filepath.FromSlash(entry))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("manifest `sources:` entry %q escapes the tool repo", entry)
	}
	if clean == "." {
		return "", fmt.Errorf("manifest `sources:` entry %q is not allowed; list the files or subdirectories the tool needs", entry)
	}
	return clean, nil
}

// walkSourceDir adds every regular file under rel (relative to dir), refusing
// symlinks and anything irregular.
func walkSourceDir(dir, rel string, add func(string, int64) error) error {
	root := filepath.Join(dir, rel)
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%q under `sources:` is a symlink; refusing (it could point outside the tool repo)", filepath.ToSlash(relPath))
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%q under `sources:` is not a regular file", filepath.ToSlash(relPath))
		}
		return add(relPath, info.Size())
	})
}
