import { useEffect, useMemo, useState } from 'react'
import { Check, ChevronDown, ChevronRight, Copy, FoldVertical, UnfoldVertical } from 'lucide-react'

export type JSONExpansion = 'auto' | 'expanded' | 'collapsed'

export interface ParsedJSONValue {
  isJSON: boolean
  value?: unknown
  pretty?: string
  kind: string
}

export function parseJSONValue(raw: string): ParsedJSONValue {
  try {
    const value = JSON.parse(raw) as unknown
    if (Array.isArray(value) || isJSONObject(value)) {
      return { isJSON: true, value, pretty: JSON.stringify(value, null, 2), kind: jsonKind(value) }
    }
    return { isJSON: false, kind: 'Text' }
  } catch {
    return { isJSON: false, kind: 'Text' }
  }
}

export function JSONViewer({ value, expansion = 'auto', compact = false }: { value: unknown; expansion?: JSONExpansion; compact?: boolean }) {
  return <div className={`json-viewer ${compact ? 'compact' : ''}`}><JSONNode value={value} name={null} depth={0} expansion={expansion}/></div>
}

export function JSONValuePreview({ raw, compact = false }: { raw: string; compact?: boolean }) {
  const parsed = useMemo(() => parseJSONValue(raw), [raw])
  const [open, setOpen] = useState(!compact)
  const [expansion, setExpansion] = useState<JSONExpansion>('auto')
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    await navigator.clipboard.writeText(raw)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1600)
  }

  if (!parsed.isJSON) {
    const numeric = raw.trim() !== '' && Number.isFinite(Number(raw.trim()))
    return <div className={`value-preview text ${numeric ? 'numeric' : ''} ${open ? 'open' : ''}`}>
      <button className="value-preview-main" onClick={() => setOpen(current => !current)} title={raw}><code>{raw || '空字符串'}</code></button>
      <button className="value-copy" onClick={() => void copy()} aria-label="复制原始值">{copied ? <Check/> : <Copy/>}</button>
    </div>
  }

  return <div className={`value-preview json ${open ? 'open' : ''}`}>
    <div className="value-preview-summary">
      <button className="json-summary-trigger" onClick={() => setOpen(current => !current)} aria-expanded={open}>
        <span className="json-kind">{parsed.kind}</span>
        <code>{jsonSummary(parsed.value)}</code>
        <span className="json-summary-chevron" aria-hidden="true">{open ? <ChevronDown/> : <ChevronRight/>}</span>
      </button>
      <button className="value-copy" onClick={() => void copy()} aria-label="复制 JSON 原文">{copied ? <Check/> : <Copy/>}</button>
    </div>
    {open && <div className="value-json-expanded">
      <div className="json-viewer-actions" role="toolbar" aria-label="JSON 展开控制">
        <button onClick={() => setExpansion('expanded')} aria-label="展开全部 JSON 节点" title="展开全部"><UnfoldVertical/></button>
        <button onClick={() => setExpansion('collapsed')} aria-label="收起全部 JSON 节点" title="收起全部"><FoldVertical/></button>
      </div>
      <JSONViewer value={parsed.value} expansion={expansion} compact={compact}/>
    </div>}
  </div>
}

function JSONNode({ value, name, depth, expansion }: { value: unknown; name: string | number | null; depth: number; expansion: JSONExpansion }) {
  const collection = Array.isArray(value) || isJSONObject(value)
  const [collapsed, setCollapsed] = useState(expansion === 'collapsed' || (expansion === 'auto' && depth >= 2))
  useEffect(() => {
    setCollapsed(expansion === 'collapsed' || (expansion === 'auto' && depth >= 2))
  }, [expansion, depth])

  if (!collection) {
    return <div className="json-line"><JSONName name={name}/><JSONScalar value={value}/></div>
  }

  const entries = Array.isArray(value) ? value.map((item, index) => [index, item] as const) : Object.entries(value)
  const opener = Array.isArray(value) ? '[' : '{'
  const closer = Array.isArray(value) ? ']' : '}'
  return <div className="json-node">
    <div className="json-line collection"><button className="json-toggle" onClick={() => setCollapsed(current => !current)} aria-label={collapsed ? '展开 JSON 节点' : '收起 JSON 节点'}>{collapsed ? <ChevronRight/> : <ChevronDown/>}</button><JSONName name={name}/><span className="json-bracket">{opener}</span>{collapsed && <><span className="json-ellipsis">…</span><span className="json-count">{entries.length} 项</span><span className="json-bracket">{closer}</span></>}</div>
    {!collapsed && <><div className="json-children">{entries.map(([key, item]) => <JSONNode key={String(key)} value={item} name={key} depth={depth + 1} expansion={expansion}/>)}</div><div className="json-line closing"><span className="json-bracket">{closer}</span></div></>}
  </div>
}

function JSONName({ name }: { name: string | number | null }) {
  if (name == null) return null
  return <><span className={typeof name === 'number' ? 'json-index' : 'json-key'}>{typeof name === 'number' ? name : `"${name}"`}</span><span className="json-colon">:</span></>
}

function JSONScalar({ value }: { value: unknown }) {
  if (value === null) return <span className="json-null">null</span>
  if (typeof value === 'string') return <span className="json-string">"{value}"</span>
  if (typeof value === 'number') return <span className="json-number">{String(value)}</span>
  if (typeof value === 'boolean') return <span className="json-boolean">{String(value)}</span>
  return <span className="json-null">{String(value)}</span>
}

function isJSONObject(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === 'object' && !Array.isArray(value)
}

function jsonKind(value: unknown) {
  if (Array.isArray(value)) return 'JSON Array'
  if (isJSONObject(value)) return 'JSON Object'
  return 'Text'
}

function jsonSummary(value: unknown) {
  if (Array.isArray(value)) return `[ ${value.length} items ]`
  if (isJSONObject(value)) return `{ ${Object.keys(value).length} fields }`
  const text = JSON.stringify(value)
  return text.length > 80 ? `${text.slice(0, 77)}…` : text
}
