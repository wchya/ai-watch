package diagnostics

import (
	"bytes"
	"context"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ai-watch/internal/configscan"
	"ai-watch/internal/jobs"
	"ai-watch/internal/security"
	"ai-watch/internal/store"
)

const (
	commandTimeout    = 2 * time.Second
	maxVersionBytes   = 1024
	maxVersionRunes   = 160
	activeJobsLimit   = 8
	defaultRuntimeDir = "/run/ai-watch"
)

type Service struct {
	scanner    *configscan.Scanner
	jobs       *jobs.Manager
	store      store.Store
	runtimeDir string
	checkCLI   func(context.Context, string, string) CLIStatus
	now        func() time.Time
}

type Snapshot struct {
	Status       string                    `json:"status"`
	GeneratedAt  time.Time                 `json:"generatedAt"`
	CLIs         []CLIStatus               `json:"clis"`
	Storage      StorageStatus             `json:"storage"`
	Proxy        ProxyStatus               `json:"proxy"`
	CCSwitchSync *store.CCSwitchSyncStatus `json:"ccSwitchSync,omitempty"`
	Runtime      RuntimeStatus             `json:"runtime"`
	Config       ConfigLifecycle           `json:"config"`
}

type CLIStatus struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Available  bool   `json:"available"`
	PathLabel  string `json:"pathLabel,omitempty"`
	Version    string `json:"version,omitempty"`
	CheckState string `json:"checkState"`
}

type StorageStatus struct {
	Available     bool   `json:"available"`
	Backend       string `json:"backend"`
	SchemaVersion int    `json:"schemaVersion"`
	LogicalBytes  int64  `json:"logicalBytes"`
	EventCount    int64  `json:"eventCount"`
	ScheduleCount int64  `json:"scheduleCount"`
}

type ProxyStatus struct {
	Configured bool   `json:"configured"`
	Available  bool   `json:"available"`
	Endpoint   string `json:"endpoint,omitempty"`
	CheckState string `json:"checkState"`
}

type RuntimeStatus struct {
	ActiveJobs       int  `json:"activeJobs"`
	ActiveJobsLimit  int  `json:"activeJobsLimit"`
	DirectoryEntries int  `json:"directoryEntries"`
	DirectoryReady   bool `json:"directoryReady"`
}

type ConfigLifecycle struct {
	HotReload       []ConfigField `json:"hotReload"`
	RestartRequired []ConfigField `json:"restartRequired"`
}

type ConfigField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

func New(scanner *configscan.Scanner, manager *jobs.Manager, st store.Store, runtimeDir string) *Service {
	if runtimeDir == "" {
		runtimeDir = os.Getenv("AI_WATCH_RUNTIME_DIR")
	}
	if runtimeDir == "" {
		runtimeDir = defaultRuntimeDir
	}
	return &Service{
		scanner: scanner, jobs: manager, store: st, runtimeDir: runtimeDir,
		checkCLI: checkCLI, now: func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) Snapshot(ctx context.Context) Snapshot {
	result := Snapshot{
		Status: "ok", GeneratedAt: s.now(), Config: configLifecycle(),
		Runtime: RuntimeStatus{ActiveJobsLimit: activeJobsLimit},
	}
	if s.scanner == nil {
		result.CLIs = []CLIStatus{
			{ID: "codex", Name: "Codex CLI", CheckState: "unavailable"},
			{ID: "claude", Name: "Claude Code CLI", CheckState: "unavailable"},
		}
		result.Status = "degraded"
	} else {
		result.CLIs = []CLIStatus{
			s.checkCLI(ctx, "codex", s.scanner.CodexBin),
			s.checkCLI(ctx, "claude", s.scanner.ClaudeBin),
		}
		result.CLIs[0].Name = "Codex CLI"
		result.CLIs[1].Name = "Claude Code CLI"
		for _, item := range result.CLIs {
			if !item.Available || item.CheckState != "ok" {
				result.Status = "degraded"
			}
		}
	}

	if s.store != nil {
		if stats, err := s.store.Diagnostics(); err == nil {
			result.Storage = StorageStatus{
				Available: true, Backend: stats.Backend, SchemaVersion: stats.SchemaVersion,
				LogicalBytes: stats.LogicalBytes, EventCount: stats.EventCount,
				ScheduleCount: stats.ScheduleCount,
			}
		} else {
			result.Status = "degraded"
		}
		if statusStore, ok := s.store.(interface {
			LoadCCSwitchSyncStatus() (store.CCSwitchSyncStatus, error)
		}); ok {
			if syncStatus, syncErr := statusStore.LoadCCSwitchSyncStatus(); syncErr == nil {
				result.CCSwitchSync = &syncStatus
				if syncStatus.Warning != "" && (syncStatus.SourceAvailable || syncStatus.LastSuccessAt != nil) {
					result.Status = "degraded"
				}
			} else {
				result.Status = "degraded"
			}
		}
	} else {
		result.Status = "degraded"
	}

	if s.jobs != nil {
		result.Runtime.ActiveJobs = s.jobs.ActiveCount()
	}
	result.Proxy = checkProxy(ctx, os.Getenv("AI_WATCH_DEFAULT_PROXY_URL"))
	if result.Proxy.Configured && !result.Proxy.Available {
		result.Status = "degraded"
	}
	entries, ready := runtimeEntries(filepath.Join(s.runtimeDir, "jobs"))
	result.Runtime.DirectoryEntries = entries
	result.Runtime.DirectoryReady = ready
	if !ready {
		result.Status = "degraded"
	}
	return result
}

func checkProxy(ctx context.Context, rawURL string) ProxyStatus {
	status := ProxyStatus{CheckState: "not_configured"}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		if strings.TrimSpace(rawURL) != "" {
			status.Configured = true
			status.CheckState = "invalid"
		}
		return status
	}
	status.Configured = true
	status.Endpoint = parsed.Host
	status.CheckState = "unavailable"
	checkCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(checkCtx, "tcp", parsed.Host)
	if err != nil {
		if checkCtx.Err() != nil {
			status.CheckState = "timeout"
		}
		return status
	}
	_ = connection.Close()
	status.Available = true
	status.CheckState = "ok"
	return status
}

