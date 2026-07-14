// Package sandbox wraps a command so Tier 0/2 tools only ever execute inside
// an OS sandbox (§7 invariant). macOS uses a Seatbelt profile via
// sandbox-exec; Linux uses bubblewrap. If no sandbox mechanism is available
// the caller must refuse to run — never fall back to unsandboxed execution.
package sandbox

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

//go:embed profiles/default.sb
var DefaultSeatbeltProfile string

// WriteDefaultProfile installs the embedded profile into the profiles dir if
// it is not already present (smith init).
func WriteDefaultProfile(profilesDir string) (string, error) {
	path := filepath.Join(profilesDir, "default.sb")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(DefaultSeatbeltProfile), 0o644); err != nil {
			return "", err
		}
	}
	return path, nil
}

// Command returns the full argv that runs `argv` sandboxed with writes
// confined to workDir (empty = current dir) and TMP, or an error if no
// sandbox mechanism exists on this machine.
func Command(profilesDir, workDir string, argv []string) ([]string, error) {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			return nil, fmt.Errorf("sandbox-exec not found; refusing to run quarantined tool unsandboxed")
		}
		profile, err := WriteDefaultProfile(profilesDir)
		if err != nil {
			return nil, err
		}
		cwd := workDir
		if cwd == "" {
			if cwd, err = os.Getwd(); err != nil {
				return nil, err
			}
		}
		if r, err := filepath.EvalSymlinks(cwd); err == nil {
			cwd = r
		}
		tmp := os.TempDir()
		if r, err := filepath.EvalSymlinks(tmp); err == nil {
			tmp = r
		}
		out := []string{"sandbox-exec", "-f", profile, "-D", "CWD=" + cwd, "-D", "TMP=" + tmp}
		return append(out, argv...), nil
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			return nil, fmt.Errorf("bwrap (bubblewrap) not found; refusing to run quarantined tool unsandboxed")
		}
		cwd := workDir
		if cwd == "" {
			var err error
			if cwd, err = os.Getwd(); err != nil {
				return nil, err
			}
		}
		out := []string{
			"bwrap",
			"--ro-bind", "/", "/",
			"--dev", "/dev",
			"--tmpfs", "/tmp",
			"--bind", cwd, cwd,
			"--unshare-net",
			"--die-with-parent",
		}
		return append(out, argv...), nil
	default:
		return nil, fmt.Errorf("no sandbox mechanism for %s; refusing to run quarantined tool", runtime.GOOS)
	}
}
