import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity, AlertCircle, Bell, BookOpen, Bot, Boxes, CalendarClock, Check, CheckCircle2, ChevronDown, ChevronLeft,
  ChevronRight, CircleDot, Clock3, Command, Copy, Database, ExternalLink, Eye,
  EyeOff, Filter, Gauge, History, KeyRound, LoaderCircle, Menu, Pause, Play, Plus, RefreshCw,
  Pencil, RotateCcw, Save, Send, Server, Settings, ShieldCheck, Sparkles, Square, Terminal,
  TimerReset, Trash2, Wifi, WifiOff, X, Zap,
} from 'lucide-react'
import { api, normalizeEvent } from './api'
import { DiagnosticsView } from './DiagnosticsView'
import { SchedulesView } from './SchedulesView'
import type {
  AppSettings, Cli, DashboardData, JobEvent, JobMode, JobOptions, JobStatus,
  JobSummary, OperationalEvent, Provider, ProviderExample, ProviderExampleWriteRequest, StartJobRequest,
} from './types'

type View = 'dashboard' | 'schedules' | 'events' | 'diagnostics' | 'settings'

const DEFAULT_OPTIONS: JobOptions = {
  runOnce: true,
  timeoutSeconds: 15,
  retryIntervalSeconds: 3,
  keepaliveIntervalSeconds: 120,
  failureThreshold: 3,
  prompt: 'hi，只回复 READY',
  expectedText: 'READY',
  requestMaxRetries: 2,
  streamMaxRetries: 2,
  model: '',
  fallbackModel: '',
  sessionName: 'claude-watch',
  notifyOnComplete: true,
}

const statusMeta: Record<JobStatus, { label: string; tone: string }> = {
  starting: { label: '准备中', tone: 'info' }, running: { label: '运行中', tone: 'running' },
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
  phase: '阶段切换', recovery: '恢复可用', countdown: '等待重试', cleanup: '运行时清理',
}
const eventTypeLabel = (type: string) => eventTypeLabels[type] || type.replaceAll('_', ' ')
const eventLevelLabel = (level?: string) => {
  if (level === 'error' || level === 'fatal') return '错误'
  if (level === 'warning' || level === 'warn') return '警告'
  if (level === 'success') return '成功'
  return '信息'
}

function Logo() {
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

export function App() {
  const [view, setView] = useState<View>('dashboard')
  const [data, setData] = useState<DashboardData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [presetProvider, setPresetProvider] = useState<Provider | null>(null)
  const [presetExample, setPresetExample] = useState<ProviderExample | null>(null)
  const [jobDefaults, setJobDefaults] = useState<JobOptions>(DEFAULT_OPTIONS)
  const [notificationJobs, setNotificationJobs] = useState<Set<string>>(() => new Set())
  const [detailJob, setDetailJob] = useState<JobSummary | null>(null)
  const [mobileNav, setMobileNav] = useState(false)
  const [eventsRefreshToken, setEventsRefreshToken] = useState(0)

  const load = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true)
    try { setData(await api.dashboard()); setError('') }
    catch (e) { setError(e instanceof Error ? e.message : '无法连接 AI Watch 服务') }
    finally { setLoading(false) }
  }, [])

  useEffect(() => { void load(); const t = window.setInterval(() => void load(true), 10_000); return () => clearInterval(t) }, [load])
  useEffect(() => { void api.settings().then(settings => setJobDefaults(current => ({ ...current, timeoutSeconds: settings.timeoutSeconds, retryIntervalSeconds: settings.retryIntervalSeconds, keepaliveIntervalSeconds: settings.keepaliveIntervalSeconds }))).catch(() => undefined) }, [])

  const openJob = (job: JobSummary) => { setDetailJob(job); setMobileNav(false) }
  const onStarted = (job: JobSummary, notifyOnComplete: boolean) => { setDrawerOpen(false); setDetailJob(job); if (notifyOnComplete) setNotificationJobs(current => new Set(current).add(job.id)); void load(true) }
  const viewLabel = view === 'dashboard' ? '总览' : view === 'schedules' ? '计划任务' : view === 'events' ? '事件' : view === 'diagnostics' ? '系统诊断' : '设置与通知'

  return <div className="app-shell">
    <div className="ambient ambient-a"/><div className="ambient ambient-b"/>
    <aside className={`sidebar ${mobileNav ? 'mobile-open' : ''}`}>
      <div className="sidebar-top"><Logo/><button className="icon-button mobile-close" onClick={() => setMobileNav(false)} aria-label="关闭菜单"><X/></button></div>
      <nav>
        <button className={view === 'dashboard' ? 'active' : ''} aria-current={view === 'dashboard' ? 'page' : undefined} onClick={() => { setView('dashboard'); setMobileNav(false) }}><Gauge/><span>总览</span></button>
        <button className={view === 'schedules' ? 'active' : ''} aria-current={view === 'schedules' ? 'page' : undefined} onClick={() => { setView('schedules'); setMobileNav(false) }}><CalendarClock/><span>计划任务</span></button>
        <button className={view === 'events' ? 'active' : ''} aria-current={view === 'events' ? 'page' : undefined} onClick={() => { setView('events'); setMobileNav(false) }}><History/><span>事件记录</span></button>
        <button className={view === 'diagnostics' ? 'active' : ''} aria-current={view === 'diagnostics' ? 'page' : undefined} onClick={() => { setView('diagnostics'); setMobileNav(false) }}><Server/><span>系统诊断</span></button>
        <button className={view === 'settings' ? 'active' : ''} aria-current={view === 'settings' ? 'page' : undefined} onClick={() => { setView('settings'); setMobileNav(false) }}><Settings/><span>设置与通知</span></button>
      </nav>
      <div className="sidebar-spacer"/>
      <div className={`connection-card ${error ? 'offline' : ''}`}>
        {error ? <WifiOff/> : <Wifi/>}<div><strong>{error ? '服务未连接' : '本地连接安全'}</strong><span>{error ? '等待后端响应' : '127.0.0.1 · 私有访问'}</span></div>
      </div>
      <div className="privacy-note"><ShieldCheck/><span>任务日志仅保留在内存，结束后即时销毁</span></div>
    </aside>

    <main className="main-area">
      <header className="topbar">
        <button className="icon-button menu-button" onClick={() => setMobileNav(true)} aria-label="打开菜单"><Menu/></button>
        <div className="crumb"><span>AI Watch</span><ChevronRight/><strong>{viewLabel}</strong></div>
        <div className="top-actions">{view !== 'schedules' && view !== 'diagnostics' && <button className="icon-button" onClick={() => view === 'events' ? setEventsRefreshToken(current => current + 1) : void load()} aria-label={view === 'events' ? '刷新事件' : '刷新'}><RefreshCw className={view !== 'events' && loading ? 'spinning' : ''}/></button>}{view !== 'diagnostics' && <button className="primary compact" onClick={() => { setPresetProvider(null); setPresetExample(null); setDrawerOpen(true) }}><Plus/>新建任务</button>}</div>
      </header>

      {view === 'dashboard' ? <Dashboard data={data} loading={loading} error={error} retry={() => void load()} openNew={() => { setPresetProvider(null); setPresetExample(null); setDrawerOpen(true) }} probeProvider={(provider) => { setPresetExample(null); setPresetProvider(provider); setDrawerOpen(true) }} referenceExample={(example) => { setPresetProvider(null); setPresetExample(example); setDrawerOpen(true) }} openJob={openJob}/> : view === 'schedules' ? <SchedulesView providers={data?.providers ?? []} defaultOptions={jobDefaults}/> : view === 'events' ? <EventsView providers={data?.providers ?? []} refreshToken={eventsRefreshToken}/> : view === 'diagnostics' ? <DiagnosticsView/> : <SettingsView/>}
    </main>
    {mobileNav && <div className="nav-scrim" onClick={() => setMobileNav(false)}/>} 
    {drawerOpen && <NewJobDrawer providers={data?.providers ?? []} initialProvider={presetProvider} initialExample={presetExample} defaultOptions={jobDefaults} close={() => { setDrawerOpen(false); setPresetProvider(null); setPresetExample(null) }} onStarted={onStarted}/>} 
    {detailJob && <JobDetail initial={detailJob} notifyOnComplete={notificationJobs.has(detailJob.id)} close={() => { setDetailJob(null); void load(true) }} onChanged={() => void load(true)}/>} 
  </div>
}

