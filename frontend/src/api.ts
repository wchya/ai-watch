import type {
  AppSettings, BulkJobRequest, BulkJobResult, Cli, DashboardData, EventListResult, EventQuery,
  DingTalkConfig, DingTalkConfigWrite, Incident, IncidentPostmortem, IncidentStatus, JobEvent, JobPhase, JobStatus, JobSummary, MaintenanceWindow, ManualProvider, ManualProviderWrite, NotificationChannel, NotificationChannelWrite, NotificationRoutes, OperationalEvent, PostmortemAction, Provider, ProviderFailoverGroup, ProviderFailoverGroupWrite, ProviderGroupEvaluation, ProviderGroupSwitchResult, ServiceLevelObjective,
  ScenarioComparison, ScenarioComparisonListResult, Schedule, ScheduleListResult, ScheduleWriteRequest, StartJobRequest, TestScenario, TestScenarioWriteRequest,
  SystemDiagnostics,
  ReliabilityData, ReliabilityRange, ReliabilityDigestPreview, RequestDetail,
} from './types'

const API_BASE = (import.meta.env.VITE_API_BASE_URL as string | undefined)?.replace(/\/$/, '') ?? '/api'

export class ApiError extends Error {
  constructor(message: string, public status: number, public code?: string) { super(message) }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method || 'GET').toUpperCase()
  const headers = new Headers({ 'Content-Type': 'application/json', ...init?.headers })
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && !headers.has('Idempotency-Key')) {
    const key = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
      ? crypto.randomUUID()
      : `ui-${Date.now()}-${Math.random().toString(36).slice(2)}`
    headers.set('Idempotency-Key', key)
  }
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers,
  })
  if (!response.ok) {
    const body = await response.json().catch(() => ({})) as { message?: string; code?: string; error?: string | { message?: string; code?: string } }
    const nested = typeof body.error === 'object' ? body.error : undefined
    throw new ApiError(nested?.message || body.message || (typeof body.error === 'string' ? body.error : '') || `请求失败 (${response.status})`, response.status, nested?.code || body.code)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

async function requestText(path: string): Promise<string> {
  const response = await fetch(`${API_BASE}${path}`)
  if (!response.ok) throw new ApiError(`请求失败 (${response.status})`, response.status)
  return response.text()
}

interface RawProvider { id: string; name: string; cli: 'codex' | 'claude'; current: boolean; enabled?: boolean; model?: string; baseUrl?: string; maskedKey?: string; proxyMode?: Provider['proxyMode']; hasProxyUrl?: boolean; maskedProxyUrl?: string; state?: Provider['state'] }
interface RawJob {
  id: string; mode: 'probe' | 'keepalive'; cli: 'codex' | 'claude'; providerId?: string;
  runOnce?: boolean;
  providerName?: string; model?: string; scenarioId?: string; scenarioName?: string; status: string; phase?: JobPhase; latestAttempt?: JobSummary['lastAttemptStatus'];
  attempts: number; startedAt: string; endedAt?: string; nextAttemptAt?: string; elapsedMillis: number
}
interface RawSettings {
  timeoutSeconds: number
  retryIntervalSeconds: number
  keepaliveIntervalSeconds: number
  historyLimit: number
  eventRetentionDays: number
  eventRetentionRows: number
  eventRetentionBytes: number
  keepaliveSummarySeconds?: number
  keepaliveSummarySuccesses?: number
  probeProgressSeconds?: number
  recoveryMergeSeconds?: number
  reliabilityAlertEnabled?: boolean
  reliabilityAlertMinSamples?: number
  reliabilityAlertSuccessRate?: number
  reliabilityAlertConsecutiveFailures?: number
  reliabilityAlertP95Millis?: number
  reliabilityAlertCooldownSeconds?: number
  reliabilityAlertRecoverySuccesses?: number
  reliabilityAlertRecoveryEnabled?: boolean
  reliabilityDigestEnabled?: boolean
  reliabilityDigestHour?: number
  reliabilityDigestMinute?: number
  reliabilityDigestTimezone?: string
  reliabilityDigestRange?: ReliabilityRange
  dingTalkConfigured?: boolean
  uiTheme?: AppSettings['uiTheme']
}
interface RawConfigStatus {
  codexCli: boolean; claudeCli: boolean; sqliteCli: boolean; codexConfig: boolean;
  claudeConfig: boolean; ccSwitchDb: boolean; codexPath?: string; claudePath?: string; ccSwitchPath?: string
}
interface RawEvent { id: number; type: string; at: string; message?: string; data?: Record<string, unknown> }
interface RawOperationalEvent {
  id: number | string
  at?: string
  timestamp?: string
  type?: string
  level?: string
  providerId?: string
  provider_id?: string
  jobId?: string
  job_id?: string
  scheduleId?: string
  schedule_id?: string
  message?: string
  data?: Record<string, unknown>
}
interface RawSchedule {
  id: string
  name: string
  enabled: boolean
  cli: 'codex' | 'claude'
  providerId?: string
  provider_id?: string
  providerName?: string
  provider_name?: string
  providerGroupId?: string
  provider_group_id?: string
  mode: 'probe' | 'keepalive'
  timezone: string
  weekdaysMask?: number
  weekdays_mask?: number
  startMinute?: number
  start_minute?: number
  endMinute?: number
  end_minute?: number
  untilSuccess?: boolean
  until_success?: boolean
  timeoutSeconds?: number
  timeout_seconds?: number
  retryIntervalSeconds?: number
  retry_interval_seconds?: number
  keepaliveIntervalSeconds?: number
  keepalive_interval_seconds?: number
  failureThreshold?: number
  failure_threshold?: number
  model?: string
  fallbackModel?: string
  fallback_model?: string
  scenarioId?: string
  scenario_id?: string
  lastOccurrenceAt?: string
  last_occurrence_at?: string
  lastStatus?: Schedule['lastStatus']
  last_status?: Schedule['lastStatus']
  lastJobId?: string
  last_job_id?: string
  nextRunAt?: string
  next_run_at?: string
  createdAt?: string
  created_at?: string
  updatedAt?: string
  updated_at?: string
}

const normalizeStatus = (status: string): JobStatus => status === 'queued' ? 'starting' : status as JobStatus
const normalizeJob = (job: RawJob): JobSummary => ({
  id: job.id, mode: job.mode, runOnce: job.runOnce, cli: job.cli, providerId: job.providerId, providerName: job.providerName,
  model: job.model, scenarioId: job.scenarioId, scenarioName: job.scenarioName, status: normalizeStatus(job.status), phase: job.phase, lastAttemptStatus: job.latestAttempt,
  attemptCount: job.attempts, startedAt: job.startedAt, endedAt: job.endedAt,
  nextAttemptAt: job.nextAttemptAt, elapsedMs: job.elapsedMillis,
})
const normalizeProvider = (provider: RawProvider): Provider => ({
  ...provider, source: provider.id === '' ? 'current' : provider.id.startsWith('manual:') ? 'manual' : 'cc-switch', maskedApiKey: provider.maskedKey,
})
const normalizeOperationalEvent = (event: RawOperationalEvent): OperationalEvent => ({
  id: String(event.id),
  at: event.at || event.timestamp || new Date(0).toISOString(),
  type: event.type || 'unknown',
  level: event.level,
  providerId: event.providerId || event.provider_id,
  jobId: event.jobId || event.job_id,
  scheduleId: event.scheduleId || event.schedule_id,
  message: event.message,
  data: event.data,
})
const normalizeSchedule = (schedule: RawSchedule): Schedule => ({
  id: schedule.id,
  name: schedule.name,
  enabled: schedule.enabled,
  cli: schedule.cli,
  providerId: schedule.providerId ?? schedule.provider_id ?? '',
  providerGroupId: schedule.providerGroupId ?? schedule.provider_group_id ?? '',
  providerName: schedule.providerName ?? schedule.provider_name,
  mode: schedule.mode,
  timezone: schedule.timezone || 'Asia/Shanghai',
  weekdaysMask: schedule.weekdaysMask ?? schedule.weekdays_mask ?? 127,
  startMinute: schedule.startMinute ?? schedule.start_minute ?? 0,
  endMinute: schedule.endMinute ?? schedule.end_minute ?? 1439,
  untilSuccess: schedule.untilSuccess ?? schedule.until_success ?? true,
  timeoutSeconds: schedule.timeoutSeconds ?? schedule.timeout_seconds ?? 15,
  retryIntervalSeconds: schedule.retryIntervalSeconds ?? schedule.retry_interval_seconds ?? 3,
  keepaliveIntervalSeconds: schedule.keepaliveIntervalSeconds ?? schedule.keepalive_interval_seconds ?? 120,
  failureThreshold: schedule.failureThreshold ?? schedule.failure_threshold ?? 3,
  model: schedule.model,
  fallbackModel: schedule.fallbackModel ?? schedule.fallback_model,
  scenarioId: schedule.scenarioId ?? schedule.scenario_id,
  lastOccurrenceAt: schedule.lastOccurrenceAt ?? schedule.last_occurrence_at,
  lastStatus: schedule.lastStatus ?? schedule.last_status,
  lastJobId: schedule.lastJobId ?? schedule.last_job_id,
  nextRunAt: schedule.nextRunAt ?? schedule.next_run_at,
  createdAt: schedule.createdAt ?? schedule.created_at,
  updatedAt: schedule.updatedAt ?? schedule.updated_at,
})
const readLocalPrefs = () => {
  try { return JSON.parse(localStorage.getItem('ai-watch-ui-settings') || '{}') as Partial<AppSettings> }
  catch { return {} }
}
const storeLocalPrefs = (settings: AppSettings) => {
  localStorage.setItem('ai-watch-ui-settings', JSON.stringify({ browserNotifications: settings.browserNotifications }))
}

export function normalizeEvent(raw: unknown): JobEvent {
  const event = raw as RawEvent
  const data = event.data || {}
  const rawJob = data.job as RawJob | undefined
  const type: JobEvent['type'] = event.type === 'output' || event.type === 'request_log' || event.type === 'error'
    ? 'log' : event.type === 'cleanup' ? 'cleanup' : event.type === 'attempt_start' || event.type === 'classification'
      ? 'attempt' : 'state'
  return {
    id: String(event.id), type,
    timestamp: event.at, message: event.message,
    level: (data.level as JobEvent['level'] | undefined) || (event.type === 'output' ? 'info' : event.type === 'error' ? 'error' : event.type === 'classification' && data.status === 'success' ? 'success' : undefined),
    attemptStatus: data.status as JobEvent['attemptStatus'] | undefined,
    job: rawJob ? normalizeJob(rawJob) : undefined, data, rawType: event.type,
  }
}

export const api = {
  diagnostics: () => request<SystemDiagnostics>('/diagnostics'),
  requestDetail: (requestId: string) => request<RequestDetail>(`/requests/${encodeURIComponent(requestId)}`),
  reliability: (range: ReliabilityRange) => request<ReliabilityData>(`/reliability?range=${encodeURIComponent(range)}`),
  reliabilityAction: (body: { cli: Cli; providerId: string; action: 'retest' | 'validate_backup' | 'pause_schedules' }) => request<Record<string, unknown>>('/reliability/actions', { method: 'POST', body: JSON.stringify(body) }),
  async reliabilityExport(range: ReliabilityRange, format: 'csv' | 'json') {
    const response = await fetch(`${API_BASE}/reliability/export?range=${encodeURIComponent(range)}&format=${format}`)
    if (!response.ok) throw new ApiError(`报告导出失败 (${response.status})`, response.status)
    const disposition = response.headers.get('Content-Disposition') || ''
    const filename = disposition.match(/filename="?([^";]+)"?/i)?.[1] || `ai-watch-reliability-${range}.${format}`
    return { blob: await response.blob(), filename }
  },
  reliabilityDigestPreview: () => request<ReliabilityDigestPreview>('/reliability/digest/preview'),
  reliabilityDigestSend: () => request<ReliabilityDigestPreview>('/reliability/digest/send', { method: 'POST' }),
  incidents: (status?: IncidentStatus) => request<Incident[]>(`/incidents${status ? `?status=${encodeURIComponent(status)}` : ''}`),
  incident: (id: string) => request<Incident>(`/incidents/${encodeURIComponent(id)}`),
  acknowledgeIncident: (id: string) => request<Incident>(`/incidents/${encodeURIComponent(id)}/acknowledge`, { method: 'POST' }),
  noteIncident: (id: string, note: string) => request<Incident>(`/incidents/${encodeURIComponent(id)}/note`, { method: 'POST', body: JSON.stringify({ note }) }),
  muteIncident: (id: string, seconds: number) => request<Incident>(`/incidents/${encodeURIComponent(id)}/mute`, { method: 'POST', body: JSON.stringify({ seconds }) }),
  closeIncident: (id: string) => request<Incident>(`/incidents/${encodeURIComponent(id)}/close`, { method: 'POST' }),
  reopenIncident: (id: string) => request<Incident>(`/incidents/${encodeURIComponent(id)}/reopen`, { method: 'POST' }),
  incidentPostmortem: (id: string) => request<IncidentPostmortem>(`/incidents/${encodeURIComponent(id)}/postmortem`),
  createIncidentPostmortem: (id: string) => request<IncidentPostmortem>(`/incidents/${encodeURIComponent(id)}/postmortem`, { method: 'POST' }),
  saveIncidentPostmortem: (id: string, body: { rootCause: string; mitigation: string; owner: string; actions: PostmortemAction[] }) => request<IncidentPostmortem>(`/incidents/${encodeURIComponent(id)}/postmortem`, { method: 'PUT', body: JSON.stringify(body) }),
  completeIncidentPostmortem: (id: string) => request<IncidentPostmortem>(`/incidents/${encodeURIComponent(id)}/postmortem/complete`, { method: 'POST' }),
  reopenIncidentPostmortem: (id: string) => request<IncidentPostmortem>(`/incidents/${encodeURIComponent(id)}/postmortem/reopen`, { method: 'POST' }),
  incidentPostmortemMarkdown: (id: string) => requestText(`/incidents/${encodeURIComponent(id)}/postmortem/markdown`),
  async dashboard(): Promise<DashboardData> {
    const [health, config, rawProviders, rawJobs] = await Promise.all([
      request<{ status: string; version?: string }>('/health'),
      request<RawConfigStatus>('/config/status'),
      request<RawProvider[]>('/providers'),
      request<RawJob[]>('/jobs'),
    ])
    const providers = rawProviders.map(normalizeProvider)
    if (config.codexConfig && !providers.some(p => p.cli === 'codex' && p.id === '')) providers.unshift({ id: '', cli: 'codex', name: '当前 Codex 配置', current: true, source: 'current', available: true })
    if (config.claudeConfig && !providers.some(p => p.cli === 'claude' && p.id === '')) providers.unshift({ id: '', cli: 'claude', name: '当前 Claude 配置', current: true, source: 'current', available: true })
    const jobs = rawJobs.map(normalizeJob)
    const runningJobs = jobs.filter(job => job.status === 'running' || job.status === 'starting')
    const recentJobs = jobs.filter(job => !runningJobs.includes(job)).slice(0, 12)
    return {
      health: {
        status: health.status === 'ok' ? 'ok' : 'degraded', version: health.version,
        items: [
          { id: 'codex-cli', name: 'Codex CLI', available: config.codexCli, description: config.codexPath || '容器内命令' },
          { id: 'claude-cli', name: 'Claude CLI', available: config.claudeCli, description: config.claudePath || '容器内命令' },
          { id: 'codex-config', name: 'Codex 配置', available: config.codexConfig, description: config.codexConfig ? '只读挂载可用' : '未发现配置' },
          { id: 'claude-config', name: 'Claude 配置', available: config.claudeConfig, description: config.claudeConfig ? '只读挂载可用' : '未发现配置' },
          { id: 'cc-switch', name: 'CC Switch 启动同步源', available: config.ccSwitchDb, description: config.ccSwitchDb ? '启动时导入 Redis；运行期使用最后成功快照' : '未挂载启动同步源；运行期继续使用 Redis 快照' },
          { id: 'sqlite', name: 'SQLite 启动同步工具', available: config.sqliteCli, description: '仅应用启动时读取 CC Switch，不参与任务运行' },
        ],
      }, providers, runningJobs, recentJobs,
    }
  },
  async getJob(id: string) { return normalizeJob(await request<RawJob>(`/jobs/${encodeURIComponent(id)}`)) },
  async startJob(body: StartJobRequest) {
    const o = body.options
    const payload = {
      mode: body.mode, runOnce: o.runOnce, cli: body.cli, providerId: body.providerId, prompt: o.prompt,
      expected: o.expectedText, timeoutSeconds: o.timeoutSeconds,
      retryIntervalSeconds: o.retryIntervalSeconds, keepaliveIntervalSeconds: o.keepaliveIntervalSeconds,
      failureThreshold: body.mode === 'keepalive' ? o.failureThreshold : undefined,
      codexRequestRetries: o.requestMaxRetries, codexStreamRetries: o.streamMaxRetries,
      model: o.model || undefined, fallbackModel: o.fallbackModel || undefined, sessionName: o.sessionName || undefined, scenarioId: o.scenarioId || undefined,
    }
    return normalizeJob(await request<RawJob>('/jobs', { method: 'POST', body: JSON.stringify(payload) }))
  },
  async stopJob(id: string) {
    await request<{ accepted: boolean }>(`/jobs/${encodeURIComponent(id)}/stop`, { method: 'POST' })
    await new Promise(resolve => setTimeout(resolve, 120))
    return api.getJob(id)
  },
  async settings(): Promise<AppSettings> {
    const raw = await request<RawSettings>('/settings')
    const local = readLocalPrefs()
    return {
      ...raw,
      keepaliveSummarySeconds: raw.keepaliveSummarySeconds ?? 3600,
      keepaliveSummarySuccesses: raw.keepaliveSummarySuccesses ?? 0,
      probeProgressSeconds: raw.probeProgressSeconds ?? 3600,
      recoveryMergeSeconds: raw.recoveryMergeSeconds ?? 0,
      reliabilityAlertEnabled: raw.reliabilityAlertEnabled ?? false,
      reliabilityAlertMinSamples: raw.reliabilityAlertMinSamples ?? 5,
      reliabilityAlertSuccessRate: raw.reliabilityAlertSuccessRate ?? 90,
      reliabilityAlertConsecutiveFailures: raw.reliabilityAlertConsecutiveFailures ?? 3,
      reliabilityAlertP95Millis: raw.reliabilityAlertP95Millis ?? 0,
      reliabilityAlertCooldownSeconds: raw.reliabilityAlertCooldownSeconds ?? 1800,
      reliabilityAlertRecoverySuccesses: raw.reliabilityAlertRecoverySuccesses ?? 2,
      reliabilityAlertRecoveryEnabled: raw.reliabilityAlertRecoveryEnabled ?? true,
      reliabilityDigestEnabled: raw.reliabilityDigestEnabled ?? false,
      reliabilityDigestHour: raw.reliabilityDigestHour ?? 9,
      reliabilityDigestMinute: raw.reliabilityDigestMinute ?? 0,
      reliabilityDigestTimezone: raw.reliabilityDigestTimezone ?? 'Asia/Shanghai',
      reliabilityDigestRange: raw.reliabilityDigestRange ?? '24h',
      browserNotifications: local.browserNotifications ?? false,
      dingTalkConfigured: raw.dingTalkConfigured ?? false,
      uiTheme: raw.uiTheme ?? 'deep-ocean',
    }
  },
  async saveSettings(body: AppSettings): Promise<AppSettings> {
    const raw = await request<RawSettings>('/settings', {
      method: 'PUT', body: JSON.stringify({
        timeoutSeconds: body.timeoutSeconds, retryIntervalSeconds: body.retryIntervalSeconds,
        keepaliveIntervalSeconds: body.keepaliveIntervalSeconds, historyLimit: body.historyLimit,
        eventRetentionDays: body.eventRetentionDays, eventRetentionRows: body.eventRetentionRows,
        eventRetentionBytes: body.eventRetentionBytes,
        keepaliveSummarySeconds: body.keepaliveSummarySeconds,
        keepaliveSummarySuccesses: body.keepaliveSummarySuccesses,
        probeProgressSeconds: body.probeProgressSeconds,
        recoveryMergeSeconds: body.recoveryMergeSeconds,
        reliabilityAlertEnabled: body.reliabilityAlertEnabled,
        reliabilityAlertMinSamples: body.reliabilityAlertMinSamples,
        reliabilityAlertSuccessRate: body.reliabilityAlertSuccessRate,
        reliabilityAlertConsecutiveFailures: body.reliabilityAlertConsecutiveFailures,
        reliabilityAlertP95Millis: body.reliabilityAlertP95Millis,
        reliabilityAlertCooldownSeconds: body.reliabilityAlertCooldownSeconds,
        reliabilityAlertRecoverySuccesses: body.reliabilityAlertRecoverySuccesses,
        reliabilityAlertRecoveryEnabled: body.reliabilityAlertRecoveryEnabled,
        reliabilityDigestEnabled: body.reliabilityDigestEnabled,
        reliabilityDigestHour: body.reliabilityDigestHour,
        reliabilityDigestMinute: body.reliabilityDigestMinute,
        reliabilityDigestTimezone: body.reliabilityDigestTimezone,
        reliabilityDigestRange: body.reliabilityDigestRange,
        uiTheme: body.uiTheme,
      }),
    })
    storeLocalPrefs(body)
    return { ...body, ...raw }
  },
  async events(query: EventQuery): Promise<EventListResult> {
    const params = new URLSearchParams({ limit: String(query.limit) })
    if (query.offset != null) params.set('offset', String(query.offset))
    if (query.type) params.set('type', query.type)
    if (query.level) params.set('level', query.level)
    if (query.providerId) params.set('providerId', query.providerId)
    if (query.jobId) params.set('jobId', query.jobId)
    if (query.scheduleId) params.set('scheduleId', query.scheduleId)
    if (query.since) params.set('since', query.since)
    if (query.until) params.set('until', query.until)
    const raw = await request<RawOperationalEvent[] | { events?: RawOperationalEvent[]; items?: RawOperationalEvent[]; total?: number; count?: number }>(`/events?${params}`)
    const items = Array.isArray(raw) ? raw : raw.events || raw.items || []
    return {
      events: items.map(normalizeOperationalEvent),
      total: Array.isArray(raw) ? items.length : raw.total ?? raw.count ?? items.length,
    }
  },
  async clearEvents(): Promise<number> {
    const result = await request<{ deleted?: number } | undefined>('/events', { method: 'DELETE' })
    return result?.deleted ?? 0
  },
  testScenarios: () => request<TestScenario[]>('/test-scenarios'),
  saveTestScenario: (body: TestScenarioWriteRequest) => request<TestScenario>('/test-scenarios', { method: 'POST', body: JSON.stringify(body) }),
  deleteTestScenario: (id: string) => request<{ deleted: boolean; id: string }>(`/test-scenarios?id=${encodeURIComponent(id)}`, { method: 'DELETE' }),
  createScenarioComparison: (body: { scenarioId: string; cli: Cli; providerIds: string[] }) => request<ScenarioComparison>('/scenario-comparisons', { method: 'POST', body: JSON.stringify(body) }),
  scenarioComparison: (id: string) => request<ScenarioComparison>(`/scenario-comparisons/${encodeURIComponent(id)}`),
  scenarioComparisons: (status?: ScenarioComparison['status']) => request<ScenarioComparisonListResult>(`/scenario-comparisons${status ? `?status=${encodeURIComponent(status)}` : ''}`),
  rerunScenarioComparison: (id: string) => request<ScenarioComparison>(`/scenario-comparisons/${encodeURIComponent(id)}/rerun`, { method: 'POST' }),
  providerGroups: () => request<ProviderFailoverGroup[]>('/provider-groups'),
  saveProviderGroup: (body: ProviderFailoverGroupWrite) => request<ProviderFailoverGroup>('/provider-groups', { method: 'POST', body: JSON.stringify(body) }),
  deleteProviderGroup: (id: string) => request<{ deleted: boolean; id: string }>(`/provider-groups?id=${encodeURIComponent(id)}`, { method: 'DELETE' }),
  evaluateProviderGroup: (id: string) => request<ProviderGroupEvaluation>(`/provider-groups/${encodeURIComponent(id)}/evaluate`, { method: 'POST' }),
  applyProviderGroupAdvice: (id: string, body: { suggestedProviderId: string; adviceUpdatedAt: string; confirmGroupId: string }) => request<ProviderGroupSwitchResult>(`/provider-groups/${encodeURIComponent(id)}/apply-advice`, { method: 'POST', body: JSON.stringify(body) }),
  maintenanceWindows: () => request<MaintenanceWindow[]>('/maintenance-windows'),
  startMaintenance: (id: string, body: { startsAt?: string; until: string }) => request<MaintenanceWindow>(`/maintenance-windows/${encodeURIComponent(id)}/start`, { method: 'POST', body: JSON.stringify(body) }),
  extendMaintenance: (id: string, seconds: number) => request<MaintenanceWindow>(`/maintenance-windows/${encodeURIComponent(id)}/extend`, { method: 'POST', body: JSON.stringify({ seconds }) }),
  endMaintenance: (id: string) => request<MaintenanceWindow>(`/maintenance-windows/${encodeURIComponent(id)}/end`, { method: 'POST' }),
  slos: () => request<ServiceLevelObjective[]>('/slos'),
  configureSLO: (id: string, body: { targetPercent: number; window: ServiceLevelObjective['window']; minimumSamples: number }) => request<ServiceLevelObjective>(`/slos/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(body) }),
  pauseSLO: (id: string) => request<ServiceLevelObjective>(`/slos/${encodeURIComponent(id)}/pause`, { method: 'POST' }),
  resumeSLO: (id: string) => request<ServiceLevelObjective>(`/slos/${encodeURIComponent(id)}/resume`, { method: 'POST' }),
  manualProviders: () => request<ManualProvider[]>('/manual-providers'),
  createManualProvider: (body: ManualProviderWrite) => request<ManualProvider>('/manual-providers', { method: 'POST', body: JSON.stringify(body) }),
  updateManualProvider: (id: string, body: ManualProviderWrite) => {
    const { id: _ignored, ...payload } = body
    return request<ManualProvider>(`/manual-providers/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(payload) })
  },
  deleteManualProvider: (id: string) => request<{ deleted: boolean; id: string }>(`/manual-providers/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  dingTalkConfig: () => request<DingTalkConfig>('/notifications/dingtalk/config'),
  saveDingTalkConfig: (body: DingTalkConfigWrite) => request<DingTalkConfig>('/notifications/dingtalk/config', { method: 'PUT', body: JSON.stringify(body) }),
  notificationChannels: () => request<NotificationChannel[]>('/notification-channels'),
  createNotificationChannel: (body: NotificationChannelWrite) => request<NotificationChannel>('/notification-channels', { method: 'POST', body: JSON.stringify(body) }),
  updateNotificationChannel: (id: string, body: NotificationChannelWrite) => request<NotificationChannel>(`/notification-channels/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(body) }),
  deleteNotificationChannel: (id: string) => request<{deleted:boolean;id:string}>(`/notification-channels/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  testNotificationChannel: (id: string) => request<{sent:boolean;id:string}>(`/notification-channels/${encodeURIComponent(id)}/test`, { method: 'POST' }),
  notificationRoutes: () => request<NotificationRoutes>('/notification-routes'),
  saveNotificationRoutes: (body: NotificationRoutes) => request<NotificationRoutes>('/notification-routes', { method: 'PUT', body: JSON.stringify({ routes: body.routes }) }),
  async schedules(): Promise<ScheduleListResult> {
    const raw = await request<RawSchedule[] | { schedules?: RawSchedule[]; items?: RawSchedule[]; total?: number; limit?: number }>('/schedules')
    const items = Array.isArray(raw) ? raw : raw.schedules || raw.items || []
    return {
      schedules: items.map(normalizeSchedule),
      total: Array.isArray(raw) ? items.length : raw.total ?? items.length,
      limit: Array.isArray(raw) ? undefined : raw.limit,
    }
  },
  async createSchedule(body: ScheduleWriteRequest): Promise<Schedule> {
    return normalizeSchedule(await request<RawSchedule>('/schedules', { method: 'POST', body: JSON.stringify(body) }))
  },
  async updateSchedule(id: string, body: ScheduleWriteRequest): Promise<Schedule> {
    return normalizeSchedule(await request<RawSchedule>(`/schedules/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(body) }))
  },
  async deleteSchedule(id: string): Promise<void> {
    await request<unknown>(`/schedules/${encodeURIComponent(id)}`, { method: 'DELETE' })
  },
  async bulkJobs(body: BulkJobRequest): Promise<BulkJobResult> {
    if (!body.items.length || body.items.length > 50) throw new ApiError('批量操作一次只能处理 1–50 个目标', 400, 'invalid_bulk_size')
    const raw = await request<{
      results?: Array<{ targetId?: string; target_id?: string; ok?: boolean; job?: RawJob; error?: string; code?: string }>
      items?: Array<{ targetId?: string; target_id?: string; ok?: boolean; job?: RawJob; error?: string; code?: string }>
      accepted?: number
      failed?: number
    }>('/jobs/bulk', { method: 'POST', body: JSON.stringify(body) })
    const items = raw.results || raw.items || []
    const results = items.map(item => ({
      targetId: item.targetId || item.target_id || '',
      ok: item.ok ?? !item.error,
      job: item.job ? normalizeJob(item.job) : undefined,
      error: item.error,
      code: item.code,
    }))
    return {
      results,
      accepted: raw.accepted ?? results.filter(item => item.ok).length,
      failed: raw.failed ?? results.filter(item => !item.ok).length,
    }
  },
  testDingTalk: () => request<{ sent: boolean }>('/notifications/test', { method: 'POST' }),
  eventsUrl: (id: string) => `${API_BASE}/jobs/${encodeURIComponent(id)}/events`,
}
