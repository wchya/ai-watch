export type Cli = 'codex' | 'claude'
export type JobMode = 'probe' | 'keepalive'
export type JobStatus = 'starting' | 'running' | 'success' | 'fatal' | 'stopped' | 'failed'
export type AttemptStatus = 'success' | 'timeout' | 'overloaded' | 'fatal' | 'unmatched' | 'stopped'
export type JobPhase = 'probe' | 'keepalive' | 'recovery_probe'

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

export interface ProviderExample {
  id: string
  name: string
  cli: Cli
  baseUrl?: string
  model?: string
  provider?: string
  description?: string
  updatedAt?: string
}

export type ProviderExampleWriteRequest = Omit<ProviderExample, 'updatedAt'>

export interface JobSummary {
  id: string
  mode: JobMode
  runOnce?: boolean
  cli: Cli
  providerId?: string
  providerName?: string
  model?: string
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
  browserNotifications: boolean
  dingTalkConfigured: boolean
  uiTheme: 'deep-ocean' | 'graphite-signal' | 'arctic-daylight'
}

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

export type ScheduleLastStatus = JobStatus | 'idle' | 'skipped'

export interface Schedule {
  id: string
  name: string
  enabled: boolean
  cli: Cli
  providerId: string
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

export type RedisDataType = 'string' | 'hash' | 'list' | 'set' | 'zset' | string

export interface RedisOverview {
  connected: boolean
  version?: string
  mode?: string
  keyCount: number
  usedMemoryBytes: number
  usedMemoryHuman?: string
  maxMemoryBytes: number
  connectedClients: number
  expiringKeys: number
  hitRate: number
  uptimeSeconds: number
  latencyMs: number
}

export interface RedisKeySummary {
  key: string
  type: RedisDataType
  ttlMillis: number
  persistent: boolean
  size: number
  memoryBytes?: number
}

export interface RedisHashEntry { field: string; value: string }
export interface RedisZSetEntry { member: string; score: number }

export interface RedisKeyDetail extends RedisKeySummary {
  encoding?: string
  version: string
  cursor?: string
  nextCursor?: string
  truncated?: boolean
  value?: string | string[] | RedisHashEntry[] | RedisZSetEntry[]
}

export interface RedisKeyListResult {
  keys: RedisKeySummary[]
  cursor: string
  nextCursor: string
}

export interface RedisPrewarmSnapshots {
  settings: number
  summaries: number
  providerExamples: number
  schedules: number
  manualProviders: number
  ccSwitchProviders: number
  dingTalk: number
}

export interface RedisPrewarmResult {
  durationMs: number
  snapshots: RedisPrewarmSnapshots
}

export interface RedisMutationInput {
  key: string
  operation: string
  version: string
  confirmKey?: string
  value?: string
  field?: string
  member?: string
  score?: number
  index?: number
}

export interface RedisMutationResult {
  key: RedisKeyDetail
  prewarm?: RedisPrewarmSnapshots
}

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
