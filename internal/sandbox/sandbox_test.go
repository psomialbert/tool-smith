package sandbox

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSeatbeltProfileWriteGrant(t *testing.T) {
	prof := seatbeltProfile(Perms{WritePaths: []string{"/opt/cache", "/var/data"}})
	for _, want := range []string{
		`(allow file-write* (subpath "/opt/cache"))`,
		`(allow file-write* (subpath "/var/data"))`,
	} {
		if !strings.Contains(prof, want) {
			t.Fatalf("profile missing write grant %q:\n%s", want, prof)
		}
	}
	// Strict profile grants no extra paths.
	if strings.Contains(seatbeltProfile(Perms{}), "/opt/cache") {
		t.Fatal("strict profile must not grant extra write paths")
	}
}

func TestProfileNameDistinctPerPerms(t *testing.T) {
	if profileName(Perms{}) != "default.sb" {
		t.Fatal("strict → default.sb")
	}
	if profileName(Perms{Network: true}) != "net.sb" {
		t.Fatal("network → net.sb")
	}
	a := profileName(Perms{WritePaths: []string{"/a"}})
	b := profileName(Perms{WritePaths: []string{"/b"}})
	if a == b {
		t.Fatalf("distinct write-path perms must get distinct profile names (%s == %s)", a, b)
	}
	if !strings.HasPrefix(a, "p-") {
		t.Fatalf("write-path profile should be hash-named, got %q", a)
	}
	// Order-independent (sorted): same set → same name.
	if profileName(Perms{WritePaths: []string{"/a", "/b"}}) != profileName(Perms{WritePaths: []string{"/b", "/a"}}) {
		t.Fatal("profileName must be order-independent")
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
	strict := seatbeltProfile(Perms{})
	if !strings.Contains(strict, "(deny network*)") {
		t.Fatal("strict profile must deny network")
	}
	granted := seatbeltProfile(Perms{Network: true})
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

	env, err := Env(nil)
	if err != nil {
		t.Fatal(err)
	}
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
	env, err := Env([]string{"GH_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(env, "\n"), "GH_TOKEN=supersecret") {
		t.Fatal("explicit --pass-env var not forwarded")
	}
	if _, err := Env([]string{"NOT_SET_ANYWHERE_XYZ"}); err == nil {
		t.Fatal("pass-env of unset var should error, not silently forward nothing")
	}
}
