package secureconfig

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"strings"
	"time"

	"ai-watch/internal/domain"
	"ai-watch/internal/notify"
	"ai-watch/internal/security"
)

const ManualProviderPrefix = "manual:"

var manualProviderID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
var providerName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var (
	ErrInvalidManualProvider = errors.New("invalid manual provider")
	ErrEncryptionUnavailable = errors.New("secure configuration encryption is unavailable")
)

type Store interface {
	ListManualProviders() ([]domain.ManualProvider, error)
	GetManualProvider(string) (domain.ManualProvider, error)
	UpsertManualProvider(domain.ManualProvider) (domain.ManualProvider, error)
	DeleteManualProvider(string) (bool, error)
	LoadDingTalkConfig() (domain.DingTalkConfig, error)
	SaveDingTalkConfig(domain.DingTalkConfig) (domain.DingTalkConfig, error)
	ClearDingTalkConfig() (bool, error)
}

type ccSwitchStore interface {
	ListCCSwitchProviders() ([]domain.CCSwitchProvider, error)
	GetCCSwitchProvider(string) (domain.CCSwitchProvider, error)
}

type Resolver interface {
	Resolve(domain.CLI, string) (domain.ResolvedConfig, error)
}

type Service struct {
	store      Store
	fallback   Resolver
	envWebhook string
}

func New(store Store, fallback Resolver, envWebhook string) *Service {
	return &Service{store: store, fallback: fallback, envWebhook: strings.TrimSpace(envWebhook)}
}

func (s *Service) Resolve(cli domain.CLI, providerID string) (domain.ResolvedConfig, error) {
	if providerID == "" || providerID == "current" {
		if s.fallback == nil {
			return domain.ResolvedConfig{}, errors.New("provider resolver is unavailable")
		}
		return s.fallback.Resolve(cli, providerID)
	}
	if !strings.HasPrefix(providerID, ManualProviderPrefix) {
		if store, ok := s.store.(ccSwitchStore); ok {
			return resolveCCSwitchProvider(store, cli, providerID)
		}
		if s.fallback == nil {
			return domain.ResolvedConfig{}, errors.New("CC Switch Redis provider store is unavailable")
		}
		return s.fallback.Resolve(cli, providerID)
	}
	if s.store == nil {
		return domain.ResolvedConfig{}, ErrEncryptionUnavailable
	}
	id := strings.TrimPrefix(providerID, ManualProviderPrefix)
	value, err := s.store.GetManualProvider(id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return domain.ResolvedConfig{}, errors.New("manual provider not found")
		}
		return domain.ResolvedConfig{}, fmt.Errorf("read manual provider: %w", err)
	}
	if value.CLI != cli {
		return domain.ResolvedConfig{}, errors.New("manual provider belongs to another CLI")
	}
	if !value.Enabled {
		return domain.ResolvedConfig{}, errors.New("manual provider is disabled")
	}
	if value.APIKey == "" {
		return domain.ResolvedConfig{}, errors.New("manual provider API key is not configured")
	}
	proxyMode, err := normalizeProxyMode(value.ProxyMode)
	if err != nil {
		return domain.ResolvedConfig{}, err
	}
	if proxyMode == domain.ProxyCustom && value.ProxyURL == "" {
		return domain.ResolvedConfig{}, errors.New("manual provider custom proxy URL is not configured")
	}
	provider := value.Provider
	if provider == "" {
		if cli == domain.CLICodex {
			provider = "openai"
		} else {
			provider = "anthropic-compatible"
		}
	}
	resolved := domain.ResolvedConfig{
		Source:       "manual",
		ProviderID:   ManualProviderPrefix + value.ID,
		ProviderName: value.Name,
		Provider:     provider,
		Model:        value.Model,
		BaseURL:      value.BaseURL,
		APIKey:       value.APIKey,
		LockIdentity: value.APIKey,
		APIKeySource: "encrypted manual provider",
		ProxyMode:    proxyMode,
		ProxyURL:     value.ProxyURL,
	}
	if cli == domain.CLICodex {
		resolved.CodexConfig = codexConfig(provider, value.Model, value.BaseURL)
	} else {
		resolved.ClaudeEnv = map[string]string{
			"ANTHROPIC_BASE_URL":   value.BaseURL,
			"ANTHROPIC_AUTH_TOKEN": value.APIKey,
			"ANTHROPIC_API_KEY":    value.APIKey,
			"ANTHROPIC_MODEL":      value.Model,
		}
	}
	return resolved, nil
}

