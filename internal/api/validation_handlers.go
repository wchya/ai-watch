package api

import (
	"net/http"
	"strings"

	"ai-watch/internal/domain"
	"ai-watch/internal/store"
)

func (s *Server) scenarioStore(w http.ResponseWriter) (store.TestScenarioStore, bool) {
	values, ok := s.store.(store.TestScenarioStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "test_scenarios_unavailable", "test scenario store is unavailable")
	}
	return values, ok
}

func (s *Server) testScenarios(w http.ResponseWriter) {
	values, ok := s.scenarioStore(w)
	if !ok {
		return
	}
	result, err := values.ListTestScenarios()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "test_scenarios_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) upsertTestScenario(w http.ResponseWriter, r *http.Request) {
	values, ok := s.scenarioStore(w)
	if !ok {
		return
	}
	var value domain.TestScenario
	if !decode(w, r, &value) {
		return
	}
	saved, err := values.UpsertTestScenario(value)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_test_scenario", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) deleteTestScenario(w http.ResponseWriter, r *http.Request) {
	values, ok := s.scenarioStore(w)
	if !ok {
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
	deleted, err := values.DeleteTestScenario(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_test_scenario", err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "test_scenario_not_found", "test scenario not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}
