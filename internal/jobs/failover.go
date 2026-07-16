package jobs

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

var ErrProviderGroupAdviceUnavailable = errors.New("provider group advice is not available for manual application")
var ErrProviderGroupAdviceStale = errors.New("provider group advice changed; refresh before applying it")
var ErrProviderGroupMaintenance = errors.New("provider group is in an active maintenance window")

func (m *Manager) queueFailoverEvaluation(event store.Event) {
	triggerSource := stringValue(event.Data["triggerSource"])
	if event.ProviderID == "" || triggerSource == "failover_validation" || triggerSource == "failover_recovery" {
		return
	}
	if _, ok := m.store.(store.ProviderGroupStore); !ok {
		return
	}
	if m.closing {
		return
	}
	m.notificationWG.Add(1)
	go func() { defer m.notificationWG.Done(); m.evaluateFailover(event) }()
}

func (m *Manager) reconcileProviderGroupRecovery(now time.Time) {
	groups, ok := m.store.(store.ProviderGroupStore)
	if !ok || m.closing {
		return
	}
	values, err := groups.ListProviderGroups()
	if err != nil {
		return
	}
	for _, group := range values {
		if !group.Enabled || group.ActiveProviderID == "" || group.ActiveProviderID == group.PrimaryProviderID || group.Advice == nil || !((group.Mode == "automatic" && group.Advice.Status == "open") || group.Advice.Status == "applied") {
			continue
		}
		if domain.ProviderGroupMaintenanceActive(group, now) {
			continue
		}
		interval := time.Duration(group.RecoveryProbeIntervalSeconds) * time.Second
		if interval < 30*time.Second {
			interval = 5 * time.Minute
		}
		if group.LastRecoveryProbeAt != nil && now.Sub(*group.LastRecoveryProbeAt) < interval {
			continue
		}
		if !m.acquireFailover(group.ID) {
			continue
		}
		group.LastRecoveryProbeAt, group.LastRecoveryProbeStatus = &now, "running"
		if _, err = groups.UpsertProviderGroup(group); err != nil {
			m.releaseFailover(group.ID)
			continue
		}
		job, startErr := m.Start(domain.JobOptions{Mode: domain.ModeProbe, RunOnce: true, CLI: group.CLI, ProviderID: group.PrimaryProviderID, ScenarioID: group.ScenarioID, TriggerSource: "failover_recovery", ClientIP: "failover_recovery"})
		if startErr != nil {
			latest, getErr := groups.GetProviderGroup(group.ID)
			if getErr == nil {
				latest.LastRecoveryProbeAt = nil
				latest.LastRecoveryProbeStatus = "deferred"
				_, _ = groups.UpsertProviderGroup(latest)
			}
			m.releaseFailover(group.ID)
			continue
		}
		m.recordOperationalEvent(store.Event{At: now, Type: "failover_recovery_probe_started", Level: "info", ProviderID: group.PrimaryProviderID, JobID: job.ID, Message: "已启动主 Provider 恢复探测", Data: map[string]any{"groupId": group.ID, "scenarioId": group.ScenarioID, "activeProviderId": group.ActiveProviderID}})
		m.notificationWG.Add(1)
		go func(groupID, jobID string) {
			defer m.notificationWG.Done()
			defer m.releaseFailover(groupID)
			m.completeProviderGroupRecoveryProbe(groupID, jobID)
		}(group.ID, job.ID)
	}
}

func (m *Manager) completeProviderGroupRecoveryProbe(groupID, jobID string) {
	groups, ok := m.store.(store.ProviderGroupStore)
	if !ok {
		return
	}
	job, completed := m.waitFailoverJob(jobID, 3700*time.Second)
	now := time.Now().UTC()
	group, err := groups.GetProviderGroup(groupID)
	if err != nil || group.ActiveProviderID == group.PrimaryProviderID || group.Advice == nil || !((group.Mode == "automatic" && group.Advice.Status == "open") || group.Advice.Status == "applied") {
		return
	}
	requestID := m.requestIDForJob(jobID)
	if completed && job.Status == domain.JobSuccess {
		group.LastRecoveryProbeStatus = "success"
		if _, err = groups.UpsertProviderGroup(group); err == nil {
			m.recoverFailoverAdvice(groups, group, requestID)
			m.recordOperationalEvent(store.Event{At: now, Type: "failover_recovery_probe_succeeded", Level: "success", ProviderID: group.PrimaryProviderID, JobID: jobID, Message: "主 Provider 恢复探测成功", Data: map[string]any{"groupId": group.ID, "requestId": requestID, "recoveryThreshold": group.RecoveryThreshold}})
		}
		return
	}
	group.LastRecoveryProbeStatus = "failed"
	_, _ = groups.UpsertProviderGroup(group)
	m.recordOperationalEvent(store.Event{At: now, Type: "failover_recovery_probe_failed", Level: "warning", ProviderID: group.PrimaryProviderID, JobID: jobID, Message: "主 Provider 尚未恢复", Data: map[string]any{"groupId": group.ID, "requestId": requestID}})
}

