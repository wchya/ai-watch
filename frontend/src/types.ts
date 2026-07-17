export type Cli = 'codex' | 'claude'
export type JobMode = 'probe' | 'keepalive'
export type JobStatus = 'queued' | 'starting' | 'running' | 'success' | 'fatal' | 'stopped' | 'failed'
export type AttemptStatus = 'success' | 'timeout' | 'overloaded' | 'fatal' | 'unmatched' | 'stopped'
export type JobPhase = 'probe' | 'keepalive' | 'recovery_probe'

export type IncidentStatus = 'open' | 'acknowledged' | 'muted' | 'resolved'
export interface IncidentEntry { id: string; at: string; type: string; message: string; requestId: string; jobId?: string; data?: Record<string, unknown> }
export interface Incident {
  id: string; subjectType: 'provider' | 'group'; subjectId: string; subjectName?: string; providerId?: string; groupId?: string
  title: string; summary?: string; status: IncidentStatus; severity: 'warning' | 'critical'; failureCount: number
  errorCounts?: Record<string, number>; jobIds?: string[]; requestIds?: string[]; timeline: IncidentEntry[]; note?: string
  startedAt: string; updatedAt: string; acknowledgedAt?: string; mutedUntil?: string; resolvedAt?: string
}

export interface PostmortemAction { text: string; owner?: string; completed: boolean }
export interface IncidentPostmortem {
  incidentId: string; status: 'draft' | 'completed'; title: string; subject: string; severity: 'warning' | 'critical'
  startedAt: string; resolvedAt?: string; durationSeconds: number; failureCount: number; errorCounts: Record<string, number>
  jobIds: string[]; requestIds: string[]; timeline: IncidentEntry[]; recoverySummary: string
  rootCause: string; mitigation: string; owner: string; actions: PostmortemAction[]
  createdAt: string; updatedAt: string; completedAt?: string
}

export interface HealthItem {
  id: string
  name: string
  description?: string
  available: boolean
  version?: string
}

export interface SystemHealth {
  status: 'ok' | 'degraded'
  version?: string
  uptimeSeconds?: number
  items: HealthItem[]
}

export interface Provider {
  id: string
  cli: Cli
  name: string
  current?: boolean
  model?: string
  baseUrl?: string
  maskedApiKey?: string
  enabled?: boolean
  proxyMode?: ProxyMode
  hasProxyUrl?: boolean
  maskedProxyUrl?: string
  source?: 'current' | 'cc-switch' | 'manual'
  available?: boolean
  state?: ProviderState
}

export type ProxyMode = 'default' | 'direct' | 'custom'

export interface ManualProvider {
  id: string
  name: string
  cli: Cli
  baseUrl: string
  model?: string
  provider?: string
  hasApiKey: boolean
  maskedKey?: string
  proxyMode?: ProxyMode
  hasProxyUrl?: boolean
  maskedProxyUrl?: string
  enabled?: boolean
  createdAt?: string
  updatedAt?: string
}

export interface ManualProviderWrite {
  id?: string
  name: string
  cli: Cli
  baseUrl: string
  model?: string
  provider?: string
  apiKey?: string
  clearApiKey?: boolean
  proxyMode?: ProxyMode
  proxyUrl?: string
  clearProxyUrl?: boolean
  enabled?: boolean
}

export interface DingTalkConfig {
  configured: boolean
  source: 'redis' | 'environment' | 'none'
  maskedWebhook?: string
  updatedAt?: string
}

export interface DingTalkConfigWrite {
  webhookUrl?: string
  clearStored?: boolean
}

export interface MihomoSubscriptionStatus {
  configured: boolean
  maskedUrl?: string
  applied: boolean
  nodeCount: number
  currentNode?: string
  updatedAt?: string
  lastCheckedAt?: string
  errorStage?: string
  errorMessage?: string
}

export type NotificationMessageType = 'incident_opened' | 'incident_recovered' | 'reliability_alert' | 'reliability_recovered' | 'reliability_digest' | 'job_notification'
export interface NotificationChannel { id:string; name:string; description?:string; type:'dingtalk'; enabled:boolean; configured:boolean; maskedWebhook?:string; createdAt:string; updatedAt:string }
export interface NotificationChannelWrite { id?:string; name:string; description?:string; type:'dingtalk'; enabled?:boolean; webhookUrl?:string }
export interface NotificationRoutes { routes:Record<NotificationMessageType,string>; updatedAt?:string }

