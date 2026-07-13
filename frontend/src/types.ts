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
  source?: 'current' | 'cc-switch'
  available?: boolean
}

export interface ProviderExample {
  id: string
  name: string
  cli: Cli
  baseUrl?: string
  model?: string
  provider?: string
  description?: string
}

export interface JobSummary {
  id: string
  mode: JobMode
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
  browserNotifications: boolean
  dingTalkEnabled: boolean
  dingTalkConfigured: boolean
}

export interface JobEvent {
  id?: string
  type: 'log' | 'attempt' | 'state' | 'cleanup' | 'heartbeat'
  timestamp: string
  level?: 'info' | 'success' | 'warning' | 'error' | 'command'
  message?: string
  job?: JobSummary
  attemptStatus?: AttemptStatus
}

export interface OperationalEvent {
  id: string
  at: string
  type: string
  level?: string
  providerId?: string
  jobId?: string
  message?: string
}

export interface EventQuery {
  limit: number
  type?: string
  providerId?: string
}

export interface EventListResult {
  events: OperationalEvent[]
  total: number
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

export type BulkJobAction = 'probe' | 'keepalive' | 'stop'

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
