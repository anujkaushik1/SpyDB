import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import type { ConnectionInfo, SchemaInfo, TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB'
import { ActivityIcon, ChevronIcon, PowerIcon, RefreshIcon, SearchIcon, Spinner, UploadIcon } from './icons'
import { SidebarResizer, useSidebarWidth } from './resize'
import { compactCount, Highlight, KindIcon, kindClass, tableTooltip } from './tableMeta'
import { isMac } from './platform'

type Props = {
  databasePicker: ReactNode // shown at the very top
  content?: ReactNode // another view (Saved queries) in place of the tables list
  schemas: SchemaInfo[] | null // null until the first load
  loading: boolean
  error: string | null
  onRefresh: () => void
  onImportFile: () => void // import a CSV / Excel file as a new table
  activeTable: string | null // qualifiedName of the table in the active tab
  onOpenTable: (table: TableInfo) => void
  onOpenPalette: () => void
  connection: ConnectionInfo
  onShowConnections: () => void
  onDisconnect: () => void
  disconnecting: boolean
}

// Schemas with their tables and views, plus the connection strip at the bottom.
export default function SchemaSidebar(props: Props) {
  const { schemas, loading, error, onRefresh, activeTable, onOpenTable, onOpenPalette, connection } = props
  const [filter, setFilter] = useState('')
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [highlighted, setHighlighted] = useState<string | null>(null) // qualifiedName picked with ↑/↓ or a click
  const tree = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useSidebarWidth()

  const toggle = (schema: string) => setCollapsed((current) => {
    const next = new Set(current)
    if (next.has(schema)) next.delete(schema)
    else next.add(schema)
    return next
  })

  const query = filter.trim().toLowerCase()
  const total = (schemas ?? []).reduce((n, s) => n + (s.tables ?? []).length, 0)
  // While filtering, keep only matching tables and hide schemas with none.
  const groups = (schemas ?? [])
    .map((s) => ({ name: s.name, tables: (s.tables ?? []).filter((t) => !query || t.name.toLowerCase().includes(query)) }))
    .filter((s) => !query || s.tables.length > 0)
  const isOpen = (schema: string) => query !== '' || !collapsed.has(schema) // filtering opens every group
  const visible = groups.flatMap((g) => (isOpen(g.name) ? g.tables : []))

  // The table ↑/↓ and Enter act on. While filtering it defaults to the first
  // match, so typing a name and pressing Enter opens it.
  const current = visible.find((t) => t.qualifiedName === highlighted) ?? (query ? visible[0] : undefined)

  useEffect(() => {
    tree.current?.querySelector('.tree-item.highlighted')?.scrollIntoView({ block: 'nearest' })
  }, [current?.qualifiedName])

  const changeFilter = (value: string) => {
    setFilter(value)
    setHighlighted(null)
  }

  const open = (table: TableInfo) => {
    setHighlighted(table.qualifiedName)
    onOpenTable(table)
  }

  // ↑/↓ move through the visible tables, Enter opens the highlighted one.
  // Returns true when the key was used.
  const navigate = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      if (visible.length === 0) return true
      const step = e.key === 'ArrowDown' ? 1 : -1
      const i = current ? visible.indexOf(current) : -1
      const next = i === -1 ? (step === 1 ? 0 : visible.length - 1) : Math.min(Math.max(i + step, 0), visible.length - 1)
      setHighlighted(visible[next].qualifiedName)
      return true
    }
    if (e.key === 'Enter' && current) {
      e.preventDefault()
      open(current)
      return true
    }
    return false
  }

  const onTreeKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    // Schema headers and Retry keep their own Enter.
    if ((e.target as HTMLElement).closest('.tree-schema, .link-btn')) return
    // Move focus off a clicked button onto the list, so its focus ring
    // doesn't sit on a different row from the highlight.
    if (navigate(e) && e.target !== tree.current) tree.current?.focus({ preventScroll: true })
  }

  return (
    <aside className="sidebar" aria-label={props.content ? 'Saved queries' : 'Tables and views'} style={{ flexBasis: width }}>
      {props.databasePicker}
      {props.content ?? (<>
      <div className="sidebar-header">
        <span className="sidebar-title">
          Tables &amp; views
          {schemas && <span className="sidebar-count">{total}</span>}
        </span>
        <button type="button" className="sidebar-icon-btn sidebar-import-btn" onClick={props.onImportFile}
                aria-label="Import a file as a new table" title="Import a CSV or Excel file as a new table">
          <UploadIcon />
        </button>
        <button type="button" className="sidebar-icon-btn" onClick={onRefresh} disabled={loading}
                aria-label="Refresh tables" title="Refresh">
          {loading ? <Spinner /> : <RefreshIcon />}
        </button>
      </div>

      <div className="sidebar-search">
        <SearchIcon />
        <input
          type="text"
          value={filter}
          onChange={(e) => changeFilter(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Escape') changeFilter('')
            else navigate(e)
          }}
          placeholder="Filter tables"
          aria-label="Filter tables"
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          autoComplete="off"
        />
        <button type="button" className="kbd-hint" onClick={onOpenPalette} title={isMac ? 'Go to table (Ctrl+P or ⌘P)' : 'Go to table (Ctrl+P)'}>
          <kbd>Ctrl P</kbd>
        </button>
      </div>

      <div className="tree" ref={tree} tabIndex={-1} onKeyDown={onTreeKeyDown}>
        {error && (
          <p className="tree-message tree-error">
            {error} <button type="button" className="link-btn" onClick={onRefresh}>Retry</button>
          </p>
        )}
        {!error && !schemas && <p className="tree-message">Loading…</p>}
        {!error && schemas && groups.length === 0 && (
          <p className="tree-message">{query ? `No tables match “${filter.trim()}”` : 'No schemas'}</p>
        )}

        {groups.map((schema) => {
          const expanded = isOpen(schema.name)
          return (
            <div className="tree-group" key={schema.name}>
              <button type="button" className="tree-schema" onClick={() => toggle(schema.name)} aria-expanded={expanded}>
                <span className={expanded ? 'tree-chevron open' : 'tree-chevron'}><ChevronIcon /></span>
                <span className="tree-label">{schema.name}</span>
                <span className="tree-badge">{schema.tables.length}</span>
              </button>

              {expanded && schema.tables.length === 0 && <p className="tree-empty">No tables</p>}
              {expanded && schema.tables.length > 0 && (
                <ul className="tree-items">
                  {schema.tables.map((t) => (
                    <li key={t.qualifiedName}>
                      <button
                        type="button"
                        className={['tree-item', t.qualifiedName === activeTable && 'active', t === current && 'highlighted'].filter(Boolean).join(' ')}
                        onClick={() => {
                          open(t)
                          // WebKit doesn't focus a button on click; focus the list so ↑/↓ work next.
                          if (!tree.current?.contains(document.activeElement)) tree.current?.focus({ preventScroll: true })
                        }}
                        onFocus={() => setHighlighted(t.qualifiedName)}
                        title={tableTooltip(t)}
                      >
                        <span className={`tree-icon ${kindClass(t.kind)}`}><KindIcon kind={t.kind} /></span>
                        <span className="tree-label"><Highlight text={t.name} query={query} /></span>
                        {t.estimatedRows != null && <span className="tree-rows">~{compactCount(t.estimatedRows)}</span>}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )
        })}
      </div>
      </>)}

      <div className="sidebar-footer">
        <div className="sidebar-footer-info">
          <p className="sidebar-footer-db"><span className="status-dot" aria-hidden="true" /> {connection.database}</p>
          <p className="sidebar-footer-host" title={`${connection.user}@${connection.host}:${connection.port} · PostgreSQL ${connection.serverVersion}`}>
            {connection.host}:{connection.port} · PG {connection.serverVersion.split(' ')[0]}
          </p>
        </div>
        <button type="button" className="sidebar-icon-btn" onClick={props.onShowConnections}
                aria-label="Active connections" title="Active connections">
          <ActivityIcon />
        </button>
        <button type="button" className="sidebar-icon-btn danger" onClick={props.onDisconnect} disabled={props.disconnecting}
                aria-label="Disconnect" title="Disconnect">
          {props.disconnecting ? <Spinner /> : <PowerIcon />}
        </button>
      </div>
      <SidebarResizer width={width} onResize={setWidth} />
    </aside>
  )
}
