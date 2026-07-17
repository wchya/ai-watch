package proxyconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ai-watch/internal/domain"
)

var (
	ErrInvalidSubscriptionURL  = errors.New("invalid Mihomo subscription URL")
	ErrEncryptionUnavailable   = errors.New("secure subscription encryption is unavailable")
	ErrMihomoUnavailable       = errors.New("Mihomo controller is unavailable")
	ErrSubscriptionUnavailable = errors.New("Mihomo subscription has no available nodes")
	ErrProxyTestFailed         = errors.New("proxy connectivity test failed")
	ErrConfigApplyFailed       = errors.New("apply Mihomo configuration failed")
	ErrRollbackFailed          = errors.New("restore previous Mihomo configuration failed")
)

type Store interface {
	LoadMihomoSubscription() (domain.MihomoSubscription, error)
	SaveMihomoSubscription(domain.MihomoSubscription) (domain.MihomoSubscription, error)
	ClearMihomoSubscription() (bool, error)
}

type GroupStatus struct {
	NodeCount   int
	CurrentNode string
}

type Controller interface {
	Reload(context.Context, string) error
	GroupStatus(context.Context, string) (GroupStatus, error)
}

type ConnectivityTester interface {
	Test(context.Context) error
}

type Options struct {
	RuntimePath           string
	RuntimeControllerPath string
	BaseControllerPath    string
	GroupName             string
	ProviderHealthURL     string
	ProviderHealthEvery   int
	GroupTestURL          string
	GroupTestEvery        int
	ReadyAttempts         int
	ReadyInterval         time.Duration
	Now                   func() time.Time
}

type Service struct {
	store       Store
	controller  Controller
	tester      ConnectivityTester
	options     Options
	operationMu sync.Mutex
	statusMu    sync.RWMutex
	lastStatus  domain.MihomoSubscriptionStatus
}

func New(store Store, controller Controller, tester ConnectivityTester, options Options) *Service {
	if options.RuntimePath == "" {
		options.RuntimePath = "/mihomo-config/runtime.yaml"
	}
	if options.RuntimeControllerPath == "" {
		options.RuntimeControllerPath = "/root/.config/mihomo/runtime.yaml"
	}
	if options.BaseControllerPath == "" {
		options.BaseControllerPath = "/root/.config/mihomo/config.yaml"
	}
	if options.GroupName == "" {
		options.GroupName = "PROXY"
	}
	if options.ProviderHealthURL == "" {
		options.ProviderHealthURL = "https://www.gstatic.com/generate_204"
	}
	if options.ProviderHealthEvery <= 0 {
		options.ProviderHealthEvery = 600
	}
	if options.GroupTestURL == "" {
		options.GroupTestURL = options.ProviderHealthURL
	}
	if options.GroupTestEvery <= 0 {
		options.GroupTestEvery = 300
	}
	if options.ReadyAttempts <= 0 {
		options.ReadyAttempts = 12
	}
	if options.ReadyInterval <= 0 {
		options.ReadyInterval = 500 * time.Millisecond
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: store, controller: controller, tester: tester, options: options}
}

func (s *Service) Status(ctx context.Context) domain.MihomoSubscriptionStatus {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	record, err := s.store.LoadMihomoSubscription()
	if err != nil {
		return s.statusWithError(domain.MihomoSubscription{}, "storage", "无法读取已保存的代理订阅")
	}
	status := s.baseStatus(record)
	if record.URL == "" {
		return s.mergeLastStatus(status)
	}
	group, err := s.controller.GroupStatus(ctx, s.options.GroupName)
	if err != nil {
		return s.statusWithError(record, "controller", "Mihomo 当前不可用")
	}
	if group.NodeCount == 0 {
		return s.statusWithError(record, "subscription", "当前没有可用代理节点")
	}
	status.Applied = group.NodeCount > 0
	status.NodeCount = group.NodeCount
	status.CurrentNode = group.CurrentNode
	now := s.options.Now()
	status.LastCheckedAt = &now
	s.setLastStatus(status)
	return status
}

