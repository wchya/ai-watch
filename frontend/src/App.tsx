import { useCallback, useEffect, useRef, useState } from 'react'
import {
  CalendarClock, Check, ChevronRight, FlaskConical, Gauge, History, KeyRound, Menu, Palette, Plus, RefreshCw,
  Settings, ShieldCheck, TrendingUp, Wifi, WifiOff, X,
} from 'lucide-react'
import { api } from './api'
import { Dashboard, EventsView, JobDetail, Logo, NewJobDrawer, SettingsView } from './AppFeatures'
import { DiagnosticsView } from './DiagnosticsView'
import { ComparisonHistoryView } from './ComparisonHistoryView'
import { FailoverGroupsView } from './FailoverGroupsView'
import { IncidentsView } from './IncidentsView'
import { MaintenanceView } from './MaintenanceView'
import { NotificationRoutingView } from './NotificationRoutingView'
import { canonicalizeLegacyPath, centerForView, isViewIn, primaryNavigation, routePath, routeTitle, viewFromPath, type NavIcon, type View } from './navigation'
import { ProviderConfigView } from './ProviderConfigView'
import { ReliabilityView } from './ReliabilityView'
import { RequestDetailView } from './RequestDetailView'
import { SchedulesView } from './SchedulesView'
import { SLOView } from './SLOView'
import { TestScenariosView } from './TestScenariosView'
import type {
  AppSettings, DashboardData, JobOptions, JobSummary, Provider,
} from './types'

const navIcons: Record<NavIcon, React.ReactNode> = { dashboard: <Gauge/>, providers: <KeyRound/>, validation: <FlaskConical/>, automation: <CalendarClock/>, stability: <TrendingUp/>, events: <History/>, settings: <Settings/> }
const requestFromPath = (pathname: string) => {
  const match = pathname.match(/^\/requests\/([^/]+)\/?$/)
  if (!match) return ''
  try { return decodeURIComponent(match[1]) } catch { return '' }
}

