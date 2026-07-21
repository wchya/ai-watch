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
  reliabilityDigestEnabled: false,
  reliabilityDigestHour: 9,
  reliabilityDigestMinute: 0,
  reliabilityDigestTimezone: 'Asia/Shanghai',
  reliabilityDigestRange: '24h',
  dingTalkConfigured: false,
  uiTheme: 'deep-ocean' as Theme,
}

const providers = [
  { id: '', name: '当前 Codex 配置', cli: 'codex', current: true, enabled: true, model: 'gpt-5', state: { status: 'idle', attempts: 0, scheduleEnabled: false } },
  { id: 'cc-switch:claude-main', name: 'Claude 主线路', cli: 'claude', current: false, enabled: true, model: 'claude-sonnet-4', baseUrl: 'https://claude.example.com', state: { status: 'success', attempts: 2, scheduleEnabled: true, scheduleName: '工作日巡检' } },
  { id: 'cc-switch:claude-backup', name: 'Claude 备用线路', cli: 'claude', current: false, enabled: true, model: 'claude-sonnet-4', baseUrl: 'https://claude-backup.example.com', state: { status: 'idle', attempts: 0, scheduleEnabled: false } },
]

const manualProviders = [
  { id: 'codex-ray', name: 'Ray 主线路', cli: 'codex', baseUrl: 'https://gateway.example.com/v1', model: 'gpt-5', provider: 'custom', hasApiKey: true, maskedKey: 'sk-••••9x2a', proxyMode: 'default', hasProxyUrl: false, enabled: true },
]

const completedJob = { id: 'job-1', mode: 'probe', runOnce: true, cli: 'codex', providerId: 'ray', providerName: 'Ray 主线路', model: 'gpt-5', status: 'success', phase: 'probe', latestAttempt: 'success', attempts: 1, startedAt: '2026-07-15T03:51:18Z', endedAt: '2026-07-15T03:51:28Z', elapsedMillis: 10000 }
const runningJob = { ...completedJob, status: 'running', latestAttempt: '', endedAt: undefined, elapsedMillis: 0 }
const secondJob = { ...completedJob, id: 'job-2', cli: 'claude', providerId: 'cc-switch:claude-main', providerName: 'Claude 主线路', model: 'claude-sonnet-4' }
const secondRunningJob = { ...secondJob, status: 'running', latestAttempt: '', endedAt: undefined, elapsedMillis: 0 }

const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })

export type ApiMock = {
  settingsWrites: Array<Record<string, unknown>>
  reliabilityRanges: string[]
  reliabilityExports: string[]
  reliabilityDigestSends: number
  reliabilityActions: string[]
  providerGroupActions: string[]
  bulkActions: string[]
  stopJobCalls: string[]
  unmatched: string[]
  consoleErrors: string[]
  failNextActionCenter: () => void
  failNextJobRead: () => void
  failNextProxyApply: () => void
  setJobStatus: (status: 'running' | 'success' | 'stopped') => void
  seedSchedules: (count: number) => void
  seedComparisons: (count: number) => void
}

