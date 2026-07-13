package configscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-watch/internal/domain"
)

func TestParseCodex(t *testing.T) {
	c := parseCodex("model = \"gpt-test\"\nmodel_provider = \"acme\"\n[model_providers.acme]\nbase_url = \"https://api.example/v1\"\napi_key_env_var = \"ACME_KEY\"\n")
	if c.Provider != "acme" || c.Model != "gpt-test" || c.BaseURL != "https://api.example/v1" || c.APIKeyEnv != "ACME_KEY" {
		t.Fatalf("unexpected parse: %+v", c)
	}
}

func TestCurrentCodexPreservesOAuthAuth(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model_provider='openai'"), 0600)
	auth := []byte(`{"tokens":{"access_token":"oauth-secret"}}`)
	_ = os.WriteFile(filepath.Join(dir, "auth.json"), auth, 0600)
	s := &Scanner{CodexDir: dir}
	cfg, err := s.Resolve(domain.CLICodex, "current")
	if err != nil {
		t.Fatal(err)
	}
	if string(cfg.AuthJSON) != string(auth) || cfg.LockIdentity == "" || strings.Contains(cfg.LockIdentity, "oauth-secret") {
		t.Fatalf("OAuth auth was not safely preserved: %+v", cfg)
	}
}

func TestCCSwitchQueryRetriesTransientSQLiteFailure(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	bin := filepath.Join(dir, "sqlite3-test")
	script := "#!/bin/sh\ncount=0\n[ -f '" + count + "' ] && count=$(cat '" + count + "')\ncount=$((count+1))\nprintf '%s' \"$count\" > '" + count + "'\nif [ \"$count\" -lt 3 ]; then echo 'database is locked' >&2; exit 11; fi\nprintf '[]'\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s := &Scanner{SQLiteBin: bin, CCSwitchDB: filepath.Join(dir, "cc-switch.db")}
	out, err := s.queryCCSwitch("SELECT 1")
	if err != nil || string(out) != "[]" {
		t.Fatalf("query did not recover: output=%q err=%v", out, err)
	}
}
