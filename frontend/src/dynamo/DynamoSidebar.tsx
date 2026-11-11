import { useCallback, useEffect, useState } from 'react'
import { Service as Dynamo, type AWSIdentity, type Connection } from '../../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import { errorMessage } from '../Banner'
import { PowerIcon, RefreshIcon, SearchIcon, Spinner, TableIcon } from '../icons'
import { SidebarResizer, useSidebarWidth } from '../resize'
import { Highlight } from '../tableMeta'

type Props = {
  connection: Connection
  identity: AWSIdentity // who signed in, for the footer
  onDisconnect: () => void
  disconnecting: boolean
}

// DynamoDB's sidebar once signed in: the region's tables (dynamo.Service.ListTables),
// a filter, and the profile, region and account at the bottom. Same look as
// the Postgres sidebar, but one flat list: DynamoDB has no schemas.
export default function DynamoSidebar({ connection, identity, onDisconnect, disconnecting }: Props) {
  const [tables, setTables] = useState<string[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState('')
  const [width, setWidth] = useSidebarWidth()

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setTables((await Dynamo.ListTables()) ?? [])
      setError(null)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

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
        <button type="button" className="sidebar-icon-btn" onClick={load} disabled={loading}
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
      </div>

      <div className="tree">
        {error && (
          <p className="tree-message tree-error">
            {error} <button type="button" className="link-btn" onClick={load}>Retry</button>
          </p>
        )}
        {!error && !tables && <p className="tree-message">Loading…</p>}
        {!error && tables && shown.length === 0 && (
          <p className="tree-message">{query ? `No tables match “${filter.trim()}”` : `No tables in ${connection.region}`}</p>
        )}
        {!error && shown.length > 0 && (
          <ul className="tree-items">
            {shown.map((name) => (
              <li key={name} className="tree-item" title={name}>
                <span className="tree-icon kind-table"><TableIcon /></span>
                <span className="tree-label"><Highlight text={name} query={query} /></span>
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
