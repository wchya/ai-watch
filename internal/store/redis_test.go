package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/domain"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestRedis(t *testing.T) (*Redis, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	value := NewRedisWithClient(t.TempDir(), "test", client, []byte("0123456789abcdef0123456789abcdef"))
	if err := value.Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = value.Close() })
	return value, server
}

func TestRedisReadWriteProbeRejectsReadOnlyState(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	server.SetError("READONLY You can't write against a read only replica")
	value := &Redis{client: client, prefix: "probe"}
	if err := value.verifyReadWrite(context.Background()); err == nil || !strings.Contains(err.Error(), "write access") {
		t.Fatalf("read-only Redis was accepted: %v", err)
	}
	server.SetError("")
}

func TestRedisCoreStoreAndWarmCache(t *testing.T) {
	st, server := newTestRedis(t)
	if value, err := server.Get("test:schema:version"); err != nil || value != "1" {
		t.Fatalf("schema metadata=%q err=%v", value, err)
	}
	if values, err := st.ListManualProviders(); err != nil || values == nil {
		t.Fatalf("empty manual provider list must be []: values=%v err=%v", values, err)
	}
	settings := domain.DefaultSettings()
	settings.TimeoutSeconds = 41
	if err := st.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadSettings()
	if err != nil || loaded.TimeoutSeconds != 41 {
		t.Fatalf("settings=%+v err=%v", loaded, err)
	}

	schedule := domain.Schedule{ID: "redis-schedule", Name: "Redis Schedule", Enabled: true, CLI: domain.CLICodex, ProviderID: "provider", Mode: domain.ModeProbe, Timezone: "UTC", WeekdaysMask: 127, StartMinute: 0, EndMinute: 1440, UntilSuccess: true, TimeoutSeconds: 15, RetryIntervalSeconds: 2, KeepaliveIntervalSeconds: 120, FailureThreshold: 3}
	savedSchedule, err := st.UpsertSchedule(schedule)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.MarkScheduleRun(schedule.ID, "occurrence-1", string(domain.JobStopped), "job-1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	markedSchedule, err := st.GetSchedule(schedule.ID)
	if err != nil || !markedSchedule.UpdatedAt.Equal(savedSchedule.UpdatedAt) {
		t.Fatalf("runtime update changed rule version: before=%s after=%s err=%v", savedSchedule.UpdatedAt, markedSchedule.UpdatedAt, err)
	}
	if values, listErr := st.ListSchedules(); listErr != nil || len(values) != 1 {
		t.Fatalf("schedules=%+v err=%v", values, listErr)
	}

	summary := domain.Summary{ID: "job-1", Mode: domain.ModeProbe, CLI: domain.CLICodex, Status: domain.JobSuccess, StartedAt: time.Now().UTC()}
	if err = st.SaveSummary(summary, 10); err != nil {
		t.Fatal(err)
	}
	if values, loadErr := st.LoadSummaries(); loadErr != nil || len(values) != 1 || values[0].ID != "job-1" {
		t.Fatalf("summaries=%+v err=%v", values, loadErr)
	}
}

func TestRedisIncidentPostmortemRoundTrip(t *testing.T) {
	st, _ := newTestRedis(t)
	now := time.Now().UTC()
	value := domain.IncidentPostmortem{IncidentID: "redis-incident", Status: "draft", Title: "Redis incident", Subject: "Provider A", Severity: "warning", StartedAt: now, ErrorCounts: map[string]int{"timeout": 1}, RootCause: "network", Mitigation: "retry", Actions: []domain.PostmortemAction{{Text: "add alert"}}}
	saved, err := st.UpsertIncidentPostmortem(value)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := st.GetIncidentPostmortem(value.IncidentID)
	if err != nil || loaded.RootCause != "network" || len(loaded.Actions) != 1 || saved.CreatedAt.IsZero() {
		t.Fatalf("loaded=%+v saved=%+v err=%v", loaded, saved, err)
	}
}

func TestRedisLegacySettingsInheritNewDigestDefaults(t *testing.T) {
	st, server := newTestRedis(t)
	server.Set("test:settings", `{"timeoutSeconds":41,"uiTheme":"deep-ocean"}`)
	loaded, err := st.loadSettingsRedis(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TimeoutSeconds != 41 || loaded.ReliabilityDigestHour != 9 || loaded.ReliabilityDigestMinute != 0 || loaded.ReliabilityDigestTimezone != "Asia/Shanghai" || loaded.ReliabilityDigestRange != "24h" {
		t.Fatalf("legacy settings did not inherit digest defaults: %+v", loaded)
	}
}

func TestRedisEventsFilterByRequestID(t *testing.T) {
	st, _ := newTestRedis(t)
	now := time.Now().UTC()
	for _, event := range []Event{
		{At: now, Type: "request_start", JobID: "job-a", Data: map[string]any{"requestId": "request-a"}},
		{At: now.Add(time.Second), Type: "request_end", JobID: "job-a", Data: map[string]any{"requestId": "request-a", "status": "success"}},
		{At: now.Add(2 * time.Second), Type: "request_end", JobID: "job-b", Data: map[string]any{"requestId": "request-b", "status": "failed"}},
	} {
		if err := st.SaveEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	values, err := st.ListEvents(EventFilter{RequestID: "request-a", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 {
		t.Fatalf("request filter returned %+v", values)
	}
	count, err := st.CountEvents(EventFilter{RequestID: "request-a"})
	if err != nil || count != 2 {
		t.Fatalf("request count=%d err=%v", count, err)
	}
}

func TestRedisProviderGroupCRUD(t *testing.T) {
	st, _ := newTestRedis(t)
	want := domain.ProviderGroup{ID: "claude-main", Name: "Claude 主备组", CLI: domain.CLIClaude, Enabled: true, PrimaryProviderID: "main", BackupProviderIDs: []string{"standby"}, ScenarioID: "basic-ready", FailureThreshold: 2, CooldownSeconds: 60, Mode: "automatic", RecoveryProbeIntervalSeconds: 45}
	saved, err := st.UpsertProviderGroup(want)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := st.GetProviderGroup(saved.ID)
	if err != nil || loaded.Name != want.Name || loaded.RecoveryProbeIntervalSeconds != 45 {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	values, err := st.ListProviderGroups()
	if err != nil || len(values) != 1 {
		t.Fatalf("values=%+v err=%v", values, err)
	}
}

func TestRedisEventsAreAtomicallyBounded(t *testing.T) {
	st, _ := newTestRedis(t)
	base := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	policy := EventRetention{MaxAge: 2 * time.Hour, MaxRows: 2, MaxBytes: 220, Now: base.Add(4 * time.Hour)}
	for index := 0; index < 4; index++ {
		if err := st.SaveEvent(Event{At: base.Add(time.Duration(index) * time.Hour), Type: "state", Message: strings.Repeat("x", 40)}, policy); err != nil {
			t.Fatal(err)
		}
	}
	values, err := st.ListEvents(EventFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) > 2 || len(values) == 0 || values[0].ID == 0 {
		t.Fatalf("events were not bounded or IDs were lost: %+v", values)
	}
	result, err := st.RetainEvents(policy)
	if err != nil || result.Count > 2 || result.Bytes > policy.MaxBytes {
		t.Fatalf("retention=%+v err=%v", result, err)
	}
	deleted, err := st.ClearEvents()
	if err != nil || deleted != result.Count {
		t.Fatalf("clear deleted=%d want=%d err=%v", deleted, result.Count, err)
	}
}

func TestRedisEventsFilterBySchedule(t *testing.T) {
	st, _ := newTestRedis(t)
	now := time.Now().UTC()
	for _, event := range []Event{
		{At: now, Type: "request_end", JobID: "job-1", Data: map[string]any{"scheduleId": "schedule-1"}},
		{At: now.Add(time.Second), Type: "request_end", JobID: "job-2", Data: map[string]any{"scheduleId": "schedule-2"}},
	} {
		if err := st.SaveEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	values, err := st.ListEvents(EventFilter{ScheduleID: "schedule-1", Limit: 10})
	if err != nil || len(values) != 1 || values[0].JobID != "job-1" {
		t.Fatalf("schedule events=%+v err=%v", values, err)
	}
}

func TestRedisIdempotencyClaimAndReplay(t *testing.T) {
	st, _ := newTestRedis(t)
	record, owner, err := st.ClaimIdempotency("operation-0001", "fingerprint-a", time.Hour)
	if err != nil || !owner || !record.Pending {
		t.Fatalf("claim=%+v owner=%v err=%v", record, owner, err)
	}
	if err := st.CompleteIdempotency("operation-0001", IdempotencyRecord{Fingerprint: "fingerprint-a", Status: 201, Body: []byte(`{"ok":true}`)}, time.Hour); err != nil {
		t.Fatal(err)
	}
	replay, owner, err := st.ClaimIdempotency("operation-0001", "fingerprint-a", time.Hour)
	if err != nil || owner || replay.Pending || replay.Status != 201 || string(replay.Body) != `{"ok":true}` {
		t.Fatalf("replay=%+v owner=%v err=%v", replay, owner, err)
	}
	conflict, owner, err := st.ClaimIdempotency("operation-0001", "fingerprint-b", time.Hour)
	if err != nil || owner || conflict.Fingerprint != "fingerprint-a" {
		t.Fatalf("conflict=%+v owner=%v err=%v", conflict, owner, err)
	}
}

func TestRedisJobEventsAreBoundedAndExpire(t *testing.T) {
	st, server := newTestRedis(t)
	policy := JobEventRetention{TTL: 24 * time.Hour, MaxRows: 2, MaxBytes: 1 << 20}
	for id := uint64(1); id <= 3; id++ {
		if err := st.SaveJobEvent("probe-1", domain.Event{ID: id, Type: "output", Message: strconv.FormatUint(id, 10)}, policy); err != nil {
			t.Fatal(err)
		}
	}
	values, err := st.ListJobEvents("probe-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].ID != 2 || values[1].ID != 3 {
		t.Fatalf("unexpected bounded events: %+v", values)
	}
	if ttl := server.TTL("test:job-logs:probe-1:data"); ttl != 24*time.Hour {
		t.Fatalf("ttl=%s want=24h", ttl)
	}
	server.FastForward(24 * time.Hour)
	values, err = st.ListJobEvents("probe-1", 0)
	if err != nil || len(values) != 0 {
		t.Fatalf("expired events=%+v err=%v", values, err)
	}
}

func TestRedisDeletesLegacyEventsByType(t *testing.T) {
	st, _ := newTestRedis(t)
	now := time.Now().UTC()
	for _, event := range []Event{
		{At: now, Type: "request_log", Message: "prompt must be removed"},
		{At: now.Add(time.Second), Type: "request_end", Message: "safe metadata"},
	} {
		if err := st.SaveEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := st.DeleteEventsByType("request_log")
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	values, err := st.ListEvents(EventFilter{Limit: 10})
	if err != nil || len(values) != 1 || values[0].Type != "request_end" {
		t.Fatalf("values=%+v err=%v", values, err)
	}
}

func TestRedisEncryptsManualProviderAndDingTalkSecrets(t *testing.T) {
	st, server := newTestRedis(t)
	provider, err := st.UpsertManualProvider(domain.ManualProvider{
		ID: "manual-one", Name: "Manual", CLI: domain.CLICodex, Enabled: true,
		BaseURL: "https://example.test/v1", APIKey: "sk-super-secret-value",
		ProxyMode: domain.ProxyCustom, ProxyURL: "socks5://proxy-user:proxy-password@proxy.example:1080",
	})
	if err != nil || !provider.HasAPIKey {
		t.Fatalf("provider=%+v err=%v", provider, err)
	}
	loaded, err := st.GetManualProvider("manual-one")
	if err != nil || loaded.APIKey != "sk-super-secret-value" || loaded.ProxyURL != "socks5://proxy-user:proxy-password@proxy.example:1080" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	if _, err = st.SaveDingTalkConfig(domain.DingTalkConfig{WebhookURL: "https://oapi.example/robot?access_token=secret", Source: "stored"}); err != nil {
		t.Fatal(err)
	}
	var dump strings.Builder
	for _, key := range server.Keys() {
		typ, _ := st.client.Type(context.Background(), key).Result()
		switch typ {
		case "string":
			value, _ := st.client.Get(context.Background(), key).Result()
			dump.WriteString(value)
		case "hash":
			values, _ := st.client.HGetAll(context.Background(), key).Result()
			for field, value := range values {
				dump.WriteString(field)
				dump.WriteString(value)
			}
		}
	}
	for _, secret := range []string{"sk-super-secret-value", "access_token=secret", "proxy-password"} {
		if strings.Contains(dump.String(), secret) {
			t.Fatalf("Redis contains plaintext secret %q", secret)
		}
	}
	loaded.ClearAPIKey = true
	loaded.APIKey = ""
	loaded.ClearProxyURL = true
	loaded.ProxyURL = ""
	cleared, err := st.UpsertManualProvider(loaded)
	if err != nil || cleared.HasAPIKey || cleared.HasProxyURL {
		t.Fatalf("clear provider key: value=%+v err=%v", cleared, err)
	}
	if config, loadErr := st.LoadDingTalkConfig(); loadErr != nil || !config.Configured || !strings.Contains(config.WebhookURL, "access_token") {
		t.Fatalf("config=%+v err=%v", config, loadErr)
	}
	if _, err = st.client.Ping(context.Background()).Result(); err != nil {
		t.Fatal(err)
	}
}

func TestRedisEncryptionUsesVersionedRandomNoncesAndRejectsWrongKey(t *testing.T) {
	st, _ := newTestRedis(t)
	first, err := st.encrypt("same-secret")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.encrypt("same-secret")
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || second.Version != 1 || first.Nonce == second.Nonce || first.Ciphertext == second.Ciphertext {
		t.Fatalf("envelopes are not independently versioned/random: first=%+v second=%+v", first, second)
	}
	block, err := aes.NewCipher([]byte("abcdef0123456789abcdef0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	wrongAEAD, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	wrong := &Redis{prefix: st.prefix, aead: wrongAEAD}
	if _, err = wrong.decrypt(first); err == nil {
		t.Fatal("wrong encryption key unexpectedly decrypted the secret")
	}
}

func TestRedisCCSwitchSnapshotEncryptsSecretsAndReplacesAtomically(t *testing.T) {
	st, server := newTestRedis(t)
	updatedAt := time.Date(2026, 7, 14, 8, 30, 0, 0, time.UTC)
	values := []domain.CCSwitchProvider{
		{
			ID: "claude-one", Name: "Claude One", CLI: domain.CLIClaude,
			BaseURL: "https://claude.example/v1", Model: "claude-test", Provider: "anthropic-compatible",
			APIKey: "claude-api-secret", ClaudeEnv: map[string]string{
				"ANTHROPIC_AUTH_TOKEN": "claude-env-secret",
				"ANTHROPIC_BASE_URL":   "https://claude.example/v1",
			}, UpdatedAt: updatedAt,
		},
		{
			ID: "codex-one", Name: "Codex One", CLI: domain.CLICodex, Current: true,
			BaseURL: "https://codex.example/v1", Model: "gpt-test", Provider: "openai-compatible",
			APIKey: "codex-api-secret", CodexConfig: "model = 'gpt-test'\n# codex-config-secret",
			UpdatedAt: updatedAt,
		},
	}
	if err := st.ReplaceCCSwitchProviders(values); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.ListCCSwitchProviders()
	if err != nil || len(loaded) != 2 || loaded[0].ID != "codex-one" || loaded[1].ID != "claude-one" {
		t.Fatalf("providers=%+v err=%v", loaded, err)
	}
	if loaded[0].APIKey != "codex-api-secret" || loaded[0].CodexConfig != values[1].CodexConfig || loaded[1].ClaudeEnv["ANTHROPIC_AUTH_TOKEN"] != "claude-env-secret" {
		t.Fatalf("decrypted provider snapshot does not match source: %+v", loaded)
	}
	loaded[1].ClaudeEnv["ANTHROPIC_AUTH_TOKEN"] = "mutated"
	again, err := st.GetCCSwitchProvider("claude-one")
	if err != nil || again.ClaudeEnv["ANTHROPIC_AUTH_TOKEN"] != "claude-env-secret" {
		t.Fatalf("cached provider was mutated by caller: provider=%+v err=%v", again, err)
	}

	var dump strings.Builder
	for _, key := range server.Keys() {
		typ, _ := st.client.Type(context.Background(), key).Result()
		switch typ {
		case "string":
			value, _ := st.client.Get(context.Background(), key).Result()
			dump.WriteString(value)
		case "hash":
			items, _ := st.client.HGetAll(context.Background(), key).Result()
			for field, value := range items {
				dump.WriteString(field)
				dump.WriteString(value)
			}
		}
	}
	for _, secret := range []string{"codex-api-secret", "codex-config-secret", "claude-api-secret", "claude-env-secret"} {
		if strings.Contains(dump.String(), secret) {
			t.Fatalf("Redis contains plaintext CC Switch secret %q", secret)
		}
	}

	replacement := []domain.CCSwitchProvider{{
		ID: "replacement", Name: "Replacement", CLI: domain.CLICodex,
		APIKey: "replacement-secret", UpdatedAt: updatedAt.Add(time.Minute),
	}}
	if err = st.ReplaceCCSwitchProviders(replacement); err != nil {
		t.Fatal(err)
	}
	if _, err = st.GetCCSwitchProvider("codex-one"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("old provider survived replacement: %v", err)
	}
	if loaded, err = st.ListCCSwitchProviders(); err != nil || len(loaded) != 1 || loaded[0].ID != "replacement" {
		t.Fatalf("replacement snapshot=%+v err=%v", loaded, err)
	}

	previousAEAD := st.aead
	st.aead = nil
	err = st.ReplaceCCSwitchProviders([]domain.CCSwitchProvider{{ID: "must-not-replace", CLI: domain.CLICodex}})
	st.aead = previousAEAD
	if err == nil {
		t.Fatal("snapshot replacement unexpectedly succeeded without encryption")
	}
	if loaded, loadErr := st.ListCCSwitchProviders(); loadErr != nil || len(loaded) != 1 || loaded[0].ID != "replacement" {
		t.Fatalf("failed replacement changed cached snapshot: providers=%+v err=%v", loaded, loadErr)
	}
	stored, err := st.listCCSwitchProvidersRedis(context.Background())
	if err != nil || len(stored) != 1 || stored[0].ID != "replacement" {
		t.Fatalf("failed replacement changed Redis snapshot: providers=%+v err=%v", stored, err)
	}

	if err = st.ReplaceCCSwitchProviders(nil); err != nil {
		t.Fatal(err)
	}
	if loaded, err = st.ListCCSwitchProviders(); err != nil || len(loaded) != 0 {
		t.Fatalf("empty snapshot=%+v err=%v", loaded, err)
	}
	if count, countErr := st.client.HLen(context.Background(), "test:cc-switch-providers").Result(); countErr != nil || count != 1 {
		t.Fatalf("empty snapshot must retain only its sentinel: count=%d err=%v", count, countErr)
	}
}

func TestRedisCCSwitchSnapshotPrewarmsAndRejectsWrongKey(t *testing.T) {
	st, server := newTestRedis(t)
	if err := st.ReplaceCCSwitchProviders([]domain.CCSwitchProvider{{
		ID: "encrypted", Name: "Encrypted", CLI: domain.CLICodex,
		APIKey: "wrong-key-secret", CodexConfig: "model = 'encrypted'",
	}}); err != nil {
		t.Fatal(err)
	}

	reopened := NewRedisWithClient(t.TempDir(), "test", redis.NewClient(&redis.Options{Addr: server.Addr()}), []byte("0123456789abcdef0123456789abcdef"))
	if err := reopened.Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if values, err := reopened.ListCCSwitchProviders(); err != nil || len(values) != 1 || values[0].APIKey != "wrong-key-secret" {
		t.Fatalf("prewarmed providers=%+v err=%v", values, err)
	}

	wrong := NewRedisWithClient(t.TempDir(), "test", redis.NewClient(&redis.Options{Addr: server.Addr()}), []byte("abcdef0123456789abcdef0123456789"))
	t.Cleanup(func() { _ = wrong.Close() })
	if err := wrong.Err(); err == nil || !strings.Contains(err.Error(), "decrypt CC Switch provider") {
		t.Fatalf("wrong encryption key was accepted: %v", err)
	}
}

func TestRedisCCSwitchSyncStatus(t *testing.T) {
	st, _ := newTestRedis(t)
	if value, err := st.LoadCCSwitchSyncStatus(); err != nil || !value.LastAttemptAt.IsZero() {
		t.Fatalf("initial status=%+v err=%v", value, err)
	}
	success := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	status := CCSwitchSyncStatus{
		SourceAvailable: true, LastAttemptAt: success, LastSuccessAt: &success,
		Count: 4, Warning: "",
	}
	if err := st.SaveCCSwitchSyncStatus(status); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadCCSwitchSyncStatus()
	if err != nil || !loaded.SourceAvailable || loaded.Count != 4 || loaded.LastSuccessAt == nil || !loaded.LastSuccessAt.Equal(success) {
		t.Fatalf("status=%+v err=%v", loaded, err)
	}
}

func TestRedisMigratesSQLiteOnceAndKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	legacy := New(dir)
	settings := domain.DefaultSettings()
	settings.TimeoutSeconds = 77
	if err := legacy.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := legacy.SaveEvent(Event{Type: "migrated", Message: "safe"}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(dir, databaseName)
	before, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	st := NewRedisWithClient(dir, "migration", client, []byte("0123456789abcdef0123456789abcdef"))
	if err := st.Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	loaded, _ := st.LoadSettings()
	count, _ := st.CountEvents(EventFilter{})
	if loaded.TimeoutSeconds != 77 || count != 1 || !server.Exists("migration:migration:sqlite-v1") {
		t.Fatalf("migration settings=%+v events=%d keys=%v", loaded, count, server.Keys())
	}
	if _, err := os.Stat(filepath.Join(dir, databaseName)); err != nil {
		t.Fatalf("SQLite backup was not retained: %v", err)
	}
	after, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("SQLite migration source was modified")
	}
	settings.TimeoutSeconds = 88
	if err = st.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := NewRedisWithClient(dir, "migration", redis.NewClient(&redis.Options{Addr: server.Addr()}), []byte("0123456789abcdef0123456789abcdef"))
	if err = reopened.Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	loaded, err = reopened.LoadSettings()
	if err != nil || loaded.TimeoutSeconds != 88 {
		t.Fatalf("migration marker did not prevent re-import: settings=%+v err=%v", loaded, err)
	}
}

func TestRedisScheduleRuleAndRuntimeUpdatesDoNotLoseEachOther(t *testing.T) {
	st, _ := newTestRedis(t)
	base := domain.Schedule{ID: "atomic-schedule", Name: "Initial", Enabled: true, CLI: domain.CLICodex, ProviderID: "provider", Mode: domain.ModeProbe, Timezone: "UTC", WeekdaysMask: 127, StartMinute: 0, EndMinute: 1440, UntilSuccess: true, TimeoutSeconds: 15, RetryIntervalSeconds: 2, KeepaliveIntervalSeconds: 120, FailureThreshold: 3}
	if _, err := st.UpsertSchedule(base); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 25; index++ {
		occurrence := "occurrence-" + strconv.Itoa(index)
		name := "Edited-" + strconv.Itoa(index)
		start := make(chan struct{})
		errorsCh := make(chan error, 2)
		go func() {
			<-start
			errorsCh <- st.MarkScheduleRun(base.ID, occurrence, string(domain.JobSuccess), "job", time.Now().UTC())
		}()
		go func() {
			<-start
			value := base
			value.Name = name
			_, err := st.UpsertSchedule(value)
			errorsCh <- err
		}()
		close(start)
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
		loaded, err := st.GetSchedule(base.ID)
		if err != nil || loaded.Name != name || loaded.LastOccurrenceKey != occurrence {
			t.Fatalf("lost concurrent schedule update: value=%+v err=%v", loaded, err)
		}
	}
}
