package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/domain"
)

func TestSettingsPersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	settings, err := store.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings != domain.DefaultSettings() {
		t.Fatalf("got defaults %+v", settings)
	}

	want := domain.Settings{
		TimeoutSeconds:           31,
		RetryIntervalSeconds:     7,
		KeepaliveIntervalSeconds: 181,
		HistoryLimit:             42,
		DingTalkConfigured:       true,
	}
	if err = store.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := New(dir)
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	info, err := os.Stat(filepath.Join(dir, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("database permissions are too broad: %o", info.Mode().Perm())
	}
}

func TestSummaryRetentionAndSanitizedSchema(t *testing.T) {
	store := New(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	base := time.Date(2026, 7, 13, 6, 0, 0, 0, time.UTC)
	for index := 1; index <= 4; index++ {
		ended := base.Add(time.Duration(index) * time.Minute)
		summary := domain.Summary{
			ID:            string(rune('0' + index)),
			Mode:          domain.ModeProbe,
			RunOnce:       index == 4,
			CLI:           domain.CLICodex,
			ProviderID:    "provider",
			ProviderName:  "Provider",
			Provider:      "custom",
			Target:        "https://example.test/v1",
			Model:         "gpt-test",
			MaskedKey:     "sk-abc...wxyz",
			Status:        domain.JobSuccess,
			LatestAttempt: domain.AttemptSuccess,
			Attempts:      index,
			StartedAt:     ended.Add(-time.Second),
			EndedAt:       &ended,
			ElapsedMillis: 1000,
		}
		if err := store.SaveSummary(summary, 3); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Microsecond)
	}
	values, err := store.LoadSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 || values[0].ID != "4" || values[2].ID != "2" {
		t.Fatalf("unexpected retained summaries: %+v", values)
	}
	if !values[0].RunOnce || values[0].Target != "https://example.test/v1" || values[0].EndedAt == nil || !values[0].EndedAt.Equal(base.Add(4*time.Minute)) {
		t.Fatalf("summary did not round trip: %+v", values[0])
	}

	rows, err := store.db.Query(`SELECT name FROM pragma_table_info('job_summaries')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err = rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	joined := strings.Join(columns, " ")
	for _, forbidden := range []string{"prompt", "output", "api_key", "auth_json", "webhook"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("summary schema contains forbidden field %q: %s", forbidden, joined)
		}
	}
}

func TestLegacyJSONMigrationRunsOnce(t *testing.T) {
	dir := t.TempDir()
	wantSettings := domain.Settings{TimeoutSeconds: 22, RetryIntervalSeconds: 4, KeepaliveIntervalSeconds: 99, HistoryLimit: 8}
	writeJSONFile(t, filepath.Join(dir, "settings.json"), wantSettings)
	start := time.Date(2026, 7, 13, 6, 37, 7, 0, time.UTC)
	wantSummaries := []domain.Summary{
		{ID: "new", Mode: domain.ModeProbe, CLI: domain.CLICodex, Status: domain.JobSuccess, StartedAt: start.Add(time.Minute)},
		{ID: "old", Mode: domain.ModeKeepalive, CLI: domain.CLIClaude, Status: domain.JobStopped, StartedAt: start},
	}
	writeJSONFile(t, filepath.Join(dir, "summaries.json"), wantSummaries)

	store := New(dir)
	gotSettings, err := store.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if gotSettings != wantSettings {
		t.Fatalf("migrated settings %+v want %+v", gotSettings, wantSettings)
	}
	gotSummaries, err := store.LoadSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSummaries) != 2 || gotSummaries[0].ID != "new" || gotSummaries[1].ID != "old" {
		t.Fatalf("migrated summaries out of order: %+v", gotSummaries)
	}
	for _, name := range []string{"settings.json", "summaries.json"} {
		if _, statErr := os.Stat(filepath.Join(dir, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("legacy file %s was not removed: %v", name, statErr)
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}

	writeJSONFile(t, filepath.Join(dir, "settings.json"), domain.Settings{TimeoutSeconds: 999})
	writeJSONFile(t, filepath.Join(dir, "summaries.json"), []domain.Summary{{ID: "reimported", StartedAt: start}})
	reopened := New(dir)
	t.Cleanup(func() { _ = reopened.Close() })
	gotSettings, err = reopened.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	gotSummaries, err = reopened.LoadSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if gotSettings != wantSettings || len(gotSummaries) != 2 || gotSummaries[0].ID != "new" {
		t.Fatalf("legacy data was imported more than once: settings=%+v summaries=%+v", gotSettings, gotSummaries)
	}
	var versions int
	if err = reopened.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 7 {
		t.Fatalf("got %d migration records", versions)
	}
}

func TestProviderExamplesSeedCRUDAndSecretBoundary(t *testing.T) {
	store := New(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	examples, err := store.ListProviderExamples()
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 2 || examples[0].CLI != domain.CLICodex || examples[1].CLI != domain.CLIClaude {
		t.Fatalf("unexpected default provider examples: %+v", examples)
	}
	for _, example := range examples {
		if example.ID == "" || example.Name == "" || example.BaseURL == "" || example.UpdatedAt.IsZero() {
			t.Fatalf("incomplete seed example: %+v", example)
		}
	}

	custom := domain.ProviderExample{
		ID:          "ray-codex",
		Name:        "Ray Codex",
		CLI:         domain.CLICodex,
		BaseURL:     "http://newapi.raycloud.cn/v1/",
		Model:       "gpt-5.6-sol",
		Provider:    "custom",
		Description: "内部 OpenAI-compatible 示例",
	}
	saved, err := store.UpsertProviderExample(custom)
	if err != nil {
		t.Fatal(err)
	}
	if saved.BaseURL != "http://newapi.raycloud.cn/v1" || saved.UpdatedAt.IsZero() {
		t.Fatalf("unexpected saved example: %+v", saved)
	}
	custom.Name = "Ray Codex Updated"
	updated, err := store.UpsertProviderExample(custom)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != custom.Name || updated.UpdatedAt.Before(saved.UpdatedAt) {
		t.Fatalf("example was not updated: %+v", updated)
	}
	examples, err = store.ListProviderExamples()
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 3 {
		t.Fatalf("upsert created duplicate examples: %+v", examples)
	}
	deleted, err := store.DeleteProviderExample(custom.ID)
	if err != nil || !deleted {
		t.Fatalf("delete failed: deleted=%v err=%v", deleted, err)
	}
	deleted, err = store.DeleteProviderExample(custom.ID)
	if err != nil || deleted {
		t.Fatalf("second delete should be a no-op: deleted=%v err=%v", deleted, err)
	}

	for _, invalid := range []domain.ProviderExample{
		{ID: "secret-query", Name: "Bad", CLI: domain.CLICodex, BaseURL: "https://example.test/v1?access_token=abcdef"},
		{ID: "secret-description", Name: "Bad", CLI: domain.CLIClaude, BaseURL: "https://example.test", Description: "sk-abcdefghijklmnop"},
		{ID: "bad-cli", Name: "Bad", CLI: domain.CLI("other"), BaseURL: "https://example.test"},
	} {
		if _, err = store.UpsertProviderExample(invalid); err == nil {
			t.Fatalf("accepted invalid provider example: %+v", invalid)
		}
	}

	rows, err := store.db.Query(`SELECT name FROM pragma_table_info('provider_examples')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err = rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	joined := strings.Join(columns, " ")
	for _, forbidden := range []string{"key", "secret", "token", "auth", "webhook"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("provider example schema contains forbidden column %q: %s", forbidden, joined)
		}
	}
}

