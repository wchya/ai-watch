package jobs

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/reliability"
	"ai-watch/internal/store"
)

type reliabilityAlertState struct {
	Alerted               bool
	LastAlert             time.Time
	LastFailureAlertCount int
	RecoverySuccesses     int
	Loaded                bool
}

func (m *Manager) queueReliabilityEvaluation(event store.Event) {
	m.mu.RLock()
	enabled := m.settings.ReliabilityAlertEnabled
	closing := m.closing
	m.mu.RUnlock()
	if !enabled || closing {
		return
	}
	select {
	case m.notificationSlots <- struct{}{}:
		m.notificationWG.Add(1)
		go func() {
			defer func() { <-m.notificationSlots; m.notificationWG.Done() }()
			m.evaluateReliabilityAlert(event)
		}()
	default:
		// Evaluation is best effort. The next request_end event will retry.
	}
}

func (m *Manager) evaluateReliabilityAlert(event store.Event) {
	m.reliabilityAlertMu.Lock()
	defer m.reliabilityAlertMu.Unlock()

	m.mu.RLock()
	settings := m.settings
	m.mu.RUnlock()
	if !settings.ReliabilityAlertEnabled {
		return
	}
	cli := eventJobString(event, "cli")
	if cli == "" {
		return
	}
	providerKey := cli + ":" + event.ProviderID
	if event.ProviderID == "" {
		providerKey = cli + ":current"
	}
	result, err := reliability.Build(m.store, "24h", event.At, map[string]bool{providerKey: true}, settings.EventRetentionDays)
	if err != nil {
		return
	}
	var provider *reliability.Provider
	for index := range result.Providers {
		if result.Providers[index].Key == providerKey {
			provider = &result.Providers[index]
			break
		}
	}
	if provider == nil {
		return
	}
	activeReasons := reliabilityAlertReasons(provider.Metrics, settings)
	now := event.At.UTC()
	state := m.reliabilityState(providerKey, event.ProviderID)
	if provider.Metrics.ConsecutiveFailures < state.LastFailureAlertCount {
		state.LastFailureAlertCount = 0
	}
	triggerReasons, failureIntervalReached := reliabilityAlertTriggerReasons(provider.Metrics, settings, state.LastFailureAlertCount)
	if len(activeReasons) > 0 {
		state.RecoverySuccesses = 0
		if len(triggerReasons) == 0 {
			m.storeReliabilityState(providerKey, state)
			return
		}
		message := reliabilityAlertMarkdown(provider.Name, cli, triggerReasons, provider.Metrics, now)
		deliveryErr := m.sendReliabilityMessage("reliability_alert", "AI Watch Provider 可靠性告警", message)
		typ, text := "reliability_alert_triggered", "Provider 可靠性阈值已触发"
		if deliveryErr != nil {
			typ, text = "reliability_alert_delivery_failed", "Provider 可靠性告警发送失败"
		}
		m.saveReliabilityAlertEvent(event, typ, text, providerKey, provider.Name, triggerReasons, provider.Metrics)
		if failureIntervalReached {
			state.LastFailureAlertCount = provider.Metrics.ConsecutiveFailures
		}
		state.Alerted, state.LastAlert = true, now
		m.storeReliabilityState(providerKey, state)
		return
	}
	if !state.Alerted {
		return
	}
	if provider.LastStatus == "success" {
		state.RecoverySuccesses++
	} else if provider.LastStatus != "stopped" {
		state.RecoverySuccesses = 0
	}
	if state.RecoverySuccesses < settings.ReliabilityAlertRecoverySuccesses {
		m.storeReliabilityState(providerKey, state)
		return
	}
	if settings.ReliabilityAlertRecoveryEnabled {
		_ = m.sendReliabilityMessage("reliability_recovered", "AI Watch Provider 可靠性恢复", reliabilityRecoveryMarkdown(provider.Name, cli, provider.Metrics, now))
	}
	m.saveReliabilityAlertEvent(event, "reliability_alert_recovered", "Provider 可靠性已恢复", providerKey, provider.Name, nil, provider.Metrics)
	m.storeReliabilityState(providerKey, reliabilityAlertState{Loaded: true})
}

func reliabilityAlertReasons(metrics reliability.Metrics, settings domain.Settings) []string {
	auxiliaryReasons := reliabilityAuxiliaryAlertReasons(metrics, settings)
	if metrics.ConsecutiveFailures < settings.ReliabilityAlertConsecutiveFailures || len(auxiliaryReasons) == 0 {
		return nil
	}
	reasons := []string{fmt.Sprintf("连续失败 %d 次", metrics.ConsecutiveFailures)}
	return append(reasons, auxiliaryReasons...)
}

func reliabilityAuxiliaryAlertReasons(metrics reliability.Metrics, settings domain.Settings) []string {
	var reasons []string
	if metrics.Completed >= settings.ReliabilityAlertMinSamples && metrics.SuccessRate != nil && *metrics.SuccessRate*100 < settings.ReliabilityAlertSuccessRate {
		reasons = append(reasons, fmt.Sprintf("24 小时成功率 %.2f%% < %s%%", *metrics.SuccessRate*100, formatReliabilityPercent(settings.ReliabilityAlertSuccessRate)))
	}
	if settings.ReliabilityAlertP95Millis > 0 && metrics.Completed >= settings.ReliabilityAlertMinSamples && metrics.P95DurationMillis != nil && *metrics.P95DurationMillis > int64(settings.ReliabilityAlertP95Millis) {
		reasons = append(reasons, fmt.Sprintf("P95 %dms > %dms", *metrics.P95DurationMillis, settings.ReliabilityAlertP95Millis))
	}
	return reasons
}

