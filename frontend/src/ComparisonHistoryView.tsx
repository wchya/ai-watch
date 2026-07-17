import { useCallback, useEffect, useMemo, useState } from 'react'
import { AlertCircle, BarChart3, CheckCircle2, Clock3, ExternalLink, History, LoaderCircle, RefreshCw, RotateCcw, ShieldCheck, XCircle } from 'lucide-react'
import { api } from './api'
import { confirmAction } from './ConfirmDialog'
import type { ScenarioComparison } from './types'

type Filter = '' | ScenarioComparison['status']
const statusLabel = (status: ScenarioComparison['status']) => status === 'running' ? '运行中' : status === 'completed' ? '已完成' : '含失败'
const statusTone = (status: string) => status === 'success' ? 'success' : ['queued', 'starting', 'running'].includes(status) ? 'running' : 'failed'
const dateTime = (value?: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
const detailIDFromPath = () => { const match = window.location.pathname.match(/^\/(?:validation\/)?comparisons\/([^/]+)\/?$/); if (!match) return ''; try { return decodeURIComponent(match[1]) } catch { return '' } }
const openRequest = (id: string) => { window.history.pushState({ aiWatchRequest: true }, '', `/requests/${encodeURIComponent(id)}`); window.dispatchEvent(new PopStateEvent('popstate')) }

export function ComparisonHistoryView() {
  const [items, setItems] = useState<ScenarioComparison[]>([])
  const [selectedId, setSelectedId] = useState(detailIDFromPath())
  const [selected, setSelected] = useState<ScenarioComparison | null>(null)
  const [filter, setFilter] = useState<Filter>('')
  const [loading, setLoading] = useState(true)
  const [detailLoading, setDetailLoading] = useState(false)
  const [rerunning, setRerunning] = useState(false)
  const [error, setError] = useState('')
  const [detailError, setDetailError] = useState('')
  const [retentionLimited, setRetentionLimited] = useState(false)

  const load = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true)
    try { const result = await api.scenarioComparisons(filter || undefined); setItems(result.items); setRetentionLimited(result.retentionLimited); setError(''); if (!selectedId && result.items[0]) setSelectedId(result.items[0].id) }
    catch (cause) { setError(cause instanceof Error ? cause.message : '对比历史读取失败') }
    finally { setLoading(false) }
  }, [filter, selectedId])

  const loadDetail = useCallback(async (id: string, quiet = false) => {
    if (!id) { setSelected(null); return }
    if (!quiet) setDetailLoading(true)
    try { const value = await api.scenarioComparison(id); setSelected(value); setDetailError(''); setItems(current => current.map(item => item.id === value.id ? value : item)) }
    catch (cause) { setSelected(null); setDetailError(cause instanceof Error ? `${cause.message}；该批次可能已超过事件保留期限。` : '对比历史已过期') }
    finally { setDetailLoading(false) }
  }, [])

  useEffect(() => { void load() }, [filter])
  useEffect(() => { void loadDetail(selectedId) }, [selectedId, loadDetail])
  useEffect(() => {
    const pop = () => setSelectedId(detailIDFromPath())
    window.addEventListener('popstate', pop)
    return () => window.removeEventListener('popstate', pop)
  }, [])
  useEffect(() => {
    if (!items.some(item => item.status === 'running') && selected?.status !== 'running') return
    const timer = window.setInterval(() => { if (!document.hidden) { void load(true); if (selectedId) void loadDetail(selectedId, true) } }, 2000)
    return () => window.clearInterval(timer)
  }, [items, load, loadDetail, selected?.status, selectedId])

  const choose = (id: string) => { window.history.pushState({}, '', `/validation/comparisons/${encodeURIComponent(id)}`); setSelectedId(id) }
  const rerun = async () => {
    if (!selected || rerunning) return
    await confirmAction({ title: '重新运行场景对比', message: `按原场景重新测试 ${selected.items.length} 个 Provider？`, detail: '系统会创建新的对比批次，原历史记录保持不变。', confirmLabel: '开始重跑', action: async () => { setRerunning(true); setDetailError(''); try { const next = await api.rerunScenarioComparison(selected.id); setItems(current => [next, ...current]); window.history.pushState({}, '', `/comparisons/${encodeURIComponent(next.id)}`); setSelectedId(next.id); setSelected(next) } finally { setRerunning(false) } } })
  }
  const ranked = useMemo(() => [...(selected?.items || [])].sort((a, b) => (a.status === 'success' ? 0 : 1) - (b.status === 'success' ? 0 : 1) || (a.durationMillis || Number.MAX_SAFE_INTEGER) - (b.durationMillis || Number.MAX_SAFE_INTEGER)), [selected])

  return <div className="page comparisons-page"><section className="page-heading comparisons-heading"><div><span className="eyebrow"><History/>Comparison history</span><h1>每一次线路对比，都能重新打开。</h1><p>从有界结构化事件恢复场景、Provider 排名、错误类型和 Request ID；历史过期时会明确提示。</p></div><button className="secondary" disabled={loading} onClick={() => void load()}><RefreshCw className={loading ? 'spinning' : ''}/>刷新历史</button></section>
    <div className="comparisons-safety"><ShieldCheck/><span><strong>仅保存脱敏对比事实</strong><small>不保存 Prompt、密钥、代理凭证或完整 CLI 输出。</small></span></div>
    <div className="comparison-filters">{([['', '全部'], ['running', '运行中'], ['completed', '已完成'], ['partial_failed', '含失败']] as const).map(([value, label]) => <button key={value} className={filter === value ? 'active' : ''} onClick={() => setFilter(value)}>{label}</button>)}</div>
    {retentionLimited && <div className="comparison-retention"><AlertCircle/>列表只展示事件保留范围内最近 500 个批次。</div>}{error && <div className="error-banner"><AlertCircle/><div><strong>对比历史读取失败</strong><span>{error}</span></div><button onClick={() => void load()}>重试</button></div>}
    <section className="comparisons-layout"><div className="panel comparison-history-list">{loading && !items.length ? <div className="comparison-loading"><LoaderCircle className="spinning"/>正在读取对比历史</div> : items.length ? items.map(item => <button key={item.id} className={selectedId === item.id ? 'active' : ''} onClick={() => choose(item.id)}><span className={`comparison-batch-status ${item.status}`}>{statusLabel(item.status)}</span><div><strong>{item.scenarioName}</strong><small>{item.cli === 'codex' ? 'Codex' : 'Claude'} · {item.items.length} 个 Provider</small><em>{dateTime(item.createdAt)}</em></div><b>{item.items.filter(row => row.status === 'success').length}/{item.items.length}</b></button>) : <div className="comparison-empty"><BarChart3/><strong>还没有对比历史</strong><span>从测试场景运行一次多线路对比后会显示在这里。</span></div>}</div>
      <div className="panel comparison-history-detail">{detailLoading ? <div className="comparison-loading"><LoaderCircle className="spinning"/>正在恢复批次详情</div> : selected ? <><header><div><span>{selected.id}</span><h2>{selected.scenarioName}</h2><small>{selected.cli === 'codex' ? 'Codex' : 'Claude'} · {dateTime(selected.createdAt)}</small></div><span className={`comparison-batch-status ${selected.status}`}>{statusLabel(selected.status)}</span></header><div className="comparison-detail-actions"><button className="primary" disabled={rerunning || selected.status === 'running'} onClick={() => void rerun()}>{rerunning ? <LoaderCircle className="spinning"/> : <RotateCcw/>}{rerunning ? '启动中' : '按原集合重跑'}</button></div>{detailError && <div className="form-error"><AlertCircle/>{detailError}</div>}<div className="comparison-ranking">{ranked.map((row, index) => <article className={statusTone(row.status)} key={`${row.providerId}:${row.jobId}`}><b>{index + 1}</b><span className="comparison-result-icon">{row.status === 'success' ? <CheckCircle2/> : statusTone(row.status) === 'running' ? <LoaderCircle className="spinning"/> : <XCircle/>}</span><div><strong>{row.providerName || row.providerId || '当前配置'}</strong><small>{row.errorType || row.error || row.responseExcerpt || row.jobId || '等待请求结果'}</small></div><span><Clock3/>{row.durationMillis != null ? `${(row.durationMillis / 1000).toFixed(2)}s` : '—'}</span>{row.requestId ? <button onClick={() => openRequest(row.requestId!)}><ExternalLink/>请求详情</button> : <em>{row.status}</em>}</article>)}</div></> : <div className="comparison-empty"><AlertCircle/><strong>{detailError ? '批次历史不可用' : '选择一个对比批次'}</strong><span>{detailError || '查看 Provider 排名和关联请求。'}</span></div>}</div></section>
  </div>
}