func resolveCCSwitchProvider(store ccSwitchStore, cli domain.CLI, id string) (domain.ResolvedConfig, error) {
	value, err := store.GetCCSwitchProvider(strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return domain.ResolvedConfig{}, errors.New("CC Switch Redis provider not found")
		}
		return domain.ResolvedConfig{}, fmt.Errorf("read CC Switch Redis provider: %w", err)
	}
	if value.CLI != cli {
		return domain.ResolvedConfig{}, errors.New("CC Switch Redis provider belongs to another CLI")
	}
	if value.APIKey == "" {
		return domain.ResolvedConfig{}, errors.New("CC Switch Redis provider API key is not configured")
	}
	provider := value.Provider
	if provider == "" {
		if cli == domain.CLICodex {
			provider = "openai"
		} else {
			provider = "anthropic-compatible"
		}
	}
	resolved := domain.ResolvedConfig{
		Source:       "cc-switch-redis",
		ProviderID:   value.ID,
		ProviderName: value.Name,
		Provider:     provider,
		Model:        value.Model,
		BaseURL:      value.BaseURL,
		APIKey:       value.APIKey,
		LockIdentity: value.APIKey,
		APIKeySource: "encrypted CC Switch Redis snapshot",
		ProxyMode:    domain.ProxyDefault,
	}
	if cli == domain.CLICodex {
		if value.CodexConfig == "" {
			return domain.ResolvedConfig{}, errors.New("CC Switch Redis Codex provider config is not available")
		}
		resolved.CodexConfig = value.CodexConfig
	} else {
		resolved.ClaudeEnv = cloneStringMap(value.ClaudeEnv)
		if resolved.BaseURL == "" {
			resolved.BaseURL = resolved.ClaudeEnv["ANTHROPIC_BASE_URL"]
		}
	}
	return resolved, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (s *Service) ListCCSwitchProviders() ([]domain.Provider, error) {
	store, ok := s.store.(ccSwitchStore)
	if !ok {
		return []domain.Provider{}, nil
	}
	values, err := store.ListCCSwitchProviders()
	if err != nil {
		return nil, err
	}
	result := make([]domain.Provider, 0, len(values))
	for _, value := range values {
		available := value.APIKey != "" && ((value.CLI == domain.CLICodex && value.CodexConfig != "") || (value.CLI == domain.CLIClaude && value.BaseURL != ""))
		result = append(result, domain.Provider{
			ID: value.ID, Name: value.Name, CLI: value.CLI, Current: value.Current,
			Model: value.Model, BaseURL: value.BaseURL, MaskedKey: security.Mask(value.APIKey),
			Available: &available,
		})
	}
	return result, nil
}

func (s *Service) ListManualProviders() ([]domain.ManualProvider, error) {
	if s.store == nil {
		return nil, ErrEncryptionUnavailable
	}
	values, err := s.store.ListManualProviders()
	if err != nil {
		return nil, err
	}
	for index := range values {
		values[index] = publicManualProvider(values[index])
	}
	return values, nil
}

func (s *Service) GetManualProvider(id string) (domain.ManualProvider, error) {
	if s.store == nil {
		return domain.ManualProvider{}, ErrEncryptionUnavailable
	}
	value, err := s.store.GetManualProvider(cleanManualID(id))
	if err != nil {
		return domain.ManualProvider{}, err
	}
	return publicManualProvider(value), nil
}

