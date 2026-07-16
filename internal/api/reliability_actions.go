package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

type reliabilityActionContext struct {
	CLI               domain.CLI        `json:"cli"`
	ProviderID        string            `json:"providerId"`
	ProviderGroupID   string            `json:"providerGroupId,omitempty"`
	CanValidateBackup bool              `json:"canValidateBackup"`
	Schedules         []domain.Schedule `json:"schedules"`
}

func (s *Server) reliabilityActionContext(w http.ResponseWriter, r *http.Request) {
	context, err := s.resolveReliabilityActionContext(r.URL.Query().Get("cli"), r.URL.Query().Get("providerId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_reliability_target", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, context)
}

func (s *Server) reliabilityAction(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil || s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "reliability_actions_unavailable", "reliability actions are unavailable")
		return
	}
	var input struct {
		CLI        domain.CLI `json:"cli"`
		ProviderID string     `json:"providerId"`
		Action     string     `json:"action"`
	}
	if !decode(w, r, &input) {
		return
	}
	context, err := s.resolveReliabilityActionContext(string(input.CLI), input.ProviderID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_reliability_target", err.Error())
		return
	}
	now := time.Now().UTC()
	switch strings.TrimSpace(input.Action) {
	case "retest":
		job, startErr := s.jobs.Start(domain.JobOptions{Mode: domain.ModeProbe, RunOnce: true, CLI: context.CLI, ProviderID: context.ProviderID, TriggerSource: "reliability_remediation", ClientIP: requestClientIP(r)})
		if startErr != nil {
			writeError(w, http.StatusConflict, bulkJobErrorCode(startErr), startErr.Error())
			return
		}
		s.recordReliabilityAction(now, "reliability_retest_started", context, map[string]any{"jobId": job.ID})
		writeJSON(w, http.StatusAccepted, map[string]any{"action": "retest", "job": job})
	case "validate_backup":
		if !context.CanValidateBackup || context.ProviderGroupID == "" {
			writeError(w, http.StatusConflict, "provider_group_unavailable", "provider has no enabled failover group with a standby member")
			return
		}
		groups := s.store.(store.ProviderGroupStore)
		group, readErr := groups.GetProviderGroup(context.ProviderGroupID)
		if readErr != nil || !group.Enabled || group.PrimaryProviderID != context.ProviderID || len(group.BackupProviderIDs) == 0 {
			writeError(w, http.StatusConflict, "provider_group_changed", "provider group relation changed; refresh reliability data")
			return
		}
		job, candidateID, startErr := s.startProviderGroupEvaluation(groups, group, r)
		if startErr != nil {
			writeError(w, http.StatusConflict, bulkJobErrorCode(startErr), startErr.Error())
			return
		}
		s.recordReliabilityAction(now, "reliability_backup_validation_started", context, map[string]any{"jobId": job.ID, "candidateProviderId": candidateID})
		writeJSON(w, http.StatusAccepted, map[string]any{"action": "validate_backup", "groupId": group.ID, "candidateProviderId": candidateID, "job": job})
	case "pause_schedules":
		paused := make([]domain.Schedule, 0, len(context.Schedules))
		for _, schedule := range context.Schedules {
			if !schedule.Enabled {
				continue
			}
			schedule.Enabled = false
			value, pauseErr := s.jobs.UpsertSchedule(schedule)
			if pauseErr != nil {
				writeError(w, http.StatusConflict, "schedule_pause_failed", pauseErr.Error())
				return
			}
			paused = append(paused, value)
		}
		ids := make([]any, len(paused))
		for index := range paused {
			ids[index] = paused[index].ID
		}
		s.recordReliabilityAction(now, "reliability_schedules_paused", context, map[string]any{"scheduleIds": ids, "paused": len(paused)})
		writeJSON(w, http.StatusOK, map[string]any{"action": "pause_schedules", "paused": len(paused), "schedules": paused})
	default:
		writeError(w, http.StatusBadRequest, "invalid_reliability_action", "action must be retest, validate_backup, or pause_schedules")
	}
}

func (s *Server) resolveReliabilityActionContext(rawCLI, providerID string) (reliabilityActionContext, error) {
	if s.store == nil {
		return reliabilityActionContext{}, errors.New("reliability action store is unavailable")
	}
	cli := domain.CLI(strings.TrimSpace(rawCLI))
	if cli != domain.CLICodex && cli != domain.CLIClaude {
		return reliabilityActionContext{}, errors.New("cli must be codex or claude")
	}
	providerID = strings.TrimSpace(providerID)
	result := reliabilityActionContext{CLI: cli, ProviderID: providerID, Schedules: []domain.Schedule{}}
	groupsByID := map[string]domain.ProviderGroup{}
	if groups, ok := s.store.(store.ProviderGroupStore); ok {
		values, err := groups.ListProviderGroups()
		if err != nil {
			return result, err
		}
		for _, group := range values {
			groupsByID[group.ID] = group
			if result.ProviderGroupID == "" && group.Enabled && group.CLI == cli && group.PrimaryProviderID == providerID && len(group.BackupProviderIDs) > 0 {
				result.ProviderGroupID, result.CanValidateBackup = group.ID, true
			}
		}
	}
	schedules, err := s.store.ListSchedules()
	if err != nil {
		return result, err
	}
	for _, schedule := range schedules {
		if schedule.CLI != cli {
			continue
		}
		related := schedule.ProviderGroupID == "" && schedule.ProviderID == providerID
		if group, ok := groupsByID[schedule.ProviderGroupID]; ok {
			related = group.PrimaryProviderID == providerID || group.ActiveProviderID == providerID
			for _, backupID := range group.BackupProviderIDs {
				related = related || backupID == providerID
			}
		}
		if related {
			result.Schedules = append(result.Schedules, schedule)
		}
	}
	return result, nil
}

func (s *Server) recordReliabilityAction(at time.Time, typ string, context reliabilityActionContext, extra map[string]any) {
	data := map[string]any{"action": strings.TrimPrefix(typ, "reliability_"), "cli": context.CLI, "providerId": context.ProviderID, "providerGroupId": context.ProviderGroupID}
	for key, value := range extra {
		data[key] = value
	}
	settings := s.jobs.Settings()
	_ = s.store.SaveEvent(store.Event{At: at, Type: typ, Level: "info", ProviderID: context.ProviderID, Message: "已执行可靠性处置操作", Data: data}, store.EventRetention{MaxAge: time.Duration(settings.EventRetentionDays) * 24 * time.Hour, MaxRows: settings.EventRetentionRows, MaxBytes: settings.EventRetentionBytes})
}
