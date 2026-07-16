package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/runner"
	"ai-watch/internal/store"
)

type failoverResolver struct{}

func (failoverResolver) Resolve(cli domain.CLI, providerID string) (domain.ResolvedConfig, error) {
	return domain.ResolvedConfig{ProviderID: providerID, ProviderName: providerID, BaseURL: "https://" + providerID + ".example.test/v1", APIKey: "key", LockIdentity: providerID}, nil
}

func TestFailoverAdviceOpensOnceAndRecovers(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	group, err := st.UpsertProviderGroup(domain.ProviderGroup{ID: "codex-main", Name: "Codex 主备组", CLI: domain.CLICodex, Enabled: true, PrimaryProviderID: "primary", BackupProviderIDs: []string{"backup"}, ScenarioID: "basic-ready", FailureThreshold: 3, CooldownSeconds: 600, Mode: "automatic", RecoveryThreshold: 2})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	executor := execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		calls++
		now := time.Now().UTC()
		return runner.Result{Output: "READY", ExitCode: 0, StartedAt: now, EndedAt: now.Add(10 * time.Millisecond), DurationMillis: 10}, nil
	})
	m := New(failoverResolver{}, executor, st)
	defer m.Shutdown()
	now := time.Now().UTC()
	for index := 0; index < 3; index++ {
		if err = st.SaveEvent(store.Event{At: now.Add(time.Duration(index) * time.Millisecond), Type: "request_end", ProviderID: "primary", Data: map[string]any{"requestId": "primary-request", "status": "failed", "triggerSource": "manual"}}); err != nil {
			t.Fatal(err)
		}
	}
	m.evaluateFailover(store.Event{At: now, Type: "request_end", ProviderID: "primary", Data: map[string]any{"requestId": "primary-request", "status": "failed", "triggerSource": "manual"}})
	loaded, err := st.GetProviderGroup(group.ID)
	if err != nil || loaded.Advice == nil || loaded.Advice.Status != "open" || loaded.Advice.SuggestedProviderID != "backup" || loaded.Advice.ValidationRequestID == "" {
		t.Fatalf("advice=%+v err=%v", loaded.Advice, err)
	}
	if loaded.ActiveProviderID != "backup" {
		t.Fatalf("automatic group active provider=%q", loaded.ActiveProviderID)
	}
	m.evaluateFailover(store.Event{At: now.Add(500 * time.Millisecond), Type: "request_end", ProviderID: "primary", Data: map[string]any{"requestId": "duplicate-failure", "status": "failed", "triggerSource": "manual"}})
	if calls != 1 {
		t.Fatalf("duplicate advice started %d validations", calls)
	}
	for index := 1; index <= 2; index++ {
		if err = st.SaveEvent(store.Event{At: now.Add(time.Duration(index) * time.Second), Type: "request_end", ProviderID: "primary", Data: map[string]any{"requestId": "recovery-request", "status": "success", "triggerSource": "manual"}}); err != nil {
			t.Fatal(err)
		}
	}
	m.evaluateFailover(store.Event{At: now.Add(2 * time.Second), Type: "request_end", ProviderID: "primary", Data: map[string]any{"requestId": "recovery-request", "status": "success", "triggerSource": "manual"}})
	loaded, err = st.GetProviderGroup(group.ID)
	if err != nil || loaded.Advice == nil || loaded.Advice.Status != "recovered" || loaded.Advice.RecoveredAt == nil {
		t.Fatalf("recovered=%+v err=%v", loaded.Advice, err)
	}
	if loaded.ActiveProviderID != "primary" {
		t.Fatalf("recovered active provider=%q", loaded.ActiveProviderID)
	}
}

