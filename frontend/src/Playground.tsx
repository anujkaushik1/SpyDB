import { useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react'
import { dragY } from './resize'

// The editor's share of the tab's height, remembered for new tabs.
const SHARE_KEY = 'spydb.editorShare'
const DEFAULT_SHARE = 0.45
const clampShare = (share: number) => Math.min(0.85, Math.max(0.15, share))
function storedShare() {
  try {
    const n = Number(localStorage.getItem(SHARE_KEY))
    return n ? clampShare(n) : DEFAULT_SHARE
  } catch {
    return DEFAULT_SHARE
  }
}
function storeShare(share: number) {
  try {
    localStorage.setItem(SHARE_KEY, String(share))
  } catch {
    // Storage unavailable: new tabs start at the default.
  }
}

type Props = {
  editor: ReactNode
  results: ReactNode // what's below the divider
}

// A query tab's layout (Postgres's SQL, DynamoDB's PartiQL): the editor on
// top, results below, and a divider between them: drag, double-click to
// reset, or ↑ / ↓ when focused.
export default function Playground({ editor, results }: Props) {
  const [share, setShare] = useState(storedShare)
  const area = useRef<HTMLDivElement>(null)
  const changeShare = (next: number) => {
    const clamped = clampShare(next)
    setShare(clamped)
    storeShare(clamped)
  }
  const startResize = (e: PointerEvent<HTMLDivElement>) => {
    const height = area.current?.getBoundingClientRect().height ?? 1
    const start = share
    dragY(e, (dy) => changeShare(start + dy / height))
  }
  const onDividerKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return
    e.preventDefault()
    changeShare(share + (e.key === 'ArrowDown' ? 0.05 : -0.05))
  }
  return (
    <div className="playground" ref={area}>
      <div className="playground-editor" style={{ flexBasis: `${share * 100}%` }}>{editor}</div>
      <div className="playground-divider" role="separator" aria-orientation="horizontal" aria-label="Resize the editor"
           aria-valuenow={Math.round(share * 100)} tabIndex={0} title="Drag to resize · double-click to reset"
           onPointerDown={startResize} onDoubleClick={() => changeShare(DEFAULT_SHARE)} onKeyDown={onDividerKey} />
      <div className="playground-results">{results}</div>
    </div>
  )
}

// What a query tab's results area lists before anything has run.
export function Shortcuts({ list }: { list: [string, string[]][] }) {
  return (
    <div className="shortcuts" aria-label="Keyboard shortcuts">
      {list.map(([label, keys]) => (
        <div className="shortcut" key={label}>
          <span className="shortcut-label">{label}</span>
          <span className="keys">{keys.map((k) => <kbd key={k}>{k}</kbd>)}</span>
        </div>
      ))}
    </div>
  )
}
