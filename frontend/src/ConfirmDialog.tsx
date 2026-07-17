import { useEffect, useRef, useState } from 'react'
import { AlertCircle, Check, LoaderCircle, ShieldAlert, X } from 'lucide-react'

type ConfirmTone = 'primary' | 'danger' | 'warning'
type ConfirmRequest = {
  title: string
  message: string
  detail?: string
  confirmLabel?: string
  tone?: ConfirmTone
  action: () => Promise<void> | void
  resolve: (completed: boolean) => void
}

const EVENT = 'ai-watch:confirm-action'

export function confirmAction(input: Omit<ConfirmRequest, 'resolve'>) {
  return new Promise<boolean>(resolve => window.dispatchEvent(new CustomEvent<ConfirmRequest>(EVENT, { detail: { ...input, resolve } })))
}

export function ConfirmHost() {
  const [request, setRequest] = useState<ConfirmRequest | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const dialogRef = useRef<HTMLElement>(null)
  const previousFocus = useRef<HTMLElement | null>(null)

  const close = (completed = false) => {
    if (busy && !completed) return
    request?.resolve(completed)
    setRequest(null); setBusy(false); setError('')
    requestAnimationFrame(() => previousFocus.current?.focus())
  }

  useEffect(() => {
    const open = (event: Event) => {
      const next = (event as CustomEvent<ConfirmRequest>).detail
      previousFocus.current = document.activeElement as HTMLElement | null
      setError(''); setBusy(false); setRequest(next)
    }
    window.addEventListener(EVENT, open)
    return () => window.removeEventListener(EVENT, open)
  }, [])

  useEffect(() => {
    if (!request) return
    const focusable = () => Array.from(dialogRef.current?.querySelectorAll<HTMLElement>('button:not([disabled]), [tabindex]:not([tabindex="-1"])') ?? [])
    requestAnimationFrame(() => focusable()[0]?.focus())
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) { event.preventDefault(); close(false); return }
      if (event.key !== 'Tab') return
      const items = focusable(); if (!items.length) return
      if (event.shiftKey && document.activeElement === items[0]) { event.preventDefault(); items.at(-1)?.focus() }
      else if (!event.shiftKey && document.activeElement === items.at(-1)) { event.preventDefault(); items[0]?.focus() }
    }
    window.addEventListener('keydown', keydown)
    return () => window.removeEventListener('keydown', keydown)
  }, [request, busy])

  if (!request) return null
  const submit = async () => {
    setBusy(true); setError('')
    try { await request.action(); close(true) }
    catch (cause) { setError(cause instanceof Error ? cause.message : '操作未完成'); setBusy(false) }
  }
  return <div className="confirm-host"><button className="confirm-host-scrim" aria-label="取消操作" disabled={busy} onClick={() => close(false)}/><section ref={dialogRef} className={`confirm-dialog tone-${request.tone || 'primary'}`} role="alertdialog" aria-modal="true" aria-labelledby="confirm-dialog-title" aria-describedby="confirm-dialog-message"><header><span><ShieldAlert/></span><button className="icon-button" disabled={busy} aria-label="关闭" onClick={() => close(false)}><X/></button></header><div><h2 id="confirm-dialog-title">{request.title}</h2><p id="confirm-dialog-message">{request.message}</p>{request.detail && <small>{request.detail}</small>}{error && <div className="confirm-dialog-error" role="alert"><AlertCircle/>{error}</div>}</div><footer><button className="secondary" disabled={busy} onClick={() => close(false)}>取消</button><button className="confirm-dialog-submit" disabled={busy} onClick={() => void submit()}>{busy ? <LoaderCircle className="spinning"/> : <Check/>}{busy ? '处理中' : request.confirmLabel || '确认'}</button></footer></section></div>
}
