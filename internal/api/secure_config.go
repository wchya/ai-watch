package api

import (
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"ai-watch/internal/domain"
	"ai-watch/internal/jobs"
	"ai-watch/internal/secureconfig"
)

type manualProviderCreateInput struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	CLI           domain.CLI       `json:"cli"`
	Enabled       *bool            `json:"enabled,omitempty"`
	BaseURL       string           `json:"baseUrl"`
	Model         string           `json:"model,omitempty"`
	Provider      string           `json:"provider,omitempty"`
	ProxyMode     domain.ProxyMode `json:"proxyMode,omitempty"`
	APIKey        string           `json:"apiKey"`
	ClearAPIKey   bool             `json:"clearApiKey,omitempty"`
	ProxyURL      string           `json:"proxyUrl,omitempty"`
	ClearProxyURL bool             `json:"clearProxyUrl,omitempty"`
}

func (v manualProviderCreateInput) write() domain.ManualProviderWrite {
	return domain.ManualProviderWrite{
		Name: v.Name, CLI: v.CLI, Enabled: v.Enabled, BaseURL: v.BaseURL, Model: v.Model,
		Provider: v.Provider, ProxyMode: v.ProxyMode, APIKey: v.APIKey,
		ClearAPIKey: v.ClearAPIKey, ProxyURL: v.ProxyURL, ClearProxyURL: v.ClearProxyURL,
	}
}

func (s *Server) manualProviders(w http.ResponseWriter) {
	if s.secure == nil {
		writeError(w, http.StatusServiceUnavailable, "secure_config_unavailable", "secure provider configuration is unavailable")
		return
	}
	values, err := s.secure.ListManualProviders()
	if err != nil {
		secureConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, values)
}

func (s *Server) ccSwitchProviderProxy(w http.ResponseWriter, r *http.Request) {
	if s.secure == nil {
		writeError(w, http.StatusServiceUnavailable, "secure_config_unavailable", "secure provider configuration is unavailable")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/cc-switch-providers/"), "/proxy")
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "cc_switch_provider_not_found", "CC Switch provider not found")
		return
	}
	var input struct {
		CLI       domain.CLI       `json:"cli"`
		ProxyMode domain.ProxyMode `json:"proxyMode"`
	}
	if !decode(w, r, &input) {
		return
	}
	value, err := s.secure.SaveCCSwitchProxyOverride(input.CLI, id, input.ProxyMode)
	if err != nil {
		switch {
		case errors.Is(err, secureconfig.ErrInvalidCCSwitchProxy):
			writeError(w, http.StatusBadRequest, "invalid_cc_switch_proxy", err.Error())
		case errors.Is(err, fs.ErrNotExist):
			writeError(w, http.StatusNotFound, "cc_switch_provider_not_found", "CC Switch provider not found")
		default:
			secureConfigError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) createManualProvider(w http.ResponseWriter, r *http.Request) {
	if s.secure == nil {
		writeError(w, http.StatusServiceUnavailable, "secure_config_unavailable", "secure provider configuration is unavailable")
		return
	}
	var input manualProviderCreateInput
	if !decode(w, r, &input) {
		return
	}
	value, err := s.secure.CreateManualProvider(input.ID, input.write())
	if err != nil {
		secureConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, value)
}

func (s *Server) manualProviderRoute(w http.ResponseWriter, r *http.Request) {
	if s.secure == nil {
		writeError(w, http.StatusServiceUnavailable, "secure_config_unavailable", "secure provider configuration is unavailable")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/manual-providers/"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "manual_provider_not_found", "manual provider not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		value, err := s.secure.GetManualProvider(id)
		if err != nil {
			secureConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	case http.MethodPut:
		var input domain.ManualProviderWrite
		if !decode(w, r, &input) {
			return
		}
		value, err := s.secure.UpdateManualProvider(id, input)
		if err != nil {
			secureConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	case http.MethodDelete:
		if conflict := s.manualProviderReference(id); conflict != "" {
			writeError(w, http.StatusConflict, "manual_provider_in_use", conflict)
			return
		}
		deleted, err := s.secure.DeleteManualProvider(id)
		if err != nil {
			secureConfigError(w, err)
			return
		}
		if !deleted {
			writeError(w, http.StatusNotFound, "manual_provider_not_found", "manual provider not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
	default:
		writeError(w, http.StatusNotFound, "manual_provider_not_found", "manual provider endpoint not found")
	}
}

func (s *Server) manualProviderReference(id string) string {
	if s.jobs == nil {
		return ""
	}
	providerID := secureconfig.ManualProviderPrefix + secureconfig.NormalizeManualProviderID(id)
	states := s.jobs.ProviderStates()
	for _, cli := range []domain.CLI{domain.CLICodex, domain.CLIClaude} {
		if state, ok := jobs.ProviderStateFor(states, cli, providerID); ok && state.ActiveJobID != "" {
			return "manual provider has an active job; stop it before deleting"
		}
	}
	schedules, err := s.jobs.ListSchedules()
	if err != nil {
		return "manual provider references could not be checked"
	}
	for _, schedule := range schedules {
		if schedule.ProviderID == providerID {
			return "manual provider is referenced by a schedule; delete or update the schedule first"
		}
	}
	return ""
}

func (s *Server) dingTalkConfig(w http.ResponseWriter) {
	if s.secure == nil {
		writeError(w, http.StatusServiceUnavailable, "secure_config_unavailable", "secure DingTalk configuration is unavailable")
		return
	}
	value, err := s.secure.EffectiveDingTalkConfig()
	if err != nil {
		secureConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) saveDingTalkConfig(w http.ResponseWriter, r *http.Request) {
	if s.secure == nil {
		writeError(w, http.StatusServiceUnavailable, "secure_config_unavailable", "secure DingTalk configuration is unavailable")
		return
	}
	var input domain.DingTalkConfigWrite
	if !decode(w, r, &input) {
		return
	}
	value, err := s.secure.SaveDingTalkConfig(input)
	if err != nil {
		secureConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func secureConfigError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, "manual_provider_not_found", "manual provider not found")
	case errors.Is(err, secureconfig.ErrInvalidManualProvider):
		writeError(w, http.StatusBadRequest, "invalid_manual_provider", err.Error())
	case errors.Is(err, secureconfig.ErrEncryptionUnavailable):
		writeError(w, http.StatusServiceUnavailable, "encryption_unavailable", err.Error())
	default:
		message := err.Error()
		if strings.Contains(strings.ToLower(message), "webhookurl") {
			writeError(w, http.StatusBadRequest, "invalid_dingtalk_config", message)
			return
		}
		writeError(w, http.StatusInternalServerError, "secure_config_failed", message)
	}
}
