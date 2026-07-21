import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity, AlertCircle, Bot, CalendarClock, Check, CheckCircle2, ChevronDown, ChevronLeft, ChevronRight, Clock3, Command, Edit3, FileText,
  Gauge, LoaderCircle, PauseCircle, Play, Plus, RefreshCw, ShieldCheck, Square, ExternalLink,
  Terminal, TimerReset, Trash2, X,
} from 'lucide-react'
import { api } from './api'
import { LIST_PAGE_SIZE, ListPagination } from './ListPagination'
import { Select } from './Select'
import { useDelayedRefresh } from './useDelayedRefresh'
import type {
  BulkJobAction, Cli, JobOptions, Provider, Schedule, ScheduleLastStatus, OperationalEvent, JobSummary,
  ProviderFailoverGroup, ScheduleWriteRequest, TestScenario,
} from './types'

const WEEKDAYS = [
  { label: '一', long: '周一', bit: 2 },
  { label: '二', long: '周二', bit: 4 },
  { label: '三', long: '周三', bit: 8 },
  { label: '四', long: '周四', bit: 16 },
  { label: '五', long: '周五', bit: 32 },
  { label: '六', long: '周六', bit: 64 },
  { label: '日', long: '周日', bit: 1 },
] as const

const localTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'Asia/Shanghai'
const timezoneOptions = Array.from(new Set([localTimezone, 'Asia/Shanghai', 'Asia/Hong_Kong', 'UTC']))
const cliLabel = (cli: Cli) => cli === 'codex' ? 'Codex' : 'Claude'
const modeLabel = (mode: Schedule['mode']) => mode === 'probe' ? '计划测活' : '计划保活'
const minimumOperationFeedback = () => new Promise<void>(resolve => window.setTimeout(resolve, 400))
const minuteToTime = (minute: number) => `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`
const timeToMinute = (time: string) => {
  const [hour, minute] = time.split(':').map(Number)
  return Math.max(0, Math.min(1439, hour * 60 + minute))
}
const weekdayLabel = (mask: number) => {
  if (mask === 127) return '每天'
  if (mask === 62) return '工作日'
  if (mask === 65) return '周末'
  return WEEKDAYS.filter(day => mask & day.bit).map(day => day.long).join('、') || '未选择'
}
const formatDateTime = (iso?: string) => iso
  ? new Date(iso).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
  : '尚未运行'

const scheduleStatusMeta: Record<ScheduleLastStatus, { label: string; tone: string }> = {
  idle: { label: '等待首次运行', tone: 'muted' },
  skipped: { label: '等待目标空闲', tone: 'muted' },
  queued: { label: '已排队', tone: 'info' },
  starting: { label: '准备中', tone: 'info' },
  running: { label: '运行中', tone: 'running' },
  success: { label: '最近成功', tone: 'success' },
  fatal: { label: '配置错误', tone: 'danger' },
  stopped: { label: '已停止', tone: 'muted' },
  failed: { label: '最近失败', tone: 'warning' },
}
const scheduleRunning = (schedule: Schedule) => schedule.lastStatus === 'queued' || schedule.lastStatus === 'starting' || schedule.lastStatus === 'running'

const defaultSchedule = (defaults: JobOptions): ScheduleWriteRequest => ({
  name: '',
  enabled: true,
  cli: 'codex',
  providerId: '',
  providerGroupId: '',
  mode: 'probe',
  timezone: localTimezone,
  weekdaysMask: 62,
  startMinute: 9 * 60,
  endMinute: 18 * 60,
  untilSuccess: true,
  timeoutSeconds: defaults.timeoutSeconds,
  retryIntervalSeconds: defaults.retryIntervalSeconds,
  keepaliveIntervalSeconds: defaults.keepaliveIntervalSeconds,
  failureThreshold: defaults.failureThreshold,
  model: defaults.model,
  fallbackModel: defaults.fallbackModel,
  scenarioId: defaults.scenarioId,
})

const scheduleToWrite = (schedule: Schedule): ScheduleWriteRequest => ({
  name: schedule.name,
  enabled: schedule.enabled,
  cli: schedule.cli,
  providerId: schedule.providerId,
  providerGroupId: schedule.providerGroupId,
  mode: schedule.mode,
  timezone: schedule.timezone,
  weekdaysMask: schedule.weekdaysMask,
  startMinute: schedule.startMinute,
  endMinute: schedule.endMinute,
  untilSuccess: schedule.untilSuccess,
  timeoutSeconds: schedule.timeoutSeconds,
  retryIntervalSeconds: schedule.retryIntervalSeconds,
  keepaliveIntervalSeconds: schedule.keepaliveIntervalSeconds,
  failureThreshold: schedule.failureThreshold,
  model: schedule.model,
  fallbackModel: schedule.fallbackModel,
  scenarioId: schedule.scenarioId,
})

function CliMark({ cli }: { cli: Cli }) {
  return <span className={`schedule-cli ${cli}`}>{cli === 'codex' ? <Command/> : <Bot/>}</span>
}

