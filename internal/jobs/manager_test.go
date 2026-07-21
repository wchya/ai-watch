package jobs

import (
	"ai-watch/internal/domain"
	"ai-watch/internal/runner"
	"ai-watch/internal/store"
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

type cachedJobEventStore struct {
	store.Store
	mu     sync.Mutex
	events map[string][]domain.Event
}

type recoverableEventStore struct {
	store.Store
	mu  sync.Mutex
	err error
}

func (s *recoverableEventStore) SaveEvent(event store.Event, retention ...store.EventRetention) error {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.Store.SaveEvent(event, retention...)
}

func (s *recoverableEventStore) setError(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *cachedJobEventStore) SaveJobEvent(jobID string, event domain.Event, _ store.JobEventRetention) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[jobID] = append(s.events[jobID], event)
	return nil
}

func (s *cachedJobEventStore) ListJobEvents(jobID string, after uint64) ([]domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var values []domain.Event
	for _, event := range s.events[jobID] {
		if event.ID > after {
			values = append(values, event)
		}
	}
	return values, nil
}

func TestPersistenceErrorIncludesEventTypeAndRecovers(t *testing.T) {
	base := store.New(t.TempDir())
	t.Cleanup(func() { _ = base.Close() })
	st := &recoverableEventStore{Store: base, err: errors.New("temporary write failure")}
	m := New(fakeResolver{}, nil, st)
	t.Cleanup(m.Shutdown)

	m.recordOperationalEvent(store.Event{Type: "request_end", Message: "safe"})
	if err := m.FlushEvents(); err == nil || err.Error() != `persist event "request_end": temporary write failure` {
		t.Fatalf("unexpected persistence error: %v", err)
	}

	st.setError(nil)
	m.recordOperationalEvent(store.Event{Type: "job_state", Message: "safe"})
	if err := m.FlushEvents(); err != nil {
		t.Fatalf("persistence did not recover: %v", err)
	}
	if got := m.PersistenceError(); got != "" {
		t.Fatalf("persistence error was not cleared: %q", got)
	}
}

