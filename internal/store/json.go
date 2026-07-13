package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"ai-watch/internal/domain"

	_ "github.com/mattn/go-sqlite3"
)

const (
	databaseName      = "ai-watch.db"
	maxEventMessage   = 4 << 10
	maxEventDataBytes = 32 << 10
	maxSchedules      = 200
)

var ErrScheduleLimit = errors.New("schedule limit reached")

var (
	forbiddenEventKey = regexp.MustCompile(`(?i)(^|[_-])(api[_-]?key|auth|authorization|credential|output|prompt|secret|token|webhook)([_-]|$)`)
	credentialValue   = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{8,}|access_token=|bearer\s+[a-z0-9._~+/=-]{8,})`)
	providerExampleID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	scheduleID        = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
)

// JSON keeps the original public type name so the jobs manager remains source
// compatible. SQLite is now the durable store; JSON files are read only by the
// one-time legacy migration.
type JSON struct {
	mu      sync.Mutex
	dir     string
	dbPath  string
	db      *sql.DB
	initErr error
}

type Event struct {
	ID         int64          `json:"id"`
	At         time.Time      `json:"at"`
	Type       string         `json:"type"`
	Level      string         `json:"level,omitempty"`
	ProviderID string         `json:"providerId,omitempty"`
	JobID      string         `json:"jobId,omitempty"`
	Message    string         `json:"message,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
}

type EventFilter struct {
	ProviderID string
	JobID      string
	Type       string
	Level      string
	Since      time.Time
	Until      time.Time
	Limit      int
	Offset     int
}

type EventRetention struct {
	MaxAge   time.Duration
	MaxRows  int
	MaxBytes int64
	// Now is primarily useful for deterministic retention runs and tests. A
	// zero value uses the current time.
	Now time.Time
}

type RetentionResult struct {
	Deleted int64 `json:"deleted"`
	Count   int64 `json:"count"`
	Bytes   int64 `json:"bytes"`
}

func New(dir string) *JSON {
	s := &JSON{dir: dir, dbPath: filepath.Join(dir, databaseName)}
	s.initErr = s.open()
	return s
}

func (s *JSON) path(name string) string { return filepath.Join(s.dir, name) }

