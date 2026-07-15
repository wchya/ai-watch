package domain

import "time"

type Mode string
type CLI string
type ProxyMode string
type JobStatus string
type AttemptStatus string
type JobPhase string

const (
	ModeProbe     Mode      = "probe"
	ModeKeepalive Mode      = "keepalive"
	CLICodex      CLI       = "codex"
	CLIClaude     CLI       = "claude"
	ProxyDefault  ProxyMode = "default"
	ProxyDirect   ProxyMode = "direct"
	ProxyCustom   ProxyMode = "custom"

	JobQueued  JobStatus = "queued"
	JobRunning JobStatus = "running"
	JobSuccess JobStatus = "success"
	JobFatal   JobStatus = "fatal"
	JobStopped JobStatus = "stopped"
	JobFailed  JobStatus = "failed"

	AttemptSuccess    AttemptStatus = "success"
	AttemptTimeout    AttemptStatus = "timeout"
	AttemptOverloaded AttemptStatus = "overloaded"
	AttemptFatal      AttemptStatus = "fatal"
	AttemptUnmatched  AttemptStatus = "unmatched"
	AttemptStopped    AttemptStatus = "stopped"

	JobPhaseProbe         JobPhase = "probe"
	JobPhaseKeepalive     JobPhase = "keepalive"
	JobPhaseRecoveryProbe JobPhase = "recovery_probe"
)

type JobOptions struct {
	Mode                     Mode   `json:"mode"`
	RunOnce                  bool   `json:"runOnce,omitempty"`
	CLI                      CLI    `json:"cli"`
	ProviderID               string `json:"providerId,omitempty"`
	Prompt                   string `json:"prompt,omitempty"`
	Expected                 string `json:"expected,omitempty"`
	TimeoutSeconds           int    `json:"timeoutSeconds,omitempty"`
	RetryIntervalSeconds     int    `json:"retryIntervalSeconds,omitempty"`
	KeepaliveIntervalSeconds int    `json:"keepaliveIntervalSeconds,omitempty"`
	FailureThreshold         int    `json:"failureThreshold,omitempty"`
	CodexRequestRetries      int    `json:"codexRequestRetries,omitempty"`
	CodexStreamRetries       int    `json:"codexStreamRetries,omitempty"`
	ClaudeMaxRetries         int    `json:"claudeMaxRetries,omitempty"`
	Model                    string `json:"model,omitempty"`
	FallbackModel            string `json:"fallbackModel,omitempty"`
	SessionName              string `json:"sessionName,omitempty"`
	TriggerSource            string `json:"-"`
	ClientIP                 string `json:"-"`
}

func (o *JobOptions) Defaults() {
	if o.Prompt == "" {
		o.Prompt = "hi，只回复 READY"
	}
	if o.Expected == "" {
		o.Expected = "READY"
	}
	if o.TimeoutSeconds == 0 {
		o.TimeoutSeconds = 15
	}
	if o.KeepaliveIntervalSeconds == 0 {
		o.KeepaliveIntervalSeconds = 120
	}
	if o.FailureThreshold == 0 {
		o.FailureThreshold = 3
	}
	if o.SessionName == "" {
		o.SessionName = "claude-watch"
	}
}

type ResolvedConfig struct {
	Source       string            `json:"source"`
	ProviderID   string            `json:"providerId,omitempty"`
	ProviderName string            `json:"providerName,omitempty"`
	Provider     string            `json:"provider"`
	Model        string            `json:"model,omitempty"`
	BaseURL      string            `json:"baseUrl"`
	APIKey       string            `json:"-"`
	AuthJSON     []byte            `json:"-"`
	LockIdentity string            `json:"-"`
	APIKeySource string            `json:"apiKeySource,omitempty"`
	CodexConfig  string            `json:"-"`
	ClaudeEnv    map[string]string `json:"-"`
	ConfigDir    string            `json:"-"`
	ProxyMode    ProxyMode         `json:"-"`
	ProxyURL     string            `json:"-"`
}

type Provider struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	CLI       CLI            `json:"cli"`
	Current   bool           `json:"current"`
	Model     string         `json:"model,omitempty"`
	BaseURL   string         `json:"baseUrl,omitempty"`
	MaskedKey string         `json:"maskedKey,omitempty"`
	Enabled   *bool          `json:"enabled,omitempty"`
	Available *bool          `json:"available,omitempty"`
	State     *ProviderState `json:"state,omitempty"`
}

