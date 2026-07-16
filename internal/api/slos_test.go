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

func TestSLOConfigureCalculatePauseAndResume(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	manager := jobs.New(apiResolver{}, apiExecutor{}, st)
	defer manager.Shutdown()
	group, err := st.UpsertProviderGroup(domain.ProviderGroup{ID: "slo-group", Name: "SLO Group", CLI: domain.CLICodex, Enabled: true, PrimaryProviderID: "primary", BackupProviderIDs: []string{"backup"}, ScenarioID: "basic-ready", Mode: "automatic"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for index, sample := range []struct {
		status      string
		maintenance bool
	}{{"success", false}, {"success", false}, {"failed", false}, {"failed", true}, {"stopped", false}} {
		if err = st.SaveEvent(store.Event{At: now.Add(time.Duration(index-5) * time.Minute), Type: "request_end", ProviderID: "primary", Data: map[string]any{"requestId": "request-" + sample.status + time.Duration(index).String(), "providerGroupId": group.ID, "status": sample.status, "maintenanceActive": sample.maintenance}}); err != nil {
			t.Fatal(err)
		}
	}
	handler := New(configscan.New(), manager, "", st).Handler()
	configure := httptest.NewRecorder()
	handler.ServeHTTP(configure, httptest.NewRequest(http.MethodPut, "/api/slos/"+group.ID, strings.NewReader(`{"targetPercent":99,"window":"24h","minimumSamples":3}`)))
	if configure.Code != http.StatusOK || !strings.Contains(configure.Body.String(), `"status":"exhausted"`) || !strings.Contains(configure.Body.String(), `"samples":3`) || !strings.Contains(configure.Body.String(), `"excluded":1`) {
		t.Fatalf("configure=%d %s", configure.Code, configure.Body.String())
	}
	pause := httptest.NewRecorder()
	handler.ServeHTTP(pause, httptest.NewRequest(http.MethodPost, "/api/slos/"+group.ID+"/pause", nil))
	if pause.Code != http.StatusOK || !strings.Contains(pause.Body.String(), `"status":"disabled"`) {
		t.Fatalf("pause=%d %s", pause.Code, pause.Body.String())
	}
	resume := httptest.NewRecorder()
	handler.ServeHTTP(resume, httptest.NewRequest(http.MethodPost, "/api/slos/"+group.ID+"/resume", nil))
	if resume.Code != http.StatusOK || strings.Contains(resume.Body.String(), `"status":"disabled"`) {
		t.Fatalf("resume=%d %s", resume.Code, resume.Body.String())
	}
	events, err := st.ListEvents(store.EventFilter{Type: "slo_configured", Limit: 10})
	if err != nil || len(events) != 1 || events[0].Data["groupId"] != group.ID {
		t.Fatalf("audit=%+v err=%v", events, err)
	}
}

func TestSLOValidationAndInsufficientSamples(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	manager := jobs.New(apiResolver{}, apiExecutor{}, st)
	defer manager.Shutdown()
	group, err := st.UpsertProviderGroup(domain.ProviderGroup{ID: "slo-validation", Name: "SLO Validation", CLI: domain.CLICodex, Enabled: true, PrimaryProviderID: "primary", BackupProviderIDs: []string{"backup"}, ScenarioID: "basic-ready", Mode: "advisory"})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(configscan.New(), manager, "", st).Handler()
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodPut, "/api/slos/"+group.ID, strings.NewReader(`{"targetPercent":89,"window":"year","minimumSamples":0}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid=%d %s", invalid.Code, invalid.Body.String())
	}
	valid := httptest.NewRecorder()
	handler.ServeHTTP(valid, httptest.NewRequest(http.MethodPut, "/api/slos/"+group.ID, strings.NewReader(`{"targetPercent":99.9,"window":"7d","minimumSamples":20}`)))
	if valid.Code != http.StatusOK || !strings.Contains(valid.Body.String(), `"status":"insufficient"`) {
		t.Fatalf("valid=%d %s", valid.Code, valid.Body.String())
	}
}