function ScheduleStatus({ status = 'idle' }: { status?: ScheduleLastStatus }) {
  const meta = scheduleStatusMeta[status] ?? { label: status ? `未知状态 · ${status}` : '等待首次运行', tone: 'muted' }
  return <span className={`status-pill ${meta.tone}`}><i/>{meta.label}</span>
}

export function SchedulesView({ providers, defaultOptions, refreshToken, openRequest, openJob }: { providers: Provider[]; defaultOptions: JobOptions; refreshToken: number; openRequest: (requestId: string) => void; openJob: (job: JobSummary) => void }) {
  const [schedules, setSchedules] = useState<Schedule[]>([])
  const [total, setTotal] = useState(0)
  const [limit, setLimit] = useState(200)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [cliFilter, setCliFilter] = useState<'all' | Cli>('all')
  const [stateFilter, setStateFilter] = useState<'all' | 'enabled' | 'paused'>('all')
  const [page, setPage] = useState(1)
  const [editor, setEditor] = useState<Schedule | 'new' | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Schedule | null>(null)
  const [changingId, setChangingId] = useState('')
  const [bulkAction, setBulkAction] = useState<BulkJobAction | null>(null)
  const [logTarget, setLogTarget] = useState<Schedule | null>(null)
  const [terminalLoadingId, setTerminalLoadingId] = useState('')
  const [scenarios, setScenarios] = useState<TestScenario[]>([])
  const [providerGroups, setProviderGroups] = useState<ProviderFailoverGroup[]>([])
  const requestSequence = useRef(0)
  const observedRefreshToken = useRef(refreshToken)

  const load = useCallback(async () => {
    const sequence = ++requestSequence.current
    setLoading(true)
    setError('')
    try {
      const result = await api.schedules()
      if (sequence !== requestSequence.current) return
      setSchedules(result.schedules)
      setTotal(result.total)
      setLimit(result.limit ?? 200)
      setSelected(current => new Set([...current].filter(id => result.schedules.some(schedule => schedule.id === id))))
    } catch (e) {
      if (sequence !== requestSequence.current) return
      setError(e instanceof Error ? e.message : '无法读取计划任务')
    } finally {
      if (sequence === requestSequence.current) setLoading(false)
    }
  }, [])

  useEffect(() => { void load(); return () => { requestSequence.current++ } }, [load])
  useEffect(() => { void Promise.all([api.testScenarios(), api.providerGroups()]).then(([nextScenarios, nextGroups]) => { setScenarios(nextScenarios); setProviderGroups(nextGroups) }).catch(() => { setScenarios([]); setProviderGroups([]) }) }, [])
  const refreshAfterOperation = useDelayedRefresh(load)
  useEffect(() => {
    if (observedRefreshToken.current === refreshToken) return
    observedRefreshToken.current = refreshToken
    void refreshAfterOperation()
  }, [refreshAfterOperation, refreshToken])
  useEffect(() => {
    if (!schedules.some(scheduleRunning)) return
    const refresh = () => { if (!document.hidden) void load() }
    const timer = window.setInterval(refresh, 3000)
    const visible = () => { if (!document.hidden) void load() }
    document.addEventListener('visibilitychange', visible)
    return () => { window.clearInterval(timer); document.removeEventListener('visibilitychange', visible) }
  }, [load, schedules])

  const filtered = useMemo(() => schedules.filter(schedule =>
    (cliFilter === 'all' || schedule.cli === cliFilter) &&
    (stateFilter === 'all' || (stateFilter === 'enabled' ? schedule.enabled : !schedule.enabled)),
  ), [cliFilter, schedules, stateFilter])
  const pageCount = Math.max(1, Math.ceil(filtered.length / LIST_PAGE_SIZE))
  const pageSchedules = useMemo(() => filtered.slice((page - 1) * LIST_PAGE_SIZE, page * LIST_PAGE_SIZE), [filtered, page])
  const selectablePageSchedules = useMemo(() => pageSchedules.filter(schedule => !scheduleRunning(schedule)), [pageSchedules])
  useEffect(() => { setPage(1) }, [cliFilter, stateFilter])
  useEffect(() => { setPage(current => Math.min(current, pageCount)) }, [pageCount])
  const enabledCount = schedules.filter(schedule => schedule.enabled).length
  const nextSchedule = schedules.filter(schedule => schedule.enabled && schedule.nextRunAt)
    .sort((a, b) => new Date(a.nextRunAt!).getTime() - new Date(b.nextRunAt!).getTime())[0]
  const selectedSchedules = schedules.filter(schedule => selected.has(schedule.id) && !scheduleRunning(schedule))

  const toggleSelected = (id: string) => setSelected(current => {
    const next = new Set(current)
    if (next.has(id)) next.delete(id)
    else if (next.size < 50) next.add(id)
    return next
  })
  const toggleAllVisible = () => setSelected(current => {
    const next = new Set(current)
    const allSelected = selectablePageSchedules.length > 0 && selectablePageSchedules.every(schedule => next.has(schedule.id))
    selectablePageSchedules.forEach(schedule => {
      if (allSelected) next.delete(schedule.id)
      else if (next.size < LIST_PAGE_SIZE) next.add(schedule.id)
    })
    return next
  })

  const save = async (body: ScheduleWriteRequest) => {
    if (editor && editor !== 'new') await Promise.all([api.updateSchedule(editor.id, body), minimumOperationFeedback()])
    else await Promise.all([api.createSchedule(body), minimumOperationFeedback()])
    setEditor(null)
    setMessage(editor === 'new' ? '计划任务已创建' : '计划任务已更新')
    await refreshAfterOperation()
  }
  const toggleEnabled = async (schedule: Schedule) => {
    setChangingId(schedule.id)
    setError('')
    try {
      await Promise.all([api.updateSchedule(schedule.id, { ...scheduleToWrite(schedule), enabled: !schedule.enabled }), minimumOperationFeedback()])
      setMessage(schedule.enabled ? '计划任务已暂停' : '计划任务已启用')
      await refreshAfterOperation()
    } catch (e) {
      setError(e instanceof Error ? e.message : '更新计划状态失败')
    } finally {
      setChangingId('')
    }
  }

  const stopRunning = async (schedule: Schedule) => {
    setChangingId(schedule.id); setError(''); setMessage('')
    try {
      await Promise.all([api.updateSchedule(schedule.id, { ...scheduleToWrite(schedule), enabled: false }), minimumOperationFeedback()])
      setMessage(`已停止并暂停计划规则：${schedule.name}`)
      await refreshAfterOperation()
    } catch (e) { setError(e instanceof Error ? e.message : '停止计划任务失败') }
    finally { setChangingId('') }
  }

  const remove = async () => {
    if (!deleteTarget) return
    setChangingId(deleteTarget.id)
    setError('')
    try {
      await Promise.all([api.deleteSchedule(deleteTarget.id), minimumOperationFeedback()])
      setSelected(current => { const next = new Set(current); next.delete(deleteTarget.id); return next })
      setDeleteTarget(null)
      setMessage('计划任务已删除')
      await refreshAfterOperation()
    } catch (e) {
      setError(e instanceof Error ? e.message : '删除计划任务失败')
    } finally {
      setChangingId('')
    }
  }
  const runBulk = async (action: BulkJobAction) => {
    if (!selectedSchedules.length) return
    setBulkAction(action)
    setError('')
    setMessage('')
    try {
      const [result] = await Promise.all([api.bulkJobs({
        action,
        items: selectedSchedules.map(schedule => ({
          targetId: schedule.id,
          scheduleId: schedule.id,
          cli: schedule.cli,
          providerId: schedule.providerId,
          timeoutSeconds: schedule.timeoutSeconds,
          retryIntervalSeconds: schedule.retryIntervalSeconds,
          keepaliveIntervalSeconds: schedule.keepaliveIntervalSeconds,
          failureThreshold: schedule.failureThreshold,
          model: schedule.model,
          fallbackModel: schedule.fallbackModel,
        })),
      }), minimumOperationFeedback()])
      setMessage(`批量操作完成：${result.accepted} 项已接受${result.failed ? `，${result.failed} 项失败` : ''}`)
      if (!result.failed) setSelected(new Set())
      await refreshAfterOperation()
    } catch (e) {
      setError(e instanceof Error ? e.message : '批量操作失败')
    } finally {
      setBulkAction(null)
    }
  }
  const openTerminal = async (schedule: Schedule) => {
    if (!schedule.lastJobId || terminalLoadingId) return
    setTerminalLoadingId(schedule.id)
    setError('')
    setMessage('')
    try {
      openJob(await api.getJob(schedule.lastJobId))
    } catch (e) {
      setError(e instanceof Error ? e.message : '最近任务已不可用，可等待下一次运行')
    } finally {
      setTerminalLoadingId('')
    }
  }

  return <div className={`page schedules-page ${selected.size ? 'has-bulk-bar' : ''}`}>
    <section className="page-heading schedules-heading"><div><span className="eyebrow"><CalendarClock/>自动巡检</span><h1>计划任务</h1><p>按时区与时间窗口自动发起测活或保活。规则只引用本地 Provider，不保存 Base URL、API Key、Prompt 或通知凭证。</p></div><button className="primary hero-action" onClick={() => setEditor('new')}><Plus/>新建计划</button></section>

    {error && <div className="error-banner schedule-error" role="alert"><AlertCircle/><div><strong>计划任务操作未完成</strong><span>{error}</span></div><button onClick={() => void load()}>重新读取</button></div>}
    {message && <div className="event-message schedule-message" role="status"><CheckCircle2/>{message}</div>}

    <section className="schedule-summary" aria-label="计划任务概况">
      <div><span>规则总数</span><strong>{loading ? '—' : total}</strong><small>容量上限 {limit}</small></div>
      <div><span>已启用</span><strong>{loading ? '—' : enabledCount}</strong><small>{total ? `${Math.round(enabledCount / total * 100)}% 正在调度` : '暂无规则'}</small></div>
      <div><span>下一次执行</span><strong>{loading ? '—' : nextSchedule ? formatDateTime(nextSchedule.nextRunAt) : '暂无'}</strong><small>{nextSchedule?.name || '等待启用计划'}</small></div>
      <div><span>已选择</span><strong>{selected.size}</strong><small>批量操作最多 50 项</small></div>
    </section>

    <section className="panel schedule-toolbar" aria-label="计划任务筛选">
      <div className="schedule-filter-group"><span>客户端</span><div role="group" aria-label="按客户端筛选">{(['all', 'codex', 'claude'] as const).map(value => <button key={value} className={cliFilter === value ? 'active' : ''} aria-pressed={cliFilter === value} onClick={() => setCliFilter(value)}>{value === 'all' ? '全部' : cliLabel(value)}</button>)}</div></div>
      <div className="schedule-filter-group"><span>状态</span><div role="group" aria-label="按启用状态筛选">{(['all', 'enabled', 'paused'] as const).map(value => <button key={value} className={stateFilter === value ? 'active' : ''} aria-pressed={stateFilter === value} onClick={() => setStateFilter(value)}>{value === 'all' ? '全部' : value === 'enabled' ? '已启用' : '已暂停'}</button>)}</div></div>
      <button className="secondary schedule-refresh" disabled={loading} onClick={() => void load()}><RefreshCw className={loading ? 'spinning' : ''}/>刷新</button>
    </section>

    <section className="panel schedule-list-panel">
      <header className="schedule-list-head"><label className="schedule-check"><input type="checkbox" aria-label="选择当前页可操作计划" checked={selectablePageSchedules.length > 0 && selectablePageSchedules.every(schedule => selected.has(schedule.id))} onChange={toggleAllVisible}/><i><Check/></i><span>选择当前页</span></label><span>{filtered.length} 条规则 · 第 {page}/{pageCount} 页</span></header>
      {loading ? <div className="schedule-loading"><LoaderCircle className="spinning"/><span>正在读取计划任务</span></div> : filtered.length ? <div className="schedule-list">{pageSchedules.map(schedule => { const running = scheduleRunning(schedule); const changing = changingId === schedule.id; const terminalLoading = terminalLoadingId === schedule.id; return <article className={`schedule-row ${!schedule.enabled ? 'paused' : ''} ${running ? 'running' : ''}`} data-status={running ? 'running' : schedule.lastStatus || 'idle'} key={schedule.id} aria-busy={changing || terminalLoading}>
        <label className="schedule-check row-check"><input type="checkbox" checked={selected.has(schedule.id)} disabled={running || (!selected.has(schedule.id) && selected.size >= 50)} onChange={() => toggleSelected(schedule.id)}/><i><Check/></i><span className="sr-only">选择 {schedule.name}</span></label>
        <CliMark cli={schedule.cli}/>
        <div className="schedule-identity"><div><strong>{schedule.name}</strong><ScheduleStatus status={running ? 'running' : schedule.lastStatus}/>{running && <em className="schedule-running-badge">运行中 · 已锁定</em>}</div><span>{schedule.providerName || schedule.providerId || '当前配置'} · {modeLabel(schedule.mode)}</span></div>
        <div className="schedule-window"><span><Clock3/>执行窗口</span><strong>{weekdayLabel(schedule.weekdaysMask)}</strong><small>{minuteToTime(schedule.startMinute)}–{minuteToTime(schedule.endMinute)} · {schedule.timezone}</small></div>
        <div className="schedule-next"><span>下一次 / 最近一次</span><strong>{schedule.enabled ? formatDateTime(schedule.nextRunAt) : '已暂停'}</strong><small>{formatDateTime(schedule.lastOccurrenceAt)}</small></div>
        <div className="schedule-row-actions"><div className="schedule-observe-actions"><button className="schedule-action-button schedule-terminal-button" disabled={changing || terminalLoading || !schedule.lastJobId} aria-label={`查看实时终端：${schedule.name}`} title={schedule.lastJobId ? running ? '查看当前任务的实时终端输出' : '回放最近一轮终端输出' : '尚无可回放的终端输出'} onClick={() => void openTerminal(schedule)}>{terminalLoading ? <LoaderCircle className="spinning"/> : <Terminal/>}<span>{terminalLoading ? '连接中' : running ? '实时终端' : '终端'}</span></button><button className="schedule-action-button schedule-log-button" disabled={changing} aria-label={`查看运行日志：${schedule.name}`} title="查看该计划产生的请求日志" onClick={() => setLogTarget(schedule)}><FileText/><span>请求日志</span></button></div><div className="schedule-manage-actions">{running ? <button className="schedule-state stop" disabled={changing} aria-label={`停止运行：${schedule.name}`} onClick={() => void stopRunning(schedule)}>{changing ? <LoaderCircle className="spinning"/> : <Square/>}<span>{changing ? '停止中' : '停止'}</span></button> : <button className={`schedule-state ${schedule.enabled ? 'on' : ''}`} disabled={changing} aria-pressed={schedule.enabled} aria-label={`${schedule.enabled ? '暂停' : '启用'}：${schedule.name}`} onClick={() => void toggleEnabled(schedule)}>{changing ? <LoaderCircle className="spinning"/> : schedule.enabled ? <PauseCircle/> : <Play/>}<span>{changing ? schedule.enabled ? '暂停中' : '启用中' : schedule.enabled ? '暂停' : '启用'}</span></button>}<button className="icon-button schedule-edit-button" disabled={running || changing} aria-label={`编辑：${schedule.name}`} onClick={() => setEditor(schedule)}><Edit3/></button><button className="icon-button danger-icon schedule-delete-button" disabled={running || changing} aria-label={`删除：${schedule.name}`} onClick={() => setDeleteTarget(schedule)}><Trash2/></button></div></div>
      </article>})}</div> : <div className="schedule-empty"><div><CalendarClock/></div><strong>{schedules.length ? '没有匹配的计划任务' : '还没有计划任务'}</strong><p>{schedules.length ? '调整客户端或状态筛选，查看其他规则。' : '创建第一条自动巡检规则，定时观察 Provider 可用性。'}</p>{!schedules.length && <button className="primary" onClick={() => setEditor('new')}><Plus/>新建计划</button>}</div>}
      {!loading && <ListPagination page={page} total={filtered.length} label="计划任务分页" onPageChange={setPage}/>}
    </section>

    <div className="schedule-safety"><ShieldCheck/><span><strong>规则只保存非敏感参数</strong><small>运行时从本地 Provider 解析连接信息；每个目标最多保留一个活跃任务，避免重复探测。</small></span></div>

    {selected.size > 0 && <div className="bulk-bar" role="region" aria-label="批量操作"><div><span>{selectedSchedules.length}</span><strong>已选择可运行计划</strong><small>{selected.size !== selectedSchedules.length ? '运行中的规则已自动锁定' : '批量操作会逐项返回结果'}</small></div><div className="bulk-actions"><button className="secondary" disabled={Boolean(bulkAction) || !selectedSchedules.length} onClick={() => void runBulk('probe_once')}>{bulkAction === 'probe_once' ? <LoaderCircle className="spinning"/> : <Gauge/>}一次测活</button><button className="secondary" disabled={Boolean(bulkAction) || !selectedSchedules.length} onClick={() => void runBulk('probe')}>{bulkAction === 'probe' ? <LoaderCircle className="spinning"/> : <RefreshCw/>}持续测活</button><button className="secondary" disabled={Boolean(bulkAction) || !selectedSchedules.length} onClick={() => void runBulk('keepalive_once')}>{bulkAction === 'keepalive_once' ? <LoaderCircle className="spinning"/> : <Activity/>}一次保活</button><button className="secondary" disabled={Boolean(bulkAction) || !selectedSchedules.length} onClick={() => void runBulk('keepalive')}>{bulkAction === 'keepalive' ? <LoaderCircle className="spinning"/> : <TimerReset/>}持续保活</button><button className="danger-button" disabled={Boolean(bulkAction) || !selectedSchedules.length} onClick={() => void runBulk('stop')}>{bulkAction === 'stop' ? <LoaderCircle className="spinning"/> : <Square/>}停止目标</button><button className="icon-button" aria-label="取消选择" onClick={() => setSelected(new Set())}><X/></button></div></div>}

    {editor && <ScheduleEditor key={editor === 'new' ? 'new' : editor.id} providers={providers} providerGroups={providerGroups} scenarios={scenarios} defaults={defaultOptions} schedule={editor === 'new' ? null : editor} close={() => setEditor(null)} save={save}/>}
    {deleteTarget && <DeleteScheduleConfirm schedule={deleteTarget} busy={changingId === deleteTarget.id} close={() => setDeleteTarget(null)} confirm={() => void remove()}/>} 
    {logTarget && <ScheduleLogDrawer schedule={logTarget} close={() => setLogTarget(null)} openRequest={openRequest}/>}
  </div>
}

