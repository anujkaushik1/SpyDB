import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { DBService, type QueryResult, type StatementResult } from '../bindings/github.com/anujkaushik1/SpyDB'
import Banner, { errorMessage } from './Banner'
import DataGrid, { type GridColumn } from './DataGrid'
import ExportMenu, { type ExportChoice } from './ExportMenu'
import { ChevronIcon, ChevronLeftIcon, PlayIcon, Spinner } from './icons'
import { typeCategory } from './tableMeta'

type QueryFailure = { message: string; hint?: string; detail?: string; line?: number; column?: number }

type Outcome =
  | { kind: 'idle' }
  | { kind: 'running' }
  | { kind: 'done'; result: QueryResult }
  | { kind: 'error'; error: QueryFailure }
  | { kind: 'cancelled' }

// Go's QueryError arrives as err.cause.
type QueryErrorCause = Partial<{ hint: string; detail: string; position: number }>

// Postgres reports errors at a 1-based character position; turn it into line/column.
function lineAndColumn(sql: string, position: number) {
  const lines = sql.slice(0, position - 1).split('\n')
  return { line: lines.length, column: lines[lines.length - 1].length + 1 }
}

// CREATE / ALTER / DROP change what the sidebar should list. Checked on the SQL
// text, since CREATE TABLE AS and CREATE MATERIALIZED VIEW report "SELECT n".
// A false match (e.g. the word in a string) only costs an extra sidebar reload.
const changesSchema = (sql: string) => /\b(create|alter|drop)\b/i.test(sql)

// What the table tab places on screen: the editor replaces the filter bar,
// `notice` is a strip above the grid, and `results` (when not null) replaces
// the table's rows with the query's.
export type SqlParts = { editor: ReactNode; notice: ReactNode; results: ReactNode }

type Props = {
  tabId: string // keeps this tab's rows apart from other tabs' in Go
  initialSql: string
  active: boolean // the editor is showing (focus it when it appears)
  leading: ReactNode // the </> toggle, at the start of the editor bar
  onSchemaChange: () => void
  onRan: () => void // a run finished without error; the table's rows may have changed
  onDirtyChange?: (dirty: boolean) => void // unsaved edits in the results
  children: (parts: SqlParts) => ReactNode
}

