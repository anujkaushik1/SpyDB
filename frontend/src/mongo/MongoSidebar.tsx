import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { Service as Mongo, type CollectionInfo, type ConnectionInfo, type SavedConnection } from '../../bindings/github.com/anujkaushik1/SpyDB/mongodb'
import { errorMessage } from '../Banner'
import { ChevronDownIcon, DatabaseIcon, EyeIcon, PowerIcon, RefreshIcon, SearchIcon, Spinner, TableIcon, TickIcon } from '../icons'
import { isMac } from '../platform'
import { SidebarResizer, useSidebarWidth } from '../resize'
import { compactCount, Highlight } from '../tableMeta'
import { useDismiss } from '../useDismiss'
import { mongoLabel, mongoTarget } from './mongoConnection'

type Props = {
  connection: ConnectionInfo
  collections: CollectionInfo[] | null // null until the first load
  loading: boolean
  error: string | null
  onRefresh: () => void
  activeCollection: string | null // the collection in the active tab
  onOpenCollection: (name: string) => void
  onOpenPalette: () => void // Ctrl+P / ⌘P: go to a collection
  onSwitchDatabase: (name: string) => Promise<void> // throws if it can't
  onSwitchConnection: (connection: SavedConnection) => Promise<void> // likewise
  onDisconnect: () => void
  disconnecting: boolean
}