function ScheduleLogDrawer({ schedule, close, openRequest }: { schedule: Schedule; close: () => void; openRequest: (requestId: string) => void }) {
  const [events, setEvents] = useState<OperationalEvent[]>([])
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [status, setStatus] = useState('all')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const loadVersion = useRef(0)
  const load = useCallback(async () => {
    const version = ++loadVersion.current
    setLoading(true); setError('')
    try {
      const result = await api.events({ scheduleId: schedule.id, type: 'request_end', limit: 50, offset })
      if (version !== loadVersion.current) return
      setEvents(result.events); setTotal(result.total)
    }
    catch (e) { if (version === loadVersion.current) setError(e instanceof Error ? e.message : '无法读取计划运行日志') }
    finally { if (version === loadVersion.current) setLoading(false) }
  }, [offset, schedule.id])
  useEffect(() => { void load(); return () => { loadVersion.current++ } }, [load])
  useEffect(() => {
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape') close() }
    window.addEventListener('keydown', key); return () => window.removeEventListener('keydown', key)
  }, [close])
  const records = useMemo(() => {
    return events.filter(event => event.type === 'request_end').map(event => ({ id: String(event.data?.requestId || `${event.jobId || 'job'}-${event.id}`), end: event })).filter(record => status === 'all' || String(record.end.data?.status || 'completed') === status)
  }, [events, status])
  const page = Math.floor(offset / 50) + 1
  const pages = Math.max(1, Math.ceil(total / 50))
  return <div className="overlay schedule-log-overlay"><button className="overlay-scrim" aria-label="关闭计划运行日志" onClick={close}/><aside className="drawer schedule-log-drawer" role="dialog" aria-modal="true" aria-labelledby="schedule-log-title"><div className="drawer-header"><div><span>计划请求时间线 · 最新在前</span><h2 id="schedule-log-title">{schedule.name}</h2></div><button className="icon-button" aria-label="关闭计划运行日志" onClick={close}><X/></button></div><div className="schedule-log-toolbar"><Select aria-label="按请求状态筛选" value={status} onChange={event => setStatus(event.target.value)}><option value="all">全部状态</option><option value="success">成功</option><option value="timeout">超时</option><option value="failed">失败</option><option value="start_failed">启动失败</option></Select><span>{total} 次请求</span><button className="secondary" disabled={loading} onClick={() => void load()}><RefreshCw className={loading ? 'spinning' : ''}/>刷新</button></div><div className="drawer-body schedule-log-body">{error ? <div className="error-banner" role="alert"><AlertCircle/><div><strong>日志读取失败</strong><span>{error}</span></div><button onClick={() => void load()}>重试</button></div> : loading && !events.length ? <div className="schedule-loading"><LoaderCircle className="spinning"/><span>正在读取请求时间线</span></div> : records.length ? <div className="schedule-request-timeline">{records.map(record => { const event = record.end; const data = event.data || {}; const requestStatus = String(data.status || 'completed'); return <article key={record.id} className={`schedule-request-entry status-${requestStatus}`}><header><span><i/><time>{new Date(event.at).toLocaleString('zh-CN', { hour12: false })}</time></span><em>{requestStatus}</em></header><div><strong>{String(data.cli || schedule.cli)} · {schedule.providerName || schedule.providerId || '当前配置'}</strong><small>第 {String(data.attempt || '—')} 次 · {data.durationMillis != null ? `${String(data.durationMillis)} ms` : '—'} · Job {event.jobId || '—'}</small></div><details><summary>查看供应商返回与脱敏详情<ChevronDown/></summary><pre>{String(data.responseExcerpt || data.error || data.classification || '暂无供应商返回信息')}</pre><dl><div><dt>Request ID</dt><dd>{record.id}</dd></div><div><dt>模型</dt><dd>{String(data.model || schedule.model || '跟随配置')}</dd></div><div><dt>分类</dt><dd>{String(data.classification || '—')}</dd></div><div><dt>退出码</dt><dd>{String(data.exitCode ?? '—')}</dd></div></dl><button className="secondary schedule-request-detail-link" onClick={() => { close(); openRequest(record.id) }}><ExternalLink/>打开完整请求详情</button></details></article> })}</div> : <div className="schedule-empty"><div><FileText/></div><strong>{schedule.lastOccurrenceAt ? '现存日志已被保留策略清理' : '该计划尚未运行'}</strong><p>{status !== 'all' ? '当前页没有符合状态筛选的请求记录。' : '计划运行后，脱敏供应商返回和请求结果会显示在这里。'}</p></div>}</div><div className="drawer-footer schedule-log-footer"><span>第 {page} / {pages} 页 · 日志受事件保留策略约束</span><div><button className="secondary" disabled={loading || offset === 0} onClick={() => setOffset(value => Math.max(0, value - 50))}><ChevronLeft/>上一页</button><button className="secondary" disabled={loading || offset + 50 >= total} onClick={() => setOffset(value => value + 50)}>下一页<ChevronRight/></button></div></div></aside></div>
}