func (s *Service) Apply(ctx context.Context, rawURL string) (domain.MihomoSubscriptionStatus, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	subscriptionURL, err := validateSubscriptionURL(rawURL)
	if err != nil {
		return s.fail(domain.MihomoSubscription{}, "validation", "订阅地址无效", ErrInvalidSubscriptionURL)
	}
	previous, err := s.store.LoadMihomoSubscription()
	if err != nil {
		return s.fail(domain.MihomoSubscription{}, "storage", "无法读取原订阅配置", fmt.Errorf("%w: read stored subscription", ErrConfigApplyFailed))
	}
	previousConfig, previousConfigExists, err := readOptionalFile(s.options.RuntimePath)
	if err != nil {
		return s.fail(previous, "write", "无法读取当前运行配置", fmt.Errorf("%w: read runtime configuration", ErrConfigApplyFailed))
	}
	configuration, err := s.generate(subscriptionURL)
	if err != nil {
		return s.fail(previous, "generate", "无法生成 Mihomo 配置", fmt.Errorf("%w: generate configuration", ErrConfigApplyFailed))
	}
	if err := writeAtomic(s.options.RuntimePath, configuration); err != nil {
		return s.fail(previous, "write", "无法写入 Mihomo 配置", fmt.Errorf("%w: write runtime configuration", ErrConfigApplyFailed))
	}
	if err := s.controller.Reload(ctx, s.options.RuntimeControllerPath); err != nil {
		return s.rollbackFailure(ctx, previous, previousConfig, previousConfigExists, "reload", "Mihomo 配置重载失败", ErrMihomoUnavailable)
	}
	group, err := s.waitForGroup(ctx)
	if err != nil {
		return s.rollbackFailure(ctx, previous, previousConfig, previousConfigExists, "subscription", "订阅未返回可用节点", ErrSubscriptionUnavailable)
	}
	if err := s.tester.Test(ctx); err != nil {
		return s.rollbackFailure(ctx, previous, previousConfig, previousConfigExists, "connectivity", "代理连通测试失败", ErrProxyTestFailed)
	}
	saved, err := s.store.SaveMihomoSubscription(domain.MihomoSubscription{URL: subscriptionURL})
	if err != nil {
		root := ErrConfigApplyFailed
		if strings.Contains(strings.ToLower(err.Error()), "encryption") {
			root = ErrEncryptionUnavailable
		}
		return s.rollbackFailure(ctx, previous, previousConfig, previousConfigExists, "storage", "订阅无法安全保存", root)
	}
	status := s.baseStatus(saved)
	status.Applied = true
	status.NodeCount = group.NodeCount
	status.CurrentNode = group.CurrentNode
	now := s.options.Now()
	status.LastCheckedAt = &now
	s.setLastStatus(status)
	return status, nil
}

func (s *Service) Clear(ctx context.Context) (domain.MihomoSubscriptionStatus, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()

	previous, err := s.store.LoadMihomoSubscription()
	if err != nil {
		return s.fail(domain.MihomoSubscription{}, "storage", "无法读取原订阅配置", fmt.Errorf("%w: read stored subscription", ErrConfigApplyFailed))
	}
	previousConfig, previousConfigExists, err := readOptionalFile(s.options.RuntimePath)
	if err != nil {
		return s.fail(previous, "write", "无法读取当前运行配置", fmt.Errorf("%w: read runtime configuration", ErrConfigApplyFailed))
	}
	if err := s.controller.Reload(ctx, s.options.BaseControllerPath); err != nil {
		return s.fail(previous, "reload", "无法恢复 Mihomo 基础配置", fmt.Errorf("%w: reload base configuration", ErrMihomoUnavailable))
	}
	if err := os.Remove(s.options.RuntimePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return s.rollbackFailure(ctx, previous, previousConfig, previousConfigExists, "write", "无法清除 Mihomo 运行配置", ErrConfigApplyFailed)
	}
	if _, err := s.store.ClearMihomoSubscription(); err != nil {
		return s.rollbackFailure(ctx, previous, previousConfig, previousConfigExists, "storage", "无法清除已保存订阅", ErrConfigApplyFailed)
	}
	status := domain.MihomoSubscriptionStatus{}
	now := s.options.Now()
	status.LastCheckedAt = &now
	s.setLastStatus(status)
	return status, nil
}