// MongoDB's sidebar once connected: the database picker on top, the open
// database's collections (with about how many documents each has), a filter,
// and the server at the bottom. Same look as Postgres's; ↑ / ↓ and Enter
// work in the filter.
export default function MongoSidebar(props: Props) {
  const { connection, collections, loading, error, onRefresh, activeCollection, onOpenCollection } = props
  const [filter, setFilter] = useState('')
  const [highlighted, setHighlighted] = useState<string | null>(null)
  const [width, setWidth] = useSidebarWidth()
  const tree = useRef<HTMLDivElement>(null)

  const query = filter.trim().toLowerCase()
  const shown = (collections ?? []).filter((c) => !query || c.name.toLowerCase().includes(query))
  const current = shown.find((c) => c.name === highlighted) ?? (query ? shown[0] : undefined)

  useEffect(() => {
    tree.current?.querySelector('.tree-item.highlighted')?.scrollIntoView({ block: 'nearest' })
  }, [current?.name])

  const navigate = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      if (shown.length === 0) return
      const step = e.key === 'ArrowDown' ? 1 : -1
      const i = current ? shown.indexOf(current) : -1
      const next = i === -1 ? (step === 1 ? 0 : shown.length - 1) : Math.min(Math.max(i + step, 0), shown.length - 1)
      setHighlighted(shown[next].name)
    } else if (e.key === 'Enter' && current) {
      e.preventDefault()
      onOpenCollection(current.name)
    }
  }

  return (
    <aside className="sidebar mongo-sidebar" aria-label="MongoDB collections" style={{ flexBasis: width }}>
      <MongoDatabasePicker connection={connection} onSwitchDatabase={props.onSwitchDatabase} onSwitchConnection={props.onSwitchConnection} />
      <div className="sidebar-header">
        <span className="sidebar-title">
          Collections
          {collections && <span className="sidebar-count">{collections.length}</span>}
        </span>
        <button type="button" className="sidebar-icon-btn" onClick={onRefresh} disabled={loading}
                aria-label="Refresh collections" title="Refresh">
          {loading ? <Spinner /> : <RefreshIcon />}
        </button>
      </div>

      <div className="sidebar-search">
        <SearchIcon />
        <input
          type="text"
          value={filter}
          onChange={(e) => { setFilter(e.target.value); setHighlighted(null) }}
          onKeyDown={(e) => {
            if (e.key === 'Escape') { setFilter(''); setHighlighted(null) } else navigate(e)
          }}
          placeholder="Filter collections"
          aria-label="Filter collections"
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          autoComplete="off"
        />
        <button type="button" className="kbd-hint" onClick={props.onOpenPalette} title={isMac ? 'Go to collection (Ctrl+P or ⌘P)' : 'Go to collection (Ctrl+P)'}>
          <kbd>Ctrl P</kbd>
        </button>
      </div>

      <div className="tree" ref={tree} tabIndex={-1} onKeyDown={(e) => { if (!(e.target as HTMLElement).closest('.link-btn')) navigate(e) }}>
        {error && (
          <p className="tree-message tree-error">
            {error} <button type="button" className="link-btn" onClick={onRefresh}>Retry</button>
          </p>
        )}
        {!error && !collections && <p className="tree-message">Loading…</p>}
        {!error && collections && shown.length === 0 && (
          <p className="tree-message">{query ? `No collections match “${filter.trim()}”` : `No collections in ${connection.database} yet`}</p>
        )}
        {!error && shown.length > 0 && (
          <ul className="tree-items">
            {shown.map((c) => (
              <li key={c.name}>
                <button type="button"
                        className={['tree-item', c.name === activeCollection && 'active', c === current && 'highlighted'].filter(Boolean).join(' ')}
                        onClick={() => {
                          setHighlighted(c.name)
                          onOpenCollection(c.name)
                          if (!tree.current?.contains(document.activeElement)) tree.current?.focus({ preventScroll: true })
                        }}
                        title={`${connection.database}.${c.name} · ${c.kind}${c.count != null ? ` · about ${c.count.toLocaleString()} documents` : ''}`}>
                  <span className={c.kind === 'view' ? 'tree-icon kind-view' : 'tree-icon kind-table'}>{c.kind === 'view' ? <EyeIcon /> : <TableIcon />}</span>
                  <span className="tree-label"><Highlight text={c.name} query={query} /></span>
                  {c.count != null && <span className="tree-rows">~{compactCount(c.count)}</span>}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="sidebar-footer">
        <div className="sidebar-footer-info">
          <p className="sidebar-footer-db"><span className="status-dot" aria-hidden="true" /> {connection.database}</p>
          <p className="sidebar-footer-host" title={`${connection.user ? `${connection.user}@` : ''}${connection.host} · MongoDB ${connection.serverVersion} · ${connection.topology}`}>
            {connection.host.split(',')[0]} · {connection.user || 'no sign-in'} · v{connection.serverVersion}
          </p>
        </div>
        <button type="button" className="sidebar-icon-btn danger" onClick={props.onDisconnect} disabled={props.disconnecting}
                aria-label="Disconnect" title="Disconnect">
          {props.disconnecting ? <Spinner /> : <PowerIcon />}
        </button>
      </div>
      <SidebarResizer width={width} onResize={setWidth} />
    </aside>
  )
}

type Choice = { kind: 'database'; database: string } | { kind: 'connection'; connection: SavedConnection }
const choiceKey = (c: Choice) => (c.kind === 'database' ? `db:${c.database}` : `conn:${c.connection.id}`)

// The switcher at the top: the server's other databases (switching is
// instant: same connection) and saved MongoDB connections to other servers,
// with one search for both. Each database keeps its own tabs.
function MongoDatabasePicker({ connection, onSwitchDatabase, onSwitchConnection }: Pick<Props, 'connection' | 'onSwitchDatabase' | 'onSwitchConnection'>) {
  const [open, setOpen] = useState(false)
  const [databases, setDatabases] = useState<string[] | null>(null)
  const [saved, setSaved] = useState<SavedConnection[]>([])
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState('')
  const [highlight, setHighlight] = useState(0)
  const [switching, setSwitching] = useState<string | null>(null)
  const root = useRef<HTMLDivElement>(null)
  const list = useRef<HTMLUListElement>(null)
  useDismiss(open, root, () => setOpen(false))

  useEffect(() => {
    if (!open) return
    setError(null)
    setFilter('')
    Promise.allSettled([Mongo.ListDatabases(), Mongo.SavedConnections()]).then(([dbs, lists]) => {
      setDatabases(dbs.status === 'fulfilled' ? dbs.value ?? [] : [])
      setSaved(lists.status === 'fulfilled' ? lists.value.saved ?? [] : [])
      const failed = [dbs, lists].find((r) => r.status === 'rejected') as PromiseRejectedResult | undefined
      if (failed) setError(errorMessage(failed.reason))
    })
  }, [open])

  const q = filter.trim().toLowerCase()
  const choices: Choice[] = [
    ...(databases ?? []).filter((d) => !q || d.toLowerCase().includes(q)).map((database): Choice => ({ kind: 'database', database })),
    ...saved.filter((c) => !q || `${c.name} ${mongoLabel(c.config)}`.toLowerCase().includes(q)).map((c): Choice => ({ kind: 'connection', connection: c })),
  ]
  const nDb = choices.filter((c) => c.kind === 'database').length
  const isCurrent = (c: Choice) => {
    if (c.kind === 'database') return c.database === connection.database
    const t = mongoTarget(c.connection.config)
    return t.hosts === connection.host && t.user === connection.user && (t.database || connection.database) === connection.database
  }
  // Typing a name that isn't there offers it: MongoDB makes a database when something is written to it.
  const typedNew = q && !(databases ?? []).some((d) => d.toLowerCase() === q) && !/[/\\. "$]/.test(filter.trim()) ? filter.trim() : null

  useEffect(() => setHighlight(0), [filter, open])
  useEffect(() => {
    list.current?.querySelector('.db-menu-item.highlighted')?.scrollIntoView({ block: 'nearest' })
  }, [highlight])

  const choose = async (choice: Choice) => {
    if (isCurrent(choice)) return setOpen(false)
    setSwitching(choiceKey(choice))
    setError(null)
    try {
      if (choice.kind === 'database') await onSwitchDatabase(choice.database)
      else await onSwitchConnection(choice.connection)
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
    } else if (e.key === 'Enter' && switching === null) {
      e.preventDefault()
      if (choices[highlight]) choose(choices[highlight])
      else if (typedNew) choose({ kind: 'database', database: typedNew })
    }
  }

  const item = (choice: Choice, index: number) => {
    const key = choiceKey(choice)
    const current = isCurrent(choice)
    const classes = ['db-menu-item', choice.kind === 'connection' && 'two-line', current && 'current', index === highlight && 'highlighted']
    return (
      <li key={key}>
        <button type="button" className={classes.filter(Boolean).join(' ')} onClick={() => choose(choice)}
                onMouseMove={() => setHighlight(index)} disabled={switching !== null} aria-current={current}>
          <span className="db-menu-check">{current && <TickIcon />}</span>
          {choice.kind === 'database' ? (
            <span className="db-menu-name">{choice.database}</span>
          ) : (
            <span className="db-menu-conn">
              <span className="db-menu-name">{choice.connection.name}</span>
              <span className="db-menu-detail">{mongoLabel(choice.connection.config)}</span>
            </span>
          )}
          {switching === key && <Spinner />}
        </button>
      </li>
    )
  }

  return (
    <div className="db-picker" ref={root}>
      <button type="button" className="db-picker-button" onClick={() => setOpen((o) => !o)} aria-haspopup="dialog" aria-expanded={open}
              title={`${connection.user ? `${connection.user}@` : ''}${connection.host}/${connection.database} — switch database or connection`}>
        <span className="db-picker-icon"><DatabaseIcon /></span>
        <span className="db-picker-name">{connection.database}</span>
        <span className={open ? 'db-picker-chevron open' : 'db-picker-chevron'}><ChevronDownIcon /></span>
      </button>

      {open && (
        <div className="db-menu" role="dialog" aria-label="Switch database or connection">
          <div className="db-menu-search">
            <SearchIcon />
            <input value={filter} onChange={(e) => setFilter(e.target.value)} onKeyDown={onSearchKeyDown}
                   placeholder="Search, or type a new database’s name" aria-label="Search databases and connections"
                   autoFocus spellCheck={false} autoCapitalize="off" autoCorrect="off" />
          </div>
          <ul className="db-menu-list" ref={list}>
            {databases === null && !error && <li className="db-menu-note">Loading…</li>}
            {nDb > 0 && <li className="db-menu-section" role="presentation">Databases on this server</li>}
            {choices.slice(0, nDb).map((c, i) => item(c, i))}
            {choices.length > nDb && <li className="db-menu-section" role="presentation">Saved connections</li>}
            {choices.slice(nDb).map((c, i) => item(c, nDb + i))}
            {typedNew && choices.length === 0 && (
              <li>
                <button type="button" className="db-menu-item highlighted" onClick={() => choose({ kind: 'database', database: typedNew })}>
                  <span className="db-menu-check" />
                  <span className="db-menu-name">Open “{typedNew}” (new: made when you first write to it)</span>
                </button>
              </li>
            )}
            {databases !== null && choices.length === 0 && !typedNew && <li className="db-menu-note">Nothing to switch to</li>}
          </ul>
          {error && <p className="db-menu-error">{error}</p>}
          <p className="db-menu-footer">Each database keeps its own tabs; switching back brings them back.</p>
        </div>
      )}
    </div>
  )
}
