import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { DBService, type SchemaInfo, type TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB'
import { errorMessage } from './Banner'
import { ChevronIcon, EyeIcon, LayersIcon, RefreshIcon, SearchIcon, Spinner, TableIcon } from './icons'

// 950 → "950", 1530 → "1.5k", 20003 → "20k", 1250000 → "1.3M"
function compactCount(n: number) {
  const short = (value: number, unit: string) => `${value.toFixed(value < 10 ? 1 : 0).replace(/\.0$/, '')}${unit}`
  if (n < 1000) return String(n)
  if (n < 1_000_000) return short(n / 1000, 'k')
  return short(n / 1_000_000, 'M')
}

const kindIcon: Record<string, ReactNode> = {
  'view': <EyeIcon />,
  'materialized view': <LayersIcon />,
}

// Wraps the part of `text` that matches `query` (already lower-case) in <mark>.
function Highlight({ text, query }: { text: string; query: string }) {
  const at = query ? text.toLowerCase().indexOf(query) : -1
  if (at < 0) return <>{text}</>
  return <>{text.slice(0, at)}<mark>{text.slice(at, at + query.length)}</mark>{text.slice(at + query.length)}</>
}

type Props = {
  activeTable: string | null // qualifiedName of the table last opened
  refreshToken: number // changes when the editor creates, alters or drops something
  onOpenTable: (table: TableInfo) => void
}

// Schemas with their tables and views (DBService.ListTables).
export default function SchemaSidebar({ activeTable, refreshToken, onOpenTable }: Props) {
  const [schemas, setSchemas] = useState<SchemaInfo[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState('')
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setSchemas((await DBService.ListTables()) ?? [])
      setError(null)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load, refreshToken])

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
        <button type="button" className="sidebar-icon-btn" onClick={load} disabled={loading}
                aria-label="Refresh tables" title="Refresh">
          {loading ? <Spinner /> : <RefreshIcon />}
        </button>
      </div>

      <label className="sidebar-search">
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
      </label>

      <div className="tree">
        {error && (
          <p className="tree-message tree-error">
            {error} <button type="button" className="link-btn" onClick={load}>Retry</button>
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
                        title={`${schema.name}.${t.name} · ${t.kind}${t.estimatedRows != null ? ` · about ${t.estimatedRows.toLocaleString()} rows` : ''}`}
                      >
                        <span className={`tree-icon kind-${t.kind.replace(' ', '-')}`}>{kindIcon[t.kind] ?? <TableIcon />}</span>
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
    </aside>
  )
}
