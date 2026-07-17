import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { AlertCircle, CheckCircle2, Flame, Gauge, LoaderCircle, Pause, Pencil, Play, RefreshCw, ShieldAlert, ShieldOff, Target, TrendingUp, X } from 'lucide-react'
import { api } from './api'
import { confirmAction } from './ConfirmDialog'
import { Select } from './Select'
import { useDelayedRefresh } from './useDelayedRefresh'
import type { ServiceLevelObjective } from './types'

type Filter = 'all' | ServiceLevelObjective['status']
const labels: Record<ServiceLevelObjective['status'], string> = { disabled: '已暂停', insufficient: '样本不足', healthy: '健康', burning: '快速消耗', critical: '即将耗尽', exhausted: '已耗尽' }
const fmt = (value: number, digits = 2) => Number.isFinite(value) ? value.toFixed(digits) : '—'

export function SLOView({ navigate }: { navigate: (view: 'reliability' | 'incidents' | 'maintenance') => void }) {
  const [items, setItems] = useState<ServiceLevelObjective[]>([]), [filter, setFilter] = useState<Filter>('all')
  const [loading, setLoading] = useState(true), [busy, setBusy] = useState(''), [error, setError] = useState(''), [message, setMessage] = useState('')
  const [editing, setEditing] = useState<ServiceLevelObjective | null>(null)
  const load = useCallback(async (quiet = false) => { if (!quiet) setLoading(true); try { setItems(await api.slos()); setError('') } catch (cause) { setError(cause instanceof Error ? cause.message : 'SLO 读取失败') } finally { setLoading(false) } }, [])
  useEffect(() => { void load() }, [load])
  const refreshAfter = useDelayedRefresh(() => load(true))
  const run = async (key: string, operation: () => Promise<unknown>, success: string) => { if (busy) return false; setBusy(key); setError(''); try { await operation(); setMessage(success); await refreshAfter(); return true } catch (cause) { setError(cause instanceof Error ? cause.message : 'SLO 操作失败'); return false } finally { setBusy('') } }
  const visible = useMemo(() => items.filter(item => filter === 'all' || item.status === filter), [items, filter])
  const atRisk = items.filter(item => ['burning','critical','exhausted'].includes(item.status)).length
  return <div className="page slo-page"><section className="page-heading slo-heading"><div><span className="eyebrow"><Target/>Service level objective</span><h1>看清可靠性还能消耗多久。</h1><p>用错误预算衡量 Provider Group 的可用性目标，快速识别正在加速恶化的线路。</p></div><button className="secondary" disabled={loading} onClick={() => void load()}><RefreshCw className={loading ? 'spinning' : ''}/>刷新</button></section>
    <section className="slo-summary"><div><span>风险中</span><strong>{atRisk}</strong><small>预算正在快速消耗</small></div><div><span>已启用</span><strong>{items.filter(i => i.enabled).length}</strong><small>滚动窗口持续计算</small></div><div><span>Provider Group</span><strong>{items.length}</strong><small>每组独立目标</small></div></section>
    <section className="slo-note"><ShieldOff/><div><strong>维护期间不消耗错误预算</strong><span>请求事实仍会记录；主动停止和无明确 Group 归属的历史请求不进入 SLO。</span></div></section>
    <div className="slo-filters">{([['all','全部'],['exhausted','已耗尽'],['critical','即将耗尽'],['burning','快速消耗'],['healthy','健康'],['insufficient','样本不足'],['disabled','已暂停']] as const).map(([value,label]) => <button className={filter === value ? 'active' : ''} onClick={() => setFilter(value)} key={value}>{label}</button>)}</div>
    {error && <div className="error-banner"><AlertCircle/><div><strong>SLO 操作失败</strong><span>{error}</span></div><button onClick={() => void load()}>重试</button></div>}{message && <div className="toast-inline success" role="status"><CheckCircle2/>{message}</div>}
    <section className="slo-grid">{loading && !items.length ? [0,1].map(i => <div className="panel slo-card skeleton-card" key={i}/>) : visible.map(item => <SLOCard item={item} busy={busy} edit={() => setEditing(item)} run={run} navigate={navigate} key={item.groupId}/>)}</section>
    {!loading && !visible.length && <div className="panel slo-empty"><Target/><strong>当前筛选下没有 SLO</strong><span>调整筛选或为 Provider Group 设置可用性目标。</span></div>}
    {editing && <SLODialog item={editing} busy={busy === `${editing.groupId}:save`} close={() => setEditing(null)} save={async body => { if (await run(`${editing.groupId}:save`, () => api.configureSLO(editing.groupId, body), `${editing.groupName} SLO 已保存`)) setEditing(null) }}/>} </div>
}

