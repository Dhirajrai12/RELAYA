package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("# comment\n\nRLY_A=plain\nRLY_B=\"quoted value\"\nexport RLY_C='single'\nRLY_D=from-file\nRLY_E=a=b==\n"), 0o600)
	t.Setenv("RLY_D", "from-env")
	for _, k := range []string{"RLY_A", "RLY_B", "RLY_C", "RLY_E"} {
		t.Setenv(k, "") // registers cleanup
		os.Unsetenv(k)
	}

	if err := LoadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"RLY_A": "plain", "RLY_B": "quoted value", "RLY_C": "single", "RLY_D": "from-env", "RLY_E": "a=b=="}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	if err := LoadEnvFile(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Errorf("missing file: %v", err)
	}
}
