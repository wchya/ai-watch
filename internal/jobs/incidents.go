package jobs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

func (m *Manager) queueIncidentEvaluation(event store.Event) {
	if event.ProviderID == "" {
		return
	}
	if _, ok := m.store.(store.IncidentStore); !ok {
		return
	}
	if m.closing {
		return
	}
	m.notificationWG.Add(1)
	go func() { defer m.notificationWG.Done(); m.aggregateIncident(event) }()
}

func (m *Manager) aggregateIncident(event store.Event) {
	m.incidentMu.Lock()
	defer m.incidentMu.Unlock()
	incidents := m.store.(store.IncidentStore)
	status := stringValue(event.Data["status"])
	if status == "running" || status == "stopped" {
		return
	}
	subjectType, subjectID, title, threshold := "provider", event.ProviderID, event.ProviderID+" 请求连续失败", 3
	var maintenanceStartsAt, maintenanceUntil *time.Time
	if groups, ok := m.store.(store.ProviderGroupStore); ok {
		values, _ := groups.ListProviderGroups()
		for _, group := range values {
			if group.Enabled && group.PrimaryProviderID == event.ProviderID {
				subjectType, subjectID, title, threshold, maintenanceStartsAt, maintenanceUntil = "group", group.ID, group.Name+" 请求连续失败", group.FailureThreshold, group.MaintenanceStartsAt, group.MaintenanceUntil
				break
			}
		}
	}
	current, findErr := incidents.FindOpenIncident(subjectType, subjectID)
	requestID, jobID, now := stringValue(event.Data["requestId"]), event.JobID, event.At
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if status == "success" {
		if findErr != nil {
			return
		}
		m.mu.RLock()
		recoverySuccesses := m.settings.ReliabilityAlertRecoverySuccesses
		m.mu.RUnlock()
		if recoverySuccesses < 1 {
			recoverySuccesses = 2
		}
		if m.consecutiveIncidentSuccesses(event.ProviderID, recoverySuccesses) < recoverySuccesses {
			return
		}
		current.Status, current.ResolvedAt = "resolved", &now
		current.Timeline = append(current.Timeline, incidentEntry(now, "recovered", "主体已恢复，请求重新通过验证", requestID, jobID))
		if saved, err := incidents.UpsertIncident(current); err == nil {
			if !saved.RecoveryNotificationSent && !incidentMuted(saved, now) {
				if notifyErr := m.sendRoutedMessage(context.Background(), "incident_recovered", "AI Watch 事故恢复", incidentRecoveryMarkdown(saved, now)); notifyErr == nil {
					saved.RecoveryNotificationSent = true
					_, _ = incidents.UpsertIncident(saved)
				}
			}
			m.recordOperationalEvent(store.Event{At: now, Type: "incident_resolved", Level: "success", ProviderID: event.ProviderID, JobID: jobID, Message: "事故已自动恢复", Data: map[string]any{"incidentId": saved.ID, "subjectType": subjectType, "subjectId": subjectID, "requestId": requestID}})
		}
		return
	}
	message := stringValue(event.Data["error"])
	if message == "" {
		message = stringValue(event.Data["responseExcerpt"])
	}
	if message == "" {
		message = "Provider 请求未通过"
	}
	opened := findErr != nil
	if opened && !errors.Is(findErr, fs.ErrNotExist) {
		return
	}
	if opened {
		current = domain.Incident{SubjectType: subjectType, SubjectID: subjectID, ProviderID: event.ProviderID, Title: title, Status: "open", Severity: "warning", ErrorCounts: map[string]int{}, StartedAt: now}
		if subjectType == "group" {
			current.GroupID = subjectID
		}
	}
	current.MaintenanceStartsAt, current.MaintenanceUntil = maintenanceStartsAt, maintenanceUntil
	for _, existing := range current.RequestIDs {
		if requestID != "" && existing == requestID {
			return
		}
	}
	current.FailureCount++
	errorType := stringValue(event.Data["errorType"])
	if errorType == "" {
		errorType = status
	}
	if current.ErrorCounts == nil {
		current.ErrorCounts = map[string]int{}
	}
	current.ErrorCounts[errorType]++
	current.RequestIDs = appendUniqueIncident(current.RequestIDs, requestID)
	current.JobIDs = appendUniqueIncident(current.JobIDs, jobID)
	current.Timeline = append(current.Timeline, incidentEntry(now, "failure", message, requestID, jobID))
	if current.FailureCount >= threshold {
		current.Severity = "critical"
	}
	saved, err := incidents.UpsertIncident(current)
	if err != nil {
		return
	}
	if opened {
		if !incidentMuted(saved, now) {
			if notifyErr := m.sendRoutedMessage(context.Background(), "incident_opened", "AI Watch 新事故", incidentOpenedMarkdown(saved, now)); notifyErr == nil {
				saved.PrimaryNotificationSent = true
				_, _ = incidents.UpsertIncident(saved)
			}
		}
		m.recordOperationalEvent(store.Event{At: now, Type: "incident_opened", Level: "warning", ProviderID: event.ProviderID, JobID: jobID, Message: "已聚合为新的开放事故", Data: map[string]any{"incidentId": saved.ID, "subjectType": subjectType, "subjectId": subjectID, "requestId": requestID}})
	}
}

func (m *Manager) consecutiveIncidentSuccesses(providerID string, limit int) int {
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

func incidentMuted(value domain.Incident, now time.Time) bool {
	return (value.MutedUntil != nil && value.MutedUntil.After(now)) || (value.SilencedUntil != nil && value.SilencedUntil.After(now)) || domain.MaintenanceWindowActive(value.MaintenanceStartsAt, value.MaintenanceUntil, now)
}

func incidentOpenedMarkdown(value domain.Incident, at time.Time) string {
	return fmt.Sprintf("### ⚠️ 新事故\n\n- 主体：%s\n- 摘要：%s\n- 失败次数：%d\n- 时间：%s", incidentSubjectLabel(value), value.Title, value.FailureCount, at.Format(time.RFC3339))
}

func incidentRecoveryMarkdown(value domain.Incident, at time.Time) string {
	return fmt.Sprintf("### ✅ 事故恢复\n\n- 主体：%s\n- 累计失败：%d\n- 关联请求：%d\n- 恢复时间：%s", incidentSubjectLabel(value), value.FailureCount, len(value.RequestIDs), at.Format(time.RFC3339))
}

func incidentSubjectLabel(value domain.Incident) string {
	if strings.TrimSpace(value.SubjectName) != "" {
		return value.SubjectName
	}
	return value.SubjectID
}

func (m *Manager) appendGroupIncidentEvent(groupID string, at time.Time, typ, message, requestID, jobID string) {
	incidents, ok := m.store.(store.IncidentStore)
	if !ok {
		return
	}
	m.incidentMu.Lock()
	defer m.incidentMu.Unlock()
	value, err := incidents.FindOpenIncident("group", groupID)
	if err != nil {
		return
	}
	value.Timeline = append(value.Timeline, incidentEntry(at, typ, message, requestID, jobID))
	_, _ = incidents.UpsertIncident(value)
}

func (m *Manager) RecordProviderGroupMaintenance(groupID, typ, message string, at time.Time) {
	m.appendGroupIncidentEvent(groupID, at, typ, message, "", "")
}

func incidentEntry(at time.Time, typ, message, requestID, jobID string) domain.IncidentEntry {
	return domain.IncidentEntry{ID: fmt.Sprintf("%d-%s", at.UnixNano(), typ), At: at, Type: typ, Message: message, RequestID: requestID, JobID: jobID}
}
func appendUniqueIncident(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}