function ScheduleEditor({ providers, providerGroups, scenarios, defaults, schedule, close, save }: { providers: Provider[]; providerGroups: ProviderFailoverGroup[]; scenarios: TestScenario[]; defaults: JobOptions; schedule: Schedule | null; close: () => void; save: (body: ScheduleWriteRequest) => Promise<void> }) {
  const [draft, setDraft] = useState<ScheduleWriteRequest>(() => schedule ? scheduleToWrite(schedule) : defaultSchedule(defaults))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const drawerRef = useRef<HTMLElement>(null)
  const filteredProviders = useMemo(() => providers.filter(provider => provider.cli === draft.cli && provider.enabled !== false && provider.available !== false), [draft.cli, providers])
  const selectedProvider = filteredProviders.find(provider => provider.id === draft.providerId)
  const compatibleGroups = providerGroups.filter(group => group.enabled && group.cli === draft.cli)
  const availableScenarios = scenarios.filter(item => item.enabled && (!item.cli || item.cli === draft.cli))

  const patch = <K extends keyof ScheduleWriteRequest>(key: K, value: ScheduleWriteRequest[K]) => setDraft(current => ({ ...current, [key]: value }))
  useEffect(() => {
    if (draft.providerGroupId || !filteredProviders.length || filteredProviders.some(provider => provider.id === draft.providerId)) return
    patch('providerId', filteredProviders.find(provider => provider.current)?.id ?? filteredProviders[0].id)
  }, [draft.providerGroupId, draft.providerId, filteredProviders])
  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null
    const focusable = () => Array.from(drawerRef.current?.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])') ?? [])
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

  const toggleDay = (bit: number) => patch('weekdaysMask', draft.weekdaysMask & bit ? draft.weekdaysMask & ~bit : draft.weekdaysMask | bit)
  const submit = async () => {
    if (!draft.name.trim()) { setError('请输入计划名称'); return }
    if (!draft.providerGroupId && !filteredProviders.some(provider => provider.id === draft.providerId)) { setError('请选择一个可用的本地 Provider 或 Provider Group'); return }
    if (!draft.weekdaysMask) { setError('至少选择一个执行日'); return }
    if (draft.endMinute === draft.startMinute) { setError('开始时间与结束时间不能相同'); return }
    setBusy(true); setError('')
    try { await save({ ...draft, name: draft.name.trim() }) }
    catch (e) { setError(e instanceof Error ? e.message : '保存计划任务失败') }
    finally { setBusy(false) }
  }

  return <div className="overlay"><button className="overlay-scrim" aria-label="关闭计划编辑" disabled={busy} onClick={close}/><aside ref={drawerRef} className="drawer schedule-drawer" role="dialog" aria-modal="true" aria-labelledby="schedule-editor-title">
    <div className="drawer-header"><div><span>{schedule ? '编辑自动巡检' : '创建自动巡检'}</span><h2 id="schedule-editor-title">{schedule ? schedule.name : '新建计划任务'}</h2></div><button className="icon-button" disabled={busy} onClick={close} aria-label="关闭计划编辑"><X/></button></div>
    <div className="drawer-body schedule-editor-body">
      <div className="schedule-editor-note"><ShieldCheck/><span>这里只保存 CLI、Provider ID 与非敏感运行参数。连接地址、密钥、Prompt 和 Webhook 不会写入计划规则。</span></div>
      <section className="form-section"><div className="form-section-title"><h3>规则与目标</h3><p>为计划命名，并选择固定 Provider 或可自动切换的 Provider Group</p></div><label className="field"><span>计划名称</span><input autoComplete="off" value={draft.name} onChange={event => patch('name', event.target.value)} placeholder="例如：工作日 Codex 主线路"/></label><div className="field-grid"><label className="field"><span>客户端</span><Select value={draft.cli} onChange={event => { const cli = event.target.value as Cli; setDraft(current => ({ ...current, cli, providerGroupId: '', providerId: providers.find(provider => provider.cli === cli && provider.enabled !== false && provider.available !== false && provider.current)?.id ?? providers.find(provider => provider.cli === cli && provider.enabled !== false && provider.available !== false)?.id ?? '', fallbackModel: cli === 'claude' ? current.fallbackModel : '' })) }}><option value="codex">Codex CLI</option><option value="claude">Claude Code CLI</option></Select></label><label className="field"><span>Provider Group（可选）</span><Select value={draft.providerGroupId || ''} onChange={event => { const id = event.target.value; const group = compatibleGroups.find(value => value.id === id); setDraft(current => ({ ...current, providerGroupId: id, providerId: group?.activeProviderId || group?.primaryProviderId || current.providerId })) }}><option value="">固定 Provider</option>{compatibleGroups.map(group => <option value={group.id} key={group.id}>{group.name}{group.mode === 'automatic' ? ' · 自动切换' : ' · 建议模式'}</option>)}</Select></label></div><label className="field"><span>{draft.providerGroupId ? '当前活跃 Provider' : '本地 Provider'}</span><Select disabled={Boolean(draft.providerGroupId)} value={draft.providerId} onChange={event => patch('providerId', event.target.value)}>{filteredProviders.length ? filteredProviders.map(provider => <option key={`${provider.cli}-${provider.id || 'current'}`} value={provider.id}>{provider.name}{provider.current ? '（当前）' : ''}</option>) : <option value="">未发现可用 Provider</option>}</Select></label>{draft.providerGroupId ? <p className="field-help">计划每次运行时读取组内当前活跃线路；自动模式只会切换绑定此组的计划。</p> : selectedProvider?.current && <p className="field-help">“当前配置”会在每次运行时重新读取，适合跟随本机当前 Provider。</p>}</section>
      <section className="form-section"><div className="form-section-title"><h3>执行日历</h3><p>时间按所选时区解释，调度器会计算下一次执行</p></div><div className="field"><span>执行日</span><div className="weekday-picker">{WEEKDAYS.map(day => <button key={day.bit} className={draft.weekdaysMask & day.bit ? 'active' : ''} aria-pressed={Boolean(draft.weekdaysMask & day.bit)} aria-label={day.long} onClick={() => toggleDay(day.bit)}>{day.label}</button>)}</div></div><div className="field-grid"><label className="field"><span>开始时间</span><input type="time" value={minuteToTime(draft.startMinute)} onChange={event => patch('startMinute', timeToMinute(event.target.value))}/></label><label className="field"><span>结束时间</span><input type="time" value={minuteToTime(draft.endMinute)} onChange={event => patch('endMinute', timeToMinute(event.target.value))}/></label></div><label className="field"><span>时区</span><Select value={draft.timezone} onChange={event => patch('timezone', event.target.value)}>{timezoneOptions.map(timezone => <option key={timezone} value={timezone}>{timezone}{timezone === localTimezone ? '（本机）' : ''}</option>)}</Select></label></section>
      <section className="form-section"><div className="form-section-title"><h3>运行策略</h3><p>计划只负责节奏，实际凭证仍由 Provider 在运行时提供</p></div><div className="schedule-mode-grid"><button className={draft.mode === 'probe' ? 'active' : ''} aria-pressed={draft.mode === 'probe'} onClick={() => patch('mode', 'probe')}><Gauge/><span><strong>测活</strong><small>窗口内验证直至成功</small></span></button><button className={draft.mode === 'keepalive' ? 'active' : ''} aria-pressed={draft.mode === 'keepalive'} onClick={() => patch('mode', 'keepalive')}><TimerReset/><span><strong>保活</strong><small>按固定间隔持续观测</small></span></button></div>{draft.mode === 'probe' && <label className="schedule-toggle-row"><span><strong>失败后继续尝试</strong><small>在当前时间窗口内按重试间隔继续测活</small></span><input type="checkbox" checked={draft.untilSuccess} onChange={event => patch('untilSuccess', event.target.checked)}/><i/></label>}<div className="field-grid"><NumberInput label="单次超时" value={draft.timeoutSeconds} min={5} suffix="秒" change={value => patch('timeoutSeconds', value)}/>{draft.mode === 'probe' ? <NumberInput label="重试间隔" value={draft.retryIntervalSeconds} min={1} suffix="秒" change={value => patch('retryIntervalSeconds', value)}/> : <NumberInput label="保活间隔" value={draft.keepaliveIntervalSeconds} min={10} suffix="秒" change={value => patch('keepaliveIntervalSeconds', value)}/>}</div>{draft.mode === 'keepalive' && <div className="field-grid"><NumberInput label="失败转恢复测活" value={draft.failureThreshold} min={1} suffix="次" change={value => patch('failureThreshold', value)}/><div/></div>}</section>
      <details className="schedule-advanced"><summary>场景与模型（可选）</summary><div><label className="field"><span>测试场景</span><Select value={draft.scenarioId || ''} onChange={event => patch('scenarioId', event.target.value)}><option value="">默认 READY 测试</option>{availableScenarios.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</Select></label><label className="field"><span>模型</span><input value={draft.model || ''} onChange={event => patch('model', event.target.value)} placeholder="跟随 Provider 配置"/></label>{draft.cli === 'claude' && <label className="field"><span>Fallback 模型</span><input value={draft.fallbackModel || ''} onChange={event => patch('fallbackModel', event.target.value)} placeholder="可留空"/></label>}</div></details>
      {error && <div className="form-error" role="alert"><AlertCircle/>{error}</div>}
    </div>
    <div className="drawer-footer"><button className="secondary" disabled={busy} onClick={close}>取消</button><button className="primary" disabled={busy || (!draft.providerGroupId && !filteredProviders.length)} onClick={() => void submit()}>{busy ? <LoaderCircle className="spinning"/> : <Check/>}{busy ? '保存中' : schedule ? '保存更改' : '创建计划'}</button></div>
  </aside></div>
}

