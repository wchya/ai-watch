import { useCallback, useEffect, useMemo, useState } from 'react'
import { AlertCircle, ArrowLeft, CheckCircle2, Clock3, Copy, ExternalLink, LoaderCircle, RefreshCw, ShieldCheck, Terminal, XCircle } from 'lucide-react'
import { api } from './api'
import type { RequestDetail } from './types'

const statusMeta = (status: string) => status === 'success' ? { label: '请求成功', tone: 'success', icon: <CheckCircle2/> } : status === 'running' ? { label: '正在运行', tone: 'running', icon: <LoaderCircle className="spinning"/> } : status === 'timeout' ? { label: '请求超时', tone: 'warning', icon: <Clock3/> } : status === 'stopped' ? { label: '已停止', tone: 'muted', icon: <XCircle/> } : { label: status === 'start_failed' ? '启动失败' : '请求失败', tone: 'danger', icon: <AlertCircle/> }
const dateTime = (value?: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
const duration = (value?: number) => value == null ? '—' : value < 1000 ? `${value} ms` : `${(value / 1000).toFixed(2)} s`
const text = (value?: string | number) => value === undefined || value === '' ? '—' : String(value)

export function RequestDetailView({ requestId, back }: { requestId: string; back: () => void }) {
  const [detail, setDetail] = useState<RequestDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const load = useCallback(async () => {
    setLoading(true); setError('')
    try { setDetail(await api.requestDetail(requestId)) }
    catch (cause) { setError(cause instanceof Error ? cause.message : '请求详情读取失败') }
    finally { setLoading(false) }
  }, [requestId])
  useEffect(() => { void load() }, [load])
  const command = useMemo(() => detail ? `$ ${detail.cli || 'cli'}${detail.model ? ` --model ${detail.model}` : ''} [PROMPT REDACTED]` : '', [detail])
  const copy = async () => {
    if (!detail) return
    await navigator.clipboard.writeText([command, `Request ID: ${detail.requestId}`, `状态: ${detail.status}`, `Provider: ${detail.providerId || detail.provider || '当前配置'}`, `耗时: ${duration(detail.durationMillis)}`, `返回摘要: ${detail.responseExcerpt || detail.error || '—'}`, `建议: ${detail.recommendation}`].join('\n'))
    setCopied(true); window.setTimeout(() => setCopied(false), 1600)
  }
  if (loading && !detail) return <div className="page request-detail-page"><div className="request-detail-loading"><LoaderCircle className="spinning"/><strong>正在聚合请求事实</strong><span>{requestId}</span></div></div>
  if (error && !detail) return <div className="page request-detail-page"><button className="request-back" onClick={back}><ArrowLeft/>返回请求日志</button><section className="panel request-detail-missing"><AlertCircle/><h1>请求详情不可用</h1><p>{error}</p><span>该请求可能已超过事件保留期、被清理或不存在。</span><button className="secondary" onClick={() => void load()}><RefreshCw/>重新读取</button></section></div>
  if (!detail) return null
  const meta = statusMeta(detail.status)
  return <div className="page request-detail-page">
    <section className="request-detail-heading"><button className="request-back" onClick={back}><ArrowLeft/>返回</button><div><span>REQUEST FACT · 脱敏记录</span><h1>{detail.providerId || detail.provider || '当前配置'}</h1><p>Request ID · <code>{detail.requestId}</code></p></div><div className={`request-detail-status ${meta.tone}`}>{meta.icon}<span><strong>{meta.label}</strong><small>{detail.complete ? '开始与终态记录完整' : '记录仍在进行或部分数据已清理'}</small></span></div></section>
    {error && <div className="error-banner" role="alert"><AlertCircle/><div><strong>刷新失败</strong><span>{error}，已保留上一次结果。</span></div><button onClick={() => void load()}>重试</button></div>}
    <section className="request-detail-summary">
      <div><span>开始时间</span><strong>{dateTime(detail.startedAt)}</strong></div><div><span>执行耗时</span><strong>{duration(detail.durationMillis)}</strong></div><div><span>尝试 / 阶段</span><strong>第 {detail.attempt || '—'} 次 · {detail.phase || detail.mode || '—'}</strong></div><div><span>退出码 / 分类</span><strong>{text(detail.exitCode)} · {detail.classification || detail.status}</strong></div>
    </section>
    <div className="request-detail-grid">
      <section className="panel request-conclusion"><header><div><span>EXECUTION CONCLUSION</span><strong>请求结论</strong></div><button className="secondary" onClick={() => void load()} disabled={loading}><RefreshCw className={loading ? 'spinning' : ''}/>刷新</button></header><div className={`request-result-callout ${meta.tone}`}>{meta.icon}<div><strong>{detail.responseExcerpt || detail.error || meta.label}</strong><span>{detail.recommendation}</span></div></div>{detail.status !== 'success' && <dl className="request-error-grid"><div><dt>失败阶段</dt><dd>{text(detail.errorStage)}</dd></div><div><dt>错误类型</dt><dd>{text(detail.errorType)}</dd></div><div><dt>可以重试</dt><dd>{detail.retryable === true ? '是' : detail.retryable === false ? '否' : '未知'}</dd></div><div><dt>下次执行</dt><dd>{dateTime(detail.nextAttemptAt)}</dd></div></dl>}</section>
      <section className="panel request-command"><header><div><span>SAFE COMMAND</span><strong>输入命令摘要</strong></div><button className="icon-button" aria-label="复制请求摘要" onClick={() => void copy()}><Copy/></button></header><pre>{command}</pre><dl><div><dt>Prompt</dt><dd>[REDACTED] · {detail.input.promptBytes || 0} bytes</dd></div><div><dt>Prompt 指纹</dt><dd>{detail.input.promptSHA256 || '—'}</dd></div><div><dt>超时</dt><dd>{detail.input.timeoutSeconds ? `${detail.input.timeoutSeconds} 秒` : '—'}</dd></div><div><dt>执行方式</dt><dd>{detail.input.runOnce ? '单次执行' : '持续任务'}</dd></div></dl>{copied && <span className="request-copy-state">已复制脱敏摘要</span>}</section>
      <section className="panel request-context"><header><div><span>EXECUTION CONTEXT</span><strong>执行上下文</strong></div><Terminal/></header><dl><Row label="CLI / 版本" value={`${detail.cli || '—'} / ${detail.cliVersion || '—'}`}/><Row label="模型" value={detail.model}/><Row label="Provider ID" value={detail.providerId}/><Row label="任务 ID" value={detail.jobId}/><Row label="计划 ID" value={detail.scheduleId}/><Row label="触发来源" value={detail.triggerSource}/><Row label="配置来源" value={detail.configSource}/><Row label="客户端 IP" value={detail.clientIP}/></dl></section>
      <section className="panel request-network"><header><div><span>NETWORK PATH</span><strong>网络与代理</strong></div><ExternalLink/></header><dl><Row label="脱敏目标" value={detail.target}/><Row label="目标主机 / 端口" value={[detail.targetHost, detail.targetPort].filter(Boolean).join(':')}/><Row label="DNS 预解析" value={detail.dnsIPs?.join(', ')}/><Row label="DNS 错误" value={detail.dnsError}/><Row label="代理模式" value={detail.proxyMode}/><Row label="代理端点" value={detail.proxyEndpoint}/></dl></section>
    </div>
    <div className="request-safety"><ShieldCheck/><span><strong>这是脱敏结构化记录</strong><small>不包含 Prompt、API Key、Token、代理凭证、完整供应商响应或宿主路径。</small></span><button className="secondary" onClick={() => void copy()}><Copy/>{copied ? '已复制' : '复制摘要'}</button></div>
  </div>
}

function Row({ label, value }: { label: string; value?: string }) { return <div><dt>{label}</dt><dd>{value || '—'}</dd></div> }
