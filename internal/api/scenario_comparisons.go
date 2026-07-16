package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

type scenarioComparisonItem struct {
	ProviderID      string     `json:"providerId"`
	ProviderName    string     `json:"providerName,omitempty"`
	JobID           string     `json:"jobId,omitempty"`
	RequestID       string     `json:"requestId,omitempty"`
	Status          string     `json:"status"`
	DurationMillis  int64      `json:"durationMillis,omitempty"`
	ErrorType       string     `json:"errorType,omitempty"`
	Error           string     `json:"error,omitempty"`
	ResponseExcerpt string     `json:"responseExcerpt,omitempty"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	EndedAt         *time.Time `json:"endedAt,omitempty"`
}

type scenarioComparison struct {
	ID           string                   `json:"id"`
	ScenarioID   string                   `json:"scenarioId"`
	ScenarioName string                   `json:"scenarioName"`
	CLI          domain.CLI               `json:"cli"`
	Status       string                   `json:"status"`
	CreatedAt    time.Time                `json:"createdAt"`
	Items        []scenarioComparisonItem `json:"items"`
}

func (s *Server) createScenarioComparison(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil || s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "scenario_comparisons_unavailable", "scenario comparisons are unavailable")
		return
	}
	scenarios, ok := s.store.(store.TestScenarioStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "test_scenarios_unavailable", "test scenarios are unavailable")
		return
	}
	var input struct {
		ScenarioID  string     `json:"scenarioId"`
		CLI         domain.CLI `json:"cli"`
		ProviderIDs []string   `json:"providerIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.ScenarioID = strings.TrimSpace(input.ScenarioID)
	if input.CLI != domain.CLICodex && input.CLI != domain.CLIClaude {
		writeError(w, http.StatusBadRequest, "invalid_comparison_cli", "cli must be codex or claude")
		return
	}
	providerIDs := uniqueComparisonProviders(input.ProviderIDs)
	if len(providerIDs) < 2 || len(providerIDs) > 10 || len(providerIDs) != len(input.ProviderIDs) {
		writeError(w, http.StatusBadRequest, "invalid_comparison_providers", "providerIds must contain 2..10 unique entries")
		return
	}
	scenario, err := scenarios.GetTestScenario(input.ScenarioID)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "test_scenario_not_found", "test scenario not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "test_scenario_read_failed", err.Error())
		return
	}
	if !scenario.Enabled {
		writeError(w, http.StatusConflict, "test_scenario_disabled", "test scenario is disabled")
		return
	}
	if scenario.CLI != "" && scenario.CLI != input.CLI {
		writeError(w, http.StatusBadRequest, "test_scenario_cli_mismatch", "test scenario does not support selected cli")
		return
	}
	comparison, err := s.startScenarioComparison(scenario, input.CLI, providerIDs, r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scenario_comparison_save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, comparison)
}

func (s *Server) startScenarioComparison(scenario domain.TestScenario, cli domain.CLI, providerIDs []string, r *http.Request) (scenarioComparison, error) {
	now := time.Now().UTC()
	comparison := scenarioComparison{ID: "comparison-" + comparisonRandomHex(10), ScenarioID: scenario.ID, ScenarioName: scenario.Name, CLI: cli, Status: "running", CreatedAt: now, Items: make([]scenarioComparisonItem, 0, len(providerIDs))}
	eventItems := make([]any, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		item := scenarioComparisonItem{ProviderID: providerID, Status: "queued"}
		job, startErr := s.jobs.Start(domain.JobOptions{Mode: domain.ModeProbe, RunOnce: true, CLI: cli, ProviderID: providerID, ScenarioID: scenario.ID, TriggerSource: "scenario_comparison", ClientIP: requestClientIP(r)})
		if startErr != nil {
			item.Status, item.Error = "start_failed", startErr.Error()
		} else {
			item.JobID, item.ProviderName = job.ID, job.ProviderName
			started := job.StartedAt
			item.StartedAt = &started
		}
		comparison.Items = append(comparison.Items, item)
		eventItems = append(eventItems, map[string]any{"providerId": item.ProviderID, "providerName": item.ProviderName, "jobId": item.JobID, "status": item.Status, "error": item.Error})
	}
	settings := s.jobs.Settings()
	if err := s.store.SaveEvent(store.Event{At: now, Type: "scenario_comparison_started", Level: "info", Message: "多 Provider 场景对比已启动", Data: map[string]any{"comparisonId": comparison.ID, "scenarioId": scenario.ID, "scenarioName": scenario.Name, "cli": cli, "items": eventItems}}, store.EventRetention{MaxAge: time.Duration(settings.EventRetentionDays) * 24 * time.Hour, MaxRows: settings.EventRetentionRows, MaxBytes: settings.EventRetentionBytes}); err != nil {
		for _, item := range comparison.Items {
			if item.JobID != "" {
				_ = s.jobs.Stop(item.JobID)
			}
		}
		return scenarioComparison{}, err
	}
	comparison.Status = comparisonStatus(comparison.Items)
	return comparison, nil
}