func (s *JSON) open() error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	_, statErr := os.Stat(s.dbPath)
	freshDatabase := errors.Is(statErr, os.ErrNotExist)
	dsn := "file:" + s.dbPath + "?_busy_timeout=5000&_foreign_keys=on&_synchronous=NORMAL"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = db.Ping(); err != nil {
		_ = db.Close()
		return fmt.Errorf("ping sqlite: %w", err)
	}
	if freshDatabase {
		if _, err = db.Exec(`PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
			_ = db.Close()
			return fmt.Errorf("enable incremental auto vacuum: %w", err)
		}
		if _, err = db.Exec(`VACUUM`); err != nil {
			_ = db.Close()
			return fmt.Errorf("initialize incremental auto vacuum: %w", err)
		}
	}
	for name, value := range map[string]int64{
		"journal_size_limit": 4 << 20,
		"wal_autocheckpoint": 256,
	} {
		if _, err = db.Exec(fmt.Sprintf("PRAGMA %s=%d", name, value)); err != nil {
			_ = db.Close()
			return fmt.Errorf("configure sqlite %s: %w", name, err)
		}
	}
	if _, err = db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		_ = db.Close()
		return fmt.Errorf("enable sqlite wal: %w", err)
	}
	s.db = db
	if err = s.migrate(); err != nil {
		_ = db.Close()
		s.db = nil
		return err
	}
	s.removeLegacyFiles()
	s.ensurePrivateFiles()
	return nil
}

func (s *JSON) removeLegacyFiles() {
	for _, name := range []string{"settings.json", "summaries.json"} {
		_ = os.Remove(s.path(name))
	}
}

func (s *JSON) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return s.initErr
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *JSON) Err() error { return s.initErr }

func (s *JSON) ready() error {
	if s.initErr != nil {
		return s.initErr
	}
	if s.db == nil {
		return errors.New("sqlite store is closed")
	}
	return nil
}

func (s *JSON) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at_ns INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	rows, err := s.db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema versions: %w", err)
	}
	applied := map[int]bool{}
	for rows.Next() {
		var version int
		if err = rows.Scan(&version); err != nil {
			rows.Close()
			return fmt.Errorf("scan schema version: %w", err)
		}
		applied[version] = true
	}
	if err = rows.Close(); err != nil {
		return fmt.Errorf("close schema versions: %w", err)
	}
	if !applied[1] {
		if err := s.applySchemaV1(); err != nil {
			return err
		}
	}
	if !applied[2] {
		if err := s.migrateLegacyJSON(); err != nil {
			return err
		}
	}
	if !applied[3] {
		if err := s.applySettingsRetentionV3(); err != nil {
			return err
		}
	}
	if !applied[4] {
		if err := s.applyProviderExamplesV4(); err != nil {
			return err
		}
	}
	if !applied[5] {
		if err := s.applySchedulesV5(); err != nil {
			return err
		}
	}
	if !applied[6] {
		if err := s.applyNotificationSettingsV6(); err != nil {
			return err
		}
	}
	if !applied[7] {
		if err := s.applyJobRunOnceV7(); err != nil {
			return err
		}
	}
	return nil
}

func (s *JSON) applyJobRunOnceV7() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin job run-once migration: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`ALTER TABLE job_summaries ADD COLUMN run_once INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return fmt.Errorf("add job run-once column: %w", err)
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(7, ?)`, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("record job run-once migration: %w", err)
	}
	return tx.Commit()
}

func (s *JSON) applyNotificationSettingsV6() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin notification settings migration: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`ALTER TABLE settings ADD COLUMN keepalive_summary_seconds INTEGER NOT NULL DEFAULT 3600`,
		`ALTER TABLE settings ADD COLUMN keepalive_summary_successes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE settings ADD COLUMN probe_progress_seconds INTEGER NOT NULL DEFAULT 3600`,
		`ALTER TABLE settings ADD COLUMN recovery_merge_seconds INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err = tx.Exec(statement); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
				return fmt.Errorf("add notification settings column: %w", err)
			}
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(6, ?)`, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("record notification settings migration: %w", err)
	}
	return tx.Commit()
}

func (s *JSON) applySchedulesV5() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin schedules migration: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS schedules (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		cli TEXT NOT NULL CHECK (cli IN ('codex', 'claude')),
		provider_id TEXT NOT NULL,
		mode TEXT NOT NULL CHECK (mode IN ('probe', 'keepalive')),
		timezone TEXT NOT NULL,
		weekdays_mask INTEGER NOT NULL,
		start_minute INTEGER NOT NULL,
		end_minute INTEGER NOT NULL,
		until_success INTEGER NOT NULL DEFAULT 1,
		timeout_seconds INTEGER NOT NULL,
		retry_interval_seconds INTEGER NOT NULL,
		keepalive_interval_seconds INTEGER NOT NULL,
		failure_threshold INTEGER NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		fallback_model TEXT NOT NULL DEFAULT '',
		last_occurrence_key TEXT NOT NULL DEFAULT '',
		last_status TEXT NOT NULL DEFAULT '',
		last_job_id TEXT NOT NULL DEFAULT '',
		last_run_at_ns INTEGER,
		created_at_ns INTEGER NOT NULL,
		updated_at_ns INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schedules table: %w", err)
	}
	if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_schedules_enabled_mode ON schedules(enabled, mode, cli, provider_id)`); err != nil {
		return fmt.Errorf("index schedules: %w", err)
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(5, ?)`, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("record schedules migration: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit schedules migration: %w", err)
	}
	return nil
}

func (s *JSON) applyProviderExamplesV4() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin provider examples migration: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS provider_examples (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		cli TEXT NOT NULL CHECK (cli IN ('codex', 'claude')),
		base_url TEXT NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		updated_at_ns INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create provider examples table: %w", err)
	}
	if _, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_provider_examples_cli_name ON provider_examples(cli, name, id)`); err != nil {
		return fmt.Errorf("index provider examples: %w", err)
	}
	now := time.Now().UTC()
	for offset, example := range defaultProviderExamples() {
		if err = upsertProviderExampleTx(tx, example, now.Add(time.Duration(offset)*time.Nanosecond)); err != nil {
			return fmt.Errorf("seed provider example: %w", err)
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(4, ?)`, now.UnixNano()); err != nil {
		return fmt.Errorf("record provider examples migration: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit provider examples migration: %w", err)
	}
	return nil
}

func defaultProviderExamples() []domain.ProviderExample {
	return []domain.ProviderExample{
		{
			ID:          "codex-openai-compatible",
			Name:        "Codex OpenAI-Compatible",
			CLI:         domain.CLICodex,
			BaseURL:     "https://api.openai.com/v1",
			Model:       "gpt-5",
			Provider:    "openai",
			Description: "Codex Responses API 示例；凭据需通过运行环境单独提供。",
		},
		{
			ID:          "claude-anthropic-compatible",
			Name:        "Claude Anthropic-Compatible",
			CLI:         domain.CLIClaude,
			BaseURL:     "https://api.anthropic.com",
			Model:       "sonnet",
			Provider:    "anthropic",
			Description: "Claude Code Anthropic API 示例；凭据需通过运行环境单独提供。",
		},
	}
}

func (s *JSON) applySettingsRetentionV3() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin settings retention migration: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`ALTER TABLE settings ADD COLUMN event_retention_days INTEGER NOT NULL DEFAULT 30`,
		`ALTER TABLE settings ADD COLUMN event_retention_rows INTEGER NOT NULL DEFAULT 5000`,
		`ALTER TABLE settings ADD COLUMN event_retention_bytes INTEGER NOT NULL DEFAULT 8388608`,
	} {
		if _, err = tx.Exec(statement); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
				return fmt.Errorf("add settings retention column: %w", err)
			}
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(3, ?)`, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("record settings retention migration: %w", err)
	}
	return tx.Commit()
}

