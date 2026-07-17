package api

import (
	"errors"
	"net/http"

	"ai-watch/internal/jobs"
)

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	type input struct {
		TimeoutSeconds                      *int     `json:"timeoutSeconds"`
		RetryIntervalSeconds                *int     `json:"retryIntervalSeconds"`
		KeepaliveIntervalSeconds            *int     `json:"keepaliveIntervalSeconds"`
		KeepaliveSummarySeconds             *int     `json:"keepaliveSummarySeconds"`
		KeepaliveSummarySuccesses           *int     `json:"keepaliveSummarySuccesses"`
		ProbeProgressSeconds                *int     `json:"probeProgressSeconds"`
		RecoveryMergeSeconds                *int     `json:"recoveryMergeSeconds"`
		ReliabilityAlertEnabled             *bool    `json:"reliabilityAlertEnabled"`
		ReliabilityAlertMinSamples          *int     `json:"reliabilityAlertMinSamples"`
		ReliabilityAlertSuccessRate         *float64 `json:"reliabilityAlertSuccessRate"`
		ReliabilityAlertConsecutiveFailures *int     `json:"reliabilityAlertConsecutiveFailures"`
		ReliabilityAlertP95Millis           *int     `json:"reliabilityAlertP95Millis"`
		ReliabilityAlertCooldownSeconds     *int     `json:"reliabilityAlertCooldownSeconds"`
		ReliabilityAlertRecoverySuccesses   *int     `json:"reliabilityAlertRecoverySuccesses"`
		ReliabilityAlertRecoveryEnabled     *bool    `json:"reliabilityAlertRecoveryEnabled"`
		ReliabilityDigestEnabled            *bool    `json:"reliabilityDigestEnabled"`
		ReliabilityDigestHour               *int     `json:"reliabilityDigestHour"`
		ReliabilityDigestMinute             *int     `json:"reliabilityDigestMinute"`
		ReliabilityDigestTimezone           *string  `json:"reliabilityDigestTimezone"`
		ReliabilityDigestRange              *string  `json:"reliabilityDigestRange"`
		HistoryLimit                        *int     `json:"historyLimit"`
		EventRetentionDays                  *int     `json:"eventRetentionDays"`
		EventRetentionRows                  *int     `json:"eventRetentionRows"`
		EventRetentionBytes                 *int64   `json:"eventRetentionBytes"`
		UITheme                             *string  `json:"uiTheme"`
	}
	var request input
	if !decode(w, r, &request) {
		return
	}
	v := s.jobs.Settings()
	if request.TimeoutSeconds != nil {
		v.TimeoutSeconds = *request.TimeoutSeconds
	}
	if request.RetryIntervalSeconds != nil {
		v.RetryIntervalSeconds = *request.RetryIntervalSeconds
	}
	if request.KeepaliveIntervalSeconds != nil {
		v.KeepaliveIntervalSeconds = *request.KeepaliveIntervalSeconds
	}
	if request.KeepaliveSummarySeconds != nil {
		v.KeepaliveSummarySeconds = *request.KeepaliveSummarySeconds
	}
	if request.KeepaliveSummarySuccesses != nil {
		v.KeepaliveSummarySuccesses = *request.KeepaliveSummarySuccesses
	}
	if request.ProbeProgressSeconds != nil {
		v.ProbeProgressSeconds = *request.ProbeProgressSeconds
	}
	if request.RecoveryMergeSeconds != nil {
		v.RecoveryMergeSeconds = *request.RecoveryMergeSeconds
	}
	if request.ReliabilityAlertEnabled != nil {
		v.ReliabilityAlertEnabled = *request.ReliabilityAlertEnabled
	}
	if request.ReliabilityAlertMinSamples != nil {
		v.ReliabilityAlertMinSamples = *request.ReliabilityAlertMinSamples
	}
	if request.ReliabilityAlertSuccessRate != nil {
		v.ReliabilityAlertSuccessRate = *request.ReliabilityAlertSuccessRate
	}
	if request.ReliabilityAlertConsecutiveFailures != nil {
		v.ReliabilityAlertConsecutiveFailures = *request.ReliabilityAlertConsecutiveFailures
	}
	if request.ReliabilityAlertP95Millis != nil {
		v.ReliabilityAlertP95Millis = *request.ReliabilityAlertP95Millis
	}
	if request.ReliabilityAlertCooldownSeconds != nil {
		v.ReliabilityAlertCooldownSeconds = *request.ReliabilityAlertCooldownSeconds
	}
	if request.ReliabilityAlertRecoverySuccesses != nil {
		v.ReliabilityAlertRecoverySuccesses = *request.ReliabilityAlertRecoverySuccesses
	}
	if request.ReliabilityAlertRecoveryEnabled != nil {
		v.ReliabilityAlertRecoveryEnabled = *request.ReliabilityAlertRecoveryEnabled
	}
	if request.ReliabilityDigestEnabled != nil {
		v.ReliabilityDigestEnabled = *request.ReliabilityDigestEnabled
	}
	if request.ReliabilityDigestHour != nil {
		v.ReliabilityDigestHour = *request.ReliabilityDigestHour
	}
	if request.ReliabilityDigestMinute != nil {
		v.ReliabilityDigestMinute = *request.ReliabilityDigestMinute
	}
	if request.ReliabilityDigestTimezone != nil {
		v.ReliabilityDigestTimezone = *request.ReliabilityDigestTimezone
	}
	if request.ReliabilityDigestRange != nil {
		v.ReliabilityDigestRange = *request.ReliabilityDigestRange
	}
	if request.HistoryLimit != nil {
		v.HistoryLimit = *request.HistoryLimit
	}
	if request.EventRetentionDays != nil {
		v.EventRetentionDays = *request.EventRetentionDays
	}
	if request.EventRetentionRows != nil {
		v.EventRetentionRows = *request.EventRetentionRows
	}
	if request.EventRetentionBytes != nil {
		v.EventRetentionBytes = *request.EventRetentionBytes
	}
	if request.UITheme != nil {
		v.UITheme = *request.UITheme
	}
	if e := s.jobs.SetSettings(v); e != nil {
		writeError(w, 400, "invalid_settings", e.Error())
		return
	}
	writeJSON(w, 200, s.jobs.Settings())
}
func (s *Server) notification(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "DingTalk webhook is not configured")
		return
	}
	if err := s.jobs.TestNotification(r.Context()); err != nil {
		notificationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"sent": true})
}

func notificationError(w http.ResponseWriter, err error) {
	if errors.Is(err, jobs.ErrNotificationsNotConfigured) {
		writeError(w, http.StatusServiceUnavailable, "not_configured", err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "notification_failed", err.Error())
}
