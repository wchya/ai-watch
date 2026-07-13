import type {
  AppSettings, DashboardData, EventListResult, EventQuery, JobEvent, JobPhase, JobStatus,
  JobSummary, OperationalEvent, Provider, ProviderExample, StartJobRequest,
} from './types'

const API_BASE = (import.meta.env.VITE_API_BASE_URL as string | undefined)?.replace(/\/$/, '') ?? '/api'

export class ApiError extends Error {
  constructor(message: string, public status: number, public code?: string) { super(message) }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  })
  if (!response.ok) {
    const body = await response.json().catch(() => ({})) as { message?: string; code?: string; error?: string | { message?: string; code?: string } }
    const nested = typeof body.error === 'object' ? body.error : undefined
    throw new ApiError(nested?.message || body.message || (typeof body.error === 'string' ? body.error : '') || `请求失败 (${response.status})`, response.status, nested?.code || body.code)
  }
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

interface RawProvider { id: string; name: string; cli: 'codex' | 'claude'; current: boolean; model?: string; baseUrl?: string; maskedKey?: string }
interface RawJob {
  id: string; mode: 'probe' | 'keepalive'; cli: 'codex' | 'claude'; providerId?: string;
  providerName?: string; model?: string; status: string; phase?: JobPhase; latestAttempt?: JobSummary['lastAttemptStatus'];
  attempts: number; startedAt: string; endedAt?: string; nextAttemptAt?: string; elapsedMillis: number
}
interface RawSettings { timeoutSeconds: number; retryIntervalSeconds: number; keepaliveIntervalSeconds: number; historyLimit: number; eventRetentionDays: number; eventRetentionRows: number; eventRetentionBytes: number; dingTalkConfigured?: boolean }
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
  message?: string
}

const normalizeStatus = (status: string): JobStatus => status === 'queued' ? 'starting' : status as JobStatus
const normalizeJob = (job: RawJob): JobSummary => ({
  id: job.id, mode: job.mode, cli: job.cli, providerId: job.providerId, providerName: job.providerName,
  model: job.model, status: normalizeStatus(job.status), phase: job.phase, lastAttemptStatus: job.latestAttempt,
  attemptCount: job.attempts, startedAt: job.startedAt, endedAt: job.endedAt,
  nextAttemptAt: job.nextAttemptAt, elapsedMs: job.elapsedMillis,
})
const normalizeProvider = (provider: RawProvider): Provider => ({
  ...provider, source: provider.id === '' ? 'current' : 'cc-switch', maskedApiKey: provider.maskedKey,
})
const normalizeOperationalEvent = (event: RawOperationalEvent): OperationalEvent => ({
  id: String(event.id),
  at: event.at || event.timestamp || new Date(0).toISOString(),
  type: event.type || 'unknown',
  level: event.level,
  providerId: event.providerId || event.provider_id,
  jobId: event.jobId || event.job_id,
  message: event.message,
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
  const type: JobEvent['type'] = event.type === 'output' || event.type === 'error'
    ? 'log' : event.type === 'cleanup' ? 'cleanup' : event.type === 'attempt_start' || event.type === 'classification'
      ? 'attempt' : 'state'
  return {
    id: String(event.id), type,
    timestamp: event.at, message: event.message,
    level: (data.level as JobEvent['level'] | undefined) || (event.type === 'output' ? 'info' : event.type === 'error' ? 'error' : event.type === 'classification' && data.status === 'success' ? 'success' : undefined),
    attemptStatus: data.status as JobEvent['attemptStatus'] | undefined,
    job: rawJob ? normalizeJob(rawJob) : undefined,
  }
}

export const api = {
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
          { id: 'cc-switch', name: 'CC Switch', available: config.ccSwitchDb, description: config.ccSwitchPath || '未挂载数据库' },
          { id: 'sqlite', name: 'SQLite', available: config.sqliteCli, description: 'Provider 读取依赖' },
        ],
      }, providers, runningJobs, recentJobs,
    }
  },
  async getJob(id: string) { return normalizeJob(await request<RawJob>(`/jobs/${encodeURIComponent(id)}`)) },
  async startJob(body: StartJobRequest) {
    const o = body.options
    const payload = {
      mode: body.mode, cli: body.cli, providerId: body.providerId, prompt: o.prompt,
      expected: o.expectedText, timeoutSeconds: o.timeoutSeconds,
      retryIntervalSeconds: o.retryIntervalSeconds, keepaliveIntervalSeconds: o.keepaliveIntervalSeconds,
      failureThreshold: body.mode === 'keepalive' ? o.failureThreshold : undefined,
      codexRequestRetries: o.requestMaxRetries, codexStreamRetries: o.streamMaxRetries,
      model: o.model || undefined, fallbackModel: o.fallbackModel || undefined, sessionName: o.sessionName || undefined,
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
    return { ...raw, browserNotifications: local.browserNotifications ?? false, dingTalkEnabled: raw.dingTalkConfigured ?? false, dingTalkConfigured: raw.dingTalkConfigured ?? false }
  },
  async saveSettings(body: AppSettings): Promise<AppSettings> {
    const raw = await request<RawSettings>('/settings', {
      method: 'PUT', body: JSON.stringify({
        timeoutSeconds: body.timeoutSeconds, retryIntervalSeconds: body.retryIntervalSeconds,
        keepaliveIntervalSeconds: body.keepaliveIntervalSeconds, historyLimit: body.historyLimit,
        eventRetentionDays: body.eventRetentionDays, eventRetentionRows: body.eventRetentionRows,
        eventRetentionBytes: body.eventRetentionBytes,
      }),
    })
    storeLocalPrefs(body)
    return { ...body, ...raw }
  },
  async events(query: EventQuery): Promise<EventListResult> {
    const params = new URLSearchParams({ limit: String(query.limit) })
    if (query.type) params.set('type', query.type)
    if (query.providerId) params.set('providerId', query.providerId)
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
  providerExamples: () => request<ProviderExample[]>('/provider-examples'),
  testDingTalk: () => request<{ sent: boolean }>('/notifications/test', { method: 'POST' }),
  eventsUrl: (id: string) => `${API_BASE}/jobs/${encodeURIComponent(id)}/events`,
}