function SLOCard({ item, busy, edit, run, navigate }: { item: ServiceLevelObjective; busy: string; edit: () => void; run: (key:string, operation:()=>Promise<unknown>, success:string)=>Promise<boolean>; navigate:(view:'reliability'|'incidents'|'maintenance')=>void }) {
  const remaining = item.allowedFailures > 0 ? Math.max(0, Math.min(100, 100 - item.consumedPercent)) : 100
  const pause = () => { void confirmAction({ title: '暂停 SLO 计算', message: `暂停“${item.groupName}”的 SLO 计算？`, detail: '历史数据仍会保留，恢复后继续按新的时间窗口计算。', confirmLabel: '暂停计算', tone: 'warning', action: async () => { await run(`${item.groupId}:pause`, () => api.pauseSLO(item.groupId), `${item.groupName} SLO 已暂停`) } }) }
  const resume = () => void run(`${item.groupId}:resume`, () => api.resumeSLO(item.groupId), `${item.groupName} SLO 已恢复`)
  return <article className={`panel slo-card ${item.status}`}><header><div><span className={`slo-status ${item.status}`}>{labels[item.status]}</span><strong>{item.groupName}</strong><small>{item.cli === 'codex' ? 'Codex' : 'Claude'} · {item.window} 滚动窗口 · 目标 {fmt(item.targetPercent,3)}%</small></div>{item.status === 'healthy' ? <Gauge/> : <Flame/>}</header>
    <div className="slo-budget"><div className="slo-budget-label"><span>错误预算剩余</span><strong>{item.enabled ? `${fmt(remaining,1)}%` : '—'}</strong></div><div className="slo-track"><i style={{width:`${remaining}%`}}/><b style={{left:`${remaining}%`}}/></div><small>理论允许 {fmt(item.allowedFailures)} 次失败，已发生 {item.failures} 次</small></div>
    <dl><div><dt>成功率</dt><dd>{item.samples ? `${fmt(item.successRate,3)}%` : '—'}</dd></div><div><dt>燃烧速率</dt><dd>{item.samples ? `${fmt(item.burnRate)}×` : '—'}</dd></div><div><dt>有效样本</dt><dd>{item.samples} / {item.minimumSamples}</dd></div><div><dt>维护排除</dt><dd>{item.excluded}</dd></div></dl>
    <div className="slo-links"><button onClick={() => navigate('reliability')}><TrendingUp/>可靠性</button><button onClick={() => navigate('incidents')}><ShieldAlert/>事故</button><button onClick={() => navigate('maintenance')}><ShieldOff/>维护</button></div>
    <footer><button className="secondary" disabled={!!busy} onClick={edit}><Pencil/>设置目标</button>{item.enabled ? <button className="secondary" disabled={!!busy} onClick={pause}>{busy === `${item.groupId}:pause` ? <LoaderCircle className="spinning"/> : <Pause/>}暂停</button> : <button className="primary" disabled={!!busy} onClick={resume}>{busy === `${item.groupId}:resume` ? <LoaderCircle className="spinning"/> : <Play/>}恢复</button>}</footer></article>
}

function SLODialog({ item, busy, close, save }: { item:ServiceLevelObjective; busy:boolean; close:()=>void; save:(body:{targetPercent:number;window:ServiceLevelObjective['window'];minimumSamples:number})=>Promise<void> }) {
  const [targetPercent,setTarget]=useState(item.targetPercent || 99.9), [windowValue,setWindow]=useState(item.window || '7d'), [minimumSamples,setMinimum]=useState(item.minimumSamples || 20)
  return <div className="scenario-dialog-overlay"><button className="scenario-dialog-scrim" disabled={busy} aria-label="关闭 SLO 设置" onClick={close}/><section className="scenario-dialog slo-dialog" role="dialog" aria-modal="true" aria-labelledby="slo-dialog-title"><header><div><span>SLO 与错误预算</span><h2 id="slo-dialog-title">{item.groupName}</h2></div><button className="icon-button" disabled={busy} aria-label="关闭" onClick={close}><X/></button></header><div className="scenario-dialog-body"><label className="field"><span>目标成功率（%）</span><input type="number" min="90" max="99.999" step="0.001" value={targetPercent} onChange={e=>setTarget(Number(e.target.value))}/></label><label className="field"><span>滚动窗口</span><Select value={windowValue} onChange={e=>setWindow(e.target.value as ServiceLevelObjective['window'])}><option value="24h">最近 24 小时</option><option value="7d">最近 7 天</option><option value="30d">最近 30 天</option></Select></label><label className="field"><span>最小有效样本</span><input type="number" min="1" max="100000" value={minimumSamples} onChange={e=>setMinimum(Number(e.target.value))}/></label><div className="slo-dialog-note"><Target/>设置只影响观测口径，不会切换 Provider 或停止计划。</div></div><footer><button className="secondary" disabled={busy} onClick={close}>取消</button><button className="primary" disabled={busy || targetPercent < 90 || targetPercent > 99.999 || minimumSamples < 1} onClick={() => void save({targetPercent,window:windowValue,minimumSamples})}>{busy ? <LoaderCircle className="spinning"/> : <Target/>}{busy ? '保存中' : '保存目标'}</button></footer></section></div>
}