function Dashboard({ data, loading, error, retry, openNew, probeProvider, referenceExample, openJob }: {
  data: DashboardData | null; loading: boolean; error: string; retry: () => void; openNew: () => void; probeProvider: (provider: Provider) => void; referenceExample: (example: ProviderExample) => void; openJob: (job: JobSummary) => void
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
        <div className="panel"><PanelTitle title="本地供应商" detail="按客户端分类，密钥仅展示脱敏信息"/><div className="provider-categories">{data?.providers.length ? <><ProviderGroup cli="codex" providers={data.providers.filter(provider => provider.cli === 'codex')} probeProvider={probeProvider}/><ProviderGroup cli="claude" providers={data.providers.filter(provider => provider.cli === 'claude')} probeProvider={probeProvider}/></> : <EmptyState icon={<Database/>} title="暂无本地供应商" detail="挂载 Codex、Claude 或 CC Switch 配置后会显示在这里。"/>}</div><ProviderExamples referenceExample={referenceExample}/></div>
      </section>
    </>}
  </div>
}

function EventsView({ providers, refreshToken }: { providers: Provider[]; refreshToken: number }) {
  const [events, setEvents] = useState<OperationalEvent[]>([])
  const [total, setTotal] = useState(0)
  const [type, setType] = useState('')
  const [level, setLevel] = useState('')
  const [providerId, setProviderId] = useState('')
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
  const requestSequence = useRef(0)
  const clearButtonRef = useRef<HTMLButtonElement>(null)
  const confirmRef = useRef<HTMLElement>(null)

  const loadEvents = useCallback(async () => {
    const sequence = ++requestSequence.current
    setLoading(true)
    setError('')
    try {
      const result = await api.events({
        limit, offset, type: type || undefined, level: level || undefined,
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
  }, [jobId, level, limit, offset, providerId, since, type, until])

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
  }, [confirmOpen])

  const closeConfirm = () => {
    setConfirmOpen(false)
    window.requestAnimationFrame(() => clearButtonRef.current?.focus())
  }
  const clearEvents = async () => {
    setClearing(true)
    setError('')
    setMessage('')
    try {
      const deleted = await api.clearEvents()
      closeConfirm()
      setMessage(deleted > 0 ? `已清空 ${deleted} 条事件记录` : '事件记录已清空')
      await loadEvents()
    } catch (e) {
      setError(e instanceof Error ? e.message : '清空事件失败')
    } finally {
      setClearing(false)
    }
  }

  const providerNames = useMemo(() => new Map(providers.filter(provider => provider.id).map(provider => [provider.id, provider.name])), [providers])
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

  return <div className="page events-page">
    <section className="page-heading events-heading"><div><span className="eyebrow"><History/>运行审计</span><h1>结构化事件记录</h1><p>查看任务、供应商与运行时生命周期信号。事件受保留策略约束，不包含原始 CLI 输出、Prompt 或凭证。</p></div><button ref={clearButtonRef} className="danger-button events-clear" disabled={loading || clearing || total === 0} onClick={() => setConfirmOpen(true)}><Trash2/>清空事件</button></section>

    <section className="event-summary" aria-label="事件计数">
      <div><span>匹配事件</span><strong>{loading ? '—' : total}</strong><small>当前服务端筛选结果</small></div>
      <div><span>已加载</span><strong>{loading ? '—' : events.length}</strong><small>本页最多 {limit} 条</small></div>
      <div className={errorCount ? 'has-errors' : ''}><span>错误信号</span><strong>{loading ? '—' : errorCount}</strong><small>当前页 error / fatal</small></div>
      <div><span>最近写入</span><strong>{loading ? '—' : newest ? formatAgo(newest) : '暂无'}</strong><small>按时间倒序展示</small></div>
    </section>

    <section className="panel event-filter-panel" aria-label="事件过滤器">
      <div className="event-filter-title"><div><Filter/><span><strong>过滤事件</strong><small>筛选由服务端执行，时间按当前设备时区输入</small></span></div><button className="secondary" disabled={loading} onClick={resetFilters}><RotateCcw/>重置</button></div>
      <div className="event-filters">
        <label><span>事件类型</span><select value={type} onChange={event => { setType(event.target.value); setOffset(0) }}><option value="">全部类型</option>{typeOptions.map(option => <option key={option} value={option}>{eventTypeLabel(option)}</option>)}</select></label>
        <label><span>级别</span><select value={level} onChange={event => { setLevel(event.target.value); setOffset(0) }}><option value="">全部级别</option><option value="info">信息</option><option value="success">成功</option><option value="warning">警告</option><option value="error">错误</option><option value="fatal">严重错误</option></select></label>
        <label><span>供应商</span><select value={providerId} onChange={event => { setProviderId(event.target.value); setOffset(0) }}><option value="">全部供应商</option>{providers.filter(provider => provider.id).map(provider => <option key={`${provider.cli}-${provider.id}`} value={provider.id}>{cliLabel(provider.cli)} · {provider.name}</option>)}</select></label>
        <label><span>任务 ID</span><input value={jobId} onChange={event => { setJobId(event.target.value); setOffset(0) }} placeholder="完整任务 ID" spellCheck={false}/></label>
        <label><span>开始时间</span><input type="datetime-local" step="1" value={since} max={until || undefined} onChange={event => { setSince(event.target.value); setOffset(0) }}/></label>
        <label><span>结束时间</span><input type="datetime-local" step="1" value={until} min={since || undefined} onChange={event => { setUntil(event.target.value); setOffset(0) }}/></label>
        <label><span>每页数量</span><select value={limit} onChange={event => { setLimit(Number(event.target.value)); setOffset(0) }}>{[50, 100, 200, 500].map(value => <option key={value} value={value}>{value} 条</option>)}</select></label>
      </div>
    </section>

    {error && <div className="error-banner event-error" role="alert"><AlertCircle/><div><strong>事件操作未完成</strong><span>{error}</span></div><button onClick={() => void loadEvents()}>重新加载</button></div>}
    {message && <div className="event-message" role="status"><CheckCircle2/>{message}</div>}

    <section className="panel event-feed" aria-busy={loading}>
      <div className="panel-title"><div><h2>事件信号流</h2><p>只展示可持久化的结构化摘要</p></div><span className="event-retention"><ShieldCheck/>有界保留</span></div>
      {loading ? <div className="event-loading"><LoaderCircle className="spinning"/><span>正在读取事件记录</span></div> : events.length ? <ol className="event-list">{events.map(event => {
        const level = event.level || 'info'
        return <li key={event.id} className={`event-item level-${level}`}><div className="event-rail"><i/></div><div className="event-content"><header><span className={`event-level level-${level}`}>{eventLevelLabel(level)}</span><strong>{eventTypeLabel(event.type)}</strong><time dateTime={event.at}>{new Date(event.at).toLocaleString('zh-CN', { hour12: false })}</time></header><p>{event.message || '记录了一次结构化运行事件。'}</p><footer>{event.providerId && <span><Database/>{providerNames.get(event.providerId) || event.providerId}</span>}{event.jobId && <span title={event.jobId}><Activity/>任务 {event.jobId}</span>}<code>#{event.id}</code></footer></div></li>
      })}</ol> : <EmptyState icon={<History/>} title="没有匹配的事件" detail="调整过滤条件，或等待新的任务与运行时事件写入。"/>}
      {!loading && total > 0 && <nav className="event-pagination" aria-label="事件分页"><span>第 {page} / {pageCount} 页 · 显示 {rangeStart}–{rangeEnd}，共 {total} 条</span><div><button className="secondary" disabled={offset === 0} onClick={() => setOffset(current => Math.max(0, current - limit))}><ChevronLeft/>上一页</button><button className="secondary" disabled={offset + limit >= total} onClick={() => setOffset(current => current + limit)}>下一页<ChevronRight/></button></div></nav>}
    </section>

    {confirmOpen && <div className="event-confirm-overlay"><button className="event-confirm-scrim" aria-label="取消清空事件" onClick={closeConfirm}/><section ref={confirmRef} className="event-confirm" role="dialog" aria-modal="true" aria-labelledby="clear-events-title"><div className="event-confirm-icon"><Trash2/></div><h2 id="clear-events-title">清空全部事件记录？</h2><p>这会删除所有结构化运行事件，而不仅是当前筛选结果。任务摘要、设置和供应商配置不会被删除，此操作无法撤销。</p><div><button className="secondary" autoFocus disabled={clearing} onClick={closeConfirm}>取消</button><button className="danger-button" disabled={clearing} onClick={() => void clearEvents()}>{clearing ? <LoaderCircle className="spinning"/> : <Trash2/>}{clearing ? '正在清空' : '确认清空'}</button></div></section></div>}
  </div>
}

function Metric({ icon, label, value, detail, tone }: { icon: React.ReactNode; label: string; value: string; detail: string; tone: string }) {
  return <div className={`metric-card ${tone}`}><div className="metric-icon">{icon}</div><div><span>{label}</span><strong>{value}</strong><small>{detail}</small></div><div className="metric-glow"/></div>
}
function PanelTitle({ title, detail, action }: { title: string; detail: string; action?: React.ReactNode }) { return <div className="panel-title"><div><h2>{title}</h2><p>{detail}</p></div>{action}</div> }
function CliIcon({ cli }: { cli: Cli }) { return <span className={`cli-icon ${cli}`}>{cli === 'codex' ? <Command/> : <Bot/>}</span> }
function ProviderGroup({ cli, providers, probeProvider }: { cli: Cli; providers: Provider[]; probeProvider: (provider: Provider) => void }) {
  return <section className={`provider-category ${cli}`}><header><div><CliIcon cli={cli}/><span><strong>{cli === 'codex' ? 'Codex Providers' : 'Claude Code Providers'}</strong><small>{cli === 'codex' ? 'OpenAI Codex CLI' : 'Anthropic Claude Code CLI'}</small></span></div><em>{providers.length}</em></header><div className="provider-mini-list">{providers.length ? providers.map(provider => <div key={`${provider.cli}-${provider.id}`}><span><strong>{provider.name}</strong><small>{provider.model || provider.baseUrl || '默认模型'}</small></span>{provider.current && <em>当前</em>}<button className="provider-probe" aria-label={`测活：${provider.name}`} onClick={() => probeProvider(provider)}><Activity/>测活</button></div>) : <p className="provider-category-empty">暂未发现此类配置</p>}</div></section>
}
function ProviderExamples({ referenceExample }: { referenceExample: (example: ProviderExample) => void }) {
  const [examples, setExamples] = useState<ProviderExample[]>([])
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [editing, setEditing] = useState<ProviderExample | null | 'new'>(null)
  const [deleting, setDeleting] = useState<ProviderExample | null>(null)
  const requestInFlight = useRef(false)
  const loadExamples = async () => {
    if (requestInFlight.current) return
    requestInFlight.current = true
    setLoading(true)
    setError('')
    try {
      setExamples(await api.providerExamples())
      setLoaded(true)
    } catch (e) {
      setError(e instanceof Error ? e.message : '无法读取供应商示例')
    } finally {
      requestInFlight.current = false
      setLoading(false)
    }
  }
  const saved = async (value: ProviderExampleWriteRequest) => {
    const example = await api.saveProviderExample(value)
    setExamples(current => [...current.filter(item => item.id !== example.id), example].sort((a, b) => a.cli.localeCompare(b.cli) || a.name.localeCompare(b.name, 'zh-CN')))
    setLoaded(true)
    setEditing(null)
    setMessage(editing === 'new' ? '供应商示例已新增' : '供应商示例已更新')
  }
  const remove = async () => {
    if (!deleting) return
    await api.deleteProviderExample(deleting.id)
    setExamples(current => current.filter(item => item.id !== deleting.id))
    setDeleting(null)
    setMessage('供应商示例已删除')
  }
  return <details className="provider-examples" onToggle={event => { if (event.currentTarget.open && !loaded && !loading) void loadExamples() }}>
    <summary><span className="provider-example-summary-icon"><BookOpen/></span><span><strong>供应商示例</strong><small>参考常见兼容服务配置，不会读取或保存密钥</small></span><em>{loaded ? `${examples.length} 个模板` : '按需加载'}</em><ChevronDown className="provider-example-chevron"/></summary>
    <div className="provider-example-body">
      <div className="provider-example-toolbar"><div className="provider-example-safety"><ShieldCheck/><span><strong>模板不含密钥</strong><small>只保存地址、模型和 Provider 写法；认证信息始终由本地配置或环境变量提供。</small></span></div><button className="secondary provider-example-create" onClick={() => setEditing('new')}><Plus/>新增示例</button></div>
      {message && <div className="provider-example-message" aria-live="polite"><CheckCircle2/>{message}<button aria-label="关闭提示" onClick={() => setMessage('')}><X/></button></div>}
      {loading ? <div className="provider-example-loading"><LoaderCircle className="spinning"/>正在读取示例</div> : error ? <div className="provider-example-error" role="alert"><AlertCircle/><span><strong>示例加载失败</strong><small>{error}</small></span><button className="secondary" onClick={() => void loadExamples()}>重试</button></div> : examples.length ? <div className="provider-example-groups"><ProviderExampleGroup cli="codex" examples={examples.filter(example => example.cli === 'codex')} referenceExample={referenceExample} editExample={setEditing} deleteExample={setDeleting}/><ProviderExampleGroup cli="claude" examples={examples.filter(example => example.cli === 'claude')} referenceExample={referenceExample} editExample={setEditing} deleteExample={setDeleting}/></div> : loaded ? <div className="provider-example-empty"><EmptyState icon={<BookOpen/>} title="暂无供应商示例" detail="创建一个不含凭证的模板，统一团队使用的地址与模型写法。"/><button className="primary" onClick={() => setEditing('new')}><Plus/>新增第一个示例</button></div> : null}
    </div>
    {editing && <ProviderExampleDialog example={editing === 'new' ? null : editing} close={() => setEditing(null)} save={saved}/>}
    {deleting && <ProviderExampleDeleteDialog example={deleting} close={() => setDeleting(null)} remove={remove}/>}
  </details>
}
function ProviderExampleGroup({ cli, examples, referenceExample, editExample, deleteExample }: { cli: Cli; examples: ProviderExample[]; referenceExample: (example: ProviderExample) => void; editExample: (example: ProviderExample) => void; deleteExample: (example: ProviderExample) => void }) {
  return <section className={`provider-example-group ${cli}`}><header><div><CliIcon cli={cli}/><span><strong>{cliLabel(cli)} 示例</strong><small>{examples.length} 个参考模板</small></span></div></header>{examples.length ? <div className="provider-example-list">{examples.map(example => <article className="provider-example-card" key={`${example.cli}-${example.id}`}><div className="provider-example-card-title"><div><strong>{example.name}</strong><span>模板 · 不含密钥</span></div>{example.provider && <code>{example.provider}</code>}</div>{example.description && <p>{example.description}</p>}<dl><div><dt>Base URL</dt><dd title={example.baseUrl || undefined}>{example.baseUrl || '未指定'}</dd></div><div><dt>模型</dt><dd>{example.model || '跟随服务配置'}</dd></div></dl><div className="provider-example-actions"><button className="provider-example-action" onClick={() => referenceExample(example)}><BookOpen/>作为新任务参考<ChevronRight/></button><button className="provider-example-icon-action" aria-label={`编辑示例：${example.name}`} title="编辑示例" onClick={() => editExample(example)}><Pencil/></button><button className="provider-example-icon-action danger" aria-label={`删除示例：${example.name}`} title="删除示例" onClick={() => deleteExample(example)}><Trash2/></button></div></article>)}</div> : <p className="provider-category-empty">暂无 {cliLabel(cli)} 示例</p>}</section>
}

const EMPTY_PROVIDER_EXAMPLE: ProviderExampleWriteRequest = { id: '', name: '', cli: 'codex', baseUrl: '', model: '', provider: '', description: '' }
function ProviderExampleDialog({ example, close, save }: { example: ProviderExample | null; close: () => void; save: (value: ProviderExampleWriteRequest) => Promise<void> }) {
  const [value, setValue] = useState<ProviderExampleWriteRequest>(example ? { id: example.id, name: example.name, cli: example.cli, baseUrl: example.baseUrl || '', model: example.model || '', provider: example.provider || '', description: example.description || '' } : EMPTY_PROVIDER_EXAMPLE)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const dialogRef = useRef<HTMLElement>(null)
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    const focusable = () => Array.from(dialogRef.current?.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled])') || [])
    dialogRef.current?.querySelector<HTMLElement>('[data-initial-focus]')?.focus()
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') close()
      if (event.key !== 'Tab') return
      const items = focusable(); if (!items.length) return
      const first = items[0]; const last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', keydown)
    return () => { window.removeEventListener('keydown', keydown); previous?.focus() }
  }, [])
  const patch = (key: keyof ProviderExampleWriteRequest, next: string) => setValue(current => ({ ...current, [key]: next }))
  const submit = async () => {
    setError('')
    const normalized = { ...value, id: value.id.trim().toLowerCase(), name: value.name.trim(), baseUrl: value.baseUrl?.trim(), model: value.model?.trim(), provider: value.provider?.trim(), description: value.description?.trim() }
    if (!/^[a-z0-9][a-z0-9._-]{0,127}$/.test(normalized.id)) return setError('示例 ID 只能使用小写字母、数字、点、下划线或连字符。')
    if (!normalized.name) return setError('请输入示例名称。')
    try {
      const parsed = new URL(normalized.baseUrl || '')
      if (!['http:', 'https:'].includes(parsed.protocol) || !parsed.host || parsed.username || parsed.password || parsed.search || parsed.hash) return setError('Base URL 必须是无账号、查询参数和片段的完整 HTTP(S) 地址。')
    } catch { return setError('请输入完整有效的 HTTP(S) Base URL。') }
    if (Object.values(normalized).some(item => typeof item === 'string' && /sk-[a-z0-9_-]{8,}|access_token=|bearer\s+[a-z0-9._~+/=-]{8,}/i.test(item))) return setError('检测到疑似凭证内容。示例中禁止保存 API Key、Token 或认证头。')
    setBusy(true)
    try { await save(normalized) } catch (e) { setError(e instanceof Error ? e.message : '保存供应商示例失败') } finally { setBusy(false) }
  }
  return <div className="provider-example-dialog-overlay"><button className="provider-example-dialog-scrim" aria-label="关闭供应商示例编辑" onClick={close}/><section ref={dialogRef} className="provider-example-dialog" role="dialog" aria-modal="true" aria-labelledby="provider-example-dialog-title"><header><div><span>{example ? '编辑非敏感模板' : '创建非敏感模板'}</span><h2 id="provider-example-dialog-title">{example ? example.name : '新增供应商示例'}</h2></div><button className="icon-button" aria-label="关闭" onClick={close}><X/></button></header><div className="provider-example-dialog-body"><div className="provider-example-guard"><ShieldCheck/><span><strong>此表单不会接收凭证</strong><small>API Key、Token、认证头和 Webhook 均不属于示例信息，请在服务端环境变量或本地配置中维护。</small></span></div><div className="field-grid"><label className="field"><span>示例名称 *</span><input data-initial-focus value={value.name} maxLength={160} onChange={event => patch('name', event.target.value)}/></label><label className="field"><span>示例 ID *</span><input value={value.id} disabled={Boolean(example)} pattern="[a-z0-9][a-z0-9._-]*" spellCheck={false} autoCapitalize="none" onChange={event => patch('id', event.target.value.toLowerCase())}/><small>{example ? '编辑时不可更改 ID' : '例如 codex-ray-compatible'}</small></label></div><fieldset className="provider-example-cli"><legend>客户端类目 *</legend><button type="button" className={value.cli === 'codex' ? 'selected' : ''} aria-pressed={value.cli === 'codex'} onClick={() => patch('cli', 'codex')}><CliIcon cli="codex"/><span><strong>Codex</strong><small>OpenAI Codex CLI</small></span><Check/></button><button type="button" className={value.cli === 'claude' ? 'selected' : ''} aria-pressed={value.cli === 'claude'} onClick={() => patch('cli', 'claude')}><CliIcon cli="claude"/><span><strong>Claude Code</strong><small>Anthropic CLI</small></span><Check/></button></fieldset><label className="field"><span>Base URL *</span><input type="url" inputMode="url" spellCheck={false} autoCapitalize="none" placeholder="https://api.example.com/v1" value={value.baseUrl} onChange={event => patch('baseUrl', event.target.value)}/><small>只允许无账号、查询参数和片段的 HTTP(S) 地址</small></label><div className="field-grid"><label className="field"><span>模型</span><input spellCheck={false} placeholder="例如 gpt-5" value={value.model} onChange={event => patch('model', event.target.value)}/></label><label className="field"><span>Provider 标识</span><input spellCheck={false} placeholder="例如 custom" value={value.provider} onChange={event => patch('provider', event.target.value)}/></label></div><label className="field"><span>说明</span><textarea rows={3} maxLength={2048} placeholder="说明兼容范围、使用场景或模型限制，不要粘贴认证信息。" value={value.description} onChange={event => patch('description', event.target.value)}/></label>{error && <div className="form-error" role="alert"><AlertCircle/>{error}</div>}</div><footer><button className="secondary" disabled={busy} onClick={close}>取消</button><button className="primary" disabled={busy} onClick={() => void submit()}>{busy ? <LoaderCircle className="spinning"/> : <Save/>}{busy ? '保存中' : example ? '保存修改' : '创建示例'}</button></footer></section></div>
}
function ProviderExampleDeleteDialog({ example, close, remove }: { example: ProviderExample; close: () => void; remove: () => Promise<void> }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const dialogRef = useRef<HTMLElement>(null)
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    const focusable = () => Array.from(dialogRef.current?.querySelectorAll<HTMLElement>('button:not([disabled])') || [])
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') close()
      if (event.key !== 'Tab') return
      const items = focusable(); if (!items.length) return
      const first = items[0]; const last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', keydown); focusable()[0]?.focus()
    return () => { window.removeEventListener('keydown', keydown); previous?.focus() }
  }, [])
  const confirm = async () => { setBusy(true); setError(''); try { await remove() } catch (e) { setError(e instanceof Error ? e.message : '删除供应商示例失败'); setBusy(false) } }
  return <div className="provider-example-dialog-overlay"><button className="provider-example-dialog-scrim" aria-label="取消删除供应商示例" onClick={close}/><section ref={dialogRef} className="provider-example-delete" role="dialog" aria-modal="true" aria-labelledby="delete-provider-example-title"><div className="event-confirm-icon"><Trash2/></div><h2 id="delete-provider-example-title">删除“{example.name}”？</h2><p>此操作只删除示例模板，不会删除本地 Provider、凭证、计划任务或运行记录。删除后无法撤销。</p>{error && <div className="form-error" role="alert"><AlertCircle/>{error}</div>}<div><button className="secondary" disabled={busy} onClick={close}>取消</button><button className="danger-button" disabled={busy} onClick={() => void confirm()}>{busy ? <LoaderCircle className="spinning"/> : <Trash2/>}{busy ? '正在删除' : '确认删除'}</button></div></section></div>
}
function JobRow({ job, open }: { job: JobSummary; open: () => void }) {
  const [seconds, setSeconds] = useState(0)
  useEffect(() => { const update = () => setSeconds(job.nextAttemptAt ? Math.max(0, Math.ceil((new Date(job.nextAttemptAt).getTime() - Date.now()) / 1000)) : 0); update(); const t = setInterval(update, 1000); return () => clearInterval(t) }, [job.nextAttemptAt])
  return <button className="job-row" onClick={open}><CliIcon cli={job.cli}/><div className="job-main"><div><strong>{cliLabel(job.cli)} · {executionLabel(job.mode, job.runOnce)}</strong><StatusPill status={job.status}/></div><span>{job.providerName || job.providerId || '当前配置'}{job.model ? ` · ${job.model}` : ''}</span></div><div className="job-stat"><span>尝试次数</span><strong>{job.attemptCount}</strong></div><div className="job-stat"><span>{job.mode === 'keepalive' && !job.runOnce ? '下次执行' : '已运行'}</span><strong>{seconds ? `${seconds}s` : formatAgo(job.startedAt)}</strong></div><ChevronRight className="row-arrow"/></button>
}