// Runs any SQL on the connection opened by Connect (DBService.RunQuery). It
// keeps its text and last result while the tab is open; the table tab decides
// where the pieces go.
export default function SqlEditor({ tabId, initialSql, active, leading, onSchemaChange, onRan, onDirtyChange, children }: Props) {
  const [sql, setSql] = useState(initialSql)
  const [hasSelection, setHasSelection] = useState(false)
  const [outcome, setOutcome] = useState<Outcome>({ kind: 'idle' })
  const textarea = useRef<HTMLTextAreaElement>(null)
  const runningCall = useRef<{ cancel: () => void } | null>(null)
  // Each run gets a number; replies for an older run are ignored.
  const runSeq = useRef(0)
  const cancelledRun = useRef(0)
  // Which result grids have unsaved edits; a new run replaces them all.
  const dirtyResults = useRef(new Set<number>())
  const setResultDirty = (index: number, dirty: boolean) => {
    if (dirty) dirtyResults.current.add(index)
    else dirtyResults.current.delete(index)
    onDirtyChange?.(dirtyResults.current.size > 0)
  }

  // Focus the editor, cursor at the end, whenever it appears.
  useEffect(() => {
    if (!active || !textarea.current) return
    const el = textarea.current
    el.focus()
    el.setSelectionRange(el.value.length, el.value.length)
  }, [active])

  // Closing the tab (or disconnecting) stops its query and frees its rows in Go.
  useEffect(() => () => {
    runningCall.current?.cancel()
    DBService.ForgetRun(tabId)
  }, [tabId])

  const running = outcome.kind === 'running'

  // Runs `text`, which starts at `offset` within `fullSql` (for error positions).
  const execute = async (text: string, offset: number, fullSql: string) => {
    if (!text.trim()) return
    const seq = ++runSeq.current
    dirtyResults.current.clear()
    onDirtyChange?.(false)
    setOutcome({ kind: 'running' })
    const call = DBService.RunQuery(tabId, text)
    runningCall.current = call
    try {
      const result = await call
      if (seq !== runSeq.current) return
      setOutcome({ kind: 'done', result })
      onRan()
      if (changesSchema(text)) onSchemaChange()
    } catch (err) {
      if (seq !== runSeq.current) return
      if (cancelledRun.current === seq) {
        setOutcome({ kind: 'cancelled' })
        return
      }
      const cause: QueryErrorCause = (err as { cause?: QueryErrorCause }).cause ?? {}
      const failure: QueryFailure = { message: errorMessage(err), hint: cause.hint || undefined, detail: cause.detail || undefined }
      if (cause.position) {
        const position = offset + cause.position
        Object.assign(failure, lineAndColumn(fullSql, position))
        // Put the cursor where Postgres found the error.
        textarea.current?.focus()
        textarea.current?.setSelectionRange(position - 1, position - 1)
      }
      setOutcome({ kind: 'error', error: failure })
    } finally {
      if (seq === runSeq.current) runningCall.current = null
    }
  }

  // Run / ⌘↵: with text selected, run only the selection.
  const run = () => {
    const el = textarea.current
    if (!el || running) return
    const { selectionStart: start, selectionEnd: end } = el
    if (start !== end) execute(sql.slice(start, end), start, sql)
    else execute(sql, 0, sql)
  }

  const cancel = () => {
    cancelledRun.current = runSeq.current
    runningCall.current?.cancel() // Wails cancels the Go context; pgx asks Postgres to stop
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault()
      run()
    } else if (e.key === 'Escape' && running) {
      e.preventDefault()
      cancel()
    }
  }

  const editor = (
    <div className="sql-bar">
      {leading}
      <div className="sql-bar-main">
        <textarea
          ref={textarea}
          id={`sql-${tabId}`}
          className="sql-input"
          aria-label="SQL query"
          value={sql}
          onChange={(e) => setSql(e.target.value)}
          onKeyDown={onKeyDown}
          onSelect={(e) => setHasSelection(e.currentTarget.selectionStart !== e.currentTarget.selectionEnd)}
          rows={3}
          spellCheck={false}
          autoCapitalize="off"
          autoCorrect="off"
          autoComplete="off"
        />
        <p className="sql-bar-hint">
          {running ? 'Esc to cancel' : '⌘↵ to run · select text to run part of it · runs on the shared connection, so SET and transactions carry over'}
        </p>
      </div>
      {running ? (
        <button type="button" className="btn btn-danger btn-sm" onClick={cancel}><Spinner /> Cancel</button>
      ) : (
        <button type="button" className="btn btn-primary btn-sm" onClick={run} disabled={!sql.trim()}>
          <PlayIcon /> {hasSelection ? 'Run selection' : 'Run'}
        </button>
      )}
    </div>
  )

  const showRows = () => setOutcome({ kind: 'idle' })
  let notice: ReactNode = null
  let results: ReactNode = null

  switch (outcome.kind) {
    case 'running':
      notice = <p className="tab-notice"><Spinner /> Running…</p>
      break
    case 'cancelled':
      notice = (
        <div className="tab-notice-banner">
          <Banner tone="info" title="Query cancelled" message="Postgres stopped the query. The connection is still open." />
        </div>
      )
      break
    case 'error': {
      const { message, hint, detail, line, column } = outcome.error
      const where = line ? ` Line ${line}, column ${column}.` : ''
      notice = (
        <div className="tab-notice-banner">
          <Banner tone="error" title="Query failed" message={`${message}${where}${hint ? ` Hint: ${hint}` : ''}`} detail={detail} />
        </div>
      )
      break
    }
    case 'done': {
      const statements = outcome.result.results ?? []
      const { runId, pageSize, durationMs } = outcome.result
      const withRows = statements.filter((r) => (r.columns ?? []).length > 0)
      if (withRows.length === 0) {
        // INSERT / UPDATE / CREATE …: say what happened; the table's rows stay below.
        notice = (
          <p className="tab-notice ok">
            {statements.length === 0 ? 'Nothing to run.' : statements.map((r) => `✓ ${r.command}`).join('  ·  ')}
            <span className="tab-notice-meta"> · {durationMs} ms</span>
          </p>
        )
        break
      }
      notice = (
        <p className="tab-notice">
          Query results{statements.length > 1 ? ` · ${statements.length} statements` : ''}
          <span className="tab-notice-meta"> · {durationMs} ms</span>
          <button type="button" className="link-btn tab-notice-action" onClick={showRows}>Show table rows</button>
        </p>
      )
      // Keyed by run, so each new run starts back on page 1.
      results = statements.length === 1 ? (
        <StatementOutput key={runId} tabId={tabId} runId={runId} index={0} pageSize={pageSize} result={statements[0]} bleed
                         onDirtyChange={setResultDirty} />
      ) : (
        <div className="sql-results-stack">
          {statements.map((r, i) => (
            <StatementOutput key={`${runId}-${i}`} tabId={tabId} runId={runId} index={i} pageSize={pageSize} result={r}
                             onDirtyChange={setResultDirty} />
          ))}
        </div>
      )
      break
    }
  }

  return <>{children({ editor, notice, results })}</>
}

