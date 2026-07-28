package sandbox

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSeatbeltProfileWriteGrant(t *testing.T) {
	prof := seatbeltProfile(Perms{WritePaths: []string{"/opt/cache", "/var/data"}}, nil)
	for _, want := range []string{
		`(allow file-write* (subpath "/opt/cache"))`,
		`(allow file-write* (subpath "/var/data"))`,
	} {
		if !strings.Contains(prof, want) {
			t.Fatalf("profile missing write grant %q:\n%s", want, prof)
		}
	}
	// Strict profile grants no extra paths.
	if strings.Contains(seatbeltProfile(Perms{}, nil), "/opt/cache") {
		t.Fatal("strict profile must not grant extra write paths")
	}
}

// A read-only path must be re-denied write access AFTER the broad TMP allow, so
// a clone dir under $TMPDIR can't be rewritten by its own self-test.
func TestSeatbeltProfileReadOnlyDenyOrder(t *testing.T) {
	prof := seatbeltProfile(Perms{}, []string{"/private/tmp/smith-adopt-x/repo"})
	deny := `(deny file-write* (subpath "/private/tmp/smith-adopt-x/repo"))`
	if !strings.Contains(prof, deny) {
		t.Fatalf("profile missing read-only deny %q:\n%s", deny, prof)
	}
	tmpAllow := `(allow file-write* (subpath (param "TMP")))`
	if strings.Index(prof, deny) < strings.Index(prof, tmpAllow) {
		t.Fatal("read-only deny must come AFTER the TMP allow (Seatbelt is last-match-wins)")
	}
}

// TestCloneStaysReadOnlyUnderWriteGrant is the cross-platform parity invariant
// behind the adopt-time tamper defense: even when the manifest grants write to
// a parent directory (e.g. /tmp) that contains the clone, the clone itself must
// stay read-only during the self-test, so the bytes that get vendored+pinned
// match the bytes that were tested. Both backends must enforce it, and the
// macOS test alone (TestSeatbeltProfileReadOnlyDenyOrder) missed that the Linux
// bwrap ordering had the opposite (broken) precedence.
func TestCloneStaysReadOnlyUnderWriteGrant(t *testing.T) {
	clone := "/tmp/smith-adopt-abc/repo"
	perms := Perms{WritePaths: []string{"/tmp"}} // a grant covering the clone's parent
	roPaths := []string{clone}

	// Linux/bwrap is last-mount-wins: the read-only re-bind of the clone must
	// come AFTER the read-write bind of the parent grant.
	argv := linuxBwrapArgv("/work", perms, roPaths, []string{"/bin/bash"})
	rwParent := lastMountIndex(argv, "--bind", "/tmp")
	roClone := lastMountIndex(argv, "--ro-bind", clone)
	if roClone < 0 {
		t.Fatalf("bwrap never re-bound the clone read-only: %v", argv)
	}
	if roClone < rwParent {
		t.Fatalf("bwrap: ro-bind of clone (idx %d) precedes rw --bind of /tmp (idx %d) — rw wins, clone is writable during self-test", roClone, rwParent)
	}

	// macOS/Seatbelt is last-match-wins: the write-DENY of the clone must come
	// AFTER the write-ALLOW of the parent grant.
	prof := seatbeltProfile(perms, roPaths)
	allowParent := strings.Index(prof, `(allow file-write* (subpath "/tmp"))`)
	denyClone := strings.Index(prof, `(deny file-write* (subpath "`+clone+`"))`)
	if denyClone < 0 {
		t.Fatalf("seatbelt never denied the clone:\n%s", prof)
	}
	if denyClone < allowParent {
		t.Fatalf("seatbelt: deny of clone (idx %d) precedes allow of /tmp (idx %d)", denyClone, allowParent)
	}
}

// lastMountIndex returns the index of the last "flag path ..." mount in argv, or
// -1. bwrap applies mounts left-to-right, so a larger index wins.
func lastMountIndex(argv []string, flag, path string) int {
	idx := -1
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == path {
			idx = i
		}
	}
	return idx
}

func TestProfileNameDistinctPerPerms(t *testing.T) {
	if profileName(Perms{}, nil) != "default.sb" {
		t.Fatal("strict → default.sb")
	}
	if profileName(Perms{Network: true}, nil) != "net.sb" {
		t.Fatal("network → net.sb")
	}
	a := profileName(Perms{WritePaths: []string{"/a"}}, nil)
	b := profileName(Perms{WritePaths: []string{"/b"}}, nil)
	if a == b {
		t.Fatalf("distinct write-path perms must get distinct profile names (%s == %s)", a, b)
	}
	if !strings.HasPrefix(a, "p-") {
		t.Fatalf("write-path profile should be hash-named, got %q", a)
	}
	// Order-independent (sorted): same set → same name.
	if profileName(Perms{WritePaths: []string{"/a", "/b"}}, nil) != profileName(Perms{WritePaths: []string{"/b", "/a"}}, nil) {
		t.Fatal("profileName must be order-independent")
	}
	// A read-only-path deny must also make the profile distinct + hash-named, so
	// a strict-looking run with a clone deny can't reuse the cached default.sb.
	ro := profileName(Perms{}, []string{"/private/tmp/repo"})
	if ro == "default.sb" || !strings.HasPrefix(ro, "p-") {
		t.Fatalf("read-only-path profile must be hash-named, got %q", ro)
	}
}

