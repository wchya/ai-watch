import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity, AlertCircle, Bell, BookOpen, Bot, Boxes, CalendarClock, Check, CheckCircle2, ChevronDown, ChevronLeft, ChevronRight,
  CircleDot, Clock3, Command, Copy, Database, ExternalLink, Eye, Filter, FlaskConical, Gauge, GitBranch, History, KeyRound, List, LoaderCircle, Pause,
  Play, Plus, RefreshCw, RotateCcw, Save, Send, Server, Settings, ShieldCheck, Sparkles, Square, Terminal, TimerReset, TrendingUp, Trash2, Wifi, WifiOff, X, Zap,
} from 'lucide-react'
import { api, normalizeEvent } from './api'
import { Select } from './Select'
import { DashboardActionCenter } from './DashboardActionCenter'
import { DingTalkConfigCard } from './DingTalkConfigCard'
import { ProxySubscriptionCard } from './ProxySubscriptionCard'
import type {
  AppSettings, Cli, DashboardData, JobEvent, JobMode, JobOptions, JobStatus, JobSummary, OperationalEvent, Provider,
  StartJobRequest, TestScenario,
} from './types'

const statusMeta: Record<JobStatus, { label: string; tone: string }> = {
  queued: { label: '已排队', tone: 'info' }, starting: { label: '准备中', tone: 'info' }, running: { label: '运行中', tone: 'running' },
  success: { label: '已就绪', tone: 'success' }, fatal: { label: '配置错误', tone: 'danger' },
  stopped: { label: '已停止', tone: 'muted' }, failed: { label: '未通过', tone: 'warning' },
}

const modeLabel = (mode: JobMode) => mode === 'probe' ? '测活' : '保活'
const executionLabel = (mode: JobMode, runOnce?: boolean) => `${runOnce ? '一次' : '持续'}${modeLabel(mode)}`
const phaseLabel = (phase: JobSummary['phase'], mode: JobMode) => phase === 'recovery_probe' ? '恢复测活' : phase === 'keepalive' || mode === 'keepalive' ? '保活观测' : '测活'
const cliLabel = (cli: Cli) => cli === 'codex' ? 'Codex' : 'Claude'
const formatAgo = (iso?: string) => {
  if (!iso) return '—'
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 1000))
  if (seconds < 8) return '刚刚'
  if (seconds < 60) return `${seconds} 秒前`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`
  return new Date(iso).toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })
}
const formatDuration = (ms?: number) => {
  if (ms == null) return '—'
  if (ms < 1000) return `${ms}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
  return `${Math.floor(ms / 60_000)}m ${Math.floor(ms % 60_000 / 1000)}s`
}
  const eventTypeLabels: Record<string, string> = {
  job_state: '任务状态', attempt_start: '开始尝试', classification: '探测判定',
  request_start: '请求开始', request_log: '请求输出', request_end: '请求结束',
  phase: '阶段切换', recovery: '恢复可用', countdown: '等待重试', cleanup: '运行时清理',
}
const eventTypeLabel = (type: string) => eventTypeLabels[type] || type.replaceAll('_', ' ')
const eventLevelLabel = (level?: string) => {
  if (level === 'error' || level === 'fatal') return '错误'
  if (level === 'warning' || level === 'warn') return '警告'
  if (level === 'success') return '成功'
  return '信息'
}
const providerStateLabel = (status?: string) => status === 'recovering' ? '恢复测活' : status === 'running' || status === 'queued' ? '运行中' : status === 'success' ? '最近可用' : status === 'fatal' || status === 'failed' ? '最近失败' : '空闲'
const providerStateTone = (status?: string) => status === 'recovering' ? 'warning' : status === 'running' || status === 'queued' ? 'running' : status === 'success' ? 'success' : status === 'fatal' || status === 'failed' ? 'danger' : 'muted'

export function Logo() {
  return <div className="brand"><div className="brand-mark"><Activity size={19}/></div><div><strong>AI Watch</strong><span>Agent Reliability</span></div></div>
}

function StatusPill({ status }: { status: JobStatus }) {
  const meta = statusMeta[status]
  return <span className={`status-pill ${meta.tone}`}><i />{meta.label}</span>
}

function EmptyState({ icon = <Boxes />, title, detail }: { icon?: React.ReactNode; title: string; detail: string }) {
  return <div className="empty-state"><div className="empty-icon">{icon}</div><strong>{title}</strong><p>{detail}</p></div>
}

function SkeletonCards() {
  return <div className="metric-grid">{[0,1,2,3].map(i => <div className="metric-card skeleton-card" key={i}><i/><i/><i/></div>)}</div>
}

export function Dashboard({ data, loading, error, retry, refreshJobs, openNew, probeProvider, showProviderRequests, openJob }: {
  data: DashboardData | null; loading: boolean; error: string; retry: () => void; refreshJobs: () => void; openNew: () => void; probeProvider: (provider: Provider) => void; showProviderRequests: (provider: Provider) => void; openJob: (job: JobSummary) => void
}) {
  const healthy = data?.health.items.filter(i => i.available).length ?? 0
  const total = data?.health.items.length ?? 0
  const successes = data?.recentJobs.filter(j => j.status === 'success').length ?? 0
  const hasHistory = Boolean(data?.recentJobs.length)
  const successRate = hasHistory ? Math.round(successes / (data?.recentJobs.length ?? 1) * 100) : 0
  return <div className="page dashboard-page">
    <section className="page-heading"><div><span className="eyebrow"><Sparkles/>控制中心</span><h1>让每一次连接，都有迹可循。</h1><p>统一检测 Codex 与 Claude 的服务状态，快速定位配置、限流与连接问题。</p></div><button className="primary hero-action" onClick={openNew}><Zap/>开始测活</button></section>
    {error && <div className="error-banner"><AlertCircle/><div><strong>暂时无法连接后端服务</strong><span>{error}。确认 Docker 容器正在运行后重试。</span></div><button onClick={retry}>重新连接</button></div>}
    {loading && !data ? <SkeletonCards/> : <>
      <section className="system-pulse" aria-label="系统实时状态" aria-live="polite"><div className="pulse-signal"><i/><span>系统脉冲</span><strong>{error ? '连接中断' : '实时在线'}</strong></div><div><span>活跃任务</span><strong>{data?.runningJobs.length ?? 0}</strong></div><div><span>可用环境</span><strong>{healthy}/{total}</strong></div><div><span>最近任务</span><strong>{data?.recentJobs[0] ? statusMeta[data.recentJobs[0].status].label : '暂无记录'}</strong></div></section>
      <section className="metric-grid">
        <Metric icon={<CircleDot/>} label="运行任务" value={String(data?.runningJobs.length ?? 0)} detail={(data?.runningJobs.length ?? 0) ? '正在持续观测' : '当前没有活跃任务'} tone="cyan"/>
        <Metric icon={<CheckCircle2/>} label="最近成功率" value={hasHistory ? `${successRate}%` : '—'} detail={hasHistory ? `基于最近 ${data?.recentJobs.length ?? 0} 次任务` : '暂无任务样本'} tone="green"/>
        <Metric icon={<Server/>} label="运行环境" value={`${healthy}/${total}`} detail={healthy === total && total > 0 ? 'CLI 与配置已就绪' : '部分环境需要检查'} tone="violet"/>
        <Metric icon={<Database/>} label="Provider" value={String(data?.providers.length ?? 0)} detail="当前可用配置源" tone="amber"/>
      </section>
      {data && <DashboardActionCenter data={data} probeProvider={probeProvider} openJob={openJob} onJobsChanged={refreshJobs}/>}
      <section className="content-grid">
        <div className="panel span-2"><PanelTitle title="运行中的任务" detail="实时状态与下一轮计划" action={<button className="text-button" onClick={openNew}><Plus/>添加任务</button>}/>
          <div className="job-list">{data?.runningJobs.length ? data.runningJobs.map(job => <JobRow key={job.id} job={job} open={() => openJob(job)}/>) : <EmptyState icon={<Activity/>} title="一切安静" detail="当前没有运行中的测活或保活任务。"/>}</div>
        </div>
        <div className="panel"><PanelTitle title="环境健康" detail="容器内工具与只读配置"/><div className="health-list">{data?.health.items.map(item => <div className="health-row" key={item.id}><div className={`health-icon ${item.available ? 'ok' : 'bad'}`}>{item.available ? <Check/> : <X/>}</div><div><strong>{item.name}</strong><span>{item.available ? item.version || item.description || '可用' : item.description || '未发现'}</span></div><em>{item.available ? '正常' : '检查'}</em></div>)}</div></div>
      </section>
      <section className="content-grid lower-grid">
        <div className="panel span-2"><PanelTitle title="最近任务" detail="仅保存结果摘要，不包含任何原始日志"/>
          <div className="recent-table"><div className="table-head"><span>任务</span><span>结果</span><span>尝试</span><span>耗时</span><span>时间</span></div>{data?.recentJobs.length ? data.recentJobs.map(job => <button className="table-row" key={job.id} onClick={() => openJob(job)}><span className="job-identity"><CliIcon cli={job.cli}/><span><strong>{cliLabel(job.cli)} · {modeLabel(job.mode)}</strong><small>{job.providerName || job.providerId || '当前配置'}</small></span></span><span><StatusPill status={job.status}/></span><span>{job.attemptCount}</span><span>{formatDuration(job.elapsedMs)}</span><span>{formatAgo(job.endedAt || job.startedAt)}</span></button>) : <EmptyState title="暂无历史摘要" detail="完成任务后，结果摘要会出现在这里。"/>}</div>
        </div>
        <div className="panel"><PanelTitle title="本地供应商" detail="按客户端分类，密钥仅展示脱敏信息"/><div className="provider-categories">{data?.providers.length ? <><ProviderGroup cli="codex" providers={data.providers.filter(provider => provider.cli === 'codex')} probeProvider={probeProvider} showRequests={showProviderRequests}/><ProviderGroup cli="claude" providers={data.providers.filter(provider => provider.cli === 'claude')} probeProvider={probeProvider} showRequests={showProviderRequests}/></> : <EmptyState icon={<Database/>} title="暂无本地供应商" detail="挂载 Codex、Claude 配置，或重启应用同步 CC Switch Provider 后会显示在这里。"/>}</div></div>
      </section>
    </>}
  </div>
}