func (m *Manager) ApplyProviderGroupAdvice(groupID, suggestedProviderID string, adviceUpdatedAt time.Time) (domain.ProviderGroupSwitchResult, error) {
	groups, ok := m.store.(store.ProviderGroupStore)
	if !ok {
		return domain.ProviderGroupSwitchResult{}, ErrProviderGroupAdviceUnavailable
	}
	if !m.acquireFailover(groupID) {
		group, err := groups.GetProviderGroup(groupID)
		if err == nil && group.Advice != nil && group.Advice.Status == "applied" && group.ActiveProviderID == suggestedProviderID && group.Advice.SuggestedProviderID == suggestedProviderID {
			return domain.ProviderGroupSwitchResult{GroupID: group.ID, PreviousProviderID: group.ActiveProviderID, ActiveProviderID: group.ActiveProviderID, ValidationRequestID: group.Advice.ValidationRequestID, AffectedScheduleCount: m.providerGroupScheduleCount(group.ID), HostConfigChanged: false}, nil
		}
		return domain.ProviderGroupSwitchResult{}, ErrProviderGroupAdviceStale
	}
	defer m.releaseFailover(groupID)
	group, err := groups.GetProviderGroup(groupID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return domain.ProviderGroupSwitchResult{}, fs.ErrNotExist
		}
		return domain.ProviderGroupSwitchResult{}, err
	}
	result := domain.ProviderGroupSwitchResult{GroupID: group.ID, PreviousProviderID: group.ActiveProviderID, ActiveProviderID: group.ActiveProviderID, HostConfigChanged: false}
	if group.Advice != nil {
		result.ValidationRequestID = group.Advice.ValidationRequestID
	}
	result.AffectedScheduleCount = m.providerGroupScheduleCount(group.ID)
	if group.Advice != nil && group.Advice.Status == "applied" && group.ActiveProviderID == suggestedProviderID && group.Advice.SuggestedProviderID == suggestedProviderID {
		return result, nil
	}
	if !group.Enabled || group.Mode != "advisory" || group.Advice == nil || group.Advice.Status != "open" || group.Advice.ValidationJobID == "" || group.Advice.ValidationRequestID == "" {
		return domain.ProviderGroupSwitchResult{}, ErrProviderGroupAdviceUnavailable
	}
	if domain.ProviderGroupMaintenanceActive(group, time.Now().UTC()) {
		return domain.ProviderGroupSwitchResult{}, ErrProviderGroupMaintenance
	}
	if suggestedProviderID == "" || suggestedProviderID != group.Advice.SuggestedProviderID || !providerGroupHasBackup(group, suggestedProviderID) {
		return domain.ProviderGroupSwitchResult{}, ErrProviderGroupAdviceStale
	}
	if adviceUpdatedAt.IsZero() || !group.Advice.UpdatedAt.Equal(adviceUpdatedAt) {
		return domain.ProviderGroupSwitchResult{}, ErrProviderGroupAdviceStale
	}
	if group.ActiveProviderID == "" {
		group.ActiveProviderID = group.PrimaryProviderID
		result.PreviousProviderID = group.PrimaryProviderID
	}
	now := time.Now().UTC()
	group.ActiveProviderID, group.LastSwitchedAt = suggestedProviderID, &now
	group.LastRecoveryProbeAt, group.LastRecoveryProbeStatus = nil, ""
	group.Advice.Status, group.Advice.UpdatedAt, group.Advice.AppliedAt = "applied", now, &now
	if _, err = groups.UpsertProviderGroup(group); err != nil {
		return domain.ProviderGroupSwitchResult{}, err
	}
	result.ActiveProviderID, result.Switched = suggestedProviderID, true
	result.AffectedScheduleCount = m.restartProviderGroupSchedules(group.ID)
	m.recordOperationalEvent(store.Event{At: now, Type: "provider_group_manual_switch", Level: "warning", ProviderID: suggestedProviderID, Message: "已人工采用验证通过的故障切换建议", Data: map[string]any{"groupId": group.ID, "previousProviderId": result.PreviousProviderID, "activeProviderId": suggestedProviderID, "validationJobId": group.Advice.ValidationJobID, "validationRequestId": group.Advice.ValidationRequestID, "affectedScheduleCount": result.AffectedScheduleCount, "hostConfigChanged": false}})
	m.appendGroupIncidentEvent(group.ID, now, "manual_switch", fmt.Sprintf("人工采用已验证建议：%s → %s，影响 %d 条计划", result.PreviousProviderID, suggestedProviderID, result.AffectedScheduleCount), group.Advice.ValidationRequestID, group.Advice.ValidationJobID)
	return result, nil
}