export async function installApiMock(page: Page): Promise<ApiMock> {
  const settingsWrites: Array<Record<string, unknown>> = []
  const reliabilityRanges: string[] = []
  const reliabilityExports: string[] = []
  let reliabilityDigestSends = 0
  const bulkActions: string[] = []
  const stopJobCalls: string[] = []
  const reliabilityActions: string[] = []
  const providerGroupActions: string[] = []
  const unmatched: string[] = []
  const consoleErrors: string[] = []
  let failActionCenter = 0
  let failJobReads = 0
  let failProxyApply = 0
  let currentJob = { ...completedJob }
  let schedules = [{ id: 'schedule-1', name: '未知状态计划', enabled: true, cli: 'codex', providerId: '', providerName: '当前 Codex 配置', mode: 'probe', timezone: 'Asia/Shanghai', weekdaysMask: 62, startMinute: 540, endMinute: 1080, untilSuccess: true, timeoutSeconds: 15, retryIntervalSeconds: 3, keepaliveIntervalSeconds: 120, failureThreshold: 3, lastStatus: 'future_status', lastJobId: 'job-1', nextRunAt: '2026-07-16T01:00:00Z' }, { id: 'schedule-running', name: '运行中计划', enabled: true, cli: 'codex', providerId: '', providerName: '当前 Codex 配置', mode: 'keepalive', timezone: 'Asia/Shanghai', weekdaysMask: 127, startMinute: 0, endMinute: 1440, untilSuccess: false, timeoutSeconds: 15, retryIntervalSeconds: 3, keepaliveIntervalSeconds: 120, failureThreshold: 3, lastStatus: 'running', lastJobId: 'job-1', nextRunAt: '2026-07-16T01:00:00Z' }, { id: 'schedule-claude-risk', name: 'Claude 异常巡检', enabled: true, cli: 'claude', providerId: 'cc-switch:claude-main', providerGroupId: 'claude-main', providerName: 'Claude 主线路', mode: 'probe', timezone: 'Asia/Shanghai', weekdaysMask: 127, startMinute: 0, endMinute: 1440, untilSuccess: true, timeoutSeconds: 15, retryIntervalSeconds: 3, keepaliveIntervalSeconds: 120, failureThreshold: 3, lastStatus: 'failed', lastJobId: 'job-2', nextRunAt: '2026-07-16T01:30:00Z' }, { id: 'schedule-advisory', name: 'Claude 建议组巡检', enabled: true, cli: 'claude', providerId: 'cc-switch:claude-main', providerGroupId: 'claude-advisory', providerName: 'Claude 建议切换组', mode: 'probe', timezone: 'Asia/Shanghai', weekdaysMask: 127, startMinute: 0, endMinute: 1440, untilSuccess: true, timeoutSeconds: 15, retryIntervalSeconds: 3, keepaliveIntervalSeconds: 120, failureThreshold: 3, lastStatus: 'idle', nextRunAt: '2026-07-16T01:45:00Z' }]
  const initialMaintenanceUntil = new Date(Date.now() + 60 * 60_000).toISOString()
  let providerGroups: Array<Record<string, any>> = [{ id: 'claude-main', name: 'Claude 主备组', cli: 'claude', enabled: true, primaryProviderId: 'cc-switch:claude-main', backupProviderIds: ['cc-switch:claude-backup'], scenarioId: 'basic-ready', failureThreshold: 3, cooldownSeconds: 600, mode: 'automatic', activeProviderId: 'cc-switch:claude-backup', recoveryThreshold: 2, recoveryProbeIntervalSeconds: 300, lastRecoveryProbeAt: '2026-07-15T11:55:00Z', lastRecoveryProbeStatus: 'success', maintenanceUntil: initialMaintenanceUntil }, { id: 'claude-advisory', name: 'Claude 建议切换组', cli: 'claude', enabled: true, primaryProviderId: 'cc-switch:claude-main', backupProviderIds: ['cc-switch:claude-backup'], scenarioId: 'basic-ready', failureThreshold: 3, cooldownSeconds: 600, mode: 'advisory', activeProviderId: 'cc-switch:claude-main', recoveryThreshold: 2, recoveryProbeIntervalSeconds: 300, advice: { status: 'open', suggestedProviderId: 'cc-switch:claude-backup', validationJobId: 'validation-job', validationRequestId: 'validation-request', reason: '备用线路已通过基础可用性场景', createdAt: '2026-07-16T01:00:00Z', updatedAt: '2026-07-16T01:01:00Z' } }]
  let incidents: Array<Record<string, any>> = [{ id: 'incident-1', subjectType: 'group', subjectId: 'claude-main', subjectName: 'Claude 主备组', providerId: 'cc-switch:claude-main', groupId: 'claude-main', title: 'Claude 主备组 请求连续失败', status: 'open', severity: 'critical', failureCount: 3, errorCounts: { timeout: 2, overloaded: 1 }, jobIds: ['job-1'], requestIds: ['req-schedule-1'], timeline: [{ id: 'entry-1', at: '2026-07-15T03:51:28Z', type: 'failure', message: '供应商请求超时', requestId: 'req-schedule-1', jobId: 'job-1' }], startedAt: '2026-07-15T03:50:00Z', updatedAt: '2026-07-15T03:51:28Z' }]
  let postmortem: Record<string, any> | null = null
  let notificationChannels: Array<Record<string, any>> = []
  let notificationRoutes: Record<string,string> = { incident_opened:'', incident_recovered:'', reliability_alert:'', reliability_recovered:'', reliability_digest:'', job_notification:'' }
  let proxySubscription: Record<string, any> = { configured: false, applied: false, nodeCount: 0 }
  let scenarioComparison: Record<string, any> | null = { id: 'comparison-1', scenarioId: 'basic-ready', scenarioName: '基础可用性', cli: 'claude', status: 'completed', createdAt: '2026-07-15T12:00:00Z', items: [{ providerId: 'cc-switch:claude-main', providerName: 'Claude 主线路', jobId: 'comparison-job-1', requestId: 'req-comparison-1', status: 'success', durationMillis: 700, responseExcerpt: 'READY', startedAt: '2026-07-15T12:00:00Z', endedAt: '2026-07-15T12:00:01Z' }, { providerId: 'cc-switch:claude-backup', providerName: 'Claude 备用线路', jobId: 'comparison-job-2', requestId: 'req-comparison-2', status: 'success', durationMillis: 900, responseExcerpt: 'READY', startedAt: '2026-07-15T12:00:00Z', endedAt: '2026-07-15T12:00:01Z' }] }
  let rerunComparison: Record<string, any> | null = null
  let comparisonListOverride: Array<Record<string, any>> | null = null

  page.on('console', message => {
    if (message.type() === 'error' && message.text() !== 'Failed to load resource: net::ERR_CONNECTION_CLOSED') consoleErrors.push(message.text())
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
    if (method === 'GET' && path === '/jobs') return json(route, [currentJob])
    if (method === 'POST' && path === '/jobs') {
      currentJob = { ...runningJob }
      return json(route, currentJob, 202)
    }
    if (method === 'GET' && path === '/jobs/job-1') {
      if (failJobReads > 0) {
        failJobReads--
        return json(route, { error: { code: 'job_not_found', message: '最近任务已不可用，可等待下一次运行' } }, 404)
      }
      return json(route, currentJob)
    }
    if (method === 'GET' && path === '/jobs/job-2') return json(route, secondJob)
    if (method === 'POST' && path === '/jobs/job-1/stop') {
      stopJobCalls.push('job-1')
      currentJob = { ...currentJob, status: 'stopped', latestAttempt: 'stopped', endedAt: '2026-07-15T03:51:29Z', elapsedMillis: 11000 }
      schedules = schedules.map(schedule => schedule.lastJobId === 'job-1' && schedule.lastStatus === 'running' ? { ...schedule, lastStatus: 'stopped' } : schedule)
      return json(route, { accepted: true }, 202)
    }
    if (method === 'GET' && path === '/provider-groups') return json(route, providerGroups)
    if (method === 'GET' && path === '/maintenance-windows') return json(route, providerGroups.map(group => { const starts = group.maintenanceStartsAt ? new Date(group.maintenanceStartsAt).getTime() : undefined; const until = group.maintenanceUntil ? new Date(group.maintenanceUntil).getTime() : undefined; const now = Date.now(); const status = !until ? 'none' : until <= now ? 'ended' : starts && starts > now ? 'scheduled' : 'active'; return { groupId: group.id, groupName: group.name, cli: group.cli, mode: group.mode || 'advisory', activeProviderId: group.activeProviderId || group.primaryProviderId, maintenanceStartsAt: group.maintenanceStartsAt, maintenanceUntil: group.maintenanceUntil, status, notificationsMuted: status === 'active', failoverSuppressed: status === 'active' } }))
    if (method === 'POST' && path.startsWith('/maintenance-windows/')) {
      const [, , groupId, action] = path.split('/')
      const body = (request.postDataJSON() || {}) as { startsAt?: string; until?: string; seconds?: number }
      bulkActions.push(`maintenance:${action}`)
      providerGroups = providerGroups.map(group => {
        if (group.id !== groupId) return group
        if (action === 'end') return { ...group, maintenanceStartsAt: undefined, maintenanceUntil: undefined }
        if (action === 'extend') { const base = Math.max(Date.now(), group.maintenanceUntil ? new Date(group.maintenanceUntil).getTime() : 0); return { ...group, maintenanceStartsAt: group.maintenanceStartsAt || new Date().toISOString(), maintenanceUntil: new Date(base + (body.seconds || 0) * 1000).toISOString() } }
        return { ...group, maintenanceStartsAt: body.startsAt || new Date().toISOString(), maintenanceUntil: body.until }
      })
      const group = providerGroups.find(value => value.id === groupId)!
      const starts = group.maintenanceStartsAt ? new Date(group.maintenanceStartsAt).getTime() : undefined; const until = group.maintenanceUntil ? new Date(group.maintenanceUntil).getTime() : undefined; const now = Date.now(); const status = !until ? 'none' : until <= now ? 'ended' : starts && starts > now ? 'scheduled' : 'active'
      return json(route, { groupId: group.id, groupName: group.name, cli: group.cli, mode: group.mode || 'advisory', activeProviderId: group.activeProviderId || group.primaryProviderId, maintenanceStartsAt: group.maintenanceStartsAt, maintenanceUntil: group.maintenanceUntil, status, notificationsMuted: status === 'active', failoverSuppressed: status === 'active' })
    }
    const sloView = (group: Record<string, any>) => { const enabled = Boolean(group.sloEnabled); const failures = enabled ? 1 : 0; const samples = enabled ? 120 : 0; const allowed = samples * (1 - (group.sloTargetPercent || 99.9) / 100); const consumed = allowed ? failures / allowed * 100 : 0; const burnRate = samples ? (failures / samples) / (1 - (group.sloTargetPercent || 99.9) / 100) : 0; return { groupId: group.id, groupName: group.name, cli: group.cli, enabled, targetPercent: group.sloTargetPercent || 99.9, window: group.sloWindow || '7d', minimumSamples: group.sloMinimumSamples || 20, status: !enabled ? 'disabled' : samples < (group.sloMinimumSamples || 20) ? 'insufficient' : allowed - failures <= 0 ? 'exhausted' : consumed >= 90 || burnRate >= 10 ? 'critical' : consumed >= 50 || burnRate >= 2 ? 'burning' : 'healthy', samples, successes: samples - failures, failures, excluded: 2, successRate: samples ? (samples - failures) / samples * 100 : 0, allowedFailures: allowed, remainingBudget: allowed - failures, consumedPercent: consumed, burnRate, windowStartedAt: '2026-07-08T12:00:00Z' } }
    if (method === 'GET' && path === '/slos') return json(route, providerGroups.map(sloView))
    if ((method === 'PUT' || method === 'POST') && path.startsWith('/slos/')) {
      const parts = path.split('/'), groupId = parts[2], action = parts[3] || 'configure', body = (request.postDataJSON() || {}) as Record<string, any>
      bulkActions.push(`slo:${action}`)
      providerGroups = providerGroups.map(group => group.id !== groupId ? group : action === 'pause' ? { ...group, sloEnabled: false } : action === 'resume' ? { ...group, sloEnabled: true } : { ...group, sloEnabled: true, sloTargetPercent: body.targetPercent, sloWindow: body.window, sloMinimumSamples: body.minimumSamples })
      return json(route, sloView(providerGroups.find(group => group.id === groupId)!))
    }
    if (method === 'POST' && path === '/scenario-comparisons') {
      const body = request.postDataJSON() as { scenarioId: string; cli: string; providerIds: string[] }
      bulkActions.push('scenario_comparison')
      scenarioComparison = { id: 'comparison-1', scenarioId: body.scenarioId, scenarioName: '基础可用性', cli: body.cli, status: 'running', createdAt: '2026-07-15T12:00:00Z', items: body.providerIds.map((providerId, index) => ({ providerId, providerName: providers.find(provider => provider.id === providerId && provider.cli === body.cli)?.name || (providerId ? providerId : `当前 ${body.cli === 'codex' ? 'Codex' : 'Claude'} 配置`), jobId: `comparison-job-${index + 1}`, status: 'running' })) }
      return json(route, scenarioComparison, 202)
    }
    if (method === 'GET' && path === '/scenario-comparisons') { const items = comparisonListOverride ?? [rerunComparison, scenarioComparison].filter(Boolean); return json(route, { items, total: items.length, retentionLimited: items.length >= 500 }) }
    if (method === 'GET' && path === '/scenario-comparisons/comparison-1' && scenarioComparison) {
      scenarioComparison = { ...scenarioComparison, status: 'completed', items: (scenarioComparison.items as Array<Record<string, any>>).map((item, index) => ({ ...item, requestId: `req-comparison-${index + 1}`, status: 'success', durationMillis: 700 + index * 100, responseExcerpt: 'READY', startedAt: '2026-07-15T12:00:00Z', endedAt: '2026-07-15T12:00:01Z' })) }
      return json(route, scenarioComparison)
    }
    if (method === 'POST' && path === '/scenario-comparisons/comparison-1/rerun' && scenarioComparison) {
      bulkActions.push('scenario_comparison_rerun')
      rerunComparison = { ...scenarioComparison, id: 'comparison-2', status: 'running', createdAt: '2026-07-15T12:10:00Z', items: (scenarioComparison.items as Array<Record<string, any>>).map((item, index) => ({ providerId: item.providerId, providerName: item.providerName, jobId: `rerun-job-${index + 1}`, status: 'running' })) }
      return json(route, rerunComparison, 202)
    }
    if (method === 'GET' && path === '/scenario-comparisons/comparison-2' && rerunComparison) return json(route, rerunComparison)
    if (method === 'GET' && path === '/incidents') { if (failActionCenter > 0) { failActionCenter--; return json(route, { message: '模拟事故事实读取失败' }, 503) }; const status = url.searchParams.get('status'); return json(route, status ? incidents.filter(item => item.status === status) : incidents) }
    if (method === 'GET' && path === '/incidents/incident-1') return json(route, incidents[0])
    if (method === 'GET' && path === '/incidents/incident-1/postmortem') return postmortem ? json(route, postmortem) : json(route, { error: { message: 'postmortem not found' } }, 404)
    if (method === 'POST' && path === '/incidents/incident-1/postmortem') { postmortem ||= { incidentId: 'incident-1', status: 'draft', title: incidents[0].title, subject: incidents[0].subjectName, severity: 'critical', startedAt: incidents[0].startedAt, durationSeconds: 88, failureCount: 3, errorCounts: incidents[0].errorCounts, jobIds: incidents[0].jobIds, requestIds: incidents[0].requestIds, timeline: incidents[0].timeline, recoverySummary: '事故尚未恢复', rootCause: '待补充根因分析', mitigation: '待补充处置总结', owner: '', actions: [], createdAt: '2026-07-15T12:00:00Z', updatedAt: '2026-07-15T12:00:00Z' }; bulkActions.push('postmortem:create'); return json(route, postmortem, 201) }
    if (method === 'PUT' && path === '/incidents/incident-1/postmortem' && postmortem) { postmortem = { ...postmortem, ...(request.postDataJSON() as Record<string, any>), updatedAt: '2026-07-15T12:01:00Z' }; bulkActions.push('postmortem:save'); return json(route, postmortem) }
    if (method === 'POST' && path === '/incidents/incident-1/postmortem/complete' && postmortem) { postmortem = { ...postmortem, status: 'completed', completedAt: '2026-07-15T12:02:00Z' }; bulkActions.push('postmortem:complete'); return json(route, postmortem) }
    if (method === 'POST' && path === '/incidents/incident-1/postmortem/reopen' && postmortem) { postmortem = { ...postmortem, status: 'draft', completedAt: undefined }; bulkActions.push('postmortem:reopen'); return json(route, postmortem) }
    if (method === 'GET' && path === '/incidents/incident-1/postmortem/markdown' && postmortem) return route.fulfill({ status: 200, contentType: 'text/markdown; charset=utf-8', body: `# 事故复盘：${postmortem.title}\n\n## 根因\n\n${postmortem.rootCause}` })
    if (method === 'POST' && path.startsWith('/incidents/incident-1/')) { const action = path.split('/').at(-1); incidents = incidents.map(item => item.id !== 'incident-1' ? item : { ...item, status: action === 'acknowledge' ? 'acknowledged' : action === 'mute' ? 'muted' : action === 'close' ? 'resolved' : action === 'reopen' ? 'open' : item.status, note: action === 'note' ? (request.postDataJSON() as { note: string }).note : item.note, acknowledgedAt: action === 'acknowledge' ? '2026-07-15T12:00:00Z' : item.acknowledgedAt, mutedUntil: action === 'mute' ? '2026-07-15T13:00:00Z' : item.mutedUntil, resolvedAt: action === 'close' ? '2026-07-15T12:00:00Z' : action === 'reopen' ? undefined : item.resolvedAt, timeline: [...item.timeline, { id: `manual-${action}`, at: '2026-07-15T12:00:00Z', type: `manual_${action}`, message: action === 'acknowledge' ? '事故已确认' : '事故操作已完成' }] }); return json(route, incidents[0]) }
    if (method === 'POST' && path === '/provider-groups') { const body = request.postDataJSON() as typeof providerGroups[number]; providerGroups = [...providerGroups.filter(group => group.id !== body.id), body]; return json(route, body) }
    if (method === 'DELETE' && path === '/provider-groups') { const id = url.searchParams.get('id'); providerGroups = providerGroups.filter(group => group.id !== id); return json(route, { deleted: true, id }) }
    if (method === 'POST' && path === '/provider-groups/claude-main/evaluate') { providerGroups = providerGroups.map(group => group.id === 'claude-main' ? { ...group, advice: { status: 'validating', suggestedProviderId: 'cc-switch:claude-backup', validationJobId: 'job-1', reason: '正在使用相同合成场景验证第一优先级备用线路', createdAt: '2026-07-15T12:00:00Z', updatedAt: '2026-07-15T12:00:00Z' } } : group); return json(route, { groupId: 'claude-main', mode: 'automatic', activeProviderId: 'cc-switch:claude-backup', candidateProviderId: 'cc-switch:claude-backup', recommendation: 'validating', job: completedJob, hostConfigChanged: false }, 202) }
    if (method === 'POST' && path === '/provider-groups/claude-advisory/apply-advice') { providerGroupActions.push('apply_advice'); providerGroups = providerGroups.map(group => group.id === 'claude-advisory' ? { ...group, activeProviderId: 'cc-switch:claude-backup', advice: { ...group.advice, status: 'applied', appliedAt: '2026-07-16T01:02:00Z', updatedAt: '2026-07-16T01:02:00Z' } } : group); return json(route, { groupId: 'claude-advisory', previousProviderId: 'cc-switch:claude-main', activeProviderId: 'cc-switch:claude-backup', validationRequestId: 'validation-request', affectedScheduleCount: 1, switched: true, hostConfigChanged: false }) }
    if (method === 'POST' && path === '/jobs/bulk') {
      const body = request.postDataJSON() as { action: string; items: Array<{ targetId: string; cli?: string; providerId?: string; scenarioId?: string }> }
      bulkActions.push(body.action)
      if (body.action === 'stop') schedules = schedules.map(schedule => schedule.id === 'schedule-running' ? { ...schedule, lastStatus: 'stopped' } : schedule)
      return json(route, { accepted: body.items.length, failed: 0, results: body.items.map((item, index) => ({ targetId: item.targetId, ok: true, job: body.action === 'stop' ? undefined : { ...completedJob, id: `bulk-job-${index + 1}`, cli: item.cli || 'codex', providerId: item.providerId || '', providerName: providers.find(provider => provider.id === item.providerId && provider.cli === item.cli)?.name || '测试线路', scenarioId: item.scenarioId, scenarioName: item.scenarioId === 'basic-ready' ? '基础可用性' : undefined, elapsedMillis: 700 + index * 100 } })) })
    }
    if (method === 'GET' && path === '/jobs/job-1/events') {
      const active = currentJob.status === 'running'
      const stream = [
        `event: request_start\ndata: ${JSON.stringify({ id: 1, type: 'request_start', at: '2026-07-15T03:51:18Z', message: '请求开始', data: { requestId: 'req-1', cli: 'codex', model: 'gpt-5', target: 'https://gateway.example.com/v1', proxyMode: 'default', job: runningJob } })}\n\n`,
        `event: request_log\ndata: ${JSON.stringify({ id: 2, type: 'request_log', at: '2026-07-15T03:51:27Z', message: 'READY\\n', data: { requestId: 'req-1', job: runningJob } })}\n\n`,
      ]
      if (!active) stream.push(`event: request_end\ndata: ${JSON.stringify({ id: 3, type: 'request_end', at: '2026-07-15T03:51:28Z', message: '请求结束', data: { requestId: 'req-1', status: currentJob.status, durationMillis: 10000, exitCode: 0, responseExcerpt: 'READY', job: currentJob } })}\n\n`)
      return route.fulfill({ status: 200, contentType: 'text/event-stream', body: stream.join('') })
    }
    if (method === 'GET' && path === '/jobs/job-2/events') {
      const stream = [
        `event: request_start\ndata: ${JSON.stringify({ id: 11, type: 'request_start', at: '2026-07-15T04:01:18Z', message: '请求开始', data: { requestId: 'req-2', cli: 'claude', model: 'claude-sonnet-4', target: 'https://claude.example.com', proxyMode: 'default', job: secondRunningJob } })}\n\n`,
        `event: request_log\ndata: ${JSON.stringify({ id: 12, type: 'request_log', at: '2026-07-15T04:01:27Z', message: 'SECOND_JOB_OUTPUT\\n', data: { requestId: 'req-2', job: secondRunningJob } })}\n\n`,
        `event: request_end\ndata: ${JSON.stringify({ id: 13, type: 'request_end', at: '2026-07-15T04:01:28Z', message: '请求结束', data: { requestId: 'req-2', status: 'success', durationMillis: 10000, exitCode: 0, responseExcerpt: 'SECOND_JOB_OUTPUT', job: secondJob } })}\n\n`,
      ]
      return route.fulfill({ status: 200, contentType: 'text/event-stream', body: stream.join('') })
    }
    if (method === 'GET' && path === '/settings') return json(route, settings)
    if (method === 'PUT' && path === '/settings') {
      const body = request.postDataJSON() as Record<string, unknown>
      settingsWrites.push(body)
      Object.assign(settings, body)
      return json(route, settings)
    }
    if (method === 'GET' && path === '/reliability/digest/preview') return json(route, { title: 'AI Watch 可靠性摘要', content: '### AI Watch 可靠性摘要\n\n- 整体成功率：99.5%\n- P95 延迟：820ms\n\n#### Provider 建议\n- **Ray 主线路**：健康 — 保持当前配置', range: settings.reliabilityDigestRange, generatedAt: '2026-07-15T10:00:00Z' })
    if (method === 'POST' && path === '/reliability/digest/send') {
      reliabilityDigestSends++
      return json(route, { title: 'AI Watch 可靠性摘要', content: '### AI Watch 可靠性摘要\n\n- 整体成功率：99.5%', range: settings.reliabilityDigestRange, generatedAt: '2026-07-15T10:00:00Z' })
    }
    if (method === 'GET' && path === '/manual-providers') return json(route, manualProviders)
    if (method === 'GET' && path === '/test-scenarios') return json(route, [
      { id: 'basic-ready', name: '基础可用性', description: '验证 Provider 能完成最小文本响应。', cli: '', enabled: true, prompt: 'hi，只回复 READY', assertionType: 'contains', expected: 'READY', timeoutSeconds: 15, builtIn: true },
      { id: 'json-object', name: 'JSON 格式遵循', description: '验证模型返回 JSON Object。', cli: '', enabled: true, prompt: '只返回 JSON', assertionType: 'json', timeoutSeconds: 20, builtIn: true },
    ])
    if (method === 'POST' && path === '/test-scenarios') return json(route, JSON.parse(request.postData() || '{}'))
    if (method === 'DELETE' && path === '/test-scenarios') return json(route, { deleted: true, id: url.searchParams.get('id') })
    if (method === 'GET' && path === '/notifications/dingtalk/config') return json(route, { configured: false, source: 'none' })
    if (method === 'GET' && path === '/proxy/subscription') return json(route, proxySubscription)
    if (method === 'PUT' && path === '/proxy/subscription') {
      const body = request.postDataJSON() as { subscriptionUrl: string }
      if (failProxyApply > 0) {
        failProxyApply--
        proxySubscription = { ...proxySubscription, applied: false, errorStage: 'subscription', errorMessage: '订阅未返回可用代理节点', lastCheckedAt: '2026-07-16T09:29:00Z' }
        return json(route, { error: { code: 'subscription_unavailable', message: '订阅未返回可用代理节点' } }, 502)
      }
      proxySubscription = { configured: true, maskedUrl: new URL(body.subscriptionUrl).origin + '/...', applied: true, nodeCount: 3, currentNode: 'Hong Kong Auto', updatedAt: '2026-07-16T09:30:00Z', lastCheckedAt: '2026-07-16T09:30:01Z' }
      bulkActions.push('proxy:save')
      return json(route, proxySubscription)
    }
    if (method === 'POST' && path === '/proxy/test') {
      proxySubscription = { ...proxySubscription, applied: proxySubscription.configured, lastCheckedAt: '2026-07-16T09:31:00Z' }
      bulkActions.push('proxy:test')
      return json(route, proxySubscription)
    }
    if (method === 'DELETE' && path === '/proxy/subscription') {
      proxySubscription = { configured: false, applied: false, nodeCount: 0, lastCheckedAt: '2026-07-16T09:32:00Z' }
      bulkActions.push('proxy:clear')
      return json(route, proxySubscription)
    }
    if (method === 'GET' && path === '/notification-channels') return json(route, notificationChannels)
    if (method === 'POST' && path === '/notification-channels') { const body=request.postDataJSON() as Record<string,any>; const channel={id:body.id||'channel-created',name:body.name,description:body.description,type:'dingtalk',enabled:body.enabled!==false,configured:true,maskedWebhook:'https://oapi.dingtalk.com/***',createdAt:'2026-07-16T01:00:00Z',updatedAt:'2026-07-16T01:00:00Z'};notificationChannels=[...notificationChannels,channel];bulkActions.push('notification-channel:create');return json(route,channel,201) }
    if (method === 'PUT' && path.startsWith('/notification-channels/')) { const id=path.split('/')[2],body=request.postDataJSON() as Record<string,any>;notificationChannels=notificationChannels.map(channel=>channel.id===id?{...channel,...body,webhookUrl:undefined,configured:true}:channel);bulkActions.push('notification-channel:update');return json(route,notificationChannels.find(channel=>channel.id===id)) }
    if (method === 'POST' && path.endsWith('/test') && path.startsWith('/notification-channels/')) { bulkActions.push('notification-channel:test');return json(route,{sent:true,id:path.split('/')[2]}) }
    if (method === 'DELETE' && path.startsWith('/notification-channels/')) { const id=path.split('/')[2];notificationChannels=notificationChannels.filter(channel=>channel.id!==id);notificationRoutes=Object.fromEntries(Object.entries(notificationRoutes).map(([kind,channelId])=>[kind,channelId===id?'':channelId]));bulkActions.push('notification-channel:delete');return json(route,{deleted:true,id}) }
    if (method === 'GET' && path === '/notification-routes') return json(route,{routes:notificationRoutes,updatedAt:'2026-07-16T01:00:00Z'})
    if (method === 'PUT' && path === '/notification-routes') { notificationRoutes=(request.postDataJSON() as {routes:Record<string,string>}).routes;bulkActions.push('notification-routes:save');return json(route,{routes:notificationRoutes,updatedAt:'2026-07-16T01:01:00Z'}) }
    if (method === 'GET' && path === '/requests/req-schedule-1') return json(route, {
      requestId: 'req-schedule-1', jobId: 'job-1', scheduleId: 'schedule-1', providerId: 'ray', attempt: 1, mode: 'probe', phase: 'probe', cli: 'codex', model: 'gpt-5', provider: 'custom', configSource: 'manual', triggerSource: 'scheduler', clientIP: 'scheduler', target: 'https://gateway.example.com/v1', targetHost: 'gateway.example.com', targetPort: '443', dnsIPs: ['203.0.113.10'], proxyMode: 'default', status: 'success', classification: 'success', startedAt: '2026-07-15T03:51:18Z', endedAt: '2026-07-15T03:51:28Z', durationMillis: 10000, exitCode: 0, responseExcerpt: 'READY', input: { promptBytes: 18, promptSHA256: 'abcdef1234567890', timeoutSeconds: 15, runOnce: true, codexRequestRetries: 2, codexStreamRetries: 2 }, complete: true, recommendation: '请求成功，无需重试；可继续观察该 Provider 的可靠性趋势',
    })
    if (method === 'GET' && path.startsWith('/requests/req-comparison-')) return json(route, {
      requestId: path.split('/').at(-1), jobId: 'comparison-job-1', providerId: 'cc-switch:claude-main', attempt: 1, mode: 'probe', phase: 'probe', cli: 'claude', model: 'claude-sonnet-4', provider: 'custom', configSource: 'cc-switch', triggerSource: 'scenario_comparison', clientIP: '127.0.0.1', target: 'https://claude.example.com', targetHost: 'claude.example.com', targetPort: '443', dnsIPs: ['203.0.113.12'], proxyMode: 'default', status: 'success', classification: 'success', startedAt: '2026-07-15T12:00:00Z', endedAt: '2026-07-15T12:00:01Z', durationMillis: 700, exitCode: 0, responseExcerpt: 'READY', input: { promptBytes: 18, promptSHA256: 'abcdef1234567890', timeoutSeconds: 15, runOnce: true }, complete: true, recommendation: '请求成功，无需重试',
    })
    if (method === 'GET' && path === '/events') {
      const scheduleId = url.searchParams.get('scheduleId')
      const events = !scheduleId || scheduleId === 'schedule-1' ? [
        { id: '201', at: '2026-07-15T03:51:18Z', type: 'request_start', level: 'info', providerId: 'ray', jobId: 'job-1', data: { scheduleId, requestId: 'req-schedule-1', cli: 'codex', model: 'gpt-5', target: 'https://gateway.example.com/v1' } },
        { id: '202', at: '2026-07-15T03:51:28Z', type: 'request_end', level: 'success', providerId: 'ray', jobId: 'job-1', data: { scheduleId, requestId: 'req-schedule-1', status: 'success', durationMillis: 10000, exitCode: 0, responseExcerpt: 'READY' } },
      ] : []
      const type = url.searchParams.get('type')
      const filtered = type ? events.filter(event => event.type === type) : events
      return json(route, { events: filtered, total: filtered.length })
    }
    if (method === 'GET' && path === '/schedules') return json(route, { schedules, total: schedules.length, limit: 200 })
    if (method === 'PUT' && path.startsWith('/schedules/')) {
      const id = decodeURIComponent(path.slice('/schedules/'.length))
      const body = request.postDataJSON() as Record<string, unknown>
      let updated: typeof schedules[number] | undefined
      schedules = schedules.map(schedule => schedule.id === id ? (updated = { ...schedule, ...body, lastStatus: body.enabled === false ? 'stopped' : schedule.lastStatus } as typeof schedule) : schedule)
      return updated ? json(route, updated) : json(route, { message: 'not found' }, 404)
    }
    if (method === 'POST' && path === '/schedules') {
      const body = request.postDataJSON() as Record<string, unknown>
      const created = { ...body, id: 'schedule-created', providerName: '当前 Codex 配置', lastStatus: 'idle', nextRunAt: '2026-07-16T02:00:00Z' }
      schedules = [...schedules, created as typeof schedules[number]]
      return json(route, created, 201)
    }
    if (method === 'GET' && path === '/diagnostics') return json(route, { status: 'ok', generatedAt: '2026-07-15T04:00:00Z', clis: [{ id: 'codex', name: 'Codex CLI', available: true, pathLabel: 'codex', version: '1.0.0', checkState: 'ok' }, { id: 'claude', name: 'Claude Code CLI', available: true, pathLabel: 'claude', version: '1.0.0', checkState: 'ok' }], storage: { available: true, backend: 'redis', schemaVersion: 9, logicalBytes: 1024, eventCount: 20, scheduleCount: schedules.length }, proxy: { configured: false, available: false, checkState: 'not_configured' }, runtime: { activeJobs: 0, activeJobsLimit: 8, directoryEntries: 0, directoryReady: true }, config: { hotReload: [], restartRequired: [] } })
    if (method === 'GET' && path === '/reliability/export') {
      const format = url.searchParams.get('format') || 'csv'
      reliabilityExports.push(`${url.searchParams.get('range') || '24h'}:${format}`)
      return route.fulfill({ status: 200, contentType: format === 'json' ? 'application/json' : 'text/csv', headers: { 'Content-Disposition': `attachment; filename="ai-watch-reliability-24h-test.${format}"` }, body: format === 'json' ? '{"range":"24h"}' : 'section,name\noverall,AI Watch\n' })
    }
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
          { key: 'codex:ray', cli: 'codex', providerId: 'ray', name: 'Ray 主线路', model: 'gpt-5', historical: false, lastStatus: 'success', lastRequestId: 'req-schedule-1', recommendation: { level: 'recommended', title: '推荐主线路', reasons: ['成功率 96%，当前无连续失败'], action: '可优先用于测活、保活和计划任务' }, remediation: { canValidateBackup: false, schedules: [] }, metrics: { requests: 24, completed: 24, successRate: .958, averageDurationMillis: 780, p95DurationMillis: 1200, maxConsecutiveFailures: 1, consecutiveFailures: 0, counts: { success: 23, timeout: 0, overloaded: 1, unmatched: 0, fatal: 0, startFailed: 0, stopped: 0 } } },
          { key: 'claude:main', cli: 'claude', providerId: 'cc-switch:claude-main', name: 'Claude 主线路', model: 'claude-sonnet-4', historical: false, lastStatus: 'overloaded', recommendation: { level: 'pause', title: '建议暂停', reasons: ['成功率 88%，低于 90%'], action: '暂停相关计划并验证备用线路' }, remediation: { providerGroupId: 'claude-main', canValidateBackup: true, schedules: schedules.filter(schedule => schedule.id === 'schedule-claude-risk').map(schedule => ({ id: schedule.id, name: schedule.name, enabled: schedule.enabled })) }, metrics: { requests: 18, completed: 16, successRate: .875, averageDurationMillis: 1180, p95DurationMillis: 2400, maxConsecutiveFailures: 2, consecutiveFailures: 1, counts: { success: 14, timeout: 1, overloaded: 1, unmatched: 0, fatal: 0, startFailed: 0, stopped: 2 } } },
        ], buckets, anomalies: buckets.filter(bucket => bucket.failures > 0).slice(0, 3),
      })
    }
    if (method === 'POST' && path === '/reliability/actions') {
      const body = request.postDataJSON() as { action: string; cli: string; providerId: string }
      reliabilityActions.push(`${body.providerId}:${body.action}`)
      if (body.action === 'pause_schedules') {
        schedules = schedules.map(schedule => schedule.id === 'schedule-claude-risk' ? { ...schedule, enabled: false, lastStatus: 'stopped' } : schedule)
        return json(route, { action: body.action, paused: 1, schedules: schedules.filter(schedule => schedule.id === 'schedule-claude-risk') })
      }
      return json(route, { action: body.action, groupId: body.action === 'validate_backup' ? 'claude-main' : undefined, candidateProviderId: body.action === 'validate_backup' ? 'cc-switch:claude-backup' : undefined, job: { ...completedJob, id: `action-${body.action}`, cli: body.cli, providerId: body.providerId } }, 202)
    }
    unmatched.push(`${method} ${path}`)
    return json(route, { message: `E2E API Mock 未覆盖 ${method} ${path}` }, 501)
  })

  return {
    settingsWrites,
    reliabilityRanges,
    reliabilityExports,
    get reliabilityDigestSends() { return reliabilityDigestSends },
    reliabilityActions,
    providerGroupActions,
    bulkActions,
    stopJobCalls,
    unmatched,
    consoleErrors,
    // React development mode can remount the dashboard action center more
    // than once. Keep the simulated partial outage active for the whole test
    // instead of making the assertion depend on an exact request count.
    failNextActionCenter: () => { failActionCenter = 10 },
    failNextJobRead: () => { failJobReads = 1 },
    failNextProxyApply: () => { failProxyApply = 1 },
    setJobStatus: (status) => {
      currentJob = status === 'running'
        ? { ...runningJob }
        : status === 'stopped'
          ? { ...completedJob, status: 'stopped', latestAttempt: 'stopped', endedAt: '2026-07-15T03:51:29Z', elapsedMillis: 11000 }
          : { ...completedJob }
      schedules = schedules.map(schedule => schedule.id === 'schedule-running' ? { ...schedule, lastStatus: status === 'running' ? 'running' : status } : schedule)
    },
    seedSchedules: (count) => {
      const template = schedules[0]
      schedules = Array.from({ length: count }, (_, index) => ({
        ...template,
        id: `schedule-page-${index + 1}`,
        name: `分页计划 ${String(index + 1).padStart(3, '0')}`,
        lastStatus: 'idle',
        lastJobId: undefined,
        nextRunAt: new Date(Date.parse('2026-07-16T01:00:00Z') + index * 60_000).toISOString(),
      })) as typeof schedules
    },
    seedComparisons: (count) => {
      comparisonListOverride = Array.from({ length: count }, (_, index) => ({
        id: index === 0 ? 'comparison-1' : `comparison-page-${index + 1}`,
        scenarioId: 'basic-ready',
        scenarioName: `分页对比 ${String(index + 1).padStart(3, '0')}`,
        cli: 'claude',
        status: 'completed',
        createdAt: new Date(Date.parse('2026-07-15T12:00:00Z') - index * 60_000).toISOString(),
        items: [{ providerId: 'cc-switch:claude-main', providerName: 'Claude 主线路', jobId: `comparison-page-job-${index + 1}`, status: 'success', durationMillis: 700 }],
      }))
    },
  }
}
