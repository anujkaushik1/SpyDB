import { useEffect, useState } from 'react'
import { DBService, type DeleteEffects, type EditTarget } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import { errorMessage } from './Banner'
import { Spinner } from './icons'

type Props = {
  target: EditTarget
  rows: string[] // each row to delete, as named here: "id = 5", or "row 12"
  onDelete: () => Promise<void> // throws to show the error here
  onStop: () => void // stops the delete running: it rolls back
  onClose: () => void
}

const SHOWN = 5 // rows listed by name; the rest are counted

// Asks before deleting rows, saying first what else goes with them: rows of
// other tables (ON DELETE CASCADE), links set to NULL, triggers. Delete
// stays off until Go has said so. Cancel has the focus, so Enter cancels;
// more rows than are listed must be confirmed by typing how many.
export default function DeleteRowsDialog({ target, rows, onDelete, onStop, onClose }: Props) {
  const [effects, setEffects] = useState<DeleteEffects | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [typed, setTyped] = useState('')

  useEffect(() => {
    let live = true // a cancelled check (the dialog closed) reports nothing
    const call = DBService.DeleteEffects(target)
    call.then(
      (e) => { if (live) setEffects(e) },
      (err) => { if (live) setError(`Couldn’t check what else this would delete: ${errorMessage(err)}`) })
    return () => { live = false; call.cancel() }
  }, [target])

  const count = rows.length
  const mustType = count > SHOWN
  const ready = !busy && !!effects && !effects.blocked && (!mustType || typed.trim() === String(count))

  const close = () => { if (!busy) onClose() }
  const confirm = async () => {
    if (!ready) return
    setBusy(true)
    setError(null)
    try {
      await onDelete()
    } catch (err) {
      setError(errorMessage(err))
      setBusy(false)
    }
  }

  const noun = count === 1 ? 'row' : `${count.toLocaleString()} rows`
  const list = (names: string[] | null) => (names ?? []).join(', ')
  const cascades = list(effects?.cascades ?? null)
  const setsNull = list(effects?.setsNull ?? null)
  const triggers = list(effects?.triggers ?? null)

  return (
    <div className="palette-backdrop centered" onMouseDown={close}>
      <form className="delete-rows" role="alertdialog" aria-label={`Delete ${noun}`}
            onMouseDown={(e) => e.stopPropagation()}
            onSubmit={(e) => { e.preventDefault(); confirm() }}
            onKeyDown={(e) => { if (e.key === 'Escape') { e.preventDefault(); close() } }}>
        <p className="delete-rows-title">
          Delete {noun} from <strong>{target.schema}.{target.table}</strong>
          {effects && <> on <strong>{effects.database}</strong></>}?
        </p>
        {effects && <p className="delete-rows-where">{effects.server} · as role {effects.role}</p>}
        <ul className="delete-rows-list">
          {rows.slice(0, SHOWN).map((name, i) => <li key={i}>{name}</li>)}
          {count > SHOWN && <li className="delete-rows-more">and {(count - SHOWN).toLocaleString()} more</li>}
        </ul>
        <p className="delete-rows-note">
          This can’t be undone. All of them are deleted, or none: if one changed since you loaded it, nothing is deleted.
        </p>
        {!effects && !error && <p className="delete-rows-note"><Spinner /> Checking what else this deletes…</p>}
        {cascades && (
          <p className="delete-rows-warn">
            Rows in <strong>{cascades}</strong> that point to {count === 1 ? 'it' : 'them'} are deleted too (ON DELETE CASCADE).
          </p>
        )}
        {setsNull && (
          <p className="delete-rows-warn">
            Rows in <strong>{setsNull}</strong> that point to {count === 1 ? 'it' : 'them'} lose that link (ON DELETE SET NULL / SET DEFAULT).
          </p>
        )}
        {triggers && <p className="delete-rows-note">Delete triggers on this table run too: {triggers}.</p>}
        {effects?.blocked && <p className="delete-rows-error">{effects.blocked}</p>}
        {mustType && (
          <label className="delete-rows-type">
            To confirm, type the number of rows: <strong>{count}</strong>
            <input className="save-query-input" value={typed} onChange={(e) => setTyped(e.target.value)} disabled={busy}
                   inputMode="numeric" spellCheck={false} autoComplete="off" autoFocus aria-label="Number of rows to delete" />
          </label>
        )}
        {error && <p className="delete-rows-error">{error}</p>}
        <div className="save-query-actions">
          {busy
            ? <button type="button" className="btn btn-secondary btn-sm" onClick={onStop}>Stop</button>
            : <button type="button" className="btn btn-secondary btn-sm" onClick={close} autoFocus={!mustType}>Cancel</button>}
          {/* The second click of a double-click (on the menu's Delete…, landing here) doesn't count. */}
          <button type="submit" className="btn btn-danger btn-sm" disabled={!ready}
                  onClick={(e) => { if (e.detail > 1) e.preventDefault() }}>
            {busy ? <><Spinner /> Deleting…</> : `Delete ${noun}`}
          </button>
        </div>
      </form>
    </div>
  )
}