func (s *Service) CreateManualProvider(id string, write domain.ManualProviderWrite) (domain.ManualProvider, error) {
	id = cleanManualID(id)
	if id == "" {
		id = "provider-" + randomHex(8)
	}
	write.APIKey = strings.TrimSpace(write.APIKey)
	if write.APIKey == "" || write.ClearAPIKey {
		return domain.ManualProvider{}, fmt.Errorf("%w: apiKey is required when creating a provider", ErrInvalidManualProvider)
	}
	proxyMode, err := normalizeProxyMode(write.ProxyMode)
	if err != nil {
		return domain.ManualProvider{}, err
	}
	enabled := true
	if write.Enabled != nil {
		enabled = *write.Enabled
	}
	value := domain.ManualProvider{
		ID: id, APIKey: write.APIKey, Enabled: enabled, ProxyMode: proxyMode,
		ProxyURL: strings.TrimSpace(write.ProxyURL),
	}
	if write.ClearProxyURL {
		value.ProxyURL = ""
		value.ClearProxyURL = true
	}
	return s.saveManualProvider(value, write, true)
}

func (s *Service) UpdateManualProvider(id string, write domain.ManualProviderWrite) (domain.ManualProvider, error) {
	if s.store == nil {
		return domain.ManualProvider{}, ErrEncryptionUnavailable
	}
	id = cleanManualID(id)
	current, err := s.store.GetManualProvider(id)
	if err != nil {
		return domain.ManualProvider{}, err
	}
	if write.ClearAPIKey {
		current.APIKey = ""
		current.ClearAPIKey = true
	} else if strings.TrimSpace(write.APIKey) != "" {
		current.APIKey = strings.TrimSpace(write.APIKey)
		current.ClearAPIKey = false
	}
	proxyMode := current.ProxyMode
	if write.ProxyMode != "" {
		proxyMode, err = normalizeProxyMode(write.ProxyMode)
		if err != nil {
			return domain.ManualProvider{}, err
		}
	} else if proxyMode == "" {
		proxyMode = domain.ProxyDefault
	}
	current.ProxyMode = proxyMode
	if write.Enabled != nil {
		current.Enabled = *write.Enabled
	}
	if write.ClearProxyURL {
		current.ProxyURL = ""
		current.ClearProxyURL = true
	} else if strings.TrimSpace(write.ProxyURL) != "" {
		current.ProxyURL = strings.TrimSpace(write.ProxyURL)
		current.ClearProxyURL = false
	}
	return s.saveManualProvider(current, write, false)
}

func (s *Service) DeleteManualProvider(id string) (bool, error) {
	if s.store == nil {
		return false, ErrEncryptionUnavailable
	}
	return s.store.DeleteManualProvider(cleanManualID(id))
}

func (s *Service) saveManualProvider(value domain.ManualProvider, write domain.ManualProviderWrite, creating bool) (domain.ManualProvider, error) {
	if s.store == nil {
		return domain.ManualProvider{}, ErrEncryptionUnavailable
	}
	value.ID = cleanManualID(value.ID)
	value.Name = strings.TrimSpace(write.Name)
	value.CLI = write.CLI
	value.BaseURL = strings.TrimSpace(write.BaseURL)
	value.Model = strings.TrimSpace(write.Model)
	value.Provider = strings.TrimSpace(write.Provider)
	if err := validateManualProvider(value, creating); err != nil {
		return domain.ManualProvider{}, err
	}
	saved, err := s.store.UpsertManualProvider(value)
	if err != nil {
		return domain.ManualProvider{}, err
	}
	return publicManualProvider(saved), nil
}

func (s *Service) Provider(value domain.ManualProvider) domain.Provider {
	proxyReady := value.ProxyMode != domain.ProxyCustom || value.HasProxyURL || value.ProxyURL != ""
	enabled := value.Enabled
	available := enabled && (value.HasAPIKey || value.APIKey != "") && proxyReady
	return domain.Provider{
		ID:        ManualProviderPrefix + value.ID,
		Name:      value.Name,
		CLI:       value.CLI,
		Model:     value.Model,
		BaseURL:   value.BaseURL,
		MaskedKey: value.MaskedKey,
		Enabled:   &enabled,
		Available: &available,
	}
}

