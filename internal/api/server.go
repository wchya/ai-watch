package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"ai-watch/internal/configscan"
	"ai-watch/internal/diagnostics"
	"ai-watch/internal/domain"
	"ai-watch/internal/jobs"
	"ai-watch/internal/reliability"
	"ai-watch/internal/secureconfig"
	"ai-watch/internal/store"
)

type Server struct {
	scanner       *configscan.Scanner
	jobs          *jobs.Manager
	webDir        string
	store         store.Store
	redis         *store.Redis
	redisMu       sync.Mutex
	secure        *secureconfig.Service
	idempotencyMu sync.Mutex
	idempotency   map[string]store.IdempotencyRecord
}

func New(scanner *configscan.Scanner, manager *jobs.Manager, webDir string, stores ...store.Store) *Server {
	var eventStore store.Store
	if len(stores) > 0 {
		eventStore = stores[0]
	}
	redisStore, _ := eventStore.(*store.Redis)
	return &Server{scanner: scanner, jobs: manager, webDir: webDir, store: eventStore, redis: redisStore, idempotency: map[string]store.IdempotencyRecord{}}
}
func (s *Server) WithSecureConfig(service *secureconfig.Service) *Server {
	s.secure = service
	return s
}
func (s *Server) Handler() http.Handler {
	return recoverMiddleware(s.idempotencyMiddleware(http.HandlerFunc(s.route)))
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/api/health" && r.Method == http.MethodGet:
		s.health(w)
	case p == "/api/diagnostics" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, diagnostics.New(s.scanner, s.jobs, s.store, "").Snapshot(r.Context()))
	case strings.HasPrefix(p, "/api/redis/"):
		s.redisRoute(w, r)
	case p == "/api/config/status" && r.Method == http.MethodGet:
		writeJSON(w, 200, s.scanner.Status())
	case p == "/api/providers" && r.Method == http.MethodGet:
		s.providers(w, r)
	case p == "/api/provider-examples" && r.Method == http.MethodGet:
		s.providerExamples(w, r)
	case p == "/api/provider-examples" && r.Method == http.MethodPost:
		s.upsertProviderExample(w, r)
	case p == "/api/provider-examples" && r.Method == http.MethodDelete:
		s.deleteProviderExample(w, r)
	case p == "/api/manual-providers" && r.Method == http.MethodGet:
		s.manualProviders(w)
	case p == "/api/manual-providers" && r.Method == http.MethodPost:
		s.createManualProvider(w, r)
	case strings.HasPrefix(p, "/api/manual-providers/"):
		s.manualProviderRoute(w, r)
	case p == "/api/jobs" && r.Method == http.MethodGet:
		writeJSON(w, 200, s.jobs.List())
	case p == "/api/jobs" && r.Method == http.MethodPost:
		s.start(w, r)
	case p == "/api/jobs/bulk" && r.Method == http.MethodPost:
		s.bulkJobs(w, r)
	case strings.HasPrefix(p, "/api/jobs/"):
		s.jobRoute(w, r)
	case p == "/api/schedules" && r.Method == http.MethodGet:
		s.schedules(w)
	case p == "/api/schedules" && r.Method == http.MethodPost:
		s.createSchedule(w, r)
	case strings.HasPrefix(p, "/api/schedules/"):
		s.scheduleRoute(w, r)
	case p == "/api/settings" && r.Method == http.MethodGet:
		writeJSON(w, 200, s.jobs.Settings())
	case p == "/api/settings" && r.Method == http.MethodPut:
		s.settings(w, r)
	case p == "/api/reliability" && r.Method == http.MethodGet:
		s.reliability(w, r)
	case p == "/api/events" && r.Method == http.MethodGet:
		s.operationalEvents(w, r)
	case p == "/api/events" && r.Method == http.MethodDelete:
		s.clearEvents(w)
	case p == "/api/notifications/test" && r.Method == http.MethodPost:
		s.notification(w, r)
	case p == "/api/notifications/status" && r.Method == http.MethodPost:
		s.notificationStatus(w, r)
	case p == "/api/notifications/dingtalk/config" && r.Method == http.MethodGet:
		s.dingTalkConfig(w)
	case p == "/api/notifications/dingtalk/config" && r.Method == http.MethodPut:
		s.saveDingTalkConfig(w, r)
	case strings.HasPrefix(p, "/api/"):
		writeError(w, 404, "not_found", "API endpoint not found")
	default:
		s.web(w, r)
	}
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
}

