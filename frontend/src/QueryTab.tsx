import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import Banner, { errorMessage } from './Banner'
import SaveQueryDialog from './SaveQueryDialog'
import SqlEditor from './SqlEditor'
import { modKey } from './platform'
import { dragY } from './resize'
import { suggestName } from './savedQueries'

type Props = {
  tabId: string
  title: string // the tab's title; the Save dialog falls back to it for a name
  initialSql: string // '' for a new tab, the saved SQL when opened from Saved queries
  linked: boolean // the tab belongs to a saved query, so Save updates it
  database: string // the label a newly saved query gets
  active: boolean // this tab is showing (focus the editor)
  onSchemaChange: () => void
  onDirtyChange: (dirty: boolean) => void
  onSaveQuery: (sql: string, name: string | null) => Promise<void> // name null: update the linked query
  onSqlChange?: (sql: string) => void // the editor's text as typed, for Duplicate
}

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

// What the results area lists before anything has run.
const SHORTCUTS: [string, string[]][] = [
  ['Run (the selection, if any)', [modKey(), '↵']],
  ['Run the statement at the cursor', [modKey(), '⇧', '↵']],
  ['Save query', [modKey(), 'S']],
  ['Autocomplete', ['Ctrl', 'Space']],
  ['Comment / uncomment lines', [modKey(), '/']],
  ['Find & replace', [modKey(), 'F']],
  ['New query tab', [modKey(), 'T']],
  ['Go to table', [modKey(), 'P']],
]

// A Ctrl+T / ⌘T tab, a SQL playground: the editor on top, results below, and
// a divider between them to drag. Results that come straight from one table
// (with its primary key) can be edited in place. Save keeps the SQL as a
// saved query: the first time it asks for a name.
export default function QueryTab({ tabId, title, initialSql, linked, database, active, onSchemaChange, onDirtyChange, onSaveQuery, onSqlChange }: Props) {
  const [dialogSql, setDialogSql] = useState<string | null>(null) // asking for a name
  const [saveState, setSaveState] = useState<'idle' | 'saving' | 'saved'>('idle')
  const [saveError, setSaveError] = useState<string | null>(null)

  useEffect(() => {
    if (saveState !== 'saved') return
    const timer = setTimeout(() => setSaveState('idle'), 1500)
    return () => clearTimeout(timer)
  }, [saveState])

  const save = async (sql: string) => {
    setSaveError(null)
    if (!linked) {
      setDialogSql(sql)
      return
    }
    setSaveState('saving')
    try {
      await onSaveQuery(sql, null)
      setSaveState('saved')
    } catch (err) {
      setSaveState('idle')
      setSaveError(errorMessage(err))
    }
  }

  // The divider: drag, double-click to reset, or ↑ / ↓ when focused.
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
    <>
      <SqlEditor
        tabId={tabId}
        initialSql={initialSql}
        active={active}
        leading={null}
        onSchemaChange={onSchemaChange}
        onRan={() => {}}
        onDirtyChange={onDirtyChange}
        onSqlChange={onSqlChange}
        onSave={save}
        saveState={saveState}
        layout="playground"
      >
        {(sql) => (
          <div className="playground" ref={area}>
            <div className="playground-editor" style={{ flexBasis: `${share * 100}%` }}>{sql.editor}</div>
            <div className="playground-divider" role="separator" aria-orientation="horizontal" aria-label="Resize the editor"
                 aria-valuenow={Math.round(share * 100)} tabIndex={0} title="Drag to resize · double-click to reset"
                 onPointerDown={startResize} onDoubleClick={() => changeShare(DEFAULT_SHARE)} onKeyDown={onDividerKey} />
            <div className="playground-results">
              {saveError && <div className="save-query-banner"><Banner tone="error" title="Couldn’t save the query" message={saveError} /></div>}
              {sql.notice}
              {sql.results ?? (
                <div className="shortcuts" aria-label="Keyboard shortcuts">
                  {SHORTCUTS.map(([label, keys]) => (
                    <div className="shortcut" key={label}>
                      <span className="shortcut-label">{label}</span>
                      <span className="keys">{keys.map((k) => <kbd key={k}>{k}</kbd>)}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        )}
      </SqlEditor>
      {dialogSql !== null && (
        <SaveQueryDialog
          initialName={suggestName(dialogSql, title)}
          database={database}
          onSave={async (name) => {
            await onSaveQuery(dialogSql, name)
            setDialogSql(null)
            setSaveState('saved')
          }}
          onClose={() => setDialogSql(null)}
        />
      )}
    </>
  )
}
