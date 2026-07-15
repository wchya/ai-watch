import type { Page, Route } from '@playwright/test'

type Theme = 'deep-ocean' | 'graphite-signal' | 'arctic-daylight'

const settings = {
  timeoutSeconds: 15,
  retryIntervalSeconds: 3,
  keepaliveIntervalSeconds: 120,
  historyLimit: 100,
  eventRetentionDays: 30,
  eventRetentionRows: 5000,
  eventRetentionBytes: 8 * 1024 * 1024,
  keepaliveSummarySeconds: 3600,
  keepaliveSummarySuccesses: 0,
  probeProgressSeconds: 3600,
  recoveryMergeSeconds: 0,
  reliabilityAlertEnabled: false,
  reliabilityAlertMinSamples: 5,
  reliabilityAlertSuccessRate: 90,
  reliabilityAlertConsecutiveFailures: 3,
  reliabilityAlertP95Millis: 0,
  reliabilityAlertCooldownSeconds: 1800,
  reliabilityAlertRecoverySuccesses: 2,
  reliabilityAlertRecoveryEnabled: true,
  dingTalkConfigured: false,
  uiTheme: 'deep-ocean' as Theme,
}

const providers = [
  { id: '', name: '当前 Codex 配置', cli: 'codex', current: true, enabled: true, model: 'gpt-5', state: { status: 'idle', attempts: 0, scheduleEnabled: false } },
  { id: 'cc-switch:claude-main', name: 'Claude 主线路', cli: 'claude', current: false, enabled: true, model: 'claude-sonnet-4', baseUrl: 'https://claude.example.com', state: { status: 'success', attempts: 2, scheduleEnabled: true, scheduleName: '工作日巡检' } },
]

const manualProviders = [
  { id: 'codex-ray', name: 'Ray 主线路', cli: 'codex', baseUrl: 'https://gateway.example.com/v1', model: 'gpt-5', provider: 'custom', hasApiKey: true, maskedKey: 'sk-••••9x2a', proxyMode: 'default', hasProxyUrl: false, enabled: true },
]

const completedJob = { id: 'job-1', mode: 'probe', runOnce: true, cli: 'codex', providerId: 'ray', providerName: 'Ray 主线路', model: 'gpt-5', status: 'success', phase: 'probe', latestAttempt: 'success', attempts: 1, startedAt: '2026-07-15T03:51:18Z', endedAt: '2026-07-15T03:51:28Z', elapsedMillis: 10000 }
const runningJob = { ...completedJob, status: 'running', latestAttempt: '', endedAt: undefined, elapsedMillis: 0 }

const redisOverview = {
  connected: true,
  version: '7.4.2',
  keyCount: 3,
  expiringKeys: 1,
  usedMemoryBytes: 1048576,
  usedMemoryHuman: '1 MiB',
  maxMemoryBytes: 402653184,
  hitRate: 0.982,
  latencyMs: 1,
  uptimeSeconds: 86400,
}

const redisKeys = {
  cursor: '0',
  nextCursor: '0',
  keys: [
    { key: 'ai-watch:settings', type: 'string', sizeBytes: 512, ttlMillis: -1, persistent: true },
    { key: 'ai-watch:provider:manual:codex-ray', type: 'hash', sizeBytes: 1024, ttlMillis: -1, persistent: true },
    { key: 'ai-watch:events:recent', type: 'list', sizeBytes: 4096, ttlMillis: 3600000, persistent: false },
  ],
}

const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })

export type ApiMock = {
  settingsWrites: Array<Record<string, unknown>>
  reliabilityRanges: string[]
  unmatched: string[]
  consoleErrors: string[]
  delayNextRedisRefresh: () => Promise<() => void>
  failNextRedisRefresh: () => void
}

