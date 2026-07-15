package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"ai-watch/internal/configscan"
	"ai-watch/internal/domain"
	"ai-watch/internal/secureconfig"
	"ai-watch/internal/store"

	"github.com/alicebob/miniredis/v2"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
)

func TestCCSwitchStartupSyncPersistsRuntimeSnapshotAndFallsBack(t *testing.T) {
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
	settings := `{"config":"model='gpt-test'\nmodel_provider='openai'\n[model_providers.openai]\nbase_url='https://codex.example/v1'","auth":{"OPENAI_API_KEY":"startup-sync-secret"}}`
	if _, err = db.Exec(`INSERT INTO providers VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, "synced-provider", "Synced", true, settings, "codex", 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	redisServer := miniredis.RunT(t)
	st := store.NewRedisWithClient(t.TempDir(), "startup", redis.NewClient(&redis.Options{Addr: redisServer.Addr()}), []byte("0123456789abcdef0123456789abcdef"))
	if err = st.Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	scanner := &configscan.Scanner{CCSwitchDB: database, RuntimeDir: filepath.Join(dir, "runtime")}
	syncCCSwitchProviders(scanner, st)
	providers, err := st.ListCCSwitchProviders()
	if err != nil || len(providers) != 1 || providers[0].APIKey != "startup-sync-secret" {
		t.Fatalf("synced providers=%+v err=%v", providers, err)
	}
	if err = os.Remove(database); err != nil {
		t.Fatal(err)
	}
	service := secureconfig.New(st, scanner, "")
	resolved, err := service.Resolve(domain.CLICodex, "synced-provider")
	if err != nil || resolved.APIKey != "startup-sync-secret" || resolved.Source != "cc-switch-redis" {
		t.Fatalf("runtime resolution after SQLite removal=%+v err=%v", resolved, err)
	}
	syncCCSwitchProviders(scanner, st)
	status, err := st.LoadCCSwitchSyncStatus()
	if err != nil || status.SourceAvailable || status.LastSuccessAt == nil || status.Count != 1 {
		t.Fatalf("missing-source fallback status=%+v err=%v", status, err)
	}
	providers, err = st.ListCCSwitchProviders()
	if err != nil || len(providers) != 1 {
		t.Fatalf("missing source discarded snapshot: providers=%+v err=%v", providers, err)
	}

	if err = os.WriteFile(database, []byte("not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	brokenScanner := &configscan.Scanner{CCSwitchDB: database, RuntimeDir: filepath.Join(dir, "broken-runtime")}
	syncCCSwitchProviders(brokenScanner, st)
	status, err = st.LoadCCSwitchSyncStatus()
	if err != nil || !status.SourceAvailable || status.Warning == "" || status.LastSuccessAt == nil {
		t.Fatalf("failed-sync status=%+v err=%v", status, err)
	}
	providers, err = st.ListCCSwitchProviders()
	if err != nil || len(providers) != 1 || providers[0].ID != "synced-provider" {
		t.Fatalf("failed sync replaced last snapshot: providers=%+v err=%v", providers, err)
	}
}
