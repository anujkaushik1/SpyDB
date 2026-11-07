import { useState } from 'react'
import type { ConnectionInfo, SchemaInfo, TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB'
import { ActivityIcon, ChevronIcon, PowerIcon, RefreshIcon, SearchIcon, Spinner } from './icons'
import { compactCount, Highlight, KindIcon, kindClass, tableTooltip } from './tableMeta'

type Props = {
  schemas: SchemaInfo[] | null // null until the first load
  loading: boolean
  error: string | null
  onRefresh: () => void
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

  return (
    <aside className="sidebar" aria-label="Tables and views">
      <div className="sidebar-header">
        <span className="sidebar-title">
          Tables &amp; views
          {schemas && <span className="sidebar-count">{total}</span>}
        </span>
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
          onChange={(e) => setFilter(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Escape') setFilter('') }}
          placeholder="Filter tables"
          aria-label="Filter tables"
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          autoComplete="off"
        />
        <button type="button" className="kbd-hint" onClick={onOpenPalette} title="Go to table (Ctrl+P or ⌘P)">
          <kbd>Ctrl P</kbd>
        </button>
      </div>

      <div className="tree">
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
          // Filtering opens every group so matches are visible.
          const open = query !== '' || !collapsed.has(schema.name)
          return (
            <div className="tree-group" key={schema.name}>
              <button type="button" className="tree-schema" onClick={() => toggle(schema.name)} aria-expanded={open}>
                <span className={open ? 'tree-chevron open' : 'tree-chevron'}><ChevronIcon /></span>
                <span className="tree-label">{schema.name}</span>
                <span className="tree-badge">{schema.tables.length}</span>
              </button>

              {open && schema.tables.length === 0 && <p className="tree-empty">No tables</p>}
              {open && schema.tables.length > 0 && (
                <ul className="tree-items">
                  {schema.tables.map((t) => (
                    <li key={t.qualifiedName}>
                      <button
                        type="button"
                        className={t.qualifiedName === activeTable ? 'tree-item active' : 'tree-item'}
                        onClick={() => onOpenTable(t)}
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
    </aside>
  )
}