func (s *JSON) applySchemaV1() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin schema migration: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE settings (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			timeout_seconds INTEGER NOT NULL,
			retry_interval_seconds INTEGER NOT NULL,
			keepalive_interval_seconds INTEGER NOT NULL,
			keepalive_summary_seconds INTEGER NOT NULL DEFAULT 3600,
			keepalive_summary_successes INTEGER NOT NULL DEFAULT 0,
			probe_progress_seconds INTEGER NOT NULL DEFAULT 3600,
			recovery_merge_seconds INTEGER NOT NULL DEFAULT 0,
			history_limit INTEGER NOT NULL,
			event_retention_days INTEGER NOT NULL DEFAULT 30,
			event_retention_rows INTEGER NOT NULL DEFAULT 5000,
			event_retention_bytes INTEGER NOT NULL DEFAULT 8388608,
			dingtalk_configured INTEGER NOT NULL DEFAULT 0,
			updated_at_ns INTEGER NOT NULL
		)`,
		`CREATE TABLE job_summaries (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT NOT NULL UNIQUE,
			mode TEXT NOT NULL,
			run_once INTEGER NOT NULL DEFAULT 0,
			cli TEXT NOT NULL,
			provider_id TEXT NOT NULL DEFAULT '',
			provider_name TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			target TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			masked_key TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			latest_attempt TEXT NOT NULL DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			started_at_ns INTEGER NOT NULL,
			ended_at_ns INTEGER,
			next_attempt_at_ns INTEGER,
			elapsed_millis INTEGER NOT NULL DEFAULT 0,
			saved_at_ns INTEGER NOT NULL
		)`,
		`CREATE INDEX idx_job_summaries_saved_at ON job_summaries(saved_at_ns DESC, seq DESC)`,
		`CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at_ns INTEGER NOT NULL,
			type TEXT NOT NULL,
			level TEXT NOT NULL DEFAULT '',
			provider_id TEXT NOT NULL DEFAULT '',
			job_id TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			data_json TEXT NOT NULL DEFAULT '{}',
			size_bytes INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX idx_events_at ON events(at_ns DESC, id DESC)`,
		`CREATE INDEX idx_events_provider_at ON events(provider_id, at_ns DESC)`,
		`CREATE INDEX idx_events_job_at ON events(job_id, at_ns DESC)`,
		`CREATE INDEX idx_events_type_at ON events(type, at_ns DESC)`,
		`CREATE INDEX idx_events_level_at ON events(level, at_ns DESC)`,
	}
	for _, statement := range statements {
		if _, err = tx.Exec(statement); err != nil {
			return fmt.Errorf("apply schema migration: %w", err)
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(1, ?)`, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("record schema migration: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit schema migration: %w", err)
	}
	return nil
}

func (s *JSON) migrateLegacyJSON() error {
	settings, hasSettings, err := readLegacySettings(s.path("settings.json"))
	if err != nil {
		return err
	}
	summaries, hasSummaries, err := readLegacySummaries(s.path("summaries.json"))
	if err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin legacy migration: %w", err)
	}
	defer tx.Rollback()
	var settingsCount, summaryCount int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&settingsCount); err != nil {
		return fmt.Errorf("count settings before legacy migration: %w", err)
	}
	if err = tx.QueryRow(`SELECT COUNT(*) FROM job_summaries`).Scan(&summaryCount); err != nil {
		return fmt.Errorf("count summaries before legacy migration: %w", err)
	}
	if settingsCount == 0 && hasSettings {
		if err = saveSettingsTx(tx, settings, time.Now().UTC()); err != nil {
			return fmt.Errorf("migrate legacy settings: %w", err)
		}
	}
	if summaryCount == 0 && hasSummaries {
		base := time.Now().UTC().Add(-time.Duration(len(summaries)) * time.Nanosecond)
		for index := len(summaries) - 1; index >= 0; index-- {
			savedAt := base.Add(time.Duration(len(summaries)-index) * time.Nanosecond)
			if err = insertSummaryTx(tx, summaries[index], savedAt); err != nil {
				return fmt.Errorf("migrate legacy summary: %w", err)
			}
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version, applied_at_ns) VALUES(2, ?)`, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("record legacy migration: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy migration: %w", err)
	}
	return nil
}

func readLegacySettings(file string) (domain.Settings, bool, error) {
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return domain.Settings{}, false, nil
	}
	if err != nil {
		return domain.Settings{}, false, fmt.Errorf("read legacy settings: %w", err)
	}
	var value domain.Settings
	if err = json.Unmarshal(b, &value); err != nil {
		return domain.Settings{}, false, fmt.Errorf("decode legacy settings: %w", err)
	}
	return value, true, nil
}

func readLegacySummaries(file string) ([]domain.Summary, bool, error) {
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read legacy summaries: %w", err)
	}
	var value []domain.Summary
	if err = json.Unmarshal(b, &value); err != nil {
		return nil, false, fmt.Errorf("decode legacy summaries: %w", err)
	}
	return value, true, nil
}

func (s *JSON) LoadSettings() (domain.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return domain.Settings{}, err
	}
	var value domain.Settings
	var configured int
	err := s.db.QueryRow(`SELECT timeout_seconds, retry_interval_seconds, keepalive_interval_seconds,
		keepalive_summary_seconds, keepalive_summary_successes, probe_progress_seconds, recovery_merge_seconds,
		history_limit, event_retention_days, event_retention_rows, event_retention_bytes,
		dingtalk_configured FROM settings WHERE id = 1`).Scan(
		&value.TimeoutSeconds, &value.RetryIntervalSeconds, &value.KeepaliveIntervalSeconds,
		&value.KeepaliveSummarySeconds, &value.KeepaliveSummarySuccesses,
		&value.ProbeProgressSeconds, &value.RecoveryMergeSeconds,
		&value.HistoryLimit, &value.EventRetentionDays, &value.EventRetentionRows,
		&value.EventRetentionBytes, &configured,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DefaultSettings(), nil
	}
	if err != nil {
		return domain.Settings{}, fmt.Errorf("load settings: %w", err)
	}
	value.DingTalkConfigured = configured != 0
	return value, nil
}

func (s *JSON) SaveSettings(value domain.Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	if err := saveSettingsDB(s.db, value, time.Now().UTC()); err != nil {
		return err
	}
	s.ensurePrivateFiles()
	return nil
}

type sqlExecer interface {
	Exec(string, ...any) (sql.Result, error)
}

func saveSettingsDB(exec sqlExecer, value domain.Settings, now time.Time) error {
	_, err := exec.Exec(`INSERT INTO settings(
		id, timeout_seconds, retry_interval_seconds, keepalive_interval_seconds,
		keepalive_summary_seconds, keepalive_summary_successes, probe_progress_seconds, recovery_merge_seconds,
		history_limit, event_retention_days, event_retention_rows, event_retention_bytes,
		dingtalk_configured, updated_at_ns
	) VALUES(1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		timeout_seconds = excluded.timeout_seconds,
		retry_interval_seconds = excluded.retry_interval_seconds,
		keepalive_interval_seconds = excluded.keepalive_interval_seconds,
		keepalive_summary_seconds = excluded.keepalive_summary_seconds,
		keepalive_summary_successes = excluded.keepalive_summary_successes,
		probe_progress_seconds = excluded.probe_progress_seconds,
		recovery_merge_seconds = excluded.recovery_merge_seconds,
		history_limit = excluded.history_limit,
		event_retention_days = excluded.event_retention_days,
		event_retention_rows = excluded.event_retention_rows,
		event_retention_bytes = excluded.event_retention_bytes,
		dingtalk_configured = excluded.dingtalk_configured,
		updated_at_ns = excluded.updated_at_ns`,
		value.TimeoutSeconds, value.RetryIntervalSeconds, value.KeepaliveIntervalSeconds,
		value.KeepaliveSummarySeconds, value.KeepaliveSummarySuccesses,
		value.ProbeProgressSeconds, value.RecoveryMergeSeconds,
		value.HistoryLimit, value.EventRetentionDays, value.EventRetentionRows,
		value.EventRetentionBytes, boolInt(value.DingTalkConfigured), now.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

func saveSettingsTx(tx *sql.Tx, value domain.Settings, now time.Time) error {
	return saveSettingsDB(tx, value, now)
}

func (s *JSON) ListProviderExamples() ([]domain.ProviderExample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, name, cli, base_url, model, provider, description, updated_at_ns
		FROM provider_examples
		ORDER BY CASE cli WHEN 'codex' THEN 0 ELSE 1 END, name, id`)
	if err != nil {
		return nil, fmt.Errorf("list provider examples: %w", err)
	}
	defer rows.Close()
	examples := make([]domain.ProviderExample, 0)
	for rows.Next() {
		var example domain.ProviderExample
		var cli string
		var updatedAt int64
		if err = rows.Scan(&example.ID, &example.Name, &cli, &example.BaseURL, &example.Model,
			&example.Provider, &example.Description, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan provider example: %w", err)
		}
		example.CLI = domain.CLI(cli)
		example.UpdatedAt = time.Unix(0, updatedAt).UTC()
		examples = append(examples, example)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider examples: %w", err)
	}
	return examples, nil
}

