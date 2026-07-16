package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ai-watch/internal/reliability"
)

func (s *Server) reliabilityExport(w http.ResponseWriter, r *http.Request) {
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
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		writeError(w, http.StatusBadRequest, "invalid_reliability_format", "format must be csv or json")
		return
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
	filename := fmt.Sprintf("ai-watch-reliability-%s-%s.%s", selectedRange, result.GeneratedAt.Format("20060102-150405"), format)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if format == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(w)
	defer writer.Flush()
	_ = writer.Write([]string{"section", "name", "cli", "model", "samples", "success_rate", "average_ms", "p95_ms", "consecutive_failures", "status", "recommendation", "reasons", "action", "bucket_start", "requests", "failures"})
	_ = writer.Write([]string{"overall", "AI Watch", "", "", strconv.Itoa(result.Overall.Completed), floatString(result.Overall.SuccessRate), floatString(result.Overall.AverageDurationMillis), intString(result.Overall.P95DurationMillis), strconv.Itoa(result.Overall.ConsecutiveFailures), "", "", "", "", "", strconv.Itoa(result.Overall.Requests), strconv.Itoa(result.Overall.Counts.Timeout + result.Overall.Counts.Overloaded + result.Overall.Counts.Unmatched + result.Overall.Counts.Fatal + result.Overall.Counts.StartFailed)})
	for _, provider := range result.Providers {
		_ = writer.Write([]string{"provider", provider.Name, provider.CLI, provider.Model, strconv.Itoa(provider.Metrics.Completed), floatString(provider.Metrics.SuccessRate), floatString(provider.Metrics.AverageDurationMillis), intString(provider.Metrics.P95DurationMillis), strconv.Itoa(provider.Metrics.ConsecutiveFailures), provider.LastStatus, provider.Recommendation.Title, strings.Join(provider.Recommendation.Reasons, "；"), provider.Recommendation.Action, "", strconv.Itoa(provider.Metrics.Requests), strconv.Itoa(provider.Metrics.Counts.Timeout + provider.Metrics.Counts.Overloaded + provider.Metrics.Counts.Unmatched + provider.Metrics.Counts.Fatal + provider.Metrics.Counts.StartFailed)})
	}
	for _, bucket := range result.Buckets {
		_ = writer.Write([]string{"bucket", "", "", "", "", floatString(bucket.SuccessRate), floatString(bucket.AverageDurationMillis), "", "", "", "", "", "", bucket.Start.Format(time.RFC3339), strconv.Itoa(bucket.Requests), strconv.Itoa(bucket.Failures)})
	}
}

func floatString(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', 4, 64)
}
func intString(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}
