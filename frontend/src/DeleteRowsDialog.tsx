import { useEffect, useState } from 'react'
import { DBService, type DeleteEffects, type EditTarget } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import { errorMessage } from './Banner'
import { Spinner } from './icons'

type Props = {
  target: EditTarget | null // a Postgres table: Go says first what else goes with its rows
  where?: string // otherwise what they're deleted from, e.g. a DynamoDB table
  noun?: [string, string] // what a row is: ['item', 'items'] in DynamoDB
  rows: string[] // each row to delete, as named here: "id = 5", or "row 12"
  onDelete: () => Promise<void> // throws to show the error here
  onStop?: () => void // stops the delete running: it rolls back. None: it can't be stopped
  onClose: () => void
}

const SHOWN = 5 // rows listed by name; the rest are counted

// What deleting from a DynamoDB table or MongoDB collection takes with it: nothing else.
const NO_EFFECTS: DeleteEffects = { database: '', server: '', role: '', cascades: [], setsNull: [], triggers: [], rules: [], blocked: '' }

// Asks before deleting rows, saying first what else goes with them: rows of
// other tables (ON DELETE CASCADE), links set to NULL, triggers. Delete
// stays off until Go has said so. Cancel has the focus, so Enter cancels;
// more rows than are listed must be confirmed by typing how many.
export default function DeleteRowsDialog({ target, where, noun: [one, many] = ['row', 'rows'], rows, onDelete, onStop, onClose }: Props) {
  const [effects, setEffects] = useState<DeleteEffects | null>(target ? null : NO_EFFECTS)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [typed, setTyped] = useState('')

  useEffect(() => {
    if (!target) return
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

  const label = count === 1 ? one : `${count.toLocaleString()} ${many}`
  const list = (names: string[] | null) => (names ?? []).join(', ')
  const cascades = list(effects?.cascades ?? null)
  const setsNull = list(effects?.setsNull ?? null)
  const triggers = list(effects?.triggers ?? null)

  return (
    <div className="palette-backdrop centered" onMouseDown={close}>
      <form className="delete-rows" role="alertdialog" aria-label={`Delete ${label}`}
            onMouseDown={(e) => e.stopPropagation()}
            onSubmit={(e) => { e.preventDefault(); confirm() }}
            onKeyDown={(e) => { if (e.key === 'Escape') { e.preventDefault(); close() } }}>
        <p className="delete-rows-title">
          Delete {count === 1 ? `the whole ${one}` : `${count.toLocaleString()} whole ${many}`} from <strong>{target ? `${target.schema}.${target.table}` : where}</strong>
          {effects?.database && <> on <strong>{effects.database}</strong></>}?
        </p>
        {effects?.server && <p className="delete-rows-where">{effects.server} · as role {effects.role}</p>}
        <ul className="delete-rows-list">
          {rows.slice(0, SHOWN).map((name, i) => <li key={i}>{name}</li>)}
          {count > SHOWN && <li className="delete-rows-more">and {(count - SHOWN).toLocaleString()} more</li>}
        </ul>
        <p className="delete-rows-note">
          {count === 1 ? 'This can’t be undone.' : `This can’t be undone. The ${count.toLocaleString()} are deleted together: if one changed since you loaded it, none is.`}
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
            To confirm, type the number of {many}: <strong>{count}</strong>
            <input className="save-query-input" value={typed} onChange={(e) => setTyped(e.target.value)} disabled={busy}
                   inputMode="numeric" spellCheck={false} autoComplete="off" autoFocus aria-label={`Number of ${many} to delete`} />
          </label>
        )}
        {error && <p className="delete-rows-error">{error}</p>}
        <div className="save-query-actions">
          {busy
            ? onStop && <button type="button" className="btn btn-secondary btn-sm" onClick={onStop}>Stop</button>
            : <button type="button" className="btn btn-secondary btn-sm" onClick={close} autoFocus={!mustType}>Cancel</button>}
          {/* The second click of a double-click (on the menu's Delete…, landing here) doesn't count. */}
          <button type="submit" className="btn btn-danger btn-sm" disabled={!ready}
                  onClick={(e) => { if (e.detail > 1) e.preventDefault() }}>
            {busy ? <><Spinner /> Deleting…</> : `Delete ${label}`}
          </button>
        </div>
      </form>
    </div>
  )
}
