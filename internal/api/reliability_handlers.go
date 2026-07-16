package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/jobs"
	"ai-watch/internal/reliability"
)

func (s *Server) reliabilityDigestPreview(w http.ResponseWriter) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "digest_unavailable", "job manager is unavailable")
		return
	}
	value, err := s.jobs.ReliabilityDigestPreview()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "digest_preview_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) reliabilityDigestSend(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "digest_unavailable", "job manager is unavailable")
		return
	}
	value, err := s.jobs.SendReliabilityDigest(r.Context())
	if errors.Is(err, jobs.ErrNotificationsNotConfigured) {
		writeError(w, http.StatusConflict, "notifications_not_configured", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "digest_send_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) reliability(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "reliability_unavailable", "event store is unavailable")
		return
	}
	if s.jobs != nil {
		if err := s.jobs.FlushEvents(); err != nil {
			writeError(w, http.StatusServiceUnavailable, "reliability_flush_failed", err.Error())
			return
		}
	}
	selectedRange := strings.TrimSpace(r.URL.Query().Get("range"))
	if selectedRange == "" {
		selectedRange = "24h"
	}
	retentionDays := 0
	if s.jobs != nil {
		retentionDays = s.jobs.Settings().EventRetentionDays
	}
	result, err := reliability.Build(s.store, selectedRange, time.Now().UTC(), s.activeProviderKeys(), retentionDays)
	if errors.Is(err, reliability.ErrInvalidRange) {
		writeError(w, http.StatusBadRequest, "invalid_reliability_range", "range must be one of 24h, 7d, or 30d")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "reliability_read_failed", err.Error())
		return
	}
	for index := range result.Providers {
		provider := &result.Providers[index]
		context, contextErr := s.resolveReliabilityActionContext(provider.CLI, provider.ProviderID)
		if contextErr != nil {
			continue
		}
		refs := make([]reliability.ScheduleRef, len(context.Schedules))
		for scheduleIndex := range context.Schedules {
			refs[scheduleIndex] = reliability.ScheduleRef{ID: context.Schedules[scheduleIndex].ID, Name: context.Schedules[scheduleIndex].Name, Enabled: context.Schedules[scheduleIndex].Enabled}
		}
		provider.Remediation = &reliability.Remediation{ProviderGroupID: context.ProviderGroupID, CanValidateBackup: context.CanValidateBackup, Schedules: refs}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) activeProviderKeys() map[string]bool {
	result := map[string]bool{}
	if s.scanner != nil {
		if values, err := s.scanner.Providers(""); err == nil {
			for _, value := range values {
				result[reliabilityProviderKey(string(value.CLI), value.ID)] = true
			}
		}
	}
	if s.secure != nil {
		if values, err := s.secure.ListCCSwitchProviders(); err == nil {
			for _, value := range values {
				result[reliabilityProviderKey(string(value.CLI), value.ID)] = true
			}
		}
		if values, err := s.secure.ListManualProviders(); err == nil {
			for _, value := range values {
				result[reliabilityProviderKey(string(value.CLI), "manual:"+value.ID)] = true
			}
		}
	}
	return result
}

func reliabilityProviderKey(cli, id string) string {
	if id == "" {
		id = "current"
	}
	return cli + ":" + id
}

type scheduleInput struct {
	Name                     string      `json:"name"`
	Enabled                  bool        `json:"enabled"`
	CLI                      domain.CLI  `json:"cli"`
	ProviderID               string      `json:"providerId"`
	ProviderGroupID          string      `json:"providerGroupId"`
	Mode                     domain.Mode `json:"mode"`
	Timezone                 string      `json:"timezone"`
	WeekdaysMask             int         `json:"weekdaysMask"`
	StartMinute              int         `json:"startMinute"`
	EndMinute                int         `json:"endMinute"`
	UntilSuccess             bool        `json:"untilSuccess"`
	TimeoutSeconds           int         `json:"timeoutSeconds"`
	RetryIntervalSeconds     int         `json:"retryIntervalSeconds"`
	KeepaliveIntervalSeconds int         `json:"keepaliveIntervalSeconds"`
	FailureThreshold         int         `json:"failureThreshold"`
	Model                    string      `json:"model"`
	FallbackModel            string      `json:"fallbackModel"`
	ScenarioID               string      `json:"scenarioId"`
}