// ImportEnvironmentDingTalk persists the environment fallback once so later
// runtime configuration no longer depends on the process environment. Existing
// stored configuration always wins and is never overwritten.
func (s *Service) ImportEnvironmentDingTalk() error {
	if s.envWebhook == "" {
		return nil
	}
	if s.store == nil {
		return ErrEncryptionUnavailable
	}
	stored, err := s.store.LoadDingTalkConfig()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if stored.WebhookURL != "" {
		return nil
	}
	if err = validateWebhook(s.envWebhook); err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.store.SaveDingTalkConfig(domain.DingTalkConfig{
		WebhookURL: s.envWebhook, Configured: true, Source: "redis", UpdatedAt: &now,
	})
	return err
}

func (s *Service) EffectiveDingTalkConfig() (domain.DingTalkConfig, error) {
	if s.store != nil {
		stored, err := s.store.LoadDingTalkConfig()
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return domain.DingTalkConfig{}, err
		}
		if stored.WebhookURL != "" {
			stored.Configured = true
			stored.Source = "redis"
			stored.MaskedWebhook = maskWebhook(stored.WebhookURL)
			return stored, nil
		}
	}
	if s.envWebhook != "" {
		return domain.DingTalkConfig{Configured: true, Source: "environment", MaskedWebhook: maskWebhook(s.envWebhook)}, nil
	}
	return domain.DingTalkConfig{Source: "none"}, nil
}

func (s *Service) SaveDingTalkConfig(write domain.DingTalkConfigWrite) (domain.DingTalkConfig, error) {
	if s.store == nil {
		return domain.DingTalkConfig{}, ErrEncryptionUnavailable
	}
	if write.ClearStored {
		if _, err := s.store.ClearDingTalkConfig(); err != nil {
			return domain.DingTalkConfig{}, err
		}
		return s.EffectiveDingTalkConfig()
	}
	webhook := strings.TrimSpace(write.WebhookURL)
	if err := validateWebhook(webhook); err != nil {
		return domain.DingTalkConfig{}, err
	}
	now := time.Now().UTC()
	if _, err := s.store.SaveDingTalkConfig(domain.DingTalkConfig{WebhookURL: webhook, Configured: true, Source: "redis", UpdatedAt: &now}); err != nil {
		return domain.DingTalkConfig{}, err
	}
	return s.EffectiveDingTalkConfig()
}

func (s *Service) Configured() bool {
	config, err := s.EffectiveDingTalkConfig()
	return err == nil && config.Configured
}

func (s *Service) Send(ctx context.Context, title, content string) error {
	webhook, err := s.effectiveWebhook()
	if err != nil {
		return err
	}
	return notify.New(webhook).Send(ctx, title, content)
}

func (s *Service) Notify(ctx context.Context, job domain.Job, attempt domain.AttemptStatus) error {
	webhook, err := s.effectiveWebhook()
	if err != nil {
		return err
	}
	return notify.New(webhook).Notify(ctx, job, attempt)
}

func (s *Service) effectiveWebhook() (string, error) {
	config, err := s.EffectiveDingTalkConfig()
	if err != nil {
		return "", err
	}
	if !config.Configured {
		return "", errors.New("DingTalk webhook is not configured")
	}
	if config.Source == "environment" {
		return s.envWebhook, nil
	}
	stored, err := s.store.LoadDingTalkConfig()
	if err != nil {
		return "", err
	}
	return stored.WebhookURL, nil
}

