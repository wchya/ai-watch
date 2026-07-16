package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/configscan"
	"ai-watch/internal/domain"
	"ai-watch/internal/jobs"
	"ai-watch/internal/store"
)

func TestReliabilityActionsResolveRelationsRetestValidateAndPause(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	manager := jobs.New(apiResolver{}, apiExecutor{}, st)
	defer manager.Shutdown()
	if _, err := st.UpsertProviderGroup(domain.ProviderGroup{ID: "codex-group", Name: "Codex Group", CLI: domain.CLICodex, Enabled: true, PrimaryProviderID: "provider-a", BackupProviderIDs: []string{"provider-b"}, ScenarioID: "basic-ready", FailureThreshold: 3, CooldownSeconds: 60, Mode: "advisory", RecoveryThreshold: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpsertSchedule(domain.Schedule{ID: "schedule-direct", Name: "Direct", Enabled: true, CLI: domain.CLICodex, ProviderID: "provider-a", Mode: domain.ModeProbe, Timezone: "Asia/Shanghai", WeekdaysMask: 127, StartMinute: 0, EndMinute: 1440, UntilSuccess: true, TimeoutSeconds: 15, RetryIntervalSeconds: 3, KeepaliveIntervalSeconds: 120, FailureThreshold: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpsertSchedule(domain.Schedule{ID: "schedule-group", Name: "Group", Enabled: true, CLI: domain.CLICodex, ProviderGroupID: "codex-group", Mode: domain.ModeProbe, Timezone: "Asia/Shanghai", WeekdaysMask: 127, StartMinute: 0, EndMinute: 1440, UntilSuccess: true, TimeoutSeconds: 15, RetryIntervalSeconds: 3, KeepaliveIntervalSeconds: 120, FailureThreshold: 3}); err != nil {
		t.Fatal(err)
	}
	handler := New(configscan.New(), manager, "", st).Handler()

	contextResponse := httptest.NewRecorder()
	handler.ServeHTTP(contextResponse, httptest.NewRequest(http.MethodGet, "/api/reliability/actions?cli=codex&providerId=provider-a", nil))
	if contextResponse.Code != http.StatusOK || !strings.Contains(contextResponse.Body.String(), `"canValidateBackup":true`) || !strings.Contains(contextResponse.Body.String(), "schedule-direct") || !strings.Contains(contextResponse.Body.String(), "schedule-group") {
		t.Fatalf("context status=%d body=%s", contextResponse.Code, contextResponse.Body.String())
	}

	retest := httptest.NewRecorder()
	handler.ServeHTTP(retest, httptest.NewRequest(http.MethodPost, "/api/reliability/actions", strings.NewReader(`{"cli":"codex","providerId":"provider-a","action":"retest"}`)))
	if retest.Code != http.StatusAccepted || !strings.Contains(retest.Body.String(), `"action":"retest"`) {
		t.Fatalf("retest status=%d body=%s", retest.Code, retest.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for manager.ActiveCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	validate := httptest.NewRecorder()
	handler.ServeHTTP(validate, httptest.NewRequest(http.MethodPost, "/api/reliability/actions", strings.NewReader(`{"cli":"codex","providerId":"provider-a","action":"validate_backup"}`)))
	if validate.Code != http.StatusAccepted || !strings.Contains(validate.Body.String(), `"candidateProviderId":"provider-b"`) {
		t.Fatalf("validate status=%d body=%s", validate.Code, validate.Body.String())
	}

	pause := httptest.NewRecorder()
	handler.ServeHTTP(pause, httptest.NewRequest(http.MethodPost, "/api/reliability/actions", strings.NewReader(`{"cli":"codex","providerId":"provider-a","action":"pause_schedules"}`)))
	if pause.Code != http.StatusOK || !strings.Contains(pause.Body.String(), `"paused":2`) {
		t.Fatalf("pause status=%d body=%s", pause.Code, pause.Body.String())
	}
	pauseAgain := httptest.NewRecorder()
	handler.ServeHTTP(pauseAgain, httptest.NewRequest(http.MethodPost, "/api/reliability/actions", strings.NewReader(`{"cli":"codex","providerId":"provider-a","action":"pause_schedules"}`)))
	if pauseAgain.Code != http.StatusOK || !strings.Contains(pauseAgain.Body.String(), `"paused":0`) {
		t.Fatalf("idempotent pause status=%d body=%s", pauseAgain.Code, pauseAgain.Body.String())
	}
}