export interface ProviderState {
  status: 'idle' | 'queued' | 'running' | 'recovering' | JobStatus
  phase?: JobPhase
  latestAttempt?: AttemptStatus
  activeJobId?: string
  attempts: number
  consecutiveFailures?: number
  lastSuccessAt?: string
  lastFailureAt?: string
  scheduleEnabled: boolean
  scheduleName?: string
  scheduleMode?: JobMode
  nextScheduledAt?: string
}

export interface TestScenario {
  id: string
  name: string
  description?: string
  cli?: Cli | ''
  enabled: boolean
  prompt: string
  assertionType: 'contains' | 'exact' | 'regex' | 'json'
  expected?: string
  timeoutSeconds?: number
  builtIn?: boolean
  createdAt?: string
  updatedAt?: string
}

export type TestScenarioWriteRequest = Omit<TestScenario, 'builtIn' | 'createdAt' | 'updatedAt'>

export interface ScenarioComparisonItem {
  providerId: string
  providerName?: string
  jobId?: string
  requestId?: string
  status: string
  durationMillis?: number
  errorType?: string
  error?: string
  responseExcerpt?: string
  startedAt?: string
  endedAt?: string
}

export interface ScenarioComparison {
  id: string
  scenarioId: string
  scenarioName: string
  cli: Cli
  status: 'running' | 'completed' | 'partial_failed'
  createdAt: string
  items: ScenarioComparisonItem[]
}

export interface ScenarioComparisonListResult {
  items: ScenarioComparison[]
  total: number
  retentionLimited: boolean
}

export interface FailoverAdvice {
  status: 'validating' | 'open' | 'recovered' | string
  primaryRequestId?: string
  suggestedProviderId?: string
  validationJobId?: string
  validationRequestId?: string
  reason?: string
  createdAt: string
  updatedAt: string
  recoveredAt?: string
  appliedAt?: string
}

export interface ProviderFailoverGroup {
  id: string
  name: string
  cli: Cli
  enabled: boolean
  primaryProviderId: string
  backupProviderIds: string[]
  scenarioId: string
  failureThreshold: number
  cooldownSeconds: number
  mode: 'advisory' | 'automatic'
  activeProviderId: string
  recoveryThreshold: number
  recoveryProbeIntervalSeconds: number
  lastRecoveryProbeAt?: string
  lastRecoveryProbeStatus?: string
  maintenanceStartsAt?: string
  maintenanceUntil?: string
  sloEnabled?: boolean
  sloTargetPercent?: number
  sloWindow?: '24h' | '7d' | '30d'
  sloMinimumSamples?: number
  lastSwitchedAt?: string
  advice?: FailoverAdvice
  createdAt?: string
  updatedAt?: string
}

export type ProviderFailoverGroupWrite = Omit<ProviderFailoverGroup, 'advice' | 'lastRecoveryProbeAt' | 'lastRecoveryProbeStatus' | 'createdAt' | 'updatedAt'>

export interface MaintenanceWindow {
  groupId: string
  groupName: string
  cli: Cli
  mode: 'advisory' | 'automatic'
  activeProviderId: string
  maintenanceStartsAt?: string
  maintenanceUntil?: string
  status: 'none' | 'scheduled' | 'active' | 'ended'
  notificationsMuted: boolean
  failoverSuppressed: boolean
}

export interface ServiceLevelObjective {
  groupId: string; groupName: string; cli: Cli; enabled: boolean; targetPercent: number; window: '24h' | '7d' | '30d'; minimumSamples: number
  status: 'disabled' | 'insufficient' | 'healthy' | 'burning' | 'critical' | 'exhausted'
  samples: number; successes: number; failures: number; excluded: number; successRate: number; allowedFailures: number
  remainingBudget: number; consumedPercent: number; burnRate: number; windowStartedAt: string
}

export interface ProviderGroupEvaluation {
  groupId: string
  mode: 'advisory' | 'automatic'
  activeProviderId: string
  candidateProviderId: string
  recommendation: 'validating'
  job: JobSummary
  hostConfigChanged: false
}

export interface ProviderGroupSwitchResult {
  groupId: string
  previousProviderId: string
  activeProviderId: string
  validationRequestId?: string
  affectedScheduleCount: number
  switched: boolean
  hostConfigChanged: false
}

export interface JobSummary {
  id: string
  mode: JobMode
  runOnce?: boolean
  cli: Cli
  providerId?: string
  providerName?: string
  model?: string
  scenarioId?: string
  scenarioName?: string
  status: JobStatus
  phase?: JobPhase
  lastAttemptStatus?: AttemptStatus
  attemptCount: number
  startedAt: string
  endedAt?: string
  nextAttemptAt?: string
  elapsedMs?: number
}

