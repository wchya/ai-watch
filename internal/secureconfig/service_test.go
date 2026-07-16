package secureconfig

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ai-watch/internal/domain"
)

type memoryStore struct {
	mu        sync.Mutex
	providers map[string]domain.ManualProvider
	dingTalk  domain.DingTalkConfig
	channels  map[string]domain.NotificationChannel
	routes    domain.NotificationRoutes
}

func (s *memoryStore) ListNotificationChannels() ([]domain.NotificationChannel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := []domain.NotificationChannel{}
	for _, value := range s.channels {
		values = append(values, value)
	}
	return values, nil
}
func (s *memoryStore) GetNotificationChannel(id string) (domain.NotificationChannel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.channels[id]
	if !ok {
		return domain.NotificationChannel{}, fs.ErrNotExist
	}
	return value, nil
}
func (s *memoryStore) UpsertNotificationChannel(value domain.NotificationChannel) (domain.NotificationChannel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.channels == nil {
		s.channels = map[string]domain.NotificationChannel{}
	}
	s.channels[value.ID] = value
	return value, nil
}
func (s *memoryStore) DeleteNotificationChannel(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.channels[id]; !ok {
		return false, nil
	}
	delete(s.channels, id)
	return true, nil
}
func (s *memoryStore) LoadNotificationRoutes() (domain.NotificationRoutes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.routes.Routes == nil {
		return domain.NotificationRoutes{Routes: map[string]string{}}, nil
	}
	return s.routes, nil
}
func (s *memoryStore) SaveNotificationRoutes(value domain.NotificationRoutes) (domain.NotificationRoutes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes = value
	return value, nil
}

func (s *memoryStore) ListManualProviders() ([]domain.ManualProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make([]domain.ManualProvider, 0, len(s.providers))
	for _, value := range s.providers {
		values = append(values, value)
	}
	return values, nil
}
func (s *memoryStore) GetManualProvider(id string) (domain.ManualProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.providers[id]
	if !ok {
		return domain.ManualProvider{}, fs.ErrNotExist
	}
	return value, nil
}
func (s *memoryStore) UpsertManualProvider(value domain.ManualProvider) (domain.ManualProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.providers == nil {
		s.providers = map[string]domain.ManualProvider{}
	}
	s.providers[value.ID] = value
	return value, nil
}
func (s *memoryStore) DeleteManualProvider(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.providers[id]; !ok {
		return false, nil
	}
	delete(s.providers, id)
	return true, nil
}
func (s *memoryStore) LoadDingTalkConfig() (domain.DingTalkConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dingTalk.WebhookURL == "" {
		return domain.DingTalkConfig{}, fs.ErrNotExist
	}
	return s.dingTalk, nil
}
func (s *memoryStore) SaveDingTalkConfig(value domain.DingTalkConfig) (domain.DingTalkConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dingTalk = value
	return value, nil
}
func (s *memoryStore) ClearDingTalkConfig() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existed := s.dingTalk.WebhookURL != ""
	s.dingTalk = domain.DingTalkConfig{}
	return existed, nil
}

type fallbackResolver struct{ called bool }

func (r *fallbackResolver) Resolve(cli domain.CLI, id string) (domain.ResolvedConfig, error) {
	r.called = true
	return domain.ResolvedConfig{ProviderID: id, Provider: string(cli)}, nil
}

type ccMemoryStore struct {
	*memoryStore
	values map[string]domain.CCSwitchProvider
}

func (s *ccMemoryStore) ListCCSwitchProviders() ([]domain.CCSwitchProvider, error) {
	result := make([]domain.CCSwitchProvider, 0, len(s.values))
	for _, value := range s.values {
		result = append(result, value)
	}
	return result, nil
}

func (s *ccMemoryStore) GetCCSwitchProvider(id string) (domain.CCSwitchProvider, error) {
	value, ok := s.values[id]
	if !ok {
		return domain.CCSwitchProvider{}, fs.ErrNotExist
	}
	return value, nil
}

