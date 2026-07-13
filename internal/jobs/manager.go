package jobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"ai-watch/internal/classify"
	"ai-watch/internal/domain"
	"ai-watch/internal/runner"
	"ai-watch/internal/security"
	"ai-watch/internal/store"
)

var ErrLockConflict = errors.New("equivalent target already has a running job")
var ErrNotFound = errors.New("job not found")
var ErrShuttingDown = errors.New("job manager is shutting down")
var ErrActiveLimit = errors.New("active job limit reached")

const (
	minRetryIntervalSeconds = 1
	maxActiveJobs           = 8
)

type Resolver interface {
	Resolve(domain.CLI, string) (domain.ResolvedConfig, error)
}
type Executor interface {
	Run(context.Context, string, domain.JobOptions, domain.ResolvedConfig, func(string)) (runner.Result, error)
}
type Notifier interface {
	Notify(context.Context, domain.Job, domain.AttemptStatus) error
	Configured() bool
}

type Manager struct {
	mu             sync.RWMutex
	resolver       Resolver
	executor       Executor
	store          *store.JSON
	jobs           map[string]*runtime
	locks          map[string]string
	history        []domain.Summary
	settings       domain.Settings
	notifier       Notifier
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	shutdown       sync.Once
	closing        bool
	eventQueue     chan eventWrite
	eventWG        sync.WaitGroup
	persistenceErr atomic.Value
	scheduleJobs   map[string]string
	scheduleWake   chan struct{}
}

type runtime struct {
	job                 domain.Job
	opts                domain.JobOptions
	cfg                 domain.ResolvedConfig
	lock                string
	ctx                 context.Context
	cancel              context.CancelFunc
	events              []domain.Event
	nextEvent           uint64
	subscribers         map[chan domain.Event]struct{}
	consecutiveFailures int
	closed              bool
	scheduleID          string
	occurrenceKey       string
}

type eventWrite struct {
	event     store.Event
	retention store.EventRetention
	barrier   chan struct{}
}

func New(res Resolver, exec Executor, st *store.JSON, notifier ...Notifier) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	settings, _ := st.LoadSettings()
	settings = normalizedSettings(settings)
	history, _ := st.LoadSummaries()
	var n Notifier
	if len(notifier) > 0 {
		n = notifier[0]
	}
	settings.DingTalkConfigured = n != nil && n.Configured()
	m := &Manager{resolver: res, executor: exec, store: st, jobs: map[string]*runtime{}, locks: map[string]string{}, history: history, settings: settings, notifier: n, ctx: ctx, cancel: cancel, eventQueue: make(chan eventWrite, 256), scheduleJobs: map[string]string{}, scheduleWake: make(chan struct{}, 1)}
	m.persistenceErr.Store("")
	m.eventWG.Add(1)
	go m.persistEvents()
	m.wg.Add(1)
	go m.scheduleLoop()
	return m
}

func (m *Manager) Start(opts domain.JobOptions) (domain.Job, error) {
	return m.start(opts, "", "")
}

