import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity, AlertTriangle, Braces, CheckCircle2, ChevronLeft, ChevronRight, CircleDot,
  Database, Flame, Gauge, Hash, KeyRound, List, LoaderCircle, MemoryStick, Pencil,
  Plus, RefreshCw, Save, Search, Server, Tags, Timer, Trash2, X,
} from 'lucide-react'
import { ApiError, api } from './api'
import { JSONValuePreview, parseJSONValue } from './JsonViewer'
import type {
  RedisHashEntry, RedisKeyDetail, RedisKeySummary, RedisMutationInput, RedisOverview,
  RedisPrewarmResult, RedisZSetEntry,
} from './types'

const KEY_LIMIT = 50
const VALUE_LIMIT = 50

const typeMeta: Record<string, { label: string; icon: React.ReactNode }> = {
  string: { label: 'String', icon: <Braces/> },
  hash: { label: 'Hash', icon: <Hash/> },
  list: { label: 'List', icon: <List/> },
  set: { label: 'Set', icon: <Tags/> },
  zset: { label: 'ZSet', icon: <Gauge/> },
}

const bytes = (value?: number) => {
  if (!value) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit += 1 }
  return `${size >= 10 || unit === 0 ? size.toFixed(0) : size.toFixed(1)} ${units[unit]}`
}

