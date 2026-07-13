import { useCallback, useEffect, useState } from 'react'
import { Activity, CheckCircle2, Clock3, Cpu, Database, FolderClock, HardDrive, LoaderCircle, RefreshCw, Server, Settings2, TriangleAlert } from 'lucide-react'
import { api } from './api'
import type { SystemDiagnostics } from './types'

const bytes = (value: number) => value < 1024 * 1024 ? `${Math.round(value / 1024)} KiB` : `${(value / 1024 / 1024).toFixed(1)} MiB`
const checkLabel = (state: string) => state === 'ok' ? '版本可读' : state === 'timeout' ? '检查超时' : state === 'version_unreadable' ? '版本不可读' : '不可用'

export function DiagnosticsView() {
  const [data, setData] = useState<SystemDiagnostics | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const load = useCallback(async () => {
    setLoading(true); setError('')
    try { setData(await api.diagnostics()) } catch (e) { setError(e instanceof Error ? e.message : '诊断信息读取失败') }
    finally { setLoading(false) }
  }, [])
  useEffect(() => { void load() }, [load])

  return <div className="page">
    <section className="page-heading diagnostics-heading"><div><span className="eyebrow"><Server/>只读检查</span><h1>系统诊断</h1><p>检查 CLI、SQLite 与运行时边界；不会读取密钥、Webhook 或任务原始输出。</p></div><button className="secondary diagnostics-refresh" disabled={loading} onClick={() => void load()}>{loading ? <LoaderCircle className="spinning"/> : <RefreshCw/>}刷新诊断</button></section>
    {loading && !data ? <div className="diagnostics-loading"><LoaderCircle className="spinning"/>正在采集只读状态</div> : error ? <div className="error-banner"><TriangleAlert/><div><strong>诊断读取失败</strong><span>{error}</span></div><button onClick={() => void load()}>重试</button></div> : data && <>
      <section className={`diagnostic-bus ${data.status}`}><div className="diagnostic-bus-state"><span className="diagnostic-orbit"><Activity/><i/></span><span><small>diagnostic bus</small><strong>{data.status === 'ok' ? '所有关键边界正常' : '存在需要关注的项目'}</strong></span></div><div className={`diagnostic-bus-node ${data.clis.every(v => v.available) ? 'ok' : 'warning'}`}><i/><Cpu/><span>CLI</span></div><div className={`diagnostic-bus-node ${data.sqlite.available ? 'ok' : 'warning'}`}><i/><Database/><span>SQLite</span></div><div className={`diagnostic-bus-node ${data.runtime.directoryReady ? 'ok' : 'warning'}`}><i/><FolderClock/><span>Runtime</span></div><time>{new Date(data.generatedAt).toLocaleString('zh-CN', { hour12: false })}</time></section>
      <div className="diagnostics-grid">
        <section className="panel diagnostics-panel"><DiagnosticHeading icon={<Cpu/>} title="CLI 运行环境" detail="仅返回安全版本行与可执行文件名称"/><div className="diagnostic-cli-list">{data.clis.map(cli => <div className={`diagnostic-cli ${cli.checkState === 'ok' ? 'ok' : 'warning'}`} key={cli.id}><span className="diagnostic-state-icon">{cli.checkState === 'ok' ? <CheckCircle2/> : <TriangleAlert/>}</span><span><strong>{cli.name}</strong><small>{checkLabel(cli.checkState)}</small></span><dl><div><dt>命令</dt><dd>{cli.pathLabel || '—'}</dd></div><div><dt>版本</dt><dd>{cli.version || '—'}</dd></div></dl></div>)}</div></section>
        <section className="panel diagnostics-panel"><DiagnosticHeading icon={<Database/>} title="存储与运行时" detail="统计值有界，不读取任何记录内容"/><div className="diagnostic-metrics"><Metric icon={<Database/>} label="Schema" value={`v${data.sqlite.schemaVersion}`}/><Metric icon={<HardDrive/>} label="逻辑大小" value={bytes(data.sqlite.logicalBytes)}/><Metric icon={<Activity/>} label="事件" value={String(data.sqlite.eventCount)}/><Metric icon={<Clock3/>} label="计划" value={String(data.sqlite.scheduleCount)}/></div><div className="runtime-gauge"><span><i style={{ width: `${Math.min(100, data.runtime.activeJobs / Math.max(1, data.runtime.activeJobsLimit) * 100)}%` }}/></span><div><strong>{data.runtime.activeJobs} / {data.runtime.activeJobsLimit}</strong><small>活跃任务</small></div></div><div className="runtime-entry"><span className={data.runtime.directoryReady && data.runtime.directoryEntries === 0 ? 'ok' : 'warning'}>{data.runtime.directoryReady && data.runtime.directoryEntries === 0 ? <CheckCircle2/> : <TriangleAlert/>}</span><div><strong>运行目录 {data.runtime.directoryEntries} 个条目</strong><small>{data.runtime.directoryReady ? '任务结束后应自动归零' : '运行目录尚未就绪'}</small></div></div></section>
        <section className="panel diagnostics-panel diagnostics-config-panel"><DiagnosticHeading icon={<Settings2/>} title="配置生效方式" detail="明确区分保存后生效与需要重启的字段"/><div className="config-lanes"><ConfigLane kind="hot" title="热更新" items={data.config.hotReload}/><ConfigLane kind="restart" title="需要重启" items={data.config.restartRequired}/></div></section>
      </div>
    </>}
  </div>
}

function DiagnosticHeading({ icon, title, detail }: { icon: React.ReactNode; title: string; detail: string }) { return <header className="diagnostics-panel-heading"><span>{icon}</span><div><h2>{title}</h2><p>{detail}</p></div></header> }
function Metric({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) { return <div><span>{icon}</span><small>{label}</small><strong>{value}</strong></div> }
function ConfigLane({ kind, title, items }: { kind: 'hot' | 'restart'; title: string; items: SystemDiagnostics['config']['hotReload'] }) { return <section className={`config-lane ${kind}`}><header><i/>{title}<em>{items.length} 项</em></header><ul>{items.map(item => <li key={item.key}><strong>{item.label}</strong><span>{item.description}</span></li>)}</ul></section> }
