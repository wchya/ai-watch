import { useCallback, useEffect, useMemo, useState } from 'react'
import { Activity, AlertCircle, AlertTriangle, CalendarClock, CheckCircle2, Clock3, Download, ExternalLink, Gauge, GitBranch, History, Lightbulb, LoaderCircle, Pause, RefreshCw, ShieldCheck, TrendingUp } from 'lucide-react'
import { api } from './api'
import { useDelayedRefresh } from './useDelayedRefresh'
import type { ReliabilityBucket, ReliabilityData, ReliabilityMetrics, ReliabilityProvider, ReliabilityRange } from './types'

const ranges: Array<[ReliabilityRange, string]> = [['24h', '24 小时'], ['7d', '7 天'], ['30d', '30 天']]

const percent = (value?: number) => value == null ? '—' : `${(value * 100).toFixed(value >= .995 ? 1 : 0)}%`
const duration = (value?: number) => value == null ? '样本不足' : value < 1000 ? `${Math.round(value)}ms` : `${(value / 1000).toFixed(1)}s`
const timeLabel = (value: string, range: ReliabilityRange) => new Date(value).toLocaleString('zh-CN', range === '24h' ? { hour: '2-digit', minute: '2-digit' } : { month: 'short', day: 'numeric', hour: range === '7d' ? '2-digit' : undefined })
const statusLabel = (value?: string) => value === 'success' ? '成功' : value === 'timeout' ? '超时' : value === 'overloaded' ? '过载' : value === 'unmatched' ? '未匹配' : value === 'fatal' ? '配置错误' : value === 'start_failed' ? '启动失败' : value === 'stopped' ? '已停止' : '暂无'

