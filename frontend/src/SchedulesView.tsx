import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity, AlertCircle, Bot, CalendarClock, Check, CheckCircle2, Clock3, Command, Edit3,
  Gauge, LoaderCircle, PauseCircle, Play, Plus, RefreshCw, ShieldCheck, Square,
  TimerReset, Trash2, X,
} from 'lucide-react'
import { api } from './api'
import type {
  BulkJobAction, Cli, JobOptions, Provider, Schedule, ScheduleLastStatus,
  ScheduleWriteRequest,
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
  skipped: { label: '已跳过', tone: 'muted' },
  starting: { label: '准备中', tone: 'info' },
  running: { label: '运行中', tone: 'running' },
  success: { label: '最近成功', tone: 'success' },
  fatal: { label: '配置错误', tone: 'danger' },
  stopped: { label: '已停止', tone: 'muted' },
  failed: { label: '最近失败', tone: 'warning' },
}

const defaultSchedule = (defaults: JobOptions): ScheduleWriteRequest => ({
  name: '',
  enabled: true,
  cli: 'codex',
  providerId: '',
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
})

const scheduleToWrite = (schedule: Schedule): ScheduleWriteRequest => ({
  name: schedule.name,
  enabled: schedule.enabled,
  cli: schedule.cli,
  providerId: schedule.providerId,
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
})

function CliMark({ cli }: { cli: Cli }) {
  return <span className={`schedule-cli ${cli}`}>{cli === 'codex' ? <Command/> : <Bot/>}</span>
}

function ScheduleStatus({ status = 'idle' }: { status?: ScheduleLastStatus }) {
  const meta = scheduleStatusMeta[status]
  return <span className={`status-pill ${meta.tone}`}><i/>{meta.label}</span>
}

