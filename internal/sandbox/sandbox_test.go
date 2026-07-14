package sandbox

import (
	"strings"
	"testing"
)

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