func TestProviderExamplesV4MigrationAndSeedsAreOneTime(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	if _, err := store.UpsertProviderExample(domain.ProviderExample{
		ID: "custom", Name: "Custom", CLI: domain.CLICodex, BaseURL: "https://example.test/v1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DROP TABLE provider_examples`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM schema_migrations WHERE version = 4`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := New(dir)
	examples, err := reopened.ListProviderExamples()
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 2 {
		t.Fatalf("v4 migration did not seed defaults: %+v", examples)
	}
	if _, err = reopened.DeleteProviderExample("codex-openai-compatible"); err != nil {
		t.Fatal(err)
	}
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}

	again := New(dir)
	t.Cleanup(func() { _ = again.Close() })
	examples, err = again.ListProviderExamples()
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) != 1 || examples[0].CLI != domain.CLIClaude {
		t.Fatalf("deleted seed was unexpectedly recreated: %+v", examples)
	}
}

func TestSchedulesV5CRUDIsBoundedAndContainsNoSecretColumns(t *testing.T) {
	dir := t.TempDir()
	st := New(dir)
	now := time.Date(2026, 7, 13, 6, 0, 0, 0, time.UTC)
	value, err := st.UpsertSchedule(domain.Schedule{
		ID: "workday-codex", Name: "工作日 Codex 保活", Enabled: true,
		CLI: domain.CLICodex, ProviderID: "provider-1", Mode: domain.ModeKeepalive,
		Timezone: "Asia/Shanghai", WeekdaysMask: 62, StartMinute: 9 * 60, EndMinute: 18 * 60,
		UntilSuccess: true, TimeoutSeconds: 15, RetryIntervalSeconds: 2,
		KeepaliveIntervalSeconds: 120, FailureThreshold: 3, Model: "gpt-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if value.ID != "workday-codex" || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() {
		t.Fatalf("unexpected saved schedule: %+v", value)
	}
	if err = st.MarkScheduleRun(value.ID, "occurrence-1", string(domain.JobSuccess), "job-1", now); err != nil {
		t.Fatal(err)
	}
	value.Name = "更新后的名称"
	updated, err := st.UpsertSchedule(value)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != value.Name || updated.LastOccurrenceKey != "occurrence-1" || updated.LastJobID != "job-1" || updated.LastOccurrenceAt == nil || !updated.LastOccurrenceAt.Equal(now) {
		t.Fatalf("schedule runtime snapshot was not preserved: %+v", updated)
	}

	rows, err := st.db.Query(`SELECT name FROM pragma_table_info('schedules')`)
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for rows.Next() {
		var column string
		if err = rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	rows.Close()
	joined := strings.Join(columns, " ")
	for _, forbidden := range []string{"api_key", "base_url", "prompt", "expected", "env", "auth", "webhook", "secret", "output"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("schedule schema contains forbidden column %q: %s", forbidden, joined)
		}
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := New(dir)
	t.Cleanup(func() { _ = reopened.Close() })
	values, err := reopened.ListSchedules()
	if err != nil || len(values) != 1 || values[0].ID != value.ID {
		t.Fatalf("schedule did not survive reopen: values=%+v err=%v", values, err)
	}
}

func TestScheduleLimitAndValidation(t *testing.T) {
	st := New(t.TempDir())
	t.Cleanup(func() { _ = st.Close() })
	base := domain.Schedule{
		Name: "Rule", Enabled: true, CLI: domain.CLICodex, ProviderID: "provider",
		Mode: domain.ModeProbe, Timezone: "UTC", WeekdaysMask: 127,
		StartMinute: 0, EndMinute: 1440, UntilSuccess: true,
		TimeoutSeconds: 15, RetryIntervalSeconds: 2,
		KeepaliveIntervalSeconds: 120, FailureThreshold: 3,
	}
	for index := 0; index < maxSchedules; index++ {
		value := base
		value.ID = fmt.Sprintf("schedule-%03d", index)
		if _, err := st.UpsertSchedule(value); err != nil {
			t.Fatalf("save schedule %d: %v", index, err)
		}
	}
	value := base
	value.ID = "schedule-over-limit"
	if _, err := st.UpsertSchedule(value); !errors.Is(err, ErrScheduleLimit) {
		t.Fatalf("got %v, want schedule limit", err)
	}
	value = base
	value.ID = "invalid-timezone"
	value.Timezone = "Not/A_Real_Zone"
	if _, err := st.UpsertSchedule(value); err == nil {
		t.Fatal("invalid timezone was accepted")
	}
	value = base
	value.ID = "leaky-model"
	value.Model = "sk-abcdefghijklmnop"
	if _, err := st.UpsertSchedule(value); err == nil {
		t.Fatal("credential-looking schedule value was accepted")
	}
}

func TestSQLiteBoundedJournalPragmasSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	assertSQLitePragmas(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := New(dir)
	t.Cleanup(func() { _ = reopened.Close() })
	assertSQLitePragmas(t, reopened)
}

func assertSQLitePragmas(t *testing.T, store *JSON) {
	t.Helper()
	var autoVacuum, journalLimit, checkpoint int64
	var journalMode string
	if err := store.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&autoVacuum); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`PRAGMA journal_size_limit`).Scan(&journalLimit); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`PRAGMA wal_autocheckpoint`).Scan(&checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if autoVacuum != 2 || journalLimit != 4<<20 || checkpoint != 256 || strings.ToLower(journalMode) != "wal" {
		t.Fatalf("unexpected pragmas auto_vacuum=%d journal_size_limit=%d wal_autocheckpoint=%d journal_mode=%s", autoVacuum, journalLimit, checkpoint, journalMode)
	}
}