func (s *JSON) UpsertProviderExample(example domain.ProviderExample) (domain.ProviderExample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return domain.ProviderExample{}, err
	}
	var err error
	if example, err = normalizeProviderExample(example); err != nil {
		return domain.ProviderExample{}, err
	}
	example.UpdatedAt = time.Now().UTC()
	if err = upsertProviderExampleDB(s.db, example); err != nil {
		return domain.ProviderExample{}, err
	}
	s.ensurePrivateFiles()
	return example, nil
}

func (s *JSON) DeleteProviderExample(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return false, err
	}
	id = strings.TrimSpace(id)
	if !providerExampleID.MatchString(id) {
		return false, errors.New("invalid provider example id")
	}
	result, err := s.db.Exec(`DELETE FROM provider_examples WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete provider example: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count deleted provider example: %w", err)
	}
	return deleted > 0, nil
}

func upsertProviderExampleTx(tx *sql.Tx, example domain.ProviderExample, updatedAt time.Time) error {
	var err error
	if example, err = normalizeProviderExample(example); err != nil {
		return err
	}
	example.UpdatedAt = updatedAt.UTC()
	return upsertProviderExampleDB(tx, example)
}

func upsertProviderExampleDB(exec sqlExecer, example domain.ProviderExample) error {
	_, err := exec.Exec(`INSERT INTO provider_examples(
		id, name, cli, base_url, model, provider, description, updated_at_ns
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		name = excluded.name,
		cli = excluded.cli,
		base_url = excluded.base_url,
		model = excluded.model,
		provider = excluded.provider,
		description = excluded.description,
		updated_at_ns = excluded.updated_at_ns`,
		example.ID, example.Name, string(example.CLI), example.BaseURL, example.Model,
		example.Provider, example.Description, example.UpdatedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("save provider example: %w", err)
	}
	return nil
}

const scheduleSelect = `SELECT id, name, enabled, cli, provider_id, mode, timezone,
	weekdays_mask, start_minute, end_minute, until_success, timeout_seconds,
	retry_interval_seconds, keepalive_interval_seconds, failure_threshold, model,
	fallback_model, last_occurrence_key, last_status, last_job_id, last_run_at_ns,
	created_at_ns, updated_at_ns FROM schedules`

func (s *JSON) ListSchedules() ([]domain.Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(scheduleSelect + ` ORDER BY enabled DESC, name, id`)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()
	values := make([]domain.Schedule, 0)
	for rows.Next() {
		value, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schedules: %w", err)
	}
	return values, nil
}

func (s *JSON) GetSchedule(id string) (domain.Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return domain.Schedule{}, err
	}
	value, err := scanSchedule(s.db.QueryRow(scheduleSelect+` WHERE id = ?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Schedule{}, sql.ErrNoRows
	}
	return value, err
}