const ttlLabel = (key: Pick<RedisKeySummary, 'persistent' | 'ttlMillis'>) => {
  if (key.persistent) return '持久'
  if (key.ttlMillis < 0) return '已过期'
  const seconds = Math.ceil(key.ttlMillis / 1000)
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.ceil(seconds / 60)}m`
  if (seconds < 86400) return `${Math.ceil(seconds / 3600)}h`
  return `${Math.ceil(seconds / 86400)}d`
}

const uptimeLabel = (seconds: number) => {
  if (!seconds) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor(seconds % 86400 / 3600)
  return days ? `${days} 天 ${hours} 小时` : `${hours} 小时`
}

type ConfirmState = {
  title: string
  description: string
  actionLabel: string
  danger?: boolean
  run: () => Promise<void>
}

export function RedisView() {
  const [overview, setOverview] = useState<RedisOverview | null>(null)
  const [keys, setKeys] = useState<RedisKeySummary[]>([])
  const [pattern, setPattern] = useState('*')
  const [typeFilter, setTypeFilter] = useState('all')
  const [cursor, setCursor] = useState('0')
  const [nextCursor, setNextCursor] = useState('0')
  const [cursorHistory, setCursorHistory] = useState<string[]>([])
  const [selectedKey, setSelectedKey] = useState('')
  const [detail, setDetail] = useState<RedisKeyDetail | null>(null)
  const [detailCursor, setDetailCursor] = useState('0')
  const [detailHistory, setDetailHistory] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [toast, setToast] = useState('')
  const [prewarm, setPrewarm] = useState<RedisPrewarmResult | null>(null)
  const [confirm, setConfirm] = useState<ConfirmState | null>(null)
  const [confirmText, setConfirmText] = useState('')
  const keyRequestSequence = useRef(0)
  const detailRequestSequence = useRef(0)

  const loadOverview = useCallback(async (propagate = false) => {
    try { setOverview(await api.redisOverview()) }
    catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法读取 Redis 概览')
      if (propagate) throw cause
    }
  }, [])

  const loadKeys = useCallback(async (targetCursor = '0', quiet = false, propagate = false) => {
    const requestSequence = ++keyRequestSequence.current
    if (!quiet) setLoading(true)
    try {
      const result = await api.redisKeys({ pattern: pattern.trim() || '*', type: typeFilter, cursor: targetCursor, limit: KEY_LIMIT })
      if (requestSequence !== keyRequestSequence.current) return
      setKeys(result.keys)
      setCursor(result.cursor)
      setNextCursor(result.nextCursor)
      setError('')
    } catch (cause) {
      if (requestSequence !== keyRequestSequence.current) return
      setError(cause instanceof Error ? cause.message : '无法扫描 Redis Key')
      if (propagate) throw cause
    } finally { if (requestSequence === keyRequestSequence.current) setLoading(false) }
  }, [pattern, typeFilter])

  const loadDetail = useCallback(async (key: string, targetCursor = '0') => {
    if (!key) return
    const requestSequence = ++detailRequestSequence.current
    setDetailLoading(true)
    try {
      const result = await api.redisKeyDetail(key, targetCursor, VALUE_LIMIT)
      if (requestSequence !== detailRequestSequence.current) return
      setDetail(result)
      setDetailCursor(targetCursor)
      setSelectedKey(key)
      setError('')
    } catch (cause) {
      if (requestSequence !== detailRequestSequence.current) return
      const message = cause instanceof Error ? cause.message : '无法读取 Redis Key'
      setError(message)
      if (cause instanceof ApiError && cause.status === 404) {
        setSelectedKey('')
        setDetail(null)
        void loadKeys(cursor, true)
      }
    } finally { if (requestSequence === detailRequestSequence.current) setDetailLoading(false) }
  }, [cursor, loadKeys])

  useEffect(() => { void Promise.all([loadOverview(), loadKeys('0')]) }, [])
  useEffect(() => {
    keyRequestSequence.current += 1
    detailRequestSequence.current += 1
    const timer = window.setTimeout(() => {
      setCursorHistory([])
      setSelectedKey('')
      setDetail(null)
      void loadKeys('0')
    }, 320)
    return () => window.clearTimeout(timer)
  }, [pattern, typeFilter])

  const refresh = async () => {
    setRefreshing(true)
    setLoading(true)
    setError('')
    try {
      const results = await Promise.allSettled([loadOverview(true), loadKeys('0', true, true), selectedKey ? loadDetail(selectedKey, '0') : Promise.resolve()])
      const failed = results.find((result): result is PromiseRejectedResult => result.status === 'rejected')
      if (failed) throw failed.reason
      setCursorHistory([])
      showToast('Redis 数据已刷新')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Redis 刷新失败')
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }

  const showToast = (message: string) => {
    setToast(message)
    window.setTimeout(() => setToast(''), 3200)
  }

  const execute = async (work: () => Promise<void>, success: string) => {
    setBusy(true)
    try {
      await work()
      await Promise.all([loadOverview(), loadKeys(cursor, true)])
      showToast(success)
      setError('')
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : 'Redis 操作失败'
      setError(message)
      if (cause instanceof ApiError && cause.status === 409 && selectedKey) void loadDetail(selectedKey, detailCursor)
      throw cause
    } finally { setBusy(false) }
  }

  const mutate = async (input: Omit<RedisMutationInput, 'key' | 'version'>, success: string) => {
    if (!detail) return
    setBusy(true)
    try {
      const result = await api.mutateRedisKey({ ...input, key: detail.key, version: detail.version, confirmKey: input.confirmKey ?? detail.key })
      setDetail(result.key)
      showToast(result.prewarm ? `${success}，应用快照已刷新` : success)
      await Promise.all([loadOverview(), loadKeys(cursor, true)])
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Redis 操作失败')
      if (cause instanceof ApiError && cause.status === 409) void loadDetail(detail.key, detailCursor)
      throw cause
    } finally { setBusy(false) }
  }

  const askConfirm = (state: ConfirmState) => {
    setConfirmText('')
    setConfirm(state)
  }

  const runConfirmed = async () => {
    if (!confirm || !detail || confirmText !== detail.key) return
    setBusy(true)
    try {
      await confirm.run()
      setConfirm(null)
      setConfirmText('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Redis 操作失败')
      setConfirm(null)
    } finally { setBusy(false) }
  }

  const warmApplication = async () => {
    setBusy(true)
    try {
      const result = await api.prewarmRedis()
      setPrewarm(result)
      showToast(`应用缓存预热完成 · ${result.durationMs}ms`)
      setError('')
    } catch (cause) { setError(cause instanceof Error ? cause.message : '缓存预热失败') }
    finally { setBusy(false) }
  }

  const pageForward = () => {
    if (nextCursor === '0') return
    setCursorHistory(history => [...history, cursor])
    void loadKeys(nextCursor)
  }

  const pageBack = () => {
    const previous = cursorHistory.at(-1)
    if (previous == null) return
    setCursorHistory(history => history.slice(0, -1))
    void loadKeys(previous)
  }

  const detailForward = () => {
    if (!detail?.nextCursor || detail.nextCursor === '0') return
    setDetailHistory(history => [...history, detailCursor])
    void loadDetail(detail.key, detail.nextCursor)
  }

  const detailBack = () => {
    const previous = detailHistory.at(-1)
    if (previous == null || !detail) return
    setDetailHistory(history => history.slice(0, -1))
    void loadDetail(detail.key, previous)
  }

  const selectKey = (key: string) => {
    setDetailHistory([])
    void loadDetail(key, '0')
  }

  return <div className="page redis-page">
    <section className="redis-hero">
      <div>
        <span className="eyebrow"><Database/>Redis Operations</span>
        <h1>把缓存状态，变成可控现场。</h1>
        <p>扫描整个 Redis 数据库，检查数据结构、TTL 与内存占用，并在修改 AI Watch 数据后自动校验和刷新应用快照。</p>
      </div>
      <div className="redis-hero-actions">
        <button className="secondary" onClick={() => void refresh()} disabled={loading || busy || refreshing}><RefreshCw className={loading || refreshing ? 'spinning' : ''}/>{refreshing ? '刷新中' : '刷新'}</button>
        <button className="primary redis-warm-button" onClick={() => void warmApplication()} disabled={busy}><Flame/>应用缓存预热</button>
      </div>
    </section>

    {error && <div className="error-banner" role="alert"><AlertTriangle/><div><strong>Redis 操作未完成</strong><span>{error}</span></div><button onClick={() => setError('')}>关闭</button></div>}

    <Overview overview={overview} loading={loading} failed={Boolean(error) && !overview}/>
    {prewarm && <PrewarmStrip result={prewarm} close={() => setPrewarm(null)}/>} 

    <section className="redis-workbench">
      {refreshing && <div className="redis-refresh-overlay" role="status" aria-live="polite"><div><LoaderCircle className="spinning"/><strong>正在刷新 Redis 数据</strong><span>正在重新读取连接状态、Key 列表和当前详情…</span></div></div>}
      <aside className="panel redis-browser">
        <header className="redis-browser-head">
          <div><span>KEY SPACE</span><strong>{overview?.keyCount ?? '—'} 个 Key</strong></div>
          <button className="icon-button" onClick={() => void loadKeys(cursor)} aria-label="刷新 Key 列表"><RefreshCw className={loading ? 'spinning' : ''}/></button>
        </header>
        <div className="redis-filters">
          <label className="redis-search"><Search/><input value={pattern} onChange={event => setPattern(event.target.value)} aria-label="Key 搜索模式" placeholder="* 或 user:*"/></label>
          <select value={typeFilter} onChange={event => setTypeFilter(event.target.value)} aria-label="Redis 数据类型">
            <option value="all">全部类型</option><option value="string">String</option><option value="hash">Hash</option><option value="list">List</option><option value="set">Set</option><option value="zset">ZSet</option>
          </select>
        </div>
        <div className="redis-key-list" aria-busy={loading}>
          {loading ? Array.from({ length: 8 }, (_, index) => <div className="redis-key-skeleton" key={index}/>) : keys.length ? keys.map(item => <KeyRow key={item.key} item={item} selected={item.key === selectedKey} open={() => selectKey(item.key)}/>) : <div className="redis-empty"><Search/><strong>没有匹配的 Key</strong><span>调整 pattern 或数据类型筛选。</span></div>}
        </div>
        <footer className="redis-pagination">
          <button className="secondary" onClick={pageBack} disabled={!cursorHistory.length}><ChevronLeft/>上一页</button>
          <code>cursor {cursor}</code>
          <button className="secondary" onClick={pageForward} disabled={nextCursor === '0'}>下一页<ChevronRight/></button>
        </footer>
      </aside>

      <section className="panel redis-inspector">
        {!selectedKey ? <RedisLanding/> : detailLoading && !detail ? <div className="redis-detail-loading"><LoaderCircle className="spinning"/><span>正在读取 Key...</span></div> : detail ? <KeyInspector
          detail={detail}
          busy={busy}
          mutate={mutate}
          confirm={askConfirm}
          reload={() => void loadDetail(detail.key, detailCursor)}
          updated={setDetail}
          refreshState={async () => { await Promise.all([loadOverview(), loadKeys(cursor, true)]) }}
          deleteKey={() => askConfirm({ title: '删除整个 Key？', description: '此操作会从 Redis 中永久删除当前 Key。AI Watch 命名空间数据会在删除后自动预热并校验。', actionLabel: '确认删除', danger: true, run: async () => execute(async () => { await api.deleteRedisKey({ key: detail.key, version: detail.version, confirmKey: detail.key }); setSelectedKey(''); setDetail(null) }, 'Key 已删除') })}
          renamed={newKey => { setSelectedKey(newKey); setDetailHistory([]); void loadDetail(newKey, '0') }}
          cursor={detailCursor}
          canBack={detailHistory.length > 0}
          canForward={Boolean(detail.nextCursor && detail.nextCursor !== '0')}
          pageBack={detailBack}
          pageForward={detailForward}
        /> : null}
      </section>
    </section>

    {confirm && detail && <ConfirmDialog state={confirm} keyName={detail.key} value={confirmText} setValue={setConfirmText} close={() => setConfirm(null)} run={() => void runConfirmed()} busy={busy}/>} 
    {toast && <div className="toast-inline success"><CheckCircle2/>{toast}</div>}
  </div>
}

function Overview({ overview, loading, failed }: { overview: RedisOverview | null; loading: boolean; failed: boolean }) {
  const connection = loading && !overview
    ? { value: 'CHECKING', detail: '正在连接 Redis', tone: 'muted' }
    : overview?.connected
      ? { value: 'ONLINE', detail: overview.version ? `Redis ${overview.version}` : '连接正常', tone: 'green' }
      : { value: 'OFFLINE', detail: failed ? '连接失败，请检查 Redis' : 'Redis 未连接', tone: 'red' }
  const metrics = [
    { icon: <Activity/>, label: '连接状态', ...connection },
    { icon: <KeyRound/>, label: 'Key 总数', value: overview ? String(overview.keyCount) : '—', detail: `${overview?.expiringKeys ?? 0} 个设置了过期时间`, tone: 'cyan' },
    { icon: <MemoryStick/>, label: '内存占用', value: overview?.usedMemoryHuman || bytes(overview?.usedMemoryBytes), detail: overview?.maxMemoryBytes ? `上限 ${bytes(overview.maxMemoryBytes)}` : '未设置 maxmemory', tone: 'violet' },
    { icon: <Gauge/>, label: '缓存命中率', value: overview ? `${(overview.hitRate * 100).toFixed(1)}%` : '—', detail: `${overview?.latencyMs ?? '—'}ms · ${uptimeLabel(overview?.uptimeSeconds ?? 0)}`, tone: 'amber' },
  ]
  return <section className="metric-grid redis-metrics">{metrics.map(metric => <div className={`metric-card tone-${metric.tone}`} key={metric.label}><div className="metric-icon">{metric.icon}</div><div><span>{metric.label}</span><strong>{metric.value}</strong><small>{metric.detail}</small></div></div>)}</section>
}

function PrewarmStrip({ result, close }: { result: RedisPrewarmResult; close: () => void }) {
  const snapshots = [
    ['设置', result.snapshots.settings], ['摘要', result.snapshots.summaries], ['示例', result.snapshots.providerExamples],
    ['计划', result.snapshots.schedules], ['手动供应商', result.snapshots.manualProviders], ['CC Switch', result.snapshots.ccSwitchProviders], ['钉钉', result.snapshots.dingTalk],
  ]
  return <section className="redis-prewarm-strip"><div className="redis-prewarm-mark"><Flame/><span>PREWARM</span></div><div className="redis-prewarm-items">{snapshots.map(([label, count]) => <span key={String(label)}><i/><strong>{count}</strong>{label}</span>)}</div><em>{result.durationMs}ms</em><button className="icon-button" onClick={close} aria-label="关闭预热结果"><X/></button></section>
}

function KeyRow({ item, selected, open }: { item: RedisKeySummary; selected: boolean; open: () => void }) {
  const meta = typeMeta[item.type] || { label: item.type.toUpperCase(), icon: <CircleDot/> }
  const ttlTone = !item.persistent && item.ttlMillis >= 0 && item.ttlMillis < 60_000 ? 'urgent' : ''
  return <button className={`redis-key-row ${selected ? 'selected' : ''}`} onClick={open}>
    <span className={`redis-type-icon type-${item.type}`}>{meta.icon}</span>
    <span className="redis-key-copy"><strong>{item.key}</strong><small>{meta.label} · {item.size} {item.type === 'string' ? 'bytes' : 'items'} · {bytes(item.memoryBytes)}</small></span>
    <span className={`redis-ttl ${ttlTone}`}><Timer/>{ttlLabel(item)}</span>
  </button>
}

function RedisLanding() {
  return <div className="redis-landing"><div className="redis-orbit"><Database/><i/><i/><i/></div><span className="eyebrow">SELECT A KEY</span><h2>选择一个 Key，进入数据现场。</h2><p>右侧检查器会根据 Redis 类型切换编辑模式。未知类型保持只读，所有 AI Watch 数据修改都会自动触发应用快照校验。</p><div><span><CheckCircle2/>结构化命令</span><span><CheckCircle2/>版本冲突保护</span><span><CheckCircle2/>失败自动回滚</span></div></div>
}

function KeyInspector({ detail, busy, mutate, confirm, reload, updated, refreshState, deleteKey, renamed, cursor, canBack, canForward, pageBack, pageForward }: {
  detail: RedisKeyDetail
  busy: boolean
  mutate: (input: Omit<RedisMutationInput, 'key' | 'version'>, success: string) => Promise<void>
  confirm: (state: ConfirmState) => void
  reload: () => void
  updated: (detail: RedisKeyDetail) => void
  refreshState: () => Promise<void>
  deleteKey: () => void
  renamed: (key: string) => void
  cursor: string
  canBack: boolean
  canForward: boolean
  pageBack: () => void
  pageForward: () => void
}) {
  const [ttl, setTTL] = useState(detail.persistent ? '' : String(Math.max(1, Math.ceil(detail.ttlMillis / 1000))))
  const [newKey, setNewKey] = useState(detail.key)
  useEffect(() => { setTTL(detail.persistent ? '' : String(Math.max(1, Math.ceil(detail.ttlMillis / 1000)))); setNewKey(detail.key) }, [detail.key, detail.ttlMillis, detail.persistent])

  const saveTTL = async () => {
    const parsed = ttl.trim() ? Number(ttl) : undefined
    if (parsed != null && (!Number.isFinite(parsed) || parsed <= 0)) return
    const result = await api.updateRedisTTL({ key: detail.key, version: detail.version, confirmKey: detail.key, ttlSeconds: parsed })
    updated(result.key)
    await refreshState()
  }
  const doRename = async () => {
    const result = await api.renameRedisKey({ key: detail.key, newKey: newKey.trim(), version: detail.version, confirmKey: detail.key })
    renamed(result.key.key)
    await refreshState()
  }

  return <div className="redis-detail">
    <header className="redis-detail-head">
      <div className={`redis-type-icon large type-${detail.type}`}>{typeMeta[detail.type]?.icon || <CircleDot/>}</div>
      <div><span>{typeMeta[detail.type]?.label || detail.type.toUpperCase()} KEY</span><h2>{detail.key}</h2></div>
      <button className="icon-button" onClick={reload} aria-label="刷新当前 Key"><RefreshCw/></button>
    </header>
    <div className="redis-detail-stats">
      <div><span>TTL</span><strong>{ttlLabel(detail)}</strong></div><div><span>元素 / 字节</span><strong>{detail.size}</strong></div><div><span>内存</span><strong>{bytes(detail.memoryBytes)}</strong></div><div><span>编码</span><strong>{detail.encoding || '—'}</strong></div><div><span>版本指纹</span><strong title={detail.version}>{detail.version ? detail.version.slice(0, 10) : '只读'}</strong></div>
    </div>
    <div className="redis-editor-shell">
      <Editor detail={detail} mutate={mutate} confirm={confirm}/>
      {detail.type !== 'string' && <div className="redis-value-pagination"><button className="secondary" onClick={pageBack} disabled={!canBack}><ChevronLeft/>上一页</button><code>cursor {cursor}</code><button className="secondary" onClick={pageForward} disabled={!canForward}>下一页<ChevronRight/></button></div>}
    </div>
    <section className="redis-maintenance">
      <div className="redis-maintenance-title"><div><AlertTriangle/><span>KEY MAINTENANCE</span></div><p>TTL、重命名与删除会直接作用于 Redis。高风险动作需要输入完整 Key 名。</p></div>
      <div className="redis-maintenance-grid">
        <label><span>TTL 秒数</span><div><input value={ttl} onChange={event => setTTL(event.target.value)} inputMode="numeric" placeholder="留空 = 持久"/><button className="secondary" disabled={busy} onClick={() => confirm({ title: '更新 Key TTL？', description: ttl.trim() ? `当前 Key 将在 ${ttl} 秒后过期。` : '当前 Key 将被设为永久有效。', actionLabel: '更新 TTL', run: saveTTL })}><Timer/>应用</button></div></label>
        <label><span>重命名</span><div><input value={newKey} onChange={event => setNewKey(event.target.value)} /><button className="secondary" disabled={busy || !newKey.trim() || newKey.trim() === detail.key} onClick={() => confirm({ title: '重命名 Redis Key？', description: `将 ${detail.key} 重命名为 ${newKey.trim()}。目标 Key 必须不存在，且不能跨越 AI Watch 命名空间边界。`, actionLabel: '确认重命名', run: doRename })}><Pencil/>重命名</button></div></label>
      </div>
      <button className="danger-button redis-delete-key" onClick={deleteKey} disabled={busy}><Trash2/>删除整个 Key</button>
    </section>
  </div>
}

function Editor({ detail, mutate, confirm }: { detail: RedisKeyDetail; mutate: (input: Omit<RedisMutationInput, 'key' | 'version'>, success: string) => Promise<void>; confirm: (state: ConfirmState) => void }) {
  if (detail.type === 'string') return <StringEditor detail={detail} mutate={mutate} confirm={confirm}/>
  if (detail.type === 'hash') return <HashEditor entries={(detail.value || []) as RedisHashEntry[]} mutate={mutate} confirm={confirm}/>
  if (detail.type === 'list') return <ListEditor values={(detail.value || []) as string[]} offset={Number(detail.cursor || 0)} mutate={mutate} confirm={confirm}/>
  if (detail.type === 'set') return <SetEditor values={(detail.value || []) as string[]} mutate={mutate} confirm={confirm}/>
  if (detail.type === 'zset') return <ZSetEditor entries={(detail.value || []) as RedisZSetEntry[]} mutate={mutate} confirm={confirm}/>
  return <div className="redis-readonly"><Server/><strong>{detail.type.toUpperCase()} 暂时只读</strong><p>首版只允许编辑 String、Hash、List、Set 和 ZSet。你仍然可以检查元信息、调整 TTL、重命名或删除这个 Key。</p></div>
}

function StringEditor({ detail, mutate, confirm }: { detail: RedisKeyDetail; mutate: (input: Omit<RedisMutationInput, 'key' | 'version'>, success: string) => Promise<void>; confirm: (state: ConfirmState) => void }) {
  const [value, setValue] = useState(String(detail.value ?? ''))
  const [mode, setMode] = useState<'preview' | 'edit'>('preview')
  useEffect(() => { setValue(String(detail.value ?? '')); setMode('preview') }, [detail.key, detail.version, detail.value])
  const parsed = useMemo(() => parseJSONValue(value), [value])
  const original = String(detail.value ?? '')
  return <div className="redis-string-editor">
    <div className="redis-editor-toolbar string-toolbar"><div><Braces/><span>STRING VALUE</span>{parsed.isJSON && <em>{parsed.kind.toUpperCase()}</em>}</div><div className="redis-editor-tabs"><button className={mode === 'preview' ? 'active' : ''} onClick={() => setMode('preview')}>结构预览</button><button className={mode === 'edit' ? 'active' : ''} onClick={() => setMode('edit')}><Pencil/>原文编辑</button></div></div>
    {mode === 'preview' ? <div className="redis-string-preview"><JSONValuePreview raw={value}/></div> : <><div className="redis-edit-tools"><span>{parsed.isJSON ? '已识别为合法 JSON' : '普通文本'}</span><button className="secondary" disabled={!parsed.isJSON || !parsed.pretty} onClick={() => parsed.pretty && setValue(parsed.pretty)}>格式化 JSON</button></div><textarea value={value} onChange={event => setValue(event.target.value)} spellCheck={false}/></>}
    {detail.truncated && <div className="redis-truncated"><AlertTriangle/>值过大，当前只显示安全预览，禁止覆盖。</div>}
    <footer><span>{new TextEncoder().encode(value).length.toLocaleString()} bytes · {parsed.kind}</span><button className="primary" disabled={detail.truncated || value === original} onClick={() => confirm({ title: '覆盖 String 值？', description: '新的内容会立即写入 Redis；如果这是 AI Watch 数据，写入后会自动预热并在失败时回滚。', actionLabel: '确认覆盖', run: () => mutate({ operation: 'string:set', value, confirmKey: detail.key }, 'String 已更新') })}><Save/>保存值</button></footer>
  </div>
}

function HashEditor({ entries, mutate, confirm }: { entries: RedisHashEntry[]; mutate: (input: Omit<RedisMutationInput, 'key' | 'version'>, success: string) => Promise<void>; confirm: (state: ConfirmState) => void }) {
  const [field, setField] = useState('')
  const [value, setValue] = useState('')
  return <CollectionTable title="HASH FIELDS" count={entries.length} columns={['Field', 'Value', '类型', '操作']} add={<><input value={field} onChange={event => setField(event.target.value)} placeholder="field"/><input value={value} onChange={event => setValue(event.target.value)} placeholder="value / JSON"/><button className="primary" disabled={!field} onClick={() => void mutate({ operation: 'hash:set', field, value }, 'Hash 字段已写入').then(() => { setField(''); setValue('') }).catch(() => undefined)}><Plus/>写入字段</button></>}>
    {entries.map(entry => <EditableValueRow key={entry.field} identity={<code>{entry.field}</code>} raw={entry.value} save={next => confirm({ title: '覆盖 Hash 字段？', description: `字段 ${entry.field} 的值将被更新。`, actionLabel: '确认更新', run: () => mutate({ operation: 'hash:set', field: entry.field, value: next }, 'Hash 字段已更新') })} remove={() => confirm({ title: '删除 Hash 字段？', description: `字段 ${entry.field} 将从当前 Hash 中删除。`, actionLabel: '确认删除', danger: true, run: () => mutate({ operation: 'hash:delete', field: entry.field }, 'Hash 字段已删除') })}/>) }
  </CollectionTable>
}

function ListEditor({ values, offset, mutate, confirm }: { values: string[]; offset: number; mutate: (input: Omit<RedisMutationInput, 'key' | 'version'>, success: string) => Promise<void>; confirm: (state: ConfirmState) => void }) {
  const [value, setValue] = useState('')
  return <CollectionTable title="LIST ITEMS" count={values.length} columns={['Index', 'Value', '类型', '操作']} add={<><input value={value} onChange={event => setValue(event.target.value)} placeholder="追加文本或 JSON"/><button className="primary" onClick={() => void mutate({ operation: 'list:append', value }, 'List 元素已追加').then(() => setValue('')).catch(() => undefined)}><Plus/>追加</button></>}>
    {values.map((item, index) => <EditableValueRow key={`${offset + index}-${index}`} identity={<code>#{offset + index}</code>} raw={item} save={next => confirm({ title: '覆盖 List 元素？', description: `索引 ${offset + index} 的值将被替换。`, actionLabel: '确认更新', run: () => mutate({ operation: 'list:set', index: offset + index, value: next }, 'List 元素已更新') })} remove={() => confirm({ title: '删除 List 元素？', description: `索引 ${offset + index} 的元素将被删除。`, actionLabel: '确认删除', danger: true, run: () => mutate({ operation: 'list:delete', index: offset + index }, 'List 元素已删除') })}/>) }
  </CollectionTable>
}

function SetEditor({ values, mutate, confirm }: { values: string[]; mutate: (input: Omit<RedisMutationInput, 'key' | 'version'>, success: string) => Promise<void>; confirm: (state: ConfirmState) => void }) {
  const [member, setMember] = useState('')
  return <CollectionTable title="SET MEMBERS" count={values.length} columns={['Member', '类型', '操作']} add={<><input value={member} onChange={event => setMember(event.target.value)} placeholder="新成员 / JSON"/><button className="primary" onClick={() => void mutate({ operation: 'set:add', member }, 'Set 成员已添加').then(() => setMember('')).catch(() => undefined)}><Plus/>添加成员</button></>}>
    {values.map(item => <tr key={item}><td className="redis-value-cell"><JSONValuePreview raw={item} compact/></td><td><ValueKind raw={item}/></td><td className="redis-table-actions"><button className="icon-button danger" onClick={() => confirm({ title: '移除 Set 成员？', description: `成员 ${item} 将从当前 Set 中移除。`, actionLabel: '确认移除', danger: true, run: () => mutate({ operation: 'set:delete', member: item }, 'Set 成员已移除') })} aria-label={`移除 ${item}`}><Trash2/></button></td></tr>)}
  </CollectionTable>
}

function ZSetEditor({ entries, mutate, confirm }: { entries: RedisZSetEntry[]; mutate: (input: Omit<RedisMutationInput, 'key' | 'version'>, success: string) => Promise<void>; confirm: (state: ConfirmState) => void }) {
  const [member, setMember] = useState('')
  const [score, setScore] = useState('0')
  return <CollectionTable title="SORTED SET" count={entries.length} columns={['Member', 'Score', '类型', '操作']} add={<><input value={member} onChange={event => setMember(event.target.value)} placeholder="member / JSON"/><input value={score} onChange={event => setScore(event.target.value)} inputMode="decimal" placeholder="score"/><button className="primary" disabled={!member || !Number.isFinite(Number(score))} onClick={() => void mutate({ operation: 'zset:set', member, score: Number(score) }, 'ZSet 成员已写入').then(() => { setMember(''); setScore('0') }).catch(() => undefined)}><Plus/>写入成员</button></>}>
    {entries.map(entry => <ScoreRow key={entry.member} entry={entry} save={next => confirm({ title: '更新 ZSet Score？', description: `成员 Score 将变为 ${next}。`, actionLabel: '确认更新', run: () => mutate({ operation: 'zset:set', member: entry.member, score: Number(next) }, 'ZSet Score 已更新') })} remove={() => confirm({ title: '删除 ZSet 成员？', description: '当前成员将从 ZSet 中删除。', actionLabel: '确认删除', danger: true, run: () => mutate({ operation: 'zset:delete', member: entry.member }, 'ZSet 成员已删除') })}/>) }
  </CollectionTable>
}

function CollectionTable({ title, count, columns, add, children }: { title: string; count: number; columns: string[]; add: React.ReactNode; children: React.ReactNode }) {
  return <div className="redis-collection"><div className="redis-editor-toolbar"><div><Database/><span>{title}</span><em>{count} ON PAGE</em></div></div><div className="redis-collection-add">{add}</div><div className="redis-table-scroll"><table className={`redis-value-table cols-${columns.length}`}><thead><tr>{columns.map(column => <th key={column}>{column}</th>)}</tr></thead><tbody>{children}</tbody></table></div></div>
}

function EditableValueRow({ identity, raw, save, remove }: { identity: React.ReactNode; raw: string; save: (value: string) => void; remove: () => void }) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(raw)
  useEffect(() => { setDraft(raw); setEditing(false) }, [raw])
  return <tr><td className="redis-identity-cell">{identity}</td><td className="redis-value-cell">{editing ? <textarea value={draft} onChange={event => setDraft(event.target.value)} autoFocus/> : <JSONValuePreview raw={raw} compact/>}</td><td><ValueKind raw={editing ? draft : raw}/></td><td className="redis-table-actions">{editing ? <><button className="icon-button" disabled={draft === raw} onClick={() => save(draft)} aria-label="保存值"><Save/></button><button className="icon-button" onClick={() => { setDraft(raw); setEditing(false) }} aria-label="取消编辑"><X/></button></> : <button className="icon-button" onClick={() => setEditing(true)} aria-label="编辑值"><Pencil/></button>}<button className="icon-button danger" onClick={remove} aria-label="删除值"><Trash2/></button></td></tr>
}

function ScoreRow({ entry, save, remove }: { entry: RedisZSetEntry; save: (value: string) => void; remove: () => void }) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(String(entry.score))
  useEffect(() => { setDraft(String(entry.score)); setEditing(false) }, [entry.score])
  const valid = draft.trim() !== '' && Number.isFinite(Number(draft))
  return <tr><td className="redis-value-cell"><JSONValuePreview raw={entry.member} compact/></td><td>{editing ? <input className="score-input" value={draft} onChange={event => setDraft(event.target.value)} inputMode="decimal" autoFocus/> : <code className="score-value">{entry.score}</code>}</td><td><ValueKind raw={entry.member}/></td><td className="redis-table-actions">{editing ? <><button className="icon-button" disabled={!valid || Number(draft) === entry.score} onClick={() => save(draft)} aria-label="保存 Score"><Save/></button><button className="icon-button" onClick={() => { setDraft(String(entry.score)); setEditing(false) }} aria-label="取消编辑"><X/></button></> : <button className="icon-button" onClick={() => setEditing(true)} aria-label="编辑 Score"><Pencil/></button>}<button className="icon-button danger" onClick={remove} aria-label="删除成员"><Trash2/></button></td></tr>
}

function ValueKind({ raw }: { raw: string }) {
  const parsed = useMemo(() => parseJSONValue(raw), [raw])
  return <span className={`value-kind ${parsed.isJSON ? 'json' : 'text'}`}>{parsed.kind}</span>
}

function ConfirmDialog({ state, keyName, value, setValue, close, run, busy }: { state: ConfirmState; keyName: string; value: string; setValue: (value: string) => void; close: () => void; run: () => void; busy: boolean }) {
  const dialogRef = useRef<HTMLDivElement>(null)
  const closeRef = useRef(close)
  closeRef.current = close
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); closeRef.current(); return }
      if (event.key !== 'Tab' || !dialogRef.current) return
      const focusable = Array.from(dialogRef.current.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled])'))
      if (!focusable.length) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKeyDown)
    return () => { document.removeEventListener('keydown', onKeyDown); previous?.focus() }
  }, [])
  return <div className="event-confirm-overlay"><button className="event-confirm-scrim" onClick={close} aria-label="关闭确认窗口"/><div ref={dialogRef} className={`event-confirm redis-confirm ${state.danger ? 'danger' : ''}`} role="dialog" aria-modal="true" aria-labelledby="redis-confirm-title"><div className="event-confirm-icon">{state.danger ? <Trash2/> : <AlertTriangle/>}</div><h2 id="redis-confirm-title">{state.title}</h2><p>{state.description}</p><label><span>输入完整 Key 以确认</span><code>{keyName}</code><input autoFocus value={value} onChange={event => setValue(event.target.value)} placeholder={keyName}/></label><div><button className="secondary" onClick={close}>取消</button><button className={state.danger ? 'danger-button' : 'primary'} disabled={value !== keyName || busy} onClick={run}>{busy ? <LoaderCircle className="spinning"/> : state.danger ? <Trash2/> : <Save/>}{state.actionLabel}</button></div></div></div>
}
