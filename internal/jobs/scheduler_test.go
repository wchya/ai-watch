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

type resolverFunc func(domain.CLI, string) (domain.ResolvedConfig, error)

func (f resolverFunc) Resolve(cli domain.CLI, providerID string) (domain.ResolvedConfig, error) {
	return f(cli, providerID)
}

func TestScheduleUsesProviderGroupActiveMember(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	group, err := st.UpsertProviderGroup(domain.ProviderGroup{ID: "codex-auto", Name: "Codex 自动组", CLI: domain.CLICodex, Enabled: true, PrimaryProviderID: "primary", BackupProviderIDs: []string{"backup"}, ActiveProviderID: "backup", Mode: "automatic", ScenarioID: "basic-ready", FailureThreshold: 3, RecoveryThreshold: 2, CooldownSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	m := New(resolverFunc(func(domain.CLI, string) (domain.ResolvedConfig, error) { return domain.ResolvedConfig{}, nil }), execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		return runner.Result{}, nil
	}), st)
	defer m.Shutdown()
	resolved, err := m.scheduleWithActiveProvider(domain.Schedule{CLI: domain.CLICodex, ProviderID: "primary", ProviderGroupID: group.ID})
	if err != nil || resolved.ProviderID != "backup" || resolved.ProviderName != group.Name {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
}

func TestScheduleOccurrenceUsesGoWeekdayMaskAndSupportsOvernight(t *testing.T) {
	base := domain.Schedule{
		ID: "rule", Timezone: "Asia/Shanghai", WeekdaysMask: 62,
		StartMinute: 9 * 60, EndMinute: 18 * 60,
		UpdatedAt: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC),
	}
	monday := time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC) // Monday 10:00 Shanghai.
	if _, active := scheduleOccurrence(base, monday); !active {
		t.Fatal("workday schedule should be active on Monday")
	}
	sunday := time.Date(2026, 7, 12, 2, 0, 0, 0, time.UTC)
	if _, active := scheduleOccurrence(base, sunday); active {
		t.Fatal("workday schedule should be inactive on Sunday")
	}

	overnight := base
	overnight.WeekdaysMask = 1 << uint(time.Monday)
	overnight.StartMinute = 22 * 60
	overnight.EndMinute = 6 * 60
	tuesdayOneAM := time.Date(2026, 7, 13, 17, 0, 0, 0, time.UTC) // Tuesday 01:00 Shanghai.
	occurrence, active := scheduleOccurrence(overnight, tuesdayOneAM)
	if !active || occurrence == "" || occurrence[len(occurrence)-10:] != "2026-07-13" {
		t.Fatalf("overnight occurrence should belong to Monday: occurrence=%q active=%v", occurrence, active)
	}
}