func (s *JSON) UpsertSchedule(value domain.Schedule) (domain.Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return domain.Schedule{}, err
	}
	var err error
	if value, err = normalizeSchedule(value); err != nil {
		return domain.Schedule{}, err
	}
	now := time.Now().UTC()
	if value.ID == "" {
		value.ID = "schedule-" + randomHex(8)
	}
	var createdAt int64
	err = s.db.QueryRow(`SELECT created_at_ns FROM schedules WHERE id = ?`, value.ID).Scan(&createdAt)
	if err == nil {
		value.CreatedAt = time.Unix(0, createdAt).UTC()
	} else if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM schedules`).Scan(&count); err != nil {
			return domain.Schedule{}, fmt.Errorf("count schedules: %w", err)
		}
		if count >= maxSchedules {
			return domain.Schedule{}, ErrScheduleLimit
		}
		value.CreatedAt = now
	} else {
		return domain.Schedule{}, fmt.Errorf("read schedule creation time: %w", err)
	}
	value.UpdatedAt = now
	_, err = s.db.Exec(`INSERT INTO schedules(
		id, name, enabled, cli, provider_id, mode, timezone, weekdays_mask,
		start_minute, end_minute, until_success, timeout_seconds,
		retry_interval_seconds, keepalive_interval_seconds, failure_threshold,
		model, fallback_model, last_occurrence_key, last_status, last_job_id,
		last_run_at_ns, created_at_ns, updated_at_ns
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		name = excluded.name, enabled = excluded.enabled, cli = excluded.cli,
		provider_id = excluded.provider_id, mode = excluded.mode,
		timezone = excluded.timezone, weekdays_mask = excluded.weekdays_mask,
		start_minute = excluded.start_minute, end_minute = excluded.end_minute,
		until_success = excluded.until_success, timeout_seconds = excluded.timeout_seconds,
		retry_interval_seconds = excluded.retry_interval_seconds,
		keepalive_interval_seconds = excluded.keepalive_interval_seconds,
		failure_threshold = excluded.failure_threshold, model = excluded.model,
		fallback_model = excluded.fallback_model, updated_at_ns = excluded.updated_at_ns`,
		value.ID, value.Name, boolInt(value.Enabled), string(value.CLI), value.ProviderID,
		string(value.Mode), value.Timezone, value.WeekdaysMask, value.StartMinute,
		value.EndMinute, boolInt(value.UntilSuccess), value.TimeoutSeconds,
		value.RetryIntervalSeconds, value.KeepaliveIntervalSeconds,
		value.FailureThreshold, value.Model, value.FallbackModel,
		value.LastOccurrenceKey, value.LastStatus, value.LastJobID,
		nullTimeNS(value.LastOccurrenceAt), value.CreatedAt.UnixNano(), value.UpdatedAt.UnixNano(),
	)
	if err != nil {
		return domain.Schedule{}, fmt.Errorf("save schedule: %w", err)
	}
	s.ensurePrivateFiles()
	return scanSchedule(s.db.QueryRow(scheduleSelect+` WHERE id = ?`, value.ID))
}

func (s *JSON) DeleteSchedule(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return false, err
	}
	id = strings.TrimSpace(id)
	if !scheduleID.MatchString(id) {
		return false, errors.New("invalid schedule id")
	}
	result, err := s.db.Exec(`DELETE FROM schedules WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete schedule: %w", err)
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

// MarkScheduleRun overwrites the single runtime snapshot for a schedule. It
// intentionally does not create a per-run history table.
func (s *JSON) MarkScheduleRun(id, occurrence, status, jobID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE schedules SET last_occurrence_key = ?, last_status = ?,
		last_job_id = ?, last_run_at_ns = ? WHERE id = ?`, occurrence, status, jobID,
		at.UTC().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("mark schedule run: %w", err)
	}
	return nil
}

func scanSchedule(row rowScanner) (domain.Schedule, error) {
	var value domain.Schedule
	var enabled, untilSuccess int
	var cli, mode string
	var lastRun sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(&value.ID, &value.Name, &enabled, &cli, &value.ProviderID,
		&mode, &value.Timezone, &value.WeekdaysMask, &value.StartMinute,
		&value.EndMinute, &untilSuccess, &value.TimeoutSeconds,
		&value.RetryIntervalSeconds, &value.KeepaliveIntervalSeconds,
		&value.FailureThreshold, &value.Model, &value.FallbackModel,
		&value.LastOccurrenceKey, &value.LastStatus, &value.LastJobID, &lastRun,
		&createdAt, &updatedAt); err != nil {
		return domain.Schedule{}, err
	}
	value.Enabled = enabled != 0
	value.CLI = domain.CLI(cli)
	value.Mode = domain.Mode(mode)
	value.UntilSuccess = untilSuccess != 0
	value.LastOccurrenceAt = timePtr(lastRun)
	value.CreatedAt = time.Unix(0, createdAt).UTC()
	value.UpdatedAt = time.Unix(0, updatedAt).UTC()
	return value, nil
}

func normalizeSchedule(value domain.Schedule) (domain.Schedule, error) {
	value.ID = strings.TrimSpace(value.ID)
	value.Name = strings.TrimSpace(value.Name)
	value.ProviderID = strings.TrimSpace(value.ProviderID)
	value.Timezone = strings.TrimSpace(value.Timezone)
	value.Model = strings.TrimSpace(value.Model)
	value.FallbackModel = strings.TrimSpace(value.FallbackModel)
	if value.ID != "" && !scheduleID.MatchString(value.ID) {
		return domain.Schedule{}, errors.New("invalid schedule id")
	}
	if value.Name == "" || len(value.Name) > 128 {
		return domain.Schedule{}, errors.New("schedule name is required and must not exceed 128 bytes")
	}
	if value.CLI != domain.CLICodex && value.CLI != domain.CLIClaude {
		return domain.Schedule{}, errors.New("schedule cli must be codex or claude")
	}
	if value.ProviderID == "" || len(value.ProviderID) > 256 {
		return domain.Schedule{}, errors.New("schedule providerId is required and must not exceed 256 bytes")
	}
	if value.Mode != domain.ModeProbe && value.Mode != domain.ModeKeepalive {
		return domain.Schedule{}, errors.New("schedule mode must be probe or keepalive")
	}
	if value.Timezone == "" {
		value.Timezone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(value.Timezone); err != nil {
		return domain.Schedule{}, errors.New("invalid schedule timezone")
	}
	if value.WeekdaysMask < 1 || value.WeekdaysMask > 127 {
		return domain.Schedule{}, errors.New("weekdaysMask must be 1..127")
	}
	if value.StartMinute < 0 || value.StartMinute > 1439 || value.EndMinute < 1 || value.EndMinute > 1440 || value.StartMinute == value.EndMinute {
		return domain.Schedule{}, errors.New("invalid schedule time window")
	}
	if value.TimeoutSeconds < 1 || value.TimeoutSeconds > 3600 {
		return domain.Schedule{}, errors.New("timeoutSeconds must be 1..3600")
	}
	if value.RetryIntervalSeconds < 1 || value.RetryIntervalSeconds > 86400 || value.KeepaliveIntervalSeconds < 1 || value.KeepaliveIntervalSeconds > 86400 {
		return domain.Schedule{}, errors.New("schedule intervals must be 1..86400")
	}
	if value.FailureThreshold < 1 || value.FailureThreshold > 100 {
		return domain.Schedule{}, errors.New("failureThreshold must be 1..100")
	}
	if len(value.Model) > 128 || len(value.FallbackModel) > 128 {
		return domain.Schedule{}, errors.New("schedule model names must not exceed 128 bytes")
	}
	for label, text := range map[string]string{
		"name": value.Name, "providerId": value.ProviderID,
		"model": value.Model, "fallbackModel": value.FallbackModel,
	} {
		if strings.Contains(text, "://") || credentialValue.MatchString(text) {
			return domain.Schedule{}, fmt.Errorf("schedule %s contains connection or credential data", label)
		}
	}
	return value, nil
}

func randomHex(bytes int) string {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%x", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

func (s *JSON) LoadSummaries() ([]domain.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(summarySelect + ` ORDER BY saved_at_ns DESC, seq DESC`)
	if err != nil {
		return nil, fmt.Errorf("load summaries: %w", err)
	}
	defer rows.Close()
	var values []domain.Summary
	for rows.Next() {
		value, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate summaries: %w", err)
	}
	return values, nil
}

func (s *JSON) SaveSummary(value domain.Summary, limit int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin summary save: %w", err)
	}
	defer tx.Rollback()
	if err = insertSummaryTx(tx, value, time.Now().UTC()); err != nil {
		return err
	}
	if limit > 0 {
		if _, err = tx.Exec(`DELETE FROM job_summaries WHERE seq IN (
			SELECT seq FROM job_summaries ORDER BY saved_at_ns DESC, seq DESC LIMIT -1 OFFSET ?
		)`, limit); err != nil {
			return fmt.Errorf("retain summaries: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit summary save: %w", err)
	}
	s.ensurePrivateFiles()
	return nil
}

const summarySelect = `SELECT job_id, mode, run_once, cli, provider_id, provider_name, provider, target,
	model, masked_key, status, latest_attempt, attempts, started_at_ns, ended_at_ns,
	next_attempt_at_ns, elapsed_millis FROM job_summaries`

func insertSummaryTx(tx *sql.Tx, value domain.Summary, savedAt time.Time) error {
	_, err := tx.Exec(`INSERT INTO job_summaries(
		job_id, mode, run_once, cli, provider_id, provider_name, provider, target, model, masked_key,
		status, latest_attempt, attempts, started_at_ns, ended_at_ns, next_attempt_at_ns,
		elapsed_millis, saved_at_ns
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(job_id) DO UPDATE SET
		mode = excluded.mode, run_once = excluded.run_once, cli = excluded.cli, provider_id = excluded.provider_id,
		provider_name = excluded.provider_name, provider = excluded.provider,
		target = excluded.target, model = excluded.model, masked_key = excluded.masked_key,
		status = excluded.status, latest_attempt = excluded.latest_attempt,
		attempts = excluded.attempts, started_at_ns = excluded.started_at_ns,
		ended_at_ns = excluded.ended_at_ns, next_attempt_at_ns = excluded.next_attempt_at_ns,
		elapsed_millis = excluded.elapsed_millis, saved_at_ns = excluded.saved_at_ns`,
		value.ID, string(value.Mode), boolInt(value.RunOnce), string(value.CLI), value.ProviderID, value.ProviderName,
		value.Provider, value.Target, value.Model, value.MaskedKey, string(value.Status),
		string(value.LatestAttempt), value.Attempts, value.StartedAt.UnixNano(),
		nullTimeNS(value.EndedAt), nullTimeNS(value.NextAttemptAt), value.ElapsedMillis,
		savedAt.UnixNano(),
	)
	if err != nil {
		return fmt.Errorf("save summary: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanSummary(row rowScanner) (domain.Summary, error) {
	var value domain.Summary
	var mode, cli, status, latest string
	var runOnce int
	var started int64
	var ended, next sql.NullInt64
	if err := row.Scan(
		&value.ID, &mode, &runOnce, &cli, &value.ProviderID, &value.ProviderName, &value.Provider,
		&value.Target, &value.Model, &value.MaskedKey, &status, &latest, &value.Attempts,
		&started, &ended, &next, &value.ElapsedMillis,
	); err != nil {
		return domain.Summary{}, fmt.Errorf("scan summary: %w", err)
	}
	value.Mode = domain.Mode(mode)
	value.RunOnce = runOnce != 0
	value.CLI = domain.CLI(cli)
	value.Status = domain.JobStatus(status)
	value.LatestAttempt = domain.AttemptStatus(latest)
	value.StartedAt = time.Unix(0, started).UTC()
	value.EndedAt = timePtr(ended)
	value.NextAttemptAt = timePtr(next)
	return value, nil
}

func (s *JSON) SaveEvent(value Event, retention ...EventRetention) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	value, data, err := prepareEvent(value)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin event save: %w", err)
	}
	defer tx.Rollback()
	size := eventSize(value, data)
	result, err := tx.Exec(`INSERT INTO events(
		at_ns, type, level, provider_id, job_id, message, data_json, size_bytes
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, value.At.UnixNano(), value.Type, value.Level,
		value.ProviderID, value.JobID, value.Message, string(data), size)
	if err != nil {
		return fmt.Errorf("save event: %w", err)
	}
	if value.ID, err = result.LastInsertId(); err != nil {
		return fmt.Errorf("read event id: %w", err)
	}
	if len(retention) > 0 {
		if _, err = retainEventsTx(tx, retention[0]); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit event save: %w", err)
	}
	s.ensurePrivateFiles()
	return nil
}

func (s *JSON) ListEvents(filter EventFilter) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return nil, err
	}
	where, args := eventWhere(filter)
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	query := `SELECT id, at_ns, type, level, provider_id, job_id, message, data_json
		FROM events` + where + ` ORDER BY at_ns DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	values := make([]Event, 0, limit)
	for rows.Next() {
		var value Event
		var at int64
		var data string
		if err = rows.Scan(&value.ID, &at, &value.Type, &value.Level, &value.ProviderID,
			&value.JobID, &value.Message, &data); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		value.At = time.Unix(0, at).UTC()
		if data != "" && data != "{}" {
			if err = json.Unmarshal([]byte(data), &value.Data); err != nil {
				return nil, fmt.Errorf("decode event data: %w", err)
			}
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return values, nil
}

func (s *JSON) CountEvents(filter EventFilter) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return 0, err
	}
	where, args := eventWhere(filter)
	var count int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events`+where, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count events: %w", err)
	}
	return count, nil
}