export async function installApiMock(page: Page): Promise<ApiMock> {
  const settingsWrites: Array<Record<string, unknown>> = []
  const reliabilityRanges: string[] = []
  const unmatched: string[] = []
  const consoleErrors: string[] = []
  let delayRedis = false
  let releaseRedis: (() => void) | undefined
  let redisRequestStarted: (() => void) | undefined
  let failRedis = false
  let schedules = [{ id: 'schedule-1', name: '未知状态计划', enabled: true, cli: 'codex', providerId: '', providerName: '当前 Codex 配置', mode: 'probe', timezone: 'Asia/Shanghai', weekdaysMask: 62, startMinute: 540, endMinute: 1080, untilSuccess: true, timeoutSeconds: 15, retryIntervalSeconds: 3, keepaliveIntervalSeconds: 120, failureThreshold: 3, lastStatus: 'future_status', lastJobId: 'job-1', nextRunAt: '2026-07-16T01:00:00Z' }]

  page.on('console', message => {
    if (message.type() === 'error') consoleErrors.push(message.text())
  })
  page.on('pageerror', error => consoleErrors.push(error.message))

  await page.route('**/api/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api/, '')
    const method = request.method()

    if (method === 'GET' && path === '/health') return json(route, { status: 'ok', version: 'test' })
    if (method === 'GET' && path === '/config/status') return json(route, { codexCli: true, claudeCli: true, sqliteCli: true, codexConfig: true, claudeConfig: true, ccSwitchDb: true })
    if (method === 'GET' && path === '/providers') return json(route, providers)
    if (method === 'GET' && path === '/jobs') return json(route, [completedJob])
    if (method === 'GET' && path === '/jobs/job-1') return json(route, completedJob)
    if (method === 'GET' && path === '/jobs/job-1/events') return route.fulfill({ status: 200, contentType: 'text/event-stream', body: [
      `event: request_start\ndata: ${JSON.stringify({ id: 1, type: 'request_start', at: '2026-07-15T03:51:18Z', message: '请求开始', data: { requestId: 'req-1', cli: 'codex', model: 'gpt-5', target: 'https://gateway.example.com/v1', proxyMode: 'default', job: runningJob } })}\n\n`,
      `event: request_log\ndata: ${JSON.stringify({ id: 2, type: 'request_log', at: '2026-07-15T03:51:27Z', message: 'READY\\n', data: { requestId: 'req-1', job: runningJob } })}\n\n`,
      `event: request_end\ndata: ${JSON.stringify({ id: 3, type: 'request_end', at: '2026-07-15T03:51:28Z', message: '请求结束', data: { requestId: 'req-1', status: 'success', durationMillis: 10000, exitCode: 0, responseExcerpt: 'READY', job: completedJob } })}\n\n`,
    ].join('') })
    if (method === 'GET' && path === '/settings') return json(route, settings)
    if (method === 'PUT' && path === '/settings') {
      const body = request.postDataJSON() as Record<string, unknown>
      settingsWrites.push(body)
      Object.assign(settings, body)
      return json(route, settings)
    }
    if (method === 'GET' && path === '/manual-providers') return json(route, manualProviders)
    if (method === 'GET' && path === '/provider-examples') return json(route, [{ id: 'codex-compatible', name: 'Codex Compatible', cli: 'codex', baseUrl: 'https://api.example.com/v1', model: 'gpt-5', provider: 'custom', description: '白色主题示例卡片' }])
    if (method === 'GET' && path === '/notifications/dingtalk/config') return json(route, { configured: false, source: 'none' })
    if (method === 'GET' && path === '/events') {
      const scheduleId = url.searchParams.get('scheduleId')
      const events = scheduleId === 'schedule-1' ? [
        { id: '201', at: '2026-07-15T03:51:18Z', type: 'request_start', level: 'info', providerId: 'ray', jobId: 'job-1', data: { scheduleId, requestId: 'req-schedule-1', cli: 'codex', model: 'gpt-5', target: 'https://gateway.example.com/v1' } },
        { id: '202', at: '2026-07-15T03:51:28Z', type: 'request_end', level: 'success', providerId: 'ray', jobId: 'job-1', data: { scheduleId, requestId: 'req-schedule-1', status: 'success', durationMillis: 10000, exitCode: 0, responseExcerpt: 'READY' } },
      ] : []
      const type = url.searchParams.get('type')
      const filtered = type ? events.filter(event => event.type === type) : events
      return json(route, { events: filtered, total: filtered.length })
    }
    if (method === 'GET' && path === '/schedules') return json(route, { schedules, total: schedules.length, limit: 200 })
    if (method === 'POST' && path === '/schedules') {
      const body = request.postDataJSON() as Record<string, unknown>
      const created = { ...body, id: 'schedule-created', providerName: '当前 Codex 配置', lastStatus: 'idle', nextRunAt: '2026-07-16T02:00:00Z' }
      schedules = [...schedules, created as typeof schedules[number]]
      return json(route, created, 201)
    }
    if (method === 'GET' && path === '/diagnostics') return json(route, { status: 'ok', generatedAt: '2026-07-15T04:00:00Z', clis: [{ id: 'codex', name: 'Codex CLI', available: true, pathLabel: 'codex', version: '1.0.0', checkState: 'ok' }, { id: 'claude', name: 'Claude Code CLI', available: true, pathLabel: 'claude', version: '1.0.0', checkState: 'ok' }], storage: { available: true, backend: 'redis', schemaVersion: 9, logicalBytes: 1024, eventCount: 20, scheduleCount: schedules.length }, proxy: { configured: false, available: false, checkState: 'not_configured' }, runtime: { activeJobs: 0, activeJobsLimit: 8, directoryEntries: 0, directoryReady: true }, config: { hotReload: [], restartRequired: [] } })
    if (method === 'GET' && path === '/reliability') {
      const range = url.searchParams.get('range') || '24h'
      reliabilityRanges.push(range)
      const count = range === '24h' ? 24 : range === '7d' ? 28 : 30
      const step = range === '24h' ? 3600000 : range === '7d' ? 21600000 : 86400000
      const end = Date.parse('2026-07-15T12:00:00Z')
      const buckets = Array.from({ length: count }, (_, index) => ({ start: new Date(end - (count - index) * step).toISOString(), requests: index % 5, successes: index % 5 === 0 ? 0 : index % 5 - 1, failures: index % 5 === 0 ? 0 : 1, stopped: 0, successRate: index % 5 === 0 ? undefined : (index % 5 - 1) / (index % 5), averageDurationMillis: 820 + index * 10 }))
      return json(route, {
        range, generatedAt: new Date(end).toISOString(), coverage: { requestedStart: buckets[0].start, end: new Date(end).toISOString(), retentionDays: 30, partial: false, sampleCount: 42 },
        overall: { requests: 42, completed: 40, successRate: .925, averageDurationMillis: 940, p95DurationMillis: 1800, maxConsecutiveFailures: 2, consecutiveFailures: 0, counts: { success: 37, timeout: 1, overloaded: 2, unmatched: 0, fatal: 0, startFailed: 0, stopped: 2 } },
        providers: [
          { key: 'codex:ray', cli: 'codex', providerId: 'ray', name: 'Ray 主线路', model: 'gpt-5', historical: false, lastStatus: 'success', recommendation: { level: 'recommended', title: '推荐主线路', reasons: ['成功率 96%，当前无连续失败'], action: '可优先用于测活、保活和计划任务' }, metrics: { requests: 24, completed: 24, successRate: .958, averageDurationMillis: 780, p95DurationMillis: 1200, maxConsecutiveFailures: 1, consecutiveFailures: 0, counts: { success: 23, timeout: 0, overloaded: 1, unmatched: 0, fatal: 0, startFailed: 0, stopped: 0 } } },
          { key: 'claude:main', cli: 'claude', providerId: 'main', name: 'Claude 主线路', model: 'claude-sonnet-4', historical: false, lastStatus: 'overloaded', recommendation: { level: 'observe', title: '建议观察', reasons: ['成功率 88%，低于 90%'], action: '保留为备用线路，并持续观察下一时间窗' }, metrics: { requests: 18, completed: 16, successRate: .875, averageDurationMillis: 1180, p95DurationMillis: 2400, maxConsecutiveFailures: 2, consecutiveFailures: 1, counts: { success: 14, timeout: 1, overloaded: 1, unmatched: 0, fatal: 0, startFailed: 0, stopped: 2 } } },
        ], buckets, anomalies: buckets.filter(bucket => bucket.failures > 0).slice(0, 3),
      })
    }
    if (method === 'GET' && path === '/redis/overview') {
      if (delayRedis) {
        delayRedis = false
        redisRequestStarted?.()
        await new Promise<void>(resolve => { releaseRedis = resolve })
      }
      if (failRedis) {
        failRedis = false
        return json(route, { message: '模拟 Redis 连接中断' }, 503)
      }
      return json(route, redisOverview)
    }
    if (method === 'GET' && path === '/redis/keys') return json(route, redisKeys)
    if (method === 'GET' && path === '/redis/keys/detail') return json(route, {
      key: url.searchParams.get('key'), type: 'string', sizeBytes: 512, ttlMillis: -1, persistent: true,
      version: 'v1', value: '{"uiTheme":"deep-ocean"}', encoding: 'json', cursor: '0', nextCursor: '0',
    })

    unmatched.push(`${method} ${path}`)
    return json(route, { message: `E2E API Mock 未覆盖 ${method} ${path}` }, 501)
  })

  return {
    settingsWrites,
    reliabilityRanges,
    unmatched,
    consoleErrors,
    delayNextRedisRefresh: () => {
      delayRedis = true
      return new Promise(resolve => { redisRequestStarted = () => resolve(() => releaseRedis?.()) })
    },
    failNextRedisRefresh: () => { failRedis = true },
  }
}