export function ReliabilityView() {
  const [range, setRange] = useState<ReliabilityRange>('24h')
  const [data, setData] = useState<ReliabilityData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [exportFormat, setExportFormat] = useState<'csv' | 'json'>('csv')
  const [exporting, setExporting] = useState(false)
  const [actionBusy, setActionBusy] = useState('')
  const [actionErrors, setActionErrors] = useState<Record<string, string>>({})
  const [actionMessage, setActionMessage] = useState('')

  const load = useCallback(async (nextRange = range) => {
    setLoading(true)
    try { setData(await api.reliability(nextRange)); setError('') }
    catch (cause) { setError(cause instanceof Error ? cause.message : '可靠性数据读取失败') }
    finally { setLoading(false) }
  }, [range])

  useEffect(() => { void load(range) }, [range])
  const refreshAfterOperation = useDelayedRefresh(() => load(range))
  const maxRequests = useMemo(() => Math.max(1, ...(data?.buckets.map(bucket => bucket.requests) ?? [1])), [data])
  const exportReport = async () => {
    setExporting(true); setError('')
    try { const result = await api.reliabilityExport(range, exportFormat); const url = URL.createObjectURL(result.blob); const link = document.createElement('a'); link.href = url; link.download = result.filename; link.click(); URL.revokeObjectURL(url) }
    catch (cause) { setError(cause instanceof Error ? cause.message : '可靠性报告导出失败') }
    finally { setExporting(false) }
  }
  const remediate = async (provider: ReliabilityProvider, action: 'retest' | 'validate_backup' | 'pause_schedules') => {
    const key = `${provider.key}:${action}`
    const enabledSchedules = provider.remediation?.schedules.filter(schedule => schedule.enabled) || []
    if (action === 'pause_schedules' && !window.confirm(`暂停与“${provider.name}”关联的 ${enabledSchedules.length} 个计划？`)) return
    setActionBusy(key); setActionErrors(current => ({ ...current, [provider.key]: '' })); setActionMessage('')
    try {
      await api.reliabilityAction({ cli: provider.cli, providerId: provider.providerId, action })
      setActionMessage(action === 'retest' ? `${provider.name} 复测已启动` : action === 'validate_backup' ? `${provider.name} 的备用线路验证已启动` : `已暂停 ${enabledSchedules.length} 个相关计划`)
      await refreshAfterOperation()
    } catch (cause) {
      setActionErrors(current => ({ ...current, [provider.key]: cause instanceof Error ? cause.message : '可靠性处置失败' }))
    } finally { setActionBusy('') }
  }

  return <div className="page reliability-page">
    <section className="page-heading reliability-heading"><div><span className="eyebrow"><TrendingUp/>Reliability Intelligence</span><h1>比较每条线路的真实稳定性。</h1><p>基于脱敏请求事件计算成功率、延迟与异常密度；统计范围受当前事件保留策略约束。</p></div><div className="reliability-heading-actions"><div><select aria-label="报告格式" value={exportFormat} onChange={event => setExportFormat(event.target.value as 'csv' | 'json')}><option value="csv">CSV</option><option value="json">JSON</option></select><button className="secondary" disabled={exporting || !data} onClick={() => void exportReport()}>{exporting ? <LoaderCircle className="spinning"/> : <Download/>}导出报告</button></div><button className="secondary" disabled={loading} onClick={() => void load()}>{loading ? <LoaderCircle className="spinning"/> : <RefreshCw/>}刷新数据</button></div></section>
    <div className="reliability-toolbar" role="group" aria-label="可靠性时间范围">{ranges.map(([value, label]) => <button key={value} className={range === value ? 'active' : ''} aria-pressed={range === value} onClick={() => setRange(value)}>{label}</button>)}</div>
    {error && <div className="error-banner" role="alert"><AlertCircle/><div><strong>可靠性数据刷新失败</strong><span>{error}{data ? '，已保留上一次成功结果。' : ''}</span></div><button onClick={() => void load()}>重试</button></div>}
    {actionMessage && <div className="toast-inline success" role="status"><CheckCircle2/>{actionMessage}</div>}
    {data?.coverage.partial && <div className="reliability-coverage" role="note"><History/><div><strong>当前时间窗只有部分数据</strong><span>事件保留策略为 {data.coverage.retentionDays} 天；页面不会把缺失时间表达成完整 SLA。</span></div></div>}
    {loading && !data ? <ReliabilityLoading/> : data && data.coverage.sampleCount > 0 ? <>
      <section className="metric-grid reliability-metrics">
        <ReliabilityMetric icon={<Activity/>} label="请求样本" value={String(data.overall.requests)} detail={`${data.providers.length} 个 Provider`} tone="cyan"/>
        <ReliabilityMetric icon={<CheckCircle2/>} label="整体成功率" value={percent(data.overall.successRate)} detail={`${data.overall.counts.success}/${data.overall.completed} 次完成请求`} tone="green"/>
        <ReliabilityMetric icon={<Clock3/>} label="P95 延迟" value={duration(data.overall.p95DurationMillis)} detail={`平均 ${duration(data.overall.averageDurationMillis)}`} tone="violet"/>
        <ReliabilityMetric icon={<AlertTriangle/>} label="异常线路" value={String(data.providers.filter(provider => (provider.metrics.successRate ?? 1) < .9).length)} detail={`${data.overall.counts.overloaded} 次过载 · ${data.overall.counts.timeout} 次超时`} tone="amber"/>
      </section>
      <section className="panel reliability-advice"><header><div><span>DIAGNOSTIC GUIDANCE</span><strong>线路诊断与处置建议</strong></div><small>人工确认 · 不修改宿主机配置</small></header><div className="reliability-advice-grid">{data.providers.map(provider => <RecommendationCard key={provider.key} provider={provider} busy={actionBusy} error={actionErrors[provider.key]} act={remediate}/>)}</div></section>
      <section className="panel reliability-trend"><header><div><span>REQUEST TREND</span><strong>请求量与成功率</strong></div><small>{ranges.find(([value]) => value === range)?.[1]} · {data.coverage.sampleCount} 个样本</small></header><div className="reliability-chart" aria-label="可靠性趋势图">{data.buckets.map(bucket => <TrendBar key={bucket.start} bucket={bucket} range={range} maxRequests={maxRequests}/>)}</div><footer><span><i className="success"/>成功率</span><span><i className="volume"/>请求量</span></footer></section>
      <section className="reliability-grid"><div className="panel reliability-ranking"><header><div><span>PROVIDER COMPARISON</span><strong>线路可靠性排名</strong></div><small>样本不足的线路后置</small></header><div className="reliability-provider-list">{data.providers.map((provider, index) => <ProviderRow key={provider.key} provider={provider} rank={index + 1}/>)}</div></div><div className="panel reliability-anomalies"><header><div><span>ANOMALY WINDOWS</span><strong>异常密度最高时段</strong></div></header>{data.anomalies.length ? <div>{data.anomalies.map(bucket => <div className="anomaly-row" key={bucket.start}><span><strong>{timeLabel(bucket.start, range)}</strong><small>{bucket.requests} 次请求</small></span><em>{bucket.failures} 次失败</em></div>)}</div> : <div className="reliability-clear"><ShieldCheck/><strong>没有异常时段</strong><span>当前时间窗内未记录失败请求。</span></div>}</div></section>
    </> : data ? <div className="panel reliability-empty"><Gauge/><strong>还没有可靠性样本</strong><p>运行一次测活、保活或计划任务后，这里会开始形成 Provider 成功率与延迟趋势。</p></div> : null}
  </div>
}