function NewJobDrawer({ providers, initialProvider, initialExample, defaultOptions, close, onStarted }: { providers: Provider[]; initialProvider: Provider | null; initialExample: ProviderExample | null; defaultOptions: JobOptions; close: () => void; onStarted: (job: JobSummary, notifyOnComplete: boolean) => void }) {
  const [step, setStep] = useState(initialProvider ? 5 : initialExample ? 3 : 1)
  const [mode, setMode] = useState<JobMode>('probe')
  const [cli, setCli] = useState<Cli>(initialProvider?.cli ?? initialExample?.cli ?? 'codex')
  const [exampleReference, setExampleReference] = useState(initialExample)
  const filtered = useMemo(() => providers.filter(p => p.cli === cli), [providers, cli])
  const [providerId, setProviderId] = useState(initialProvider?.id ?? '')
  const [options, setOptions] = useState<JobOptions>({ ...defaultOptions, model: initialExample?.model || defaultOptions.model })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const drawerRef = useRef<HTMLElement>(null)
  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null
    const focusable = () => Array.from(drawerRef.current?.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])') ?? [])
    focusable()[0]?.focus()
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') close()
      if (event.key !== 'Tab') return
      const items = focusable(); if (!items.length) return
      const first = items[0]; const last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => { window.removeEventListener('keydown', onKeyDown); previousFocus?.focus() }
  }, [close])
  useEffect(() => { setProviderId(current => filtered.some(p => p.id === current) ? current : (filtered.find(p => p.current)?.id || filtered[0]?.id || '')) }, [cli, filtered])
  const selected = filtered.find(p => p.id === providerId)
  const canNext = step !== 3 || Boolean(selected)
  const submit = async () => {
    setBusy(true); setError('')
    const body: StartJobRequest = { mode, cli, providerId, options }
    try { onStarted(await api.startJob(body), options.notifyOnComplete) } catch (e) { setError(e instanceof Error ? e.message : '任务启动失败') } finally { setBusy(false) }
  }
  return <div className="overlay"><div className="overlay-scrim" onClick={close}/><aside ref={drawerRef} className="drawer" role="dialog" aria-modal="true" aria-labelledby="new-job-title">
    <div className="drawer-header"><div><span>新建任务</span><h2 id="new-job-title">{step === 1 ? '选择运行模式' : step === 2 ? '选择客户端' : step === 3 ? '选择配置源' : step === 4 ? '高级参数' : '确认并启动'}</h2></div><button className="icon-button" onClick={close} aria-label="关闭新建任务"><X/></button></div>
    <div className="steps">{[1,2,3,4,5].map(n => <div key={n} className={`${n === step ? 'active' : ''} ${n < step ? 'done' : ''}`}><span>{n < step ? <Check/> : n}</span><i/></div>)}</div>
    <div className="drawer-body">
      {exampleReference && <div className="example-reference-banner"><BookOpen/><span><strong>参考模板：{exampleReference.name}</strong><small>已预选 {cliLabel(exampleReference.cli)}{exampleReference.model ? `，并参考模型 ${exampleReference.model}` : ''}。模板不含密钥，请选择一个可用的本地配置源。</small></span></div>}
      {step === 1 && <div className="choice-grid"><Choice active={mode === 'probe' && options.runOnce} onClick={() => { setMode('probe'); setOptions(current => ({ ...current, runOnce: true })) }} icon={<Gauge/>} title="一次测活" tag="执行一次" detail="只调用一次所选 CLI，并立即返回本次探测结果。" footer="适合快速检查当前连通性"/><Choice active={mode === 'probe' && !options.runOnce} onClick={() => { setMode('probe'); setOptions(current => ({ ...current, runOnce: false })) }} icon={<RotateCcw/>} title="持续测活" tag="直至成功" detail="按重试间隔持续探测，成功或遇到不可恢复错误后结束。" footer="适合等待服务恢复可用"/><Choice active={mode === 'keepalive' && options.runOnce} onClick={() => { setMode('keepalive'); setOptions(current => ({ ...current, runOnce: true })) }} icon={<Activity/>} title="一次保活" tag="单轮观测" detail="按保活规则执行一轮检查，不进入后续周期。" footer="适合验证保活参数"/><Choice active={mode === 'keepalive' && !options.runOnce} onClick={() => { setMode('keepalive'); setOptions(current => ({ ...current, runOnce: false })) }} icon={<TimerReset/>} title="持续保活" tag="持续运行" detail="立即检查一次，之后按固定间隔持续观测，直到手动停止。" footer="适合长期观测服务稳定性"/></div>}
      {step === 2 && <div className="choice-grid"><Choice active={cli === 'codex'} onClick={() => { setCli('codex'); if (exampleReference?.cli !== 'codex') setExampleReference(null) }} icon={<Command/>} title="Codex CLI" tag="OpenAI" detail="使用只读沙箱与临时会话，检查 Codex 连接状态。" footer="支持当前配置与 CC Switch"/><Choice active={cli === 'claude'} onClick={() => { setCli('claude'); if (exampleReference?.cli !== 'claude') setExampleReference(null) }} icon={<Bot/>} title="Claude CLI" tag="Anthropic" detail="禁用工具与会话持久化，安全检查 Claude 连接状态。" footer="支持当前配置与 CC Switch"/></div>}
      {step === 3 && <div><div className="inline-note"><ShieldCheck/><span>选择只影响当前任务，不会切换 CC Switch 的当前 Provider。</span></div><div className="provider-grid">{filtered.length ? filtered.map(p => <ProviderCard key={p.id || `current-${p.cli}`} provider={p} selected={providerId === p.id} onClick={() => setProviderId(p.id)}/>) : <EmptyState icon={<Database/>} title="没有可用配置" detail={`未发现 ${cliLabel(cli)} 当前配置或 CC Switch Provider。`}/>}</div></div>}
      {step === 4 && <AdvancedFields mode={mode} cli={cli} options={options} setOptions={setOptions}/>} 
      {step === 5 && <div className="confirmation"><div className="confirm-hero"><div className="confirm-orbit"><CliIcon cli={cli}/><i/></div><span>即将启动</span><h3>{cliLabel(cli)} {executionLabel(mode, options.runOnce)}任务</h3><p>所有 CLI 输出只在运行时通过内存实时传递，任务结束后立即销毁。</p></div><div className="confirm-list"><Confirm label="运行模式" value={executionLabel(mode, options.runOnce)}/><Confirm label="客户端" value={cliLabel(cli)}/>{exampleReference && <Confirm label="参考模板" value={exampleReference.name}/>}<Confirm label="配置源" value={selected?.name || providerId}/><Confirm label="模型" value={options.model || selected?.model || '跟随配置'}/><Confirm label="单次超时" value={`${options.timeoutSeconds} 秒`}/>{!options.runOnce && <Confirm label={mode === 'probe' ? '重试间隔' : '保活间隔'} value={`${mode === 'probe' ? options.retryIntervalSeconds : options.keepaliveIntervalSeconds} 秒`}/>} {mode === 'keepalive' && !options.runOnce && <Confirm label="失败转测活阈值" value={`${options.failureThreshold} 次`}/>}</div></div>}
      {error && <div className="form-error" role="alert"><AlertCircle/>{error}</div>}
    </div>
    <div className="drawer-footer"><button className="secondary" onClick={() => step === 1 ? close() : setStep(step - 1)}>{step === 1 ? '取消' : <><ChevronLeft/>上一步</>}</button>{step < 5 ? <button className="primary" disabled={!canNext} onClick={() => setStep(step + 1)}>继续<ChevronRight/></button> : <button className="primary launch" disabled={busy} onClick={() => void submit()}>{busy ? <LoaderCircle className="spinning"/> : <Play/>}{busy ? '正在启动' : '启动任务'}</button>}</div>
  </aside></div>
}