func (m *Manager) start(opts domain.JobOptions, scheduleID, occurrenceKey string) (domain.Job, error) {
	if opts.Mode != domain.ModeProbe && opts.Mode != domain.ModeKeepalive {
		return domain.Job{}, errors.New("mode must be probe or keepalive")
	}
	if opts.CLI != domain.CLICodex && opts.CLI != domain.CLIClaude {
		return domain.Job{}, errors.New("cli must be codex or claude")
	}
	m.mu.RLock()
	defaults := m.settings
	m.mu.RUnlock()
	if opts.TimeoutSeconds == 0 {
		opts.TimeoutSeconds = defaults.TimeoutSeconds
	}
	if opts.RetryIntervalSeconds == 0 {
		opts.RetryIntervalSeconds = defaults.RetryIntervalSeconds
	}
	if opts.KeepaliveIntervalSeconds == 0 {
		opts.KeepaliveIntervalSeconds = defaults.KeepaliveIntervalSeconds
	}
	opts.Defaults()
	if opts.TimeoutSeconds < 1 || opts.TimeoutSeconds > 3600 {
		return domain.Job{}, errors.New("timeoutSeconds must be 1..3600")
	}
	if opts.RetryIntervalSeconds < minRetryIntervalSeconds || opts.KeepaliveIntervalSeconds < 1 {
		return domain.Job{}, errors.New("invalid intervals")
	}
	if opts.FailureThreshold < 1 {
		return domain.Job{}, errors.New("failureThreshold must be positive")
	}
	if opts.CodexRequestRetries < 0 || opts.CodexStreamRetries < 0 || opts.ClaudeMaxRetries < 0 {
		return domain.Job{}, errors.New("retry counts must be non-negative")
	}
	cfg, err := m.resolver.Resolve(opts.CLI, opts.ProviderID)
	if err != nil {
		return domain.Job{}, err
	}
	identity := cfg.LockIdentity
	if identity == "" {
		identity = cfg.APIKey
	}
	lock := targetKey(opts.CLI, cfg.BaseURL, identity)
	id := newID()
	ctx, cancel := context.WithCancel(m.ctx)
	now := time.Now().UTC()
	phase := domain.JobPhaseProbe
	if opts.Mode == domain.ModeKeepalive {
		phase = domain.JobPhaseKeepalive
	}
	job := domain.Job{ID: id, Mode: opts.Mode, CLI: opts.CLI, ProviderID: cfg.ProviderID, ProviderName: cfg.ProviderName, Provider: cfg.Provider, Target: sanitizeTarget(cfg.BaseURL), Model: first(opts.Model, cfg.Model), MaskedKey: security.Mask(cfg.APIKey), Status: domain.JobQueued, Phase: phase, StartedAt: now}
	rt := &runtime{job: job, opts: opts, cfg: cfg, lock: lock, ctx: ctx, cancel: cancel, subscribers: map[chan domain.Event]struct{}{}, scheduleID: scheduleID, occurrenceKey: occurrenceKey}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		cancel()
		return domain.Job{}, ErrShuttingDown
	}
	if len(m.jobs) >= maxActiveJobs {
		m.mu.Unlock()
		cancel()
		return domain.Job{}, ErrActiveLimit
	}
	if other, ok := m.locks[lock]; ok {
		m.mu.Unlock()
		cancel()
		return domain.Job{}, fmt.Errorf("%w: %s", ErrLockConflict, other)
	}
	m.locks[lock] = id
	m.jobs[id] = rt
	if scheduleID != "" {
		m.scheduleJobs[scheduleID] = id
	}
	m.publishLocked(rt, "job_state", "任务已排队", map[string]any{"status": domain.JobQueued})
	m.wg.Add(1)
	m.mu.Unlock()
	if scheduleID != "" {
		if err := m.store.MarkScheduleRun(scheduleID, occurrenceKey, string(domain.JobQueued), id, now); err != nil {
			m.persistenceErr.Store(err.Error())
		}
	}
	go func() {
		defer m.wg.Done()
		m.run(rt)
	}()
	return job, nil
}