func TestOperationalEventRedactsCredentialBeforePersistence(t *testing.T) {
	base := store.New(t.TempDir())
	t.Cleanup(func() { _ = base.Close() })
	m := New(fakeResolver{}, nil, base)
	t.Cleanup(m.Shutdown)

	m.recordOperationalEvent(store.Event{Type: "reliability_digest_failed", Data: map[string]any{
		"error": "request failed: access_token=abcdefghijkl",
	}})
	if err := m.FlushEvents(); err != nil {
		t.Fatalf("persist redacted event: %v", err)
	}
	events, err := base.ListEvents(store.EventFilter{Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	got, _ := events[0].Data["error"].(string)
	if got != "request failed: access_token=[REDACTED]" {
		t.Fatalf("credential was not redacted: %q", got)
	}
}

type fakeResolver struct{ cfg domain.ResolvedConfig }

func (f fakeResolver) Resolve(domain.CLI, string) (domain.ResolvedConfig, error) { return f.cfg, nil }

type execFunc func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error)

func (f execFunc) Run(c context.Context, id string, o domain.JobOptions, r domain.ResolvedConfig, w func(string)) (runner.Result, error) {
	return f(c, id, o, r, w)
}

type fakeNotifier struct{ called chan domain.AttemptStatus }

func (f fakeNotifier) Configured() bool { return true }
func (f fakeNotifier) Notify(_ context.Context, _ domain.Job, a domain.AttemptStatus) error {
	f.called <- a
	return nil
}
func waitDone(t *testing.T, m *Manager, id string) domain.Job {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		j, _ := m.Get(id)
		if j.EndedAt != nil {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return domain.Job{}
}

func waitJob(t *testing.T, m *Manager, id string, match func(domain.Job) bool) domain.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := m.Get(id)
		if err == nil && match(job) {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, err := m.Get(id)
	t.Fatalf("job condition not reached: job=%+v err=%v", job, err)
	return domain.Job{}
}

func receiveAttempt(t *testing.T, attempts <-chan int, want int) {
	t.Helper()
	select {
	case got := <-attempts:
		if got != want {
			t.Fatalf("got attempt %d, want %d", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("attempt %d did not run", want)
	}
}
func TestProbeClearsOutputAndPersistsSummary(t *testing.T) {
	secret := "secret-value"
	e := execFunc(func(_ context.Context, _ string, _ domain.JobOptions, _ domain.ResolvedConfig, out func(string)) (runner.Result, error) {
		out(secret + " READY")
		return runner.Result{Output: "READY", ExitCode: 0}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://x", APIKey: secret}}, e, store.New(t.TempDir()))
	j, err := m.Start(domain.JobOptions{Mode: domain.ModeProbe, CLI: domain.CLICodex})
	if err != nil {
		t.Fatal(err)
	}
	done := waitDone(t, m, j.ID)
	if done.Status != domain.JobSuccess {
		t.Fatalf("got %s", done.Status)
	}
	replay, ch, cleanup, err := m.Subscribe(j.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	for range ch {
	}
	for _, ev := range replay {
		if ev.Type == "output" || strings.Contains(ev.Message, secret) {
			t.Fatal("completed job retained output")
		}
	}
}

func TestProbeCachesRedactedOutputForHistoricalReplay(t *testing.T) {
	secret := "sk-cached-secret-value"
	base := store.New(t.TempDir())
	defer base.Close()
	st := &cachedJobEventStore{Store: base, events: map[string][]domain.Event{}}
	executor := execFunc(func(_ context.Context, _ string, _ domain.JobOptions, _ domain.ResolvedConfig, out func(string)) (runner.Result, error) {
		out("Authorization: Bearer " + secret + " READY")
		return runner.Result{Output: "READY", ExitCode: 0}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://example.test/v1", APIKey: secret}}, executor, st)
	defer m.Shutdown()
	job, err := m.Start(domain.JobOptions{Mode: domain.ModeProbe, RunOnce: true, CLI: domain.CLICodex})
	if err != nil {
		t.Fatal(err)
	}
	_ = waitDone(t, m, job.ID)
	replay, ch, cleanup, err := m.Subscribe(job.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	for range ch {
	}
	var foundOutput bool
	for _, event := range replay {
		if strings.Contains(event.Message, secret) {
			t.Fatalf("cached secret leaked: %+v", event)
		}
		if event.Type == "request_log" && strings.Contains(event.Message, "READY") {
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Fatalf("cached output missing: %+v", replay)
	}
}

func TestKeepaliveDoesNotCacheDetailedOutput(t *testing.T) {
	base := store.New(t.TempDir())
	defer base.Close()
	st := &cachedJobEventStore{Store: base, events: map[string][]domain.Event{}}
	executor := execFunc(func(_ context.Context, _ string, _ domain.JobOptions, _ domain.ResolvedConfig, out func(string)) (runner.Result, error) {
		out("READY")
		return runner.Result{Output: "READY", ExitCode: 0}, nil
	})
	m := New(fakeResolver{}, executor, st)
	defer m.Shutdown()
	job, err := m.Start(domain.JobOptions{Mode: domain.ModeKeepalive, RunOnce: true, CLI: domain.CLICodex})
	if err != nil {
		t.Fatal(err)
	}
	_ = waitDone(t, m, job.ID)
	m.flushEventWrites()
	if values, _ := st.ListJobEvents(job.ID, 0); len(values) != 0 {
		t.Fatalf("keepalive output was cached: %+v", values)
	}
}

func TestRequestEventsHaveUniquePairedIDsAndStructuredResult(t *testing.T) {
	base := store.New(t.TempDir())
	defer base.Close()
	st := &cachedJobEventStore{Store: base, events: map[string][]domain.Event{}}
	executor := execFunc(func(_ context.Context, _ string, _ domain.JobOptions, _ domain.ResolvedConfig, out func(string)) (runner.Result, error) {
		out("READY")
		return runner.Result{Output: "READY", ExitCode: 0}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://example.test/v1", ProxyMode: domain.ProxyDirect}}, executor, st)
	job, err := m.Start(domain.JobOptions{Mode: domain.ModeProbe, RunOnce: true, CLI: domain.CLICodex})
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, m, job.ID)
	if err := m.FlushEvents(); err != nil {
		t.Fatal(err)
	}
	replay, err := st.ListJobEvents(job.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var startID, endID string
	for _, event := range replay {
		if event.Type == "request_start" {
			startID, _ = event.Data["requestId"].(string)
			if event.Data["target"] != "https://example.test/v1" || event.Data["proxyMode"] != string(domain.ProxyDirect) {
				t.Fatalf("request start metadata missing: %+v", event.Data)
			}
		}
		if event.Type == "request_end" {
			endID, _ = event.Data["requestId"].(string)
			if event.Data["status"] != "success" || event.Data["classification"] != string(domain.AttemptSuccess) {
				t.Fatalf("request end metadata missing: %+v", event.Data)
			}
		}
	}
	if startID == "" || startID != endID {
		t.Fatalf("request events are not paired: start=%q end=%q events=%+v", startID, endID, replay)
	}
}

func TestJobResolvesSyntheticScenarioAtStart(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	_, err := st.UpsertTestScenario(domain.TestScenario{ID: "scenario-exact", Name: "Exact READY", CLI: domain.CLICodex, Enabled: true, Prompt: "synthetic prompt", AssertionType: "exact", Expected: "READY", TimeoutSeconds: 22})
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan domain.JobOptions, 1)
	executor := execFunc(func(_ context.Context, _ string, opts domain.JobOptions, _ domain.ResolvedConfig, _ func(string)) (runner.Result, error) {
		received <- opts
		return runner.Result{Output: "READY\n", ExitCode: 0}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://example.test/v1"}}, executor, st)
	defer m.Shutdown()
	job, err := m.Start(domain.JobOptions{Mode: domain.ModeProbe, RunOnce: true, CLI: domain.CLICodex, ScenarioID: "scenario-exact"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitDone(t, m, job.ID)
	if done.Status != domain.JobSuccess || done.ScenarioID != "scenario-exact" || done.ScenarioName != "Exact READY" {
		t.Fatalf("unexpected scenario job: %+v", done)
	}
	select {
	case opts := <-received:
		if opts.Prompt != "synthetic prompt" || opts.Expected != "READY" || opts.AssertionType != "exact" || opts.TimeoutSeconds != 22 {
			t.Fatalf("scenario was not resolved into runtime options: %+v", opts)
		}
	case <-time.After(time.Second):
		t.Fatal("scenario job was not executed")
	}
}

func TestRunOnceFinishesAfterExactlyOneAttempt(t *testing.T) {
	tests := []struct {
		name        string
		mode        domain.Mode
		result      runner.Result
		wantStatus  domain.JobStatus
		wantAttempt domain.AttemptStatus
	}{
		{name: "probe retryable failure", mode: domain.ModeProbe, result: runner.Result{ExitCode: 1, Output: "unexpected response"}, wantStatus: domain.JobFailed, wantAttempt: domain.AttemptUnmatched},
		{name: "probe fatal failure", mode: domain.ModeProbe, result: runner.Result{ExitCode: 1, Output: "not logged in"}, wantStatus: domain.JobFatal, wantAttempt: domain.AttemptFatal},
		{name: "immediate keepalive success", mode: domain.ModeKeepalive, result: runner.Result{ExitCode: 0, Output: "READY"}, wantStatus: domain.JobSuccess, wantAttempt: domain.AttemptSuccess},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st := store.New(t.TempDir())
			defer st.Close()
			calls := 0
			executor := execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
				calls++
				return test.result, nil
			})
			m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://example.test/v1", LockIdentity: test.name}}, executor, st)
			defer m.Shutdown()
			job, err := m.Start(domain.JobOptions{Mode: test.mode, RunOnce: true, CLI: domain.CLICodex})
			if err != nil {
				t.Fatal(err)
			}
			done := waitDone(t, m, job.ID)
			if calls != 1 || !done.RunOnce || done.Attempts != 1 || done.Status != test.wantStatus || done.LatestAttempt != test.wantAttempt {
				t.Fatalf("unexpected one-shot result: calls=%d job=%+v", calls, done)
			}
			if done.NextAttemptAt != nil || done.Phase == domain.JobPhaseRecoveryProbe {
				t.Fatalf("one-shot job entered a continuing state: %+v", done)
			}
		})
	}
}

func TestContinuousProbeStillRetriesUntilSuccess(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	calls := 0
	executor := execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		calls++
		if calls == 1 {
			return runner.Result{ExitCode: 1, Output: "unexpected response"}, nil
		}
		return runner.Result{ExitCode: 0, Output: "READY"}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://example.test/v1", LockIdentity: "continuous"}}, executor, st)
	defer m.Shutdown()
	job, err := m.Start(domain.JobOptions{Mode: domain.ModeProbe, CLI: domain.CLICodex, RetryIntervalSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	done := waitJob(t, m, job.ID, func(job domain.Job) bool { return job.EndedAt != nil })
	if calls != 2 || done.RunOnce || done.Status != domain.JobSuccess || done.Attempts != 2 {
		t.Fatalf("continuous probe behavior changed: calls=%d job=%+v", calls, done)
	}
}

func TestLifecycleEventsPersistWithoutRawOutput(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	secret := "sk-event-secret-value"
	executor := execFunc(func(_ context.Context, _ string, _ domain.JobOptions, _ domain.ResolvedConfig, out func(string)) (runner.Result, error) {
		out(secret + " READY")
		return runner.Result{Output: "READY", ExitCode: 0}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://example.com/v1", APIKey: secret, ProviderID: "provider-1"}}, executor, st)
	job, err := m.Start(domain.JobOptions{Mode: domain.ModeProbe, CLI: domain.CLICodex})
	if err != nil {
		t.Fatal(err)
	}
	_ = waitDone(t, m, job.ID)
	m.Shutdown()
	events, err := st.ListEvents(store.EventFilter{JobID: job.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected persisted lifecycle events")
	}
	for _, event := range events {
		if event.Type == "output" || strings.Contains(event.Message, secret) {
			t.Fatalf("persisted unsafe event: %+v", event)
		}
	}
}

func TestEventQueueAppliesBackpressureWithoutDropping(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	m := New(fakeResolver{}, execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		return runner.Result{}, nil
	}), st)
	rt := &runtime{job: domain.Job{ID: "event-stress", ProviderID: "p1"}, subscribers: map[chan domain.Event]struct{}{}}
	for index := 0; index < 600; index++ {
		m.mu.Lock()
		m.publishLocked(rt, "job_state", "state", nil)
		m.mu.Unlock()
	}
	if err := m.FlushEvents(); err != nil {
		t.Fatal(err)
	}
	count, err := st.CountEvents(store.EventFilter{JobID: rt.job.ID})
	if err != nil {
		t.Fatal(err)
	}
	if count != 600 {
		t.Fatalf("persisted %d events, want 600", count)
	}
	m.Shutdown()
}
func TestTargetLockAndStop(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	e := execFunc(func(ctx context.Context, _ string, _ domain.JobOptions, _ domain.ResolvedConfig, _ func(string)) (runner.Result, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return runner.Result{Stopped: true}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "x", APIKey: "k"}}, e, store.New(t.TempDir()))
	j, e1 := m.Start(domain.JobOptions{Mode: domain.ModeKeepalive, CLI: domain.CLICodex})
	if e1 != nil {
		t.Fatal(e1)
	}
	<-started
	if _, e2 := m.Start(domain.JobOptions{Mode: domain.ModeProbe, CLI: domain.CLICodex}); !errors.Is(e2, ErrLockConflict) {
		t.Fatalf("expected lock conflict, got %v", e2)
	}
	if e := m.Stop(j.ID); e != nil {
		t.Fatal(e)
	}
	if got := waitDone(t, m, j.ID).Status; got != domain.JobStopped {
		t.Fatalf("got %s", got)
	}
}

func TestProbeTerminalNotifies(t *testing.T) {
	n := fakeNotifier{called: make(chan domain.AttemptStatus, 1)}
	e := execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		return runner.Result{ExitCode: 0, Output: "READY"}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "x"}}, e, store.New(t.TempDir()), n)
	j, err := m.Start(domain.JobOptions{Mode: domain.ModeProbe, CLI: domain.CLICodex})
	if err != nil {
		t.Fatal(err)
	}
	_ = waitDone(t, m, j.ID)
	select {
	case got := <-n.called:
		if got != domain.AttemptSuccess {
			t.Fatalf("got %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("notification not sent")
	}
}

func TestKeepaliveFailureKindsEnterRecoveryProbe(t *testing.T) {
	cases := []struct {
		name   string
		result runner.Result
		want   domain.AttemptStatus
	}{
		{name: "fatal", result: runner.Result{ExitCode: 1, Output: "not logged in"}, want: domain.AttemptFatal},
		{name: "timeout", result: runner.Result{ExitCode: 124, TimedOut: true}, want: domain.AttemptTimeout},
		{name: "overloaded", result: runner.Result{ExitCode: 1, Output: "429 too many requests"}, want: domain.AttemptOverloaded},
		{name: "unmatched", result: runner.Result{ExitCode: 1, Output: "unexpected response"}, want: domain.AttemptUnmatched},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			st := store.New(t.TempDir())
			defer st.Close()
			var once sync.Once
			executor := execFunc(func(ctx context.Context, _ string, _ domain.JobOptions, _ domain.ResolvedConfig, _ func(string)) (runner.Result, error) {
				first := false
				once.Do(func() { first = true })
				if first {
					return test.result, nil
				}
				<-ctx.Done()
				return runner.Result{Stopped: true}, nil
			})
			m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://example.com", APIKey: "key"}}, executor, st)
			job, err := m.Start(domain.JobOptions{
				Mode: domain.ModeKeepalive, CLI: domain.CLICodex,
				FailureThreshold: 1, RetryIntervalSeconds: 1, KeepaliveIntervalSeconds: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			active := waitJob(t, m, job.ID, func(job domain.Job) bool {
				return job.Phase == domain.JobPhaseRecoveryProbe && job.LatestAttempt == test.want
			})
			if active.Status != domain.JobRunning || active.Attempts != 1 {
				t.Fatalf("unexpected recovery job: %+v", active)
			}
			if err := m.Stop(job.ID); err != nil {
				t.Fatal(err)
			}
			if done := waitDone(t, m, job.ID); done.Status != domain.JobStopped {
				t.Fatalf("got %s", done.Status)
			}
			m.Shutdown()
		})
	}
}

func TestKeepaliveRecoveryReturnsToNormalAndNotifiesOnce(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	notifier := fakeNotifier{called: make(chan domain.AttemptStatus, 4)}
	attempts := make(chan int, 8)
	count := 0
	executor := execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		count++
		attempts <- count
		switch count {
		case 1:
			return runner.Result{ExitCode: 1, Output: "unexpected response"}, nil
		case 2:
			return runner.Result{ExitCode: 1, Output: "429 overloaded"}, nil
		default:
			return runner.Result{ExitCode: 0, Output: "READY"}, nil
		}
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://example.com", APIKey: "key"}}, executor, st, notifier)
	job, err := m.Start(domain.JobOptions{
		Mode: domain.ModeKeepalive, CLI: domain.CLICodex,
		FailureThreshold: 2, RetryIntervalSeconds: 1, KeepaliveIntervalSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	receiveAttempt(t, attempts, 1)
	waitJob(t, m, job.ID, func(job domain.Job) bool {
		return job.Attempts == 1 && job.Phase == domain.JobPhaseKeepalive && job.LatestAttempt == domain.AttemptUnmatched
	})
	receiveAttempt(t, attempts, 2)
	waitJob(t, m, job.ID, func(job domain.Job) bool {
		return job.Attempts == 2 && job.Phase == domain.JobPhaseRecoveryProbe && job.LatestAttempt == domain.AttemptOverloaded
	})
	receiveAttempt(t, attempts, 3)
	waitJob(t, m, job.ID, func(job domain.Job) bool {
		return job.Attempts == 3 && job.Phase == domain.JobPhaseKeepalive && job.LatestAttempt == domain.AttemptSuccess
	})
	select {
	case got := <-notifier.called:
		if got != domain.AttemptSuccess {
			t.Fatalf("got notification %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("recovery notification not sent")
	}
	replay, _, cleanup, err := m.Subscribe(job.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	var phaseEvent, recoveryEvent bool
	for _, event := range replay {
		phaseEvent = phaseEvent || event.Type == "phase"
		recoveryEvent = recoveryEvent || event.Type == "recovery"
	}
	if !phaseEvent || !recoveryEvent {
		t.Fatalf("missing recovery lifecycle events: phase=%v recovery=%v", phaseEvent, recoveryEvent)
	}

	receiveAttempt(t, attempts, 4)
	waitJob(t, m, job.ID, func(job domain.Job) bool { return job.Attempts == 4 })
	select {
	case got := <-notifier.called:
		t.Fatalf("ordinary keepalive success sent another notification: %s", got)
	case <-time.After(100 * time.Millisecond):
	}
	if err := m.Stop(job.ID); err != nil {
		t.Fatal(err)
	}
	if done := waitDone(t, m, job.ID); done.Status != domain.JobStopped || done.Phase != domain.JobPhaseKeepalive {
		t.Fatalf("unexpected stopped job: %+v", done)
	}
	m.Shutdown()
}

func TestKeepaliveSuccessResetsConsecutiveFailures(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	attempts := make(chan int, 4)
	count := 0
	executor := execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		count++
		attempts <- count
		if count == 2 {
			return runner.Result{ExitCode: 0, Output: "READY"}, nil
		}
		return runner.Result{ExitCode: 1, Output: "unexpected response"}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://example.com", APIKey: "key"}}, executor, st)
	job, err := m.Start(domain.JobOptions{
		Mode: domain.ModeKeepalive, CLI: domain.CLICodex,
		FailureThreshold: 2, RetryIntervalSeconds: 1, KeepaliveIntervalSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for want := 1; want <= 3; want++ {
		receiveAttempt(t, attempts, want)
	}
	waitJob(t, m, job.ID, func(job domain.Job) bool {
		return job.Attempts == 3 && job.Phase == domain.JobPhaseKeepalive && job.LatestAttempt == domain.AttemptUnmatched
	})
	m.mu.RLock()
	failures := m.jobs[job.ID].consecutiveFailures
	m.mu.RUnlock()
	if failures != 1 {
		t.Fatalf("got %d consecutive failures, want 1", failures)
	}
	if err := m.Stop(job.ID); err != nil {
		t.Fatal(err)
	}
	_ = waitDone(t, m, job.ID)
	m.Shutdown()
}

func TestOrdinaryKeepaliveSuccessDoesNotNotify(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	notifier := fakeNotifier{called: make(chan domain.AttemptStatus, 1)}
	executed := make(chan struct{})
	executor := execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		select {
		case <-executed:
		default:
			close(executed)
		}
		return runner.Result{ExitCode: 0, Output: "READY"}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://example.com", APIKey: "key"}}, executor, st, notifier)
	job, err := m.Start(domain.JobOptions{Mode: domain.ModeKeepalive, CLI: domain.CLICodex, KeepaliveIntervalSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	<-executed
	waitJob(t, m, job.ID, func(job domain.Job) bool { return job.LatestAttempt == domain.AttemptSuccess })
	select {
	case got := <-notifier.called:
		t.Fatalf("ordinary keepalive success notified: %s", got)
	case <-time.After(100 * time.Millisecond):
	}
	if err := m.Stop(job.ID); err != nil {
		t.Fatal(err)
	}
	_ = waitDone(t, m, job.ID)
	m.Shutdown()
}

func TestShutdownStopsRecoveryProbe(t *testing.T) {
	st := store.New(t.TempDir())
	defer st.Close()
	executor := execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		return runner.Result{ExitCode: 1, Output: "not logged in"}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://example.com", APIKey: "key"}}, executor, st)
	job, err := m.Start(domain.JobOptions{
		Mode: domain.ModeKeepalive, CLI: domain.CLICodex,
		FailureThreshold: 1, RetryIntervalSeconds: 30, KeepaliveIntervalSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, job.ID, func(job domain.Job) bool { return job.Phase == domain.JobPhaseRecoveryProbe })
	m.Shutdown()
	done, err := m.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != domain.JobStopped || done.Phase != domain.JobPhaseRecoveryProbe || done.LatestAttempt != domain.AttemptStopped {
		t.Fatalf("unexpected shutdown result: %+v", done)
	}
}

func TestFinishedJobMovesToHistoryAndClearsRuntime(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	secret := "sk-sensitive-runtime-value"
	e := execFunc(func(_ context.Context, _ string, _ domain.JobOptions, _ domain.ResolvedConfig, out func(string)) (runner.Result, error) {
		close(started)
		<-release
		out(secret + " READY")
		return runner.Result{ExitCode: 0, Output: "READY"}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{
		BaseURL:  "https://user:pass@example.com/gateway/v1?token=secret#fragment",
		APIKey:   secret,
		AuthJSON: []byte(secret),
		ClaudeEnv: map[string]string{
			"ANTHROPIC_AUTH_TOKEN": secret,
		},
	}}, e, store.New(t.TempDir()))
	job, err := m.Start(domain.JobOptions{Mode: domain.ModeProbe, CLI: domain.CLICodex, Prompt: secret})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	m.mu.RLock()
	rt := m.jobs[job.ID]
	m.mu.RUnlock()
	if rt == nil {
		t.Fatal("active runtime not found")
	}
	lock := rt.lock
	close(release)
	done := waitDone(t, m, job.ID)
	if done.Target != "https://example.com/gateway/v1" {
		t.Fatalf("unsafe or incomplete target: %q", done.Target)
	}

	m.mu.RLock()
	_, active := m.jobs[job.ID]
	_, locked := m.locks[lock]
	m.mu.RUnlock()
	if active {
		t.Fatal("completed job remained in active map")
	}
	if locked {
		t.Fatal("completed job retained target lock")
	}
	if rt.opts != (domain.JobOptions{}) {
		t.Fatalf("runtime options were not cleared: %+v", rt.opts)
	}
	if rt.cfg.APIKey != "" || rt.cfg.AuthJSON != nil || rt.cfg.ClaudeEnv != nil || rt.cfg.CodexConfig != "" || rt.cfg.ConfigDir != "" {
		t.Fatalf("resolved config was not cleared: %+v", rt.cfg)
	}
	if rt.events != nil || rt.subscribers != nil || rt.ctx != nil || rt.cancel != nil || rt.lock != "" {
		t.Fatal("runtime references were not released")
	}

	got, err := m.Get(job.ID)
	if err != nil || got.Status != domain.JobSuccess {
		t.Fatalf("history fallback failed: job=%+v err=%v", got, err)
	}
	replay, ch, cleanup, err := m.Subscribe(job.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(replay) != 0 {
		t.Fatalf("completed job replay retained events: %+v", replay)
	}
	if _, open := <-ch; open {
		t.Fatal("historical subscription should be closed")
	}
	if err := m.Stop(job.ID); err != nil {
		t.Fatalf("stopping a historical job should be idempotent: %v", err)
	}
}

func TestShutdownWaitsForJobsAndRejectsNewWork(t *testing.T) {
	started := make(chan struct{})
	exited := make(chan struct{})
	e := execFunc(func(ctx context.Context, _ string, _ domain.JobOptions, _ domain.ResolvedConfig, _ func(string)) (runner.Result, error) {
		close(started)
		<-ctx.Done()
		time.Sleep(25 * time.Millisecond)
		close(exited)
		return runner.Result{Stopped: true}, nil
	})
	m := New(fakeResolver{domain.ResolvedConfig{BaseURL: "https://example.com", APIKey: "key"}}, e, store.New(t.TempDir()))
	job, err := m.Start(domain.JobOptions{Mode: domain.ModeKeepalive, CLI: domain.CLICodex})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	m.Shutdown()
	select {
	case <-exited:
	default:
		t.Fatal("shutdown returned before executor exited")
	}
	got, err := m.Get(job.ID)
	if err != nil || got.Status != domain.JobStopped {
		t.Fatalf("shutdown did not persist stopped history: job=%+v err=%v", got, err)
	}
	if _, err := m.Start(domain.JobOptions{Mode: domain.ModeProbe, CLI: domain.CLICodex}); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("expected ErrShuttingDown, got %v", err)
	}
	m.Shutdown()
}

func TestRetryIntervalHasSafeMinimum(t *testing.T) {
	st := store.New(t.TempDir())
	unsafe := domain.DefaultSettings()
	unsafe.RetryIntervalSeconds = 0
	if err := st.SaveSettings(unsafe); err != nil {
		t.Fatal(err)
	}
	m := New(fakeResolver{}, execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		return runner.Result{}, nil
	}), st)
	if got := m.Settings().RetryIntervalSeconds; got != domain.DefaultSettings().RetryIntervalSeconds {
		t.Fatalf("unsafe persisted retry interval was not normalized: %d", got)
	}
	settings := domain.DefaultSettings()
	settings.RetryIntervalSeconds = 0
	if err := m.SetSettings(settings); err == nil {
		t.Fatal("zero retry interval should be rejected")
	}
}

func TestReliabilitySuccessRateAcceptsHundredthPercent(t *testing.T) {
	st := store.New(t.TempDir())
	m := New(fakeResolver{}, execFunc(func(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error) {
		return runner.Result{}, nil
	}), st)
	defer m.Shutdown()

	settings := domain.DefaultSettings()
	settings.ReliabilityAlertSuccessRate = 0.01
	if err := m.SetSettings(settings); err != nil {
		t.Fatalf("0.01 should be accepted: %v", err)
	}
	settings.ReliabilityAlertSuccessRate = 0.009
	if err := m.SetSettings(settings); err == nil {
		t.Fatal("value below 0.01 should be rejected")
	}
	for _, invalid := range []float64{1.234, math.NaN(), math.Inf(1)} {
		settings.ReliabilityAlertSuccessRate = invalid
		if err := m.SetSettings(settings); err == nil {
			t.Fatalf("invalid success rate %v should be rejected", invalid)
		}
	}
}

func TestSanitizeTargetPreservesSafePath(t *testing.T) {
	got := sanitizeTarget("https://user:pass@example.com:8443/gateway/v1/models?api_key=secret#debug")
	if want := "https://example.com:8443/gateway/v1/models"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCodexResponseExtractsAssistantText(t *testing.T) {
	output := "OpenAI Codex v0.144.4\n--------\nworkdir: /app\nmodel: gpt-5.6-sol\n--------\nuser\n[REDACTED]\n\ncodex\nREADY\ntokens used\n8,187\n"
	if got := codexResponse(output); got != "READY" {
		t.Fatalf("got %q", got)
	}
}