export function EventsView({ providers, refreshToken, initialProviderId, openRequest }: { providers: Provider[]; refreshToken: number; initialProviderId?: string; openRequest: (requestId: string) => void }) {
  const [events, setEvents] = useState<OperationalEvent[]>([])
  const [total, setTotal] = useState(0)
  const [type, setType] = useState('')
  const [level, setLevel] = useState('')
  const [providerId, setProviderId] = useState(initialProviderId || '')
  const [jobId, setJobId] = useState('')
  const [since, setSince] = useState('')
  const [until, setUntil] = useState('')
  const [limit, setLimit] = useState(100)
  const [offset, setOffset] = useState(0)
  const [loading, setLoading] = useState(true)
  const [clearing, setClearing] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [logKind, setLogKind] = useState<'events' | 'requests'>('events')
  const [eventView, setEventView] = useState<'list' | 'timeline'>(() => localStorage.getItem('ai-watch-event-view') === 'timeline' ? 'timeline' : 'list')
  const requestSequence = useRef(0)
  const clearButtonRef = useRef<HTMLButtonElement>(null)
  const confirmRef = useRef<HTMLElement>(null)

  useEffect(() => {
    if (!initialProviderId) return
    setProviderId(initialProviderId)
    setLogKind('requests')
    setOffset(0)
  }, [initialProviderId])

  const loadEvents = useCallback(async () => {
    const sequence = ++requestSequence.current
    setLoading(true)
    setError('')
    try {
      const result = await api.events({
        limit, offset, type: logKind === 'requests' ? 'request_end' : type || undefined, level: level || undefined,
        providerId: providerId || undefined, jobId: jobId.trim() || undefined,
        since: since ? new Date(since).toISOString() : undefined,
        until: until ? new Date(until).toISOString() : undefined,
      })
      if (sequence !== requestSequence.current) return
      if (result.total > 0 && offset >= result.total) {
        setOffset(Math.floor((result.total - 1) / limit) * limit)
        return
      }
      setEvents(result.events)
      setTotal(result.total)
    } catch (e) {
      if (sequence !== requestSequence.current) return
      setError(e instanceof Error ? e.message : '无法读取事件记录')
    } finally {
      if (sequence === requestSequence.current) setLoading(false)
    }
  }, [jobId, level, limit, logKind, offset, providerId, since, type, until])

  useEffect(() => { void loadEvents() }, [loadEvents, refreshToken])
  useEffect(() => {
    if (!confirmOpen) return
    const focusable = () => Array.from(confirmRef.current?.querySelectorAll<HTMLElement>('button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])') ?? [])
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { closeConfirm(); return }
      if (event.key !== 'Tab') return
      const items = focusable()
      if (!items.length) return
      const first = items[0]
      const last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [clearing, confirmOpen])

  const closeConfirm = (force = false) => {
    if (clearing && !force) return
    setConfirmOpen(false)
    window.requestAnimationFrame(() => clearButtonRef.current?.focus())
  }
  const clearEvents = async () => {
    setClearing(true)
    setError('')
    setMessage('')
    try {
      const deleted = await api.clearEvents()
      closeConfirm(true)
      setMessage(deleted > 0 ? `已清空 ${deleted} 条事件记录` : '事件记录已清空')
      await loadEvents()
    } catch (e) {
      setError(e instanceof Error ? e.message : '清空事件失败')
    } finally {
      setClearing(false)
    }
  }

  const providerNames = useMemo(() => new Map(providers.filter(provider => provider.id).map(provider => [provider.id, provider.name])), [providers])
  const requestRecords = useMemo(() => {
    return events.filter(event => event.type === 'request_end').map(event => ({
      id: String(event.data?.requestId || `${event.jobId || 'job'}-${event.id}`),
      end: event,
      data: event.data || {},
    }))
  }, [events])
  const lifecycleEvents = useMemo(() => events.filter(event => event.type !== 'request_start' && event.type !== 'request_end'), [events])
  const typeOptions = useMemo(() => Array.from(new Set([...Object.keys(eventTypeLabels), ...events.map(event => event.type)])).sort(), [events])
  const errorCount = events.filter(event => event.level === 'error' || event.level === 'fatal').length
  const newest = events[0]?.at
  const page = Math.floor(offset / limit) + 1
  const pageCount = Math.max(1, Math.ceil(total / limit))
  const rangeStart = total === 0 ? 0 : offset + 1
  const rangeEnd = Math.min(offset + events.length, total)
  const resetFilters = () => {
    setType(''); setLevel(''); setProviderId(''); setJobId(''); setSince(''); setUntil(''); setLimit(100); setOffset(0)
  }
  const switchEventView = (value: 'list' | 'timeline') => { setEventView(value); localStorage.setItem('ai-watch-event-view', value) }

  return <div className="page events-page">
    <section className="page-heading events-heading"><div><span className="eyebrow"><History/>运行审计</span><h1>结构化事件记录</h1><p>查看任务、供应商与运行时生命周期信号。事件受保留策略约束，不包含原始 CLI 输出、Prompt 或凭证。</p></div><button ref={clearButtonRef} className="danger-button events-clear" disabled={loading || clearing || total === 0} onClick={() => setConfirmOpen(true)}><Trash2/>清空事件</button></section>

    <nav className="event-kind-tabs" aria-label="日志类型"><button className={logKind === 'events' ? 'active' : ''} onClick={() => { setLogKind('events'); setOffset(0) }}><History/><span><strong>事件记录</strong><small>任务、调度与系统生命周期</small></span><em>{logKind === 'events' ? lifecycleEvents.length : '—'}</em></button><button className={logKind === 'requests' ? 'active' : ''} onClick={() => { setLogKind('requests'); setOffset(0) }}><Terminal/><span><strong>请求日志</strong><small>每条记录对应一次完整 CLI 请求</small></span><em>{logKind === 'requests' ? requestRecords.length : '—'}</em></button></nav>

    <section className="event-summary" aria-label="事件计数">
      <div><span>匹配事件</span><strong>{loading ? '—' : total}</strong><small>当前服务端筛选结果</small></div>
      <div><span>已加载</span><strong>{loading ? '—' : events.length}</strong><small>本页最多 {limit} 条</small></div>
      <div className={errorCount ? 'has-errors' : ''}><span>错误信号</span><strong>{loading ? '—' : errorCount}</strong><small>当前页 error / fatal</small></div>
      <div><span>最近写入</span><strong>{loading ? '—' : newest ? formatAgo(newest) : '暂无'}</strong><small>按时间倒序展示</small></div>
    </section>

    <section className="panel event-filter-panel" aria-label="事件过滤器">
      <div className="event-filter-title"><div><Filter/><span><strong>过滤事件</strong><small>筛选由服务端执行，时间按当前设备时区输入</small></span></div><button className="secondary" disabled={loading} onClick={resetFilters}><RotateCcw/>重置</button></div>
      <div className="event-filters">
        <label><span>事件类型</span><Select value={logKind === 'requests' ? 'request_end' : type} disabled={logKind === 'requests'} onChange={event => { setType(event.target.value); setOffset(0) }}><option value="">全部类型</option>{typeOptions.map(option => <option key={option} value={option}>{eventTypeLabel(option)}</option>)}</Select></label>
        <label><span>级别</span><Select value={level} onChange={event => { setLevel(event.target.value); setOffset(0) }}><option value="">全部级别</option><option value="info">信息</option><option value="success">成功</option><option value="warning">警告</option><option value="error">错误</option><option value="fatal">严重错误</option></Select></label>
        <label><span>供应商</span><Select value={providerId} onChange={event => { setProviderId(event.target.value); setOffset(0) }}><option value="">全部供应商</option>{providers.filter(provider => provider.id).map(provider => <option key={`${provider.cli}-${provider.id}`} value={provider.id}>{cliLabel(provider.cli)} · {provider.name}</option>)}</Select></label>
        <label><span>任务 ID</span><input value={jobId} onChange={event => { setJobId(event.target.value); setOffset(0) }} placeholder="完整任务 ID" spellCheck={false}/></label>
        <label><span>开始时间</span><input type="datetime-local" step="1" value={since} max={until || undefined} onChange={event => { setSince(event.target.value); setOffset(0) }}/></label>
        <label><span>结束时间</span><input type="datetime-local" step="1" value={until} min={since || undefined} onChange={event => { setUntil(event.target.value); setOffset(0) }}/></label>
        <label><span>每页数量</span><Select value={limit} onChange={event => { setLimit(Number(event.target.value)); setOffset(0) }}>{[50, 100, 200, 500].map(value => <option key={value} value={value}>{value} 条</option>)}</Select></label>
      </div>
    </section>

    {error && <div className="error-banner event-error" role="alert"><AlertCircle/><div><strong>事件操作未完成</strong><span>{error}</span></div><button onClick={() => void loadEvents()}>重新加载</button></div>}
    {message && <div className="event-message" role="status"><CheckCircle2/>{message}</div>}

    <section className="panel event-feed" aria-busy={loading}>
      <div className="panel-title"><div><h2>{logKind === 'requests' ? 'CLI 请求日志' : eventView === 'list' ? '事件详情列表' : '事件信号流'}</h2><p>{logKind === 'requests' ? '每行对应一次独立 CLI 调用' : '结构化字段经过脱敏后展示'}</p></div><div className="event-view-actions">{logKind === 'events' && <div className="event-view-switch" role="group" aria-label="事件展示方式"><button className={eventView === 'list' ? 'active' : ''} onClick={() => switchEventView('list')}><List/>列表</button><button className={eventView === 'timeline' ? 'active' : ''} onClick={() => switchEventView('timeline')}><GitBranch/>时间线</button></div>}<span className="event-retention"><ShieldCheck/>有界保留</span></div></div>
      {loading ? <div className="event-loading"><LoaderCircle className="spinning"/><span>正在读取事件记录</span></div> : logKind === 'requests' ? <RequestLogList records={requestRecords} providerNames={providerNames} openRequest={openRequest}/> : lifecycleEvents.length && eventView === 'list' ? <div className="event-detail-table"><div className="event-detail-head"><span>时间 / 级别</span><span>事件</span><span>任务 / Provider</span><span>摘要</span><span>详情</span></div>{lifecycleEvents.map(event => <details className={`event-detail-row level-${event.level || 'info'}`} key={event.id}><summary><span><time>{new Date(event.at).toLocaleString('zh-CN', { hour12: false })}</time><em className={`event-level level-${event.level || 'info'}`}>{eventLevelLabel(event.level)}</em></span><span><strong>{eventTypeLabel(event.type)}</strong></span><span><small>{event.jobId ? `任务 ${event.jobId}` : '无任务关联'}</small><small>{event.providerId ? providerNames.get(event.providerId) || event.providerId : '无 Provider'}</small></span><span>{event.message || '结构化运行事件'}</span><span><ChevronDown/></span></summary><EventRecordDetails event={event} providerName={event.providerId ? providerNames.get(event.providerId) : undefined}/></details>)}</div> : lifecycleEvents.length ? <ol className="event-list">{lifecycleEvents.map(event => {
        const level = event.level || 'info'
        return <li key={event.id} className={`event-item level-${level}`}><div className="event-rail"><i/></div><div className="event-content"><header><span className={`event-level level-${level}`}>{eventLevelLabel(level)}</span><strong>{eventTypeLabel(event.type)}</strong><time dateTime={event.at}>{new Date(event.at).toLocaleString('zh-CN', { hour12: false })}</time></header><p>{event.message || '记录了一次结构化运行事件。'}</p><footer>{event.providerId && <span><Database/>{providerNames.get(event.providerId) || event.providerId}</span>}{event.jobId && <span title={event.jobId}><Activity/>任务 {event.jobId}</span>}<code>#{event.id}</code></footer></div></li>
      })}</ol> : <EmptyState icon={<History/>} title="没有匹配的事件" detail="调整过滤条件，或等待新的任务与运行时事件写入。"/>}
      {!loading && total > 0 && <nav className="event-pagination" aria-label="事件分页"><span>第 {page} / {pageCount} 页 · 显示 {rangeStart}–{rangeEnd}，共 {total} 条</span><div><button className="secondary" disabled={offset === 0} onClick={() => setOffset(current => Math.max(0, current - limit))}><ChevronLeft/>上一页</button><button className="secondary" disabled={offset + limit >= total} onClick={() => setOffset(current => current + limit)}>下一页<ChevronRight/></button></div></nav>}
    </section>

    {confirmOpen && <div className="event-confirm-overlay"><button className="event-confirm-scrim" aria-label="取消清空事件" disabled={clearing} onClick={() => closeConfirm()}/><section ref={confirmRef} className="event-confirm" role="dialog" aria-modal="true" aria-labelledby="clear-events-title"><div className="event-confirm-icon"><Trash2/></div><h2 id="clear-events-title">清空全部事件记录？</h2><p>这会删除所有结构化运行事件，而不仅是当前筛选结果。任务摘要、设置和供应商配置不会被删除，此操作无法撤销。</p><div><button className="secondary" autoFocus disabled={clearing} onClick={() => closeConfirm()}>取消</button><button className="danger-button" disabled={clearing} onClick={() => void clearEvents()}>{clearing ? <LoaderCircle className="spinning"/> : <Trash2/>}{clearing ? '正在清空' : '确认清空'}</button></div></section></div>}
  </div>
}

function EventRecordDetails({ event, providerName }: { event: OperationalEvent; providerName?: string }) {
  const data = event.data || {}
  const entries = [
    ['请求 ID', data.requestId], ['任务 ID', event.jobId], ['Provider', providerName || event.providerId],
    ['触发来源', data.triggerSource], ['发起端 IP', data.clientIP], ['模式 / 阶段', [data.mode, data.phase].filter(Boolean).join(' / ')],
    ['CLI', data.cli], ['CLI 可执行文件', data.cliExecutable], ['CLI 版本', data.cliVersion], ['模型', data.model], ['配置来源', data.configSource], ['尝试序号', data.attempt],
    ['目标地址', data.target], ['目标主机', data.targetHost], ['目标端口', data.targetPort],
    ['DNS 预解析', Array.isArray(data.dnsIPs) ? data.dnsIPs.join(', ') : data.dnsIPs], ['DNS 错误', data.dnsError],
    ['代理模式', data.proxyMode], ['代理地址', data.proxyEndpoint], ['状态', data.status],
    ['开始时间', data.startedAt], ['结束时间', data.endedAt], ['耗时', data.durationMillis != null ? `${String(data.durationMillis)} ms` : undefined],
    ['退出码', data.exitCode], ['分类结果', data.classification], ['错误阶段', data.errorStage], ['错误类型', data.errorType], ['可重试', data.retryable === true ? '是' : data.retryable === false ? '否' : undefined], ['错误详情', data.error],
    ['请求体摘要', data.requestBody ? JSON.stringify(data.requestBody) : undefined], ['下一次执行', data.nextAttemptAt], ['返回信息', data.responseExcerpt],
  ].filter((entry): entry is [string, unknown] => entry[1] !== undefined && entry[1] !== null && entry[1] !== '')
  return <div className="event-record-details"><div className="event-record-grid">{entries.map(([label, value]) => <div key={label}><span>{label}</span><strong>{String(value)}</strong></div>)}</div>{Object.keys(data).length > 0 && <details className="event-raw-data"><summary>查看完整脱敏结构</summary><pre>{JSON.stringify(data, null, 2)}</pre></details>}</div>
}

function RequestLogList({ records, providerNames, openRequest }: {
  records: Array<{ id: string; start?: OperationalEvent; end?: OperationalEvent; data: Record<string, unknown> }>
  providerNames: Map<string, string>
  openRequest: (requestId: string) => void
}) {
  if (!records.length) return <EmptyState icon={<Terminal/>} title="没有匹配的请求日志" detail="启动一次测活或保活后，这里会按 requestId 聚合请求详情。"/>
  return <div className="request-log-list"><div className="request-log-head"><span>请求时间</span><span>CLI / Provider</span><span>来源 / IP</span><span>状态 / 耗时</span><span>返回摘要</span><span/></div>{records.map(record => {
    const event = record.end || record.start!
    const data = record.data
    const providerId = event.providerId || String(data.providerId || '')
    const status = String(data.status || (record.end ? 'completed' : 'running'))
    return <details className={`request-log-row status-${status}`} key={record.id}>
      <summary><span><time>{new Date(record.start?.at || record.end?.at || 0).toLocaleString('zh-CN', { hour12: false })}</time><code>{record.id}</code></span><span><strong>{String(data.cli || 'CLI 未知')}</strong><small>{providerNames.get(providerId) || providerId || String(data.provider || '当前配置')}</small></span><span><strong>{String(data.triggerSource || 'manual')}</strong><small>{String(data.clientIP || '不可观测')}</small></span><span><em>{status}</em><small>{data.durationMillis != null ? `${String(data.durationMillis)} ms` : '执行中'}</small></span><span>{String(data.responseExcerpt || data.error || data.classification || '等待返回信息')}</span><span><ChevronDown/></span></summary>
      <EventRecordDetails event={{ ...event, data, type: 'request', message: 'CLI 请求详情' }} providerName={providerNames.get(providerId)}/>
      <div className="request-log-open"><button className="secondary" onClick={() => openRequest(record.id)}><ExternalLink/>打开完整请求详情</button></div>
    </details>
  })}</div>
}

function Metric({ icon, label, value, detail, tone }: { icon: React.ReactNode; label: string; value: string; detail: string; tone: string }) {
  return <div className={`metric-card ${tone}`}><div className="metric-icon">{icon}</div><div><span>{label}</span><strong>{value}</strong><small>{detail}</small></div><div className="metric-glow"/></div>
}
function PanelTitle({ title, detail, action }: { title: string; detail: string; action?: React.ReactNode }) { return <div className="panel-title"><div><h2>{title}</h2><p>{detail}</p></div>{action}</div> }
function CliIcon({ cli }: { cli: Cli }) { return <span className={`cli-icon ${cli}`}>{cli === 'codex' ? <Command/> : <Bot/>}</span> }
function ProviderGroup({ cli, providers, probeProvider, showRequests }: { cli: Cli; providers: Provider[]; probeProvider: (provider: Provider) => void; showRequests: (provider: Provider) => void }) {
  return <section className={`provider-category ${cli}`}><header><div><CliIcon cli={cli}/><span><strong>{cli === 'codex' ? 'Codex Providers' : 'Claude Code Providers'}</strong><small>{cli === 'codex' ? 'OpenAI Codex CLI' : 'Anthropic Claude Code CLI'}</small></span></div><em>{providers.length}</em></header><div className="provider-mini-list">{providers.length ? providers.map(provider => <div key={`${provider.cli}-${provider.id}`} className={`provider-mini-item ${provider.enabled === false ? 'disabled' : ''}`}><span className="provider-mini-main"><strong>{provider.name}</strong><small>{provider.model || provider.baseUrl || '默认模型'}</small><span className="provider-mini-meta"><em className="provider-source-readonly">{provider.source === 'current' ? '当前配置 · 只读' : provider.source === 'cc-switch' ? 'CC Switch · Redis快照/启动同步，只读' : '手填配置'}</em>{provider.state?.scheduleEnabled && <em><CalendarClock/>{provider.state.scheduleName || '计划已启用'}</em>}{provider.state?.lastSuccessAt && <em title={new Date(provider.state.lastSuccessAt).toLocaleString('zh-CN')}><CheckCircle2/>成功 {formatAgo(provider.state.lastSuccessAt)}</em>}{provider.state?.lastFailureAt && <em title={new Date(provider.state.lastFailureAt).toLocaleString('zh-CN')}><AlertCircle/>失败 {formatAgo(provider.state.lastFailureAt)}</em>}</span></span><span className={`provider-runtime-state ${providerStateTone(provider.state?.status)}`}><i/>{provider.enabled === false ? '已停用' : providerStateLabel(provider.state?.status)}{provider.state?.consecutiveFailures ? ` · ${provider.state.consecutiveFailures} 次失败` : ''}</span>{provider.current && <em className="provider-current">当前</em>}<span className="provider-mini-actions"><button className="provider-requests" disabled={!provider.id} aria-label={provider.id ? `查看最近请求：${provider.name}` : `${provider.name} 暂不支持独立请求筛选`} title={provider.id ? '最近请求' : '当前配置没有稳定 Provider ID'} onClick={() => showRequests(provider)}><Eye/></button><button className="provider-probe" disabled={provider.enabled === false || provider.available === false} aria-label={`测活：${provider.name}`} onClick={() => probeProvider(provider)}><Activity/>测活</button></span></div>) : <p className="provider-category-empty">暂未发现此类配置</p>}</div></section>
}
function JobRow({ job, open }: { job: JobSummary; open: () => void }) {
  const [seconds, setSeconds] = useState(0)
  useEffect(() => { const update = () => setSeconds(job.nextAttemptAt ? Math.max(0, Math.ceil((new Date(job.nextAttemptAt).getTime() - Date.now()) / 1000)) : 0); update(); const t = setInterval(update, 1000); return () => clearInterval(t) }, [job.nextAttemptAt])
  return <button className="job-row" onClick={open}><CliIcon cli={job.cli}/><div className="job-main"><div><strong>{cliLabel(job.cli)} · {executionLabel(job.mode, job.runOnce)}</strong><StatusPill status={job.status}/></div><span>{job.providerName || job.providerId || '当前配置'}{job.model ? ` · ${job.model}` : ''}</span></div><div className="job-stat"><span>尝试次数</span><strong>{job.attemptCount}</strong></div><div className="job-stat"><span>{job.mode === 'keepalive' && !job.runOnce ? '下次执行' : '已运行'}</span><strong>{seconds ? `${seconds}s` : formatAgo(job.startedAt)}</strong></div><ChevronRight className="row-arrow"/></button>
}

export function NewJobDrawer({ providers, initialProvider, defaultOptions, close, onStarted }: { providers: Provider[]; initialProvider: Provider | null; defaultOptions: JobOptions; close: () => void; onStarted: (job: JobSummary, notifyOnComplete: boolean) => void }) {
  const [step, setStep] = useState(initialProvider ? 5 : 1)
  const [mode, setMode] = useState<JobMode>('probe')
  const [cli, setCli] = useState<Cli>(initialProvider?.cli ?? 'codex')
  const filtered = useMemo(() => providers.filter(p => p.cli === cli && p.enabled !== false && p.available !== false), [providers, cli])
  const [providerId, setProviderId] = useState(initialProvider?.id ?? '')
  const [options, setOptions] = useState<JobOptions>(() => ({ ...defaultOptions, timeoutSeconds: Math.max(45, defaultOptions.timeoutSeconds) }))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [scenarios, setScenarios] = useState<TestScenario[]>([])
  const drawerRef = useRef<HTMLElement>(null)
  useEffect(() => { void api.testScenarios().then(setScenarios).catch(() => setScenarios([])) }, [])
  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null
    const focusable = () => Array.from(drawerRef.current?.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])') ?? [])
    focusable()[0]?.focus()
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) close()
      if (event.key !== 'Tab') return
      const items = focusable(); if (!items.length) return
      const first = items[0]; const last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => { window.removeEventListener('keydown', onKeyDown); previousFocus?.focus() }
  }, [busy, close])
  useEffect(() => { setProviderId(current => filtered.some(p => p.id === current) ? current : (filtered.find(p => p.current)?.id || filtered[0]?.id || '')) }, [cli, filtered])
  const selected = filtered.find(p => p.id === providerId)
  const canNext = step !== 3 || Boolean(selected)
  const submit = async () => {
    setBusy(true); setError('')
    const body: StartJobRequest = { mode, cli, providerId, options }
    try { onStarted(await api.startJob(body), options.notifyOnComplete) } catch (e) { setError(e instanceof Error ? e.message : '任务启动失败') } finally { setBusy(false) }
  }
  return <div className="overlay"><div className="overlay-scrim" onClick={() => { if (!busy) close() }}/><aside ref={drawerRef} className="drawer" role="dialog" aria-modal="true" aria-labelledby="new-job-title">
    <div className="drawer-header"><div><span>新建任务</span><h2 id="new-job-title">{step === 1 ? '选择运行模式' : step === 2 ? '选择客户端' : step === 3 ? '选择配置源' : step === 4 ? '高级参数' : '确认并启动'}</h2></div><button className="icon-button" disabled={busy} onClick={close} aria-label="关闭新建任务"><X/></button></div>
    <div className="steps">{[1,2,3,4,5].map(n => <div key={n} className={`${n === step ? 'active' : ''} ${n < step ? 'done' : ''}`}><span>{n < step ? <Check/> : n}</span><i/></div>)}</div>
    <div className="drawer-body">
      {step === 1 && <div className="choice-grid"><Choice active={mode === 'probe' && options.runOnce} onClick={() => { setMode('probe'); setOptions(current => ({ ...current, runOnce: true })) }} icon={<Gauge/>} title="一次测活" tag="执行一次" detail="只调用一次所选 CLI，并立即返回本次探测结果。" footer="适合快速检查当前连通性"/><Choice active={mode === 'probe' && !options.runOnce} onClick={() => { setMode('probe'); setOptions(current => ({ ...current, runOnce: false })) }} icon={<RotateCcw/>} title="持续测活" tag="直至成功" detail="按重试间隔持续探测，成功或遇到不可恢复错误后结束。" footer="适合等待服务恢复可用"/><Choice active={mode === 'keepalive' && options.runOnce} onClick={() => { setMode('keepalive'); setOptions(current => ({ ...current, runOnce: true })) }} icon={<Activity/>} title="一次保活" tag="单轮观测" detail="按保活规则执行一轮检查，不进入后续周期。" footer="适合验证保活参数"/><Choice active={mode === 'keepalive' && !options.runOnce} onClick={() => { setMode('keepalive'); setOptions(current => ({ ...current, runOnce: false })) }} icon={<TimerReset/>} title="持续保活" tag="持续运行" detail="立即检查一次，之后按固定间隔持续观测，直到手动停止。" footer="适合长期观测服务稳定性"/></div>}
      {step === 2 && <div className="choice-grid"><Choice active={cli === 'codex'} onClick={() => setCli('codex')} icon={<Command/>} title="Codex CLI" tag="OpenAI" detail="使用只读沙箱与临时会话，检查 Codex 连接状态。" footer="支持当前配置与 Redis Provider 快照"/><Choice active={cli === 'claude'} onClick={() => setCli('claude')} icon={<Bot/>} title="Claude CLI" tag="Anthropic" detail="禁用工具与会话持久化，安全检查 Claude 连接状态。" footer="支持当前配置与 Redis Provider 快照"/></div>}
      {step === 3 && <div><div className="inline-note"><ShieldCheck/><span>CC Switch Provider 已在应用启动时同步到 Redis；启动任务不会访问 SQLite，也不会切换 CC Switch 当前 Provider。</span></div><div className="provider-grid">{filtered.length ? filtered.map(p => <ProviderCard key={p.id || `current-${p.cli}`} provider={p} selected={providerId === p.id} onClick={() => setProviderId(p.id)}/>) : <EmptyState icon={<Database/>} title="没有可用配置" detail={`未发现 ${cliLabel(cli)} 当前配置或 Redis Provider 快照。`}/>}</div></div>}
      {step === 4 && <><ScenarioPicker cli={cli} options={options} scenarios={scenarios} setOptions={setOptions}/><AdvancedFields mode={mode} cli={cli} options={options} setOptions={setOptions}/></>}
      {step === 5 && <div className="confirmation"><div className="confirm-hero"><div className="confirm-orbit"><CliIcon cli={cli}/><i/></div><span>即将启动</span><h3>{cliLabel(cli)} {executionLabel(mode, options.runOnce)}任务</h3><p>所有 CLI 输出只在运行时通过内存实时传递，任务结束后立即销毁。</p></div><div className="confirm-list"><Confirm label="运行模式" value={executionLabel(mode, options.runOnce)}/><Confirm label="客户端" value={cliLabel(cli)}/><Confirm label="配置源" value={selected?.name || providerId}/><Confirm label="模型" value={options.model || selected?.model || '跟随配置'}/><Confirm label="单次超时" value={`${options.timeoutSeconds} 秒`}/>{!options.runOnce && <Confirm label={mode === 'probe' ? '重试间隔' : '保活间隔'} value={`${mode === 'probe' ? options.retryIntervalSeconds : options.keepaliveIntervalSeconds} 秒`}/>} {mode === 'keepalive' && !options.runOnce && <Confirm label="失败转测活阈值" value={`${options.failureThreshold} 次`}/>}</div></div>}
      {error && <div className="form-error" role="alert"><AlertCircle/>{error}</div>}
    </div>
    <div className="drawer-footer"><button className="secondary" disabled={busy} onClick={() => step === 1 ? close() : setStep(step - 1)}>{step === 1 ? '取消' : <><ChevronLeft/>上一步</>}</button>{step < 5 ? <button className="primary" disabled={!canNext || busy} onClick={() => setStep(step + 1)}>继续<ChevronRight/></button> : <button className="primary launch" disabled={busy} onClick={() => void submit()}>{busy ? <LoaderCircle className="spinning"/> : <Play/>}{busy ? '正在启动' : '启动任务'}</button>}</div>
  </aside></div>
}

