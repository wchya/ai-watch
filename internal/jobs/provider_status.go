package jobs

import (
	"time"

	"ai-watch/internal/domain"
)

func providerStateKey(cli domain.CLI, providerID string) string {
	return string(cli) + "\x00" + providerID
}

// ProviderStates builds a bounded operational snapshot from active jobs,
// retained summaries, and the current schedule definitions. It does not add a
// second history store or persist per-provider counters.
func (m *Manager) ProviderStates() map[string]domain.ProviderState {
	states := make(map[string]domain.ProviderState)
	m.mu.RLock()
	for _, rt := range m.jobs {
		key := providerStateKey(rt.job.CLI, rt.job.ProviderID)
		state := states[key]
		state.Status = "running"
		if rt.job.Phase == domain.JobPhaseRecoveryProbe {
			state.Status = "recovering"
		}
		state.Phase = rt.job.Phase
		state.LatestAttempt = rt.job.LatestAttempt
		state.ActiveJobID = rt.job.ID
		state.Attempts += rt.job.Attempts
		state.ConsecutiveFailures = rt.consecutiveFailures
		states[key] = state
	}
	for _, job := range m.history {
		key := providerStateKey(job.CLI, job.ProviderID)
		state := states[key]
		state.Attempts += job.Attempts
		if state.Status == "" {
			state.Status = string(job.Status)
			state.Phase = job.Phase
			state.LatestAttempt = job.LatestAttempt
		}
		at := job.StartedAt
		if job.EndedAt != nil {
			at = *job.EndedAt
		}
		if job.LatestAttempt == domain.AttemptSuccess {
			if state.LastSuccessAt == nil || at.After(*state.LastSuccessAt) {
				value := at
				state.LastSuccessAt = &value
			}
		} else if job.LatestAttempt != "" && job.LatestAttempt != domain.AttemptStopped {
			if state.LastFailureAt == nil || at.After(*state.LastFailureAt) {
				value := at
				state.LastFailureAt = &value
			}
		}
		states[key] = state
	}
	m.mu.RUnlock()

	schedules, err := m.store.ListSchedules()
	if err == nil {
		now := time.Now().UTC()
		for _, schedule := range schedules {
			key := providerStateKey(schedule.CLI, schedule.ProviderID)
			state := states[key]
			if schedule.Enabled {
				next := nextScheduleRun(schedule, now)
				if !state.ScheduleEnabled || earlier(next, state.NextScheduledAt) {
					state.ScheduleEnabled = true
					state.ScheduleName = schedule.Name
					state.ScheduleMode = schedule.Mode
					state.NextScheduledAt = next
				}
			}
			states[key] = state
		}
	}
	for key, state := range states {
		if state.Status == "" {
			state.Status = "idle"
		}
		states[key] = state
	}
	return states
}

func earlier(candidate, current *time.Time) bool {
	if candidate == nil {
		return false
	}
	return current == nil || candidate.Before(*current)
}

func ProviderStateFor(states map[string]domain.ProviderState, cli domain.CLI, providerID string) (domain.ProviderState, bool) {
	value, ok := states[providerStateKey(cli, providerID)]
	return value, ok
}
