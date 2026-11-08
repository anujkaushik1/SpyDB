import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent } from 'react'
import type { CopyFormat } from './copyFormats'
import { shortcut } from './platform'
import { useDismiss } from './useDismiss'

export const FORMAT_LABELS: Record<CopyFormat, string> = { json: 'JSON', csv: 'CSV', text: 'Text', markdown: 'Markdown' }

const FORMATS: { format: CopyFormat; hint?: string }[] = [
  { format: 'json' },
  { format: 'csv' },
  { format: 'text', hint: shortcut('C') },
  { format: 'markdown' },
]

type Props = {
  x: number // where the right-click happened, in viewport px
  y: number
  rowCount: number
  onCopyValue?: () => void // given when the click was on a cell
  onOpenJson?: () => void // given when that cell is json / jsonb
  onViewJson: () => void // the selected rows as JSON
  onCopy: (format: CopyFormat) => void
  onClose: () => void
}

// The grid's right-click menu: copy the clicked value, or the selected rows
// in one of the formats.
export default function RowMenu({ x, y, rowCount, onCopyValue, onOpenJson, onViewJson, onCopy, onClose }: Props) {
  const root = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState({ left: x, top: y })
  useDismiss(true, root, onClose)

  // Keep it on screen: open left / upwards when there's no room, and focus
  // the first item so the keyboard works straight away.
  useLayoutEffect(() => {
    const el = root.current
    if (!el) return
    const { width, height } = el.getBoundingClientRect()
    setPosition({
      left: x + width > window.innerWidth - 8 ? Math.max(8, x - width) : x,
      top: y + height > window.innerHeight - 8 ? Math.max(8, y - height) : y,
    })
    el.querySelector('button')?.focus()
  }, [x, y])

  // Scrolling or resizing would leave it floating away from its row.
  useEffect(() => {
    window.addEventListener('wheel', onClose, { passive: true })
    window.addEventListener('resize', onClose)
    window.addEventListener('blur', onClose)
    return () => {
      window.removeEventListener('wheel', onClose)
      window.removeEventListener('resize', onClose)
      window.removeEventListener('blur', onClose)
    }
  }, [onClose])

  // ↑ / ↓ move between items; Enter and Esc are handled by the buttons and useDismiss.
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
    e.preventDefault()
    const items = [...(root.current?.querySelectorAll('button') ?? [])]
    const i = items.indexOf(document.activeElement as HTMLButtonElement)
    const next = e.key === 'ArrowDown' ? Math.min(i + 1, items.length - 1) : Math.max(i - 1, 0)
    items[next]?.focus()
  }

  return (
    <div className="row-menu" role="menu" ref={root} style={position} onKeyDown={onKeyDown}
         onContextMenu={(e) => e.preventDefault()}>
      <button type="button" role="menuitem" className="row-menu-item" onClick={onViewJson}>
        View {rowCount === 1 ? 'row' : `${rowCount.toLocaleString()} rows`} as JSON
      </button>
      {onOpenJson && <button type="button" role="menuitem" className="row-menu-item" onClick={onOpenJson}>Open in JSON editor</button>}
      {onCopyValue && <button type="button" role="menuitem" className="row-menu-item" onClick={onCopyValue}>Copy value</button>}
      <div className="row-menu-sep" role="separator" />
      <p className="row-menu-title">Copy {rowCount.toLocaleString()} row{rowCount === 1 ? '' : 's'} as</p>
      {FORMATS.map(({ format, hint }) => (
        <button key={format} type="button" role="menuitem" className="row-menu-item" onClick={() => onCopy(format)}>
          {FORMAT_LABELS[format]}
          {hint && <kbd>{hint}</kbd>}
        </button>
      ))}
    </div>
  )
}