func (m *Manager) run(rt *runtime) {
	m.update(rt, func(j *domain.Job) { j.Status = domain.JobRunning }, "job_state", "任务开始运行", map[string]any{"status": domain.JobRunning})
	for {
		if rt.ctx.Err() != nil {
			m.finish(rt, domain.JobStopped, domain.AttemptStopped, "任务已停止")
			return
		}
		m.mu.Lock()
		rt.job.Attempts++
		attempt := rt.job.Attempts
		m.publishLocked(rt, "attempt_start", fmt.Sprintf("第 %d 次调用", attempt), map[string]any{"attempt": attempt})
		m.mu.Unlock()
		attemptCtx, cancel := context.WithTimeout(rt.ctx, time.Duration(rt.opts.TimeoutSeconds)*time.Second)
		res, err := m.executor.Run(attemptCtx, rt.job.ID, rt.opts, rt.cfg, func(chunk string) { m.publish(rt, "output", chunk, nil) })
		cancel()
		if err != nil {
			m.publish(rt, "error", "CLI 启动失败", map[string]any{"error": err.Error()})
			m.clearOutput(rt)
			m.finish(rt, domain.JobFailed, domain.AttemptFatal, "任务执行失败")
			return
		}
		state := classify.Result(rt.opts.CLI, res.ExitCode, res.Output, rt.opts.Expected, res.TimedOut, res.Stopped)
		m.mu.Lock()
		rt.job.LatestAttempt = state
		m.clearOutputLocked(rt)
		m.publishLocked(rt, "classification", string(state), map[string]any{"status": state, "attempt": attempt})
		m.mu.Unlock()
		if state == domain.AttemptStopped {
			m.finish(rt, domain.JobStopped, state, "任务已停止")
			return
		}
		if rt.opts.Mode == domain.ModeProbe {
			if state == domain.AttemptSuccess {
				m.finish(rt, domain.JobSuccess, state, "测活成功")
				return
			}
			if state == domain.AttemptFatal {
				m.finish(rt, domain.JobFatal, state, "检测到不可重试错误")
				return
			}
			if !m.wait(rt, time.Duration(rt.opts.RetryIntervalSeconds)*time.Second) {
				m.finish(rt, domain.JobStopped, domain.AttemptStopped, "任务已停止")
				return
			}
		}
		if rt.opts.Mode == domain.ModeKeepalive {
			if !m.continueKeepalive(rt, state) {
				m.finish(rt, domain.JobStopped, domain.AttemptStopped, "任务已停止")
				return
			}
		}
	}
}

func (m *Manager) wait(rt *runtime, d time.Duration) bool {
	if d <= 0 {
		return rt.ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-rt.ctx.Done():
		return false
	}
}
func (m *Manager) continueKeepalive(rt *runtime, state domain.AttemptStatus) bool {
	if state == domain.AttemptSuccess {
		var recovered bool
		var notification domain.Job
		m.mu.Lock()
		recovered = rt.job.Phase == domain.JobPhaseRecoveryProbe
		rt.consecutiveFailures = 0
		if recovered {
			rt.job.Phase = domain.JobPhaseKeepalive
			m.publishLocked(rt, "recovery", "供应商已恢复可用", map[string]any{
				"phase": domain.JobPhaseKeepalive,
			})
			notification = view(rt.job)
		}
		m.mu.Unlock()
		if recovered {
			m.notify(notification, domain.AttemptSuccess)
		}
		return m.waitCountdown(rt, time.Duration(rt.opts.KeepaliveIntervalSeconds)*time.Second, "等待下一次保活")
	}

	m.mu.Lock()
	rt.consecutiveFailures++
	failures := rt.consecutiveFailures
	if rt.job.Phase != domain.JobPhaseRecoveryProbe && failures >= rt.opts.FailureThreshold {
		rt.job.Phase = domain.JobPhaseRecoveryProbe
		m.publishLocked(rt, "phase", "连续失败达到阈值，进入恢复探测", map[string]any{
			"phase":               domain.JobPhaseRecoveryProbe,
			"consecutiveFailures": failures,
			"failureThreshold":    rt.opts.FailureThreshold,
		})
	}
	recovering := rt.job.Phase == domain.JobPhaseRecoveryProbe
	m.mu.Unlock()
	if recovering {
		return m.waitCountdown(rt, time.Duration(rt.opts.RetryIntervalSeconds)*time.Second, "等待下一次恢复探测")
	}
	return m.waitCountdown(rt, time.Duration(rt.opts.KeepaliveIntervalSeconds)*time.Second, "等待下一次保活")
}

