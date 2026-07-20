import { Children, isValidElement, useEffect, useId, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Check, ChevronDown } from 'lucide-react'

type SelectProps = React.SelectHTMLAttributes<HTMLSelectElement>

type SelectOption = {
  value: string
  label: string
  disabled: boolean
}

export function Select({ children, className = '', disabled, value, defaultValue, onChange, ...props }: SelectProps) {
  const nativeRef = useRef<HTMLSelectElement>(null)
  const buttonRef = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  const [position, setPosition] = useState({ left: 0, top: 0, width: 220, maxHeight: 320 })
  const listboxId = useId()
  const options = useMemo<SelectOption[]>(() => Children.toArray(children).flatMap(child => {
    if (!isValidElement<{ value?: string | number; disabled?: boolean; children?: React.ReactNode }>(child) || child.type !== 'option') return []
    return [{ value: String(child.props.value ?? ''), label: String(child.props.children ?? ''), disabled: Boolean(child.props.disabled) }]
  }), [children])
  const selectedValue = String(value ?? defaultValue ?? nativeRef.current?.value ?? '')
  const selectedIndex = Math.max(0, options.findIndex(option => option.value === selectedValue))
  const selected = options[selectedIndex]

  const measure = () => {
    const rect = buttonRef.current?.getBoundingClientRect()
    if (!rect) return
    const gap = 6
    const availableBelow = window.innerHeight - rect.bottom - gap - 12
    const availableAbove = rect.top - gap - 12
    const maxHeight = Math.max(160, Math.min(340, Math.max(availableBelow, availableAbove)))
    const openAbove = availableBelow < 180 && availableAbove > availableBelow
    const width = Math.max(rect.width, 190)
    setPosition({
      left: Math.min(rect.left, window.innerWidth - width - 12),
      top: openAbove ? Math.max(12, rect.top - Math.min(maxHeight, options.length * 42 + 12) - gap) : rect.bottom + gap,
      width,
      maxHeight,
    })
  }

  const openMenu = () => {
    if (disabled) return
    measure()
    setActive(selectedIndex)
    setOpen(true)
  }

  const choose = (index: number) => {
    const option = options[index]
    const native = nativeRef.current
    if (!option || option.disabled || !native) return
    native.value = option.value
    native.dispatchEvent(new Event('change', { bubbles: true }))
    setOpen(false)
    buttonRef.current?.focus()
  }

  const move = (direction: 1 | -1) => {
    if (!options.length) return
    let next = active
    do { next = (next + direction + options.length) % options.length } while (options[next]?.disabled && next !== active)
    setActive(next)
  }

  useEffect(() => {
    if (!open) return
    const close = (event: PointerEvent) => {
      if (buttonRef.current?.contains(event.target as Node)) return
      if ((event.target as Element).closest?.('.select-popover')) return
      setOpen(false)
    }
    const reposition = () => measure()
    document.addEventListener('pointerdown', close)
    window.addEventListener('resize', reposition)
    window.addEventListener('scroll', reposition, true)
    return () => {
      document.removeEventListener('pointerdown', close)
      window.removeEventListener('resize', reposition)
      window.removeEventListener('scroll', reposition, true)
    }
  }, [open, options.length])

  return <span className={`select-shell ${disabled ? 'disabled' : ''} ${className}`.trim()}>
    <select ref={nativeRef} className="select-native" disabled={disabled} value={value} defaultValue={defaultValue} onChange={onChange} {...props}>
      {children}
    </select>
    <button ref={buttonRef} type="button" className={`select-trigger ${open ? 'open' : ''}`} disabled={disabled} aria-haspopup="listbox" aria-expanded={open} aria-controls={listboxId} onClick={() => open ? setOpen(false) : openMenu()} onKeyDown={event => {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); if (!open) openMenu(); else move(event.key === 'ArrowDown' ? 1 : -1) }
      else if (event.key === 'Home') { event.preventDefault(); setActive(options.findIndex(option => !option.disabled)) }
      else if (event.key === 'End') { event.preventDefault(); let last = options.length - 1; while (last > 0 && options[last]?.disabled) last--; setActive(last) }
      else if ((event.key === 'Enter' || event.key === ' ') && open) { event.preventDefault(); choose(active) }
      else if (event.key === 'Escape' && open) { event.preventDefault(); event.stopPropagation(); setOpen(false) }
    }}>
      <span>{selected?.label || '请选择'}</span><ChevronDown/>
    </button>
    {open && createPortal(<div id={listboxId} className="select-popover" role="listbox" style={{ left: position.left, top: position.top, width: position.width, maxHeight: position.maxHeight }}>
      {options.map((option, index) => <button type="button" role="option" aria-selected={index === selectedIndex} disabled={option.disabled} className={`${index === selectedIndex ? 'selected' : ''} ${index === active ? 'active' : ''}`} key={`${option.value}-${index}`} onPointerMove={() => setActive(index)} onClick={() => choose(index)}>
        <span>{option.label}</span>{index === selectedIndex && <Check/>}
      </button>)}
    </div>, document.body)}
  </span>
}
