import { useEffect, useRef, useState, type ReactNode } from 'react'
import { DBService, type TableFilter, type TableInfo, type TablePage } from '../bindings/github.com/anujkaushik1/SpyDB'
import Banner, { errorDetail, errorMessage } from './Banner'
import SqlEditor from './SqlEditor'
import {
  ArrowDownIcon, ArrowUpIcon, ChevronIcon, ChevronLeftIcon, CodeIcon, KeyIcon, PlusIcon, RefreshIcon, SearchIcon, Spinner, XIcon,
} from './icons'
import { cellClass, cellText } from './tableMeta'

const PAGE_SIZE = 100

// Keys match filterOperators in tabledata.go.
const OPERATORS = [
  { value: 'eq', label: 'equals' },
  { value: 'neq', label: 'not equal' },
  { value: 'contains', label: 'contains' },
  { value: 'starts', label: 'starts with' },
  { value: 'gt', label: '>' },
  { value: 'gte', label: '≥' },
  { value: 'lt', label: '<' },
  { value: 'lte', label: '≤' },
  { value: 'is_null', label: 'is null' },
  { value: 'is_not_null', label: 'is not null' },
]
const NO_VALUE = new Set(['is_null', 'is_not_null'])

type DraftFilter = TableFilter & { key: number }
type Sort = { column: string; desc: boolean } | null

const emptyFilter = (key: number): DraftFilter => ({ key, column: '', operator: 'eq', value: '' })

type Props = {
  table: TableInfo
  tabId: string
  onSchemaChange: () => void
}

// One table or view: rows a page at a time with sorting and filters
// (DBService.TableData). Clicking </> swaps the filter bar for a SQL editor;
// the rows stay below until a query returns rows of its own.
export default function TableTab({ table, tabId, onSchemaChange }: Props) {
  const [sqlMode, setSqlMode] = useState(false)
  // Bumped to reload the rows: after a query ran, and when leaving the editor.
  const [reloadSignal, setReloadSignal] = useState(0)
  const reload = () => setReloadSignal((n) => n + 1)

  const toggleSql = () => {
    if (sqlMode) reload()
    setSqlMode((on) => !on)
  }

  return (
    <SqlEditor
      tabId={tabId}
      initialSql={`SELECT * FROM ${table.qualifiedName};`}
      active={sqlMode}
      leading={<SqlModeToggle on onToggle={toggleSql} />}
      onSchemaChange={onSchemaChange}
      onRan={reload}
    >
      {(sql) => (
        <TableView
          table={table}
          toggle={<SqlModeToggle on={false} onToggle={toggleSql} />}
          reloadSignal={reloadSignal}
          top={sqlMode ? sql.editor : null}
          notice={sqlMode ? sql.notice : null}
          results={sqlMode ? sql.results : null}
        />
      )}
    </SqlEditor>
  )
}

type TableViewProps = {
  table: TableInfo
  toggle: ReactNode // the </> button, at the start of the filter bar
  reloadSignal: number // a change reloads the rows
  top: ReactNode // replaces the filter bar (the SQL editor), or null
  notice: ReactNode // a strip above the grid, or null
  results: ReactNode // replaces the rows and their footer (query results), or null
}