func (s *Service) Test(ctx context.Context) (domain.MihomoSubscriptionStatus, error) {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	record, err := s.store.LoadMihomoSubscription()
	if err != nil {
		return s.fail(domain.MihomoSubscription{}, "storage", "无法读取代理订阅", fmt.Errorf("%w: read stored subscription", ErrConfigApplyFailed))
	}
	group, err := s.controller.GroupStatus(ctx, s.options.GroupName)
	if err != nil {
		return s.fail(record, "controller", "Mihomo 当前不可用", ErrMihomoUnavailable)
	}
	if record.URL != "" && group.NodeCount == 0 {
		return s.fail(record, "subscription", "当前没有可用代理节点", ErrSubscriptionUnavailable)
	}
	if err := s.tester.Test(ctx); err != nil {
		return s.fail(record, "connectivity", "代理连通测试失败", ErrProxyTestFailed)
	}
	status := s.baseStatus(record)
	status.Applied = record.URL != ""
	status.NodeCount = group.NodeCount
	status.CurrentNode = group.CurrentNode
	now := s.options.Now()
	status.LastCheckedAt = &now
	s.setLastStatus(status)
	return status, nil
}

func (s *Service) Restore(ctx context.Context) error {
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	record, err := s.store.LoadMihomoSubscription()
	if err != nil || record.URL == "" {
		return err
	}
	configuration, err := s.generate(record.URL)
	if err != nil {
		_, err = s.fail(record, "generate", "无法恢复订阅配置", fmt.Errorf("%w: generate startup configuration", ErrConfigApplyFailed))
		return err
	}
	if err := writeAtomic(s.options.RuntimePath, configuration); err != nil {
		_, err = s.fail(record, "write", "无法恢复订阅配置", fmt.Errorf("%w: write startup configuration", ErrConfigApplyFailed))
		return err
	}
	if err := s.controller.Reload(ctx, s.options.RuntimeControllerPath); err != nil {
		return s.restoreStartupFailure(ctx, record, "reload", "Mihomo 启动恢复失败", ErrMihomoUnavailable)
	}
	group, err := s.waitForGroup(ctx)
	if err != nil {
		return s.restoreStartupFailure(ctx, record, "subscription", "订阅启动恢复后没有可用节点", ErrSubscriptionUnavailable)
	}
	if err := s.tester.Test(ctx); err != nil {
		return s.restoreStartupFailure(ctx, record, "connectivity", "订阅启动恢复后的代理连通测试失败", ErrProxyTestFailed)
	}
	status := s.baseStatus(record)
	status.Applied = true
	status.NodeCount = group.NodeCount
	status.CurrentNode = group.CurrentNode
	now := s.options.Now()
	status.LastCheckedAt = &now
	s.setLastStatus(status)
	return nil
}

func (s *Service) restoreStartupFailure(ctx context.Context, record domain.MihomoSubscription, stage, message string, root error) error {
	if err := s.restorePrevious(ctx, domain.MihomoSubscription{}, nil, false); err != nil {
		_, result := s.fail(record, "rollback", "订阅启动恢复失败，且基础配置恢复失败", fmt.Errorf("%w: %v", ErrRollbackFailed, err))
		return result
	}
	_, result := s.fail(record, stage, message, root)
	return result
}

func (s *Service) generate(subscriptionURL string) ([]byte, error) {
	cachePath := fmt.Sprintf("./providers/subscription-%d.yaml", s.options.Now().UnixNano())
	configuration := map[string]any{
		"mixed-port":          7890,
		"socks-port":          7891,
		"allow-lan":           true,
		"bind-address":        "*",
		"mode":                "rule",
		"log-level":           "info",
		"external-controller": "0.0.0.0:9090",
		"secret":              "",
		"proxy-providers": map[string]any{
			"subscription": map[string]any{
				"type": "http", "url": subscriptionURL, "path": cachePath, "interval": 3600,
				"health-check": map[string]any{"enable": true, "url": s.options.ProviderHealthURL, "interval": s.options.ProviderHealthEvery},
			},
		},
		"proxy-groups": []any{map[string]any{
			"name": s.options.GroupName, "type": "url-test", "use": []string{"subscription"}, "url": s.options.GroupTestURL, "interval": s.options.GroupTestEvery,
		}},
		"rules": []string{"MATCH," + s.options.GroupName},
	}
	return json.MarshalIndent(configuration, "", "  ")
}