func (s *JSON) ClearEvents() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return 0, err
	}
	result, err := s.db.Exec(`DELETE FROM events`)
	if err != nil {
		return 0, fmt.Errorf("clear events: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count cleared events: %w", err)
	}
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	if _, err = s.db.Exec(`VACUUM`); err != nil {
		return deleted, fmt.Errorf("vacuum cleared events: %w", err)
	}
	return deleted, nil
}

func (s *JSON) RetainEvents(retention EventRetention) (RetentionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return RetentionResult{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return RetentionResult{}, fmt.Errorf("begin event retention: %w", err)
	}
	defer tx.Rollback()
	result, err := retainEventsTx(tx, retention)
	if err != nil {
		return RetentionResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return RetentionResult{}, fmt.Errorf("commit event retention: %w", err)
	}
	if result.Deleted > 0 {
		_, _ = s.db.Exec(`PRAGMA incremental_vacuum(64)`)
	}
	return result, nil
}

func retainEventsTx(tx *sql.Tx, retention EventRetention) (RetentionResult, error) {
	var result RetentionResult
	now := retention.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if retention.MaxAge > 0 {
		res, err := tx.Exec(`DELETE FROM events WHERE at_ns < ?`, now.Add(-retention.MaxAge).UnixNano())
		if err != nil {
			return result, fmt.Errorf("retain events by age: %w", err)
		}
		deleted, _ := res.RowsAffected()
		result.Deleted += deleted
	}
	if retention.MaxRows > 0 {
		res, err := tx.Exec(`DELETE FROM events WHERE id IN (
			SELECT id FROM events ORDER BY at_ns DESC, id DESC LIMIT -1 OFFSET ?
		)`, retention.MaxRows)
		if err != nil {
			return result, fmt.Errorf("retain events by count: %w", err)
		}
		deleted, _ := res.RowsAffected()
		result.Deleted += deleted
	}
	if retention.MaxBytes > 0 {
		var total int64
		if err := tx.QueryRow(`SELECT COALESCE(SUM(size_bytes), 0) FROM events`).Scan(&total); err != nil {
			return result, fmt.Errorf("measure event bytes: %w", err)
		}
		if total > retention.MaxBytes {
			rows, err := tx.Query(`SELECT id, size_bytes FROM events ORDER BY at_ns ASC, id ASC`)
			if err != nil {
				return result, fmt.Errorf("select events for byte retention: %w", err)
			}
			var ids []int64
			for rows.Next() && total > retention.MaxBytes {
				var id, size int64
				if err = rows.Scan(&id, &size); err != nil {
					rows.Close()
					return result, fmt.Errorf("scan event size: %w", err)
				}
				ids = append(ids, id)
				total -= size
			}
			if err = rows.Close(); err != nil {
				return result, fmt.Errorf("close event size rows: %w", err)
			}
			for _, id := range ids {
				if _, err = tx.Exec(`DELETE FROM events WHERE id = ?`, id); err != nil {
					return result, fmt.Errorf("retain events by bytes: %w", err)
				}
			}
			result.Deleted += int64(len(ids))
		}
	}
	if err := tx.QueryRow(`SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM events`).Scan(&result.Count, &result.Bytes); err != nil {
		return result, fmt.Errorf("read event retention result: %w", err)
	}
	return result, nil
}

