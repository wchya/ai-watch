package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

func (s *Server) providerGroupStore(w http.ResponseWriter) (store.ProviderGroupStore, bool) {
	values, ok := s.store.(store.ProviderGroupStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "provider_groups_unavailable", "provider groups are unavailable")
		return nil, false
	}
	return values, true
}

func (s *Server) providerGroups(w http.ResponseWriter) {
	values, ok := s.providerGroupStore(w)
	if !ok {
		return
	}
	result, err := values.ListProviderGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "provider_groups_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) upsertProviderGroup(w http.ResponseWriter, r *http.Request) {
	values, ok := s.providerGroupStore(w)
	if !ok {
		return
	}
	var value domain.ProviderGroup
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_provider_group", "invalid provider group payload")
		return
	}
	if scenarios, valid := s.store.(store.TestScenarioStore); valid {
		scenario, err := scenarios.GetTestScenario(value.ScenarioID)
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusBadRequest, "scenario_not_found", "test scenario does not exist")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "scenario_read_failed", err.Error())
			return
		}
		if !scenario.Enabled || (scenario.CLI != "" && scenario.CLI != value.CLI) {
			writeError(w, http.StatusBadRequest, "scenario_incompatible", "test scenario is disabled or incompatible with selected cli")
			return
		}
	}
	if current, getErr := values.GetProviderGroup(value.ID); getErr == nil {
		value.LastSwitchedAt = current.LastSwitchedAt
		value.LastRecoveryProbeAt = current.LastRecoveryProbeAt
		value.LastRecoveryProbeStatus = current.LastRecoveryProbeStatus
	}
	saved, err := values.UpsertProviderGroup(value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_provider_group", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) deleteProviderGroup(w http.ResponseWriter, r *http.Request) {
	values, ok := s.providerGroupStore(w)
	if !ok {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_provider_group_id", "provider group id is required")
		return
	}
	deleted, err := values.DeleteProviderGroup(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "provider_group_delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "id": id})
}

func (s *Server) evaluateProviderGroup(w http.ResponseWriter, r *http.Request, id string) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "jobs_unavailable", "job manager is unavailable")
		return
	}
	values, ok := s.providerGroupStore(w)
	if !ok {
		return
	}
	group, err := values.GetProviderGroup(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_group_not_found", "provider group not found")
		return
	}
	if !group.Enabled {
		writeError(w, http.StatusConflict, "provider_group_disabled", "provider group is disabled")
		return
	}
	if group.PrimaryProviderID == "" || len(group.BackupProviderIDs) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_provider_group", "provider group has no valid standby member")
		return
	}
	job, candidateID, err := s.startProviderGroupEvaluation(values, group, r)
	if err != nil {
		writeError(w, http.StatusConflict, bulkJobErrorCode(err), err.Error())
		return
	}
	activeProviderID := group.ActiveProviderID
	if activeProviderID == "" {
		activeProviderID = group.PrimaryProviderID
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"groupId": group.ID, "mode": group.Mode, "activeProviderId": activeProviderID, "candidateProviderId": candidateID, "recommendation": "validating", "job": job, "hostConfigChanged": false})
}

func (s *Server) startProviderGroupEvaluation(values store.ProviderGroupStore, group domain.ProviderGroup, r *http.Request) (domain.Job, string, error) {
	if domain.ProviderGroupMaintenanceActive(group, time.Now().UTC()) {
		return domain.Job{}, "", errors.New("provider group is in an active maintenance window")
	}
	candidateID := group.BackupProviderIDs[0]
	job, err := s.jobs.Start(domain.JobOptions{Mode: domain.ModeProbe, RunOnce: true, CLI: group.CLI, ProviderID: candidateID, ScenarioID: group.ScenarioID, TriggerSource: "failover_validation", ClientIP: requestClientIP(r)})
	if err != nil {
		return domain.Job{}, candidateID, err
	}
	now := time.Now().UTC()
	group.Advice = &domain.FailoverAdvice{Status: "validating", SuggestedProviderID: candidateID, ValidationJobID: job.ID, Reason: "正在使用相同合成场景验证第一优先级备用线路", CreatedAt: now, UpdatedAt: now}
	if _, saveErr := values.UpsertProviderGroup(group); saveErr != nil {
		_ = s.jobs.Stop(job.ID)
		return domain.Job{}, candidateID, saveErr
	}
	go s.jobs.CompleteProviderGroupEvaluation(group.ID, job.ID)
	return job, candidateID, nil
}

func (s *Server) applyProviderGroupAdvice(w http.ResponseWriter, r *http.Request, id string) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "jobs_unavailable", "job manager is unavailable")
		return
	}
	var input struct {
		SuggestedProviderID string    `json:"suggestedProviderId"`
		AdviceUpdatedAt     time.Time `json:"adviceUpdatedAt"`
		ConfirmGroupID      string    `json:"confirmGroupId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.ConfirmGroupID) != id || strings.TrimSpace(input.SuggestedProviderID) == "" || input.AdviceUpdatedAt.IsZero() {
		writeError(w, http.StatusBadRequest, "invalid_apply_advice", "group confirmation, suggested provider and advice timestamp are required")
		return
	}
	result, err := s.jobs.ApplyProviderGroupAdvice(id, strings.TrimSpace(input.SuggestedProviderID), input.AdviceUpdatedAt)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "provider_group_not_found", "provider group not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, "provider_group_advice_not_applicable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