function Choice({ active, onClick, icon, title, tag, detail, footer }: { active: boolean; onClick: () => void; icon: React.ReactNode; title: string; tag: string; detail: string; footer: string }) { return <button className={`choice-card ${active ? 'selected' : ''}`} aria-pressed={active} onClick={onClick}><span className="choice-check">{active && <Check/>}</span><div className="choice-icon">{icon}</div><div className="choice-title"><h3>{title}</h3><em>{tag}</em></div><p>{detail}</p><small><CheckCircle2/>{footer}</small></button> }
function ProviderCard({ provider, selected, onClick }: { provider: Provider; selected: boolean; onClick: () => void }) { return <button className={`provider-card ${selected ? 'selected' : ''}`} aria-pressed={selected} onClick={onClick}><span className="radio-dot"><i/></span><div className="provider-top"><CliIcon cli={provider.cli}/><div><strong>{provider.name}</strong><span>{provider.source === 'current' ? '当前 CLI 配置' : 'CC Switch Provider'}</span></div>{provider.current && <em>当前</em>}</div><dl><div><dt>模型</dt><dd>{provider.model || '跟随配置'}</dd></div><div><dt>Base URL</dt><dd>{provider.baseUrl || '默认地址'}</dd></div><div><dt>API Key</dt><dd><KeyRound/>{provider.maskedApiKey || '环境变量'}</dd></div></dl></button> }
function Confirm({ label, value }: { label: string; value: string }) { return <div><span>{label}</span><strong>{value}</strong></div> }