export interface DashboardData {
  health: SystemHealth
  providers: Provider[]
  runningJobs: JobSummary[]
  recentJobs: JobSummary[]
}

export interface JobOptions {
  runOnce: boolean
  timeoutSeconds: number
  retryIntervalSeconds: number
  keepaliveIntervalSeconds: number
  failureThreshold: number
  prompt: string
  expectedText: string
  requestMaxRetries: number
  streamMaxRetries: number
  model?: string
  fallbackModel?: string
  sessionName?: string
  notifyOnComplete: boolean
  scenarioId?: string
}

export interface StartJobRequest {
  mode: JobMode
  cli: Cli
  providerId: string
  options: JobOptions
}

export interface AppSettings {
  timeoutSeconds: number
  retryIntervalSeconds: number
  keepaliveIntervalSeconds: number
  historyLimit: number
  eventRetentionDays: number
  eventRetentionRows: number
  eventRetentionBytes: number
  keepaliveSummarySeconds: number
  keepaliveSummarySuccesses: number
  probeProgressSeconds: number
  recoveryMergeSeconds: number
  reliabilityAlertEnabled: boolean
  reliabilityAlertMinSamples: number
  reliabilityAlertSuccessRate: number
  reliabilityAlertConsecutiveFailures: number
  reliabilityAlertP95Millis: number
  reliabilityAlertCooldownSeconds: number
  reliabilityAlertRecoverySuccesses: number
  reliabilityAlertRecoveryEnabled: boolean
  reliabilityDigestEnabled: boolean
  reliabilityDigestHour: number
  reliabilityDigestMinute: number
  reliabilityDigestTimezone: string
  reliabilityDigestRange: ReliabilityRange
  browserNotifications: boolean
  dingTalkConfigured: boolean
  uiTheme: 'deep-ocean' | 'graphite-signal' | 'arctic-daylight'
}

export interface ReliabilityDigestPreview { title: string; content: string; range: ReliabilityRange; generatedAt: string }

export interface JobEvent {
  id?: string
  type: 'log' | 'attempt' | 'state' | 'cleanup' | 'heartbeat'
  timestamp: string
  level?: 'info' | 'success' | 'warning' | 'error' | 'command'
  message?: string
  job?: JobSummary
  attemptStatus?: AttemptStatus
  data?: Record<string, unknown>
  rawType?: string
}

export interface OperationalEvent {
  id: string
  at: string
  type: string
  level?: string
  providerId?: string
  jobId?: string
  scheduleId?: string
  message?: string
  data?: Record<string, unknown>
}

export interface RequestDetail {
  requestId: string
  jobId?: string
  scheduleId?: string
  providerId?: string
  attempt?: number
  mode?: string
  phase?: string
  cli?: Cli
  model?: string
  provider?: string
  configSource?: string
  triggerSource?: string
  clientIP?: string
  target?: string
  targetHost?: string
  targetPort?: string
  dnsIPs?: string[]
  dnsError?: string
  proxyMode?: string
  proxyEndpoint?: string
  cliExecutable?: string
  cliVersion?: string
  status: string
  classification?: string
  startedAt: string
  endedAt?: string
  durationMillis?: number
  exitCode?: number
  errorStage?: string
  errorType?: string
  error?: string
  retryable?: boolean
  responseExcerpt?: string
  nextAttemptAt?: string
  input: {
    promptBytes?: number
    promptSHA256?: string
    timeoutSeconds?: number
    runOnce: boolean
    codexRequestRetries?: number
    codexStreamRetries?: number
    claudeMaxRetries?: number
    fallbackModel?: string
  }
  complete: boolean
  recommendation: string
}

export interface EventQuery {
  limit: number
  offset?: number
  type?: string
  level?: string
  providerId?: string
  jobId?: string
  scheduleId?: string
  since?: string
  until?: string
}

export interface EventListResult {
  events: OperationalEvent[]
  total: number
}

export interface DiagnosticCLI {
  id: 'codex' | 'claude'
  name: string
  available: boolean
  pathLabel?: string
  version?: string
  checkState: 'ok' | 'unavailable' | 'timeout' | 'version_unreadable'
}

export interface DiagnosticConfigField {
  key: string
  label: string
  description: string
}

