package store

import (
	"testing"
	"time"

	"ai-watch/internal/domain"
)

func TestSQLiteIncidentCRUDAndOpenLookup(t *testing.T) {
	st := New(t.TempDir())
	defer st.Close()
	now := time.Now().UTC()
	saved, err := st.UpsertIncident(domain.Incident{SubjectType: "provider", SubjectID: "provider-a", ProviderID: "provider-a", Title: "Provider A 请求失败", Status: "open", Severity: "warning", FailureCount: 1, ErrorCounts: map[string]int{"timeout": 1}, RequestIDs: []string{"request-a"}, Timeline: []domain.IncidentEntry{{ID: "entry-a", At: now, Type: "failure", Message: "timeout"}}, StartedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	open, err := st.FindOpenIncident("provider", "provider-a")
	if err != nil || open.ID != saved.ID {
		t.Fatalf("open=%+v err=%v", open, err)
	}
	values, err := st.ListIncidents("open")
	if err != nil || len(values) != 1 {
		t.Fatalf("values=%+v err=%v", values, err)
	}
	resolved := now.Add(time.Minute)
	saved.Status, saved.ResolvedAt = "resolved", &resolved
	if _, err = st.UpsertIncident(saved); err != nil {
		t.Fatal(err)
	}
	if values, err = st.ListIncidents("resolved"); err != nil || len(values) != 1 {
		t.Fatalf("resolved=%+v err=%v", values, err)
	}
}

func TestSQLiteIncidentPostmortemRoundTrip(t *testing.T) {
	st := New(t.TempDir())
	defer st.Close()
	now := time.Now().UTC()
	value := domain.IncidentPostmortem{IncidentID: "incident-postmortem", Status: "draft", Title: "Provider failure", Subject: "Provider A", Severity: "critical", StartedAt: now, FailureCount: 3, ErrorCounts: map[string]int{"timeout": 3}, RequestIDs: []string{"request-a"}, Timeline: []domain.IncidentEntry{{ID: "entry-a", At: now, Type: "failure", Message: "timeout"}}, RootCause: "network", Mitigation: "switched", Owner: "ops", Actions: []domain.PostmortemAction{{Text: "add probe", Owner: "ops"}}}
	saved, err := st.UpsertIncidentPostmortem(value)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := st.GetIncidentPostmortem(value.IncidentID)
	if err != nil || loaded.RootCause != "network" || loaded.CreatedAt.IsZero() || len(loaded.Actions) != 1 || saved.UpdatedAt.IsZero() {
		t.Fatalf("loaded=%+v saved=%+v err=%v", loaded, saved, err)
	}
}
