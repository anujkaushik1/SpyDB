import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { DBService, type ConnectionInfo, type ConnectionLists, type SavedConnection } from '../bindings/github.com/anujkaushik1/SpyDB'
import { errorMessage } from './Banner'
import { connectionDetail, connectionTitle, pickerSections, sameTarget } from './connectionSearch'
import { ChevronDownIcon, DatabaseIcon, SearchIcon, Spinner, TickIcon } from './icons'
import { useDismiss } from './useDismiss'

type Props = {
  connection: ConnectionInfo
  onSwitchDatabase: (database: string) => Promise<void> // throws if the switch fails
  onSwitchConnection: (connection: SavedConnection, saved: boolean) => Promise<void> // likewise
}

type Choice =
  | { kind: 'database'; database: string }
  | { kind: 'connection'; connection: SavedConnection; saved: boolean }

const choiceKey = (c: Choice) => (c.kind === 'database' ? `db:${c.database}` : `conn:${c.connection.id}`)

// The switcher at the top of the sidebar: other databases on this server
// (DBService.ListDatabases) and saved / recent connections to other servers,
// with one search for both. Picking either reconnects (the old connection
// stays open until the new one works); each connection keeps its own tabs.
export default function DatabasePicker({ connection, onSwitchDatabase, onSwitchConnection }: Props) {
  const [open, setOpen] = useState(false)
  const [databases, setDatabases] = useState<string[] | null>(null)
  const [lists, setLists] = useState<ConnectionLists | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState('')
  const [highlight, setHighlight] = useState(0) // index into choices, moved with ↑ / ↓
  const [switching, setSwitching] = useState<string | null>(null) // choiceKey
  const root = useRef<HTMLDivElement>(null)
  const list = useRef<HTMLUListElement>(null)
  useDismiss(open, root, () => setOpen(false))

  // Reload both lists each time the menu opens; either may have changed.
  useEffect(() => {
    if (!open) return
    setError(null)
    setFilter('')
    Promise.allSettled([DBService.ListDatabases(), DBService.SavedConnections()]).then(([dbs, saved]) => {
      setDatabases(dbs.status === 'fulfilled' ? dbs.value ?? [] : [])
      setLists(saved.status === 'fulfilled' ? saved.value : null)
      const failed = [dbs, saved].find((r) => r.status === 'rejected') as PromiseRejectedResult | undefined
      if (failed) setError(errorMessage(failed.reason))
    })
  }, [open])

  const sections = pickerSections(databases ?? [], lists, filter)
  const choices: Choice[] = [
    ...sections.databases.map((database): Choice => ({ kind: 'database', database })),
    ...sections.saved.map((c): Choice => ({ kind: 'connection', connection: c, saved: true })),
    ...sections.recent.map((c): Choice => ({ kind: 'connection', connection: c, saved: false })),
  ]
  const isCurrent = (c: Choice) =>
    c.kind === 'database' ? c.database === connection.database : sameTarget(c.connection.config, connection)

  useEffect(() => setHighlight(0), [filter, open])
  useEffect(() => {
    list.current?.querySelector('.db-menu-item.highlighted')?.scrollIntoView({ block: 'nearest' })
  }, [highlight])

  const choose = async (choice: Choice) => {
    if (isCurrent(choice)) {
      setOpen(false)
      return
    }
    setSwitching(choiceKey(choice))
    setError(null)
    try {
      if (choice.kind === 'database') await onSwitchDatabase(choice.database)
      else await onSwitchConnection(choice.connection, choice.saved)
      setOpen(false)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSwitching(null)
    }
  }

  const onSearchKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      const step = e.key === 'ArrowDown' ? 1 : -1
      setHighlight((h) => Math.min(Math.max(h + step, 0), Math.max(choices.length - 1, 0)))
    } else if (e.key === 'Enter' && choices[highlight] && switching === null) {
      e.preventDefault()
      choose(choices[highlight])
    }
  }

  const item = (choice: Choice, index: number) => {
    const key = choiceKey(choice)
    const current = isCurrent(choice)
    const classes = ['db-menu-item', choice.kind === 'connection' && 'two-line', current && 'current', index === highlight && 'highlighted']
    return (
      <li key={key}>
        <button type="button" className={classes.filter(Boolean).join(' ')} onClick={() => choose(choice)}
                onMouseMove={() => setHighlight(index)} disabled={switching !== null} aria-current={current}
                title={choice.kind === 'connection' ? `${connectionTitle(choice.connection)} — ${connectionDetail(choice.connection)}` : undefined}>
          <span className="db-menu-check">{current && <TickIcon />}</span>
          {choice.kind === 'database' ? (
            <span className="db-menu-name">{choice.database}</span>
          ) : (
            <span className="db-menu-conn">
              <span className="db-menu-name">{connectionTitle(choice.connection)}</span>
              <span className="db-menu-detail">{connectionDetail(choice.connection)}</span>
            </span>
          )}
          {switching === key && <Spinner />}
        </button>
      </li>
    )
  }

  const nDb = sections.databases.length
  const nSaved = sections.saved.length
  const loading = databases === null && !error

  return (
    <div className="db-picker" ref={root}>
      <button type="button" className="db-picker-button" onClick={() => setOpen((o) => !o)}
              aria-haspopup="dialog" aria-expanded={open}
              title={`${connection.user}@${connection.host}:${connection.port}/${connection.database} — switch database or connection`}>
        <span className="db-picker-icon"><DatabaseIcon /></span>
        <span className="db-picker-name">{connection.database}</span>
        <span className={open ? 'db-picker-chevron open' : 'db-picker-chevron'}><ChevronDownIcon /></span>
      </button>

      {open && (
        <div className="db-menu" role="dialog" aria-label="Switch database or connection">
          <div className="db-menu-search">
            <SearchIcon />
            <input value={filter} onChange={(e) => setFilter(e.target.value)} onKeyDown={onSearchKeyDown}
                   placeholder="Search databases and connections" aria-label="Search databases and connections"
                   autoFocus spellCheck={false} autoCapitalize="off" autoCorrect="off" />
          </div>

          <ul className="db-menu-list" ref={list}>
            {loading && <li className="db-menu-note">Loading…</li>}
            {nDb > 0 && <li className="db-menu-section" role="presentation">Databases on this server</li>}
            {choices.slice(0, nDb).map((c, i) => item(c, i))}
            {nSaved > 0 && <li className="db-menu-section" role="presentation">Saved connections</li>}
            {choices.slice(nDb, nDb + nSaved).map((c, i) => item(c, nDb + i))}
            {sections.recent.length > 0 && <li className="db-menu-section" role="presentation">Recent</li>}
            {choices.slice(nDb + nSaved).map((c, i) => item(c, nDb + nSaved + i))}
            {!loading && choices.length === 0 && (
              <li className="db-menu-note">{filter.trim() ? `Nothing matches “${filter.trim()}”` : 'Nothing to switch to'}</li>
            )}
          </ul>

          {error && <p className="db-menu-error">{error}</p>}
          <p className="db-menu-footer">Each connection keeps its own tabs; switching back brings them back. If a switch fails, you stay connected here.</p>
        </div>
      )}
    </div>
  )
}
