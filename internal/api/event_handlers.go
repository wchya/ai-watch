package api

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ai-watch/internal/store"
)

func (s *Server) operationalEvents(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "events_unavailable", "event store is unavailable")
		return
	}
	query := r.URL.Query()
	limit, err := parseBoundedQueryInt(query.Get("limit"), 100, 1, 500)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_event_limit", "limit must be an integer between 1 and 500")
		return
	}
	offset, err := parseBoundedQueryInt(query.Get("offset"), 0, 0, 1_000_000)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_event_offset", "offset must be an integer between 0 and 1000000")
		return
	}
	since, err := parseEventTime(query.Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_event_since", "since must be an RFC3339 timestamp")
		return
	}
	until, err := parseEventTime(query.Get("until"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_event_until", "until must be an RFC3339 timestamp")
		return
	}
	if !since.IsZero() && !until.IsZero() && since.After(until) {
		writeError(w, http.StatusBadRequest, "invalid_event_range", "since must not be after until")
		return
	}
	filter := store.EventFilter{
		ProviderID: strings.TrimSpace(query.Get("providerId")),
		JobID:      strings.TrimSpace(query.Get("jobId")),
		ScheduleID: strings.TrimSpace(query.Get("scheduleId")),
		RequestID:  strings.TrimSpace(query.Get("requestId")),
		Type:       strings.TrimSpace(query.Get("type")),
		Level:      strings.TrimSpace(query.Get("level")),
		Since:      since,
		Until:      until,
		Limit:      limit,
		Offset:     offset,
	}
	events, err := s.store.ListEvents(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "events_read_failed", err.Error())
		return
	}
	total, err := s.store.CountEvents(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "events_count_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "total": total})
}

func parseBoundedQueryInt(raw string, fallback, min, max int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return 0, errors.New("query integer is outside the allowed range")
	}
	return value, nil
}

func parseEventTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return value.UTC(), nil
}

func requestClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (s *Server) clearEvents(w http.ResponseWriter) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "events_unavailable", "event store is unavailable")
		return
	}
	if s.jobs != nil {
		if err := s.jobs.FlushEvents(); err != nil {
			writeError(w, http.StatusServiceUnavailable, "events_flush_failed", err.Error())
			return
		}
	}
	deleted, err := s.store.ClearEvents()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "events_clear_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
}