func providerGroupHasBackup(group domain.ProviderGroup, providerID string) bool {
	for _, candidate := range group.BackupProviderIDs {
		if candidate == providerID {
			return true
		}
	}
	return false
}

func (m *Manager) evaluateFailover(event store.Event) {
	groupsStore := m.store.(store.ProviderGroupStore)
	groups, err := groupsStore.ListProviderGroups()
	if err != nil {
		return
	}
	status := stringValue(event.Data["status"])
	requestID := stringValue(event.Data["requestId"])
	for _, group := range groups {
		if group.Advice != nil && group.Advice.Status == "validating" && group.Advice.ValidationJobID == event.JobID {
			now := time.Now().UTC()
			group.Advice.UpdatedAt = now
			group.Advice.ValidationRequestID = requestID
			if status == "success" {
				group.Advice.Status = "open"
				group.Advice.Reason = fmt.Sprintf("备用线路已通过场景 %s，建议人工评估切换", group.ScenarioID)
			} else {
				group.Advice.Status = "validation_failed"
				group.Advice.Reason = "备用线路未通过验证，未生成切换建议"
			}
			_, _ = groupsStore.UpsertProviderGroup(group)
			if status == "success" {
				m.activateAutomaticProviderGroup(groupsStore, group, group.Advice.SuggestedProviderID, now)
			}
			continue
		}
		if !group.Enabled || group.PrimaryProviderID != event.ProviderID {
			continue
		}
		if domain.ProviderGroupMaintenanceActive(group, time.Now().UTC()) {
			continue
		}
		if status == "success" {
			m.recoverFailoverAdvice(groupsStore, group, requestID)
			continue
		}
		if status == "stopped" || status == "running" {
			continue
		}
		if group.Advice != nil && group.Advice.Status == "open" {
			continue
		}
		if group.Advice != nil && group.CooldownSeconds > 0 && time.Since(group.Advice.UpdatedAt) < time.Duration(group.CooldownSeconds)*time.Second {
			continue
		}
		if m.consecutiveProviderFailures(event.ProviderID, group.FailureThreshold) < group.FailureThreshold {
			continue
		}
		if !m.acquireFailover(group.ID) {
			continue
		}
		m.validateFailoverGroup(groupsStore, group, requestID)
		m.releaseFailover(group.ID)
	}
}

func (m *Manager) consecutiveProviderFailures(providerID string, limit int) int {
	if err := m.FlushEvents(); err != nil {
		return 0
	}
	events, err := m.store.ListEvents(store.EventFilter{ProviderID: providerID, Type: "request_end", Limit: max(20, limit*2)})
	if err != nil {
		return 0
	}
	count := 0
	for _, event := range events {
		if stringValue(event.Data["triggerSource"]) == "failover_validation" {
			continue
		}
		switch stringValue(event.Data["status"]) {
		case "success":
			return count
		case "stopped":
			continue
		default:
			count++
		}
		if count >= limit {
			return count
		}
	}
	return count
}

func (m *Manager) acquireFailover(id string) bool {
	m.failoverMu.Lock()
	defer m.failoverMu.Unlock()
	if m.failoverRunning[id] {
		return false
	}
	m.failoverRunning[id] = true
	return true
}
func (m *Manager) releaseFailover(id string) {
	m.failoverMu.Lock()
	delete(m.failoverRunning, id)
	m.failoverMu.Unlock()
}