const DEFAULT_OPTIONS: JobOptions = {
  runOnce: true,
  timeoutSeconds: 45,
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
export function App() {
  const initialRequestId = requestFromPath(window.location.pathname)
  const [view, setView] = useState<View>(() => viewFromPath(window.location.pathname) ?? (initialRequestId ? 'events' : 'dashboard'))
  const [requestId, setRequestId] = useState(initialRequestId)
  const [data, setData] = useState<DashboardData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [presetProvider, setPresetProvider] = useState<Provider | null>(null)
  const [jobDefaults, setJobDefaults] = useState<JobOptions>(DEFAULT_OPTIONS)
  const [notificationJobs, setNotificationJobs] = useState<Set<string>>(() => new Set())
  const [detailJob, setDetailJob] = useState<JobSummary | null>(null)
  const [mobileNav, setMobileNav] = useState(false)
  const [eventsRefreshToken, setEventsRefreshToken] = useState(0)
  const [eventsProviderFilter, setEventsProviderFilter] = useState('')
  const [providersRefreshToken, setProvidersRefreshToken] = useState(0)
  const [uiTheme, setUiTheme] = useState<AppSettings['uiTheme']>('deep-ocean')
  const [themeOpen, setThemeOpen] = useState(false)
  const [themeSaving, setThemeSaving] = useState(false)
  const [themeMessage, setThemeMessage] = useState('')
  const themeRef = useRef<HTMLDivElement>(null)

  const navigate = useCallback((next: View) => {
    if (next !== view || requestId) {
      window.history.pushState({}, '', routePath(next))
      setView(next)
      setRequestId('')
    }
    setMobileNav(false)
  }, [requestId, view])

  const openRequest = useCallback((nextRequestId: string) => {
    const value = nextRequestId.trim()
    if (!value) return
    window.history.pushState({ aiWatchRequest: true }, '', `/requests/${encodeURIComponent(value)}`)
    setRequestId(value)
    setMobileNav(false)
  }, [])

  const closeRequest = useCallback(() => {
    if (window.history.state?.aiWatchRequest) window.history.back()
    else navigate('events')
  }, [navigate])

  useEffect(() => {
    const initialView = viewFromPath(window.location.pathname)
    if (!initialView && !requestFromPath(window.location.pathname)) window.history.replaceState({}, '', routePath('dashboard'))
    else {
      const canonical = canonicalizeLegacyPath(window.location.pathname)
      if (canonical) window.history.replaceState({}, '', canonical)
    }
    const onPopState = () => {
      const nextRequestId = requestFromPath(window.location.pathname)
      setRequestId(nextRequestId)
      if (!nextRequestId) setView(viewFromPath(window.location.pathname) ?? 'dashboard')
    }
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])

  const load = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true)
    try { setData(await api.dashboard()); setError('') }
    catch (e) { setError(e instanceof Error ? e.message : '无法连接 AI Watch 服务') }
    finally { setLoading(false) }
  }, [])

  useEffect(() => {
    void load()
    const refresh = () => { if (!document.hidden) void load(true) }
    const t = window.setInterval(refresh, 10_000)
    const visible = () => { if (!document.hidden) void load(true) }
    document.addEventListener('visibilitychange', visible)
    return () => { window.clearInterval(t); document.removeEventListener('visibilitychange', visible) }
  }, [load])
  useEffect(() => {
    void api.settings().then(settings => {
      setUiTheme(settings.uiTheme)
      setJobDefaults(current => ({ ...current, timeoutSeconds: settings.timeoutSeconds, retryIntervalSeconds: settings.retryIntervalSeconds, keepaliveIntervalSeconds: settings.keepaliveIntervalSeconds }))
    }).catch(cause => {
      setThemeMessage(cause instanceof Error ? `设置加载失败：${cause.message}` : '设置加载失败')
      window.setTimeout(() => setThemeMessage(''), 5000)
    })
  }, [])
  useEffect(() => {
    if (!themeOpen) return
    const close = (event: MouseEvent) => { if (!themeRef.current?.contains(event.target as Node)) setThemeOpen(false) }
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape') setThemeOpen(false) }
    document.addEventListener('mousedown', close); window.addEventListener('keydown', key)
    return () => { document.removeEventListener('mousedown', close); window.removeEventListener('keydown', key) }
  }, [themeOpen])

  const chooseTheme = async (next: AppSettings['uiTheme']) => {
    if (next === uiTheme || themeSaving) { setThemeOpen(false); return }
    const previous = uiTheme
    setUiTheme(next); setThemeSaving(true); setThemeMessage('')
    try {
      const settings = await api.settings()
      await api.saveSettings({ ...settings, uiTheme: next })
      setThemeMessage('主题已保存')
      window.setTimeout(() => setThemeMessage(''), 2400)
    } catch (cause) {
      setUiTheme(previous)
      setThemeMessage(cause instanceof Error ? `主题保存失败：${cause.message}` : '主题保存失败')
    } finally { setThemeSaving(false); setThemeOpen(false) }
  }

  const openJob = (job: JobSummary) => { setDetailJob(job); setMobileNav(false) }
  const onStarted = (job: JobSummary, notifyOnComplete: boolean) => { setDrawerOpen(false); setDetailJob(job); if (notifyOnComplete) setNotificationJobs(current => new Set(current).add(job.id)); void load(true) }
  const viewLabel = requestId ? '请求详情' : routeTitle(view)
  useEffect(() => { document.title = `AI Watch · ${viewLabel}` }, [viewLabel])

  return <div className={`app-shell theme-${uiTheme}`}>
    <div className="ambient ambient-a"/><div className="ambient ambient-b"/>
    <aside className={`sidebar ${mobileNav ? 'mobile-open' : ''}`}>
      <div className="sidebar-top"><Logo/><button className="icon-button mobile-close" onClick={() => setMobileNav(false)} aria-label="关闭菜单"><X/></button></div>
      <nav>
        {primaryNavigation.map(item => { const active = isViewIn(view, item.views); return <button key={item.label} className={active ? 'active' : ''} aria-current={active ? 'page' : undefined} onClick={() => navigate(item.defaultView)}>{navIcons[item.icon]}<span>{item.label}</span></button> })}
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
        <div className="top-actions"><div className="theme-picker" ref={themeRef}><button className="theme-trigger" aria-haspopup="menu" aria-expanded={themeOpen} onClick={() => setThemeOpen(current => !current)}><Palette/><span><small>界面主题</small><strong>{uiTheme === 'deep-ocean' ? '深海终端' : uiTheme === 'graphite-signal' ? '石墨信号' : '极昼控制台'}</strong></span><i className={`theme-dot ${uiTheme}`}/></button>{themeOpen && <div className="theme-popover" role="menu"><header><strong>切换全局主题</strong><small>选择后立即预览并保存</small></header>{([
          ['deep-ocean', '深海终端', '深蓝黑 · 青色信号'], ['graphite-signal', '石墨信号', '中性石墨 · 薄荷信号'], ['arctic-daylight', '极昼控制台', '浅灰蓝 · 深色数据'],
        ] as const).map(([value, label, detail]) => <button role="menuitemradio" aria-checked={uiTheme === value} key={value} disabled={themeSaving} onClick={() => void chooseTheme(value)}><i className={`theme-swatch ${value}`}/><span><strong>{label}</strong><small>{detail}</small></span>{uiTheme === value && <Check/>}</button>)}</div>}</div>{!requestId && (view === 'dashboard' || view === 'events' || view === 'providers') && <button className="icon-button" onClick={() => view === 'events' ? setEventsRefreshToken(current => current + 1) : view === 'providers' ? setProvidersRefreshToken(current => current + 1) : void load()} aria-label={view === 'events' ? '刷新事件' : view === 'providers' ? '刷新供应商' : '刷新总览'}><RefreshCw className={view === 'dashboard' && loading ? 'spinning' : ''}/></button>}{!requestId && view !== 'diagnostics' && <button className="primary compact" onClick={() => { setPresetProvider(null); setDrawerOpen(true) }}><Plus/>新建任务</button>}</div>
      </header>

      {!requestId && centerForView(view) && <CenterTabs label={centerForView(view)!.label} view={view} navigate={navigate} items={centerForView(view)!.views.map(target => [target, routeTitle(target)])}/>}
      {requestId ? <RequestDetailView requestId={requestId} back={closeRequest}/> : view === 'dashboard' ? <Dashboard data={data} loading={loading} error={error} retry={() => void load()} openNew={() => { setPresetProvider(null); setDrawerOpen(true) }} probeProvider={(provider) => { setPresetProvider(provider); setDrawerOpen(true) }} showProviderRequests={(provider) => { setEventsProviderFilter(provider.id); navigate('events') }} openJob={openJob}/> : view === 'providers' ? <ProviderConfigView discoveredProviders={(data?.providers ?? []).filter(provider => provider.source !== 'manual')} refreshToken={providersRefreshToken} onProbe={(provider) => { setPresetProvider(provider); setDrawerOpen(true) }} onChanged={() => void load(true)}/> : view === 'scenarios' ? <TestScenariosView openRequest={openRequest}/> : view === 'comparisons' ? <ComparisonHistoryView/> : view === 'failover' ? <FailoverGroupsView providers={data?.providers ?? []}/> : view === 'maintenance' ? <MaintenanceView/> : view === 'slos' ? <SLOView navigate={navigate}/> : view === 'reliability' ? <ReliabilityView/> : view === 'incidents' ? <IncidentsView openRequest={openRequest}/> : view === 'schedules' ? <SchedulesView providers={data?.providers ?? []} defaultOptions={jobDefaults} openRequest={openRequest} openJob={openJob}/> : view === 'events' ? <EventsView providers={data?.providers ?? []} refreshToken={eventsRefreshToken} initialProviderId={eventsProviderFilter} openRequest={openRequest}/> : view === 'notification-routing' ? <NotificationRoutingView/> : view === 'diagnostics' ? <DiagnosticsView/> : <SettingsView onThemeChanged={setUiTheme}/>}
    </main>
    {mobileNav && <div className="nav-scrim" onClick={() => setMobileNav(false)}/>}
    {drawerOpen && <NewJobDrawer providers={data?.providers ?? []} initialProvider={presetProvider} defaultOptions={jobDefaults} close={() => { setDrawerOpen(false); setPresetProvider(null) }} onStarted={onStarted}/>}
    {detailJob && <JobDetail initial={detailJob} notifyOnComplete={notificationJobs.has(detailJob.id)} close={() => { setDetailJob(null); void load(true) }} onChanged={() => void load(true)}/>}
    {themeMessage && <div className={`theme-toast ${themeMessage.includes('失败') ? 'error' : ''}`} role="status">{themeMessage}</div>}
  </div>
}

function CenterTabs({ label, view, navigate, items }: { label: string; view: View; navigate: (view: View) => void; items: Array<[View, string]> }) {
  return <nav className="center-tabs" aria-label={label}>{items.map(([target, title]) => <button key={target} className={view === target ? 'active' : ''} aria-current={view === target ? 'page' : undefined} onClick={() => navigate(target)}>{title}</button>)}</nav>
}