func (m *Manager) waitCountdown(rt *runtime, d time.Duration, message string) bool {
	next := time.Now().UTC().Add(d)
	m.mu.Lock()
	rt.job.NextAttemptAt = &next
	m.publishLocked(rt, "countdown", message, map[string]any{"nextAttemptAt": next, "phase": rt.job.Phase})
	m.mu.Unlock()
	ok := m.wait(rt, d)
	m.mu.Lock()
	rt.job.NextAttemptAt = nil
	m.mu.Unlock()
	return ok
}

func (m *Manager) finish(rt *runtime, status domain.JobStatus, attempt domain.AttemptStatus, message string) {
	now := time.Now().UTC()
	m.mu.Lock()
	if rt.closed {
		m.mu.Unlock()
		return
	}
	rt.job.Status = status
	rt.job.LatestAttempt = attempt
	rt.job.EndedAt = &now
	rt.job.NextAttemptAt = nil
	rt.job.ElapsedMillis = now.Sub(rt.job.StartedAt).Milliseconds()
	delete(m.locks, rt.lock)
	m.clearOutputLocked(rt)
	m.publishLocked(rt, "cleanup", "运行时日志和临时配置已销毁", nil)
	m.publishLocked(rt, "job_state", message, map[string]any{"status": status})
	summary := rt.job
	mode := rt.opts.Mode
	scheduleID := rt.scheduleID
	occurrenceKey := rt.occurrenceKey
	m.history = append([]domain.Summary{summary}, m.history...)
	limit := m.settings.HistoryLimit
	if limit > 0 && len(m.history) > limit {
		m.history = m.history[:limit]
	}
	rt.closed = true
	if current, ok := m.jobs[rt.job.ID]; ok && current == rt {
		delete(m.jobs, rt.job.ID)
	}
	if current := m.scheduleJobs[scheduleID]; scheduleID != "" && current == rt.job.ID {
		delete(m.scheduleJobs, scheduleID)
	}
	for ch := range rt.subscribers {
		close(ch)
	}
	rt.cancel()
	rt.opts = domain.JobOptions{}
	rt.cfg = domain.ResolvedConfig{}
	rt.lock = ""
	rt.ctx = nil
	rt.cancel = nil
	rt.events = nil
	rt.subscribers = nil
	rt.scheduleID = ""
	rt.occurrenceKey = ""
	m.mu.Unlock()
	if err := m.store.SaveSummary(summary, limit); err != nil {
		m.persistenceErr.Store(err.Error())
	}
	if mode == domain.ModeProbe && (status == domain.JobSuccess || status == domain.JobFatal) {
		m.notify(summary, attempt)
	}
	if scheduleID != "" {
		if err := m.store.MarkScheduleRun(scheduleID, occurrenceKey, string(status), summary.ID, now); err != nil {
			m.persistenceErr.Store(err.Error())
		}
		m.wakeSchedules()
	}
}

func (m *Manager) notify(job domain.Job, attempt domain.AttemptStatus) {
	if m.notifier == nil || !m.notifier.Configured() {
		return
	}
	go func() { _ = m.notifier.Notify(context.Background(), job, attempt) }()
}

