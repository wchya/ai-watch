package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ai-watch/internal/configscan"
	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

func TestIncidentAPIListsAcknowledgesMutesAndReopens(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	now := time.Now().UTC()
	incident, err := st.UpsertIncident(domain.Incident{SubjectType: "provider", SubjectID: "provider-a", ProviderID: "provider-a", Title: "Provider A 请求失败", Status: "open", Severity: "warning", StartedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	h := New(configscan.New(), nil, "", st).Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, request)
		return recorder
	}
	if result := call(http.MethodGet, "/api/incidents?status=open", ""); result.Code != http.StatusOK || !strings.Contains(result.Body.String(), incident.ID) {
		t.Fatalf("list status=%d body=%s", result.Code, result.Body.String())
	}
	if result := call(http.MethodPost, "/api/incidents/"+incident.ID+"/acknowledge", `{}`); result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"status":"acknowledged"`) {
		t.Fatalf("ack status=%d body=%s", result.Code, result.Body.String())
	}
	if result := call(http.MethodPost, "/api/incidents/"+incident.ID+"/mute", `{"seconds":300}`); result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"status":"muted"`) {
		t.Fatalf("mute status=%d body=%s", result.Code, result.Body.String())
	}
	if result := call(http.MethodPost, "/api/incidents/"+incident.ID+"/reopen", `{}`); result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"status":"open"`) {
		t.Fatalf("reopen status=%d body=%s", result.Code, result.Body.String())
	}
}

func TestIncidentPostmortemSnapshotEditCompleteReopenAndMarkdown(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	now := time.Now().UTC().Add(-10 * time.Minute)
	resolved := now.Add(5 * time.Minute)
	incident, err := st.UpsertIncident(domain.Incident{SubjectType: "group", SubjectID: "group-a", SubjectName: "Group A", GroupID: "group-a", Title: "Group A failed", Status: "resolved", Severity: "critical", FailureCount: 3, ErrorCounts: map[string]int{"timeout": 2, "overloaded": 1}, JobIDs: []string{"job-a"}, RequestIDs: []string{"request-a"}, Timeline: []domain.IncidentEntry{{ID: "entry-a", At: now, Type: "failure", Message: "request timed out", RequestID: "request-a"}, {ID: "entry-b", At: resolved, Type: "recovered", Message: "primary recovered"}}, Note: "switched to backup", StartedAt: now, ResolvedAt: &resolved})
	if err != nil {
		t.Fatal(err)
	}
	h := New(configscan.New(), nil, "", st).Handler()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, httptest.NewRequest(method, path, strings.NewReader(body)))
		return recorder
	}
	created := call(http.MethodPost, "/api/incidents/"+incident.ID+"/postmortem", "")
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"recoverySummary":"primary recovered"`) {
		t.Fatalf("created=%d %s", created.Code, created.Body.String())
	}
	incident.Timeline = append(incident.Timeline, domain.IncidentEntry{ID: "late", At: time.Now().UTC(), Type: "manual_note", Message: "late mutation"})
	if _, err = st.UpsertIncident(incident); err != nil {
		t.Fatal(err)
	}
	stable := call(http.MethodGet, "/api/incidents/"+incident.ID+"/postmortem", "")
	if strings.Contains(stable.Body.String(), "late mutation") {
		t.Fatalf("snapshot changed after incident mutation: %s", stable.Body.String())
	}
	saved := call(http.MethodPut, "/api/incidents/"+incident.ID+"/postmortem", `{"rootCause":"upstream timeout","mitigation":"failover","owner":"ops","actions":[{"text":"add alert","owner":"ops","completed":false}]}`)
	if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), "upstream timeout") {
		t.Fatalf("saved=%d %s", saved.Code, saved.Body.String())
	}
	completed := call(http.MethodPost, "/api/incidents/"+incident.ID+"/postmortem/complete", "")
	if completed.Code != http.StatusOK || !strings.Contains(completed.Body.String(), `"status":"completed"`) {
		t.Fatalf("completed=%d %s", completed.Code, completed.Body.String())
	}
	conflict := call(http.MethodPut, "/api/incidents/"+incident.ID+"/postmortem", `{"rootCause":"changed","mitigation":"","owner":"","actions":[]}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict=%d %s", conflict.Code, conflict.Body.String())
	}
	markdown := call(http.MethodGet, "/api/incidents/"+incident.ID+"/postmortem/markdown", "")
	if markdown.Code != http.StatusOK || !strings.Contains(markdown.Body.String(), "# 事故复盘") || !strings.Contains(markdown.Body.String(), "upstream timeout") || strings.Contains(markdown.Body.String(), "PROMPT") {
		t.Fatalf("markdown=%d %s", markdown.Code, markdown.Body.String())
	}
	reopened := call(http.MethodPost, "/api/incidents/"+incident.ID+"/postmortem/reopen", "")
	if reopened.Code != http.StatusOK || !strings.Contains(reopened.Body.String(), `"status":"draft"`) {
		t.Fatalf("reopened=%d %s", reopened.Code, reopened.Body.String())
	}
}
