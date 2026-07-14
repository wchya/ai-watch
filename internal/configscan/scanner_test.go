package configscan

import (
	"database/sql"
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

func TestCCSwitchQueryUsesNativeReadOnlySQLite(t *testing.T) {
	database := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE providers(id TEXT, name TEXT); INSERT INTO providers VALUES('p1', 'Provider 1')`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s := &Scanner{CCSwitchDB: database, RuntimeDir: t.TempDir()}
	out, err := s.queryCCSwitch("SELECT id, name FROM providers")
	if err != nil || !strings.Contains(string(out), `"id":"p1"`) {
		t.Fatalf("native query failed: output=%q err=%v", out, err)
	}
}

func TestProvidersFallBackToLastSanitizedCCSwitchSnapshot(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "cc-switch.db")
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE providers(
		id TEXT, name TEXT, is_current BOOLEAN, settings_config TEXT,
		app_type TEXT, sort_index INTEGER, created_at INTEGER
	)`); err != nil {
		t.Fatal(err)
	}
	settings := `{"config":"model='gpt-test'\nmodel_provider='openai'","auth":{"OPENAI_API_KEY":"sk-cache-secret"}}`
	if _, err = db.Exec(`INSERT INTO providers VALUES(?, ?, ?, ?, ?, ?, ?)`, "provider-1", "Cached Provider", true, settings, "codex", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s := &Scanner{CCSwitchDB: database, RuntimeDir: filepath.Join(dir, "runtime"), providerCache: map[domain.CLI][]domain.Provider{}}
	providers, err := s.Providers(domain.CLICodex)
	if err != nil || len(providers) != 1 || providers[0].Name != "Cached Provider" || strings.Contains(providers[0].MaskedKey, "cache-secret") {
		t.Fatalf("initial providers=%+v err=%v", providers, err)
	}
	if err = os.WriteFile(database, []byte("not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	s.queryMu.Lock()
	s.queryCache = map[string]ccQueryCache{}
	s.queryMu.Unlock()
	providers, err = s.Providers(domain.CLICodex)
	if err != nil || len(providers) != 1 || providers[0].Name != "Cached Provider" || s.CCSwitchWarning() == "" {
		t.Fatalf("cached providers=%+v warning=%q err=%v", providers, s.CCSwitchWarning(), err)
	}
}