func TestCCSwitchProviderListsAndResolvesFromRedisStore(t *testing.T) {
	store := &ccMemoryStore{memoryStore: &memoryStore{}, values: map[string]domain.CCSwitchProvider{
		"cc-codex": {
			ID: "cc-codex", Name: "Synced Codex", CLI: domain.CLICodex, Current: true,
			BaseURL: "https://codex.example/v1", Model: "gpt-test", Provider: "openai",
			APIKey: "cc-codex-secret", CodexConfig: "model = 'gpt-test'",
		},
		"cc-claude": {
			ID: "cc-claude", Name: "Synced Claude", CLI: domain.CLIClaude,
			BaseURL: "https://claude.example", Model: "claude-test", Provider: "anthropic-compatible",
			APIKey: "cc-claude-secret", ClaudeEnv: map[string]string{"ANTHROPIC_AUTH_TOKEN": "cc-claude-secret", "ANTHROPIC_BASE_URL": "https://claude.example"},
		},
	}}
	fallback := &fallbackResolver{}
	service := New(store, fallback, "")
	providers, err := service.ListCCSwitchProviders()
	if err != nil || len(providers) != 2 {
		t.Fatalf("providers=%+v err=%v", providers, err)
	}
	serialized, err := json.Marshal(providers)
	if err != nil || strings.Contains(string(serialized), "cc-codex-secret") || strings.Contains(string(serialized), "model =") {
		t.Fatalf("public providers leaked synced secrets: %s err=%v", serialized, err)
	}
	codex, err := service.Resolve(domain.CLICodex, "cc-codex")
	if err != nil || codex.Source != "cc-switch-redis" || codex.APIKey != "cc-codex-secret" || codex.CodexConfig == "" {
		t.Fatalf("resolved codex=%+v err=%v", codex, err)
	}
	claude, err := service.Resolve(domain.CLIClaude, "cc-claude")
	if err != nil || claude.APIKey != "cc-claude-secret" || claude.ClaudeEnv["ANTHROPIC_BASE_URL"] != "https://claude.example" {
		t.Fatalf("resolved claude=%+v err=%v", claude, err)
	}
	claude.ClaudeEnv["ANTHROPIC_AUTH_TOKEN"] = "mutated"
	stored, _ := store.GetCCSwitchProvider("cc-claude")
	if stored.ClaudeEnv["ANTHROPIC_AUTH_TOKEN"] != "cc-claude-secret" {
		t.Fatal("resolved Claude environment mutated the Redis-store value")
	}
	if fallback.called {
		t.Fatal("synced CC Switch provider unexpectedly used the runtime SQLite fallback")
	}
}