function AdvancedFields({ mode, cli, options, setOptions }: { mode: JobMode; cli: Cli; options: JobOptions; setOptions: (v: JobOptions) => void }) {
  const patch = (key: keyof JobOptions, value: string | number | boolean) => setOptions({ ...options, [key]: value })
  return <div className="form-sections"><FormSection title="运行节奏" detail={options.runOnce ? '当前任务只执行一轮，不会进入重试或后续保活周期' : mode === 'keepalive' ? '控制调用节奏，以及连续失败后何时进入恢复测活' : '控制单次调用与下一次尝试的时间'}><div className="field-grid"><NumberField label="单次超时" value={options.timeoutSeconds} suffix="秒" min={5} onChange={v => patch('timeoutSeconds', v)}/>{!options.runOnce && (mode === 'probe' ? <NumberField label="重试间隔" value={options.retryIntervalSeconds} suffix="秒" min={1} onChange={v => patch('retryIntervalSeconds', v)}/> : <><NumberField label="保活间隔" value={options.keepaliveIntervalSeconds} suffix="秒" min={10} onChange={v => patch('keepaliveIntervalSeconds', v)}/><NumberField label="失败转测活阈值" value={options.failureThreshold} suffix="次" min={1} onChange={v => patch('failureThreshold', v)}/></>)}</div></FormSection><FormSection title="探测内容" detail="CLI 应当按照提示返回期望文本"><label className="field"><span>Prompt</span><textarea value={options.prompt} rows={3} onChange={e => patch('prompt', e.target.value)}/></label><label className="field"><span>期望文本</span><input value={options.expectedText} onChange={e => patch('expectedText', e.target.value)}/></label></FormSection>{cli === 'codex' ? <FormSection title="Codex 参数" detail="覆盖当前 Provider 的请求重试策略"><div className="field-grid"><NumberField label="请求重试" value={options.requestMaxRetries} min={0} onChange={v => patch('requestMaxRetries', v)}/><NumberField label="流式重试" value={options.streamMaxRetries} min={0} onChange={v => patch('streamMaxRetries', v)}/></div><label className="field"><span>模型（可选）</span><input placeholder="跟随 Provider 配置" value={options.model} onChange={e => patch('model', e.target.value)}/></label></FormSection> : <FormSection title="Claude 参数" detail="可选模型与会话显示名称"><div className="field-grid"><label className="field"><span>模型（可选）</span><input placeholder="跟随配置" value={options.model} onChange={e => patch('model', e.target.value)}/></label><label className="field"><span>Fallback 模型</span><input placeholder="可留空" value={options.fallbackModel} onChange={e => patch('fallbackModel', e.target.value)}/></label></div><label className="field"><span>会话名称</span><input value={options.sessionName} onChange={e => patch('sessionName', e.target.value)}/></label></FormSection>}<label className="toggle-row"><div><strong>任务结束通知</strong><span>允许浏览器在测活完成时发送系统通知</span></div><input type="checkbox" checked={options.notifyOnComplete} onChange={e => patch('notifyOnComplete', e.target.checked)}/><i/></label></div>
}
function FormSection({ title, detail, children }: { title: string; detail: string; children: React.ReactNode }) { return <section className="form-section"><div className="form-section-title"><h3>{title}</h3><p>{detail}</p></div>{children}</section> }
function NumberField({ label, value, suffix, min, max, onChange }: { label: string; value: number; suffix?: string; min: number; max?: number; onChange: (v: number) => void }) { return <label className="field"><span>{label}</span><div className="number-input"><input type="number" min={min} max={max} value={value} onChange={e => onChange(Math.min(max ?? Number.MAX_SAFE_INTEGER, Math.max(min, Number(e.target.value))))}/>{suffix && <em>{suffix}</em>}</div></label> }