func TestLegacyMigrationImportsEachDatasetIndependently(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	if err := store.SaveSettings(domain.Settings{TimeoutSeconds: 10, RetryIntervalSeconds: 1, KeepaliveIntervalSeconds: 10, HistoryLimit: 5}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate a database created by schema v1 before the legacy-data migration
	// was applied, then add stale legacy JSON beside it.
	db, err := openTestDatabase(filepath.Join(dir, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DELETE FROM schema_migrations WHERE version >= 2`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(dir, "settings.json"), domain.Settings{TimeoutSeconds: 999})
	writeJSONFile(t, filepath.Join(dir, "summaries.json"), []domain.Summary{{ID: "stale", StartedAt: time.Now()}})

	reopened := New(dir)
	t.Cleanup(func() { _ = reopened.Close() })
	settings, err := reopened.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.TimeoutSeconds != 10 {
		t.Fatalf("non-empty database was overwritten: %+v", settings)
	}
	summaries, err := reopened.LoadSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].ID != "stale" {
		t.Fatalf("empty summary table was not migrated independently: %+v", summaries)
	}
}

func TestEventsListFilterCountAndClear(t *testing.T) {
	store := New(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	values := []Event{
		{At: base, Type: "job.started", Level: "info", ProviderID: "p1", JobID: "j1", Message: "started", Data: map[string]any{"attempt": float64(1)}},
		{At: base.Add(time.Minute), Type: "attempt.failed", Level: "warning", ProviderID: "p1", JobID: "j1", Message: "timeout"},
		{At: base.Add(2 * time.Minute), Type: "job.started", Level: "info", ProviderID: "p2", JobID: "j2", Message: "started"},
	}
	for _, value := range values {
		if err := store.SaveEvent(value); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.ListEvents(EventFilter{ProviderID: "p1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Type != "attempt.failed" || got[1].At != base {
		t.Fatalf("unexpected provider events: %+v", got)
	}
	got, err = store.ListEvents(EventFilter{Type: "job.started", Since: base.Add(time.Second), Until: base.Add(3 * time.Minute), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ProviderID != "p2" {
		t.Fatalf("unexpected filtered events: %+v", got)
	}
	count, err := store.CountEvents(EventFilter{Level: "info"})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("got count %d", count)
	}
	deleted, err := store.ClearEvents()
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("cleared %d events", deleted)
	}
	count, err = store.CountEvents(EventFilter{})
	if err != nil || count != 0 {
		t.Fatalf("events remain after clear: count=%d err=%v", count, err)
	}
}

func TestEventRetentionByAgeRowsAndBytes(t *testing.T) {
	t.Run("age and rows", func(t *testing.T) {
		store := New(t.TempDir())
		t.Cleanup(func() { _ = store.Close() })
		now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
		for index, age := range []time.Duration{3 * time.Hour, 90 * time.Minute, 30 * time.Minute, 10 * time.Minute} {
			if err := store.SaveEvent(Event{At: now.Add(-age), Type: "test", Message: string(rune('a' + index))}); err != nil {
				t.Fatal(err)
			}
		}
		result, err := store.RetainEvents(EventRetention{MaxAge: 2 * time.Hour, MaxRows: 2, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if result.Deleted != 2 || result.Count != 2 {
			t.Fatalf("unexpected retention result: %+v", result)
		}
		remaining, err := store.ListEvents(EventFilter{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(remaining) != 2 || remaining[0].Message != "d" || remaining[1].Message != "c" {
			t.Fatalf("unexpected remaining events: %+v", remaining)
		}
	})

	t.Run("bytes applied during insert", func(t *testing.T) {
		store := New(t.TempDir())
		t.Cleanup(func() { _ = store.Close() })
		policy := EventRetention{MaxBytes: 150}
		for index := 0; index < 4; index++ {
			value := Event{Type: "test", Message: strings.Repeat(string(rune('a'+index)), 64)}
			if err := store.SaveEvent(value, policy); err != nil {
				t.Fatal(err)
			}
		}
		result, err := store.RetainEvents(policy)
		if err != nil {
			t.Fatal(err)
		}
		if result.Bytes > policy.MaxBytes || result.Count >= 4 || result.Count == 0 {
			t.Fatalf("byte retention failed: %+v", result)
		}
	})
}

func TestEventsRejectSecretsAndForbiddenRawFields(t *testing.T) {
	store := New(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	cases := []Event{
		{Type: "bad", Message: "token sk-abcdefghijklmnop"},
		{Type: "bad", Data: map[string]any{"apiKey": "masked-or-not"}},
		{Type: "bad", Data: map[string]any{"nested": map[string]any{"output": "READY"}}},
		{Type: "bad", Data: map[string]any{"url": "https://oapi.example/robot?access_token=abcdef"}},
	}
	for _, value := range cases {
		if err := store.SaveEvent(value); err == nil {
			t.Fatalf("accepted sensitive event: %+v", value)
		}
	}
	count, err := store.CountEvents(EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("persisted %d sensitive events", count)
	}
}

func TestDatabaseReopenPreservesEvents(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	at := time.Date(2026, 7, 13, 10, 0, 0, 123, time.UTC)
	if err := store.SaveEvent(Event{At: at, Type: "job.finished", Level: "success", JobID: "job", Data: map[string]any{"attempts": float64(3)}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := New(dir)
	t.Cleanup(func() { _ = reopened.Close() })
	events, err := reopened.ListEvents(EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].At != at || events[0].JobID != "job" || events[0].Data["attempts"] != float64(3) {
		t.Fatalf("event did not survive reopen: %+v", events)
	}
}

func TestDiagnosticsReturnsCountsWithoutStoredContent(t *testing.T) {
	st := New(t.TempDir())
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SaveEvent(Event{Type: "job_state", Message: "safe summary"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSchedule(domain.Schedule{
		ID: "diagnostic-schedule", Name: "Diagnostic", Enabled: true,
		CLI: domain.CLICodex, ProviderID: "provider", Mode: domain.ModeProbe,
		Timezone: "UTC", WeekdaysMask: 127, StartMinute: 0, EndMinute: 1439,
		UntilSuccess: true, TimeoutSeconds: 15, RetryIntervalSeconds: 2,
		KeepaliveIntervalSeconds: 120, FailureThreshold: 3,
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := st.Diagnostics()
	if err != nil {
		t.Fatal(err)
	}
	if stats.SchemaVersion < 7 || stats.LogicalBytes <= 0 || stats.EventCount != 1 || stats.ScheduleCount != 1 {
		t.Fatalf("unexpected diagnostics: %+v", stats)
	}
}

func writeJSONFile(t *testing.T, file string, value any) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func openTestDatabase(file string) (*sql.DB, error) {
	return sql.Open("sqlite3", "file:"+file+"?_busy_timeout=5000&_journal_mode=WAL")
}