function ReliabilityMetric({ icon, label, value, detail, tone }: { icon: React.ReactNode; label: string; value: string; detail: string; tone: string }) { return <div className={`metric-card tone-${tone}`}><div className="metric-icon">{icon}</div><div><span>{label}</span><strong>{value}</strong><small>{detail}</small></div></div> }
function ReliabilityLoading() { return <div className="reliability-loading"><LoaderCircle className="spinning"/><strong>正在聚合可靠性数据</strong><span>读取脱敏请求结果并计算时间桶…</span></div> }

function TrendBar({ bucket, range, maxRequests }: { bucket: ReliabilityBucket; range: ReliabilityRange; maxRequests: number }) {
  const height = bucket.requests ? Math.max(8, bucket.requests / maxRequests * 100) : 2
  const success = bucket.successRate == null ? 0 : bucket.successRate * 100
  return <div className="trend-column" title={`${timeLabel(bucket.start, range)} · ${bucket.requests} 请求 · ${percent(bucket.successRate)}`}><div className="trend-bar" style={{ height: `${height}%` }}><i style={{ height: `${success}%` }}/></div><span>{range === '24h' || new Date(bucket.start).getHours() === 0 ? timeLabel(bucket.start, range) : ''}</span></div>
}

function ProviderRow({ provider, rank }: { provider: ReliabilityProvider; rank: number }) {
  const failures = provider.metrics.counts.timeout + provider.metrics.counts.overloaded + provider.metrics.counts.unmatched + provider.metrics.counts.fatal + provider.metrics.counts.startFailed
  return <article className="reliability-provider"><div className="reliability-rank">{rank}</div><div className="reliability-provider-main"><div><strong>{provider.name}</strong>{provider.historical && <em>历史配置</em>}<span>{provider.cli === 'codex' ? 'Codex' : 'Claude'} · {provider.model || '跟随配置'}</span></div><small>最近结果：{statusLabel(provider.lastStatus)}</small></div><MetricCell label="成功率" value={percent(provider.metrics.successRate)} highlight/><MetricCell label="样本" value={String(provider.metrics.completed)}/><MetricCell label="平均 / P95" value={`${duration(provider.metrics.averageDurationMillis)} / ${duration(provider.metrics.p95DurationMillis)}`}/><MetricCell label="失败" value={`${failures} · 峰值 ${provider.metrics.maxConsecutiveFailures}`}/></article>
}

function MetricCell({ label, value, highlight = false }: { label: string; value: string; highlight?: boolean }) { return <div className={`reliability-cell ${highlight ? 'highlight' : ''}`}><span>{label}</span><strong>{value}</strong></div> }

function RecommendationCard({ provider, busy, error, act }: { provider: ReliabilityProvider; busy: string; error?: string; act: (provider: ReliabilityProvider, action: 'retest' | 'validate_backup' | 'pause_schedules') => Promise<void> }) {
  const advice = provider.recommendation
  const enabledSchedules = provider.remediation?.schedules.filter(schedule => schedule.enabled) || []
  const navigateSchedules = () => { window.history.pushState({}, '', '/schedules'); window.dispatchEvent(new PopStateEvent('popstate')) }
  const button = (action: 'retest' | 'validate_backup' | 'pause_schedules') => `${provider.key}:${action}`
  return <article className={`reliability-advice-card level-${advice.level}`}><header><span><Lightbulb/><strong>{provider.name}</strong>{provider.historical && <em>历史配置</em>}</span><b>{advice.title}</b></header><ul>{advice.reasons.map(reason => <li key={reason}>{reason}</li>)}</ul><footer><i><span>建议动作</span><strong>{advice.action}</strong></i><div className="reliability-remediation-actions">{!provider.historical && <button disabled={!!busy} onClick={() => void act(provider, 'retest')}>{busy === button('retest') ? <LoaderCircle className="spinning"/> : <Activity/>}立即复测</button>}{provider.remediation?.canValidateBackup && <button disabled={!!busy} onClick={() => void act(provider, 'validate_backup')}>{busy === button('validate_backup') ? <LoaderCircle className="spinning"/> : <GitBranch/>}测试备用</button>}{provider.remediation?.schedules.length ? <button disabled={!!busy} onClick={navigateSchedules}><CalendarClock/>相关计划</button> : null}{advice.level === 'pause' && enabledSchedules.length > 0 && <button className="pause" disabled={!!busy} onClick={() => void act(provider, 'pause_schedules')}>{busy === button('pause_schedules') ? <LoaderCircle className="spinning"/> : <Pause/>}暂停 {enabledSchedules.length} 个计划</button>}{provider.lastRequestId && <a href={`/requests/${encodeURIComponent(provider.lastRequestId)}`}><ExternalLink/>最近请求</a>}</div>{error && <div className="reliability-action-error"><AlertCircle/>{error}</div>}</footer></article>
}
