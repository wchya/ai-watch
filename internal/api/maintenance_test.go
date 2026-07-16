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

func TestMaintenanceWindowStartExtendEndAndSuppressEvaluation(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	manager := jobs.New(apiResolver{}, apiExecutor{}, st)
	defer manager.Shutdown()
	group, err := st.UpsertProviderGroup(domain.ProviderGroup{ID: "maintenance-group", Name: "Maintenance Group", CLI: domain.CLICodex, Enabled: true, PrimaryProviderID: "primary", BackupProviderIDs: []string{"backup"}, ScenarioID: "basic-ready", Mode: "automatic", FailureThreshold: 3, RecoveryThreshold: 2, RecoveryProbeIntervalSeconds: 300, CooldownSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	incident, err := st.UpsertIncident(domain.Incident{SubjectType: "group", SubjectID: group.ID, GroupID: group.ID, ProviderID: "primary", Title: "Group failed", Status: "open", Severity: "warning", StartedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(configscan.New(), manager, "", st).Handler()

	start := httptest.NewRecorder()
	until := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	handler.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/api/maintenance-windows/"+group.ID+"/start", strings.NewReader(`{"until":"`+until+`"}`)))
	if start.Code != http.StatusOK || !strings.Contains(start.Body.String(), `"status":"active"`) || !strings.Contains(start.Body.String(), `"notificationsMuted":true`) {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	updatedIncident, _ := st.GetIncident(incident.ID)
	if updatedIncident.MaintenanceStartsAt == nil || updatedIncident.MaintenanceUntil == nil {
		t.Fatalf("incident maintenance not synced: %+v", updatedIncident)
	}

	evaluate := httptest.NewRecorder()
	handler.ServeHTTP(evaluate, httptest.NewRequest(http.MethodPost, "/api/provider-groups/"+group.ID+"/evaluate", nil))
	if evaluate.Code != http.StatusConflict || !strings.Contains(evaluate.Body.String(), "maintenance") {
		t.Fatalf("evaluate=%d %s", evaluate.Code, evaluate.Body.String())
	}

	extend := httptest.NewRecorder()
	handler.ServeHTTP(extend, httptest.NewRequest(http.MethodPost, "/api/maintenance-windows/"+group.ID+"/extend", strings.NewReader(`{"seconds":3600}`)))
	if extend.Code != http.StatusOK || !strings.Contains(extend.Body.String(), `"status":"active"`) {
		t.Fatalf("extend=%d %s", extend.Code, extend.Body.String())
	}

	overLimit := httptest.NewRecorder()
	handler.ServeHTTP(overLimit, httptest.NewRequest(http.MethodPost, "/api/maintenance-windows/"+group.ID+"/extend", strings.NewReader(`{"seconds":2592000}`)))
	if overLimit.Code != http.StatusBadRequest || !strings.Contains(overLimit.Body.String(), "no more than 30 days") {
		t.Fatalf("overLimit=%d %s", overLimit.Code, overLimit.Body.String())
	}

	end := httptest.NewRecorder()
	handler.ServeHTTP(end, httptest.NewRequest(http.MethodPost, "/api/maintenance-windows/"+group.ID+"/end", strings.NewReader(`{}`)))
	if end.Code != http.StatusOK || !strings.Contains(end.Body.String(), `"status":"none"`) {
		t.Fatalf("end=%d %s", end.Code, end.Body.String())
	}
	updatedIncident, _ = st.GetIncident(incident.ID)
	if updatedIncident.MaintenanceStartsAt != nil || updatedIncident.MaintenanceUntil != nil {
		t.Fatalf("incident maintenance not cleared: %+v", updatedIncident)
	}
}

func TestMaintenanceWindowActiveHonorsFutureStart(t *testing.T) {
	now := time.Now().UTC()
	starts, until := now.Add(time.Hour), now.Add(2*time.Hour)
	if domain.MaintenanceWindowActive(&starts, &until, now) {
		t.Fatal("future maintenance became active early")
	}
	if !domain.MaintenanceWindowActive(&starts, &until, now.Add(90*time.Minute)) {
		t.Fatal("maintenance did not become active")
	}
}