export interface SystemDiagnostics {
  status: 'ok' | 'degraded'
  generatedAt: string
  clis: DiagnosticCLI[]
  storage: {
    available: boolean
    backend: 'redis' | 'sqlite' | string
    schemaVersion: number
    logicalBytes: number
    eventCount: number
    scheduleCount: number
  }
  proxy: {
    configured: boolean
    available: boolean
    endpoint?: string
    checkState: 'ok' | 'not_configured' | 'invalid' | 'unavailable' | 'timeout'
  }
  ccSwitchSync?: {
    sourceAvailable: boolean
    lastAttemptAt?: string
    lastSuccessAt?: string
    count: number
    warning?: string
  }
  runtime: {
    activeJobs: number
    activeJobsLimit: number
    directoryEntries: number
    directoryReady: boolean
  }
  config: {
    hotReload: DiagnosticConfigField[]
    restartRequired: DiagnosticConfigField[]
  }
}

export type ScheduleLastStatus = JobStatus | 'idle' | 'skipped' | 'queued'

export interface Schedule {
  id: string
  name: string
  enabled: boolean
  cli: Cli
  providerId: string
  providerGroupId?: string
  providerName?: string
  mode: JobMode
  timezone: string
  weekdaysMask: number
  startMinute: number
  endMinute: number
  untilSuccess: boolean
  timeoutSeconds: number
  retryIntervalSeconds: number
  keepaliveIntervalSeconds: number
  failureThreshold: number
  model?: string
  fallbackModel?: string
  scenarioId?: string
  lastOccurrenceAt?: string
  lastStatus?: ScheduleLastStatus
  lastJobId?: string
  nextRunAt?: string
  createdAt?: string
  updatedAt?: string
}

export type ScheduleWriteRequest = Omit<Schedule,
  'id' | 'providerName' | 'lastOccurrenceAt' | 'lastStatus' | 'lastJobId' |
  'nextRunAt' | 'createdAt' | 'updatedAt'>

export interface ScheduleListResult {
  schedules: Schedule[]
  total: number
  limit?: number
}

export type BulkJobAction = 'probe' | 'probe_once' | 'keepalive' | 'keepalive_once' | 'stop'

export interface BulkJobTarget {
  targetId: string
  scheduleId?: string
  scenarioId?: string
  cli: Cli
  providerId: string
  timeoutSeconds?: number
  retryIntervalSeconds?: number
  keepaliveIntervalSeconds?: number
  failureThreshold?: number
  model?: string
  fallbackModel?: string
}

export interface BulkJobRequest {
  action: BulkJobAction
  items: BulkJobTarget[]
}

export interface BulkJobItemResult {
  targetId: string
  ok: boolean
  job?: JobSummary
  error?: string
  code?: string
}

export interface BulkJobResult {
  results: BulkJobItemResult[]
  accepted: number
  failed: number
}

export interface ApiErrorBody { error?: string; message?: string; code?: string }

export type ReliabilityRange = '24h' | '7d' | '30d'

export interface ReliabilityCounts {
  success: number
  timeout: number
  overloaded: number
  unmatched: number
  fatal: number
  startFailed: number
  stopped: number
}

export interface ReliabilityMetrics {
  requests: number
  completed: number
  successRate?: number
  averageDurationMillis?: number
  p95DurationMillis?: number
  maxConsecutiveFailures: number
  consecutiveFailures: number
  counts: ReliabilityCounts
}

export interface ReliabilityProvider {
  key: string
  cli: Cli
  providerId: string
  name: string
  model?: string
  historical: boolean
  lastStatus?: string
  lastRequestId?: string
  lastRequestAt?: string
  lastSuccessAt?: string
  lastFailureAt?: string
  metrics: ReliabilityMetrics
  recommendation: {
    level: 'recommended' | 'healthy' | 'observe' | 'pause' | 'insufficient'
    title: string
    reasons: string[]
    action: string
  }
  remediation?: {
    providerGroupId?: string
    canValidateBackup: boolean
    schedules: Array<{ id: string; name: string; enabled: boolean }>
  }
}

export interface ReliabilityBucket {
  start: string
  requests: number
  successes: number
  failures: number
  stopped: number
  successRate?: number
  averageDurationMillis?: number
}

export interface ReliabilityData {
  range: ReliabilityRange
  generatedAt: string
  coverage: {
    requestedStart: string
    end: string
    retentionDays: number
    partial: boolean
    sampleCount: number
  }
  overall: ReliabilityMetrics
  providers: ReliabilityProvider[]
  buckets: ReliabilityBucket[]
  anomalies: ReliabilityBucket[]
}
