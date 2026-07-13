package jobs

import (
	"ai-watch/internal/domain"
	"ai-watch/internal/runner"
	"ai-watch/internal/store"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

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

func TestSanitizeTargetPreservesSafePath(t *testing.T) {
	got := sanitizeTarget("https://user:pass@example.com:8443/gateway/v1/models?api_key=secret#debug")
	if want := "https://example.com:8443/gateway/v1/models"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
