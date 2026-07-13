package jobs

import (
	"errors"
	"sort"
	"time"

	"ai-watch/internal/domain"
)

const scheduleReconcileInterval = 15 * time.Second

type scheduleCandidate struct {
	schedule   domain.Schedule
	occurrence string
	lock       string
}

func (m *Manager) scheduleLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(scheduleReconcileInterval)
	defer ticker.Stop()
	m.reconcileSchedules(time.Now().UTC())
	for {
		select {
		case <-ticker.C:
			m.reconcileSchedules(time.Now().UTC())
		case <-m.scheduleWake:
			m.reconcileSchedules(time.Now().UTC())
		case <-m.ctx.Done():
			return
		}
	}
}

func (m *Manager) wakeSchedules() {
	select {
	case m.scheduleWake <- struct{}{}:
	default:
	}
}

func (m *Manager) ListSchedules() ([]domain.Schedule, error) {
	values, err := m.store.ListSchedules()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for index := range values {
		values[index].NextRunAt = nextScheduleRun(values[index], now)
	}
	return values, nil
}

func (m *Manager) UpsertSchedule(value domain.Schedule) (domain.Schedule, error) {
	m.mu.RLock()
	settings := m.settings
	closing := m.closing
	m.mu.RUnlock()
	if closing {
		return domain.Schedule{}, ErrShuttingDown
	}
	if value.TimeoutSeconds == 0 {
		value.TimeoutSeconds = settings.TimeoutSeconds
	}
	if value.RetryIntervalSeconds == 0 {
		value.RetryIntervalSeconds = settings.RetryIntervalSeconds
	}
	if value.KeepaliveIntervalSeconds == 0 {
		value.KeepaliveIntervalSeconds = settings.KeepaliveIntervalSeconds
	}
	if value.FailureThreshold == 0 {
		value.FailureThreshold = 3
	}
	if value.Timezone == "" {
		value.Timezone = "Asia/Shanghai"
	}
	saved, err := m.store.UpsertSchedule(value)
	if err != nil {
		return domain.Schedule{}, err
	}
	// A saved rule takes effect immediately. The old runtime is canceled and
	// the single reconciler may start the new occurrence with fresh options.
	m.stopScheduleJob(saved.ID)
	m.wakeSchedules()
	values, err := m.ListSchedules()
	if err == nil {
		for _, candidate := range values {
			if candidate.ID == saved.ID {
				return candidate, nil
			}
		}
	}
	return saved, nil
}

func (m *Manager) DeleteSchedule(id string) (bool, error) {
	m.stopScheduleJob(id)
	deleted, err := m.store.DeleteSchedule(id)
	if err == nil {
		m.wakeSchedules()
	}
	return deleted, err
}

func (m *Manager) stopScheduleJob(scheduleID string) bool {
	m.mu.RLock()
	jobID := m.scheduleJobs[scheduleID]
	rt := m.jobs[jobID]
	var cancel func()
	if rt != nil && !rt.closed && rt.cancel != nil {
		cancel = rt.cancel
	}
	m.mu.RUnlock()
	if cancel != nil {
		cancel()
		return true
	}
	return false
}

func (m *Manager) StopTarget(cli domain.CLI, providerID, scheduleID string) error {
	if scheduleID != "" && m.stopScheduleJob(scheduleID) {
		return nil
	}
	m.mu.RLock()
	cancels := make([]func(), 0, 1)
	for _, rt := range m.jobs {
		if rt.job.CLI == cli && rt.job.ProviderID == providerID && !rt.closed && rt.cancel != nil {
			cancels = append(cancels, rt.cancel)
		}
	}
	m.mu.RUnlock()
	if len(cancels) == 0 {
		return ErrNotFound
	}
	for _, cancel := range cancels {
		cancel()
	}
	return nil
}