function Choice({ active, onClick, icon, title, tag, detail, footer }: { active: boolean; onClick: () => void; icon: React.ReactNode; title: string; tag: string; detail: string; footer: string }) { return <button className={`choice-card ${active ? 'selected' : ''}`} aria-pressed={active} onClick={onClick}><span className="choice-check">{active && <Check/>}</span><div className="choice-icon">{icon}</div><div className="choice-title"><h3>{title}</h3><em>{tag}</em></div><p>{detail}</p><small><CheckCircle2/>{footer}</small></button> }
function ProviderCard({ provider, selected, onClick }: { provider: Provider; selected: boolean; onClick: () => void }) { return <button className={`provider-card ${selected ? 'selected' : ''}`} aria-pressed={selected} onClick={onClick}><span className="radio-dot"><i/></span><div className="provider-top"><CliIcon cli={provider.cli}/><div><strong>{provider.name}</strong><span>{provider.source === 'current' ? '当前 CLI 配置 · 自动发现只读' : provider.source === 'cc-switch' ? 'CC Switch · Redis快照/启动同步，只读' : '手填 Provider'}</span></div>{provider.current && <em>当前</em>}</div><dl><div><dt>模型</dt><dd>{provider.model || '跟随配置'}</dd></div><div><dt>Base URL</dt><dd>{provider.baseUrl || '默认地址'}</dd></div><div><dt>API Key</dt><dd><KeyRound/>{provider.maskedApiKey || '环境变量'}</dd></div></dl></button> }
function Confirm({ label, value }: { label: string; value: string }) { return <div><span>{label}</span><strong>{value}</strong></div> }