func (m *Manager) validateFailoverGroup(groups store.ProviderGroupStore, group domain.ProviderGroup, primaryRequestID string) {
	for _, providerID := range group.BackupProviderIDs {
		select {
		case <-m.ctx.Done():
			return
		default:
		}
		job, err := m.Start(domain.JobOptions{Mode: domain.ModeProbe, RunOnce: true, CLI: group.CLI, ProviderID: providerID, ScenarioID: group.ScenarioID, TriggerSource: "failover_validation"})
		if err != nil {
			continue
		}
		done, ok := m.waitFailoverJob(job.ID, 3700*time.Second)
		if !ok || done.Status != domain.JobSuccess {
			continue
		}
		validationRequestID := m.requestIDForJob(job.ID)
		now := time.Now().UTC()
		group.Advice = &domain.FailoverAdvice{Status: "open", PrimaryRequestID: primaryRequestID, SuggestedProviderID: providerID, ValidationJobID: job.ID, ValidationRequestID: validationRequestID, Reason: fmt.Sprintf("主线路连续失败 %d 次，备用线路已通过场景 %s", group.FailureThreshold, group.ScenarioID), CreatedAt: now, UpdatedAt: now}
		if _, err = groups.UpsertProviderGroup(group); err == nil {
			m.recordOperationalEvent(store.Event{At: now, Type: "failover_advice_opened", Level: "warning", ProviderID: group.PrimaryProviderID, JobID: job.ID, Message: "备用 Provider 验证成功，已生成切换建议", Data: map[string]any{"groupId": group.ID, "primaryRequestId": primaryRequestID, "suggestedProviderId": providerID, "validationRequestId": validationRequestID, "scenarioId": group.ScenarioID}})
			m.appendGroupIncidentEvent(group.ID, now, "failover_advice_opened", "备用 Provider 验证成功，已生成切换建议", validationRequestID, job.ID)
			m.activateAutomaticProviderGroup(groups, group, providerID, now)
		}
		return
	}
	now := time.Now().UTC()
	group.Advice = &domain.FailoverAdvice{Status: "validation_failed", PrimaryRequestID: primaryRequestID, Reason: "没有备用 Provider 通过同一合成场景验证", CreatedAt: now, UpdatedAt: now}
	_, _ = groups.UpsertProviderGroup(group)
	m.recordOperationalEvent(store.Event{At: now, Type: "failover_validation_failed", Level: "error", ProviderID: group.PrimaryProviderID, Message: "主线路异常，但没有备用 Provider 通过验证", Data: map[string]any{"groupId": group.ID, "primaryRequestId": primaryRequestID, "scenarioId": group.ScenarioID}})
	m.appendGroupIncidentEvent(group.ID, now, "failover_validation_failed", "所有备用 Provider 均未通过验证", primaryRequestID, "")
}

func (m *Manager) waitFailoverJob(id string, timeout time.Duration) (domain.Job, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return domain.Job{}, false
		case <-deadline.C:
			return domain.Job{}, false
		case <-ticker.C:
			job, err := m.Get(id)
			if err == nil && job.EndedAt != nil {
				return job, true
			}
		}
	}
}

func (m *Manager) requestIDForJob(jobID string) string {
	_ = m.FlushEvents()
	events, _ := m.store.ListEvents(store.EventFilter{JobID: jobID, Type: "request_end", Limit: 1})
	if len(events) == 0 {
		return ""
	}
	return stringValue(events[0].Data["requestId"])
}

// CompleteProviderGroupEvaluation is the single completion path for a
// user-triggered standby validation. It reloads the latest group before
// writing so an automatic switch cannot be overwritten by an API snapshot.
func (m *Manager) CompleteProviderGroupEvaluation(groupID, jobID string) {
	groups, ok := m.store.(store.ProviderGroupStore)
	if !ok {
		return
	}
	job, completed := m.waitFailoverJob(jobID, 3700*time.Second)
	now := time.Now().UTC()
	group, err := groups.GetProviderGroup(groupID)
	if err != nil || group.Advice == nil || group.Advice.ValidationJobID != jobID || group.Advice.Status != "validating" {
		return
	}
	group.Advice.UpdatedAt = now
	group.Advice.ValidationRequestID = m.requestIDForJob(jobID)
	if completed && job.Status == domain.JobSuccess {
		group.Advice.Status = "open"
		group.Advice.Reason = "备用线路已通过相同合成场景，可作为切换目标"
		if _, err = groups.UpsertProviderGroup(group); err == nil {
			m.activateAutomaticProviderGroup(groups, group, group.Advice.SuggestedProviderID, now)
		}
		return
	}
	group.Advice.Status = "validation_failed"
	if completed {
		group.Advice.Reason = "备用线路未通过合成场景，未生成切换建议"
	} else {
		group.Advice.Reason = "备用线路验证超时，未生成切换建议"
	}
	_, _ = groups.UpsertProviderGroup(group)
}

