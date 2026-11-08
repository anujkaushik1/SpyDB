import { useEffect, useRef, useState, type FormEvent } from 'react'
import { errorMessage } from './Banner'
import { Spinner } from './icons'

type Props = {
  initialName: string // suggested from the SQL's first line
  database: string // the label the query gets
  onSave: (name: string) => Promise<void> // throws to show the error here
  onClose: () => void
}

// Asks for a name the first time a query tab is saved.
export default function SaveQueryDialog({ initialName, database, onSave, onClose }: Props) {
  const [name, setName] = useState(initialName)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => input.current?.select(), [])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!name.trim() || busy) return
    setBusy(true)
    setError(null)
    try {
      await onSave(name.trim())
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <form className="save-query" role="dialog" aria-label="Save query" onSubmit={submit}
            onMouseDown={(e) => e.stopPropagation()}
            onKeyDown={(e) => { if (e.key === 'Escape') { e.preventDefault(); onClose() } }}>
        <p className="save-query-title">Save query</p>
        <label className="save-query-label" htmlFor="save-query-name">Name</label>
        <input id="save-query-name" ref={input} className="save-query-input" value={name} onChange={(e) => setName(e.target.value)}
               autoFocus spellCheck={false} autoComplete="off" />
        <p className="save-query-note">
          Saved queries show under the <strong>&lt;/&gt;</strong> icon on the left, on every connection, labelled
          {database ? <> <strong>{database}</strong></> : ' with no database'}.
        </p>
        {error && <p className="save-query-error">{error}</p>}
        <div className="save-query-actions">
          <button type="button" className="btn btn-secondary btn-sm" onClick={onClose}>Cancel</button>
          <button type="submit" className="btn btn-primary btn-sm" disabled={!name.trim() || busy}>
            {busy ? <><Spinner /> Saving…</> : 'Save'}
          </button>
        </div>
      </form>
    </div>
  )
}
