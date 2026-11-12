import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { DBService, type TableFilter, type TableInfo, type TablePage } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import Banner, { errorDetail, errorMessage } from './Banner'
import DataGrid, { type GridColumn, type GridForeignKey, type Sort } from './DataGrid'
import ExportMenu, { type ExportChoice } from './ExportMenu'
import ImportDialog from './ImportDialog'
import SqlEditor from './SqlEditor'
import { ChevronIcon, ChevronLeftIcon, CodeIcon, PlusIcon, RefreshIcon, SearchIcon, Spinner, UploadIcon, XIcon } from './icons'

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

const emptyFilter = (key: number): DraftFilter => ({ key, column: '', operator: 'eq', value: '' })

// Filters to show the tab with, e.g. from following a foreign key; a new seq
// applies them again (the tab may already be open).
export type FilterPreset = { filters: TableFilter[]; seq: number }

// Where a foreign key value points: the table and the filters that find its row.
export type FollowTarget = { schema: string; table: string; qualifiedName: string; filters: TableFilter[] }

type Props = {
  table: TableInfo
  tabId: string
  preset?: FilterPreset
  onSchemaChange: () => void
  onDirtyChange: (dirty: boolean) => void // unsaved cell edits in this tab
  onFollow: (target: FollowTarget) => void
}

// One table or view: rows a page at a time with sorting and filters
// (DBService.TableData). Clicking </> swaps the filter bar for a SQL editor;
// the rows stay below until a query returns rows of its own.
export default function TableTab({ table, tabId, preset, onSchemaChange, onDirtyChange, onFollow }: Props) {
  const [sqlMode, setSqlMode] = useState(false)
  // Unsaved edits in the table's rows and in query results, shown as a dot on the tab.
  const [rowsDirty, setRowsDirty] = useState(false)
  const [resultsDirty, setResultsDirty] = useState(false)
  useEffect(() => onDirtyChange(rowsDirty || resultsDirty), [rowsDirty || resultsDirty])
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
      onDirtyChange={setResultsDirty}
    >
      {(sql) => (
        <TableView
          table={table}
          preset={preset}
          onFollow={onFollow}
          onImported={onSchemaChange}
          toggle={<SqlModeToggle on={false} onToggle={toggleSql} />}
          reloadSignal={reloadSignal}
          top={sqlMode ? sql.editor : null}
          notice={sqlMode ? sql.notice : null}
          results={sql.results}
          showResults={sqlMode && sql.results !== null}
          onDirtyChange={setRowsDirty}
        />
      )}
    </SqlEditor>
  )
}

type TableViewProps = {
  table: TableInfo
  preset?: FilterPreset
  onFollow: (target: FollowTarget) => void
  onImported: () => void // rows were imported: refresh the sidebar's counts
  toggle: ReactNode // the </> button, at the start of the filter bar
  reloadSignal: number // a change reloads the rows
  top: ReactNode // replaces the filter bar (the SQL editor), or null
  notice: ReactNode // a strip above the grid, or null
  results: ReactNode // query results, or null
  showResults: boolean // results take the rows' place (both stay mounted, so edits survive)
  onDirtyChange: (dirty: boolean) => void
}

