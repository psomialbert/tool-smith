package main

import (
	"os"
	"strings"
	"testing"
)

func TestMiseToolName(t *testing.T) {
	cases := map[string]string{
		"ubi:DataDog/pup":   "pup",
		"go:github.com/x/y": "y",
		"aqua:owner/repo":   "repo",
		"node":              "node",
		"cargo:ripgrep":     "ripgrep",
	}
	for spec, want := range cases {
		if got := miseToolName(spec); got != want {
			t.Errorf("miseToolName(%q) = %q, want %q", spec, got, want)
		}
	}
}

func TestIsMise(t *testing.T) {
	if !(ProjTool{Runtime: "mise"}).isMise() {
		t.Fatal("runtime mise should be external")
	}
	if (ProjTool{Runtime: "bash"}).isMise() {
		t.Fatal("bash tool is not external")
	}
}

func TestVerifyTreatsMiseAsExternal(t *testing.T) {
	t.Chdir(t.TempDir())
	saveProjLock(ProjLock{Tools: map[string]ProjTool{
		"pup": {Source: "mise:ubi:DataDog/pup", Version: "1.2.3", Runtime: "mise"},
	}})
	// No .smith/tools/pup dir exists; verify must NOT fail on the external tool.
	out := captureStdout(t, func() error { return cmdVerify(testPaths(t), nil) })
	if !strings.Contains(out, "external") {
		t.Fatalf("expected external label for the mise tool:\n%s", out)
	}
}

func TestDoctorSkipsMiseEntries(t *testing.T) {
	t.Chdir(t.TempDir())
	os.MkdirAll(smithDir, 0o755)
	lock := ProjLock{Tools: map[string]ProjTool{
		"pup": {Source: "mise:ubi:DataDog/pup", Version: "1.2.3", Runtime: "mise"},
	}}
	saveProjLock(lock)
	dc := docCtx{projectDir: ".", lock: lock}
	// vendored-sha and installed must not flag the external tool (no vendored
	// source, not on .smith/bin).
	for _, cn := range []string{"vendored-sha", "installed"} {
		for _, f := range findCheck(cn).diagnose(dc) {
			if strings.Contains(f.Message, "pup") && f.Severity != sevOK {
				t.Fatalf("%s should skip the mise tool, got %+v", cn, f)
			}
		}
	}
}
