import { useCallback, useEffect, useState } from 'react'
import { DBService, type DBConfig, type ServerConnections } from '../bindings/github.com/anujkaushik1/SpyDB'
import Banner, { errorDetail, errorMessage } from './Banner'
import { RefreshIcon, Spinner } from './icons'

// 45 → "45s", 192 → "3m 12s", 7500 → "2h 5m", 273600 → "3d 4h"
function formatDuration(seconds: number) {
  if (seconds < 60) return `${seconds}s`
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m}m ${seconds % 60}s`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  return `${Math.floor(h / 24)}d ${h % 24}h`
}

// "idle in transaction" holds locks while doing nothing, so it gets a warning colour.
function stateClass(state: string) {
  if (state === 'active') return 'state state-active'
  if (state.startsWith('idle in transaction')) return 'state state-warn'
  return 'state state-idle'
}

type Props = {
  config: DBConfig
  // True when not connected: each load opens and closes its own connection.
  temporary: boolean
}

// Lists the server's client connections (pg_stat_activity). Loads when shown.
export default function ServerConnectionsPanel({ config, temporary }: Props) {
  const [data, setData] = useState<ServerConnections | null>(null)
  const [error, setError] = useState<{ message: string; detail?: string } | null>(null)
  const [loading, setLoading] = useState(true)
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setData(await DBService.ActiveConnections(config))
      setError(null)
      setUpdatedAt(new Date())
    } catch (err) {
      setError({ message: errorMessage(err), detail: errorDetail(err) })
    } finally {
      setLoading(false)
    }
  }, [config])

  useEffect(() => { load() }, [load])

  const rows = data?.connections ?? []
  // e.g. { active: 1, idle: 3 }
  const byState: Record<string, number> = {}
  for (const c of rows) byState[c.state || 'unknown'] = (byState[c.state || 'unknown'] ?? 0) + 1

  return (
    <section className="panel" id="server-connections" aria-labelledby="server-connections-title">
      <header className="panel-header">
        <div>
          <h2 className="panel-title" id="server-connections-title">Active connections</h2>
          <p className="panel-subtitle">
            {data ? (
              <>
                <strong>{rows.length}</strong> of {data.maxConnections} allowed
                {Object.entries(byState).map(([state, n]) => <span key={state}> · {n} {state}</span>)}
              </>
            ) : loading ? 'Loading…' : 'Not loaded'}
            {temporary && <span className="muted"> · via a temporary connection</span>}
          </p>
        </div>
        <div className="panel-actions">
          {updatedAt && <span className="hint">Updated {updatedAt.toLocaleTimeString()}</span>}
          <button type="button" className="btn btn-secondary btn-sm" onClick={load} disabled={loading}>
            {loading ? <Spinner /> : <RefreshIcon />} Refresh
          </button>
        </div>
      </header>

      {error && <Banner tone="error" title="Couldn’t load connections" message={error.message} detail={error.detail} />}

      {data && (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>PID</th>
                <th>User</th>
                <th>Database</th>
                <th>Client</th>
                <th>Application</th>
                <th>State</th>
                <th>Connected</th>
                <th>Last query</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((c) => (
                <tr key={c.pid}>
                  <td className="mono num">{c.pid}</td>
                  <td>{c.user || '—'}</td>
                  <td>{c.database || '—'}</td>
                  <td className="mono">{c.client || '—'}</td>
                  <td>{c.application === 'SpyDB' ? <span className="tag">SpyDB</span> : c.application || '—'}</td>
                  <td><span className={stateClass(c.state)}>{c.state || '—'}</span></td>
                  <td className="num">{formatDuration(c.connectedSeconds)}</td>
                  <td className="mono query" title={c.query}>
                    {c.isCurrent ? <span className="muted">(this list)</span> : c.query || '—'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