func (v scheduleInput) schedule(id string) domain.Schedule {
	return domain.Schedule{
		ID: id, Name: v.Name, Enabled: v.Enabled, CLI: v.CLI, ProviderID: v.ProviderID,
		Mode: v.Mode, Timezone: v.Timezone, WeekdaysMask: v.WeekdaysMask,
		StartMinute: v.StartMinute, EndMinute: v.EndMinute, UntilSuccess: v.UntilSuccess,
		TimeoutSeconds: v.TimeoutSeconds, RetryIntervalSeconds: v.RetryIntervalSeconds,
		KeepaliveIntervalSeconds: v.KeepaliveIntervalSeconds,
		FailureThreshold:         v.FailureThreshold, Model: v.Model, FallbackModel: v.FallbackModel,
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
			job, startErr := s.jobs.Start(domain.JobOptions{
				Mode: mode, RunOnce: request.Action == "probe_once" || request.Action == "keepalive_once",
				CLI: item.CLI, ProviderID: item.ProviderID,
				TimeoutSeconds:           item.TimeoutSeconds,
				RetryIntervalSeconds:     item.RetryIntervalSeconds,
				KeepaliveIntervalSeconds: item.KeepaliveIntervalSeconds,
				FailureThreshold:         item.FailureThreshold, Model: item.Model,
				FallbackModel: item.FallbackModel,
				TriggerSource: "bulk", ClientIP: requestClientIP(r),
			})
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

func (s *Server) providerExamples(w http.ResponseWriter, _ *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "provider_examples_unavailable", "provider example store is unavailable")
		return
	}
	examples, err := s.store.ListProviderExamples()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "provider_examples_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, examples)
}

func (s *Server) upsertProviderExample(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "provider_examples_unavailable", "provider example store is unavailable")
		return
	}
	var example domain.ProviderExample
	if !decode(w, r, &example) {
		return
	}
	saved, err := s.store.UpsertProviderExample(example)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_provider_example", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) deleteProviderExample(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "provider_examples_unavailable", "provider example store is unavailable")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		var request struct {
			ID string `json:"id"`
		}
		if !decode(w, r, &request) {
			return
		}
		id = request.ID
	}
	deleted, err := s.store.DeleteProviderExample(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_provider_example", err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "provider_example_not_found", "provider example not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

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

func (s *Server) health(w http.ResponseWriter) {
	if s.store != nil {
		if _, err := s.store.Diagnostics(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "time": time.Now().UTC(), "version": "dev", "storageError": "Redis is unavailable"})
			return
		}
	}
	if s.jobs != nil {
		if message := s.jobs.PersistenceError(); message != "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "time": time.Now().UTC(), "version": "dev", "persistenceError": message})
			return
		}
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "time": time.Now().UTC(), "version": "dev"})
}
func (s *Server) providers(w http.ResponseWriter, r *http.Request) {
	cli := domain.CLI(r.URL.Query().Get("cli"))
	v, e := s.scanner.Providers(cli)
	if e != nil {
		writeError(w, 400, "provider_discovery", e.Error())
		return
	}
	if s.secure != nil {
		ccSwitch, err := s.secure.ListCCSwitchProviders()
		if err != nil {
			secureConfigError(w, err)
			return
		}
		for _, value := range ccSwitch {
			if cli == "" || value.CLI == cli {
				v = append(v, value)
			}
		}
		manual, err := s.secure.ListManualProviders()
		if err != nil {
			secureConfigError(w, err)
			return
		}
		for _, value := range manual {
			if cli == "" || value.CLI == cli {
				v = append(v, s.secure.Provider(value))
			}
		}
	}
	if s.jobs != nil {
		states := s.jobs.ProviderStates()
		for index := range v {
			if state, ok := jobs.ProviderStateFor(states, v[index].CLI, v[index].ID); ok {
				v[index].State = &state
			}
		}
	}
	if s.scanner.CCSwitchWarning() != "" {
		w.Header().Set("X-AI-Watch-Warning", "cc-switch-degraded")
	}
	writeJSON(w, 200, v)
}
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	var v domain.JobOptions
	if !decode(w, r, &v) {
		return
	}
	v.TriggerSource = "manual"
	v.ClientIP = requestClientIP(r)
	job, e := s.jobs.Start(v)
	if e != nil {
		code := 400
		kind := "invalid_job"
		if errors.Is(e, jobs.ErrLockConflict) {
			code = 409
			kind = "lock_conflict"
		} else if errors.Is(e, jobs.ErrActiveLimit) {
			code = http.StatusTooManyRequests
			kind = "active_limit"
		}
		writeError(w, code, kind, e.Error())
		return
	}
	writeJSON(w, 201, job)
}
func (s *Server) jobRoute(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, 404, "not_found", "job not found")
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		v, e := s.jobs.Get(id)
		if e != nil {
			writeError(w, 404, "not_found", e.Error())
			return
		}
		writeJSON(w, 200, v)
		return
	}
	if len(parts) == 2 && parts[1] == "stop" && r.Method == http.MethodPost {
		if e := s.jobs.Stop(id); e != nil {
			writeError(w, 404, "not_found", e.Error())
			return
		}
		writeJSON(w, 202, map[string]bool{"accepted": true})
		return
	}
	if len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodGet {
		s.events(w, r, id)
		return
	}
	writeError(w, 404, "not_found", "job endpoint not found")
}

