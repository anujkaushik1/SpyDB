import { useState } from 'react'
import type { AWSIdentity, Connection } from '../../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import { PowerIcon, RefreshIcon, SearchIcon, Spinner, TableIcon } from '../icons'
import { isMac } from '../platform'
import { SidebarResizer, useSidebarWidth } from '../resize'
import { Highlight } from '../tableMeta'

type Props = {
  connection: Connection
  identity: AWSIdentity // who signed in, for the footer
  tables: string[] | null // null until the first load
  loading: boolean
  error: string | null
  onRefresh: () => void
  activeTable: string | null // the table in the active tab
  onOpenTable: (table: string) => void
  onOpenPalette: () => void // Ctrl+P / ⌘P: go to a table
  onDisconnect: () => void
  disconnecting: boolean
}

// DynamoDB's sidebar once signed in: the region's tables (loaded by App, which
// shares them with the Ctrl+P switcher), a filter, and the profile, region
// and account at the bottom. Same look as the Postgres sidebar, but one flat
// list: DynamoDB has no schemas. Clicking a table opens it; Enter in the
// filter opens the first match.
export default function DynamoSidebar(props: Props) {
  const { connection, identity, tables, loading, error, onRefresh, activeTable, onOpenTable, onDisconnect, disconnecting } = props
  const [filter, setFilter] = useState('')
  const [width, setWidth] = useSidebarWidth()

  const query = filter.trim().toLowerCase()
  const shown = (tables ?? []).filter((name) => !query || name.toLowerCase().includes(query))
  // "arn:aws:iam::624447008677:user/velocis-zfundspro" → "velocis-zfundspro"
  const who = identity.arn.split('/').pop() || identity.arn

  return (
    <aside className="sidebar dynamo-sidebar" aria-label="DynamoDB tables" style={{ flexBasis: width }}>
      <div className="sidebar-header">
        <span className="sidebar-title">
          Tables
          {tables && <span className="sidebar-count">{tables.length}</span>}
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
          onKeyDown={(e) => {
            if (e.key === 'Escape') setFilter('')
            if (e.key === 'Enter' && query && shown.length > 0) onOpenTable(shown[0])
          }}
          placeholder="Filter tables"
          aria-label="Filter tables"
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          autoComplete="off"
        />
        <button type="button" className="kbd-hint" onClick={props.onOpenPalette} title={isMac ? 'Go to table (Ctrl+P or ⌘P)' : 'Go to table (Ctrl+P)'}>
          <kbd>Ctrl P</kbd>
        </button>
      </div>

      <div className="tree">
        {error && (
          <p className="tree-message tree-error">
            {error} <button type="button" className="link-btn" onClick={onRefresh}>Retry</button>
          </p>
        )}
        {!error && !tables && <p className="tree-message">Loading…</p>}
        {!error && tables && shown.length === 0 && (
          <p className="tree-message">{query ? `No tables match “${filter.trim()}”` : `No tables in ${connection.region}`}</p>
        )}
        {!error && shown.length > 0 && (
          <ul className="tree-items">
            {shown.map((name) => (
              <li key={name}>
                <button type="button" className={name === activeTable ? 'tree-item active' : 'tree-item'}
                        onClick={() => onOpenTable(name)} title={name}>
                  <span className="tree-icon kind-table"><TableIcon /></span>
                  <span className="tree-label"><Highlight text={name} query={query} /></span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="sidebar-footer">
        <div className="sidebar-footer-info">
          <p className="sidebar-footer-db"><span className="status-dot" aria-hidden="true" /> {connection.profile}</p>
          <p className="sidebar-footer-host" title={`${identity.arn} · ${connection.region}`}>
            {connection.region} · {who} · {identity.account}
          </p>
        </div>
        <button type="button" className="sidebar-icon-btn danger" onClick={onDisconnect} disabled={disconnecting}
                aria-label="Disconnect" title="Disconnect">
          {disconnecting ? <Spinner /> : <PowerIcon />}
        </button>
      </div>
      <SidebarResizer width={width} onResize={setWidth} />
    </aside>
  )
}
