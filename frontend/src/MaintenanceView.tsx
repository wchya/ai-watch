import { useCallback, useEffect, useMemo, useState } from 'react'
import { AlertCircle, BellOff, CalendarClock, CheckCircle2, Clock3, LoaderCircle, Plus, RefreshCw, ShieldCheck, ShieldOff, TimerReset, X } from 'lucide-react'
import { api } from './api'
import { confirmAction } from './ConfirmDialog'
import { useDelayedRefresh } from './useDelayedRefresh'
import type { MaintenanceWindow } from './types'

type Filter = 'all' | MaintenanceWindow['status']
const statusLabel = (status: MaintenanceWindow['status']) => status === 'active' ? '进行中' : status === 'scheduled' ? '即将开始' : status === 'ended' ? '已结束' : '未设置'
const dateTime = (value?: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'

export function MaintenanceView() {
  const [items, setItems] = useState<MaintenanceWindow[]>([])
  const [filter, setFilter] = useState<Filter>('all')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [custom, setCustom] = useState<MaintenanceWindow | null>(null)
  const load = useCallback(async (quiet = false) => { if (!quiet) setLoading(true); try { setItems(await api.maintenanceWindows()); setError('') } catch (cause) { setError(cause instanceof Error ? cause.message : '维护窗口读取失败') } finally { setLoading(false) } }, [])
  useEffect(() => { void load() }, [load])
  const refreshAfter = useDelayedRefresh(() => load(true))
  const run = async (key: string, operation: () => Promise<MaintenanceWindow>, success: string) => { if (busy) return false; setBusy(key); setError(''); setMessage(''); try { await operation(); setMessage(success); await refreshAfter(); return true } catch (cause) { setError(cause instanceof Error ? cause.message : '维护窗口操作失败'); return false } finally { setBusy('') } }
  const start = (item: MaintenanceWindow, seconds: number) => { const minutes = seconds / 60; void confirmAction({ title: '开启维护窗口', message: `立即为“${item.groupName}”开启 ${minutes} 分钟维护窗口？`, detail: '窗口期间继续记录请求和事故，但暂停新通知、备用验证与自动切换。', confirmLabel: `开启 ${minutes} 分钟`, action: async () => { const until = new Date(Date.now() + seconds * 1000).toISOString(); await api.startMaintenance(item.groupId, { until }); setMessage(`${item.groupName} 维护窗口已开始`); await refreshAfter() } }) }
  const extend = (item: MaintenanceWindow, seconds: number) => { const minutes = seconds / 60; void confirmAction({ title: '延长维护窗口', message: `将“${item.groupName}”的维护窗口延长 ${minutes} 分钟？`, detail: `当前窗口结束时间将顺延 ${minutes} 分钟。`, confirmLabel: `延长 ${minutes} 分钟`, tone: 'warning', action: async () => { await api.extendMaintenance(item.groupId, seconds); setMessage(`${item.groupName} 维护窗口已延长`); await refreshAfter() } }) }
  const end = (item: MaintenanceWindow) => { void confirmAction({ title: '提前结束维护', message: `立即结束“${item.groupName}”的维护窗口？`, detail: '结束后通知、备用验证和自动切换将恢复正常。', confirmLabel: '结束维护', tone: 'danger', action: async () => { await api.endMaintenance(item.groupId); setMessage(`${item.groupName} 维护窗口已结束`); await refreshAfter() } }) }
  const visible = useMemo(() => items.filter(item => filter === 'all' || item.status === filter), [filter, items])
  const active = items.filter(item => item.status === 'active').length
  const scheduled = items.filter(item => item.status === 'scheduled').length
  return <div className="page maintenance-page"><section className="page-heading maintenance-heading"><div><span className="eyebrow"><CalendarClock/>Maintenance control</span><h1>维护期间继续记录，但暂时保持安静。</h1><p>集中管理 ProviderGroup 维护窗口；只抑制新通知、备用验证与自动切换，不停止已有任务。</p></div><button className="secondary" disabled={loading} onClick={() => void load()}><RefreshCw className={loading ? 'spinning' : ''}/>刷新</button></section>
    <section className="maintenance-summary"><div><span>进行中</span><strong>{active}</strong><small>通知和切换已抑制</small></div><div><span>即将开始</span><strong>{scheduled}</strong><small>开始前不影响现有行为</small></div><div><span>Provider Group</span><strong>{items.length}</strong><small>全部可独立设置</small></div></section>
    <section className="maintenance-behavior"><ShieldCheck/><div><strong>维护窗口安全边界</strong><span>请求、失败计数和事故时间线继续记录；不发送新事故通知、不启动备用验证、不执行自动切换；宿主机配置不会改变。</span></div></section>
    <div className="maintenance-filters">{([['all', '全部'], ['active', '进行中'], ['scheduled', '即将开始'], ['ended', '已结束'], ['none', '未设置']] as const).map(([value, label]) => <button className={filter === value ? 'active' : ''} key={value} onClick={() => setFilter(value)}>{label}</button>)}</div>
    {error && <div className="error-banner"><AlertCircle/><div><strong>维护窗口操作失败</strong><span>{error}</span></div><button onClick={() => void load()}>重试</button></div>}{message && <div className="toast-inline success" role="status"><CheckCircle2/>{message}</div>}
    <section className="maintenance-grid">{loading && !items.length ? [0,1].map(index => <div className="panel maintenance-card skeleton-card" key={index}/>) : visible.map(item => <article className={`panel maintenance-card ${item.status}`} key={item.groupId}><header><div><span className={`maintenance-status ${item.status}`}>{statusLabel(item.status)}</span><strong>{item.groupName}</strong><small>{item.cli === 'codex' ? 'Codex' : 'Claude'} · {item.mode === 'automatic' ? '自动切换组' : '建议模式'}</small></div><CalendarClock/></header><dl><div><dt>开始时间</dt><dd>{dateTime(item.maintenanceStartsAt)}</dd></div><div><dt>结束时间</dt><dd>{dateTime(item.maintenanceUntil)}</dd></div><div><dt>当前活跃 Provider</dt><dd>{item.activeProviderId || '主线路'}</dd></div></dl>{item.status === 'active' ? <div className="maintenance-effects"><span><BellOff/>事故通知已静默</span><span><ShieldOff/>故障切换已抑制</span></div> : item.status === 'scheduled' ? <div className="maintenance-effects scheduled"><span><Clock3/>窗口尚未开始，当前行为不受影响</span></div> : null}<footer>{item.status === 'active' || item.status === 'scheduled' ? <><button disabled={!!busy} onClick={() => extend(item, 1800)}>{busy === `${item.groupId}:extend` ? <LoaderCircle className="spinning"/> : <TimerReset/>}延长 30 分钟</button><button className="danger-button" disabled={!!busy} onClick={() => end(item)}>{busy === `${item.groupId}:end` ? <LoaderCircle className="spinning"/> : <X/>}提前结束</button></> : <><button disabled={!!busy} onClick={() => start(item, 1800)}>30 分钟</button><button disabled={!!busy} onClick={() => start(item, 3600)}>1 小时</button><button disabled={!!busy} onClick={() => start(item, 14400)}>4 小时</button></>}<button className="secondary" disabled={!!busy} onClick={() => setCustom(item)}><Plus/>自定义</button></footer></article>)}</section>
    {!loading && !visible.length && <div className="panel maintenance-empty"><CalendarClock/><strong>当前筛选下没有维护窗口</strong><span>调整筛选或为 ProviderGroup 创建新的维护窗口。</span></div>}
    {custom && <CustomMaintenance item={custom} busy={busy === `${custom.groupId}:custom`} close={() => setCustom(null)} submit={async (startsAt, until) => { if (await run(`${custom.groupId}:custom`, () => api.startMaintenance(custom.groupId, { startsAt, until }), `${custom.groupName} 自定义维护窗口已设置`)) setCustom(null) }}/>} 
  </div>
}

function CustomMaintenance({ item, busy, close, submit }: { item: MaintenanceWindow; busy: boolean; close: () => void; submit: (startsAt: string, until: string) => Promise<void> }) {
  const initialStart = new Date(Date.now() + 5 * 60_000)
  const [startsAt, setStartsAt] = useState(initialStart.toISOString().slice(0,16))
  const [until, setUntil] = useState(new Date(initialStart.getTime() + 60 * 60_000).toISOString().slice(0,16))
  const save = () => { const startDate = new Date(startsAt); const endDate = new Date(until); if (!startsAt || !until || endDate <= startDate) return; submit(startDate.toISOString(), endDate.toISOString()) }
  return <div className="scenario-dialog-overlay"><button className="scenario-dialog-scrim" disabled={busy} aria-label="关闭自定义维护窗口" onClick={close}/><section className="scenario-dialog maintenance-dialog" role="dialog" aria-modal="true" aria-labelledby="maintenance-dialog-title"><header><div><span>自定义维护窗口</span><h2 id="maintenance-dialog-title">{item.groupName}</h2></div><button className="icon-button" disabled={busy} onClick={close} aria-label="关闭"><X/></button></header><div className="scenario-dialog-body"><label className="field"><span>开始时间</span><input type="datetime-local" value={startsAt} onChange={event => setStartsAt(event.target.value)}/></label><label className="field"><span>结束时间</span><input type="datetime-local" value={until} onChange={event => setUntil(event.target.value)}/></label><div className="maintenance-dialog-note"><ShieldCheck/>开始前不会抑制任何行为，进入窗口后才静默通知并暂停故障切换。</div></div><footer><button className="secondary" disabled={busy} onClick={close}>取消</button><button className="primary" disabled={busy || !startsAt || !until || new Date(until) <= new Date(startsAt)} onClick={save}>{busy ? <LoaderCircle className="spinning"/> : <CalendarClock/>}{busy ? '保存中' : '设置窗口'}</button></footer></section></div>
}
