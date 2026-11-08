import { useEffect, useRef, useState } from 'react'
import { DBService } from '../bindings/github.com/anujkaushik1/SpyDB'
import { errorMessage } from './Banner'
import { ChevronDownIcon, DatabaseIcon, SearchIcon, Spinner, TickIcon } from './icons'
import { useDismiss } from './useDismiss'

type Props = {
  current: string
  unsavedTabs: number // tabs with unsaved cell edits; switching closes every tab
  onSwitch: (database: string) => Promise<void> // throws if the switch fails
}

// The database menu at the top of the sidebar: lists the server's databases
// (DBService.ListDatabases) and switches by reconnecting.
export default function DatabasePicker({ current, unsavedTabs, onSwitch }: Props) {
  const [open, setOpen] = useState(false)
  const [databases, setDatabases] = useState<string[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState('')
  const [switching, setSwitching] = useState<string | null>(null)
  const [confirming, setConfirming] = useState<string | null>(null) // waiting for "discard unsaved edits?"
  const root = useRef<HTMLDivElement>(null)
  useDismiss(open, root, () => setOpen(false))

  // Reload the list each time the menu opens; databases may have been added meanwhile.
  useEffect(() => {
    if (!open) return
    setError(null)
    setFilter('')
    setConfirming(null)
    DBService.ListDatabases()
      .then((list) => setDatabases(list ?? []))
      .catch((err) => setError(errorMessage(err)))
  }, [open])

  const choose = async (database: string, confirmed = false) => {
    if (database === current) {
      setOpen(false)
      return
    }
    if (unsavedTabs > 0 && !confirmed) {
      setConfirming(database)
      return
    }
    setSwitching(database)
    setError(null)
    try {
      await onSwitch(database)
      setOpen(false)
    } catch (err) {
      setConfirming(null)
      setError(errorMessage(err))
    } finally {
      setSwitching(null)
    }
  }

  const query = filter.trim().toLowerCase()
  const shown = (databases ?? []).filter((d) => d.toLowerCase().includes(query))

  return (
    <div className="db-picker" ref={root}>
      <button type="button" className="db-picker-button" onClick={() => setOpen((o) => !o)}
              aria-haspopup="dialog" aria-expanded={open} title="Switch database">
        <span className="db-picker-icon"><DatabaseIcon /></span>
        <span className="db-picker-name">{current}</span>
        <span className={open ? 'db-picker-chevron open' : 'db-picker-chevron'}><ChevronDownIcon /></span>
      </button>

      {open && (
        <div className="db-menu" role="dialog" aria-label="Switch database">
          {(databases?.length ?? 0) > 6 && !confirming && (
            <div className="db-menu-search">
              <SearchIcon />
              <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Find a database"
                     aria-label="Find a database" autoFocus spellCheck={false} autoCapitalize="off" autoCorrect="off" />
            </div>
          )}

          {confirming ? (
            <div className="db-menu-confirm">
              <p>
                Switch to <strong>{confirming}</strong>? {unsavedTabs} tab{unsavedTabs === 1 ? ' has' : 's have'} unsaved
                edits. Switching closes all tabs and discards them.
              </p>
              <div className="db-menu-confirm-actions">
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => setConfirming(null)}>Cancel</button>
                <button type="button" className="btn btn-danger btn-sm" onClick={() => choose(confirming, true)} disabled={switching !== null}>
                  {switching ? <><Spinner /> Switching…</> : 'Discard and switch'}
                </button>
              </div>
            </div>
          ) : (
            <ul className="db-menu-list">
              {databases === null && !error && <li className="db-menu-note">Loading…</li>}
              {shown.map((database) => (
                <li key={database}>
                  <button type="button" className={database === current ? 'db-menu-item current' : 'db-menu-item'}
                          onClick={() => choose(database)} disabled={switching !== null} aria-current={database === current}>
                    <span className="db-menu-check">{database === current && <TickIcon />}</span>
                    <span className="db-menu-name">{database}</span>
                    {switching === database && <Spinner />}
                  </button>
                </li>
              ))}
              {databases && shown.length === 0 && <li className="db-menu-note">No databases match “{filter.trim()}”</li>}
            </ul>
          )}

          {error && <p className="db-menu-error">{error}</p>}
          <p className="db-menu-footer">Reconnects to the same server with the same login, and closes open tabs.</p>
        </div>
      )}
    </div>
  )
}
