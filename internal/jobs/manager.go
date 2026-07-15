package jobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
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
	probeLogTTL             = 24 * time.Hour
	probeLogMaxRows         = 5000
	probeLogMaxBytes        = 2 << 20
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
	mu                sync.RWMutex
	resolver          Resolver
	executor          Executor
	store             store.Store
	jobs              map[string]*runtime
	locks             map[string]string
	history           []domain.Summary
	settings          domain.Settings
	notifier          Notifier
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	shutdown          sync.Once
	closing           bool
	eventQueue        chan eventWrite
	eventWG           sync.WaitGroup
	persistenceErr    atomic.Value
	scheduleJobs      map[string]string
	scheduleWake      chan struct{}
	notifications     notificationState
	notificationSlots chan struct{}
	notificationWG    sync.WaitGroup
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
	operationalEvent *store.Event
	retention        store.EventRetention
	jobID            string
	jobEvent         *domain.Event
	barrier          chan struct{}
}

func New(res Resolver, exec Executor, st store.Store, notifier ...Notifier) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	settings, _ := st.LoadSettings()
	settings = normalizedSettings(settings)
	history, _ := st.LoadSummaries()
	var n Notifier
	if len(notifier) > 0 {
		n = notifier[0]
	}
	settings.DingTalkConfigured = n != nil && n.Configured()
	m := &Manager{resolver: res, executor: exec, store: st, jobs: map[string]*runtime{}, locks: map[string]string{}, history: history, settings: settings, notifier: n, ctx: ctx, cancel: cancel, eventQueue: make(chan eventWrite, 1024), scheduleJobs: map[string]string{}, scheduleWake: make(chan struct{}, 1), notificationSlots: make(chan struct{}, 4)}
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
	job := domain.Job{ID: id, Mode: opts.Mode, RunOnce: opts.RunOnce, CLI: opts.CLI, ProviderID: cfg.ProviderID, ProviderName: cfg.ProviderName, Provider: cfg.Provider, Target: sanitizeTarget(cfg.BaseURL), Model: first(opts.Model, cfg.Model), MaskedKey: security.Mask(cfg.APIKey), Status: domain.JobQueued, Phase: phase, StartedAt: now}
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
	if rt.scheduleID != "" {
		if err := m.store.MarkScheduleRun(rt.scheduleID, rt.occurrenceKey, string(domain.JobRunning), rt.job.ID, time.Now().UTC()); err != nil {
			m.persistenceErr.Store(err.Error())
		}
	}
	for {
		if rt.ctx.Err() != nil {
			m.finish(rt, domain.JobStopped, domain.AttemptStopped, "任务已停止")
			return
		}
		m.mu.Lock()
		rt.job.Attempts++
		attempt := rt.job.Attempts
		requestID := fmt.Sprintf("%s-%d-%d", rt.job.ID, attempt, time.Now().UnixNano())
		requestStarted := time.Now().UTC()
		triggerSource := rt.opts.TriggerSource
		if triggerSource == "" {
			triggerSource = "manual"
		}
		if rt.job.Phase == domain.JobPhaseRecoveryProbe {
			triggerSource = "recovery_probe"
		}
		targetHost, targetPort := endpointParts(rt.cfg.BaseURL)
		var dnsIPs []string
		var dnsError string
		if rt.opts.TriggerSource != "" && targetHost != "" {
			dnsCtx, dnsCancel := context.WithTimeout(rt.ctx, 2*time.Second)
			addresses, lookupErr := net.DefaultResolver.LookupHost(dnsCtx, targetHost)
			dnsCancel()
			if lookupErr != nil {
				dnsError = lookupErr.Error()
			} else {
				dnsIPs = addresses
			}
		}
		proxyEndpoint := ""
		if rt.cfg.ProxyMode == domain.ProxyCustom {
			proxyEndpoint = sanitizeTarget(rt.cfg.ProxyURL)
		}
		promptHash := sha256.Sum256([]byte(rt.opts.Prompt))
		requestBody := map[string]any{
			"promptBytes": len([]byte(rt.opts.Prompt)), "promptSHA256": hex.EncodeToString(promptHash[:8]),
			"expectedText": rt.opts.Expected, "model": first(rt.opts.Model, rt.cfg.Model),
			"timeoutSeconds": rt.opts.TimeoutSeconds, "runOnce": rt.opts.RunOnce,
			"codexRequestRetries": rt.opts.CodexRequestRetries, "codexStreamRetries": rt.opts.CodexStreamRetries,
			"claudeMaxRetries": rt.opts.ClaudeMaxRetries, "fallbackModel": rt.opts.FallbackModel,
		}
		m.publishLocked(rt, "attempt_start", fmt.Sprintf("第 %d 次调用", attempt), map[string]any{"attempt": attempt})
		m.publishLocked(rt, "request_start", "请求开始", map[string]any{
			"requestId": requestID, "attempt": attempt, "mode": rt.opts.Mode,
			"cli": rt.opts.CLI, "providerId": rt.job.ProviderID, "target": rt.cfg.BaseURL,
			"proxyMode": rt.cfg.ProxyMode, "startedAt": requestStarted,
			"triggerSource": triggerSource, "clientIP": rt.opts.ClientIP, "phase": rt.job.Phase,
			"targetHost": targetHost, "targetPort": targetPort, "proxyEndpoint": proxyEndpoint,
			"dnsIPs": dnsIPs, "dnsError": dnsError,
			"model": first(rt.opts.Model, rt.cfg.Model), "configSource": rt.cfg.Source, "provider": rt.cfg.Provider,
			"requestBody": requestBody,
		})
		m.mu.Unlock()
		attemptCtx, cancel := context.WithTimeout(rt.ctx, time.Duration(rt.opts.TimeoutSeconds)*time.Second)
		res, err := m.executor.Run(attemptCtx, rt.job.ID, rt.opts, rt.cfg, func(chunk string) {
			m.publish(rt, "request_log", chunk, map[string]any{"requestId": requestID, "stream": "combined"})
		})
		cancel()
		if err != nil {
			m.publish(rt, "request_end", "请求启动失败", map[string]any{"requestId": requestID, "attempt": attempt, "status": "start_failed", "error": err.Error(), "durationMillis": time.Since(requestStarted).Milliseconds(), "startedAt": requestStarted, "endedAt": time.Now().UTC(), "cli": rt.opts.CLI, "providerId": rt.job.ProviderID, "triggerSource": triggerSource, "phase": rt.job.Phase, "model": first(rt.opts.Model, rt.cfg.Model)})
			m.publish(rt, "error", "CLI 启动失败", map[string]any{"error": err.Error(), "requestId": requestID})
			m.clearOutput(rt)
			m.finish(rt, domain.JobFailed, domain.AttemptFatal, "任务执行失败")
			return
		}
		state := classify.Result(rt.opts.CLI, res.ExitCode, res.Output, rt.opts.Expected, res.TimedOut, res.Stopped)
		if res.StartedAt.IsZero() {
			res.StartedAt = requestStarted
		}
		if res.EndedAt.IsZero() {
			res.EndedAt = time.Now().UTC()
		}
		if res.DurationMillis <= 0 {
			res.DurationMillis = res.EndedAt.Sub(res.StartedAt).Milliseconds()
		}
		requestStatus := "failed"
		if state == domain.AttemptSuccess {
			requestStatus = "success"
		} else if state == domain.AttemptTimeout {
			requestStatus = "timeout"
		} else if state == domain.AttemptStopped {
			requestStatus = "stopped"
		}
		var nextAttemptAt *time.Time
		if !rt.opts.RunOnce && state != domain.AttemptStopped && state != domain.AttemptFatal {
			next := time.Now().UTC().Add(time.Duration(rt.opts.RetryIntervalSeconds) * time.Second)
			if rt.opts.Mode == domain.ModeKeepalive && state == domain.AttemptSuccess {
				next = time.Now().UTC().Add(time.Duration(rt.opts.KeepaliveIntervalSeconds) * time.Second)
			}
			nextAttemptAt = &next
		}
		errorType := ""
		if state != domain.AttemptSuccess {
			errorType = string(state)
		}
		responseExcerpt := safeOutputExcerpt(res.Output, rt)
		errorMessage := ""
		if state != domain.AttemptSuccess && state != domain.AttemptStopped {
			errorMessage = responseExcerpt
			if errorMessage == "" {
				if state == domain.AttemptTimeout {
					errorMessage = fmt.Sprintf("CLI 请求超过 %d 秒未完成", rt.opts.TimeoutSeconds)
				} else {
					errorMessage = "CLI 请求未返回可识别的供应商响应"
				}
			}
		}
		m.publish(rt, "request_end", "请求结束", map[string]any{"requestId": requestID, "attempt": attempt, "status": requestStatus, "durationMillis": res.DurationMillis, "startedAt": res.StartedAt, "endedAt": res.EndedAt, "exitCode": res.ExitCode, "classification": state, "cli": rt.opts.CLI, "providerId": rt.job.ProviderID, "triggerSource": triggerSource, "phase": rt.job.Phase, "model": first(rt.opts.Model, rt.cfg.Model), "cliExecutable": res.CLIExecutable, "cliVersion": res.CLIVersion, "nextAttemptAt": nextAttemptAt, "errorType": errorType, "error": errorMessage, "responseExcerpt": responseExcerpt})
		m.mu.Lock()
		rt.job.LatestAttempt = state
		m.clearOutputLocked(rt)
		m.publishLocked(rt, "classification", string(state), map[string]any{"status": state, "attempt": attempt})
		m.mu.Unlock()
		if state == domain.AttemptStopped {
			m.finish(rt, domain.JobStopped, state, "任务已停止")
			return
		}
		if rt.opts.RunOnce {
			if rt.opts.Mode == domain.ModeKeepalive && state == domain.AttemptSuccess {
				m.mu.RLock()
				notification := view(rt.job)
				m.mu.RUnlock()
				m.recordKeepaliveSuccess(notification)
			}
			status, message := oneShotResult(rt.opts.Mode, state)
			m.finish(rt, status, state, message)
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
			m.mu.RLock()
			notification := view(rt.job)
			m.mu.RUnlock()
			m.recordProbeProgress(notification)
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

func oneShotResult(mode domain.Mode, attempt domain.AttemptStatus) (domain.JobStatus, string) {
	label := "测活"
	if mode == domain.ModeKeepalive {
		label = "保活"
	}
	switch attempt {
	case domain.AttemptSuccess:
		return domain.JobSuccess, label + "成功"
	case domain.AttemptFatal:
		return domain.JobFatal, label + "检测到不可重试错误"
	default:
		return domain.JobFailed, label + "单次执行未通过"
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
			m.queueRecovery(notification)
		} else {
			m.mu.RLock()
			notification = view(rt.job)
			m.mu.RUnlock()
			m.recordKeepaliveSuccess(notification)
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
	notification := view(rt.job)
	m.mu.Unlock()
	if recovering {
		m.recordProbeProgress(notification)
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
	if mode == domain.ModeProbe {
		m.clearProbeProgress(summary)
	}
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
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return
	}
	select {
	case m.notificationSlots <- struct{}{}:
		m.notificationWG.Add(1)
	default:
		m.mu.Unlock()
		return
	}
	notifier := m.notifier
	m.mu.Unlock()
	go func() {
		defer func() {
			<-m.notificationSlots
			m.notificationWG.Done()
		}()
		_ = notifier.Notify(context.Background(), job, attempt)
	}()
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
	if rt.scheduleID != "" {
		data["scheduleId"] = rt.scheduleID
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
	var operationalEvent *store.Event
	if typ != "output" && typ != "request_log" {
		level := "info"
		if typ == "error" || rt.job.Status == domain.JobFatal || rt.job.Status == domain.JobFailed {
			level = "error"
		} else if typ == "recovery" || rt.job.Status == domain.JobSuccess {
			level = "success"
		}
		safeEvent := redactJobEvent(e, rt)
		if rt.scheduleID != "" {
			if safeEvent.Data == nil {
				safeEvent.Data = map[string]any{}
			}
			safeEvent.Data["scheduleId"] = rt.scheduleID
		}
		value := store.Event{At: e.At, Type: typ, Level: level, ProviderID: rt.job.ProviderID, JobID: rt.job.ID, ScheduleID: rt.scheduleID, Message: safeEvent.Message, Data: safeEvent.Data}
		operationalEvent = &value
	}
	settings := m.settings
	item := eventWrite{
		operationalEvent: operationalEvent,
		retention:        store.EventRetention{MaxAge: time.Duration(settings.EventRetentionDays) * 24 * time.Hour, MaxRows: settings.EventRetentionRows, MaxBytes: settings.EventRetentionBytes},
	}
	if rt.opts.Mode == domain.ModeProbe {
		cached := redactJobEvent(e, rt)
		item.jobID = rt.job.ID
		item.jobEvent = &cached
	}
	if item.operationalEvent != nil || item.jobEvent != nil {
		m.eventQueue <- item
	}
}

func (m *Manager) recordOperationalEvent(event store.Event) {
	m.mu.RLock()
	if m.closing {
		m.mu.RUnlock()
		return
	}
	settings := m.settings
	m.mu.RUnlock()
	item := eventWrite{
		operationalEvent: &event,
		retention: store.EventRetention{
			MaxAge:  time.Duration(settings.EventRetentionDays) * 24 * time.Hour,
			MaxRows: settings.EventRetentionRows, MaxBytes: settings.EventRetentionBytes,
		},
	}
	select {
	case m.eventQueue <- item:
	case <-m.ctx.Done():
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

func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.jobs)
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
	rt, ok := m.jobs[id]
	if !ok {
		if _, historical := m.historyJobLocked(id); historical {
			m.mu.Unlock()
			m.flushEventWrites()
			var replay []domain.Event
			if cache, ok := m.store.(store.JobEventStore); ok {
				var err error
				replay, err = cache.ListJobEvents(id, after)
				if err != nil {
					return nil, nil, nil, err
				}
			}
			ch := make(chan domain.Event)
			close(ch)
			return replay, ch, func() {}, nil
		}
		m.mu.Unlock()
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
		m.mu.Unlock()
		return replay, ch, func() {}, nil
	}
	rt.subscribers[ch] = struct{}{}
	m.mu.Unlock()
	return replay, ch, func() {
		m.mu.Lock()
		if _, ok := rt.subscribers[ch]; ok {
			delete(rt.subscribers, ch)
			close(ch)
		}
		m.mu.Unlock()
	}, nil
}
func (m *Manager) Settings() domain.Settings {
	m.mu.RLock()
	value := m.settings
	notifier := m.notifier
	m.mu.RUnlock()
	value.DingTalkConfigured = notifier != nil && notifier.Configured()
	return value
}
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
	if v.ProbeProgressSeconds < 0 || v.ProbeProgressSeconds > 604800 || v.RecoveryMergeSeconds < 0 || v.RecoveryMergeSeconds > 86400 {
		return errors.New("invalid notification interval settings")
	}
	if v.KeepaliveSummarySeconds < 0 || v.KeepaliveSummarySeconds > 604800 || v.KeepaliveSummarySuccesses < 0 || v.KeepaliveSummarySuccesses > 1_000_000 {
		return errors.New("invalid keepalive summary settings")
	}
	if v.ReliabilityAlertMinSamples < 1 || v.ReliabilityAlertMinSamples > 10000 || v.ReliabilityAlertSuccessRate < 1 || v.ReliabilityAlertSuccessRate > 100 || v.ReliabilityAlertConsecutiveFailures < 1 || v.ReliabilityAlertConsecutiveFailures > 10000 || v.ReliabilityAlertP95Millis < 0 || v.ReliabilityAlertP95Millis > 86_400_000 || v.ReliabilityAlertCooldownSeconds < 0 || v.ReliabilityAlertCooldownSeconds > 604800 || v.ReliabilityAlertRecoverySuccesses < 1 || v.ReliabilityAlertRecoverySuccesses > 10000 {
		return errors.New("invalid reliability alert settings")
	}
	if v.TimeoutSeconds < 1 || v.RetryIntervalSeconds < minRetryIntervalSeconds || v.KeepaliveIntervalSeconds < 1 || v.HistoryLimit < 1 {
		return errors.New("invalid settings")
	}
	if v.EventRetentionDays < 1 || v.EventRetentionDays > 3650 || v.EventRetentionRows < 100 || v.EventRetentionRows > 1_000_000 || v.EventRetentionBytes < 1<<20 || v.EventRetentionBytes > 1<<30 {
		return errors.New("invalid event retention settings")
	}
	if !domain.ValidUITheme(v.UITheme) {
		return errors.New("invalid UI theme")
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
		if m.notifications.recoveryTimer != nil {
			m.notifications.recoveryTimer.Stop()
			m.notifications.recoveryTimer = nil
		}
		m.notifications = notificationState{}
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
		m.notificationWG.Wait()
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
		if item.operationalEvent != nil {
			if err := m.store.SaveEvent(*item.operationalEvent, item.retention); err != nil {
				m.persistenceErr.Store(err.Error())
			} else if item.operationalEvent.Type == "request_end" {
				m.queueReliabilityEvaluation(*item.operationalEvent)
			}
		}
		if item.jobEvent != nil {
			if cache, ok := m.store.(store.JobEventStore); ok {
				if err := cache.SaveJobEvent(item.jobID, *item.jobEvent, store.JobEventRetention{TTL: probeLogTTL, MaxRows: probeLogMaxRows, MaxBytes: probeLogMaxBytes}); err != nil {
					m.persistenceErr.Store(err.Error())
				}
			}
		}
	}
}

func redactJobEvent(event domain.Event, rt *runtime) domain.Event {
	secrets := []string{rt.opts.Prompt, rt.cfg.APIKey, string(rt.cfg.AuthJSON), rt.cfg.BaseURL, rt.cfg.ProxyURL, rt.cfg.CodexConfig}
	for _, value := range rt.cfg.ClaudeEnv {
		secrets = append(secrets, value)
	}
	event.Message = security.Redact(event.Message, secrets...)
	if event.Data == nil {
		return event
	}
	data, err := json.Marshal(event.Data)
	if err != nil {
		event.Data = nil
		return event
	}
	var cloned map[string]any
	if err := json.Unmarshal(data, &cloned); err != nil {
		event.Data = nil
		return event
	}
	event.Data = redactEventData(cloned, secrets)
	return event
}

func safeOutputExcerpt(output string, rt *runtime) string {
	secrets := []string{rt.opts.Prompt, rt.cfg.APIKey, string(rt.cfg.AuthJSON), rt.cfg.BaseURL, rt.cfg.ProxyURL, rt.cfg.CodexConfig}
	for _, value := range rt.cfg.ClaudeEnv {
		secrets = append(secrets, value)
	}
	value := security.Redact(output, secrets...)
	if rt.opts.CLI == domain.CLICodex {
		value = codexResponse(value)
	}
	value = strings.TrimSpace(value)
	const limit = 2000
	if len(value) > limit {
		return value[:limit] + "\n…[TRUNCATED]"
	}
	return value
}

func codexResponse(output string) string {
	const assistantMarker = "\ncodex\n"
	if index := strings.LastIndex(output, assistantMarker); index >= 0 {
		value := output[index+len(assistantMarker):]
		if end := strings.Index(value, "\ntokens used\n"); end >= 0 {
			value = value[:end]
		}
		return strings.TrimSpace(value)
	}
	parts := strings.Split(output, "\n--------\n")
	if len(parts) >= 3 {
		lines := strings.Split(parts[len(parts)-1], "\n")
		for len(lines) > 0 {
			line := strings.TrimSpace(lines[0])
			if line != "" && line != "user" && line != "[REDACTED]" {
				break
			}
			lines = lines[1:]
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	return strings.TrimSpace(output)
}

func redactEventData(values map[string]any, secrets []string) map[string]any {
	for key, value := range values {
		switch typed := value.(type) {
		case string:
			if key == "target" {
				values[key] = sanitizeTarget(typed)
			} else {
				values[key] = security.Redact(typed, secrets...)
			}
		case map[string]any:
			values[key] = redactEventData(typed, secrets)
		case []any:
			for index, item := range typed {
				if text, ok := item.(string); ok {
					typed[index] = security.Redact(text, secrets...)
				} else if nested, ok := item.(map[string]any); ok {
					typed[index] = redactEventData(nested, secrets)
				}
			}
		}
	}
	return values
}

func (m *Manager) FlushEvents() error {
	m.flushEventWrites()
	if message := m.PersistenceError(); message != "" {
		return errors.New(message)
	}
	return nil
}

func (m *Manager) flushEventWrites() {
	barrier := make(chan struct{})
	m.mu.RLock()
	if m.closing {
		m.mu.RUnlock()
		return
	}
	m.eventQueue <- eventWrite{barrier: barrier}
	m.mu.RUnlock()
	<-barrier
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
	if v.KeepaliveSummarySeconds < 0 {
		v.KeepaliveSummarySeconds = defaults.KeepaliveSummarySeconds
	}
	if v.KeepaliveSummarySuccesses < 0 {
		v.KeepaliveSummarySuccesses = defaults.KeepaliveSummarySuccesses
	}
	if v.ProbeProgressSeconds < 0 {
		v.ProbeProgressSeconds = defaults.ProbeProgressSeconds
	}
	if v.RecoveryMergeSeconds < 0 {
		v.RecoveryMergeSeconds = defaults.RecoveryMergeSeconds
	}
	if v.ReliabilityAlertMinSamples < 1 {
		v.ReliabilityAlertMinSamples = defaults.ReliabilityAlertMinSamples
	}
	if v.ReliabilityAlertSuccessRate < 1 {
		v.ReliabilityAlertSuccessRate = defaults.ReliabilityAlertSuccessRate
	}
	if v.ReliabilityAlertConsecutiveFailures < 1 {
		v.ReliabilityAlertConsecutiveFailures = defaults.ReliabilityAlertConsecutiveFailures
	}
	if v.ReliabilityAlertP95Millis < 0 {
		v.ReliabilityAlertP95Millis = defaults.ReliabilityAlertP95Millis
	}
	if v.ReliabilityAlertCooldownSeconds < 0 {
		v.ReliabilityAlertCooldownSeconds = defaults.ReliabilityAlertCooldownSeconds
	}
	if v.ReliabilityAlertRecoverySuccesses < 1 {
		v.ReliabilityAlertRecoverySuccesses = defaults.ReliabilityAlertRecoverySuccesses
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
	if !domain.ValidUITheme(v.UITheme) {
		v.UITheme = defaults.UITheme
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

func endpointParts(raw string) (string, string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return "", ""
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else if parsed.Scheme == "http" {
			port = "80"
		}
	}
	return parsed.Hostname(), port
}
