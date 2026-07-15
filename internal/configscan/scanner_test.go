package configscan

import (
	"database/sql"
	"encoding/json"
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

func TestLoadCCSwitchProvidersReturnsDetachedNormalizedRecords(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "cc-switch.db")
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE providers(
		id TEXT, name TEXT, is_current BOOLEAN, settings_config TEXT,
		app_type TEXT, sort_index INTEGER, created_at INTEGER, updated_at INTEGER
	)`); err != nil {
		t.Fatal(err)
	}
	codexSettings := `{"config":"model='gpt-test'\nmodel_provider='openai'\n[model_providers.openai]\nbase_url='https://codex.example/v1'","auth":{"OPENAI_API_KEY":"sk-codex-secret"}}`
	claudeSettings := `{"env":{"ANTHROPIC_BASE_URL":"https://claude.example","ANTHROPIC_MODEL":"claude-test","ANTHROPIC_AUTH_TOKEN":"sk-claude-secret"}}`
	if _, err = db.Exec(`INSERT INTO providers VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, "provider-1", "Codex Provider", true, codexSettings, "codex", 1, 1, 1_700_000_000_000); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO providers VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, "provider-2", "Claude Provider", false, claudeSettings, "claude", 1, 1, "2026-07-14T12:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	s := &Scanner{CCSwitchDB: database, RuntimeDir: filepath.Join(dir, "runtime")}
	providers, err := s.LoadCCSwitchProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers=%+v", providers)
	}
	codex, claude := providers[1], providers[0]
	if codex.CLI != domain.CLICodex {
		codex, claude = claude, codex
	}
	if codex.ID != "provider-1" || codex.Provider != "openai" || codex.Model != "gpt-test" || codex.BaseURL != "https://codex.example/v1" || codex.APIKey != "sk-codex-secret" || codex.CodexConfig == "" || codex.UpdatedAt.IsZero() {
		t.Fatalf("unexpected Codex provider: %+v", codex)
	}
	if claude.ID != "provider-2" || claude.Provider != "anthropic-compatible" || claude.Model != "claude-test" || claude.APIKey != "sk-claude-secret" || claude.ClaudeEnv["ANTHROPIC_BASE_URL"] != "https://claude.example" || claude.UpdatedAt.IsZero() {
		t.Fatalf("unexpected Claude provider: %+v", claude)
	}
	serialized, err := json.Marshal(providers)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "secret") || strings.Contains(string(serialized), "model_provider") || strings.Contains(string(serialized), "ANTHROPIC_AUTH_TOKEN") {
		t.Fatalf("serialized providers leaked sensitive source data: %s", serialized)
	}
	if err = os.Remove(database); err != nil {
		t.Fatal(err)
	}
	if codex.APIKey != "sk-codex-secret" || claude.ClaudeEnv["ANTHROPIC_MODEL"] != "claude-test" {
		t.Fatal("loaded startup records unexpectedly depended on the removed SQLite database")
	}
}

func TestLoadCCSwitchProvidersSupportsSchemaWithoutUpdatedAt(t *testing.T) {
	database := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE providers(
		id TEXT, name TEXT, is_current BOOLEAN, settings_config TEXT,
		app_type TEXT, sort_index INTEGER, created_at INTEGER
	); INSERT INTO providers VALUES(
		'provider-1', 'Provider 1', 1,
		'{"config":"model=''gpt-test''"}', 'codex', 1, 1700000000000
	)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Scanner{CCSwitchDB: database, RuntimeDir: t.TempDir()}
	providers, err := s.LoadCCSwitchProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0].ID != "provider-1" || providers[0].UpdatedAt.IsZero() {
		t.Fatalf("providers=%+v", providers)
	}
}

func TestCCSwitchQueryDoesNotMisreportSQLiteErrorsAsTimeouts(t *testing.T) {
	database := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := sql.Open("sqlite3", database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE providers(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Scanner{CCSwitchDB: database, RuntimeDir: t.TempDir()}
	_, err = s.queryCCSwitch("SELECT missing_column FROM providers")
	if err == nil || strings.Contains(strings.ToLower(err.Error()), "timed out") {
		t.Fatalf("expected the SQLite error instead of a timeout, got %v", err)
	}
}

func TestProvidersAndResolveDoNotAccessCCSwitchSQLite(t *testing.T) {
	codexDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("model_provider='openai'"), 0600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(t.TempDir(), "cc-switch.db")
	if err := os.WriteFile(database, []byte("not a SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Scanner{CodexDir: codexDir, CCSwitchDB: database, RuntimeDir: t.TempDir()}
	providers, err := s.Providers(domain.CLICodex)
	if err != nil || len(providers) != 1 || !providers[0].Current {
		t.Fatalf("providers=%+v err=%v", providers, err)
	}
	if s.CCSwitchWarning() != "" {
		t.Fatalf("Providers accessed SQLite: warning=%q", s.CCSwitchWarning())
	}
	_, err = s.Resolve(domain.CLICodex, "cc-provider")
	if err == nil || !strings.Contains(err.Error(), "Redis provider store") {
		t.Fatalf("expected explicit Redis resolution error, got %v", err)
	}
	if s.CCSwitchWarning() != "" {
		t.Fatalf("Resolve accessed SQLite: warning=%q", s.CCSwitchWarning())
	}
}
