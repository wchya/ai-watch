package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/jobs"
)

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