func reliabilityAlertTriggerReasons(metrics reliability.Metrics, settings domain.Settings, lastFailureAlertCount int) ([]string, bool) {
	failureIntervalReached := settings.ReliabilityAlertConsecutiveFailures > 0 &&
		metrics.ConsecutiveFailures >= settings.ReliabilityAlertConsecutiveFailures &&
		metrics.ConsecutiveFailures%settings.ReliabilityAlertConsecutiveFailures == 0 &&
		metrics.ConsecutiveFailures > lastFailureAlertCount
	auxiliaryReasons := reliabilityAuxiliaryAlertReasons(metrics, settings)
	if !failureIntervalReached || len(auxiliaryReasons) == 0 {
		return nil, false
	}
	reasons := []string{fmt.Sprintf("连续失败 %d 次（每 %d 次告警）", metrics.ConsecutiveFailures, settings.ReliabilityAlertConsecutiveFailures)}
	return append(reasons, auxiliaryReasons...), true
}

func formatReliabilityPercent(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func (m *Manager) reliabilityState(key, providerID string) reliabilityAlertState {
	m.mu.Lock()
	if m.notifications.reliability == nil {
		m.notifications.reliability = map[string]reliabilityAlertState{}
	}
	state := m.notifications.reliability[key]
	m.mu.Unlock()
	if state.Loaded {
		return state
	}
	for _, typ := range []string{"reliability_alert_triggered", "reliability_alert_delivery_failed", "reliability_alert_recovered"} {
		events, err := m.store.ListEvents(store.EventFilter{ProviderID: providerID, Type: typ, Limit: 100})
		if err != nil {
			continue
		}
		for _, event := range events {
			if eventDataString(event.Data, "providerKey") != key || event.At.Before(state.LastAlert) {
				continue
			}
			state.LastAlert = event.At
			state.Alerted = typ != "reliability_alert_recovered"
			if state.Alerted {
				state.LastFailureAlertCount = eventDataInt(event.Data, "consecutiveFailures")
			} else {
				state.LastFailureAlertCount = 0
			}
		}
	}
	state.Loaded = true
	m.storeReliabilityState(key, state)
	return state
}

func (m *Manager) storeReliabilityState(key string, state reliabilityAlertState) {
	m.mu.Lock()
	if m.notifications.reliability == nil {
		m.notifications.reliability = map[string]reliabilityAlertState{}
	}
	m.notifications.reliability[key] = state
	m.mu.Unlock()
}

func (m *Manager) saveReliabilityAlertEvent(source store.Event, typ, message, providerKey, providerName string, reasons []string, metrics reliability.Metrics) {
	values := make([]any, len(reasons))
	for i := range reasons {
		values[i] = reasons[i]
	}
	data := map[string]any{"providerKey": providerKey, "providerName": providerName, "reasons": values, "requests": metrics.Requests, "completed": metrics.Completed, "consecutiveFailures": metrics.ConsecutiveFailures, "maxConsecutiveFailures": metrics.MaxConsecutiveFailures}
	if metrics.SuccessRate != nil {
		data["successRate"] = *metrics.SuccessRate
	}
	if metrics.P95DurationMillis != nil {
		data["p95DurationMillis"] = *metrics.P95DurationMillis
	}
	m.mu.RLock()
	settings := m.settings
	m.mu.RUnlock()
	if err := m.store.SaveEvent(store.Event{At: source.At, Type: typ, Level: alertLevel(typ), ProviderID: source.ProviderID, JobID: source.JobID, Message: message, Data: data}, store.EventRetention{MaxAge: time.Duration(settings.EventRetentionDays) * 24 * time.Hour, MaxRows: settings.EventRetentionRows, MaxBytes: settings.EventRetentionBytes}); err != nil {
		m.persistenceErr.Store(err.Error())
	}
}

func (m *Manager) sendReliabilityMessage(kind, title, content string) error {
	return m.sendRoutedMessage(context.Background(), kind, title, content)
}
func reliabilityAlertMarkdown(name, cli string, reasons []string, metrics reliability.Metrics, at time.Time) string {
	return fmt.Sprintf("### ⚠️ Provider 可靠性告警\n\n- Provider：%s\n- CLI：%s\n- 原因：%s\n- 样本：%d\n- 时间：%s", name, reliabilityCLILabel(cli), strings.Join(reasons, "；"), metrics.Completed, at.Format(time.RFC3339))
}
func reliabilityRecoveryMarkdown(name, cli string, metrics reliability.Metrics, at time.Time) string {
	return fmt.Sprintf("### ✅ Provider 可靠性恢复\n\n- Provider：%s\n- CLI：%s\n- 24 小时样本：%d\n- 时间：%s", name, reliabilityCLILabel(cli), metrics.Completed, at.Format(time.RFC3339))
}
func eventJobString(event store.Event, key string) string {
	job, _ := event.Data["job"].(map[string]any)
	return eventDataString(job, key)
}
func eventDataString(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return strings.TrimSpace(value)
}
func eventDataInt(data map[string]any, key string) int {
	switch value := data[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	default:
		return 0
	}
}
func alertLevel(typ string) string {
	if typ == "reliability_alert_recovered" {
		return "success"
	}
	return "warning"
}
func reliabilityCLILabel(value string) string {
	if value == "codex" {
		return "Codex"
	}
	if value == "claude" {
		return "Claude"
	}
	return value
}