func TestAutomaticGroupProbesPrimaryAndRecoversAfterThreshold(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	var calls atomic.Int32
	executor := execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		calls.Add(1)
		now := time.Now().UTC()
		return runner.Result{Output: "READY", ExitCode: 0, StartedAt: now, EndedAt: now.Add(time.Millisecond), DurationMillis: 1}, nil
	})
	m := New(failoverResolver{}, executor, st)
	defer m.Shutdown()
	group, err := st.UpsertProviderGroup(domain.ProviderGroup{ID: "codex-recovery", Name: "Codex 自动恢复组", CLI: domain.CLICodex, Enabled: true, PrimaryProviderID: "primary", BackupProviderIDs: []string{"backup"}, ActiveProviderID: "backup", ScenarioID: "basic-ready", FailureThreshold: 3, CooldownSeconds: 60, Mode: "automatic", RecoveryThreshold: 2, RecoveryProbeIntervalSeconds: 30, Advice: &domain.FailoverAdvice{Status: "open", SuggestedProviderID: "backup", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	m.reconcileProviderGroupRecovery(base)
	waitForGroup := func(check func(domain.ProviderGroup) bool) domain.ProviderGroup {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			loaded, getErr := st.GetProviderGroup(group.ID)
			if getErr == nil && check(loaded) {
				return loaded
			}
			time.Sleep(10 * time.Millisecond)
		}
		loaded, _ := st.GetProviderGroup(group.ID)
		return loaded
	}
	first := waitForGroup(func(value domain.ProviderGroup) bool { return value.LastRecoveryProbeStatus == "success" })
	if first.ActiveProviderID != "backup" || calls.Load() != 1 {
		t.Fatalf("first recovery probe should keep backup active: group=%+v calls=%d", first, calls.Load())
	}
	m.reconcileProviderGroupRecovery(base.Add(31 * time.Second))
	recovered := waitForGroup(func(value domain.ProviderGroup) bool { return value.ActiveProviderID == "primary" })
	if recovered.Advice == nil || recovered.Advice.Status != "recovered" || calls.Load() != 2 {
		t.Fatalf("second recovery probe should restore primary: group=%+v calls=%d", recovered, calls.Load())
	}
}

func TestAutomaticRecoveryProbeSkipsMaintenanceWindow(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	var calls atomic.Int32
	m := New(failoverResolver{}, execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		calls.Add(1)
		return runner.Result{Output: "READY", ExitCode: 0}, nil
	}), st)
	defer m.Shutdown()
	maintenance := time.Now().UTC().Add(time.Hour)
	_, err := st.UpsertProviderGroup(domain.ProviderGroup{ID: "codex-maintenance", Name: "维护窗口组", CLI: domain.CLICodex, Enabled: true, PrimaryProviderID: "primary", BackupProviderIDs: []string{"backup"}, ActiveProviderID: "backup", ScenarioID: "basic-ready", Mode: "automatic", RecoveryThreshold: 1, RecoveryProbeIntervalSeconds: 30, MaintenanceUntil: &maintenance, Advice: &domain.FailoverAdvice{Status: "open", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}})
	if err != nil {
		t.Fatal(err)
	}
	m.reconcileProviderGroupRecovery(time.Now().UTC())
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("maintenance window started %d recovery probes", calls.Load())
	}
}

