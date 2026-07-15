// Package misex shells out to mise for everything Toolsmith deliberately
// does not reimplement: backend resolution, download, checksum-verified
// install, cross-arch handling (§2, §4).
package misex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func run(args ...string) (string, error) {
	cmd := exec.Command("mise", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("mise %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Latest resolves the newest available version for a tool name via mise's
// registry (aqua-backed for most tools).
func Latest(name string) (string, error) {
	return run("latest", name)
}

// Install downloads and installs tool@version into mise's own store. mise
// performs the backend's checksum verification during this step. The tool is
// NOT activated on any PATH by this call. The downloaded archive is kept so
// smith can attestation-verify the exact bytes that were fetched.
func Install(spec string) error {
	cmd := exec.Command("mise", "install", spec)
	cmd.Env = append(os.Environ(), "MISE_ALWAYS_KEEP_DOWNLOAD=1")
	cmd.Stdout = os.Stderr // progress is diagnostics, keep stdout machine-clean
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Lock generates a cross-platform lockfile for spec at lockPath, capturing
// checksums for the given platforms (current platform always included by
// mise). It runs in a throwaway project so it never disturbs the caller's
// mise state, then moves the produced mise.lock to lockPath. Returns the
// number of platform entries locked.
func Lock(spec, lockPath string, platforms []string) error {
	name, _, _ := strings.Cut(spec, "@")
	ver := ""
	if i := strings.Index(spec, "@"); i >= 0 {
		ver = spec[i+1:]
	}
	work, err := os.MkdirTemp("", "smith-lock-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	toml := fmt.Sprintf("[tools]\n%q = %q\n", name, ver)
	if err := os.WriteFile(filepath.Join(work, "mise.toml"), []byte(toml), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "mise.lock"), nil, 0o644); err != nil {
		return err
	}
	trust := exec.Command("mise", "trust")
	trust.Dir = work
	trust.Stdout, trust.Stderr = os.Stderr, os.Stderr
	_ = trust.Run()

	args := []string{"lock"}
	if len(platforms) > 0 {
		args = append(args, "--platform", strings.Join(platforms, ","))
	}
	cmd := exec.Command("mise", args...)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "MISE_ALWAYS_KEEP_DOWNLOAD=1")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mise lock %s: %w", spec, err)
	}
	raw, err := os.ReadFile(filepath.Join(work, "mise.lock"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(lockPath, raw, 0o644)
}

// Registry returns the primary backend spec for a short tool name, e.g.
// "aqua:cli/cli" for gh. Empty when unknown.
func Registry(name string) string {
	out, err := run("registry", name)
	if err != nil {
		return ""
	}
	return strings.Fields(out)[0]
}

// DownloadsDir locates the kept download archive dir for an installed spec:
// <data>/installs/<n>/<v> (from `mise where`) -> <data>/downloads/<n>/<v>.
func DownloadsDir(spec string) (string, error) {
	where, err := Where(spec)
	if err != nil {
		return "", err
	}
	installs := where
	for i := 0; i < 2; i++ {
		installs = strings.TrimSuffix(installs, "/")
		if idx := strings.LastIndex(installs, "/"); idx >= 0 {
			installs = installs[:idx]
		}
	}
	if !strings.HasSuffix(installs, "/installs") {
		return "", fmt.Errorf("unexpected mise install layout: %s", where)
	}
	rel := strings.TrimPrefix(where, installs+"/")
	return strings.TrimSuffix(installs, "installs") + "downloads/" + rel, nil
}

// Where returns the install directory for an installed tool@version.
func Where(spec string) (string, error) {
	return run("where", spec)
}

// BinPaths returns the directories mise considers to hold the tool's
// executables — the backend knows the archive layout, we don't.
func BinPaths(spec string) ([]string, error) {
	out, err := run("bin-paths", spec)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			dirs = append(dirs, l)
		}
	}
	return dirs, nil
}

// Trust marks a project config path as trusted so mise will load it.
func Trust(dir string) error {
	cmd := exec.Command("mise", "trust")
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// EnvPath returns the PATH that mise would activate for dir.
func EnvPath(dir string) (string, error) {
	cmd := exec.Command("mise", "env", "-s", "bash")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("mise env: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "export PATH=") {
			v := strings.TrimPrefix(line, "export PATH=")
			return strings.Trim(v, "'\""), nil
		}
	}
	return "", nil
}
