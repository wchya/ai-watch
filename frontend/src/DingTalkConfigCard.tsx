import { useEffect, useState } from 'react'
import { AlertCircle, Check, KeyRound, LoaderCircle, Send, ShieldCheck, Trash2, Webhook } from 'lucide-react'
import { api } from './api'
import type { DingTalkConfig } from './types'

export function DingTalkConfigCard({ onConfigured }: { onConfigured?: (configured: boolean) => void }) {
  const [config, setConfig] = useState<DingTalkConfig | null>(null)
  const [webhook, setWebhook] = useState('')
  const [busy, setBusy] = useState<'load' | 'save' | 'clear' | 'test' | null>('load')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  const apply = (value: DingTalkConfig) => { setConfig(value); onConfigured?.(value.configured) }
  useEffect(() => {
    api.dingTalkConfig().then(apply).catch(e => setError(e instanceof Error ? e.message : '无法读取钉钉配置')).finally(() => setBusy(null))
  }, [])

  const save = async () => {
    if (!webhook.trim()) { setError('请输入钉钉机器人 Webhook URL'); return }
    setBusy('save'); setError(''); setMessage('')
    try { apply(await api.saveDingTalkConfig({ webhookUrl: webhook.trim() })); setWebhook(''); setMessage('Webhook 已加密保存') }
    catch (e) { setError(e instanceof Error ? e.message : '保存 Webhook 失败') }
    finally { setBusy(null) }
  }
  const clear = async () => {
    setBusy('clear'); setError(''); setMessage('')
    try {
      const next = await api.saveDingTalkConfig({ clearStored: true })
      apply(next); setWebhook('')
      setMessage(next.source === 'environment' ? 'Redis Webhook 已清除，现已回退环境变量配置' : 'Redis Webhook 已清除，当前没有可用的环境配置')
    }
    catch (e) { setError(e instanceof Error ? e.message : '清除 Webhook 失败') }
    finally { setBusy(null) }
  }
  const test = async () => {
    setBusy('test'); setError(''); setMessage('')
    try { await api.testDingTalk(); setMessage('测试通知已发送') }
    catch (e) { setError(e instanceof Error ? e.message : '通知发送失败') }
    finally { setBusy(null) }
  }

  const source = config?.source === 'redis' ? 'Redis 加密配置' : config?.source === 'environment' ? '环境变量回退' : '未配置'
  return <div className="dingtalk-secure-config">
    <div className="dingtalk-config-status"><div className="notification-icon dingtalk"><Webhook/></div><div><strong>钉钉机器人</strong><span>{busy === 'load' ? '正在读取配置' : `${source}${config?.maskedWebhook ? ` · ${config.maskedWebhook}` : ''}`}</span></div><span className={`config-badge ${config?.configured ? 'ok' : ''}`}>{config?.configured ? '已配置' : '未配置'}</span></div>
    <div className="secure-config-note compact"><ShieldCheck/><span><strong>Redis 优先，环境变量回退</strong><small>页面保存后使用 AES-GCM 密文；清除 Redis 配置后会自动回退到 DINGTALK_WEBHOOK_URL。</small></span></div>
    <label className="field"><span>替换 Webhook URL</span><div className="dingtalk-webhook-input"><KeyRound/><input type="password" autoComplete="new-password" value={webhook} onChange={event => setWebhook(event.target.value)} placeholder={config?.configured ? '留空不修改当前配置' : 'https://oapi.dingtalk.com/robot/send?...'}/></div></label>
    {error && <div className="form-error" role="alert"><AlertCircle/>{error}</div>}
    {message && <div className="dingtalk-config-message" role="status"><Check/>{message}</div>}
    <div className="notification-test-actions"><button className="primary" disabled={busy !== null || !webhook.trim()} onClick={() => void save()}>{busy === 'save' ? <LoaderCircle className="spinning"/> : <Check/>}{busy === 'save' ? '加密保存中' : '保存 Webhook'}</button><button className="secondary" disabled={busy !== null || !config?.configured} onClick={() => void test()}>{busy === 'test' ? <LoaderCircle className="spinning"/> : <Send/>}{busy === 'test' ? '发送中' : '发送测试通知'}</button>{config?.source === 'redis' && <button className="danger-button" disabled={busy !== null} onClick={() => void clear()}>{busy === 'clear' ? <LoaderCircle className="spinning"/> : <Trash2/>}{busy === 'clear' ? '清除中' : '清除 Redis 配置'}</button>}</div>
  </div>
}