function TableView({ table, preset, onFollow, onImported, toggle, reloadSignal, top, notice, results, showResults, onDirtyChange }: TableViewProps) {
  const [importOpen, setImportOpen] = useState(false)
  const [page, setPage] = useState(0)
  const [sort, setSort] = useState<Sort>(null)
  const nextKey = useRef(1)
  const draftOf = (filters: TableFilter[]) =>
    filters.length > 0 ? filters.map((f) => ({ ...f, key: nextKey.current++ })) : [emptyFilter(nextKey.current++)]
  const [draft, setDraft] = useState<DraftFilter[]>(() => draftOf(preset?.filters ?? []))
  const [applied, setApplied] = useState<TableFilter[]>(preset?.filters ?? [])
  const [foreignKeys, setForeignKeys] = useState<GridForeignKey[]>([])
  const [reloadToken, setReloadToken] = useState(0)
  const [data, setData] = useState<TablePage | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<{ message: string; detail?: string } | null>(null)
  const [pageInput, setPageInput] = useState('1')
  const grid = useRef<HTMLDivElement>(null)

  // A foreign key followed into this tab while it was open: show its row.
  const presetSeq = useRef(preset?.seq)
  useEffect(() => {
    if (!preset || preset.seq === presetSeq.current) return
    presetSeq.current = preset.seq
    setDraft(draftOf(preset.filters))
    setApplied(preset.filters)
    setPage(0)
  }, [preset])

  // The table's foreign keys, once (and again after SQL ran here, which may
  // have changed them), not per page. Without them the grid just has no links.
  useEffect(() => {
    let current = true
    const call = DBService.ForeignKeys(table.schema, table.name)
    call
      .then((keys) => {
        if (current) setForeignKeys((keys ?? []).map((k) => ({ ...k, columns: k.columns ?? [], refColumns: k.refColumns ?? [] })))
      })
      .catch(() => {
        if (current) setForeignKeys([])
      })
    return () => {
      current = false
      call.cancel()
    }
  }, [table.schema, table.name, reloadSignal])

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
  // A new array only when the data changes; the grid resets its selection on new rows.
  const gridRows = useMemo(() => (data?.rows ?? []).map((r) => r ?? []), [data])
  const gridKeys = useMemo(() => (data?.keys ?? []).map((k) => k ?? []), [data])
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

  // Cells save by primary key, or for a table without one by row address
  // (data.target, data.keys); views and the like are read-only.
  const target = data?.target ?? null
  const readOnlyReason = data && !target ? data.readOnly : null
  const gridColumns: GridColumn[] = columns.map((c) => ({ ...c, source: c.name }))

  // Export: every page, in the order shown. Full table ignores the filters.
  const exportOrder = { schema: table.schema, table: table.name, sortBy: sort?.column ?? '', sortDesc: sort?.desc ?? false }
  const exportChoices: ExportChoice[] = [
    ...(filtered ? [{
      label: `Filtered rows (${applied.length} filter${applied.length > 1 ? 's' : ''})`,
      note: 'Every row that matches, not just this page',
      fileName: `${table.name}_filtered`,
      run: (path: string, format: string) => DBService.ExportTable({ ...exportOrder, filters: applied }, path, format),
    }] : []),
    {
      label: 'Full table',
      note: table.estimatedRows != null ? `About ${table.estimatedRows.toLocaleString()} rows${sort ? `, sorted by ${sort.column}` : ''}` : undefined,
      fileName: table.name,
      run: (path: string, format: string) => DBService.ExportTable({ ...exportOrder, filters: [] }, path, format),
    },
  ]

  const estimate = !filtered ? table.estimatedRows : null
  const totalPages = estimate != null ? Math.max(1, Math.ceil(estimate / PAGE_SIZE)) : null
  const first = page * PAGE_SIZE + 1

  // Enter in any field of a filter (the column and operator lists too) applies them.
  const filterBar = (
    <div className="filter-bar" onKeyDown={(e) => {
      if (e.key === 'Enter' && (e.target as HTMLElement).matches('select, input')) {
        e.preventDefault()
        applyFilters()
      }
    }}>
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
    <div className="rows-pane" hidden={showResults}>
      {error && (
        <div className="table-error">
          <Banner tone="error" title="Couldn’t load rows" message={error.message} detail={error.detail} />
        </div>
      )}

      <DataGrid
        columns={gridColumns}
        rows={gridRows}
        firstRowNumber={first}
        target={target}
        rowKeys={gridKeys}
        readOnlyReason={readOnlyReason ?? undefined}
        onSaved={() => setReloadToken((t) => t + 1)}
        canDelete
        onDirtyChange={onDirtyChange}
        sort={sort}
        onSort={toggleSort}
        foreignKeys={foreignKeys}
        onFollow={(key, values) => onFollow({
          schema: key.refSchema,
          table: key.refTable,
          qualifiedName: key.refQualifiedName,
          filters: key.refColumns.map((column, k) => ({ column, operator: 'eq', value: values[k] })),
        })}
        wrapClass={loading && data ? 'grid-wrap loading' : 'grid-wrap'}
        wrapRef={grid}
      >
        {!loading && data && rows.length === 0 && (
          <p className="grid-empty">
            {page > 0 ? `No rows on page ${page + 1}.` : filtered ? 'No rows match these filters.' : 'This table is empty.'}
          </p>
        )}
        {loading && !data && <p className="grid-empty">Loading…</p>}
      </DataGrid>

      <footer className="grid-footer">
        <span className="grid-status">
          {filtered
            ? `${applied.length} filter${applied.length > 1 ? 's' : ''}`
            : estimate != null ? `~${estimate.toLocaleString()} rows` : table.kind}
          {rows.length > 0 && ` · rows ${first.toLocaleString()}–${(first + rows.length - 1).toLocaleString()}`}
          {sort && ` · sorted by ${sort.column} ${sort.desc ? '↓' : '↑'}`}
          {data && ` · ${data.durationMs} ms`}
          {readOnlyReason && ` · read-only: ${readOnlyReason}`}
          {target?.kind === 'ctid' && ' · no primary key: rows are matched by address'}
        </span>
        <div className="grid-pager">
          {(table.kind === 'table' || table.kind === 'partitioned table') && (
            <button type="button" className="btn btn-secondary btn-sm export-btn" onClick={() => setImportOpen(true)}>
              <UploadIcon /> Import
            </button>
          )}
          {columns.length > 0 && <ExportMenu choices={exportChoices} />}
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
      {importOpen && (
        <ImportDialog mode="existing" schema={table.schema} table={table.name} schemas={[table.schema]}
                      onClose={() => setImportOpen(false)}
                      onImported={() => { setReloadToken((t) => t + 1); onImported() }} />
      )}
    </div>
  )

  return (
    <div className="table-view">
      {top ?? filterBar}
      {notice}
      {rowsView}
      {results && <div className="results-pane" hidden={!showResults}>{results}</div>}
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