function ScenarioPicker({ cli, options, scenarios, setOptions }: { cli: Cli; options: JobOptions; scenarios: TestScenario[]; setOptions: (value: JobOptions) => void }) {
  const available = scenarios.filter(item => item.enabled && (!item.cli || item.cli === cli))
  const selected = available.find(item => item.id === options.scenarioId)
  return <div className="form-sections scenario-picker"><FormSection title="测试场景" detail="使用可重复的合成测试，或继续填写一次性 Prompt"><label className="field"><span>场景</span><Select value={options.scenarioId || ''} onChange={event => setOptions({ ...options, scenarioId: event.target.value })}><option value="">自定义 Prompt</option>{available.map(item => <option key={item.id} value={item.id}>{item.name} · {item.assertionType}</option>)}</Select><small>场景 Prompt 会持久化；普通自定义 Prompt 不会入库。</small></label>{selected && <div className="scenario-picker-preview"><FlaskConical/><span><strong>{selected.name}</strong><small>{selected.description || selected.prompt}</small></span></div>}</FormSection></div>
}

function AdvancedFields({ mode, cli, options, setOptions }: { mode: JobMode; cli: Cli; options: JobOptions; setOptions: (v: JobOptions) => void }) {
  const patch = (key: keyof JobOptions, value: string | number | boolean) => setOptions({ ...options, [key]: value })
  return <div className="form-sections"><FormSection title="运行节奏" detail={options.runOnce ? '当前任务只执行一轮，不会进入重试或后续保活周期' : mode === 'keepalive' ? '控制调用节奏，以及连续失败后何时进入恢复测活' : '控制单次调用与下一次尝试的时间'}><div className="field-grid"><NumberField label="单次超时" value={options.timeoutSeconds} suffix="秒" min={5} onChange={v => patch('timeoutSeconds', v)}/>{!options.runOnce && (mode === 'probe' ? <NumberField label="重试间隔" value={options.retryIntervalSeconds} suffix="秒" min={1} onChange={v => patch('retryIntervalSeconds', v)}/> : <><NumberField label="保活间隔" value={options.keepaliveIntervalSeconds} suffix="秒" min={10} onChange={v => patch('keepaliveIntervalSeconds', v)}/><NumberField label="失败转测活阈值" value={options.failureThreshold} suffix="次" min={1} onChange={v => patch('failureThreshold', v)}/></>)}</div></FormSection><FormSection title="探测内容" detail="CLI 应当按照提示返回期望文本"><label className="field"><span>Prompt</span><textarea value={options.prompt} rows={3} onChange={e => patch('prompt', e.target.value)}/></label><label className="field"><span>期望文本</span><input value={options.expectedText} onChange={e => patch('expectedText', e.target.value)}/></label></FormSection>{cli === 'codex' ? <FormSection title="Codex 参数" detail="覆盖当前 Provider 的请求重试策略"><div className="field-grid"><NumberField label="请求重试" value={options.requestMaxRetries} min={0} onChange={v => patch('requestMaxRetries', v)}/><NumberField label="流式重试" value={options.streamMaxRetries} min={0} onChange={v => patch('streamMaxRetries', v)}/></div><label className="field"><span>模型（可选）</span><input placeholder="跟随 Provider 配置" value={options.model} onChange={e => patch('model', e.target.value)}/></label></FormSection> : <FormSection title="Claude 参数" detail="可选模型与会话显示名称"><div className="field-grid"><label className="field"><span>模型（可选）</span><input placeholder="跟随配置" value={options.model} onChange={e => patch('model', e.target.value)}/></label><label className="field"><span>Fallback 模型</span><input placeholder="可留空" value={options.fallbackModel} onChange={e => patch('fallbackModel', e.target.value)}/></label></div><label className="field"><span>会话名称</span><input value={options.sessionName} onChange={e => patch('sessionName', e.target.value)}/></label></FormSection>}<label className="toggle-row"><div><strong>任务结束通知</strong><span>允许浏览器在测活完成时发送系统通知</span></div><input type="checkbox" checked={options.notifyOnComplete} onChange={e => patch('notifyOnComplete', e.target.checked)}/><i/></label></div>
}
function FormSection({ title, detail, children }: { title: string; detail: string; children: React.ReactNode }) { return <section className="form-section"><div className="form-section-title"><h3>{title}</h3><p>{detail}</p></div>{children}</section> }
function NumberField({ label, value, suffix, min, max, step = 1, onChange }: { label: string; value: number; suffix?: string; min: number; max?: number; step?: number; onChange: (v: number) => void }) { return <label className="field"><span>{label}</span><div className="number-input"><input type="number" min={min} max={max} step={step} value={value} onChange={e => { const next = Number(e.target.value); if (Number.isFinite(next)) onChange(Math.min(max ?? Number.MAX_SAFE_INTEGER, Math.max(min, next))) }}/>{suffix && <em>{suffix}</em>}</div></label> }