func (s *Service) waitForGroup(ctx context.Context) (GroupStatus, error) {
	var lastErr error
	for attempt := 0; attempt < s.options.ReadyAttempts; attempt++ {
		group, err := s.controller.GroupStatus(ctx, s.options.GroupName)
		if err == nil && group.NodeCount > 0 {
			return group, nil
		}
		lastErr = err
		if attempt+1 == s.options.ReadyAttempts {
			break
		}
		timer := time.NewTimer(s.options.ReadyInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return GroupStatus{}, ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr != nil {
		return GroupStatus{}, lastErr
	}
	return GroupStatus{}, ErrSubscriptionUnavailable
}

func (s *Service) rollbackFailure(ctx context.Context, previous domain.MihomoSubscription, previousConfig []byte, previousConfigExists bool, stage, message string, root error) (domain.MihomoSubscriptionStatus, error) {
	if err := s.restorePrevious(ctx, previous, previousConfig, previousConfigExists); err != nil {
		return s.fail(previous, "rollback", "新配置失败，且旧配置恢复失败", fmt.Errorf("%w: %v", ErrRollbackFailed, err))
	}
	return s.fail(previous, stage, message, root)
}

func (s *Service) restorePrevious(ctx context.Context, previous domain.MihomoSubscription, previousConfig []byte, previousConfigExists bool) error {
	if previous.URL != "" && previousConfigExists {
		if err := writeAtomic(s.options.RuntimePath, previousConfig); err != nil {
			return err
		}
		return s.controller.Reload(ctx, s.options.RuntimeControllerPath)
	}
	if previous.URL != "" {
		configuration, err := s.generate(previous.URL)
		if err != nil {
			return err
		}
		if err := writeAtomic(s.options.RuntimePath, configuration); err != nil {
			return err
		}
		return s.controller.Reload(ctx, s.options.RuntimeControllerPath)
	}
	_ = os.Remove(s.options.RuntimePath)
	return s.controller.Reload(ctx, s.options.BaseControllerPath)
}

func (s *Service) baseStatus(record domain.MihomoSubscription) domain.MihomoSubscriptionStatus {
	status := domain.MihomoSubscriptionStatus{Configured: record.URL != "", MaskedURL: maskSubscriptionURL(record.URL)}
	if !record.UpdatedAt.IsZero() {
		updated := record.UpdatedAt
		status.UpdatedAt = &updated
	}
	return status
}

func (s *Service) fail(record domain.MihomoSubscription, stage, message string, err error) (domain.MihomoSubscriptionStatus, error) {
	status := s.statusWithError(record, stage, message)
	return status, err
}

func (s *Service) statusWithError(record domain.MihomoSubscription, stage, message string) domain.MihomoSubscriptionStatus {
	status := s.baseStatus(record)
	status.ErrorStage = stage
	status.ErrorMessage = message
	now := s.options.Now()
	status.LastCheckedAt = &now
	s.setLastStatus(status)
	return status
}

func (s *Service) mergeLastStatus(status domain.MihomoSubscriptionStatus) domain.MihomoSubscriptionStatus {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	if s.lastStatus.Configured == status.Configured && s.lastStatus.MaskedURL == status.MaskedURL {
		status.Applied = s.lastStatus.Applied
		status.NodeCount = s.lastStatus.NodeCount
		status.CurrentNode = s.lastStatus.CurrentNode
		status.LastCheckedAt = s.lastStatus.LastCheckedAt
		status.ErrorStage = s.lastStatus.ErrorStage
		status.ErrorMessage = s.lastStatus.ErrorMessage
	}
	return status
}

func (s *Service) setLastStatus(status domain.MihomoSubscriptionStatus) {
	s.statusMu.Lock()
	s.lastStatus = status
	s.statusMu.Unlock()
}

func validateSubscriptionURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 4096 {
		return "", ErrInvalidSubscriptionURL
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Fragment != "" {
		return "", ErrInvalidSubscriptionURL
	}
	return parsed.String(), nil
}

func maskSubscriptionURL(value string) string {
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host + "/..."
}

func readOptionalFile(path string) ([]byte, bool, error) {
	value, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return value, err == nil, err
}

func writeAtomic(path string, value []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".runtime-*.yaml")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(value); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