func eventWhere(filter EventFilter) (string, []any) {
	var clauses []string
	var args []any
	if filter.ProviderID != "" {
		clauses = append(clauses, "provider_id = ?")
		args = append(args, filter.ProviderID)
	}
	if filter.JobID != "" {
		clauses = append(clauses, "job_id = ?")
		args = append(args, filter.JobID)
	}
	if filter.Type != "" {
		clauses = append(clauses, "type = ?")
		args = append(args, filter.Type)
	}
	if filter.Level != "" {
		clauses = append(clauses, "level = ?")
		args = append(args, filter.Level)
	}
	if !filter.Since.IsZero() {
		clauses = append(clauses, "at_ns >= ?")
		args = append(args, filter.Since.UnixNano())
	}
	if !filter.Until.IsZero() {
		clauses = append(clauses, "at_ns <= ?")
		args = append(args, filter.Until.UnixNano())
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func normalizeProviderExample(example domain.ProviderExample) (domain.ProviderExample, error) {
	example.ID = strings.ToLower(strings.TrimSpace(example.ID))
	example.Name = strings.TrimSpace(example.Name)
	example.BaseURL = strings.TrimSpace(example.BaseURL)
	example.Model = strings.TrimSpace(example.Model)
	example.Provider = strings.TrimSpace(example.Provider)
	example.Description = strings.TrimSpace(example.Description)
	if !providerExampleID.MatchString(example.ID) {
		return domain.ProviderExample{}, errors.New("provider example id must use lowercase letters, numbers, dot, underscore, or hyphen")
	}
	if example.Name == "" || len(example.Name) > 160 {
		return domain.ProviderExample{}, errors.New("provider example name is required and must not exceed 160 bytes")
	}
	if example.CLI != domain.CLICodex && example.CLI != domain.CLIClaude {
		return domain.ProviderExample{}, errors.New("provider example cli must be codex or claude")
	}
	if len(example.BaseURL) > 2048 || len(example.Model) > 256 || len(example.Provider) > 160 || len(example.Description) > 2048 {
		return domain.ProviderExample{}, errors.New("provider example field is too long")
	}
	parsed, err := url.Parse(example.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return domain.ProviderExample{}, errors.New("provider example baseUrl must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return domain.ProviderExample{}, errors.New("provider example baseUrl must not contain credentials, query parameters, or fragments")
	}
	for field, value := range map[string]string{
		"name": example.Name, "baseUrl": example.BaseURL, "model": example.Model,
		"provider": example.Provider, "description": example.Description,
	} {
		if credentialValue.MatchString(value) {
			return domain.ProviderExample{}, fmt.Errorf("provider example %s contains credential-like data", field)
		}
	}
	example.BaseURL = strings.TrimRight(example.BaseURL, "/")
	return example, nil
}

func prepareEvent(value Event) (Event, []byte, error) {
	value.Type = strings.TrimSpace(value.Type)
	value.Level = strings.TrimSpace(value.Level)
	value.ProviderID = strings.TrimSpace(value.ProviderID)
	value.JobID = strings.TrimSpace(value.JobID)
	if value.Type == "" {
		return Event{}, nil, errors.New("event type is required")
	}
	if len(value.Type) > 128 || len(value.Level) > 32 || len(value.ProviderID) > 256 || len(value.JobID) > 256 {
		return Event{}, nil, errors.New("event metadata is too long")
	}
	if len(value.Message) > maxEventMessage {
		return Event{}, nil, fmt.Errorf("event message exceeds %d bytes", maxEventMessage)
	}
	if credentialValue.MatchString(value.Message) {
		return Event{}, nil, errors.New("event message contains credential-like data")
	}
	if err := validateEventData(value.Data); err != nil {
		return Event{}, nil, err
	}
	data := []byte("{}")
	if len(value.Data) > 0 {
		var err error
		data, err = json.Marshal(value.Data)
		if err != nil {
			return Event{}, nil, fmt.Errorf("encode event data: %w", err)
		}
	}
	if len(data) > maxEventDataBytes {
		return Event{}, nil, fmt.Errorf("event data exceeds %d bytes", maxEventDataBytes)
	}
	if credentialValue.Match(data) {
		return Event{}, nil, errors.New("event data contains credential-like data")
	}
	if value.At.IsZero() {
		value.At = time.Now().UTC()
	} else {
		value.At = value.At.UTC()
	}
	return value, data, nil
}

func validateEventData(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if forbiddenEventKey.MatchString(key) {
				return fmt.Errorf("event data contains forbidden field %q", key)
			}
			if err := validateEventData(item); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range typed {
			if err := validateEventData(item); err != nil {
				return err
			}
		}
	case string:
		if credentialValue.MatchString(typed) {
			return errors.New("event data contains credential-like data")
		}
	}
	return nil
}

func eventSize(value Event, data []byte) int64 {
	return int64(len(value.Type) + len(value.Level) + len(value.ProviderID) + len(value.JobID) + len(value.Message) + len(data))
}

func nullTimeNS(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().UnixNano()
}

func timePtr(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := time.Unix(0, value.Int64).UTC()
	return &t
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *JSON) ensurePrivateFiles() {
	for _, file := range []string{s.dbPath, s.dbPath + "-wal", s.dbPath + "-shm"} {
		if err := os.Chmod(file, 0600); err != nil && !errors.Is(err, os.ErrNotExist) {
			// Permission hardening is best effort after SQLite has already opened
			// the file. Operational writes still return their own errors.
			continue
		}
	}
}