export function JobDetail({ initial, notifyOnComplete, close, onChanged }: { initial: JobSummary; notifyOnComplete: boolean; close: () => void; onChanged: () => void }) {
  const [job, setJob] = useState(initial)
  const [events, setEvents] = useState<JobEvent[]>([])
  const [connected, setConnected] = useState(false)
  const [paused, setPaused] = useState(false)
  const [stopping, setStopping] = useState(false)
  const outputRef = useRef<HTMLDivElement>(null)
  const previousStatus = useRef(initial.status)
  const changeNotified = useRef(false)
  const running = job.status === 'running' || job.status === 'starting'
  const runningRef = useRef(running)
  runningRef.current = running
  const notifyChanged = useCallback(() => {
    if (changeNotified.current) return
    changeNotified.current = true
    onChanged()
  }, [onChanged])
  const closeDetail = useCallback(() => { notifyChanged(); close() }, [close, notifyChanged])
  useEffect(() => {
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); closeDetail() }
    }
    window.addEventListener('keydown', handleKey)
    return () => window.removeEventListener('keydown', handleKey)
  }, [closeDetail])
  useEffect(() => {
    const source = new EventSource(api.eventsUrl(job.id))
    source.onopen = () => setConnected(true)
    source.onerror = () => {
      setConnected(false)
      if (!runningRef.current) source.close()
    }
    const handleEvent = (e: MessageEvent) => {
      try {
        const event = normalizeEvent(JSON.parse(e.data))
        if (event.job) setJob(event.job)
        setEvents(prev => [...prev.slice(-4999), event])
        if (event.job && !['running','starting'].includes(event.job.status)) {
          source.close()
          setConnected(false)
        }
        if (event.type !== 'log') void api.getJob(job.id).then(setJob).catch(() => undefined)
      } catch { /* ignore malformed heartbeats */ }
    }
    const eventNames = ['output', 'error', 'cleanup', 'attempt_start', 'classification', 'job_state', 'countdown', 'phase', 'recovery', 'request_start', 'request_log', 'request_end']
    eventNames.forEach(name => source.addEventListener(name, handleEvent as EventListener))
    return () => { source.close(); setConnected(false) }
  }, [job.id])
  useEffect(() => { if (!paused) outputRef.current?.scrollTo({ top: outputRef.current.scrollHeight, behavior: 'smooth' }) }, [events, paused])
  useEffect(() => {
    const wasRunning = previousStatus.current === 'running' || previousStatus.current === 'starting'
    const isFinished = job.status !== 'running' && job.status !== 'starting'
    if (wasRunning && isFinished) notifyChanged()
    if (wasRunning && isFinished && notifyOnComplete && typeof Notification !== 'undefined' && Notification.permission === 'granted') {
      new Notification(`AI Watch · ${statusMeta[job.status].label}`, { body: `${cliLabel(job.cli)} ${modeLabel(job.mode)}任务：${job.providerName || job.providerId || '当前配置'}` })
    }
    previousStatus.current = job.status
  }, [job.status, job.cli, job.mode, job.providerId, job.providerName, notifyOnComplete, notifyChanged])
  const stop = async () => { setStopping(true); try { const next = await api.stopJob(job.id); setJob(next); notifyChanged() } finally { setStopping(false) } }
  const copy = () => void navigator.clipboard.writeText(events.map(e => `[${new Date(e.timestamp).toLocaleTimeString()}] ${e.message ?? e.type}`).join('\n'))
  return <div className="detail-overlay" role="dialog" aria-modal="true" aria-label="测活终端输出"><div className="detail-header"><button className="icon-button" onClick={closeDetail} aria-label="返回并关闭终端"><ChevronLeft/></button><CliIcon cli={job.cli}/><div><span>{cliLabel(job.cli)} · {executionLabel(job.mode, job.runOnce)}</span><h2>{job.providerName || job.providerId || '当前配置'}</h2></div><StatusPill status={job.status}/><div className="detail-actions">{running && <button className="danger-button" disabled={stopping} onClick={() => void stop()}>{stopping ? <LoaderCircle className="spinning"/> : <Square/>}停止任务</button>}<button className="icon-button terminal-close-button" onClick={closeDetail} aria-label="关闭测活终端"><X/></button></div></div><div className="detail-body"><section className="detail-stats"><div><span>任务 ID</span><strong className="mono">{job.id.slice(0, 12)}</strong></div><div><span>请求次数</span><strong>{events.filter(e => e.data?.requestId && e.type !== 'log').length}</strong></div><div><span>已运行</span><strong>{formatDuration(job.elapsedMs ?? Date.now() - new Date(job.startedAt).getTime())}</strong></div><div><span>模式 / 最近结果</span><strong>{executionLabel(job.mode, job.runOnce)} · {job.lastAttemptStatus || '等待中'}</strong></div></section><section className="terminal-card"><div className="terminal-bar"><div className="window-dots"><i/><i/><i/></div><button className={`stream-state terminal-replay-close ${connected ? 'online' : ''}`} onClick={closeDetail} aria-label="关闭终端并返回任务列表">{connected ? <Wifi/> : <WifiOff/>}{connected ? '实时连接' : running ? '正在重连' : '缓存回放'}</button><div className="terminal-actions"><button onClick={() => setPaused(!paused)}>{paused ? <Play/> : <Pause/>}{paused ? '继续滚动' : '暂停滚动'}</button><button onClick={copy} disabled={!events.length}><Copy/>复制</button></div></div><div className="terminal-output" ref={outputRef}>{events.length ? events.map((event, index) => <div className={`log-line ${event.level || ''}`} key={event.id || `${event.timestamp}-${index}`}><time>{new Date(event.timestamp).toLocaleTimeString('zh-CN', { hour12: false })}</time><span>{event.level === 'command' ? '$' : event.level === 'success' ? '✓' : event.level === 'error' ? '×' : '›'}</span><code>{terminalEventText(event)}</code></div>) : <div className="terminal-empty">{running ? <><LoaderCircle className="spinning"/><span>等待 CLI 输出…</span></> : <><Trash2/><span>{job.mode === 'probe' ? '测活日志不存在或已超过 24 小时。' : '保活任务不缓存完整运行日志。'}</span></>}</div>}</div><div className="terminal-foot"><ShieldCheck/><span>{job.mode === 'probe' ? '测活日志脱敏后在 Redis 中缓存 24 小时' : '保活输出仅保留在运行时内存中'}</span><em>{job.mode === 'probe' ? '最多 5000 条 / 约 2 MiB' : '任务结束后自动清空'}</em></div></section></div></div>
}

