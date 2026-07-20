import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { AlertCircle, Braces, Check, CheckCircle2, Clock3, ExternalLink, FlaskConical, Gauge, LoaderCircle, Pencil, Play, Plus, Regex, Save, ShieldCheck, Trash2, X } from 'lucide-react'
import { api } from './api'
import { confirmAction } from './ConfirmDialog'
import { Select } from './Select'
import type { Cli, JobSummary, Provider, ScenarioComparison, ScenarioComparisonItem, TestScenario, TestScenarioWriteRequest } from './types'

const emptyScenario: TestScenarioWriteRequest = {
  id: '', name: '', description: '', cli: '', enabled: true,
  prompt: '', assertionType: 'contains', expected: '', timeoutSeconds: 15,
}

const assertionLabel = (value: TestScenario['assertionType']) => value === 'contains' ? '包含文本' : value === 'exact' ? '完全一致' : value === 'regex' ? '正则匹配' : 'JSON Object'

export function TestScenariosView({ openRequest = (requestId: string) => { window.history.pushState({ aiWatchRequest: true }, '', `/requests/${encodeURIComponent(requestId)}`); window.dispatchEvent(new PopStateEvent('popstate')) } }: { openRequest?: (requestId: string) => void }) {
  const [values, setValues] = useState<TestScenario[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [editing, setEditing] = useState<TestScenario | 'new' | null>(null)
  const [running, setRunning] = useState<TestScenario | null>(null)
  const loadVersion = useRef(0)
  const load = useCallback(async () => {
    const version = ++loadVersion.current
    setLoading(true); setError('')
    try { const next = await api.testScenarios(); if (version !== loadVersion.current) return; setValues(next) }
    catch (cause) { if (version === loadVersion.current) setError(cause instanceof Error ? cause.message : '无法读取测试场景') }
    finally { if (version === loadVersion.current) setLoading(false) }
  }, [])
  useEffect(() => { void load(); return () => { loadVersion.current++ } }, [load])
  const saved = async (body: TestScenarioWriteRequest) => {
    const value = await api.saveTestScenario(body)
    setValues(current => [...current.filter(item => item.id !== value.id), value].sort((a, b) => Number(b.builtIn) - Number(a.builtIn) || a.name.localeCompare(b.name, 'zh-CN')))
    setEditing(null); setMessage('测试场景已保存'); window.setTimeout(() => setMessage(''), 2600)
  }
  const remove = async (value: TestScenario) => {
    await confirmAction({ title: '删除测试场景', message: `删除“${value.name}”？`, detail: '历史运行记录会保留，但不能再使用该场景创建新任务。', confirmLabel: '删除场景', tone: 'danger', action: async () => { await api.deleteTestScenario(value.id); setValues(current => current.filter(item => item.id !== value.id)); setMessage('测试场景已删除'); window.setTimeout(() => setMessage(''), 2600) } })
  }
  return <div className="page scenarios-page">
    <section className="page-heading scenarios-heading"><div><span className="eyebrow"><FlaskConical/>Synthetic checks</span><h1>用同一把尺子，比较每条线路。</h1><p>把可重复的 Prompt、超时和断言保存为非敏感测试资产，让手动任务与计划任务使用完全相同的验证标准。</p></div><button className="primary" onClick={() => setEditing('new')}><Plus/>新建场景</button></section>
    <div className="scenarios-privacy"><ShieldCheck/><span><strong>场景会持久化</strong><small>只保存合成测试内容。不要写入 API Key、个人信息或业务机密；普通任务 Prompt 仍不会入库。</small></span></div>
    {error && <div className="error-banner" role="alert"><AlertCircle/><div><strong>测试场景操作未完成</strong><span>{error}</span></div><button onClick={() => void load()}>重试</button></div>}
    {message && <div className="toast-inline success"><CheckCircle2/>{message}</div>}
    <section className="scenario-grid" aria-busy={loading}>{loading ? Array.from({ length: 4 }, (_, index) => <div className="panel scenario-card skeleton-card" key={index}/>) : values.map(value => <article className={`panel scenario-card ${value.enabled ? '' : 'disabled'}`} key={value.id}><header><span className="scenario-symbol">{value.assertionType === 'json' ? <Braces/> : value.assertionType === 'regex' ? <Regex/> : <FlaskConical/>}</span><div><strong>{value.name}</strong><small>{value.builtIn ? '内置场景' : '自定义场景'} · {value.cli ? value.cli === 'codex' ? 'Codex' : 'Claude' : '全部 CLI'}</small></div><em>{value.enabled ? '启用' : '停用'}</em></header><p>{value.description || '未填写场景说明。'}</p><dl><div><dt>断言</dt><dd>{assertionLabel(value.assertionType)}</dd></div><div><dt>超时</dt><dd>{value.timeoutSeconds || '跟随任务'} 秒</dd></div></dl><pre>{value.prompt}</pre><footer><button className="scenario-run" disabled={!value.enabled} onClick={() => setRunning(value)}><Play/>多线路对比</button><button className="secondary" onClick={() => setEditing(value)}><Pencil/>编辑</button>{!value.builtIn && <button className="scenario-delete" aria-label={`删除：${value.name}`} onClick={() => void remove(value)}><Trash2/></button>}</footer></article>)}</section>
    {!loading && !values.length && <div className="panel scenario-empty"><FlaskConical/><strong>还没有测试场景</strong><span>创建一个可重复的合成测试，用于比较 Provider。</span><button className="primary" onClick={() => setEditing('new')}><Plus/>创建第一个场景</button></div>}
    {editing && <ScenarioDialog value={editing === 'new' ? null : editing} close={() => setEditing(null)} save={saved}/>} 
    {running && <ScenarioBatchDialog scenario={running} close={() => setRunning(null)} openRequest={openRequest}/>}
  </div>
}

type ScenarioRunRow = { provider: Provider; job?: JobSummary; error?: string }
const terminalJob = (job?: JobSummary) => Boolean(job && ['success', 'fatal', 'failed', 'stopped'].includes(job.status))
const runStateLabel = (row: ScenarioRunRow) => row.error ? '启动失败' : !row.job ? '等待启动' : row.job.status === 'success' ? '通过' : row.job.status === 'running' || row.job.status === 'starting' ? '运行中' : row.job.status === 'stopped' ? '已停止' : '未通过'

function ScenarioRunDialog({ scenario, close }: { scenario: TestScenario; close: () => void }) {
  const [providers, setProviders] = useState<Provider[]>([])
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [rows, setRows] = useState<ScenarioRunRow[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const rowPollVersion = useRef(0)
  useEffect(() => {
    let active = true
    void api.dashboard().then(data => {
      if (!active) return
      const compatible = data.providers.filter(provider => provider.enabled !== false && provider.available !== false && (!scenario.cli || provider.cli === scenario.cli))
      setProviders(compatible)
      setSelected(new Set(compatible.map(provider => `${provider.cli}:${provider.id}`)))
    }).catch(cause => active && setError(cause instanceof Error ? cause.message : '无法读取 Provider')).finally(() => active && setLoading(false))
    return () => { active = false }
  }, [scenario.cli])
  const selectedProviders = useMemo(() => providers.filter(provider => selected.has(`${provider.cli}:${provider.id}`)), [providers, selected])
  useEffect(() => {
    const pending = rows.filter(row => row.job && !terminalJob(row.job))
    if (!pending.length) return
    const version = ++rowPollVersion.current
    const refresh = async () => {
      if (document.hidden) return
      const updated = await Promise.all(pending.map(async row => {
        try { return { ...row, job: await api.getJob(row.job!.id) } }
        catch (cause) { return { ...row, error: cause instanceof Error ? cause.message : '读取结果失败' } }
      }))
      if (version === rowPollVersion.current) setRows(current => current.map(row => updated.find(item => item.provider.cli === row.provider.cli && item.provider.id === row.provider.id) || row))
    }
    const timer = window.setTimeout(() => void refresh(), 1000)
    const visible = () => { if (!document.hidden) { window.clearTimeout(timer); void refresh() } }
    document.addEventListener('visibilitychange', visible)
    return () => { rowPollVersion.current++; window.clearTimeout(timer); document.removeEventListener('visibilitychange', visible) }
  }, [rows])
  const toggle = (provider: Provider) => {
    const id = `${provider.cli}:${provider.id}`
    setSelected(current => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next })
  }
  const run = async () => {
    if (!selectedProviders.length) { setError('请至少选择一个 Provider'); return }
    rowPollVersion.current++; setBusy(true); setError(''); setRows([])
    try {
      const result = await api.bulkJobs({ action: 'probe_once', items: selectedProviders.map(provider => ({ targetId: `${provider.cli}:${provider.id}`, cli: provider.cli, providerId: provider.id, scenarioId: scenario.id })) })
      setRows(selectedProviders.map(provider => {
        const item = result.results.find(value => value.targetId === `${provider.cli}:${provider.id}`)
        return { provider, job: item?.job, error: item?.ok ? undefined : item?.error || '任务未被接受' }
      }))
    } catch (cause) { setError(cause instanceof Error ? cause.message : '批量测试启动失败') }
    finally { setBusy(false) }
  }
  const finished = rows.length > 0 && rows.every(row => row.error || terminalJob(row.job))
  return <div className="scenario-dialog-overlay"><button className="scenario-dialog-scrim" disabled={busy} aria-label="关闭线路对比" onClick={close}/><section className="scenario-dialog scenario-run-dialog" role="dialog" aria-modal="true" aria-labelledby="scenario-run-title"><header><div><span>多 Provider 合成测试</span><h2 id="scenario-run-title">{scenario.name}</h2></div><button className="icon-button" disabled={busy} aria-label="关闭" onClick={close}><X/></button></header><div className="scenario-dialog-body"><div className="scenario-run-intro"><Gauge/><span><strong>同一 Prompt、断言与超时</strong><small>每条线路各启动一次独立测活，结果按 Provider 并排展示。</small></span></div>{loading ? <div className="scenario-run-loading"><LoaderCircle className="spinning"/>正在读取 Provider…</div> : <div className="scenario-provider-picker">{providers.map(provider => { const id = `${provider.cli}:${provider.id}`; return <button key={id} className={selected.has(id) ? 'selected' : ''} onClick={() => toggle(provider)} aria-pressed={selected.has(id)}><span className={`scenario-provider-cli ${provider.cli}`}>{provider.cli === 'codex' ? 'CX' : 'CL'}</span><span><strong>{provider.name}</strong><small>{provider.model || '跟随 Provider 默认模型'}</small></span>{selected.has(id) && <Check/>}</button> })}{!providers.length && <div className="scenario-run-empty">没有与此场景兼容且可用的 Provider。</div>}</div>}{error && <div className="form-error" role="alert"><AlertCircle/>{error}</div>}{rows.length > 0 && <div className="scenario-compare"><header><span>{finished ? '本轮对比已完成' : '正在执行测试'}</span><strong>{rows.filter(row => row.job?.status === 'success').length} / {rows.length} 通过</strong></header>{rows.map(row => <div className={`scenario-compare-row ${row.error || (terminalJob(row.job) && row.job?.status !== 'success') ? 'failed' : row.job?.status === 'success' ? 'success' : 'running'}`} key={`${row.provider.cli}:${row.provider.id}`}><span className={`scenario-provider-cli ${row.provider.cli}`}>{row.provider.cli === 'codex' ? 'CX' : 'CL'}</span><div><strong>{row.provider.name}</strong><small>{row.error || row.job?.model || row.provider.model || '默认模型'}</small></div><div className="scenario-result-metric"><Clock3/><span>{row.job?.elapsedMs != null ? `${(row.job.elapsedMs / 1000).toFixed(1)}s` : '—'}</span></div><em>{row.job && !terminalJob(row.job) && <LoaderCircle className="spinning"/>}{runStateLabel(row)}</em></div>)}</div>}</div><footer><button className="secondary" disabled={busy} onClick={close}>关闭</button><button className="primary" disabled={busy || loading || !selectedProviders.length} onClick={() => void run()}>{busy ? <LoaderCircle className="spinning"/> : <Play/>}{rows.length ? '重新运行对比' : '运行所选线路'}</button></footer></section></div>
}

const comparisonTerminal = (status: string) => !['queued', 'starting', 'running'].includes(status)
const comparisonStateLabel = (item: ScenarioComparisonItem) => item.status === 'success' ? '通过' : item.status === 'queued' ? '已排队' : item.status === 'starting' || item.status === 'running' ? '运行中' : item.status === 'start_failed' ? '启动失败' : '未通过'

function ScenarioBatchDialog({ scenario, close, openRequest }: { scenario: TestScenario; close: () => void; openRequest: (requestId: string) => void }) {
  const [providers, setProviders] = useState<Provider[]>([])
  const [cli, setCli] = useState<Cli>(scenario.cli || 'codex')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [comparison, setComparison] = useState<ScenarioComparison | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const comparisonVersion = useRef(0)
  useEffect(() => {
    let active = true
    void api.dashboard().then(data => {
      if (!active) return
      const available = data.providers.filter(provider => provider.enabled !== false && provider.available !== false)
      setProviders(available)
      const initialCLI = scenario.cli || (available.some(provider => provider.cli === 'codex') ? 'codex' : 'claude')
      setCli(initialCLI)
      setSelected(new Set(available.filter(provider => provider.cli === initialCLI).slice(0, 10).map(provider => provider.id)))
    }).catch(cause => active && setError(cause instanceof Error ? cause.message : '无法读取 Provider')).finally(() => active && setLoading(false))
    return () => { active = false }
  }, [scenario.cli])
  const compatible = useMemo(() => providers.filter(provider => provider.cli === cli), [cli, providers])
  useEffect(() => {
    if (!comparison || comparison.status === 'completed') return
    const version = ++comparisonVersion.current
    const refresh = async () => {
      if (document.hidden) return
      try { const next = await api.scenarioComparison(comparison.id); if (version === comparisonVersion.current) setComparison(next) }
      catch (cause) { if (version === comparisonVersion.current) setError(cause instanceof Error ? cause.message : '对比结果刷新失败') }
    }
    const timer = window.setTimeout(() => void refresh(), 1000)
    const visible = () => { if (!document.hidden) { window.clearTimeout(timer); void refresh() } }
    document.addEventListener('visibilitychange', visible)
    return () => { comparisonVersion.current++; window.clearTimeout(timer); document.removeEventListener('visibilitychange', visible) }
  }, [comparison])
  const toggle = (provider: Provider) => setSelected(current => {
    const next = new Set(current)
    if (next.has(provider.id)) next.delete(provider.id)
    else if (next.size < 10) next.add(provider.id)
    return next
  })
  const changeCLI = (next: Cli) => {
    comparisonVersion.current++; setBusy(false); setCli(next); setComparison(null)
    setSelected(new Set(providers.filter(provider => provider.cli === next).slice(0, 10).map(provider => provider.id)))
  }
  const run = async () => {
    if (selected.size < 2 || selected.size > 10) { setError('请选择 2–10 个同客户端 Provider'); return }
    const version = ++comparisonVersion.current
    setBusy(true); setError(''); setComparison(null)
    try { const next = await api.createScenarioComparison({ scenarioId: scenario.id, cli, providerIds: [...selected] }); if (version === comparisonVersion.current) setComparison(next) }
    catch (cause) { if (version === comparisonVersion.current) setError(cause instanceof Error ? cause.message : '批量测试启动失败') }
    finally { if (version === comparisonVersion.current) setBusy(false) }
  }
  const rows = useMemo(() => [...(comparison?.items || [])].sort((a, b) => {
    const aRank = a.status === 'success' ? 0 : comparisonTerminal(a.status) ? 2 : 1
    const bRank = b.status === 'success' ? 0 : comparisonTerminal(b.status) ? 2 : 1
    return aRank - bRank || (a.durationMillis || Number.MAX_SAFE_INTEGER) - (b.durationMillis || Number.MAX_SAFE_INTEGER) || (a.providerName || a.providerId).localeCompare(b.providerName || b.providerId, 'zh-CN')
  }), [comparison])
  return <div className="scenario-dialog-overlay"><button className="scenario-dialog-scrim" disabled={busy} aria-label="关闭线路对比" onClick={close}/><section className="scenario-dialog scenario-run-dialog" role="dialog" aria-modal="true" aria-labelledby="scenario-batch-title"><header><div><span>多 Provider 合成测试</span><h2 id="scenario-batch-title">{scenario.name}</h2></div><button className="icon-button" disabled={busy} aria-label="关闭" onClick={close}><X/></button></header><div className="scenario-dialog-body"><div className="scenario-run-intro"><Gauge/><span><strong>服务端对比批次 · 同一 Prompt、断言与超时</strong><small>选择 2–10 条同客户端线路；刷新后仍可从结构化请求事实恢复结果。</small></span></div>{!scenario.cli && <div className="scenario-cli-switch" role="group" aria-label="选择对比客户端"><button className={cli === 'codex' ? 'active' : ''} onClick={() => changeCLI('codex')}>Codex</button><button className={cli === 'claude' ? 'active' : ''} onClick={() => changeCLI('claude')}>Claude</button></div>}{loading ? <div className="scenario-run-loading"><LoaderCircle className="spinning"/>正在读取 Provider…</div> : <><div className="scenario-selection-count"><span>已选择 <strong>{selected.size}</strong> / 10</span><small>至少选择 2 条线路</small></div><div className="scenario-provider-picker">{compatible.map(provider => <button key={provider.id} className={selected.has(provider.id) ? 'selected' : ''} disabled={!selected.has(provider.id) && selected.size >= 10} onClick={() => toggle(provider)} aria-pressed={selected.has(provider.id)}><span className={`scenario-provider-cli ${provider.cli}`}>{provider.cli === 'codex' ? 'CX' : 'CL'}</span><span><strong>{provider.name}</strong><small>{provider.model || '跟随 Provider 默认模型'}</small></span>{selected.has(provider.id) && <Check/>}</button>)}{!compatible.length && <div className="scenario-run-empty">没有与此场景兼容且可用的 Provider。</div>}</div></>}{error && <div className="form-error" role="alert"><AlertCircle/>{error}</div>}{comparison && <div className="scenario-compare"><header><span>{comparison.status === 'completed' ? '本轮对比已完成' : `批次 ${comparison.id} 正在执行`}</span><strong>{rows.filter(row => row.status === 'success').length} / {rows.length} 通过</strong></header>{rows.map(row => <div className={`scenario-compare-row ${row.status === 'success' ? 'success' : comparisonTerminal(row.status) ? 'failed' : 'running'}`} key={row.providerId}><span className={`scenario-provider-cli ${cli}`}>{cli === 'codex' ? 'CX' : 'CL'}</span><div><strong>{row.providerName || row.providerId}</strong><small>{row.errorType || row.error || row.responseExcerpt || row.jobId || '等待请求结果'}</small></div><div className="scenario-result-metric"><Clock3/><span>{row.durationMillis != null ? `${(row.durationMillis / 1000).toFixed(1)}s` : '—'}</span></div><em>{!comparisonTerminal(row.status) && <LoaderCircle className="spinning"/>}{comparisonStateLabel(row)}</em>{row.requestId && <button className="scenario-request-link" onClick={() => openRequest(row.requestId!)}><ExternalLink/>请求详情</button>}</div>)}</div>}</div><footer><button className="secondary" disabled={busy} onClick={close}>关闭</button><button className="primary" disabled={busy || loading || selected.size < 2 || selected.size > 10 || comparison?.status === 'running'} onClick={() => void run()}>{busy ? <LoaderCircle className="spinning"/> : <Play/>}{comparison ? '重新运行对比' : '运行所选线路'}</button></footer></section></div>
}

function ScenarioDialog({ value, close, save }: { value: TestScenario | null; close: () => void; save: (body: TestScenarioWriteRequest) => Promise<void> }) {
  const [draft, setDraft] = useState<TestScenarioWriteRequest>(value ? { id: value.id, name: value.name, description: value.description || '', cli: value.cli || '', enabled: value.enabled, prompt: value.prompt, assertionType: value.assertionType, expected: value.expected || '', timeoutSeconds: value.timeoutSeconds || 15 } : emptyScenario)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const patch = <K extends keyof TestScenarioWriteRequest>(key: K, next: TestScenarioWriteRequest[K]) => setDraft(current => ({ ...current, [key]: next }))
  const submit = async () => {
    if (!draft.name.trim() || !draft.prompt.trim()) { setError('请填写场景名称和 Prompt'); return }
    if (draft.assertionType !== 'json' && !draft.expected?.trim()) { setError('当前断言需要填写期望值'); return }
    setBusy(true); setError('')
    try { await save({ ...draft, id: draft.id.trim().toLowerCase(), name: draft.name.trim(), prompt: draft.prompt.trim(), expected: draft.expected?.trim() }) }
    catch (cause) { setError(cause instanceof Error ? cause.message : '保存测试场景失败') }
    finally { setBusy(false) }
  }
  return <div className="scenario-dialog-overlay"><button className="scenario-dialog-scrim" disabled={busy} aria-label="关闭场景编辑" onClick={close}/><section className="scenario-dialog" role="dialog" aria-modal="true" aria-labelledby="scenario-dialog-title"><header><div><span>{value ? '编辑合成测试' : '创建合成测试'}</span><h2 id="scenario-dialog-title">{value?.name || '新建测试场景'}</h2></div><button className="icon-button" disabled={busy} aria-label="关闭" onClick={close}><X/></button></header><div className="scenario-dialog-body"><div className="scenarios-privacy compact"><ShieldCheck/><span><strong>只使用非敏感测试内容</strong><small>场景 Prompt 会保存到 Redis，运行事件只记录场景 ID 和断言结果。</small></span></div><div className="field-grid"><label className="field"><span>场景名称 *</span><input autoFocus value={draft.name} maxLength={160} onChange={event => patch('name', event.target.value)}/></label><label className="field"><span>场景 ID *</span><input disabled={Boolean(value)} value={draft.id} placeholder="例如 json-ready" onChange={event => patch('id', event.target.value.toLowerCase())}/></label></div><div className="field-grid"><label className="field"><span>适用客户端</span><Select value={draft.cli} onChange={event => patch('cli', event.target.value as Cli | '')}><option value="">Codex 与 Claude</option><option value="codex">仅 Codex</option><option value="claude">仅 Claude</option></Select></label><label className="field"><span>单次超时</span><input type="number" min={0} max={3600} value={draft.timeoutSeconds || 0} onChange={event => patch('timeoutSeconds', Number(event.target.value))}/></label></div><label className="field"><span>说明</span><textarea rows={2} value={draft.description} maxLength={2048} onChange={event => patch('description', event.target.value)}/></label><label className="field"><span>Prompt *</span><textarea rows={6} value={draft.prompt} maxLength={16384} onChange={event => patch('prompt', event.target.value)}/></label><div className="field-grid"><label className="field"><span>断言方式</span><Select value={draft.assertionType} onChange={event => patch('assertionType', event.target.value as TestScenario['assertionType'])}><option value="contains">包含文本</option><option value="exact">完全一致</option><option value="regex">正则表达式</option><option value="json">合法 JSON Object</option></Select></label>{draft.assertionType !== 'json' && <label className="field"><span>期望值 *</span><input value={draft.expected} maxLength={4096} onChange={event => patch('expected', event.target.value)}/></label>}</div><label className="scenario-enabled"><span><strong>启用此场景</strong><small>停用后已有历史仍保留，但不能用于新任务。</small></span><input type="checkbox" checked={draft.enabled} onChange={event => patch('enabled', event.target.checked)}/><i/></label>{error && <div className="form-error" role="alert"><AlertCircle/>{error}</div>}</div><footer><button className="secondary" disabled={busy} onClick={close}>取消</button><button className="primary" disabled={busy} onClick={() => void submit()}>{busy ? <LoaderCircle className="spinning"/> : value ? <Save/> : <Check/>}{busy ? '保存中' : '保存场景'}</button></footer></section></div>
}
