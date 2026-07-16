package api

import (
	"errors"
	"net/http"
	"strings"

	"ai-watch/internal/domain"
	"ai-watch/internal/jobs"
	"ai-watch/internal/store"
)

func (v scheduleInput) schedule(id string) domain.Schedule {
	return domain.Schedule{
		ID: id, Name: v.Name, Enabled: v.Enabled, CLI: v.CLI, ProviderID: v.ProviderID, ProviderGroupID: v.ProviderGroupID,
		Mode: v.Mode, Timezone: v.Timezone, WeekdaysMask: v.WeekdaysMask,
		StartMinute: v.StartMinute, EndMinute: v.EndMinute, UntilSuccess: v.UntilSuccess,
		TimeoutSeconds: v.TimeoutSeconds, RetryIntervalSeconds: v.RetryIntervalSeconds,
		KeepaliveIntervalSeconds: v.KeepaliveIntervalSeconds,
		FailureThreshold:         v.FailureThreshold, Model: v.Model, FallbackModel: v.FallbackModel, ScenarioID: v.ScenarioID,
	}
}

func (s *Server) schedules(w http.ResponseWriter) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "schedules_unavailable", "schedule manager is unavailable")
		return
	}
	values, err := s.jobs.ListSchedules()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "schedules_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, values)
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "schedules_unavailable", "schedule manager is unavailable")
		return
	}
	var input scheduleInput
	if !decode(w, r, &input) {
		return
	}
	value, err := s.jobs.UpsertSchedule(input.schedule(""))
	if err != nil {
		scheduleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (s *Server) scheduleRoute(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "schedules_unavailable", "schedule manager is unavailable")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/schedules/"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "schedule_not_found", "schedule not found")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var input scheduleInput
		if !decode(w, r, &input) {
			return
		}
		value, err := s.jobs.UpsertSchedule(input.schedule(id))
		if err != nil {
			scheduleError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	case http.MethodDelete:
		deleted, err := s.jobs.DeleteSchedule(id)
		if err != nil {
			scheduleError(w, err)
			return
		}
		if !deleted {
			writeError(w, http.StatusNotFound, "schedule_not_found", "schedule not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
	default:
		writeError(w, http.StatusNotFound, "schedule_not_found", "schedule endpoint not found")
	}
}

func scheduleError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrScheduleLimit) {
		writeError(w, http.StatusConflict, "schedule_limit", err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_schedule", err.Error())
}

type bulkJobItem struct {
	TargetID                 string     `json:"targetId"`
	ScheduleID               string     `json:"scheduleId"`
	ScenarioID               string     `json:"scenarioId"`
	CLI                      domain.CLI `json:"cli"`
	ProviderID               string     `json:"providerId"`
	TimeoutSeconds           int        `json:"timeoutSeconds"`
	RetryIntervalSeconds     int        `json:"retryIntervalSeconds"`
	KeepaliveIntervalSeconds int        `json:"keepaliveIntervalSeconds"`
	FailureThreshold         int        `json:"failureThreshold"`
	Model                    string     `json:"model"`
	FallbackModel            string     `json:"fallbackModel"`
}

type bulkJobResult struct {
	TargetID string      `json:"targetId"`
	OK       bool        `json:"ok"`
	Job      *domain.Job `json:"job,omitempty"`
	Error    string      `json:"error,omitempty"`
	Code     string      `json:"code,omitempty"`
}

func (s *Server) bulkJobs(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "jobs_unavailable", "job manager is unavailable")
		return
	}
	var request struct {
		Action string        `json:"action"`
		Items  []bulkJobItem `json:"items"`
	}
	if !decode(w, r, &request) {
		return
	}
	if len(request.Items) == 0 || len(request.Items) > 50 {
		writeError(w, http.StatusBadRequest, "invalid_bulk_items", "items must contain 1..50 entries")
		return
	}
	if request.Action != "probe" && request.Action != "probe_once" && request.Action != "keepalive" && request.Action != "keepalive_once" && request.Action != "stop" {
		writeError(w, http.StatusBadRequest, "invalid_bulk_action", "action must be probe, probe_once, keepalive, keepalive_once, or stop")
		return
	}
	results := make([]bulkJobResult, 0, len(request.Items))
	accepted := 0
	for _, item := range request.Items {
		result := bulkJobResult{TargetID: item.TargetID}
		var err error
		if request.Action == "stop" {
			err = s.jobs.StopTarget(item.CLI, item.ProviderID, item.ScheduleID)
		} else {
			mode := domain.ModeProbe
			if request.Action == "keepalive" || request.Action == "keepalive_once" {
				mode = domain.ModeKeepalive
			}
			options := domain.JobOptions{
				Mode: mode, RunOnce: request.Action == "probe_once" || request.Action == "keepalive_once",
				CLI: item.CLI, ProviderID: item.ProviderID, ScenarioID: item.ScenarioID,
				TimeoutSeconds:           item.TimeoutSeconds,
				RetryIntervalSeconds:     item.RetryIntervalSeconds,
				KeepaliveIntervalSeconds: item.KeepaliveIntervalSeconds,
				FailureThreshold:         item.FailureThreshold, Model: item.Model,
				FallbackModel: item.FallbackModel,
				TriggerSource: "bulk", ClientIP: requestClientIP(r),
			}
			var job domain.Job
			var startErr error
			if item.ScheduleID != "" {
				job, startErr = s.jobs.StartForSchedule(options, item.ScheduleID)
			} else {
				job, startErr = s.jobs.Start(options)
			}
			err = startErr
			if err == nil {
				result.Job = &job
			}
		}
		if err == nil {
			result.OK = true
			accepted++
		} else {
			result.Error = err.Error()
			result.Code = bulkJobErrorCode(err)
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "accepted": accepted, "failed": len(results) - accepted})
}

func bulkJobErrorCode(err error) string {
	switch {
	case errors.Is(err, jobs.ErrLockConflict):
		return "lock_conflict"
	case errors.Is(err, jobs.ErrActiveLimit):
		return "active_limit"
	case errors.Is(err, jobs.ErrNotFound):
		return "not_found"
	case errors.Is(err, jobs.ErrShuttingDown):
		return "shutting_down"
	default:
		return "invalid_job"
	}
}