function terminalEventText(event: JobEvent) {
  if (event.rawType === 'request_start') {
    const cli = String(event.data?.cli || event.job?.cli || 'cli')
    const model = String(event.data?.model || event.job?.model || '默认模型')
    return `$ ${cli} --model ${model} [PROMPT REDACTED]\n请求目标 ${String(event.data?.target || '目标未知')} · proxy=${String(event.data?.proxyMode || '—')}`
  }
  if (event.rawType === 'request_end') {
    const summary = `${event.message || '请求结束'} · ${String(event.data?.status || '')} · ${String(event.data?.durationMillis || 0)}ms · exit=${String(event.data?.exitCode ?? '—')}`
    const response = String(event.data?.responseExcerpt || '').trim()
    return response ? `${summary}\n供应商返回：${response}` : summary
  }
  return event.message || event.type
}

export function SettingsView({ onThemeChanged }: { onThemeChanged: (theme: AppSettings['uiTheme']) => void }) {
  const [settings, setSettings] = useState<AppSettings | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')
  const [messageTone, setMessageTone] = useState<'success' | 'error'>('success')
  const [digestPreview, setDigestPreview] = useState('')
  const [digestBusy, setDigestBusy] = useState<'preview' | 'send' | null>(null)
  useEffect(() => { api.settings().then(setSettings).catch(e => setMessage(e instanceof Error ? e.message : '加载失败')).finally(() => setLoading(false)) }, [])
  useEffect(() => {
    if (!message || messageTone !== 'success') return
    const timer = window.setTimeout(() => setMessage(''), 3200)
    return () => window.clearTimeout(timer)
  }, [message, messageTone])
  const patch = <K extends keyof AppSettings>(key: K, value: AppSettings[K]) => settings && setSettings({ ...settings, [key]: value })
  const save = async () => { if (!settings) return; setSaving(true); setMessage(''); try { const saved = await api.saveSettings(settings); setSettings(saved); onThemeChanged(saved.uiTheme); setMessageTone('success'); setMessage('设置已保存') } catch (e) { setMessageTone('error'); setMessage(e instanceof Error ? e.message : '保存失败') } finally { setSaving(false) } }
  const previewDigest = async () => { setDigestBusy('preview'); try { const value = await api.reliabilityDigestPreview(); setDigestPreview(value.content) } catch (e) { setMessageTone('error'); setMessage(e instanceof Error ? e.message : '摘要预览失败') } finally { setDigestBusy(null) } }
  const sendDigest = async () => { setDigestBusy('send'); try { const value = await api.reliabilityDigestSend(); setDigestPreview(value.content); setMessageTone('success'); setMessage('可靠性摘要已发送') } catch (e) { setMessageTone('error'); setMessage(e instanceof Error ? e.message : '摘要发送失败') } finally { setDigestBusy(null) } }
  const browserPermission = typeof Notification === 'undefined' ? 'unsupported' : Notification.permission
  return <div className="page settings-page">
    <section className="page-heading"><div><span className="eyebrow"><Settings/>全局偏好</span><h1>设置与通知</h1><p>定义任务节奏与通知策略。供应商密钥和钉钉 Webhook 均以 AES-GCM 密文保存在 Redis。</p></div></section>
    {loading ? <div className="settings-loading"><LoaderCircle className="spinning"/>正在读取设置</div> : settings && <div className="settings-grid">
      <section className="panel settings-panel theme-settings-panel"><PanelTitle title="界面主题" detail="切换后立即预览，保存后由 Redis 持久化"/><div className="theme-choice-grid">{([
        ['deep-ocean', '深海终端', '高对比青蓝控制台'], ['graphite-signal', '石墨信号', '低饱和中性暗色'], ['arctic-daylight', '极昼控制台', '清晰明亮的浅色界面'],
      ] as const).map(([value, label, detail]) => <button key={value} className={`theme-choice ${settings.uiTheme === value ? 'active' : ''}`} onClick={() => { patch('uiTheme', value); onThemeChanged(value) }}><i className={`theme-swatch ${value}`}/><span><strong>{label}</strong><small>{detail}</small></span>{settings.uiTheme === value && <Check/>}</button>)}</div></section>
      <section className="panel settings-panel"><PanelTitle title="任务默认值" detail="新建任务时会自动带入，可在任务中临时调整"/><div className="setting-fields"><NumberField label="单次调用超时" value={settings.timeoutSeconds} suffix="秒" min={5} onChange={v => patch('timeoutSeconds', v)}/><NumberField label="测活重试间隔" value={settings.retryIntervalSeconds} suffix="秒" min={1} onChange={v => patch('retryIntervalSeconds', v)}/><NumberField label="保活执行间隔" value={settings.keepaliveIntervalSeconds} suffix="秒" min={10} onChange={v => patch('keepaliveIntervalSeconds', v)}/><NumberField label="摘要保留数量" value={settings.historyLimit} suffix="条" min={10} onChange={v => patch('historyLimit', v)}/><NumberField label="事件保留天数" value={settings.eventRetentionDays} suffix="天" min={1} onChange={v => patch('eventRetentionDays', v)}/><NumberField label="事件最大条数" value={settings.eventRetentionRows} suffix="条" min={100} onChange={v => patch('eventRetentionRows', v)}/><NumberField label="事件容量上限" value={Math.max(1, Math.round(settings.eventRetentionBytes / 1048576))} suffix="MiB" min={1} onChange={v => patch('eventRetentionBytes', v * 1048576)}/></div><div className="settings-callout"><Database/><div><strong>摘要持久化，测活明细短期缓存</strong><span>Prompt 和密钥不会入库；测活 CLI 输出脱敏后在 Redis 缓存 24 小时，保活原始输出仍只存在运行时内存。</span></div></div></section>
      <ProxySubscriptionCard/>
      <section className="panel settings-panel notification-panel"><PanelTitle title="通知渠道" detail="浏览器权限留在本机；钉钉凭证在服务端加密"/><div className="notification-list"><div className="notification-card"><div className="notification-icon browser"><Bell/></div><div><strong>浏览器通知</strong><span>{browserPermission === 'granted' ? '权限已允许' : browserPermission === 'denied' ? '权限已被浏览器阻止' : browserPermission === 'unsupported' ? '当前浏览器不支持系统通知' : '替代容器中不可用的 macOS 通知'}</span></div><button className={`switch ${settings.browserNotifications ? 'on' : ''}`} aria-label="切换浏览器通知" aria-pressed={settings.browserNotifications} disabled={browserPermission === 'unsupported' || browserPermission === 'denied'} onClick={async () => { if (!settings.browserNotifications && browserPermission === 'default') await Notification.requestPermission(); patch('browserNotifications', !settings.browserNotifications) }}><i/></button></div><DingTalkConfigCard onConfigured={configured => setSettings(current => current ? { ...current, dingTalkConfigured: configured } : current)}/></div><div className="secret-note"><KeyRound/><span>Webhook 明文不会返回浏览器；保存成功后输入框会立即清空。</span></div></section>
      <section className="panel settings-panel notification-policy-panel"><PanelTitle title="通知聚合策略" detail="降低保活噪声，同时保留长时间探测与恢复信号"/><div className="notification-policy-intro"><Sparkles/><span><strong>按窗口合并消息</strong><small>保活成功可按时间或次数汇总；多个 Provider 只有在合并窗口大于 0 时才会合并恢复通知。</small></span></div><div className="setting-fields notification-policy-fields"><NumberField label="保活按时间汇总" value={settings.keepaliveSummarySeconds} suffix="秒" min={0} max={604800} onChange={v => patch('keepaliveSummarySeconds', v)}/><NumberField label="保活按成功次数汇总" value={settings.keepaliveSummarySuccesses} suffix="次" min={0} max={1000000} onChange={v => patch('keepaliveSummarySuccesses', v)}/><NumberField label="测活进度通知间隔" value={settings.probeProgressSeconds} suffix="秒" min={1} max={604800} onChange={v => patch('probeProgressSeconds', v)}/><NumberField label="恢复通知合并窗口" value={settings.recoveryMergeSeconds} suffix="秒" min={0} max={86400} onChange={v => patch('recoveryMergeSeconds', v)}/></div><div className="notification-policy-foot"><CircleDot/><span><strong>恢复合并为 0 时保留单 Provider 模板</strong><small>保活时间或次数设为 0 会关闭对应汇总条件；两个条件同时启用时，任一条件先达到即发送。</small></span></div></section>
      <section className="panel settings-panel reliability-alert-settings"><PanelTitle title="Provider 可靠性告警" detail="每次请求结束后评估滚动 24 小时指标"/><label className="toggle-row reliability-alert-toggle"><div><strong>启用可靠性告警</strong><span>{settings.dingTalkConfigured ? '连续失败达到设定间隔的倍数，且成功率或 P95 异常时通过钉钉通知。' : '钉钉未配置；满足组合告警条件时只记录结构化告警事件。'}</span></div><input type="checkbox" checked={settings.reliabilityAlertEnabled} onChange={event => patch('reliabilityAlertEnabled', event.target.checked)}/><i/></label><div className={`setting-fields reliability-alert-fields ${settings.reliabilityAlertEnabled ? '' : 'disabled'}`}><NumberField label="连续失败告警间隔" value={settings.reliabilityAlertConsecutiveFailures} suffix="次" min={1} max={10000} onChange={v => patch('reliabilityAlertConsecutiveFailures', v)}/><NumberField label="最少完成样本" value={settings.reliabilityAlertMinSamples} suffix="次" min={1} max={10000} onChange={v => patch('reliabilityAlertMinSamples', v)}/><NumberField label="成功率下限" value={settings.reliabilityAlertSuccessRate} suffix="%" min={0.01} max={100} step={0.01} onChange={v => patch('reliabilityAlertSuccessRate', v)}/><NumberField label="P95 延迟上限" value={settings.reliabilityAlertP95Millis} suffix={settings.reliabilityAlertP95Millis === 0 ? '关闭' : 'ms'} min={0} max={86400000} onChange={v => patch('reliabilityAlertP95Millis', v)}/><NumberField label="连续成功恢复" value={settings.reliabilityAlertRecoverySuccesses} suffix="次" min={1} max={10000} onChange={v => patch('reliabilityAlertRecoverySuccesses', v)}/></div><label className="toggle-row reliability-recovery-toggle"><div><strong>发送恢复通知</strong><span>任一成功会重置连续失败计数；组合异常清除并达到连续成功次数后发送恢复通知。</span></div><input type="checkbox" checked={settings.reliabilityAlertRecoveryEnabled} onChange={event => patch('reliabilityAlertRecoveryEnabled', event.target.checked)}/><i/></label><div className="notification-policy-foot"><TrendingUp/><span><strong>连续失败间隔是必选门槛</strong><small>仅在 N、2N、3N 边界且成功率低于下限或 P95 超过上限时通知；成功率和 P95 不会独立触发。</small></span></div></section>
      <section className="panel settings-panel reliability-digest-settings"><PanelTitle title="定时可靠性摘要" detail="每天通过钉钉发送一次脱敏可靠性报告"/><label className="toggle-row reliability-digest-toggle"><div><strong>启用每日自动摘要</strong><span>{settings.dingTalkConfigured ? '到达设定时间后自动发送；同一自然日只发送一次。' : '请先配置钉钉机器人；当前仍可生成摘要预览。'}</span></div><input type="checkbox" checked={settings.reliabilityDigestEnabled} onChange={event => patch('reliabilityDigestEnabled', event.target.checked)}/><i/></label><div className={`reliability-digest-fields ${settings.reliabilityDigestEnabled ? '' : 'disabled'}`}><label className="field"><span>发送时间</span><input type="time" value={`${String(settings.reliabilityDigestHour).padStart(2, '0')}:${String(settings.reliabilityDigestMinute).padStart(2, '0')}`} onChange={event => { const [hour, minute] = event.target.value.split(':').map(Number); if (Number.isInteger(hour) && Number.isInteger(minute)) setSettings(current => current ? { ...current, reliabilityDigestHour: hour, reliabilityDigestMinute: minute } : current) }}/></label><label className="field"><span>时区</span><Select value={settings.reliabilityDigestTimezone} onChange={event => patch('reliabilityDigestTimezone', event.target.value)}><option value="Asia/Shanghai">Asia/Shanghai</option><option value="Asia/Tokyo">Asia/Tokyo</option><option value="Asia/Singapore">Asia/Singapore</option><option value="UTC">UTC</option><option value="America/Los_Angeles">America/Los_Angeles</option><option value="Europe/London">Europe/London</option></Select></label><label className="field"><span>统计范围</span><Select value={settings.reliabilityDigestRange} onChange={event => patch('reliabilityDigestRange', event.target.value as AppSettings['reliabilityDigestRange'])}><option value="24h">最近 24 小时</option><option value="7d">最近 7 天</option><option value="30d">最近 30 天</option></Select></label></div><div className="reliability-digest-actions"><button className="secondary" disabled={digestBusy !== null} onClick={() => void previewDigest()}>{digestBusy === 'preview' ? <LoaderCircle className="spinning"/> : <Eye/>}{digestBusy === 'preview' ? '生成中' : '立即预览'}</button><button className="primary" disabled={digestBusy !== null || !settings.dingTalkConfigured} onClick={() => void sendDigest()}>{digestBusy === 'send' ? <LoaderCircle className="spinning"/> : <Send/>}{digestBusy === 'send' ? '发送中' : '立即发送'}</button></div>{digestPreview && <div className="reliability-digest-preview"><div><strong>摘要预览</strong><span>内容仅包含脱敏聚合指标</span></div><pre>{digestPreview}</pre></div>}<div className="notification-policy-foot"><ShieldCheck/><span><strong>手动发送不占用今日自动摘要</strong><small>自动发送失败会记录结构化事件并在下一轮检查时重试。</small></span></div></section>
    </div>}
    {message && <div className={`toast-inline ${messageTone === 'success' ? 'success' : ''}`} role="status" aria-live="polite">{messageTone === 'success' ? <CheckCircle2/> : <AlertCircle/>}{message}</div>}
    <div className="sticky-save"><div><strong>修改全局默认值</strong><span>不会影响已经运行的任务</span></div><button className="primary" disabled={!settings || saving} onClick={() => void save()}>{saving ? <LoaderCircle className="spinning"/> : <Save/>}{saving ? '保存中' : '保存设置'}</button></div>
  </div>
}