func checkCLI(ctx context.Context, id, binary string) CLIStatus {
	status := CLIStatus{ID: id, CheckState: "unavailable"}
	path, err := exec.LookPath(binary)
	if err != nil {
		return status
	}
	status.Available = true
	status.PathLabel = safePathLabel(path, id)
	checkCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	var output limitedBuffer
	cmd := exec.CommandContext(checkCtx, path, "--version")
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	if checkCtx.Err() != nil {
		status.CheckState = "timeout"
		return status
	}
	if err != nil {
		status.CheckState = "version_unreadable"
		return status
	}
	status.Version = sanitizeVersion(output.String())
	if status.Version == "" {
		status.CheckState = "version_unreadable"
		return status
	}
	status.CheckState = "ok"
	return status
}

func safePathLabel(path, fallback string) string {
	label := filepath.Base(strings.TrimSpace(path))
	if label == "" || label == "." || label == string(filepath.Separator) {
		return fallback
	}
	if len(label) > 80 {
		return fallback
	}
	for _, r := range label {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-+", r)) {
			return fallback
		}
	}
	return label
}

func sanitizeVersion(value string) string {
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, line)
		if utf8.RuneCountInString(line) > maxVersionRunes {
			line = string([]rune(line)[:maxVersionRunes])
		}
		line = security.Redact(line)
		if strings.Contains(line, "[REDACTED]") {
			continue
		}
		safe := true
		for _, r := range line {
			if !(unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || strings.ContainsRune("._-+()", r)) {
				safe = false
				break
			}
		}
		if safe {
			return line
		}
	}
	return ""
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := maxVersionBytes - b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return original, nil
}

func runtimeEntries(path string) (int, bool) {
	entries, err := os.ReadDir(path)
	if err == nil {
		return len(entries), true
	}
	if os.IsNotExist(err) {
		return 0, false
	}
	return 0, false
}

func configLifecycle() ConfigLifecycle {
	return ConfigLifecycle{
		HotReload: []ConfigField{
			{Key: "taskDefaults", Label: "任务默认节奏", Description: "超时、重试、保活间隔只影响后续新任务。"},
			{Key: "eventRetention", Label: "事件保留策略", Description: "保留天数、条数和容量保存后立即生效。"},
			{Key: "notificationPolicy", Label: "通知聚合策略", Description: "进度、摘要和恢复合并窗口保存后立即生效。"},
			{Key: "manualProviders", Label: "手动 Provider", Description: "凭证、模型、启停和代理策略保存后影响后续新任务。"},
			{Key: "dingTalkWebhook", Label: "钉钉 Webhook", Description: "Redis 中的加密 Webhook 保存或清除后立即生效。"},
			{Key: "schedules", Label: "计划任务", Description: "新增、编辑、启停和删除会唤醒调度器重新协调。"},
		},
		RestartRequired: []ConfigField{
			{Key: "cliBinaries", Label: "CLI 可执行文件", Description: "CODEX_BIN 与 CLAUDE_BIN 在服务启动时装配。"},
			{Key: "mountedConfigs", Label: "配置挂载位置", Description: "Codex、Claude 与 CC Switch 路径变更后需要重启。"},
			{Key: "storageAndRuntime", Label: "存储与运行目录", Description: "数据目录、运行目录和 Web 静态目录在启动时确定。"},
			{Key: "networkAndProxy", Label: "监听与默认代理", Description: "监听地址、Mihomo 服务和默认代理 URL 由启动环境与 Compose 确定。"},
		},
	}
}