type StatementProps = {
  tabId: string
  runId: number
  index: number // which statement of the run
  pageSize: number
  result: StatementResult
  bleed?: boolean // the only result: fill the grid area, edge to edge, like the table's rows
  onDirtyChange: (index: number, dirty: boolean) => void
}

function StatementOutput({ tabId, runId, index, pageSize, result, bleed, onDirtyChange }: StatementProps) {
  const columns = result.columns ?? []
  // A result straight from one table with its primary key can be edited (result.target).
  const gridColumns: GridColumn[] = columns.map((c) => ({
    name: c.name, type: c.type, category: typeCategory(c.type), primaryKey: c.primaryKey, source: c.source,
  }))
  const [page, setPage] = useState(0)
  const [rows, setRows] = useState(result.rows ?? [])
  const gridRows = useMemo(() => rows.map((r) => r ?? []), [rows])
  const [loading, setLoading] = useState(false)
  const [pageError, setPageError] = useState<string | null>(null)
  const tableWrap = useRef<HTMLDivElement>(null)

  const pages = Math.ceil(result.storedRows / pageSize)
  const offset = page * pageSize

  // Export: exactly the rows this query returned; or the whole table they came from.
  const target = result.target
  const exportChoices: ExportChoice[] = [
    {
      label: `These ${result.rowCount.toLocaleString()} row${result.rowCount === 1 ? '' : 's'}`,
      note: result.rowCount > result.storedRows
        ? 'More rows than SpyDB keeps: the query runs again, read-only, and streams into the file'
        : 'Exactly what the query returned',
      fileName: target?.table ?? 'query',
      run: (path: string, format: string) => DBService.ExportResult(tabId, runId, index, path, format),
    },
    ...(target ? [{
      label: `Full table (${target.schema}.${target.table})`,
      note: 'Every row of the table the result came from',
      fileName: target.table,
      run: (path: string, format: string) =>
        DBService.ExportTable({ schema: target.schema, table: target.table, sortBy: '', sortDesc: false, filters: [] }, path, format),
    }] : []),
  ]

  // Later pages come from rows Go already holds (DBService.FetchRows).
  const goToPage = async (next: number) => {
    setLoading(true)
    setPageError(null)
    try {
      setRows((await DBService.FetchRows(tabId, runId, index, next * pageSize)) ?? [])
      setPage(next)
      tableWrap.current?.scrollTo({ top: 0 })
    } catch (err) {
      setPageError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }

  // INSERT, UPDATE, CREATE TABLE, ...: no rows, just the command tag.
  if (columns.length === 0) {
    return <p className="command-tag">✓ {result.command}</p>
  }

  return (
    <div className={bleed ? 'statement bleed' : 'statement'}>
      <DataGrid
        columns={gridColumns}
        rows={gridRows}
        firstRowNumber={offset + 1}
        target={result.target ?? null}
        keepSavedValues
        onDirtyChange={(dirty) => onDirtyChange(index, dirty)}
        wrapClass={bleed ? 'grid-wrap' : 'result-box'}
        wrapRef={tableWrap}
      >
        {rows.length === 0 && <p className="grid-empty">The query returned no rows.</p>}
      </DataGrid>
      <div className={bleed ? 'grid-footer' : 'result-footer'}>
        <span className={bleed ? 'grid-status' : 'result-meta'}>
          {pages > 1
            ? `Rows ${(offset + 1).toLocaleString()}–${(offset + rows.length).toLocaleString()} of ${result.rowCount.toLocaleString()}`
            : result.rowCount === 1 ? '1 row' : `${result.rowCount.toLocaleString()} rows`}
          {result.rowCount > result.storedRows && ` · only the first ${result.storedRows.toLocaleString()} can be paged`}
          {result.target ? ` · editable (${result.target.schema}.${result.target.table})` : ' · read-only'}
          {pageError && <span className="page-error"> · {pageError}</span>}
        </span>
        <div className="grid-pager">
          <ExportMenu choices={exportChoices} />
          {pages > 1 && (
            <>
              {loading && <Spinner />}
              <button type="button" className="pager-btn" onClick={() => goToPage(page - 1)}
                      disabled={loading || page === 0} aria-label="Previous page"><ChevronLeftIcon /></button>
              <span className="grid-page-total">{page + 1} / {pages}</span>
              <button type="button" className="pager-btn" onClick={() => goToPage(page + 1)}
                      disabled={loading || page >= pages - 1} aria-label="Next page"><ChevronIcon /></button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