function JobDetail({ initial, notifyOnComplete, close, onChanged }: { initial: JobSummary; notifyOnComplete: boolean; close: () => void; onChanged: () => void }) {
  const [job, setJob] = useState(initial)
  const [events, setEvents] = useState<JobEvent[]>([])
  const [connected, setConnected] = useState(false)
  const [paused, setPaused] = useState(false)
  const [stopping, setStopping] = useState(false)
  const outputRef = useRef<HTMLDivElement>(null)
  const previousStatus = useRef(initial.status)
  const running = job.status === 'running' || job.status === 'starting'
  useEffect(() => {
    if (!running) { setEvents([]); return }
    const source = new EventSource(api.eventsUrl(job.id))
    source.onopen = () => setConnected(true)
    source.onerror = () => setConnected(false)
    const handleEvent = (e: MessageEvent) => {
      try {
        const event = normalizeEvent(JSON.parse(e.data))
        if (event.job) setJob(event.job)
        if (event.type === 'cleanup' || (event.job && !['running','starting'].includes(event.job.status))) setEvents([])
        else setEvents(prev => [...prev.slice(-499), event])
        if (event.type !== 'log') void api.getJob(job.id).then(setJob).catch(() => undefined)
      } catch { /* ignore malformed heartbeats */ }
    }
    const eventNames = ['output', 'error', 'cleanup', 'attempt_start', 'classification', 'job_state', 'countdown']
    eventNames.forEach(name => source.addEventListener(name, handleEvent as EventListener))
    return () => { source.close(); setConnected(false); setEvents([]) }
  }, [job.id, running])
  useEffect(() => { if (!paused) outputRef.current?.scrollTo({ top: outputRef.current.scrollHeight, behavior: 'smooth' }) }, [events, paused])
  useEffect(() => {
    const wasRunning = previousStatus.current === 'running' || previousStatus.current === 'starting'
    const isFinished = job.status !== 'running' && job.status !== 'starting'
    if (wasRunning && isFinished && notifyOnComplete && typeof Notification !== 'undefined' && Notification.permission === 'granted') {
      new Notification(`AI Watch · ${statusMeta[job.status].label}`, { body: `${cliLabel(job.cli)} ${modeLabel(job.mode)}任务：${job.providerName || job.providerId || '当前配置'}` })
    }
    previousStatus.current = job.status
  }, [job.status, job.cli, job.mode, job.providerId, job.providerName, notifyOnComplete])
  const stop = async () => { setStopping(true); try { const next = await api.stopJob(job.id); setJob(next); setEvents([]); onChanged() } finally { setStopping(false) } }
  const copy = () => void navigator.clipboard.writeText(events.map(e => `[${new Date(e.timestamp).toLocaleTimeString()}] ${e.message ?? e.type}`).join('\n'))
  return <div className="detail-overlay"><div className="detail-header"><button className="icon-button" onClick={close}><ChevronLeft/></button><CliIcon cli={job.cli}/><div><span>{cliLabel(job.cli)} · {executionLabel(job.mode, job.runOnce)}</span><h2>{job.providerName || job.providerId || '当前配置'}</h2></div><StatusPill status={job.status}/><div className="detail-actions">{running && <button className="danger-button" disabled={stopping} onClick={() => void stop()}>{stopping ? <LoaderCircle className="spinning"/> : <Square/>}停止任务</button>}<button className="icon-button" onClick={close}><X/></button></div></div><div className="detail-body"><section className="detail-stats"><div><span>任务 ID</span><strong className="mono">{job.id.slice(0, 12)}</strong></div><div><span>尝试次数</span><strong>{job.attemptCount}</strong></div><div><span>已运行</span><strong>{formatDuration(job.elapsedMs ?? Date.now() - new Date(job.startedAt).getTime())}</strong></div><div><span>模式 / 最近结果</span><strong>{executionLabel(job.mode, job.runOnce)} · {job.lastAttemptStatus || '等待中'}</strong></div></section><section className="terminal-card"><div className="terminal-bar"><div className="window-dots"><i/><i/><i/></div><div className={`stream-state ${connected ? 'online' : ''}`}>{connected ? <Wifi/> : <WifiOff/>}{connected ? '实时连接' : running ? '正在重连' : '连接已关闭'}</div><div className="terminal-actions"><button onClick={() => setPaused(!paused)}>{paused ? <Play/> : <Pause/>}{paused ? '继续滚动' : '暂停滚动'}</button><button onClick={copy} disabled={!events.length}><Copy/>复制</button></div></div><div className="terminal-output" ref={outputRef}>{events.length ? events.map((event, index) => <div className={`log-line ${event.level || ''}`} key={event.id || `${event.timestamp}-${index}`}><time>{new Date(event.timestamp).toLocaleTimeString('zh-CN', { hour12: false })}</time><span>{event.level === 'command' ? '$' : event.level === 'success' ? '✓' : event.level === 'error' ? '×' : '›'}</span><code>{event.message || event.type}</code></div>) : <div className="terminal-empty">{running ? <><LoaderCircle className="spinning"/><span>等待 CLI 输出…</span></> : <><Trash2/><span>任务已结束，实时日志已从内存销毁。</span></>}</div>}</div><div className="terminal-foot"><ShieldCheck/><span>此处输出不会写入磁盘，任务结束后自动清空</span><em>最大 500 行内存缓冲</em></div></section></div></div>
}