func TestFunctionalProbe(t *testing.T) {
	err := Functional(filepath.Join(t.TempDir(), "profiles"))
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if err == nil {
			t.Fatal("expected error on unsupported platform")
		}
		return
	}
	// darwin/linux: either it works, or it fails closed with a non-empty message.
	if err != nil && err.Error() == "" {
		t.Fatal("Functional returned a non-nil but empty error")
	}
}

func TestSeatbeltProfileNetworkGrant(t *testing.T) {
	strict := seatbeltProfile(Perms{}, nil)
	if !strings.Contains(strict, "(deny network*)") {
		t.Fatal("strict profile must deny network")
	}
	granted := seatbeltProfile(Perms{Network: true}, nil)
	if strings.Contains(granted, "(deny network*)") {
		t.Fatal("network-granted profile must not deny network")
	}
	// Both must still confine writes.
	for _, p := range []string{strict, granted} {
		if !strings.Contains(p, "(deny file-write*)") {
			t.Fatal("profile must deny writes by default")
		}
	}
}

func TestCommandAppliesPerms(t *testing.T) {
	dir := t.TempDir()
	strict, err := Command(dir, dir, Perms{}, []string{"echo", "hi"})
	if err != nil {
		t.Skipf("no sandbox on this host: %v", err)
	}
	net, err := Command(dir, dir, Perms{Network: true}, []string{"echo", "hi"})
	if err != nil {
		t.Fatal(err)
	}
	switch runtime.GOOS {
	case "linux":
		if !contains(strict, "--unshare-net") {
			t.Fatal("strict linux run must unshare-net")
		}
		if contains(net, "--unshare-net") {
			t.Fatal("network-granted linux run must not unshare-net")
		}
	case "darwin":
		// Different perms select different profile files.
		sp, np := argAfter(strict, "-f"), argAfter(net, "-f")
		if sp == "" || sp == np {
			t.Fatalf("expected distinct profiles for strict (%s) vs net (%s)", sp, np)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func argAfter(ss []string, flag string) string {
	for i, s := range ss {
		if s == flag && i+1 < len(ss) {
			return ss[i+1]
		}
	}
	return ""
}

func TestEnvStripsSecrets(t *testing.T) {
	t.Setenv("GH_TOKEN", "supersecret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "alsosecret")
	t.Setenv("LANG", "en_US.UTF-8")

	env, cleanup, err := Env(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "supersecret") || strings.Contains(joined, "GH_TOKEN") {
		t.Fatalf("secret leaked into sandbox env:\n%s", joined)
	}
	if !strings.Contains(joined, "LANG=en_US.UTF-8") {
		t.Fatal("allowlisted LANG missing")
	}
	if !strings.Contains(joined, "HOME=") || strings.Contains(joined, "HOME="+t.TempDir()[:5]+"..") {
		t.Fatal("scratch HOME missing")
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") && !strings.Contains(kv, "smith") {
			t.Fatalf("HOME does not point at a smith scratch dir: %s", kv)
		}
	}
}

func TestEnvExplicitPassthrough(t *testing.T) {
	t.Setenv("GH_TOKEN", "supersecret")
	env, cleanup, err := Env([]string{"GH_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !strings.Contains(strings.Join(env, "\n"), "GH_TOKEN=supersecret") {
		t.Fatal("explicit --pass-env var not forwarded")
	}
	if _, _, err := Env([]string{"NOT_SET_ANYWHERE_XYZ"}); err == nil {
		t.Fatal("pass-env of unset var should error, not silently forward nothing")
	}
}

// TestBuildFlagDoesNotAffectProfile: Perms.Build is a build-policy flag, not a
// mount grant — it must not change the seatbelt profile or its cache name, or a
// build-opted tool would collide with / evict a non-build profile.
func TestBuildFlagDoesNotAffectProfile(t *testing.T) {
	if seatbeltProfile(Perms{Build: true}, nil) != seatbeltProfile(Perms{}, nil) {
		t.Fatal("Build must not alter the seatbelt profile")
	}
	if profileName(Perms{Build: true}, nil) != profileName(Perms{}, nil) {
		t.Fatal("Build must not alter the profile cache name")
	}
}
