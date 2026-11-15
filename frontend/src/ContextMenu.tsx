import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent } from 'react'
import { useDismiss } from './useDismiss'

export type MenuItem = { label: string; onClick: () => void; disabled?: boolean } | 'separator'

// A right-click menu (tabs, the sidebars' tables), kept on screen; ↑ / ↓ move, Enter picks, Esc closes.
export default function ContextMenu({ x, y, items, onClose }: { x: number; y: number; items: MenuItem[]; onClose: () => void }) {
  const root = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState({ left: x, top: y })
  useDismiss(true, root, onClose)
  useLayoutEffect(() => {
    const el = root.current
    if (!el) return
    const { width, height } = el.getBoundingClientRect()
    setPosition({
      left: x + width > window.innerWidth - 8 ? Math.max(8, x - width) : x,
      top: y + height > window.innerHeight - 8 ? Math.max(8, y - height) : y,
    })
    el.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus()
  }, [x, y])
  useEffect(() => {
    window.addEventListener('resize', onClose)
    window.addEventListener('blur', onClose)
    return () => {
      window.removeEventListener('resize', onClose)
      window.removeEventListener('blur', onClose)
    }
  }, [onClose])
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
    e.preventDefault()
    const buttons = [...(root.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])]
    const i = buttons.indexOf(document.activeElement as HTMLButtonElement)
    buttons[e.key === 'ArrowDown' ? Math.min(i + 1, buttons.length - 1) : Math.max(i - 1, 0)]?.focus()
  }
  // A trailing separator (nothing after it) isn't drawn.
  const shown = items.filter((item, i) => item !== 'separator' || i < items.length - 1)
  return (
    <div className="row-menu" role="menu" ref={root} style={position} onKeyDown={onKeyDown}
         onContextMenu={(e) => e.preventDefault()} onClick={(e) => e.stopPropagation()}>
      {shown.map((item, i) => item === 'separator'
        ? <div key={i} className="row-menu-sep" role="separator" />
        : <button key={item.label} type="button" role="menuitem" className="row-menu-item" onClick={item.onClick} disabled={item.disabled}>
            {item.label}
          </button>)}
    </div>
  )
}