export function SchedulesView({ providers, defaultOptions }: { providers: Provider[]; defaultOptions: JobOptions }) {
  const [schedules, setSchedules] = useState<Schedule[]>([])
  const [total, setTotal] = useState(0)
  const [limit, setLimit] = useState(200)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [cliFilter, setCliFilter] = useState<'all' | Cli>('all')
  const [stateFilter, setStateFilter] = useState<'all' | 'enabled' | 'paused'>('all')
  const [editor, setEditor] = useState<Schedule | 'new' | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Schedule | null>(null)
  const [changingId, setChangingId] = useState('')
  const [bulkAction, setBulkAction] = useState<BulkJobAction | null>(null)
  const requestSequence = useRef(0)

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

  useEffect(() => { void load() }, [load])

  const filtered = useMemo(() => schedules.filter(schedule =>
    (cliFilter === 'all' || schedule.cli === cliFilter) &&
    (stateFilter === 'all' || (stateFilter === 'enabled' ? schedule.enabled : !schedule.enabled)),
  ), [cliFilter, schedules, stateFilter])
  const enabledCount = schedules.filter(schedule => schedule.enabled).length
  const nextSchedule = schedules.filter(schedule => schedule.enabled && schedule.nextRunAt)
    .sort((a, b) => new Date(a.nextRunAt!).getTime() - new Date(b.nextRunAt!).getTime())[0]
  const selectedSchedules = schedules.filter(schedule => selected.has(schedule.id))

  const toggleSelected = (id: string) => setSelected(current => {
    const next = new Set(current)
    if (next.has(id)) next.delete(id)
    else if (next.size < 50) next.add(id)
    return next
  })
  const toggleAllVisible = () => setSelected(current => {
    const next = new Set(current)
    const visible = filtered.slice(0, 50)
    const allSelected = visible.length > 0 && visible.every(schedule => next.has(schedule.id))
    visible.forEach(schedule => allSelected ? next.delete(schedule.id) : next.add(schedule.id))
    return next
  })

  const save = async (body: ScheduleWriteRequest) => {
    if (editor && editor !== 'new') await api.updateSchedule(editor.id, body)
    else await api.createSchedule(body)
    setEditor(null)
    setMessage(editor === 'new' ? '计划任务已创建' : '计划任务已更新')
    await load()
  }
  const toggleEnabled = async (schedule: Schedule) => {
    setChangingId(schedule.id)
    setError('')
    try {
      await api.updateSchedule(schedule.id, { ...scheduleToWrite(schedule), enabled: !schedule.enabled })
      setMessage(schedule.enabled ? '计划任务已暂停' : '计划任务已启用')
      await load()
    } catch (e) {
      setError(e instanceof Error ? e.message : '更新计划状态失败')
    } finally {
      setChangingId('')
    }
  }
  const remove = async () => {
    if (!deleteTarget) return
    setChangingId(deleteTarget.id)
    setError('')
    try {
      await api.deleteSchedule(deleteTarget.id)
      setSelected(current => { const next = new Set(current); next.delete(deleteTarget.id); return next })
      setDeleteTarget(null)
      setMessage('计划任务已删除')
      await load()
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
      const result = await api.bulkJobs({
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
      })
      setMessage(`批量操作完成：${result.accepted} 项已接受${result.failed ? `，${result.failed} 项失败` : ''}`)
      if (!result.failed) setSelected(new Set())
      await load()
    } catch (e) {
      setError(e instanceof Error ? e.message : '批量操作失败')
    } finally {
      setBulkAction(null)
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
      <header className="schedule-list-head"><label className="schedule-check"><input type="checkbox" checked={filtered.length > 0 && filtered.slice(0, 50).every(schedule => selected.has(schedule.id))} onChange={toggleAllVisible}/><i><Check/></i><span>选择当前结果</span></label><span>{filtered.length} 条规则</span></header>
      {loading ? <div className="schedule-loading"><LoaderCircle className="spinning"/><span>正在读取计划任务</span></div> : filtered.length ? <div className="schedule-list">{filtered.map(schedule => <article className={`schedule-row ${!schedule.enabled ? 'paused' : ''}`} key={schedule.id}>
        <label className="schedule-check row-check"><input type="checkbox" checked={selected.has(schedule.id)} disabled={!selected.has(schedule.id) && selected.size >= 50} onChange={() => toggleSelected(schedule.id)}/><i><Check/></i><span className="sr-only">选择 {schedule.name}</span></label>
        <CliMark cli={schedule.cli}/>
        <div className="schedule-identity"><div><strong>{schedule.name}</strong><ScheduleStatus status={schedule.lastStatus}/></div><span>{schedule.providerName || schedule.providerId || '当前配置'} · {modeLabel(schedule.mode)}</span></div>
        <div className="schedule-window"><span><Clock3/>执行窗口</span><strong>{weekdayLabel(schedule.weekdaysMask)}</strong><small>{minuteToTime(schedule.startMinute)}–{minuteToTime(schedule.endMinute)} · {schedule.timezone}</small></div>
        <div className="schedule-next"><span>下一次 / 最近一次</span><strong>{schedule.enabled ? formatDateTime(schedule.nextRunAt) : '已暂停'}</strong><small>{formatDateTime(schedule.lastOccurrenceAt)}</small></div>
        <div className="schedule-row-actions"><button className={`schedule-state ${schedule.enabled ? 'on' : ''}`} disabled={changingId === schedule.id} aria-pressed={schedule.enabled} aria-label={`${schedule.enabled ? '暂停' : '启用'}：${schedule.name}`} onClick={() => void toggleEnabled(schedule)}>{changingId === schedule.id ? <LoaderCircle className="spinning"/> : schedule.enabled ? <PauseCircle/> : <Play/>}<span>{schedule.enabled ? '暂停' : '启用'}</span></button><button className="icon-button" aria-label={`编辑：${schedule.name}`} onClick={() => setEditor(schedule)}><Edit3/></button><button className="icon-button danger-icon" aria-label={`删除：${schedule.name}`} onClick={() => setDeleteTarget(schedule)}><Trash2/></button></div>
      </article>)}</div> : <div className="schedule-empty"><div><CalendarClock/></div><strong>{schedules.length ? '没有匹配的计划任务' : '还没有计划任务'}</strong><p>{schedules.length ? '调整客户端或状态筛选，查看其他规则。' : '创建第一条自动巡检规则，定时观察 Provider 可用性。'}</p>{!schedules.length && <button className="primary" onClick={() => setEditor('new')}><Plus/>新建计划</button>}</div>}
    </section>

    <div className="schedule-safety"><ShieldCheck/><span><strong>规则只保存非敏感参数</strong><small>运行时从本地 Provider 解析连接信息；每个目标最多保留一个活跃任务，避免重复探测。</small></span></div>

    {selected.size > 0 && <div className="bulk-bar" role="region" aria-label="批量操作"><div><span>{selected.size}</span><strong>已选择计划</strong><small>批量操作会逐项返回结果</small></div><div className="bulk-actions"><button className="secondary" disabled={Boolean(bulkAction)} onClick={() => void runBulk('probe_once')}>{bulkAction === 'probe_once' ? <LoaderCircle className="spinning"/> : <Gauge/>}一次测活</button><button className="secondary" disabled={Boolean(bulkAction)} onClick={() => void runBulk('probe')}>{bulkAction === 'probe' ? <LoaderCircle className="spinning"/> : <RefreshCw/>}持续测活</button><button className="secondary" disabled={Boolean(bulkAction)} onClick={() => void runBulk('keepalive_once')}>{bulkAction === 'keepalive_once' ? <LoaderCircle className="spinning"/> : <Activity/>}一次保活</button><button className="secondary" disabled={Boolean(bulkAction)} onClick={() => void runBulk('keepalive')}>{bulkAction === 'keepalive' ? <LoaderCircle className="spinning"/> : <TimerReset/>}持续保活</button><button className="danger-button" disabled={Boolean(bulkAction)} onClick={() => void runBulk('stop')}>{bulkAction === 'stop' ? <LoaderCircle className="spinning"/> : <Square/>}停止目标</button><button className="icon-button" aria-label="取消选择" onClick={() => setSelected(new Set())}><X/></button></div></div>}

    {editor && <ScheduleEditor key={editor === 'new' ? 'new' : editor.id} providers={providers} defaults={defaultOptions} schedule={editor === 'new' ? null : editor} close={() => setEditor(null)} save={save}/>} 
    {deleteTarget && <DeleteScheduleConfirm schedule={deleteTarget} busy={changingId === deleteTarget.id} close={() => setDeleteTarget(null)} confirm={() => void remove()}/>} 
  </div>
}

function ScheduleEditor({ providers, defaults, schedule, close, save }: { providers: Provider[]; defaults: JobOptions; schedule: Schedule | null; close: () => void; save: (body: ScheduleWriteRequest) => Promise<void> }) {
  const [draft, setDraft] = useState<ScheduleWriteRequest>(() => schedule ? scheduleToWrite(schedule) : defaultSchedule(defaults))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const drawerRef = useRef<HTMLElement>(null)
  const filteredProviders = useMemo(() => providers.filter(provider => provider.cli === draft.cli), [draft.cli, providers])
  const selectedProvider = filteredProviders.find(provider => provider.id === draft.providerId)

  const patch = <K extends keyof ScheduleWriteRequest>(key: K, value: ScheduleWriteRequest[K]) => setDraft(current => ({ ...current, [key]: value }))
  useEffect(() => {
    if (!filteredProviders.length || filteredProviders.some(provider => provider.id === draft.providerId)) return
    patch('providerId', filteredProviders.find(provider => provider.current)?.id ?? filteredProviders[0].id)
  }, [draft.providerId, filteredProviders])
  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null
    const focusable = () => Array.from(drawerRef.current?.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])') ?? [])
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

  const toggleDay = (bit: number) => patch('weekdaysMask', draft.weekdaysMask & bit ? draft.weekdaysMask & ~bit : draft.weekdaysMask | bit)
  const submit = async () => {
    if (!draft.name.trim()) { setError('请输入计划名称'); return }
    if (!filteredProviders.some(provider => provider.id === draft.providerId)) { setError('请选择一个可用的本地 Provider'); return }
    if (!draft.weekdaysMask) { setError('至少选择一个执行日'); return }
    if (draft.endMinute === draft.startMinute) { setError('开始时间与结束时间不能相同'); return }
    setBusy(true); setError('')
    try { await save({ ...draft, name: draft.name.trim() }) }
    catch (e) { setError(e instanceof Error ? e.message : '保存计划任务失败') }
    finally { setBusy(false) }
  }

  return <div className="overlay"><button className="overlay-scrim" aria-label="关闭计划编辑" onClick={close}/><aside ref={drawerRef} className="drawer schedule-drawer" role="dialog" aria-modal="true" aria-labelledby="schedule-editor-title">
    <div className="drawer-header"><div><span>{schedule ? '编辑自动巡检' : '创建自动巡检'}</span><h2 id="schedule-editor-title">{schedule ? schedule.name : '新建计划任务'}</h2></div><button className="icon-button" onClick={close} aria-label="关闭计划编辑"><X/></button></div>
    <div className="drawer-body schedule-editor-body">
      <div className="schedule-editor-note"><ShieldCheck/><span>这里只保存 CLI、Provider ID 与非敏感运行参数。连接地址、密钥、Prompt 和 Webhook 不会写入计划规则。</span></div>
      <section className="form-section"><div className="form-section-title"><h3>规则与目标</h3><p>为计划命名，并选择要巡检的本地配置</p></div><label className="field"><span>计划名称</span><input autoComplete="off" value={draft.name} onChange={event => patch('name', event.target.value)} placeholder="例如：工作日 Codex 主线路"/></label><div className="field-grid"><label className="field"><span>客户端</span><select value={draft.cli} onChange={event => { const cli = event.target.value as Cli; setDraft(current => ({ ...current, cli, providerId: providers.find(provider => provider.cli === cli && provider.current)?.id ?? providers.find(provider => provider.cli === cli)?.id ?? '', fallbackModel: cli === 'claude' ? current.fallbackModel : '' })) }}><option value="codex">Codex CLI</option><option value="claude">Claude Code CLI</option></select></label><label className="field"><span>本地 Provider</span><select value={draft.providerId} onChange={event => patch('providerId', event.target.value)}>{filteredProviders.length ? filteredProviders.map(provider => <option key={`${provider.cli}-${provider.id || 'current'}`} value={provider.id}>{provider.name}{provider.current ? '（当前）' : ''}</option>) : <option value="">未发现可用 Provider</option>}</select></label></div>{selectedProvider?.current && <p className="field-help">“当前配置”会在每次运行时重新读取，适合跟随本机当前 Provider。</p>}</section>
      <section className="form-section"><div className="form-section-title"><h3>执行日历</h3><p>时间按所选时区解释，调度器会计算下一次执行</p></div><div className="field"><span>执行日</span><div className="weekday-picker">{WEEKDAYS.map(day => <button key={day.bit} className={draft.weekdaysMask & day.bit ? 'active' : ''} aria-pressed={Boolean(draft.weekdaysMask & day.bit)} aria-label={day.long} onClick={() => toggleDay(day.bit)}>{day.label}</button>)}</div></div><div className="field-grid"><label className="field"><span>开始时间</span><input type="time" value={minuteToTime(draft.startMinute)} onChange={event => patch('startMinute', timeToMinute(event.target.value))}/></label><label className="field"><span>结束时间</span><input type="time" value={minuteToTime(draft.endMinute)} onChange={event => patch('endMinute', timeToMinute(event.target.value))}/></label></div><label className="field"><span>时区</span><select value={draft.timezone} onChange={event => patch('timezone', event.target.value)}>{timezoneOptions.map(timezone => <option key={timezone} value={timezone}>{timezone}{timezone === localTimezone ? '（本机）' : ''}</option>)}</select></label></section>
      <section className="form-section"><div className="form-section-title"><h3>运行策略</h3><p>计划只负责节奏，实际凭证仍由 Provider 在运行时提供</p></div><div className="schedule-mode-grid"><button className={draft.mode === 'probe' ? 'active' : ''} aria-pressed={draft.mode === 'probe'} onClick={() => patch('mode', 'probe')}><Gauge/><span><strong>测活</strong><small>窗口内验证直至成功</small></span></button><button className={draft.mode === 'keepalive' ? 'active' : ''} aria-pressed={draft.mode === 'keepalive'} onClick={() => patch('mode', 'keepalive')}><TimerReset/><span><strong>保活</strong><small>按固定间隔持续观测</small></span></button></div>{draft.mode === 'probe' && <label className="schedule-toggle-row"><span><strong>失败后继续尝试</strong><small>在当前时间窗口内按重试间隔继续测活</small></span><input type="checkbox" checked={draft.untilSuccess} onChange={event => patch('untilSuccess', event.target.checked)}/><i/></label>}<div className="field-grid"><NumberInput label="单次超时" value={draft.timeoutSeconds} min={5} suffix="秒" change={value => patch('timeoutSeconds', value)}/>{draft.mode === 'probe' ? <NumberInput label="重试间隔" value={draft.retryIntervalSeconds} min={1} suffix="秒" change={value => patch('retryIntervalSeconds', value)}/> : <NumberInput label="保活间隔" value={draft.keepaliveIntervalSeconds} min={10} suffix="秒" change={value => patch('keepaliveIntervalSeconds', value)}/>}</div>{draft.mode === 'keepalive' && <div className="field-grid"><NumberInput label="失败转恢复测活" value={draft.failureThreshold} min={1} suffix="次" change={value => patch('failureThreshold', value)}/><div/></div>}</section>
      <details className="schedule-advanced"><summary>模型覆盖（可选）</summary><div><label className="field"><span>模型</span><input value={draft.model || ''} onChange={event => patch('model', event.target.value)} placeholder="跟随 Provider 配置"/></label>{draft.cli === 'claude' && <label className="field"><span>Fallback 模型</span><input value={draft.fallbackModel || ''} onChange={event => patch('fallbackModel', event.target.value)} placeholder="可留空"/></label>}</div></details>
      {error && <div className="form-error" role="alert"><AlertCircle/>{error}</div>}
    </div>
    <div className="drawer-footer"><button className="secondary" disabled={busy} onClick={close}>取消</button><button className="primary" disabled={busy || !filteredProviders.length} onClick={() => void submit()}>{busy ? <LoaderCircle className="spinning"/> : <Check/>}{busy ? '保存中' : schedule ? '保存更改' : '创建计划'}</button></div>
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