function SettingsView() {
  const [settings, setSettings] = useState<AppSettings | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')
  const [messageTone, setMessageTone] = useState<'success' | 'error'>('success')
  const [testing, setTesting] = useState<'test' | 'status' | null>(null)
  useEffect(() => { api.settings().then(setSettings).catch(e => setMessage(e instanceof Error ? e.message : '加载失败')).finally(() => setLoading(false)) }, [])
  const patch = (key: keyof AppSettings, value: number | boolean) => settings && setSettings({ ...settings, [key]: value })
  const save = async () => { if (!settings) return; setSaving(true); setMessage(''); try { setSettings(await api.saveSettings(settings)); setMessageTone('success'); setMessage('设置已保存') } catch (e) { setMessageTone('error'); setMessage(e instanceof Error ? e.message : '保存失败') } finally { setSaving(false) } }
  const testNotification = async (kind: 'test' | 'status') => {
    setTesting(kind); setMessage('')
    try {
      if (kind === 'test') await api.testDingTalk(); else await api.sendDingTalkStatus()
      setMessageTone('success'); setMessage(kind === 'test' ? '测试通知已发送' : '状态通知已发送')
    } catch (e) { setMessageTone('error'); setMessage(e instanceof Error ? e.message : '通知发送失败') } finally { setTesting(null) }
  }
  const browserPermission = typeof Notification === 'undefined' ? 'unsupported' : Notification.permission
  return <div className="page settings-page"><section className="page-heading"><div><span className="eyebrow"><Settings/>全局偏好</span><h1>设置与通知</h1><p>为后续任务定义默认节奏与通知聚合策略。所有敏感通知凭证只存在于服务端环境变量。</p></div></section>{loading ? <div className="settings-loading"><LoaderCircle className="spinning"/>正在读取设置</div> : settings && <div className="settings-grid"><section className="panel settings-panel"><PanelTitle title="任务默认值" detail="新建任务时会自动带入，可在任务中临时调整"/><div className="setting-fields"><NumberField label="单次调用超时" value={settings.timeoutSeconds} suffix="秒" min={5} onChange={v => patch('timeoutSeconds', v)}/><NumberField label="测活重试间隔" value={settings.retryIntervalSeconds} suffix="秒" min={1} onChange={v => patch('retryIntervalSeconds', v)}/><NumberField label="保活执行间隔" value={settings.keepaliveIntervalSeconds} suffix="秒" min={10} onChange={v => patch('keepaliveIntervalSeconds', v)}/><NumberField label="摘要保留数量" value={settings.historyLimit} suffix="条" min={10} onChange={v => patch('historyLimit', v)}/><NumberField label="事件保留天数" value={settings.eventRetentionDays} suffix="天" min={1} onChange={v => patch('eventRetentionDays', v)}/><NumberField label="事件最大条数" value={settings.eventRetentionRows} suffix="条" min={100} onChange={v => patch('eventRetentionRows', v)}/><NumberField label="事件容量上限" value={Math.max(1, Math.round(settings.eventRetentionBytes / 1048576))} suffix="MiB" min={1} onChange={v => patch('eventRetentionBytes', v * 1048576)}/></div><div className="settings-callout"><Database/><div><strong>仅保存摘要元数据</strong><span>状态、运行时间、尝试次数与耗时会保留；Prompt、API Key 与 CLI 原始输出不会入库。</span></div></div></section><section className="panel settings-panel notification-panel"><PanelTitle title="通知渠道" detail="凭证留在服务端，浏览器只控制策略与测试"/><div className="notification-list"><div className="notification-card"><div className="notification-icon browser"><Bell/></div><div><strong>浏览器通知</strong><span>{browserPermission === 'granted' ? '权限已允许' : browserPermission === 'denied' ? '权限已被浏览器阻止' : browserPermission === 'unsupported' ? '当前浏览器不支持系统通知' : '替代容器中不可用的 macOS 通知'}</span></div><button className={`switch ${settings.browserNotifications ? 'on' : ''}`} aria-label="切换浏览器通知" aria-pressed={settings.browserNotifications} disabled={browserPermission === 'unsupported' || browserPermission === 'denied'} onClick={async () => { if (!settings.browserNotifications && browserPermission === 'default') await Notification.requestPermission(); patch('browserNotifications', !settings.browserNotifications) }}><i/></button></div><div className="notification-card notification-card-dingtalk"><div className="notification-icon dingtalk"><Zap/></div><div><strong>钉钉机器人</strong><span>{settings.dingTalkConfigured ? 'Webhook 已通过服务端环境变量配置，可直接验证通知链路' : '需要设置 DINGTALK_WEBHOOK_URL 环境变量'}</span></div><span className={`config-badge ${settings.dingTalkConfigured ? 'ok' : ''}`}>{settings.dingTalkConfigured ? '已配置' : '未配置'}</span></div><div className="notification-test-actions"><button className="secondary" disabled={!settings.dingTalkConfigured || testing !== null} onClick={() => void testNotification('test')}>{testing === 'test' ? <LoaderCircle className="spinning"/> : <Send/>}{testing === 'test' ? '发送中' : '发送测试通知'}</button><button className="secondary" disabled={!settings.dingTalkConfigured || testing !== null} onClick={() => void testNotification('status')}>{testing === 'status' ? <LoaderCircle className="spinning"/> : <Activity/>}{testing === 'status' ? '发送中' : '发送状态通知'}</button></div></div><div className="secret-note"><KeyRound/><span>Webhook 永远不会发送到浏览器，也不会保存在设置数据库中。</span></div></section><section className="panel settings-panel notification-policy-panel"><PanelTitle title="通知聚合策略" detail="降低保活噪声，同时保留长时间探测与恢复信号"/><div className="notification-policy-intro"><Sparkles/><span><strong>按窗口合并消息</strong><small>保活成功可按时间或次数汇总；多个 Provider 只有在合并窗口大于 0 时才会合并恢复通知。</small></span></div><div className="setting-fields notification-policy-fields"><NumberField label="保活按时间汇总" value={settings.keepaliveSummarySeconds} suffix="秒" min={0} max={604800} onChange={v => patch('keepaliveSummarySeconds', v)}/><NumberField label="保活按成功次数汇总" value={settings.keepaliveSummarySuccesses} suffix="次" min={0} max={1000000} onChange={v => patch('keepaliveSummarySuccesses', v)}/><NumberField label="测活进度通知间隔" value={settings.probeProgressSeconds} suffix="秒" min={1} max={604800} onChange={v => patch('probeProgressSeconds', v)}/><NumberField label="恢复通知合并窗口" value={settings.recoveryMergeSeconds} suffix="秒" min={0} max={86400} onChange={v => patch('recoveryMergeSeconds', v)}/></div><div className="notification-policy-foot"><CircleDot/><span><strong>恢复合并为 0 时保留单 Provider 模板</strong><small>保活时间或次数设为 0 会关闭对应汇总条件；两个条件同时启用时，任一条件先达到即发送。</small></span></div></section></div>}{message && <div className={`toast-inline ${messageTone === 'success' ? 'success' : ''}`} role="status" aria-live="polite">{messageTone === 'success' ? <CheckCircle2/> : <AlertCircle/>}{message}</div>}<div className="sticky-save"><div><strong>修改全局默认值</strong><span>不会影响已经运行的任务</span></div><button className="primary" disabled={!settings || saving} onClick={() => void save()}>{saving ? <LoaderCircle className="spinning"/> : <Save/>}{saving ? '保存中' : '保存设置'}</button></div></div>
}