func (s *Server) scenarioComparison(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "scenario_comparisons_unavailable", "scenario comparisons are unavailable")
		return
	}
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/scenario-comparisons/"))
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "invalid_comparison_id", "comparison id is required")
		return
	}
	result, err := s.loadScenarioComparison(id)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "scenario_comparison_not_found", "scenario comparison not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scenario_comparison_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) scenarioComparisons(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "scenario_comparisons_unavailable", "scenario comparisons are unavailable")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != "running" && status != "completed" && status != "partial_failed" {
		writeError(w, http.StatusBadRequest, "invalid_comparison_status", "status must be running, completed, or partial_failed")
		return
	}
	limit, err := parseBoundedQueryInt(r.URL.Query().Get("limit"), 100, 1, 200)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_comparison_limit", "limit must be 1..200")
		return
	}
	events, err := s.store.ListEvents(store.EventFilter{Type: "scenario_comparison_started", Limit: 500})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scenario_comparisons_read_failed", err.Error())
		return
	}
	items := make([]scenarioComparison, 0, min(limit, len(events)))
	for index := range events {
		value := s.scenarioComparisonFromEvent(events[index])
		if status != "" && value.Status != status {
			continue
		}
		items = append(items, value)
		if len(items) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items), "retentionLimited": len(events) >= 500})
}

func (s *Server) rerunScenarioComparison(w http.ResponseWriter, r *http.Request) {
	if s.store == nil || s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "scenario_comparisons_unavailable", "scenario comparisons are unavailable")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/scenario-comparisons/"), "/rerun")
	id = strings.Trim(id, "/")
	previous, err := s.loadScenarioComparison(id)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "scenario_comparison_not_found", "scenario comparison not found or expired")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scenario_comparison_read_failed", err.Error())
		return
	}
	scenarios, ok := s.store.(store.TestScenarioStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "test_scenarios_unavailable", "test scenarios are unavailable")
		return
	}
	scenario, err := scenarios.GetTestScenario(previous.ScenarioID)
	if err != nil || !scenario.Enabled || (scenario.CLI != "" && scenario.CLI != previous.CLI) {
		writeError(w, http.StatusConflict, "scenario_comparison_expired", "original scenario is missing, disabled, or incompatible")
		return
	}
	providerIDs := make([]string, len(previous.Items))
	for index := range previous.Items {
		providerIDs[index] = previous.Items[index].ProviderID
	}
	result, err := s.startScenarioComparison(scenario, previous.CLI, providerIDs, r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "scenario_comparison_save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) loadScenarioComparison(id string) (scenarioComparison, error) {
	events, err := s.store.ListEvents(store.EventFilter{Type: "scenario_comparison_started", Limit: 500})
	if err != nil {
		return scenarioComparison{}, err
	}
	for index := range events {
		if stringValueAPI(events[index].Data["comparisonId"]) == id {
			return s.scenarioComparisonFromEvent(events[index]), nil
		}
	}
	return scenarioComparison{}, fs.ErrNotExist
}

func (s *Server) scenarioComparisonFromEvent(source store.Event) scenarioComparison {
	result := scenarioComparison{ID: stringValueAPI(source.Data["comparisonId"]), ScenarioID: stringValueAPI(source.Data["scenarioId"]), ScenarioName: stringValueAPI(source.Data["scenarioName"]), CLI: domain.CLI(stringValueAPI(source.Data["cli"])), CreatedAt: source.At}
	for _, raw := range anySlice(source.Data["items"]) {
		entry := anyMap(raw)
		item := scenarioComparisonItem{ProviderID: stringValueAPI(entry["providerId"]), ProviderName: stringValueAPI(entry["providerName"]), JobID: stringValueAPI(entry["jobId"]), Status: stringValueAPI(entry["status"]), Error: stringValueAPI(entry["error"])}
		s.enrichScenarioComparisonItem(&item)
		result.Items = append(result.Items, item)
	}
	result.Status = comparisonStatus(result.Items)
	return result
}

func (s *Server) enrichScenarioComparisonItem(item *scenarioComparisonItem) {
	if item.JobID == "" {
		return
	}
	if job, err := s.jobs.Get(item.JobID); err == nil {
		item.ProviderName = firstNonEmpty(item.ProviderName, job.ProviderName)
		item.Status = string(job.Status)
		item.DurationMillis = job.ElapsedMillis
		started := job.StartedAt
		item.StartedAt, item.EndedAt = &started, job.EndedAt
	}
	events, err := s.store.ListEvents(store.EventFilter{JobID: item.JobID, Type: "request_end", Limit: 1})
	if err != nil || len(events) == 0 {
		return
	}
	data := events[0].Data
	item.RequestID = stringValueAPI(data["requestId"])
	item.ErrorType = stringValueAPI(data["errorType"])
	item.Error = firstNonEmpty(stringValueAPI(data["error"]), item.Error)
	item.ResponseExcerpt = stringValueAPI(data["responseExcerpt"])
	if value, ok := int64Value(data["durationMillis"]); ok {
		item.DurationMillis = value
	}
	if value, ok := timeValue(data["startedAt"]); ok {
		item.StartedAt = &value
	}
	if value, ok := timeValue(data["endedAt"]); ok {
		item.EndedAt = &value
	}
	if status := stringValueAPI(data["status"]); status != "" {
		if status == "success" {
			item.Status = "success"
		} else if item.Status != "start_failed" {
			item.Status = "failed"
		}
	}
}

func comparisonStatus(items []scenarioComparisonItem) string {
	failed := false
	for _, item := range items {
		if item.Status == "queued" || item.Status == "starting" || item.Status == "running" {
			return "running"
		}
		failed = failed || item.Status != "success"
	}
	if failed {
		return "partial_failed"
	}
	return "completed"
}

func uniqueComparisonProviders(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func comparisonRandomHex(size int) string {
	b := make([]byte, size)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
func stringValueAPI(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func anySlice(value any) []any {
	if result, ok := value.([]any); ok {
		return result
	}
	return nil
}
func anyMap(value any) map[string]any { result, _ := value.(map[string]any); return result }
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
func int64Value(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case float64:
		return int64(typed), true
	}
	return 0, false
}
func timeValue(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC(), true
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, typed)
		return parsed.UTC(), err == nil
	}
	return time.Time{}, false
}
