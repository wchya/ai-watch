import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity, AlertCircle, Bot, Check, CheckCircle2, Command, Database, Eye, EyeOff,
  KeyRound, LoaderCircle, Network, Pencil, Plus, Power, RefreshCw, ShieldCheck, Trash2, X,
} from 'lucide-react'
import { api } from './api'
import type { Cli, ManualProvider, ManualProviderWrite, Provider, ProxyMode } from './types'

const emptyDraft = (cli: Cli): ManualProviderWrite => ({
  id: '', name: '', cli, baseUrl: '', model: '', provider: '', apiKey: '', clearApiKey: false,
  proxyMode: 'default', proxyUrl: '', clearProxyUrl: false, enabled: true,
})

const providerForProbe = (value: ManualProvider): Provider => ({
  id: `manual:${value.id}`, name: value.name, cli: value.cli, baseUrl: value.baseUrl,
  model: value.model, maskedApiKey: value.maskedKey, source: 'manual', available: value.hasApiKey && value.enabled !== false && (value.proxyMode !== 'custom' || Boolean(value.hasProxyUrl)),
  enabled: value.enabled !== false, proxyMode: value.proxyMode, hasProxyUrl: value.hasProxyUrl, maskedProxyUrl: value.maskedProxyUrl,
})

export function ProviderConfigView({ discoveredProviders, onProbe, onChanged, refreshToken = 0 }: { discoveredProviders: Provider[]; onProbe: (provider: Provider) => void; onChanged: () => void; refreshToken?: number }) {
  const [providers, setProviders] = useState<ManualProvider[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [editor, setEditor] = useState<ManualProvider | 'codex' | 'claude' | null>(null)
  const [deleting, setDeleting] = useState<ManualProvider | null>(null)
  const [deletingBusy, setDeletingBusy] = useState(false)
  const [changingId, setChangingId] = useState('')
  const [changingCCSwitchId, setChangingCCSwitchId] = useState('')

  const load = useCallback(async () => {
    setLoading(true); setError('')
    try { setProviders(await api.manualProviders()) }
    catch (e) { setError(e instanceof Error ? e.message : '无法读取手填供应商') }
    finally { setLoading(false) }
  }, [])
  useEffect(() => { void load() }, [load, refreshToken])

  const remove = async () => {
    if (!deleting || deletingBusy) return
    setDeletingBusy(true)
    setError('')
    try {
      await api.deleteManualProvider(deleting.id)
      setDeleting(null); setMessage(`已删除 ${deleting.name}`); await load(); onChanged()
    } catch (e) { setError(e instanceof Error ? e.message : '删除供应商失败') }
    finally { setDeletingBusy(false) }
  }
  const toggleEnabled = async (value: ManualProvider) => {
    if (changingId) return
    setChangingId(value.id); setError(''); setMessage('')
    try {
      const updated = await api.updateManualProvider(value.id, {
        name: value.name, cli: value.cli, baseUrl: value.baseUrl, model: value.model,
        provider: value.provider, proxyMode: value.proxyMode ?? 'default', enabled: value.enabled === false,
      })
      setProviders(current => current.map(item => item.id === updated.id ? updated : item))
      setMessage(updated.enabled === false ? `${updated.name} 已停用` : `${updated.name} 已启用`)
      onChanged()
    } catch (e) { setError(e instanceof Error ? e.message : '更新供应商状态失败') }
    finally { setChangingId('') }
  }
  const updateCCSwitchProxy = async (value: Provider, next: 'default' | 'direct') => {
    if (!value.id || changingCCSwitchId) return
    if ((value.proxyMode || 'default') === next) return
    setChangingCCSwitchId(value.id); setError(''); setMessage('')
    try {
      await api.updateCCSwitchProviderProxy(value.id, value.cli, next)
      setMessage(`${value.name} 已切换为${next === 'direct' ? '直连' : '默认代理'}`)
      onChanged()
    } catch (e) { setError(e instanceof Error ? e.message : '更新 CC Switch 代理策略失败') }
    finally { setChangingCCSwitchId('') }
  }

  return <div className="page provider-config-page">
    <section className="page-heading provider-config-heading"><div><span className="eyebrow"><KeyRound/>安全连接目录</span><h1>供应商配置</h1><p>自动发现配置保持只读；手填连接的 API Key 与自定义代理 URL 使用 AES-GCM 加密后写入 Redis。</p></div><button className="primary hero-action" onClick={() => setEditor('codex')}><Plus/>新增供应商</button></section>
    {error && <div className="error-banner" role="alert"><AlertCircle/><div><strong>供应商操作未完成</strong><span>{error}</span></div><button onClick={() => void load()}>重新读取</button></div>}
    {message && <div className="event-message" role="status"><CheckCircle2/>{message}<button className="icon-button" aria-label="关闭提示" onClick={() => setMessage('')}><X/></button></div>}
    <section className="provider-vault-summary"><div><span>手填配置</span><strong>{loading ? '—' : providers.length}</strong><small>Redis 加密存储</small></div><div><span>Codex</span><strong>{loading ? '—' : providers.filter(item => item.cli === 'codex').length}</strong><small>OpenAI-compatible</small></div><div><span>Claude Code</span><strong>{loading ? '—' : providers.filter(item => item.cli === 'claude').length}</strong><small>Anthropic-compatible</small></div><div><span>凭证状态</span><strong>{loading ? '—' : `${providers.filter(item => item.hasApiKey).length}/${providers.length}`}</strong><small>已配置 API Key</small></div></section>
    <div className="provider-vault-actions"><div><ShieldCheck/><span><strong>密钥保持写入态</strong><small>编辑时留空会保留原密钥；只有显式清除才会删除密文。</small></span></div><button className="secondary" disabled={loading} onClick={() => void load()}><RefreshCw className={loading ? 'spinning' : ''}/>刷新</button></div>
    <section className="panel provider-discovered-readonly"><header><div><ShieldCheck/><span><strong>自动发现配置</strong><small>当前 CLI 配置保持只读；CC Switch 的代理策略由 AI Watch 独立保存，不会改写同步快照。</small></span></div><em>{discoveredProviders.length}</em></header><div>{discoveredProviders.length ? discoveredProviders.map(value => <article key={`${value.cli}-${value.id}`}><span className={`cli-icon ${value.cli}`}>{value.cli === 'codex' ? <Command/> : <Bot/>}</span><span><strong>{value.name}</strong><small>{value.source === 'current' ? '当前 CLI 配置 · 只读' : `CC Switch · ${value.proxyMode === 'direct' ? '直连' : '默认代理'}`} · {value.baseUrl || value.model || '默认连接'}</small></span>{value.source === 'cc-switch' && <label className="cc-switch-proxy-select"><Network/><span>代理策略</span><select aria-label={`${value.name} 代理策略`} value={value.proxyMode || 'default'} disabled={changingCCSwitchId === value.id} onChange={event => void updateCCSwitchProxy(value, event.target.value as 'default' | 'direct')}><option value="default">默认代理</option><option value="direct">直连</option></select>{changingCCSwitchId === value.id && <LoaderCircle className="spinning"/>}</label>}<button className="secondary compact" disabled={value.available === false} onClick={() => onProbe(value)}><Activity/>测活</button></article>) : <p>暂未发现当前 CLI 配置或已同步的 CC Switch Redis 快照。</p>}</div></section>
    <div className="provider-vault-grid">
      {(['codex', 'claude'] as Cli[]).map(cli => <ProviderVaultCategory key={cli} cli={cli} providers={providers.filter(item => item.cli === cli)} loading={loading} changingId={changingId} create={() => setEditor(cli)} edit={setEditor} probe={value => onProbe(providerForProbe(value))} toggleEnabled={value => void toggleEnabled(value)} remove={setDeleting}/>) }
    </div>
    {editor && <ManualProviderEditor key={typeof editor === 'string' ? `new-${editor}` : editor.id} provider={typeof editor === 'string' ? null : editor} initialCli={typeof editor === 'string' ? editor : editor.cli} close={() => setEditor(null)} saved={async value => { setEditor(null); setMessage(`${value.name} 已保存`); await load(); onChanged() }}/>} 
    {deleting && <DeleteManualProviderDialog provider={deleting} busy={deletingBusy} close={() => setDeleting(null)} confirm={() => void remove()}/>} 
  </div>
}

function proxyModeLabel(value: ManualProvider) {
  if (value.proxyMode === 'direct') return '直连，不使用代理'
  if (value.proxyMode === 'custom') return value.hasProxyUrl ? value.maskedProxyUrl || '自定义代理已配置' : '自定义代理未配置'
  return '默认代理 · 环境配置'
}

function ProviderVaultCategory({ cli, providers, loading, changingId, create, edit, probe, toggleEnabled, remove }: { cli: Cli; providers: ManualProvider[]; loading: boolean; changingId: string; create: () => void; edit: (value: ManualProvider) => void; probe: (value: ManualProvider) => void; toggleEnabled: (value: ManualProvider) => void; remove: (value: ManualProvider) => void }) {
  return <section className={`panel provider-vault-category ${cli}`}><header><div className="provider-vault-title"><span className={`cli-icon ${cli}`}>{cli === 'codex' ? <Command/> : <Bot/>}</span><span><strong>{cli === 'codex' ? 'Codex Providers' : 'Claude Code Providers'}</strong><small>{cli === 'codex' ? 'Responses API 与 OpenAI-compatible' : 'Anthropic API 与兼容网关'}</small></span></div><button className="secondary compact" onClick={create}><Plus/>新增</button></header>
    {loading ? <div className="provider-vault-loading"><LoaderCircle className="spinning"/>正在读取加密配置</div> : providers.length ? <div className="provider-vault-list">{providers.map(value => <article key={value.id} className={`provider-vault-card ${value.enabled === false ? 'disabled' : ''}`}><div className="provider-vault-card-main"><div className="provider-vault-card-name"><strong>{value.name}</strong><span className={`credential-state ${value.hasApiKey ? 'ready' : 'missing'}`}><i/>{value.hasApiKey ? '凭证已加密' : '缺少凭证'}</span><span className={`provider-enabled-state ${value.enabled === false ? 'off' : 'on'}`}>{value.enabled === false ? '已停用' : '已启用'}</span></div><p>{value.baseUrl}</p><dl><div><dt>Provider</dt><dd>{value.provider || (cli === 'codex' ? 'openai' : 'anthropic-compatible')}</dd></div><div><dt>模型</dt><dd>{value.model || '跟随服务端'}</dd></div><div><dt>API Key</dt><dd>{value.maskedKey || '未配置'}</dd></div><div><dt>代理</dt><dd title={proxyModeLabel(value)}><Network/>{proxyModeLabel(value)}</dd></div></dl></div><div className="provider-vault-card-actions"><button className="provider-probe" disabled={!value.hasApiKey || value.enabled === false || (value.proxyMode === 'custom' && !value.hasProxyUrl)} aria-label={`测活：${value.name}`} onClick={() => probe(value)}><Activity/>测活</button><button className={`icon-button provider-power ${value.enabled === false ? 'off' : ''}`} disabled={changingId === value.id} aria-label={`${value.enabled === false ? '启用' : '停用'} ${value.name}`} aria-pressed={value.enabled !== false} onClick={() => toggleEnabled(value)}>{changingId === value.id ? <LoaderCircle className="spinning"/> : <Power/>}</button><button className="icon-button" aria-label={`编辑 ${value.name}`} onClick={() => edit(value)}><Pencil/></button><button className="icon-button danger-icon" aria-label={`删除 ${value.name}`} onClick={() => remove(value)}><Trash2/></button></div></article>)}</div> : <div className="provider-vault-empty"><Database/><strong>还没有手填配置</strong><p>新增一个 {cli === 'codex' ? 'Codex' : 'Claude Code'} 供应商，保存后即可参与测活、保活和计划任务。</p><button className="primary" onClick={create}><Plus/>新增 {cli === 'codex' ? 'Codex' : 'Claude'} 供应商</button></div>}
  </section>
}

function ManualProviderEditor({ provider, initialCli, close, saved }: { provider: ManualProvider | null; initialCli: Cli; close: () => void; saved: (value: ManualProvider) => Promise<void> }) {
  const [draft, setDraft] = useState<ManualProviderWrite>(() => provider ? { id: provider.id, name: provider.name, cli: provider.cli, baseUrl: provider.baseUrl, model: provider.model || '', provider: provider.provider || '', apiKey: '', clearApiKey: false, proxyMode: provider.proxyMode || 'default', proxyUrl: '', clearProxyUrl: false, enabled: provider.enabled !== false } : emptyDraft(initialCli))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [showSecret, setShowSecret] = useState(false)
  const drawer = useRef<HTMLElement>(null)
  const patch = <K extends keyof ManualProviderWrite>(key: K, value: ManualProviderWrite[K]) => setDraft(current => ({ ...current, [key]: value }))

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    const focusable = () => Array.from(drawer.current?.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])') ?? [])
    focusable()[0]?.focus()
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) close()
      if (event.key !== 'Tab') return
      const items = focusable(); if (!items.length) return
      if (event.shiftKey && document.activeElement === items[0]) { event.preventDefault(); items.at(-1)?.focus() }
      else if (!event.shiftKey && document.activeElement === items.at(-1)) { event.preventDefault(); items[0].focus() }
    }
    window.addEventListener('keydown', keydown)
    return () => { window.removeEventListener('keydown', keydown); previous?.focus() }
  }, [busy, close])

  const submit = async () => {
    if (!draft.name.trim()) { setError('请输入供应商名称'); return }
    if (!draft.baseUrl.trim()) { setError('请输入 Base URL'); return }
    if (!provider && !draft.apiKey?.trim()) { setError('新建供应商必须填写 API Key'); return }
    if ((draft.proxyMode || 'default') === 'custom' && !draft.clearProxyUrl && !provider?.hasProxyUrl && !draft.proxyUrl?.trim()) { setError('自定义代理模式必须填写代理 URL'); return }
    setBusy(true); setError('')
    try {
      const body = { ...draft, id: draft.id?.trim().toLowerCase(), name: draft.name.trim(), baseUrl: draft.baseUrl.trim(), apiKey: draft.clearApiKey ? '' : draft.apiKey?.trim(), proxyUrl: draft.clearProxyUrl ? '' : draft.proxyUrl?.trim() }
      const value = provider ? await api.updateManualProvider(provider.id, body) : await api.createManualProvider(body)
      patch('apiKey', ''); await saved(value)
    } catch (e) { setError(e instanceof Error ? e.message : '保存供应商失败') }
    finally { setBusy(false) }
  }

  const proxyMode = draft.proxyMode || 'default'
  return <div className="overlay"><button className="overlay-scrim" aria-label="关闭供应商编辑" disabled={busy} onClick={() => { if (!busy) close() }}/><aside ref={drawer} className="drawer manual-provider-drawer" role="dialog" aria-modal="true" aria-labelledby="manual-provider-title"><div className="drawer-header"><div><span>加密连接配置</span><h2 id="manual-provider-title">{provider ? `编辑 ${provider.name}` : '新增手填供应商'}</h2></div><button className="icon-button" disabled={busy} onClick={close} aria-label="关闭"><X/></button></div><div className="drawer-body"><div className="secure-config-note"><ShieldCheck/><span><strong>AES-GCM 加密存储</strong><small>浏览器提交后不会再次读取 API Key 或代理 URL 明文。Redis 中只保存随机 nonce 与密文。</small></span></div><div className="form-sections"><section className="form-section"><div className="form-section-title"><h3>身份与客户端</h3><p>ID 用于计划和任务引用，保存后不可修改</p></div><div className="field-grid"><label className="field"><span>配置 ID</span><input value={draft.id || ''} disabled={Boolean(provider)} onChange={event => patch('id', event.target.value.toLowerCase().replace(/[^a-z0-9._-]/g, ''))} placeholder="留空自动生成"/></label><label className="field"><span>客户端</span><select value={draft.cli} onChange={event => patch('cli', event.target.value as Cli)}><option value="codex">Codex CLI</option><option value="claude">Claude Code CLI</option></select></label></div><label className="field"><span>供应商名称</span><input value={draft.name} onChange={event => patch('name', event.target.value)} placeholder="例如：Ray 主线路"/></label></section><section className="form-section"><div className="form-section-title"><h3>连接信息</h3><p>Base URL 不允许内嵌凭证、查询参数或 fragment</p></div><label className="field"><span>Base URL</span><input type="url" value={draft.baseUrl} onChange={event => patch('baseUrl', event.target.value)} placeholder={draft.cli === 'codex' ? 'https://api.example.com/v1' : 'https://api.example.com'}/></label><div className="field-grid"><label className="field"><span>Provider 标识</span><input value={draft.provider || ''} onChange={event => patch('provider', event.target.value)} placeholder={draft.cli === 'codex' ? 'custom' : 'anthropic-compatible'}/></label><label className="field"><span>模型（可选）</span><input value={draft.model || ''} onChange={event => patch('model', event.target.value)} placeholder="跟随服务端配置"/></label></div><label className="field"><span>代理策略</span><select value={proxyMode} onChange={event => { const mode = event.target.value as ProxyMode; setDraft(current => ({ ...current, proxyMode: mode, clearProxyUrl: mode !== 'custom' ? false : current.clearProxyUrl })) }}><option value="default">默认代理（跟随环境配置）</option><option value="direct">直连（不使用代理）</option><option value="custom">自定义代理 URL</option></select><small className="field-help">默认代理使用容器环境配置；直连会清除本次任务的代理变量。</small></label>{proxyMode === 'custom' && <><label className="field secret-field"><span>自定义代理 URL（写入后仅显示脱敏状态）</span><div><input type="password" autoComplete="new-password" value={draft.proxyUrl || ''} onChange={event => { patch('proxyUrl', event.target.value); if (event.target.value) patch('clearProxyUrl', false) }} placeholder={provider?.hasProxyUrl ? `留空保持当前代理 ${provider.maskedProxyUrl || ''}` : '例如 http://mihomo:7890 或 socks5://mihomo:7891'}/><Network/></div></label>{provider?.hasProxyUrl && <label className="clear-secret-row"><span><strong>清除现有代理 URL</strong><small>保存后此 Provider 将不可用于测活，直到重新配置代理 URL 或改用默认/直连模式。</small></span><input type="checkbox" checked={Boolean(draft.clearProxyUrl)} onChange={event => patch('clearProxyUrl', event.target.checked)}/><i><Check/></i></label>}</>}</section><section className="form-section credential-section"><div className="form-section-title"><h3>访问凭证</h3><p>{provider?.hasApiKey ? `当前密钥：${provider.maskedKey || '已配置'}` : '保存前必须提供可用的 API Key'}</p></div><label className="field secret-field"><span>{provider ? '替换 API Key（留空保持不变）' : 'API Key'}</span><div><input type={showSecret ? 'text' : 'password'} autoComplete="new-password" value={draft.apiKey || ''} disabled={draft.clearApiKey} onChange={event => patch('apiKey', event.target.value)} placeholder={provider ? '留空保持现有密钥' : '输入 API Key'}/><button type="button" className="icon-button" disabled={draft.clearApiKey} aria-label={showSecret ? '隐藏 API Key' : '显示 API Key'} onClick={() => setShowSecret(value => !value)}>{showSecret ? <EyeOff/> : <Eye/>}</button></div></label>{provider?.hasApiKey && <label className="clear-secret-row"><span><strong>清除现有密钥</strong><small>保存后此供应商将不能启动任务，直到重新配置密钥。</small></span><input type="checkbox" checked={Boolean(draft.clearApiKey)} onChange={event => patch('clearApiKey', event.target.checked)}/><i><Check/></i></label>}<label className="toggle-row"><div><strong>启用此供应商</strong><span>停用后不会出现在测活、保活和计划任务的可用目标中。</span></div><input type="checkbox" checked={draft.enabled !== false} onChange={event => patch('enabled', event.target.checked)}/><i/></label></section></div>{error && <div className="form-error" role="alert"><AlertCircle/>{error}</div>}</div><div className="drawer-footer"><button className="secondary" disabled={busy} onClick={close}>取消</button><button className="primary" disabled={busy} onClick={() => void submit()}>{busy ? <LoaderCircle className="spinning"/> : <Check/>}{busy ? '正在加密保存' : '保存配置'}</button></div></aside></div>
}