func (m *Manager) recoverFailoverAdvice(groups store.ProviderGroupStore, group domain.ProviderGroup, requestID string) {
	m.groupMutationMu.Lock()
	defer m.groupMutationMu.Unlock()
	latest, err := groups.GetProviderGroup(group.ID)
	if err != nil {
		return
	}
	if group.LastRecoveryProbeStatus != "" {
		latest.LastRecoveryProbeAt, latest.LastRecoveryProbeStatus = group.LastRecoveryProbeAt, group.LastRecoveryProbeStatus
	}
	group = latest
	if group.Advice == nil || (group.Advice.Status != "open" && group.Advice.Status != "applied") {
		_, _ = groups.UpsertProviderGroup(group)
		return
	}
	manualApplied := group.Advice.Status == "applied"
	threshold := group.RecoveryThreshold
	if threshold < 1 {
		threshold = 2
	}
	if m.consecutiveProviderSuccesses(group.PrimaryProviderID, threshold) < threshold {
		_, _ = groups.UpsertProviderGroup(group)
		return
	}
	now := time.Now().UTC()
	group.Advice.Status, group.Advice.UpdatedAt, group.Advice.RecoveredAt = "recovered", now, &now
	if (group.Mode == "automatic" || manualApplied) && group.ActiveProviderID != group.PrimaryProviderID {
		group.ActiveProviderID, group.LastSwitchedAt = group.PrimaryProviderID, &now
	}
	if _, err := groups.UpsertProviderGroup(group); err == nil {
		m.restartProviderGroupSchedules(group.ID)
		m.recordOperationalEvent(store.Event{At: now, Type: "failover_advice_recovered", Level: "success", ProviderID: group.PrimaryProviderID, Message: "主 Provider 已恢复，切换建议已关闭", Data: map[string]any{"groupId": group.ID, "requestId": requestID}})
		m.appendGroupIncidentEvent(group.ID, now, "failover_advice_recovered", "主 Provider 已恢复，切换建议已关闭", requestID, "")
	}
}

func (m *Manager) consecutiveProviderSuccesses(providerID string, limit int) int {
	if err := m.FlushEvents(); err != nil {
		return 0
	}
	events, err := m.store.ListEvents(store.EventFilter{ProviderID: providerID, Type: "request_end", Limit: max(20, limit*2)})
	if err != nil {
		return 0
	}
	count := 0
	for _, event := range events {
		switch stringValue(event.Data["status"]) {
		case "success":
			count++
		case "stopped", "running":
			continue
		default:
			return count
		}
		if count >= limit {
			return count
		}
	}
	return count
}

func (m *Manager) activateAutomaticProviderGroup(groups store.ProviderGroupStore, group domain.ProviderGroup, providerID string, now time.Time) {
	if group.Mode != "automatic" || providerID == "" || group.ActiveProviderID == providerID {
		return
	}
	if domain.ProviderGroupMaintenanceActive(group, now) {
		return
	}
	group.ActiveProviderID, group.LastSwitchedAt = providerID, &now
	if _, err := groups.UpsertProviderGroup(group); err != nil {
		return
	}
	m.restartProviderGroupSchedules(group.ID)
	m.recordOperationalEvent(store.Event{At: now, Type: "provider_group_switched", Level: "warning", ProviderID: providerID, Message: "AI Watch 组内计划已切换到验证通过的备用 Provider", Data: map[string]any{"groupId": group.ID, "activeProviderId": providerID, "hostConfigChanged": false}})
	m.appendGroupIncidentEvent(group.ID, now, "automatic_switch", "AI Watch 组内计划已切换到验证通过的备用 Provider", "", "")
}

func (m *Manager) providerGroupScheduleCount(groupID string) int {
	values, err := m.store.ListSchedules()
	if err != nil {
		return 0
	}
	count := 0
	for _, schedule := range values {
		if schedule.ProviderGroupID == groupID {
			count++
		}
	}
	return count
}

func (m *Manager) restartProviderGroupSchedules(groupID string) int {
	values, err := m.store.ListSchedules()
	if err != nil {
		return 0
	}
	count := 0
	for _, schedule := range values {
		if schedule.ProviderGroupID == groupID {
			count++
			m.stopScheduleJob(schedule.ID)
		}
	}
	m.wakeSchedules()
	return count
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
