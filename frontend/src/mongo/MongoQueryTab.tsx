import { useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { modKey } from '../platform'
import { dragY } from '../resize'
import MongoQueryEditor from './MongoQueryEditor'

type Props = {
  tabId: string
  initialText: string
  active: boolean // this tab is showing (focus the editor)
  collections: string[]
  onDirtyChange: (dirty: boolean) => void
  onTextChange?: (text: string) => void // for Duplicate
  onRan: () => void // a write may have made or emptied collections
}

// The editor's share of the tab's height (Postgres's query tabs keep theirs the same way).
const DEFAULT_SHARE = 0.4
const clampShare = (share: number) => Math.min(0.85, Math.max(0.15, share))

// What the results area lists before anything has run.
const SHORTCUTS: [string, string[]][] = [
  ['Run (the selection, or the command at the cursor)', [modKey(), '↵']],
  ['Run the command at the cursor', [modKey(), '⇧', '↵']],
  ['Suggestions', ['Ctrl', 'Space']],
  ['Find & replace', [modKey(), 'F']],
  ['New query tab', [modKey(), 'T']],
  ['Go to collection', [modKey(), 'P']],
]

const EXAMPLES = [
  'db.orders.find({status: "PAID"}).sort({_id: -1}).limit(20)',
  'db.orders.aggregate([{$group: {_id: "$status", n: {$sum: 1}}}])',
  'db.orders.updateMany({status: "NEW"}, {$set: {status: "OPEN"}})',
]

// A Ctrl+T / ⌘T tab for MongoDB: the query editor on top, results below,
// with a divider between them to drag.
export default function MongoQueryTab({ tabId, initialText, active, collections, onDirtyChange, onTextChange, onRan }: Props) {
  const [share, setShare] = useState(DEFAULT_SHARE)
  const area = useRef<HTMLDivElement>(null)
  const startResize = (e: PointerEvent<HTMLDivElement>) => {
    const height = area.current?.getBoundingClientRect().height ?? 1
    const start = share
    dragY(e, (dy) => setShare(clampShare(start + dy / height)))
  }
  const onDividerKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'ArrowUp' && e.key !== 'ArrowDown') return
    e.preventDefault()
    setShare((s) => clampShare(s + (e.key === 'ArrowDown' ? 0.05 : -0.05)))
  }

  return (
    <MongoQueryEditor tabId={tabId} initialText={initialText} active={active} leading={null} layout="playground"
                      collections={collections} onDirtyChange={onDirtyChange} onTextChange={onTextChange}
                      onRan={(result) => { if (result.documents === null) onRan() }}>
      {(parts) => (
        <div className="playground" ref={area}>
          <div className="playground-editor" style={{ flexBasis: `${share * 100}%` }}>{parts.editor}</div>
          <div className="playground-divider" role="separator" aria-orientation="horizontal" aria-label="Resize the editor"
               aria-valuenow={Math.round(share * 100)} tabIndex={0} title="Drag to resize · double-click to reset"
               onPointerDown={startResize} onDoubleClick={() => setShare(DEFAULT_SHARE)} onKeyDown={onDividerKey} />
          <div className="playground-results">
            {parts.notice}
            {parts.results ?? (
              <div className="shortcuts" aria-label="Keyboard shortcuts">
                {SHORTCUTS.map(([label, keys]) => (
                  <div className="shortcut" key={label}>
                    <span className="shortcut-label">{label}</span>
                    <span className="keys">{keys.map((k) => <kbd key={k}>{k}</kbd>)}</span>
                  </div>
                ))}
                <p className="mongo-examples-title">For example</p>
                {EXAMPLES.map((e) => <code className="mongo-example" key={e}>{e}</code>)}
              </div>
            )}
          </div>
        </div>
      )}
    </MongoQueryEditor>
  )
}
