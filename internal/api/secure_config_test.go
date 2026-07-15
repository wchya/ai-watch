package api

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ai-watch/internal/configscan"
	"ai-watch/internal/domain"
	"ai-watch/internal/jobs"
	"ai-watch/internal/secureconfig"
	"ai-watch/internal/store"
)

type apiSecureStore struct {
	mu        sync.Mutex
	providers map[string]domain.ManualProvider
	ccSwitch  map[string]domain.CCSwitchProvider
	dingTalk  domain.DingTalkConfig
}

func (s *apiSecureStore) ListManualProviders() ([]domain.ManualProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make([]domain.ManualProvider, 0, len(s.providers))
	for _, value := range s.providers {
		values = append(values, value)
	}
	return values, nil
}
func (s *apiSecureStore) GetManualProvider(id string) (domain.ManualProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.providers[id]
	if !ok {
		return domain.ManualProvider{}, fs.ErrNotExist
	}
	return value, nil
}
func (s *apiSecureStore) UpsertManualProvider(value domain.ManualProvider) (domain.ManualProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.providers == nil {
		s.providers = map[string]domain.ManualProvider{}
	}
	s.providers[value.ID] = value
	return value, nil
}
func (s *apiSecureStore) DeleteManualProvider(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[id]; !ok {
		return false, nil
	}
	delete(s.providers, id)
	return true, nil
}
func (s *apiSecureStore) LoadDingTalkConfig() (domain.DingTalkConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dingTalk, nil
}
func (s *apiSecureStore) SaveDingTalkConfig(value domain.DingTalkConfig) (domain.DingTalkConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dingTalk = value
	return value, nil
}
func (s *apiSecureStore) ClearDingTalkConfig() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existed := s.dingTalk.WebhookURL != ""
	s.dingTalk = domain.DingTalkConfig{}
	return existed, nil
}

func (s *apiSecureStore) ListCCSwitchProviders() ([]domain.CCSwitchProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]domain.CCSwitchProvider, 0, len(s.ccSwitch))
	for _, value := range s.ccSwitch {
		result = append(result, value)
	}
	return result, nil
}

func (s *apiSecureStore) GetCCSwitchProvider(id string) (domain.CCSwitchProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.ccSwitch[id]
	if !ok {
		return domain.CCSwitchProvider{}, fs.ErrNotExist
	}
	return value, nil
}

func TestProvidersAPIIncludesMaskedCCSwitchRedisSnapshot(t *testing.T) {
	secureStore := &apiSecureStore{ccSwitch: map[string]domain.CCSwitchProvider{
		"cc-provider": {
			ID: "cc-provider", Name: "Synced", CLI: domain.CLICodex,
			BaseURL: "https://example.test/v1", Model: "gpt-test", Provider: "openai",
			APIKey: "cc-switch-api-secret", CodexConfig: "model = 'gpt-test'",
		},
	}}
	scanner := &configscan.Scanner{CodexDir: t.TempDir(), ClaudeDir: t.TempDir()}
	server := New(scanner, nil, "").WithSecureConfig(secureconfig.New(secureStore, scanner, ""))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/providers?cli=codex", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"id":"cc-provider"`) || strings.Contains(recorder.Body.String(), "cc-switch-api-secret") || !strings.Contains(recorder.Body.String(), `"available":true`) {
		t.Fatalf("provider snapshot response=%d %s", recorder.Code, recorder.Body.String())
	}
}

func TestManualProviderAPIKeepsSecretsOutOfResponses(t *testing.T) {
	secureStore := &apiSecureStore{}
	server := (&Server{}).WithSecureConfig(secureconfig.New(secureStore, nil, ""))
	body := `{"id":"ray","name":"Ray","cli":"codex","baseUrl":"https://ray.example/v1","provider":"custom","proxyMode":"custom","proxyUrl":"socks5://proxy-user:proxy-secret@proxy.example:1080","apiKey":"sk-super-secret-value"}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/manual-providers", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "super-secret-value") || strings.Contains(recorder.Body.String(), "proxy-secret") || !strings.Contains(recorder.Body.String(), `"hasApiKey":true`) || !strings.Contains(recorder.Body.String(), `"hasProxyUrl":true`) {
		t.Fatalf("create response leaked or omitted secret state: %s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/manual-providers", nil))
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "super-secret-value") {
		t.Fatalf("list response leaked secret: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestManualProviderDeleteUsesNormalizedIDForScheduleConflict(t *testing.T) {
	persistence := store.New(t.TempDir())
	defer persistence.Close()
	manager := jobs.New(apiResolver{}, apiExecutor{}, persistence)
	defer manager.Shutdown()
	_, err := manager.UpsertSchedule(domain.Schedule{
		ID: "manual-reference", Name: "Manual reference", Enabled: true,
		CLI: domain.CLICodex, ProviderID: "manual:ray", Mode: domain.ModeProbe,
		Timezone: "UTC", WeekdaysMask: 127, StartMinute: 0, EndMinute: 1440,
		UntilSuccess: true, TimeoutSeconds: 15, RetryIntervalSeconds: 2,
		KeepaliveIntervalSeconds: 120, FailureThreshold: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	secureStore := &apiSecureStore{providers: map[string]domain.ManualProvider{
		"ray": {ID: "ray", Name: "Ray", CLI: domain.CLICodex, Enabled: true, BaseURL: "https://ray.example/v1", APIKey: "secret"},
	}}
	server := New(nil, manager, "", persistence).WithSecureConfig(secureconfig.New(secureStore, nil, ""))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/api/manual-providers/RAY", nil))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "referenced by a schedule") {
		t.Fatalf("case-normalized delete conflict returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if _, ok := secureStore.providers["ray"]; !ok {
		t.Fatal("referenced provider was deleted")
	}
}

func TestManualProviderAPIValidationAndMissing(t *testing.T) {
	server := (&Server{}).WithSecureConfig(secureconfig.New(&apiSecureStore{}, nil, ""))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/manual-providers", bytes.NewBufferString(`{"id":"bad","name":"Bad","cli":"codex","baseUrl":"not-a-url","apiKey":"secret"}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid provider returned %d: %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/manual-providers/missing", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing provider returned %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestDingTalkConfigAPIIsWriteOnly(t *testing.T) {
	secureStore := &apiSecureStore{}
	server := (&Server{}).WithSecureConfig(secureconfig.New(secureStore, nil, "https://env.example/robot?access_token=environment-secret"))
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/notifications/dingtalk/config", nil))
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "environment-secret") || !strings.Contains(recorder.Body.String(), `"source":"environment"`) {
		t.Fatalf("environment config was not safely returned: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/notifications/dingtalk/config", strings.NewReader(`{"webhookUrl":"https://oapi.dingtalk.com/robot/send?access_token=redis-secret"}`))
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "redis-secret") || !strings.Contains(recorder.Body.String(), `"source":"redis"`) {
		t.Fatalf("stored config was not safely returned: %d %s", recorder.Code, recorder.Body.String())
	}
	var response domain.DingTalkConfig
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || !response.Configured {
		t.Fatalf("invalid config response: %+v err=%v", response, err)
	}
}