function NumberInput({ label, value, min, suffix, change }: { label: string; value: number; min: number; suffix: string; change: (value: number) => void }) {
  return <label className="field"><span>{label}</span><div className="number-input"><input type="number" min={min} value={value} onChange={event => change(Math.max(min, Number(event.target.value)))}/><em>{suffix}</em></div></label>
}

function DeleteScheduleConfirm({ schedule, busy, close, confirm }: { schedule: Schedule; busy: boolean; close: () => void; confirm: () => void }) {
  const dialogRef = useRef<HTMLElement>(null)
  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null
    const focusable = () => Array.from(dialogRef.current?.querySelectorAll<HTMLButtonElement>('button:not([disabled])') ?? [])
    focusable()[0]?.focus()
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) close()
      if (event.key !== 'Tab') return
      const items = focusable(); if (!items.length) return
      if (event.shiftKey && document.activeElement === items[0]) { event.preventDefault(); items.at(-1)?.focus() }
      else if (!event.shiftKey && document.activeElement === items.at(-1)) { event.preventDefault(); items[0].focus() }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => { window.removeEventListener('keydown', onKeyDown); previousFocus?.focus() }
  }, [busy, close])
  return <div className="event-confirm-overlay"><button className="event-confirm-scrim" aria-label="取消删除" disabled={busy} onClick={close}/><section ref={dialogRef} className="event-confirm" role="alertdialog" aria-modal="true" aria-labelledby="delete-schedule-title"><div className="event-confirm-icon"><Trash2/></div><h2 id="delete-schedule-title">删除“{schedule.name}”？</h2><p>此操作会永久删除计划规则，但不会删除已有任务摘要或事件记录。正在运行的目标需另行停止。</p><div><button className="secondary" disabled={busy} onClick={close}>取消</button><button className="danger-button" disabled={busy} onClick={confirm}>{busy ? <LoaderCircle className="spinning"/> : <Trash2/>}{busy ? '删除中' : '确认删除'}</button></div></section></div>
}
