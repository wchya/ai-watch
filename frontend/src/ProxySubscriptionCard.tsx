import { useEffect, useState } from 'react'
import { AlertCircle, Check, Gauge, KeyRound, LoaderCircle, Network, RefreshCw, ShieldCheck, Trash2 } from 'lucide-react'
import { api } from './api'
import type { MihomoSubscriptionStatus } from './types'

const checkedAt = (value?: string) => value ? new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '尚未检查'
const errorStageLabel: Record<string, string> = { validation: '地址校验', storage: '加密存储', generate: '配置生成', write: '配置写入', reload: '热重载', controller: 'Controller', subscription: '订阅加载', connectivity: '连通测试', rollback: '配置回滚' }

export function ProxySubscriptionCard() {
  const [config, setConfig] = useState<MihomoSubscriptionStatus | null>(null)
  const [subscriptionUrl, setSubscriptionUrl] = useState('')
  const [busy, setBusy] = useState<'load' | 'save' | 'test' | 'clear' | null>('load')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    api.mihomoSubscription().then(setConfig).catch(e => setError(e instanceof Error ? e.message : '无法读取代理订阅状态')).finally(() => setBusy(null))
  }, [])

  const save = async () => {
    const value = subscriptionUrl.trim()
    if (!value) { setError('请输入订阅地址'); return }
    setBusy('save'); setError(''); setMessage('')
    try { setConfig(await api.saveMihomoSubscription(value)); setSubscriptionUrl(''); setMessage('订阅已加密保存并应用') }
    catch (e) { setError(e instanceof Error ? e.message : '订阅应用失败'); api.mihomoSubscription().then(setConfig).catch(() => undefined) }
    finally { setBusy(null) }
  }
  const test = async () => {
    setBusy('test'); setError(''); setMessage('')
    try { setConfig(await api.testMihomoProxy()); setMessage('代理连通测试通过') }
    catch (e) { setError(e instanceof Error ? e.message : '代理测试失败'); api.mihomoSubscription().then(setConfig).catch(() => undefined) }
    finally { setBusy(null) }
  }
  const clear = async () => {
    if (!window.confirm('清除页面保存的订阅并恢复基础代理配置？')) return
    setBusy('clear'); setError(''); setMessage('')
    try { setConfig(await api.clearMihomoSubscription()); setSubscriptionUrl(''); setMessage('订阅已清除，基础代理配置已恢复') }
    catch (e) { setError(e instanceof Error ? e.message : '清除订阅失败'); api.mihomoSubscription().then(setConfig).catch(() => undefined) }
    finally { setBusy(null) }
  }

  const applied = Boolean(config?.configured && config.applied && !config.errorStage)
  const badge = config?.configured ? applied ? '已应用' : '需检查' : '未配置'
  return <section className="panel settings-panel proxy-subscription-panel">
    <div className="panel-title"><div><h2>代理订阅</h2><p>加密保存并热重载 Mihomo</p></div></div>
    <div className="dingtalk-secure-config proxy-subscription-config">
      <div className="dingtalk-config-status"><div className="notification-icon proxy"><Network/></div><div><strong>Mihomo 订阅</strong><span>{busy === 'load' ? '正在读取配置' : config?.maskedUrl || '使用基础代理配置'}</span></div><span className={`config-badge ${applied ? 'ok' : ''}`}>{badge}</span></div>
      <div className="proxy-subscription-metrics"><div><Gauge/><span><strong>{config?.nodeCount || 0}</strong><small>可用节点</small></span></div><div><Network/><span><strong title={config?.currentNode}>{config?.currentNode || '—'}</strong><small>当前节点</small></span></div><div><RefreshCw/><span><strong>{checkedAt(config?.lastCheckedAt)}</strong><small>最近检查</small></span></div></div>
      <div className="secure-config-note compact"><ShieldCheck/><span><strong>订阅地址仅加密保存</strong><small>页面不会返回明文；新配置验证失败时自动恢复上一份可用配置。</small></span></div>
      <label className="field"><span>{config?.configured ? '替换订阅地址' : '订阅地址'}</span><div className="dingtalk-webhook-input"><KeyRound/><input aria-label="Mihomo 订阅地址" type="password" autoComplete="new-password" value={subscriptionUrl} onChange={event => setSubscriptionUrl(event.target.value)} placeholder={config?.configured ? '留空保持当前订阅' : 'https://example.com/subscription'}/></div></label>
      {config?.errorStage && <div className="form-error proxy-config-error" role={error ? 'alert' : 'status'}><AlertCircle/><span><strong>失败阶段：{errorStageLabel[config.errorStage] || config.errorStage}</strong><small>{error || config.errorMessage}</small></span></div>}
      {error && !config?.errorStage && <div className="form-error" role="alert"><AlertCircle/>{error}</div>}
      {message && <div className="dingtalk-config-message" role="status"><Check/>{message}</div>}
      <div className="notification-test-actions"><button className="primary" disabled={busy !== null || !subscriptionUrl.trim()} onClick={() => void save()}>{busy === 'save' ? <LoaderCircle className="spinning"/> : <Check/>}{busy === 'save' ? '应用中' : '保存并应用'}</button><button className="secondary" disabled={busy !== null} onClick={() => void test()}>{busy === 'test' ? <LoaderCircle className="spinning"/> : <RefreshCw/>}{busy === 'test' ? '测试中' : '重新测试'}</button>{config?.configured && <button className="danger-button" disabled={busy !== null} onClick={() => void clear()}>{busy === 'clear' ? <LoaderCircle className="spinning"/> : <Trash2/>}{busy === 'clear' ? '清除中' : '清除订阅'}</button>}</div>
    </div>
  </section>
}