// CCSwitchProvider is a normalized startup import record. Sensitive source
// material is deliberately excluded from JSON so it can only cross trusted
// scanner/store boundaries before being encrypted at rest.
type CCSwitchProvider struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	CLI         CLI               `json:"cli"`
	Current     bool              `json:"current"`
	BaseURL     string            `json:"baseUrl,omitempty"`
	Model       string            `json:"model,omitempty"`
	Provider    string            `json:"provider,omitempty"`
	APIKey      string            `json:"-"`
	CodexConfig string            `json:"-"`
	ClaudeEnv   map[string]string `json:"-"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

type ProviderState struct {
	Status              string        `json:"status"`
	Phase               JobPhase      `json:"phase,omitempty"`
	LatestAttempt       AttemptStatus `json:"latestAttempt,omitempty"`
	ActiveJobID         string        `json:"activeJobId,omitempty"`
	Attempts            int           `json:"attempts"`
	ConsecutiveFailures int           `json:"consecutiveFailures,omitempty"`
	LastSuccessAt       *time.Time    `json:"lastSuccessAt,omitempty"`
	LastFailureAt       *time.Time    `json:"lastFailureAt,omitempty"`
	ScheduleEnabled     bool          `json:"scheduleEnabled"`
	ScheduleName        string        `json:"scheduleName,omitempty"`
	ScheduleMode        Mode          `json:"scheduleMode,omitempty"`
	NextScheduledAt     *time.Time    `json:"nextScheduledAt,omitempty"`
}

// ProviderExample is a non-sensitive connection template. Credentials are
// intentionally absent: examples describe only how a CLI provider is shaped,
// while authentication must be supplied by mounted config or environment.
type ProviderExample struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	CLI         CLI       `json:"cli"`
	BaseURL     string    `json:"baseUrl"`
	Model       string    `json:"model,omitempty"`
	Provider    string    `json:"provider,omitempty"`
	Description string    `json:"description,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ManualProvider is a user-managed provider configuration. APIKey is available
// only inside the trusted resolver/store boundary and is never serialized.
type ManualProvider struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CLI       CLI       `json:"cli"`
	Enabled   bool      `json:"enabled"`
	BaseURL   string    `json:"baseUrl"`
	Model     string    `json:"model,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	ProxyMode ProxyMode `json:"proxyMode"`
	APIKey    string    `json:"-"`
	ProxyURL  string    `json:"-"`
	// ClearAPIKey is an in-memory persistence instruction and is never exposed.
	ClearAPIKey bool `json:"-"`
	// ClearProxyURL is an in-memory persistence instruction and is never exposed.
	ClearProxyURL  bool      `json:"-"`
	HasAPIKey      bool      `json:"hasApiKey"`
	MaskedKey      string    `json:"maskedKey,omitempty"`
	HasProxyURL    bool      `json:"hasProxyUrl"`
	MaskedProxyURL string    `json:"maskedProxyUrl,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type ManualProviderWrite struct {
	Name          string    `json:"name"`
	CLI           CLI       `json:"cli"`
	Enabled       *bool     `json:"enabled,omitempty"`
	BaseURL       string    `json:"baseUrl"`
	Model         string    `json:"model,omitempty"`
	Provider      string    `json:"provider,omitempty"`
	ProxyMode     ProxyMode `json:"proxyMode,omitempty"`
	APIKey        string    `json:"apiKey,omitempty"`
	ClearAPIKey   bool      `json:"clearApiKey,omitempty"`
	ProxyURL      string    `json:"proxyUrl,omitempty"`
	ClearProxyURL bool      `json:"clearProxyUrl,omitempty"`
}

type DingTalkConfig struct {
	WebhookURL    string     `json:"-"`
	Configured    bool       `json:"configured"`
	Source        string     `json:"source"`
	MaskedWebhook string     `json:"maskedWebhook,omitempty"`
	UpdatedAt     *time.Time `json:"updatedAt,omitempty"`
}

type DingTalkConfigWrite struct {
	WebhookURL  string `json:"webhookUrl,omitempty"`
	ClearStored bool   `json:"clearStored,omitempty"`
}

// Schedule is a non-sensitive rule that points at an already discovered
// provider. Connection details and runtime input deliberately do not belong in
// this model: the jobs manager resolves them only when an occurrence starts.
type Schedule struct {
	ID                       string     `json:"id"`
	Name                     string     `json:"name"`
	Enabled                  bool       `json:"enabled"`
	CLI                      CLI        `json:"cli"`
	ProviderID               string     `json:"providerId"`
	ProviderName             string     `json:"providerName,omitempty"`
	Mode                     Mode       `json:"mode"`
	Timezone                 string     `json:"timezone"`
	WeekdaysMask             int        `json:"weekdaysMask"`
	StartMinute              int        `json:"startMinute"`
	EndMinute                int        `json:"endMinute"`
	UntilSuccess             bool       `json:"untilSuccess"`
	TimeoutSeconds           int        `json:"timeoutSeconds"`
	RetryIntervalSeconds     int        `json:"retryIntervalSeconds"`
	KeepaliveIntervalSeconds int        `json:"keepaliveIntervalSeconds"`
	FailureThreshold         int        `json:"failureThreshold"`
	Model                    string     `json:"model,omitempty"`
	FallbackModel            string     `json:"fallbackModel,omitempty"`
	LastOccurrenceKey        string     `json:"lastOccurrenceKey,omitempty"`
	LastStatus               string     `json:"lastStatus,omitempty"`
	LastJobID                string     `json:"lastJobId,omitempty"`
	LastOccurrenceAt         *time.Time `json:"lastOccurrenceAt,omitempty"`
	NextRunAt                *time.Time `json:"nextRunAt,omitempty"`
	CreatedAt                time.Time  `json:"createdAt"`
	UpdatedAt                time.Time  `json:"updatedAt"`
}