func TestManualProviderCreateUpdateResolveAndMask(t *testing.T) {
	store := &memoryStore{}
	fallback := &fallbackResolver{}
	service := New(store, fallback, "")
	created, err := service.CreateManualProvider("Ray_Main", domain.ManualProviderWrite{
		Name: "Ray", CLI: domain.CLICodex, BaseURL: "https://ray.example/v1",
		Model: "gpt-test", Provider: "custom", APIKey: "sk-manual-provider-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != "ray_main" || !created.HasAPIKey || created.APIKey != "" || strings.Contains(created.MaskedKey, "manual-provider-secret") {
		t.Fatalf("manual provider was not safely returned: %+v", created)
	}
	if !created.Enabled || created.ProxyMode != domain.ProxyDefault {
		t.Fatalf("manual provider defaults were not applied: %+v", created)
	}
	resolved, err := service.Resolve(domain.CLICodex, "manual:ray_main")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.APIKey != "sk-manual-provider-secret" || resolved.ProviderID != "manual:ray_main" || !strings.Contains(resolved.CodexConfig, "ray.example") {
		t.Fatalf("manual provider was not resolved: %+v", resolved)
	}
	updated, err := service.UpdateManualProvider("ray_main", domain.ManualProviderWrite{
		Name: "Ray 2", CLI: domain.CLICodex, BaseURL: "https://ray.example/v2", Provider: "custom",
	})
	if err != nil || !updated.HasAPIKey {
		t.Fatalf("blank update should retain secret: %+v err=%v", updated, err)
	}
	stored, _ := store.GetManualProvider("ray_main")
	if stored.APIKey != "sk-manual-provider-secret" {
		t.Fatal("manual provider secret was not retained")
	}
	if _, err = service.Resolve(domain.CLIClaude, "manual:ray_main"); err == nil {
		t.Fatal("cross-CLI manual provider resolution should fail")
	}
	if fallback.called {
		t.Fatal("manual provider unexpectedly used fallback resolver")
	}
}

func TestManualProviderCustomProxyRetainReplaceClearAndMask(t *testing.T) {
	store := &memoryStore{}
	service := New(store, nil, "")
	created, err := service.CreateManualProvider("proxied", domain.ManualProviderWrite{
		Name: "Proxied", CLI: domain.CLICodex, BaseURL: "https://example.test/v1", APIKey: "secret",
		ProxyMode: domain.ProxyCustom, ProxyURL: "socks5://proxy-user:proxy-pass@proxy.example:1080",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created.HasProxyURL || created.ProxyURL != "" || created.MaskedProxyURL != "socks5://****@proxy.example:1080" || strings.Contains(created.MaskedProxyURL, "proxy-pass") {
		t.Fatalf("custom proxy was not safely returned: %+v", created)
	}
	resolved, err := service.Resolve(domain.CLICodex, "manual:proxied")
	if err != nil || resolved.ProxyMode != domain.ProxyCustom || resolved.ProxyURL != "socks5://proxy-user:proxy-pass@proxy.example:1080" {
		t.Fatalf("custom proxy was not resolved: %+v err=%v", resolved, err)
	}
	retained, err := service.UpdateManualProvider("proxied", domain.ManualProviderWrite{
		Name: "Proxied", CLI: domain.CLICodex, BaseURL: "https://example.test/v1", ProxyMode: domain.ProxyCustom,
	})
	if err != nil || !retained.HasProxyURL {
		t.Fatalf("blank proxy update should retain the secret: %+v err=%v", retained, err)
	}
	stored, _ := store.GetManualProvider("proxied")
	if stored.ProxyURL != "socks5://proxy-user:proxy-pass@proxy.example:1080" {
		t.Fatalf("proxy URL was not retained: %+v", stored)
	}
	replaced, err := service.UpdateManualProvider("proxied", domain.ManualProviderWrite{
		Name: "Proxied", CLI: domain.CLICodex, BaseURL: "https://example.test/v1", ProxyMode: domain.ProxyCustom,
		ProxyURL: "http://other-user:other-pass@proxy.example:8080",
	})
	if err != nil || replaced.MaskedProxyURL != "http://****@proxy.example:8080" {
		t.Fatalf("proxy replacement failed: %+v err=%v", replaced, err)
	}
	cleared, err := service.UpdateManualProvider("proxied", domain.ManualProviderWrite{
		Name: "Proxied", CLI: domain.CLICodex, BaseURL: "https://example.test/v1", ProxyMode: domain.ProxyCustom,
		ClearProxyURL: true,
	})
	if err != nil || cleared.HasProxyURL || cleared.MaskedProxyURL != "" {
		t.Fatalf("proxy clear failed: %+v err=%v", cleared, err)
	}
	if _, err = service.Resolve(domain.CLICodex, "manual:proxied"); err == nil || !strings.Contains(err.Error(), "custom proxy URL") {
		t.Fatalf("custom provider without proxy URL should not resolve: %v", err)
	}
}

func TestManualProviderProxyValidationAndEnabledLifecycle(t *testing.T) {
	store := &memoryStore{}
	service := New(store, nil, "")
	for _, test := range []struct {
		name string
		mode domain.ProxyMode
		url  string
	}{
		{name: "missing custom URL", mode: domain.ProxyCustom},
		{name: "unsupported scheme", mode: domain.ProxyCustom, url: "ftp://proxy.example:21"},
		{name: "relative URL", mode: domain.ProxyCustom, url: "proxy.example:8080"},
		{name: "fragment", mode: domain.ProxyCustom, url: "http://proxy.example:8080/#secret"},
		{name: "invalid mode", mode: "sometimes", url: "http://proxy.example:8080"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.CreateManualProvider("invalid", domain.ManualProviderWrite{
				Name: "Invalid", CLI: domain.CLICodex, BaseURL: "https://example.test/v1", APIKey: "secret",
				ProxyMode: test.mode, ProxyURL: test.url,
			})
			if err == nil {
				t.Fatal("invalid proxy configuration was accepted")
			}
		})
	}
	created, err := service.CreateManualProvider("enabled", domain.ManualProviderWrite{
		Name: "Enabled", CLI: domain.CLIClaude, BaseURL: "https://example.test", APIKey: "secret",
		ProxyMode: domain.ProxyDirect,
	})
	if err != nil || !created.Enabled {
		t.Fatalf("provider should default to enabled: %+v err=%v", created, err)
	}
	disabled := false
	updated, err := service.UpdateManualProvider("enabled", domain.ManualProviderWrite{
		Name: "Enabled", CLI: domain.CLIClaude, BaseURL: "https://example.test", ProxyMode: domain.ProxyDirect,
		Enabled: &disabled,
	})
	providerState := service.Provider(updated)
	if err != nil || updated.Enabled || providerState.Available == nil || *providerState.Available {
		t.Fatalf("provider was not disabled: %+v err=%v", updated, err)
	}
	if _, err = service.Resolve(domain.CLIClaude, "manual:enabled"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled provider should not resolve: %v", err)
	}
	updated, err = service.UpdateManualProvider("enabled", domain.ManualProviderWrite{
		Name: "Enabled", CLI: domain.CLIClaude, BaseURL: "https://example.test", ProxyMode: domain.ProxyDirect,
	})
	if err != nil || updated.Enabled {
		t.Fatalf("omitted enabled flag should retain disabled state: %+v err=%v", updated, err)
	}
	enabled := true
	updated, err = service.UpdateManualProvider("enabled", domain.ManualProviderWrite{
		Name: "Enabled", CLI: domain.CLIClaude, BaseURL: "https://example.test", ProxyMode: domain.ProxyDirect,
		Enabled: &enabled,
	})
	providerState = service.Provider(updated)
	if err != nil || !updated.Enabled || providerState.Available == nil || !*providerState.Available {
		t.Fatalf("provider was not re-enabled: %+v err=%v", updated, err)
	}
}

func TestManualProviderValidationAndClearSecret(t *testing.T) {
	store := &memoryStore{}
	service := New(store, nil, "")
	if _, err := service.CreateManualProvider("bad", domain.ManualProviderWrite{Name: "Bad", CLI: domain.CLICodex, BaseURL: "https://example.test/v1"}); err == nil {
		t.Fatal("provider without API key should be rejected")
	}
	if _, err := service.CreateManualProvider("bad", domain.ManualProviderWrite{Name: "Bad", CLI: domain.CLICodex, BaseURL: "https://user:pass@example.test/v1", APIKey: "secret"}); err == nil {
		t.Fatal("provider URL credentials should be rejected")
	}
	_, err := service.CreateManualProvider("good", domain.ManualProviderWrite{Name: "Good", CLI: domain.CLIClaude, BaseURL: "https://example.test", APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := service.UpdateManualProvider("good", domain.ManualProviderWrite{Name: "Good", CLI: domain.CLIClaude, BaseURL: "https://example.test", ClearAPIKey: true})
	if err != nil || cleared.HasAPIKey {
		t.Fatalf("secret clear failed: %+v err=%v", cleared, err)
	}
	if _, err = service.Resolve(domain.CLIClaude, "manual:good"); err == nil {
		t.Fatal("provider without API key should not resolve")
	}
}

func TestDingTalkRedisPrecedenceClearAndSend(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()
	store := &memoryStore{}
	service := New(store, nil, "https://environment.example/robot?access_token=env-secret")
	config, err := service.EffectiveDingTalkConfig()
	if err != nil || config.Source != "environment" || strings.Contains(config.MaskedWebhook, "env-secret") {
		t.Fatalf("environment fallback was not masked: %+v err=%v", config, err)
	}
	config, err = service.SaveDingTalkConfig(domain.DingTalkConfigWrite{WebhookURL: server.URL + "/robot?access_token=redis-secret"})
	if err != nil || config.Source != "redis" || strings.Contains(config.MaskedWebhook, "redis-secret") {
		t.Fatalf("stored DingTalk config was not preferred: %+v err=%v", config, err)
	}
	if err = service.Send(context.Background(), "test", "content"); err != nil || requests != 1 {
		t.Fatalf("dynamic DingTalk send failed: requests=%d err=%v", requests, err)
	}
	config, err = service.SaveDingTalkConfig(domain.DingTalkConfigWrite{ClearStored: true})
	if err != nil || config.Source != "environment" {
		t.Fatalf("clear should reveal environment fallback: %+v err=%v", config, err)
	}
}

func TestImportEnvironmentDingTalkDoesNotOverwriteStoredConfig(t *testing.T) {
	store := &memoryStore{}
	service := New(store, nil, "https://environment.example/robot?access_token=env-secret")
	if err := service.ImportEnvironmentDingTalk(); err != nil {
		t.Fatal(err)
	}
	stored, err := store.LoadDingTalkConfig()
	if err != nil || stored.WebhookURL != "https://environment.example/robot?access_token=env-secret" || stored.Source != "redis" {
		t.Fatalf("environment webhook was not imported: %+v err=%v", stored, err)
	}
	stored.WebhookURL = "https://stored.example/robot?access_token=stored-secret"
	if _, err = store.SaveDingTalkConfig(stored); err != nil {
		t.Fatal(err)
	}
	if err = service.ImportEnvironmentDingTalk(); err != nil {
		t.Fatal(err)
	}
	stored, _ = store.LoadDingTalkConfig()
	if !strings.Contains(stored.WebhookURL, "stored-secret") {
		t.Fatalf("stored webhook was overwritten: %+v", stored)
	}
	if err = New(store, nil, "").ImportEnvironmentDingTalk(); err != nil {
		t.Fatalf("empty environment webhook should be a no-op: %v", err)
	}
}

func TestNotificationRoutingUsesDedicatedChannelAndFallsBack(t *testing.T) {
	var dedicatedCalls, defaultCalls int
	dedicated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dedicatedCalls++
		http.Error(w, "failed", http.StatusBadGateway)
	}))
	defer dedicated.Close()
	defaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defaultCalls++
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer defaultServer.Close()
	store := &memoryStore{providers: map[string]domain.ManualProvider{}, dingTalk: domain.DingTalkConfig{WebhookURL: defaultServer.URL, Configured: true, Source: "redis"}, channels: map[string]domain.NotificationChannel{"incidents": {ID: "incidents", Name: "Incidents", Type: "dingtalk", Enabled: true, Configured: true, WebhookURL: dedicated.URL}}, routes: domain.NotificationRoutes{Routes: map[string]string{"incident_opened": "incidents"}}}
	service := New(store, nil, "")
	if err := service.SendRouted(context.Background(), "incident_opened", "title", "content"); err != nil {
		t.Fatal(err)
	}
	if dedicatedCalls != 1 || defaultCalls != 1 {
		t.Fatalf("dedicated=%d default=%d", dedicatedCalls, defaultCalls)
	}
	store.channels["incidents"] = domain.NotificationChannel{ID: "incidents", Name: "Incidents", Type: "dingtalk", Enabled: true, Configured: true, WebhookURL: defaultServer.URL}
	if err := service.SendRouted(context.Background(), "incident_opened", "title", "content"); err != nil {
		t.Fatal(err)
	}
	if defaultCalls != 2 {
		t.Fatalf("same webhook was sent more than once: %d", defaultCalls)
	}
}