func validateManualProvider(value domain.ManualProvider, creating bool) error {
	if !manualProviderID.MatchString(value.ID) {
		return fmt.Errorf("%w: id must use lowercase letters, numbers, dot, underscore, or hyphen", ErrInvalidManualProvider)
	}
	if value.Name == "" || len(value.Name) > 160 {
		return fmt.Errorf("%w: name is required and must not exceed 160 bytes", ErrInvalidManualProvider)
	}
	if value.CLI != domain.CLICodex && value.CLI != domain.CLIClaude {
		return fmt.Errorf("%w: cli must be codex or claude", ErrInvalidManualProvider)
	}
	parsed, err := url.Parse(value.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%w: baseUrl must be an absolute HTTP(S) URL", ErrInvalidManualProvider)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%w: baseUrl must not contain credentials, query parameters, or fragments", ErrInvalidManualProvider)
	}
	if len(value.BaseURL) > 2048 || len(value.Model) > 256 || len(value.Provider) > 160 {
		return fmt.Errorf("%w: provider field is too long", ErrInvalidManualProvider)
	}
	if value.Provider != "" && !providerName.MatchString(value.Provider) {
		return fmt.Errorf("%w: provider must use letters, numbers, underscore, or hyphen", ErrInvalidManualProvider)
	}
	if creating && value.APIKey == "" {
		return fmt.Errorf("%w: apiKey is required", ErrInvalidManualProvider)
	}
	if len(value.APIKey) > 8192 {
		return fmt.Errorf("%w: apiKey is too long", ErrInvalidManualProvider)
	}
	proxyMode, err := normalizeProxyMode(value.ProxyMode)
	if err != nil {
		return err
	}
	if value.ProxyURL != "" {
		if err = validateProxyURL(value.ProxyURL); err != nil {
			return err
		}
	}
	if creating && proxyMode == domain.ProxyCustom && value.ProxyURL == "" {
		return fmt.Errorf("%w: proxyUrl is required when proxyMode is custom", ErrInvalidManualProvider)
	}
	return nil
}

func normalizeProxyMode(value domain.ProxyMode) (domain.ProxyMode, error) {
	switch value {
	case "", domain.ProxyDefault:
		return domain.ProxyDefault, nil
	case domain.ProxyDirect, domain.ProxyCustom:
		return value, nil
	default:
		return "", fmt.Errorf("%w: proxyMode must be default, direct, or custom", ErrInvalidManualProvider)
	}
}

func validateProxyURL(value string) error {
	parsed, err := url.Parse(value)
	if value == "" || err != nil || parsed.Host == "" {
		return fmt.Errorf("%w: proxyUrl must be an absolute proxy URL", ErrInvalidManualProvider)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("%w: proxyUrl must use http, https, socks5, or socks5h", ErrInvalidManualProvider)
	}
	if parsed.Fragment != "" || len(value) > 4096 {
		return fmt.Errorf("%w: invalid proxyUrl", ErrInvalidManualProvider)
	}
	return nil
}

func validateWebhook(value string) error {
	parsed, err := url.Parse(value)
	if value == "" || err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("webhookUrl must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.Fragment != "" || len(value) > 4096 {
		return errors.New("invalid DingTalk webhook URL")
	}
	return nil
}

func publicManualProvider(value domain.ManualProvider) domain.ManualProvider {
	if value.ProxyMode == "" {
		value.ProxyMode = domain.ProxyDefault
	}
	value.HasAPIKey = value.APIKey != ""
	value.MaskedKey = security.Mask(value.APIKey)
	value.HasProxyURL = value.ProxyURL != ""
	value.MaskedProxyURL = maskProxyURL(value.ProxyURL)
	value.APIKey = ""
	value.ProxyURL = ""
	value.ClearAPIKey = false
	value.ClearProxyURL = false
	return value
}

func cleanManualID(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ManualProviderPrefix)
}

func NormalizeManualProviderID(value string) string { return cleanManualID(value) }

func maskWebhook(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "configured"
	}
	masked := parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath()
	if parsed.RawQuery != "" {
		masked += "?access_token=****"
	}
	return masked
}

func maskProxyURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		if value == "" {
			return ""
		}
		return "configured"
	}
	masked := parsed.Scheme + "://"
	if parsed.User != nil {
		masked += "****@"
	}
	return masked + parsed.Host
}

func codexConfig(provider, model, baseURL string) string {
	return fmt.Sprintf("model_provider = %q\nmodel = %q\n\n[model_providers.%s]\nname = %q\nwire_api = \"responses\"\nrequires_openai_auth = true\nbase_url = %q\n", provider, model, provider, provider, baseURL)
}

func randomHex(size int) string {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%x", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(buffer)
}