function DeleteManualProviderDialog({ provider, busy, close, confirm }: { provider: ManualProvider; busy: boolean; close: () => void; confirm: () => void }) {
  const dialogRef = useRef<HTMLElement>(null)
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    const focusable = () => Array.from(dialogRef.current?.querySelectorAll<HTMLElement>('button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])') ?? [])
    focusable()[0]?.focus()
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) { event.preventDefault(); close(); return }
      if (event.key !== 'Tab') return
      const items = focusable(); if (!items.length) return
      if (event.shiftKey && document.activeElement === items[0]) { event.preventDefault(); items.at(-1)?.focus() }
      else if (!event.shiftKey && document.activeElement === items.at(-1)) { event.preventDefault(); items[0].focus() }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => { window.removeEventListener('keydown', onKeyDown); previous?.focus() }
  }, [busy, close])
  return <div className="event-confirm-overlay"><button className="event-confirm-scrim" aria-label="取消删除" disabled={busy} onClick={() => { if (!busy) close() }}/><section ref={dialogRef} className="event-confirm" role="alertdialog" aria-modal="true" aria-labelledby="delete-manual-provider"><div className="event-confirm-icon"><Trash2/></div><h2 id="delete-manual-provider">删除“{provider.name}”？</h2><p>密文与连接元数据会一起删除。存在运行任务或计划引用时，服务端会拒绝此操作。</p><div><button className="secondary" autoFocus disabled={busy} onClick={close}>取消</button><button className="danger-button" disabled={busy} onClick={confirm}>{busy ? <LoaderCircle className="spinning"/> : <Trash2/>}{busy ? '删除中' : '确认删除'}</button></div></section></div>
}