func (m *Manager) update(rt *runtime, change func(*domain.Job), typ, msg string, data map[string]any) {
	m.mu.Lock()
	change(&rt.job)
	m.publishLocked(rt, typ, msg, data)
	m.mu.Unlock()
}
func (m *Manager) publish(rt *runtime, typ, msg string, data map[string]any) {
	m.mu.Lock()
	m.publishLocked(rt, typ, msg, data)
	m.mu.Unlock()
}
func (m *Manager) publishLocked(rt *runtime, typ, msg string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["job"] = view(rt.job)
	rt.nextEvent++
	e := domain.Event{ID: rt.nextEvent, Type: typ, At: time.Now().UTC(), Message: msg, Data: data}
	rt.events = append(rt.events, e)
	if len(rt.events) > 256 {
		rt.events = rt.events[len(rt.events)-256:]
	}
	for ch := range rt.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
	if typ != "output" {
		level := "info"
		if typ == "error" || rt.job.Status == domain.JobFatal || rt.job.Status == domain.JobFailed {
			level = "error"
		} else if typ == "recovery" || rt.job.Status == domain.JobSuccess {
			level = "success"
		}
		settings := m.settings
		m.eventQueue <- eventWrite{
			event:     store.Event{At: e.At, Type: typ, Level: level, ProviderID: rt.job.ProviderID, JobID: rt.job.ID, Message: msg},
			retention: store.EventRetention{MaxAge: time.Duration(settings.EventRetentionDays) * 24 * time.Hour, MaxRows: settings.EventRetentionRows, MaxBytes: settings.EventRetentionBytes},
		}
	}
}
func (m *Manager) clearOutput(rt *runtime) { m.mu.Lock(); m.clearOutputLocked(rt); m.mu.Unlock() }
func (m *Manager) clearOutputLocked(rt *runtime) {
	kept := rt.events[:0]
	for _, e := range rt.events {
		if e.Type != "output" {
			kept = append(kept, e)
		}
	}
	rt.events = kept
}

func (m *Manager) Get(id string) (domain.Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rt, ok := m.jobs[id]
	if ok {
		return view(rt.job), nil
	}
	if summary, ok := m.historyJobLocked(id); ok {
		return summary, nil
	}
	return domain.Job{}, ErrNotFound
}
func (m *Manager) List() []domain.Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]domain.Job, 0, len(m.jobs)+len(m.history))
	for _, rt := range m.jobs {
		out = append(out, view(rt.job))
	}
	known := map[string]bool{}
	for _, j := range out {
		known[j.ID] = true
	}
	for _, j := range m.history {
		if !known[j.ID] {
			out = append(out, j)
		}
	}
	return out
}
func view(j domain.Job) domain.Job {
	if j.EndedAt == nil {
		j.ElapsedMillis = time.Since(j.StartedAt).Milliseconds()
	}
	return j
}
func (m *Manager) Stop(id string) error {
	m.mu.RLock()
	rt, ok := m.jobs[id]
	var cancel context.CancelFunc
	if ok && !rt.closed {
		cancel = rt.cancel
	}
	_, historical := m.historyJobLocked(id)
	m.mu.RUnlock()
	if !ok {
		if historical {
			return nil
		}
		return ErrNotFound
	}
	if cancel == nil {
		return nil
	}
	cancel()
	return nil
}
func (m *Manager) Subscribe(id string, after uint64) ([]domain.Event, <-chan domain.Event, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt, ok := m.jobs[id]
	if !ok {
		if _, historical := m.historyJobLocked(id); historical {
			ch := make(chan domain.Event)
			close(ch)
			return nil, ch, func() {}, nil
		}
		return nil, nil, nil, ErrNotFound
	}
	var replay []domain.Event
	for _, e := range rt.events {
		if e.ID > after {
			replay = append(replay, e)
		}
	}
	ch := make(chan domain.Event, 64)
	if rt.closed {
		close(ch)
		return replay, ch, func() {}, nil
	}
	rt.subscribers[ch] = struct{}{}
	return replay, ch, func() {
		m.mu.Lock()
		if _, ok := rt.subscribers[ch]; ok {
			delete(rt.subscribers, ch)
			close(ch)
		}
		m.mu.Unlock()
	}, nil
}
func (m *Manager) Settings() domain.Settings { m.mu.RLock(); defer m.mu.RUnlock(); return m.settings }
func (m *Manager) SetSettings(v domain.Settings) error {
	defaults := domain.DefaultSettings()
	if v.EventRetentionDays == 0 {
		v.EventRetentionDays = defaults.EventRetentionDays
	}
	if v.EventRetentionRows == 0 {
		v.EventRetentionRows = defaults.EventRetentionRows
	}
	if v.EventRetentionBytes == 0 {
		v.EventRetentionBytes = defaults.EventRetentionBytes
	}
	if v.TimeoutSeconds < 1 || v.RetryIntervalSeconds < minRetryIntervalSeconds || v.KeepaliveIntervalSeconds < 1 || v.HistoryLimit < 1 {
		return errors.New("invalid settings")
	}
	if v.EventRetentionDays < 1 || v.EventRetentionDays > 3650 || v.EventRetentionRows < 100 || v.EventRetentionRows > 1_000_000 || v.EventRetentionBytes < 1<<20 || v.EventRetentionBytes > 1<<30 {
		return errors.New("invalid event retention settings")
	}
	v.DingTalkConfigured = m.notifier != nil && m.notifier.Configured()
	if err := m.store.SaveSettings(v); err != nil {
		return err
	}
	m.mu.Lock()
	m.settings = v
	if len(m.history) > v.HistoryLimit {
		m.history = m.history[:v.HistoryLimit]
	}
	m.mu.Unlock()
	return nil
}
func (m *Manager) Shutdown() {
	m.shutdown.Do(func() {
		m.mu.Lock()
		m.closing = true
		var cancels []context.CancelFunc
		for _, rt := range m.jobs {
			if !rt.closed && rt.cancel != nil {
				cancels = append(cancels, rt.cancel)
			}
		}
		m.mu.Unlock()
		m.cancel()
		for _, cancel := range cancels {
			cancel()
		}
		m.wg.Wait()
		close(m.eventQueue)
		m.eventWG.Wait()
	})
}