func TestApplyProviderGroupAdviceSwitchesOnlyBoundSchedulesAndIsIdempotent(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	m := New(failoverResolver{}, execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		return runner.Result{Output: "READY", ExitCode: 0}, nil
	}), st)
	defer m.Shutdown()
	now := time.Now().UTC()
	group, err := st.UpsertProviderGroup(domain.ProviderGroup{ID: "codex-advisory", Name: "Codex 建议组", CLI: domain.CLICodex, Enabled: true, PrimaryProviderID: "primary", BackupProviderIDs: []string{"backup"}, ActiveProviderID: "primary", ScenarioID: "basic-ready", Mode: "advisory", RecoveryThreshold: 2, RecoveryProbeIntervalSeconds: 300, Advice: &domain.FailoverAdvice{Status: "open", SuggestedProviderID: "backup", ValidationJobID: "validation-job", ValidationRequestID: "validation-request", CreatedAt: now, UpdatedAt: now}})
	if err != nil {
		t.Fatal(err)
	}
	for _, schedule := range []domain.Schedule{
		{ID: "bound", Name: "绑定计划", Enabled: false, CLI: domain.CLICodex, ProviderID: "primary", ProviderGroupID: group.ID, Mode: domain.ModeProbe, Timezone: "UTC", WeekdaysMask: 127, StartMinute: 0, EndMinute: 1440, UntilSuccess: true, TimeoutSeconds: 5, RetryIntervalSeconds: 1, KeepaliveIntervalSeconds: 60, FailureThreshold: 3},
		{ID: "fixed", Name: "固定计划", Enabled: false, CLI: domain.CLICodex, ProviderID: "primary", Mode: domain.ModeProbe, Timezone: "UTC", WeekdaysMask: 127, StartMinute: 0, EndMinute: 1440, UntilSuccess: true, TimeoutSeconds: 5, RetryIntervalSeconds: 1, KeepaliveIntervalSeconds: 60, FailureThreshold: 3},
	} {
		if _, err = st.UpsertSchedule(schedule); err != nil {
			t.Fatal(err)
		}
	}
	incident, err := st.UpsertIncident(domain.Incident{SubjectType: "group", SubjectID: group.ID, GroupID: group.ID, Title: "组故障", Status: "open", Severity: "warning", FailureCount: 1, Timeline: []domain.IncidentEntry{{ID: "failure", At: now, Type: "failure", Message: "failed"}}, StartedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.ApplyProviderGroupAdvice(group.ID, "backup", group.Advice.UpdatedAt)
	if err != nil || !result.Switched || result.PreviousProviderID != "primary" || result.ActiveProviderID != "backup" || result.AffectedScheduleCount != 1 || result.HostConfigChanged {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	loaded, err := st.GetProviderGroup(group.ID)
	if err != nil || loaded.ActiveProviderID != "backup" || loaded.Advice == nil || loaded.Advice.Status != "applied" || loaded.Advice.AppliedAt == nil {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	repeated, err := m.ApplyProviderGroupAdvice(group.ID, "backup", group.Advice.UpdatedAt)
	if err != nil || repeated.Switched || repeated.AffectedScheduleCount != 1 {
		t.Fatalf("repeated=%+v err=%v", repeated, err)
	}
	if err = m.FlushEvents(); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListEvents(store.EventFilter{Type: "provider_group_manual_switch", Limit: 10})
	if err != nil || len(events) != 1 || stringValue(events[0].Data["validationRequestId"]) != "validation-request" || events[0].Data["hostConfigChanged"] != false {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	updatedIncident, err := st.GetIncident(incident.ID)
	if err != nil || len(updatedIncident.Timeline) != 2 || updatedIncident.Timeline[1].Type != "manual_switch" || updatedIncident.Timeline[1].RequestID != "validation-request" {
		t.Fatalf("incident=%+v err=%v", updatedIncident, err)
	}
	for index := 1; index <= 2; index++ {
		if err = st.SaveEvent(store.Event{At: now.Add(time.Duration(index) * time.Second), Type: "request_end", ProviderID: "primary", Data: map[string]any{"requestId": fmt.Sprintf("recovery-%d", index), "status": "success", "triggerSource": "failover_recovery"}}); err != nil {
			t.Fatal(err)
		}
	}
	m.recoverFailoverAdvice(st, loaded, "recovery-2")
	recovered, err := st.GetProviderGroup(group.ID)
	if err != nil || recovered.ActiveProviderID != "primary" || recovered.Advice == nil || recovered.Advice.Status != "recovered" {
		t.Fatalf("manual switch recovery=%+v err=%v", recovered, err)
	}
}

func TestApplyProviderGroupAdviceRejectsStaleAndMaintenance(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	m := New(failoverResolver{}, execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		return runner.Result{Output: "READY", ExitCode: 0}, nil
	}), st)
	defer m.Shutdown()
	now := time.Now().UTC()
	maintenance := now.Add(time.Hour)
	group, err := st.UpsertProviderGroup(domain.ProviderGroup{ID: "codex-guarded", Name: "Codex 受保护组", CLI: domain.CLICodex, Enabled: true, PrimaryProviderID: "primary", BackupProviderIDs: []string{"backup"}, ActiveProviderID: "primary", ScenarioID: "basic-ready", Mode: "advisory", MaintenanceUntil: &maintenance, Advice: &domain.FailoverAdvice{Status: "open", SuggestedProviderID: "backup", ValidationJobID: "job", ValidationRequestID: "request", CreatedAt: now, UpdatedAt: now}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyProviderGroupAdvice(group.ID, "backup", group.Advice.UpdatedAt); !errors.Is(err, ErrProviderGroupMaintenance) {
		t.Fatalf("maintenance error=%v", err)
	}
	group.MaintenanceUntil = nil
	group, err = st.UpsertProviderGroup(group)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ApplyProviderGroupAdvice(group.ID, "other", group.Advice.UpdatedAt); !errors.Is(err, ErrProviderGroupAdviceStale) {
		t.Fatalf("stale member error=%v", err)
	}
	if _, err = m.ApplyProviderGroupAdvice(group.ID, "backup", group.Advice.UpdatedAt.Add(-time.Second)); !errors.Is(err, ErrProviderGroupAdviceStale) {
		t.Fatalf("stale timestamp error=%v", err)
	}
}
