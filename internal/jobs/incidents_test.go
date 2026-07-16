package jobs

import (
	"testing"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

func TestIncidentMutedHonorsMaintenanceStart(t *testing.T) {
	now := time.Now().UTC()
	startsAt, until := now.Add(time.Hour), now.Add(2*time.Hour)
	incident := domain.Incident{MaintenanceStartsAt: &startsAt, MaintenanceUntil: &until}
	if incidentMuted(incident, now) {
		t.Fatal("future maintenance muted incident notifications early")
	}
	if !incidentMuted(incident, now.Add(90*time.Minute)) {
		t.Fatal("active maintenance did not mute incident notifications")
	}
}

func TestIncidentAggregatesDuplicateRequestsAndRecovers(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	m := New(failoverResolver{}, execFunc(nil), st)
	defer m.Shutdown()
	now := time.Now().UTC()
	failure := store.Event{At: now, Type: "request_end", ProviderID: "provider-a", JobID: "job-a", Data: map[string]any{"requestId": "request-a", "status": "timeout", "errorType": "timeout", "error": "请求超时"}}
	m.aggregateIncident(failure)
	m.aggregateIncident(failure)
	values, err := st.ListIncidents("")
	if err != nil || len(values) != 1 {
		t.Fatalf("values=%+v err=%v", values, err)
	}
	if values[0].FailureCount != 1 || len(values[0].RequestIDs) != 1 || values[0].Status != "open" {
		t.Fatalf("incident=%+v", values[0])
	}
	for index := 1; index <= 2; index++ {
		if err = st.SaveEvent(store.Event{At: now.Add(time.Duration(index) * time.Second), Type: "request_end", ProviderID: "provider-a", JobID: "job-b", Data: map[string]any{"requestId": "request-b", "status": "success"}}); err != nil {
			t.Fatal(err)
		}
	}
	m.aggregateIncident(store.Event{At: now.Add(2 * time.Second), Type: "request_end", ProviderID: "provider-a", JobID: "job-b", Data: map[string]any{"requestId": "request-b", "status": "success"}})
	resolved, err := st.GetIncident(values[0].ID)
	if err != nil || resolved.Status != "resolved" || resolved.ResolvedAt == nil || resolved.RecoveryNotificationSent {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
}
