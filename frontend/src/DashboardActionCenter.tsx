import { useCallback, useEffect, useMemo, useState } from 'react'
import { Activity, AlertCircle, CalendarClock, CheckCircle2, Clock3, ExternalLink, LoaderCircle, RefreshCw, ShieldAlert, Square, TrendingUp } from 'lucide-react'
import { api } from './api'
import type { DashboardData, Incident, JobSummary, Provider, ReliabilityData, Schedule } from './types'

type ActionItem = {
  id: string
  kind: 'incident' | 'provider' | 'job' | 'schedule'
  priority: number
  title: string
  detail: string
  meta: string
  provider?: Provider
  job?: JobSummary
  requestId?: string
}

const navigate = (path: string) => {
  window.history.pushState({}, '', path)
  window.dispatchEvent(new PopStateEvent('popstate'))
}
const ago = (value?: string) => {
  if (!value) return '刚刚'
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000))
  if (seconds < 60) return `${seconds} 秒前`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`
  return new Date(value).toLocaleDateString('zh-CN')
}

export function DashboardActionCenter({ data, probeProvider, openJob }: { data: DashboardData; probeProvider: (provider: Provider) => void; openJob: (job: JobSummary) => void }) {
  const [incidents, setIncidents] = useState<Incident[]>([])
  const [schedules, setSchedules] = useState<Schedule[]>([])
  const [reliability, setReliability] = useState<ReliabilityData | null>(null)
  const [loading, setLoading] = useState(true)
  const [errors, setErrors] = useState<string[]>([])
  const [operationError, setOperationError] = useState('')
  const [stopping, setStopping] = useState('')
  const [stopped, setStopped] = useState<Set<string>>(new Set())

  const load = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true)
    const results = await Promise.allSettled([api.incidents(), api.schedules(), api.reliability('24h')])
    const nextErrors: string[] = []
    if (results[0].status === 'fulfilled') setIncidents(results[0].value)
    else nextErrors.push('事故')
    if (results[1].status === 'fulfilled') setSchedules(results[1].value.schedules)
    else nextErrors.push('计划')
    if (results[2].status === 'fulfilled') setReliability(results[2].value)
    else nextErrors.push('可靠性')
    setErrors(nextErrors)
    setLoading(false)
  }, [])

  useEffect(() => {
    void load()
    const refresh = () => { if (!document.hidden) void load(true) }
    const timer = window.setInterval(refresh, 15_000)
    const visible = () => { if (!document.hidden) void load(true) }
    document.addEventListener('visibilitychange', visible)
    return () => { window.clearInterval(timer); document.removeEventListener('visibilitychange', visible) }
  }, [load])

  const items = useMemo(() => {
    const result: ActionItem[] = []
    incidents.filter(incident => incident.status !== 'resolved').forEach(incident => result.push({
      id: `incident:${incident.id}`, kind: 'incident', priority: incident.severity === 'critical' ? 100 : 90,
      title: incident.title, detail: `${incident.subjectName || incident.subjectId} · ${incident.failureCount} 次失败`, meta: ago(incident.updatedAt),
    }))
    reliability?.providers.filter(value => value.recommendation.level === 'pause' || value.metrics.consecutiveFailures > 0).forEach(value => {
      const provider = data.providers.find(item => item.cli === value.cli && item.id === value.providerId)
      result.push({ id: `provider:${value.key}`, kind: 'provider', priority: value.recommendation.level === 'pause' ? 85 : 72, title: value.name, detail: value.recommendation.reasons[0] || value.recommendation.action, meta: value.metrics.consecutiveFailures ? `连续失败 ${value.metrics.consecutiveFailures} 次` : ago(value.lastFailureAt), provider, requestId: value.lastRequestId })
    })
    data.providers.filter(provider => ['failed', 'fatal'].includes(provider.state?.status || '') && !result.some(item => item.provider?.cli === provider.cli && item.provider.id === provider.id)).forEach(provider => result.push({ id: `provider-state:${provider.cli}:${provider.id}`, kind: 'provider', priority: 76, title: provider.name, detail: '最近一次测活未通过', meta: provider.state?.consecutiveFailures ? `连续失败 ${provider.state.consecutiveFailures} 次` : ago(provider.state?.lastFailureAt), provider }))
    data.runningJobs.filter(job => !stopped.has(job.id)).forEach(job => result.push({ id: `job:${job.id}`, kind: 'job', priority: 55, title: `${job.providerName || job.providerId || '当前配置'}正在${job.mode === 'probe' ? '测活' : '保活'}`, detail: `${job.cli === 'codex' ? 'Codex' : 'Claude'} · 已尝试 ${job.attemptCount} 次`, meta: ago(job.startedAt), job }))
    schedules.filter(schedule => schedule.enabled && ['failed', 'fatal'].includes(schedule.lastStatus || '')).forEach(schedule => result.push({ id: `schedule:${schedule.id}`, kind: 'schedule', priority: 68, title: schedule.name, detail: `${schedule.providerName || schedule.providerId || '当前配置'} · 最近运行失败`, meta: ago(schedule.lastOccurrenceAt) }))
    schedules.filter(schedule => schedule.enabled && schedule.nextRunAt && !['failed', 'fatal'].includes(schedule.lastStatus || '')).sort((a, b) => new Date(a.nextRunAt!).getTime() - new Date(b.nextRunAt!).getTime()).slice(0, 2).forEach(schedule => result.push({ id: `schedule-next:${schedule.id}`, kind: 'schedule', priority: 35, title: schedule.name, detail: `${schedule.providerName || schedule.providerId || '当前配置'} · 即将自动执行`, meta: new Date(schedule.nextRunAt!).toLocaleString('zh-CN', { hour12: false }) }))
    return result.sort((a, b) => b.priority - a.priority || a.title.localeCompare(b.title, 'zh-CN')).slice(0, 10)
  }, [data.providers, data.runningJobs, incidents, reliability, schedules, stopped])

  const stop = async (job: JobSummary) => {
    if (stopping) return
    setStopping(job.id); setOperationError('')
    try { await api.stopJob(job.id); setStopped(current => new Set(current).add(job.id)) }
    catch (cause) { setOperationError(cause instanceof Error ? cause.message : '停止任务失败') }
    finally { setStopping('') }
  }

  return <section className="panel action-center"><header><div><span>ACTION CENTER</span><strong>需要处理</strong><small>事故、异常线路、运行任务和计划</small></div><button className="action-refresh" disabled={loading} onClick={() => void load()} aria-label="刷新行动中心"><RefreshCw className={loading ? 'spinning' : ''}/></button></header>
    {errors.length > 0 && <div className="action-partial-error"><AlertCircle/><span>{errors.join('、')}数据暂不可用，其他结果仍可操作。</span></div>}
    {operationError && <div className="action-partial-error"><AlertCircle/><span>{operationError}</span></div>}
    {loading && !items.length ? <div className="action-loading"><LoaderCircle className="spinning"/>正在汇总待处理事项</div> : items.length ? <div className="action-list">{items.map(item => <article className={`action-item kind-${item.kind} priority-${item.priority >= 80 ? 'high' : item.priority >= 60 ? 'medium' : 'normal'}`} key={item.id}><span className="action-kind-icon">{item.kind === 'incident' ? <ShieldAlert/> : item.kind === 'provider' ? <TrendingUp/> : item.kind === 'job' ? <Activity/> : <CalendarClock/>}</span><div><strong>{item.title}</strong><span>{item.detail}</span><small><Clock3/>{item.meta}</small></div><nav>{item.kind === 'incident' && <button onClick={() => navigate('/incidents')}>查看事故</button>}{item.kind === 'provider' && item.provider && <button onClick={() => probeProvider(item.provider!)}>立即测活</button>}{item.kind === 'provider' && item.requestId && <button onClick={() => navigate(`/requests/${encodeURIComponent(item.requestId!)}`)}><ExternalLink/>最近请求</button>}{item.kind === 'job' && item.job && <><button onClick={() => openJob(item.job!)}>查看任务</button><button className="action-stop" disabled={!!stopping} onClick={() => void stop(item.job!)}>{stopping === item.job.id ? <LoaderCircle className="spinning"/> : <Square/>}停止</button></>}{item.kind === 'schedule' && <button onClick={() => navigate('/schedules')}>打开计划</button>}</nav></article>)}</div> : <div className="action-clear"><CheckCircle2/><strong>当前没有待处理事项</strong><span>所有线路、任务与计划均处于可接受状态。</span></div>}
  </section>
}