func (s *Server) events(w http.ResponseWriter, r *http.Request, id string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "streaming_unavailable", "streaming unavailable")
		return
	}
	after := uint64(0)
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("after")
	}
	if n, e := strconv.ParseUint(raw, 10, 64); e == nil {
		after = n
	}
	replay, ch, cleanup, e := s.jobs.Subscribe(id, after)
	if e != nil {
		writeError(w, 404, "not_found", e.Error())
		return
	}
	defer cleanup()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(event domain.Event) bool {
		b, _ := json.Marshal(event)
		// Emit both a typed event and a default message. This keeps EventSource
		// clients using addEventListener(type) and onmessage clients compatible.
		if _, e := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\nid: %d\ndata: %s\n\n", event.ID, event.Type, b, event.ID, b); e != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	for _, e := range replay {
		if !send(e) {
			return
		}
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return
			}
			if !send(e) {
				return
			}
		case <-ticker.C:
			if _, e := fmt.Fprint(w, ": keepalive\n\n"); e != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	type input struct {
		TimeoutSeconds                      *int    `json:"timeoutSeconds"`
		RetryIntervalSeconds                *int    `json:"retryIntervalSeconds"`
		KeepaliveIntervalSeconds            *int    `json:"keepaliveIntervalSeconds"`
		KeepaliveSummarySeconds             *int    `json:"keepaliveSummarySeconds"`
		KeepaliveSummarySuccesses           *int    `json:"keepaliveSummarySuccesses"`
		ProbeProgressSeconds                *int    `json:"probeProgressSeconds"`
		RecoveryMergeSeconds                *int    `json:"recoveryMergeSeconds"`
		ReliabilityAlertEnabled             *bool   `json:"reliabilityAlertEnabled"`
		ReliabilityAlertMinSamples          *int    `json:"reliabilityAlertMinSamples"`
		ReliabilityAlertSuccessRate         *int    `json:"reliabilityAlertSuccessRate"`
		ReliabilityAlertConsecutiveFailures *int    `json:"reliabilityAlertConsecutiveFailures"`
		ReliabilityAlertP95Millis           *int    `json:"reliabilityAlertP95Millis"`
		ReliabilityAlertCooldownSeconds     *int    `json:"reliabilityAlertCooldownSeconds"`
		ReliabilityAlertRecoverySuccesses   *int    `json:"reliabilityAlertRecoverySuccesses"`
		ReliabilityAlertRecoveryEnabled     *bool   `json:"reliabilityAlertRecoveryEnabled"`
		HistoryLimit                        *int    `json:"historyLimit"`
		EventRetentionDays                  *int    `json:"eventRetentionDays"`
		EventRetentionRows                  *int    `json:"eventRetentionRows"`
		EventRetentionBytes                 *int64  `json:"eventRetentionBytes"`
		UITheme                             *string `json:"uiTheme"`
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

func (s *Server) notificationStatus(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "DingTalk webhook is not configured")
		return
	}
	if err := s.jobs.SendStatusSummary(r.Context()); err != nil {
		notificationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

func notificationError(w http.ResponseWriter, err error) {
	if errors.Is(err, jobs.ErrNotificationsNotConfigured) {
		writeError(w, http.StatusServiceUnavailable, "not_configured", err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "notification_failed", err.Error())
}

func (s *Server) web(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.webDir == "" {
		http.Error(w, "AI Watch backend is running", http.StatusNotFound)
		return
	}
	root := os.DirFS(s.webDir)
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	if st, e := fs.Stat(root, name); e == nil && !st.IsDir() {
		http.ServeFileFS(w, r, root, name)
		return
	}
	if _, e := fs.Stat(root, "index.html"); e == nil {
		http.ServeFileFS(w, r, root, "index.html")
		return
	}
	http.NotFound(w, r)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		writeError(w, 400, "invalid_json", e.Error())
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				writeError(w, 500, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