function TableView({ table, toggle, reloadSignal, top, notice, results }: TableViewProps) {
  const [page, setPage] = useState(0)
  const [sort, setSort] = useState<Sort>(null)
  const [draft, setDraft] = useState<DraftFilter[]>([emptyFilter(1)])
  const [applied, setApplied] = useState<TableFilter[]>([])
  const [reloadToken, setReloadToken] = useState(0)
  const [data, setData] = useState<TablePage | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<{ message: string; detail?: string } | null>(null)
  const [pageInput, setPageInput] = useState('1')
  const nextKey = useRef(2)
  const grid = useRef<HTMLDivElement>(null)

  // Reload when the page, sort or applied filters change. Closing the tab or
  // asking again cancels the request still running.
  useEffect(() => {
    let current = true
    setLoading(true)
    const call = DBService.TableData({
      schema: table.schema,
      table: table.name,
      page,
      pageSize: PAGE_SIZE,
      sortBy: sort?.column ?? '',
      sortDesc: sort?.desc ?? false,
      filters: applied,
    })
    call
      .then((result) => {
        if (!current) return
        setData(result)
        setError(null)
        grid.current?.scrollTo({ top: 0 })
      })
      .catch((err) => {
        if (current) setError({ message: errorMessage(err), detail: errorDetail(err) })
      })
      .finally(() => {
        if (current) setLoading(false)
      })
    return () => {
      current = false
      call.cancel()
    }
  }, [table.schema, table.name, page, sort, applied, reloadToken, reloadSignal])

  useEffect(() => setPageInput(String(page + 1)), [page])

  const columns = data?.columns ?? []
  const rows = data?.rows ?? []
  const filtered = applied.length > 0

  // Click a header: ascending → descending → back to primary-key order.
  const toggleSort = (column: string) => {
    setSort((s) => (s?.column !== column ? { column, desc: false } : !s.desc ? { column, desc: true } : null))
    setPage(0)
  }

  const updateDraft = (key: number, change: Partial<TableFilter>) =>
    setDraft((d) => d.map((f) => (f.key === key ? { ...f, ...change } : f)))

  // Filters with no value (other than "is null") are ignored.
  const applyFilters = () => {
    const ready = draft
      .map((f) => ({ column: f.column || columns[0]?.name || '', operator: f.operator, value: f.value }))
      .filter((f) => f.column && (NO_VALUE.has(f.operator) || f.value !== ''))
    setApplied(ready)
    setPage(0)
  }

  const clearFilters = () => {
    setDraft([emptyFilter(nextKey.current++)])
    setApplied([])
    setPage(0)
  }

  const goToPage = () => {
    const n = Number.parseInt(pageInput, 10)
    if (Number.isFinite(n) && n >= 1) setPage(n - 1)
    else setPageInput(String(page + 1))
  }

  const estimate = !filtered ? table.estimatedRows : null
  const totalPages = estimate != null ? Math.max(1, Math.ceil(estimate / PAGE_SIZE)) : null
  const first = page * PAGE_SIZE + 1

  const filterBar = (
    <div className="filter-bar">
      {draft.map((f, i) => (
        <div className="filter-row" key={f.key}>
          {i === 0 ? toggle : <span className="filter-indent" aria-hidden="true" />}
          <select className="select select-column" aria-label="Filter column" value={f.column || columns[0]?.name || ''}
                  onChange={(e) => updateDraft(f.key, { column: e.target.value })}>
            {columns.map((c) => <option key={c.name} value={c.name}>{c.name}</option>)}
          </select>
          <select className="select select-operator" aria-label="Filter operator" value={f.operator}
                  onChange={(e) => updateDraft(f.key, { operator: e.target.value })}>
            {OPERATORS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
          </select>
          <input
            className="input filter-value"
            aria-label="Filter value"
            value={NO_VALUE.has(f.operator) ? '' : f.value}
            disabled={NO_VALUE.has(f.operator)}
            placeholder={NO_VALUE.has(f.operator) ? '' : 'Enter value'}
            onChange={(e) => updateDraft(f.key, { value: e.target.value })}
            onKeyDown={(e) => { if (e.key === 'Enter') applyFilters() }}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            autoComplete="off"
          />
          {draft.length > 1 && (
            <button type="button" className="sidebar-icon-btn" aria-label="Remove filter" title="Remove filter"
                    onClick={() => setDraft((d) => d.filter((x) => x.key !== f.key))}>
              <XIcon />
            </button>
          )}
          {i === 0 && (
            <>
              <button type="button" className="sidebar-icon-btn" aria-label="Add filter" title="Add filter (combined with AND)"
                      onClick={() => setDraft((d) => [...d, emptyFilter(nextKey.current++)])}>
                <PlusIcon />
              </button>
              <button type="button" className="btn btn-primary btn-sm" onClick={applyFilters}>
                <SearchIcon /> Apply
              </button>
              {filtered && (
                <button type="button" className="btn btn-secondary btn-sm" onClick={clearFilters}>Clear</button>
              )}
            </>
          )}
        </div>
      ))}
    </div>
  )

  // The table's rows and their footer; query results take their place.
  const rowsView = (
    <>
      {error && (
        <div className="table-error">
          <Banner tone="error" title="Couldn’t load rows" message={error.message} detail={error.detail} />
        </div>
      )}

      <div className={loading && data ? 'grid-wrap loading' : 'grid-wrap'} ref={grid}>
        {columns.length > 0 && (
          <table className="grid">
            <thead>
              <tr>
                <th className="grid-rownum" aria-label="Row number" />
                {columns.map((c) => {
                  const sorted = sort?.column === c.name ? (sort.desc ? 'desc' : 'asc') : null
                  return (
                    <th key={c.name} aria-sort={sorted === 'asc' ? 'ascending' : sorted === 'desc' ? 'descending' : 'none'}>
                      <button type="button" className="grid-head" onClick={() => toggleSort(c.name)}
                              title={`${c.name} · ${c.type}${c.primaryKey ? ' · primary key' : ''} — click to sort`}>
                        {c.primaryKey && <span className="grid-key" aria-label="Primary key"><KeyIcon /></span>}
                        <span className="grid-col-name">{c.name}</span>
                        <span className="grid-col-type">{c.type}</span>
                        <span className={sorted ? 'grid-sort active' : 'grid-sort'}>
                          {sorted === 'desc' ? <ArrowDownIcon /> : <ArrowUpIcon />}
                        </span>
                      </button>
                    </th>
                  )
                })}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={i}>
                  <td className="grid-rownum">{first + i}</td>
                  {(row ?? []).map((value, j) => (
                    <td key={j} className={cellClass(value, columns[j].category)} title={value ?? 'NULL'}>
                      {value === null ? 'NULL' : cellText(value, columns[j].category)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {!loading && data && rows.length === 0 && (
          <p className="grid-empty">
            {page > 0 ? `No rows on page ${page + 1}.` : filtered ? 'No rows match these filters.' : 'This table is empty.'}
          </p>
        )}
        {loading && !data && <p className="grid-empty">Loading…</p>}
      </div>

      <footer className="grid-footer">
        <span className="grid-status">
          {filtered
            ? `${applied.length} filter${applied.length > 1 ? 's' : ''}`
            : estimate != null ? `~${estimate.toLocaleString()} rows` : table.kind}
          {rows.length > 0 && ` · rows ${first.toLocaleString()}–${(first + rows.length - 1).toLocaleString()}`}
          {sort && ` · sorted by ${sort.column} ${sort.desc ? '↓' : '↑'}`}
          {data && ` · ${data.durationMs} ms`}
        </span>
        <div className="grid-pager">
          <button type="button" className="sidebar-icon-btn" onClick={() => setReloadToken((t) => t + 1)}
                  disabled={loading} aria-label="Reload rows" title="Reload">
            {loading ? <Spinner /> : <RefreshIcon />}
          </button>
          <button type="button" className="pager-btn" onClick={() => setPage((p) => p - 1)}
                  disabled={loading || page === 0} aria-label="Previous page"><ChevronLeftIcon /></button>
          <input
            className="page-input"
            aria-label="Page"
            value={pageInput}
            inputMode="numeric"
            onChange={(e) => setPageInput(e.target.value.replace(/\D/g, ''))}
            onKeyDown={(e) => { if (e.key === 'Enter') goToPage() }}
            onBlur={goToPage}
          />
          {totalPages != null && <span className="grid-page-total">of ~{totalPages.toLocaleString()}</span>}
          <button type="button" className="pager-btn" onClick={() => setPage((p) => p + 1)}
                  disabled={loading || !data?.hasMore} aria-label="Next page"><ChevronIcon /></button>
        </div>
      </footer>
    </>
  )

  return (
    <div className="table-view">
      {top ?? filterBar}
      {notice}
      {results ?? rowsView}
    </div>
  )
}

// The </> button: switch between the table view and the SQL editor.
function SqlModeToggle({ on, onToggle }: { on: boolean; onToggle: () => void }) {
  return (
    <button type="button" className={on ? 'sql-toggle on' : 'sql-toggle'} onClick={onToggle} aria-pressed={on}
            title={on ? 'Back to the table view' : 'Write a SQL query'}>
      <CodeIcon />
    </button>
  )
}