func TestScheduledProbeRunsOncePerOccurrence(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	var calls atomic.Int32
	executor := execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		calls.Add(1)
		return runner.Result{ExitCode: 0, Output: "READY"}, nil
	})
	resolver := resolverFunc(func(cli domain.CLI, providerID string) (domain.ResolvedConfig, error) {
		return domain.ResolvedConfig{ProviderID: providerID, ProviderName: "Provider", BaseURL: "https://example.test/v1", LockIdentity: providerID}, nil
	})
	m := New(resolver, executor, st)
	defer m.Shutdown()
	select {
	case <-m.scheduleStarted:
	case <-time.After(time.Second):
		t.Fatal("startup schedule reconciliation did not finish")
	}
	schedule, err := st.UpsertSchedule(domain.Schedule{
		ID: "probe-once", Name: "Probe once", Enabled: true, CLI: domain.CLICodex,
		ProviderID: "provider-1", Mode: domain.ModeProbe, Timezone: "UTC",
		WeekdaysMask: 127, StartMinute: 0, EndMinute: 1440, UntilSuccess: true,
		TimeoutSeconds: 5, RetryIntervalSeconds: 1,
		KeepaliveIntervalSeconds: 60, FailureThreshold: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.reconcileSchedules(time.Now().UTC())
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := st.GetSchedule(schedule.ID)
		if getErr == nil && current.LastStatus == string(domain.JobSuccess) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	current, err := st.GetSchedule(schedule.ID)
	if err != nil || current.LastStatus != string(domain.JobSuccess) || current.LastJobID == "" {
		t.Fatalf("scheduled job did not finish: schedule=%+v err=%v", current, err)
	}
	m.reconcileSchedules(time.Now().UTC())
	time.Sleep(25 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("occurrence ran %d times, want exactly once", got)
	}
	// A graceful process shutdown records stopped. On restart the reconciler
	// must restore the desired active occurrence instead of treating it as done.
	if err := st.MarkScheduleRun(schedule.ID, current.LastOccurrenceKey, string(domain.JobStopped), current.LastJobID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	m.reconcileSchedules(time.Now().UTC())
	deadline = time.Now().Add(time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("stopped occurrence was not reconciled after restart: calls=%d", got)
	}
}

func TestScheduleJobOptionsMapsProbeUntilSuccessToRunOnce(t *testing.T) {
	once := scheduleJobOptions(domain.Schedule{Mode: domain.ModeProbe, UntilSuccess: false, ProviderGroupID: "group-a"})
	continuous := scheduleJobOptions(domain.Schedule{Mode: domain.ModeProbe, UntilSuccess: true})
	keepalive := scheduleJobOptions(domain.Schedule{Mode: domain.ModeKeepalive, UntilSuccess: false})
	if !once.RunOnce || continuous.RunOnce || keepalive.RunOnce {
		t.Fatalf("unexpected schedule execution mapping: once=%+v continuous=%+v keepalive=%+v", once, continuous, keepalive)
	}
	if once.ProviderGroupID != "group-a" {
		t.Fatalf("provider group attribution was not copied to job options: %+v", once)
	}

	now := time.Now().UTC()
	schedule := domain.Schedule{LastOccurrenceKey: "occurrence", LastStatus: string(domain.JobFailed), LastOccurrenceAt: &now, Mode: domain.ModeProbe}
	if !shouldSkipScheduleOccurrence(schedule, "occurrence", now.Add(time.Hour)) {
		t.Fatal("completed one-shot schedule occurrence was started again")
	}
}

func TestScheduledProbeTakesPriorityOverKeepaliveForSameTarget(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	started := make(chan domain.Mode, 2)
	executor := execFunc(func(ctx context.Context, _ string, opts domain.JobOptions, _ domain.ResolvedConfig, _ func(string)) (runner.Result, error) {
		started <- opts.Mode
		<-ctx.Done()
		return runner.Result{Stopped: true}, nil
	})
	resolver := resolverFunc(func(cli domain.CLI, providerID string) (domain.ResolvedConfig, error) {
		return domain.ResolvedConfig{ProviderID: providerID, BaseURL: "https://same.test/v1", LockIdentity: "same"}, nil
	})
	m := New(resolver, executor, st)
	defer m.Shutdown()
	time.Sleep(20 * time.Millisecond) // let the startup reconciliation finish
	for _, schedule := range []domain.Schedule{
		{ID: "keepalive", Name: "A keepalive", Mode: domain.ModeKeepalive},
		{ID: "probe", Name: "Z probe", Mode: domain.ModeProbe},
	} {
		schedule.Enabled = true
		schedule.CLI = domain.CLICodex
		schedule.ProviderID = schedule.ID
		schedule.Timezone = "UTC"
		schedule.WeekdaysMask = 127
		schedule.StartMinute = 0
		schedule.EndMinute = 1440
		schedule.UntilSuccess = true
		schedule.TimeoutSeconds = 10
		schedule.RetryIntervalSeconds = 1
		schedule.KeepaliveIntervalSeconds = 60
		schedule.FailureThreshold = 3
		if _, err := st.UpsertSchedule(schedule); err != nil {
			t.Fatal(err)
		}
	}
	m.reconcileSchedules(time.Now().UTC())
	select {
	case mode := <-started:
		if mode != domain.ModeProbe {
			t.Fatalf("started %s before probe", mode)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduled probe did not start")
	}
	select {
	case mode := <-started:
		t.Fatalf("same target started a second scheduled job: %s", mode)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestGlobalActiveJobLimit(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	executor := execFunc(func(ctx context.Context, _ string, _ domain.JobOptions, _ domain.ResolvedConfig, _ func(string)) (runner.Result, error) {
		<-ctx.Done()
		return runner.Result{Stopped: true}, nil
	})
	resolver := resolverFunc(func(cli domain.CLI, providerID string) (domain.ResolvedConfig, error) {
		return domain.ResolvedConfig{ProviderID: providerID, BaseURL: "https://" + providerID + ".test/v1", LockIdentity: providerID}, nil
	})
	m := New(resolver, executor, st)
	defer m.Shutdown()
	var jobs []domain.Job
	for index := 0; index < maxActiveJobs; index++ {
		job, err := m.Start(domain.JobOptions{Mode: domain.ModeKeepalive, CLI: domain.CLICodex, ProviderID: fmt.Sprintf("provider-%d", index)})
		if err != nil {
			t.Fatalf("start job %d: %v", index, err)
		}
		jobs = append(jobs, job)
	}
	if _, err := m.Start(domain.JobOptions{Mode: domain.ModeKeepalive, CLI: domain.CLICodex, ProviderID: "provider-over-limit"}); !errors.Is(err, ErrActiveLimit) {
		t.Fatalf("got %v, want active limit", err)
	}
	for _, job := range jobs {
		if err := m.Stop(job.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScheduleResolveFailureIsVisibleAndRetryable(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	schedule, err := st.UpsertSchedule(domain.Schedule{
		ID: "missing-provider", Name: "Missing", Enabled: true, CLI: domain.CLICodex,
		ProviderID: "missing", Mode: domain.ModeProbe, Timezone: "UTC", WeekdaysMask: 127,
		StartMinute: 0, EndMinute: 1440, UntilSuccess: true, TimeoutSeconds: 10,
		RetryIntervalSeconds: 1, KeepaliveIntervalSeconds: 60, FailureThreshold: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	m := New(resolverFunc(func(domain.CLI, string) (domain.ResolvedConfig, error) {
		return domain.ResolvedConfig{}, errors.New("provider source unavailable")
	}), execFunc(nil), st)
	defer m.Shutdown()
	m.reconcileSchedules(time.Now().UTC())
	current, err := st.GetSchedule(schedule.ID)
	if err != nil || current.LastStatus != "resolve_failed" || current.LastOccurrenceAt == nil {
		t.Fatalf("resolve failure was not recorded: schedule=%+v err=%v", current, err)
	}
	if err = m.FlushEvents(); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListEvents(store.EventFilter{Type: "schedule_resolve_failed", Limit: 10})
	if err != nil || len(events) == 0 || events[0].ProviderID != "missing" {
		t.Fatalf("resolve failure event missing: events=%+v err=%v", events, err)
	}
}
