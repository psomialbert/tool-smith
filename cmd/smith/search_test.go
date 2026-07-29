package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// stubGH puts a fake `gh` first on PATH that exits with code and writes msg to
// stderr, so search's error handling can be exercised without a real gh/network.
func stubGH(t *testing.T, code int, msg string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh-based gh stub is POSIX-only")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n"
	if msg != "" {
		script += "echo " + msg + " >&2\n"
	}
	script += "exit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// gh exits 4 when it isn't authenticated. A bare "exit status 4" tells a
// newcomer nothing, so search must name the cause and point at the path that
// works without auth.
func TestSearchUnauthenticatedGHExplainsAuth(t *testing.T) {
	p := testPaths(t)
	stubGH(t, 4, "")

	err := cmdSearch(p, nil)
	if err == nil {
		t.Fatal("expected an error when gh is unauthenticated")
	}
	got := err.Error()
	if !strings.Contains(got, "gh auth login") {
		t.Errorf("error should point at `gh auth login`, got: %s", got)
	}
	if !strings.Contains(got, "smith use") {
		t.Errorf("error should note that `smith use` works unauthenticated, got: %s", got)
	}
	if strings.Contains(got, "exit status 4") {
		t.Errorf("bare exit status should be replaced by an explanation, got: %s", got)
	}
}

// Any other gh failure should surface gh's own stderr rather than swallowing it.
func TestSearchSurfacesGHStderr(t *testing.T) {
	p := testPaths(t)
	stubGH(t, 1, "some-upstream-detail")

	err := cmdSearch(p, nil)
	if err == nil {
		t.Fatal("expected an error when gh fails")
	}
	if !strings.Contains(err.Error(), "some-upstream-detail") {
		t.Errorf("gh stderr should be surfaced, got: %s", err.Error())
	}
}

// A missing gh is a different failure from an unauthenticated one, and should
// still tell the user adoption does not require gh.
func TestSearchMissingGHMentionsUnauthenticatedPath(t *testing.T) {
	p := testPaths(t)
	t.Setenv("PATH", t.TempDir()) // no gh anywhere

	err := cmdSearch(p, nil)
	if err == nil {
		t.Fatal("expected an error when gh is absent")
	}
	if !strings.Contains(err.Error(), "smith use") {
		t.Errorf("error should note adoption works without gh, got: %s", err.Error())
	}
}