func (m *Manager) persistEvents() {
	defer m.eventWG.Done()
	for item := range m.eventQueue {
		if item.barrier != nil {
			close(item.barrier)
			continue
		}
		if err := m.store.SaveEvent(item.event, item.retention); err != nil {
			m.persistenceErr.Store(err.Error())
		}
	}
}

func (m *Manager) FlushEvents() error {
	barrier := make(chan struct{})
	m.mu.RLock()
	if m.closing {
		m.mu.RUnlock()
		return ErrShuttingDown
	}
	m.eventQueue <- eventWrite{barrier: barrier}
	m.mu.RUnlock()
	<-barrier
	if message := m.PersistenceError(); message != "" {
		return errors.New(message)
	}
	return nil
}

func (m *Manager) PersistenceError() string {
	value, _ := m.persistenceErr.Load().(string)
	return value
}

func (m *Manager) historyJobLocked(id string) (domain.Job, bool) {
	for _, summary := range m.history {
		if summary.ID == id {
			return summary, true
		}
	}
	return domain.Job{}, false
}

func normalizedSettings(v domain.Settings) domain.Settings {
	defaults := domain.DefaultSettings()
	if v.TimeoutSeconds < 1 {
		v.TimeoutSeconds = defaults.TimeoutSeconds
	}
	if v.RetryIntervalSeconds < minRetryIntervalSeconds {
		v.RetryIntervalSeconds = defaults.RetryIntervalSeconds
	}
	if v.KeepaliveIntervalSeconds < 1 {
		v.KeepaliveIntervalSeconds = defaults.KeepaliveIntervalSeconds
	}
	if v.HistoryLimit < 1 {
		v.HistoryLimit = defaults.HistoryLimit
	}
	if v.EventRetentionDays < 1 {
		v.EventRetentionDays = defaults.EventRetentionDays
	}
	if v.EventRetentionRows < 100 {
		v.EventRetentionRows = defaults.EventRetentionRows
	}
	if v.EventRetentionBytes < 1<<20 {
		v.EventRetentionBytes = defaults.EventRetentionBytes
	}
	return v
}

func targetKey(cli domain.CLI, base, key string) string {
	sum := sha256.Sum256([]byte(string(cli) + "|" + base + "|" + key))
	return hex.EncodeToString(sum[:])
}
func newID() string { b := make([]byte, 8); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func first(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}

func sanitizeTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "configured endpoint"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}