type ConfigStatus struct {
	CodexCLI     bool   `json:"codexCli"`
	ClaudeCLI    bool   `json:"claudeCli"`
	SQLiteCLI    bool   `json:"sqliteCli"`
	CodexConfig  bool   `json:"codexConfig"`
	ClaudeConfig bool   `json:"claudeConfig"`
	CCSwitchDB   bool   `json:"ccSwitchDb"`
	CodexPath    string `json:"codexPath,omitempty"`
	ClaudePath   string `json:"claudePath,omitempty"`
	CCSwitchPath string `json:"ccSwitchPath,omitempty"`
}

type Job struct {
	ID            string        `json:"id"`
	Mode          Mode          `json:"mode"`
	RunOnce       bool          `json:"runOnce"`
	CLI           CLI           `json:"cli"`
	ProviderID    string        `json:"providerId,omitempty"`
	ProviderName  string        `json:"providerName,omitempty"`
	Provider      string        `json:"provider,omitempty"`
	Target        string        `json:"target"`
	Model         string        `json:"model,omitempty"`
	MaskedKey     string        `json:"maskedKey,omitempty"`
	Status        JobStatus     `json:"status"`
	Phase         JobPhase      `json:"phase"`
	LatestAttempt AttemptStatus `json:"latestAttempt,omitempty"`
	Attempts      int           `json:"attempts"`
	StartedAt     time.Time     `json:"startedAt"`
	EndedAt       *time.Time    `json:"endedAt,omitempty"`
	NextAttemptAt *time.Time    `json:"nextAttemptAt,omitempty"`
	ElapsedMillis int64         `json:"elapsedMillis"`
}

type Summary = Job

type Event struct {
	ID      uint64         `json:"id"`
	Type    string         `json:"type"`
	At      time.Time      `json:"at"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

type Settings struct {
	TimeoutSeconds                      int    `json:"timeoutSeconds"`
	RetryIntervalSeconds                int    `json:"retryIntervalSeconds"`
	KeepaliveIntervalSeconds            int    `json:"keepaliveIntervalSeconds"`
	KeepaliveSummarySeconds             int    `json:"keepaliveSummarySeconds"`
	KeepaliveSummarySuccesses           int    `json:"keepaliveSummarySuccesses"`
	ProbeProgressSeconds                int    `json:"probeProgressSeconds"`
	RecoveryMergeSeconds                int    `json:"recoveryMergeSeconds"`
	ReliabilityAlertEnabled             bool   `json:"reliabilityAlertEnabled"`
	ReliabilityAlertMinSamples          int    `json:"reliabilityAlertMinSamples"`
	ReliabilityAlertSuccessRate         int    `json:"reliabilityAlertSuccessRate"`
	ReliabilityAlertConsecutiveFailures int    `json:"reliabilityAlertConsecutiveFailures"`
	ReliabilityAlertP95Millis           int    `json:"reliabilityAlertP95Millis"`
	ReliabilityAlertCooldownSeconds     int    `json:"reliabilityAlertCooldownSeconds"`
	ReliabilityAlertRecoverySuccesses   int    `json:"reliabilityAlertRecoverySuccesses"`
	ReliabilityAlertRecoveryEnabled     bool   `json:"reliabilityAlertRecoveryEnabled"`
	HistoryLimit                        int    `json:"historyLimit"`
	EventRetentionDays                  int    `json:"eventRetentionDays"`
	EventRetentionRows                  int    `json:"eventRetentionRows"`
	EventRetentionBytes                 int64  `json:"eventRetentionBytes"`
	DingTalkConfigured                  bool   `json:"dingTalkConfigured"`
	UITheme                             string `json:"uiTheme"`
}

const (
	UIThemeDeepOcean      = "deep-ocean"
	UIThemeGraphiteSignal = "graphite-signal"
	UIThemeArcticDaylight = "arctic-daylight"
)

func ValidUITheme(value string) bool {
	return value == UIThemeDeepOcean || value == UIThemeGraphiteSignal || value == UIThemeArcticDaylight
}

func DefaultSettings() Settings {
	return Settings{
		TimeoutSeconds: 15, RetryIntervalSeconds: 2, KeepaliveIntervalSeconds: 120,
		KeepaliveSummarySeconds: 3600, KeepaliveSummarySuccesses: 0,
		ProbeProgressSeconds: 3600, RecoveryMergeSeconds: 0,
		ReliabilityAlertEnabled: false, ReliabilityAlertMinSamples: 5, ReliabilityAlertSuccessRate: 90,
		ReliabilityAlertConsecutiveFailures: 3, ReliabilityAlertP95Millis: 0,
		ReliabilityAlertCooldownSeconds: 1800, ReliabilityAlertRecoverySuccesses: 2, ReliabilityAlertRecoveryEnabled: true,
		HistoryLimit: 100, EventRetentionDays: 30, EventRetentionRows: 5000,
		EventRetentionBytes: 8 << 20,
		UITheme:             UIThemeDeepOcean,
	}
}