func (m *Manager) reconcileSchedules(now time.Time) {
	schedules, err := m.store.ListSchedules()
	if err != nil {
		m.persistenceErr.Store(err.Error())
		return
	}
	activeIDs := make(map[string]bool, len(schedules))
	candidates := make([]scheduleCandidate, 0, len(schedules))
	for _, schedule := range schedules {
		occurrence, active := scheduleOccurrence(schedule, now)
		if !schedule.Enabled || !active {
			m.stopScheduleJob(schedule.ID)
			continue
		}
		activeIDs[schedule.ID] = true
		m.mu.RLock()
		_, alreadyRunning := m.scheduleJobs[schedule.ID]
		m.mu.RUnlock()
		if alreadyRunning || shouldSkipScheduleOccurrence(schedule, occurrence, now) {
			continue
		}
		cfg, resolveErr := m.resolver.Resolve(schedule.CLI, schedule.ProviderID)
		if resolveErr != nil {
			continue
		}
		identity := cfg.LockIdentity
		if identity == "" {
			identity = cfg.APIKey
		}
		lock := targetKey(schedule.CLI, cfg.BaseURL, identity)
		cfg = domain.ResolvedConfig{}
		candidates = append(candidates, scheduleCandidate{schedule: schedule, occurrence: occurrence, lock: lock})
	}

	// A scheduled recovery probe owns a target ahead of scheduled keepalive.
	probeLocks := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.schedule.Mode == domain.ModeProbe {
			probeLocks[candidate.lock] = true
		}
	}
	m.mu.RLock()
	var preempt []func()
	for _, rt := range m.jobs {
		if rt.scheduleID != "" && rt.opts.Mode == domain.ModeKeepalive && probeLocks[rt.lock] && rt.cancel != nil {
			preempt = append(preempt, rt.cancel)
		}
	}
	m.mu.RUnlock()
	for _, cancel := range preempt {
		cancel()
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].schedule.Mode != candidates[j].schedule.Mode {
			return candidates[i].schedule.Mode == domain.ModeProbe
		}
		return candidates[i].schedule.ID < candidates[j].schedule.ID
	})
	for _, candidate := range candidates {
		m.mu.RLock()
		_, alreadyRunning := m.scheduleJobs[candidate.schedule.ID]
		m.mu.RUnlock()
		if alreadyRunning || !activeIDs[candidate.schedule.ID] {
			continue
		}
		opts := scheduleJobOptions(candidate.schedule)
		_, err = m.start(opts, candidate.schedule.ID, candidate.occurrence)
		if err != nil && !errors.Is(err, ErrLockConflict) && !errors.Is(err, ErrActiveLimit) && !errors.Is(err, ErrShuttingDown) {
			m.persistenceErr.Store(err.Error())
		}
	}
}

func scheduleJobOptions(schedule domain.Schedule) domain.JobOptions {
	return domain.JobOptions{
		Mode: schedule.Mode, RunOnce: schedule.Mode == domain.ModeProbe && !schedule.UntilSuccess,
		CLI: schedule.CLI, ProviderID: schedule.ProviderID,
		TimeoutSeconds:           schedule.TimeoutSeconds,
		RetryIntervalSeconds:     schedule.RetryIntervalSeconds,
		KeepaliveIntervalSeconds: schedule.KeepaliveIntervalSeconds,
		FailureThreshold:         schedule.FailureThreshold, Model: schedule.Model,
		FallbackModel: schedule.FallbackModel,
	}
}

func terminalScheduleStatus(status string) bool {
	switch domain.JobStatus(status) {
	case domain.JobSuccess, domain.JobFatal, domain.JobFailed:
		return true
	default:
		return false
	}
}

func shouldSkipScheduleOccurrence(schedule domain.Schedule, occurrence string, _ time.Time) bool {
	if schedule.LastOccurrenceKey != occurrence || !terminalScheduleStatus(schedule.LastStatus) {
		return false
	}
	return true
}

func scheduleOccurrence(schedule domain.Schedule, now time.Time) (string, bool) {
	location, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return "", false
	}
	local := now.In(location)
	minute := local.Hour()*60 + local.Minute()
	date := local
	active := false
	if schedule.EndMinute > schedule.StartMinute {
		active = weekdayEnabled(schedule.WeekdaysMask, local.Weekday()) && minute >= schedule.StartMinute && minute < schedule.EndMinute
	} else if minute >= schedule.StartMinute {
		active = weekdayEnabled(schedule.WeekdaysMask, local.Weekday())
	} else if minute < schedule.EndMinute {
		date = local.AddDate(0, 0, -1)
		active = weekdayEnabled(schedule.WeekdaysMask, date.Weekday())
	}
	if !active {
		return "", false
	}
	return schedule.ID + ":" + schedule.UpdatedAt.UTC().Format("20060102T150405.000000000Z") + ":" + date.Format("2006-01-02"), true
}

func weekdayEnabled(mask int, weekday time.Weekday) bool {
	return mask&(1<<uint(weekday)) != 0
}

func nextScheduleRun(schedule domain.Schedule, now time.Time) *time.Time {
	if !schedule.Enabled {
		return nil
	}
	if _, active := scheduleOccurrence(schedule, now); active {
		value := now.UTC()
		return &value
	}
	location, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return nil
	}
	local := now.In(location)
	for offset := 0; offset <= 7; offset++ {
		day := local.AddDate(0, 0, offset)
		if !weekdayEnabled(schedule.WeekdaysMask, day.Weekday()) {
			continue
		}
		candidate := time.Date(day.Year(), day.Month(), day.Day(), schedule.StartMinute/60, schedule.StartMinute%60, 0, 0, location)
		if candidate.After(local) {
			value := candidate.UTC()
			return &value
		}
	}
	return nil
}
