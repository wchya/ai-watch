package jobs

import (
	"testing"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

func TestProviderStatesMergeRuntimeHistoryAndSchedule(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	m := New(fakeResolver{}, execFunc(nil), st)
	defer m.Shutdown()
	now := time.Now().UTC()
	successAt := now.Add(-time.Minute)
	failureAt := now.Add(-2 * time.Minute)
	m.mu.Lock()
	m.history = []domain.Summary{
		{ID: "success", CLI: domain.CLICodex, ProviderID: "provider-1", Status: domain.JobSuccess, LatestAttempt: domain.AttemptSuccess, Attempts: 2, StartedAt: successAt, EndedAt: &successAt},
		{ID: "failure", CLI: domain.CLICodex, ProviderID: "provider-1", Status: domain.JobFailed, LatestAttempt: domain.AttemptTimeout, Attempts: 3, StartedAt: failureAt, EndedAt: &failureAt},
	}
	m.jobs["active"] = &runtime{job: domain.Job{ID: "active", CLI: domain.CLICodex, ProviderID: "provider-1", Status: domain.JobRunning, Phase: domain.JobPhaseRecoveryProbe, LatestAttempt: domain.AttemptTimeout, Attempts: 4}, consecutiveFailures: 2}
	m.mu.Unlock()
	if _, err := st.UpsertSchedule(domain.Schedule{
		ID: "schedule-1", Name: "工作日保活", Enabled: true, CLI: domain.CLICodex,
		ProviderID: "provider-1", Mode: domain.ModeKeepalive, Timezone: "UTC",
		WeekdaysMask: 127, StartMinute: 0, EndMinute: 1440, UntilSuccess: true,
		TimeoutSeconds: 10, RetryIntervalSeconds: 2, KeepaliveIntervalSeconds: 60, FailureThreshold: 3,
	}); err != nil {
		t.Fatal(err)
	}
	states := m.ProviderStates()
	state, ok := ProviderStateFor(states, domain.CLICodex, "provider-1")
	if !ok || state.Status != "recovering" || state.ActiveJobID != "active" || state.Attempts != 9 || state.ConsecutiveFailures != 2 {
		t.Fatalf("unexpected runtime state: %+v", state)
	}
	if state.LastSuccessAt == nil || state.LastFailureAt == nil || !state.ScheduleEnabled || state.ScheduleName != "工作日保活" {
		t.Fatalf("missing history or schedule state: %+v", state)
	}
}
