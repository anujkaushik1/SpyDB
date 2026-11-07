import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { DBService, type QueryResult, type StatementResult } from '../bindings/github.com/anujkaushik1/SpyDB'
import Banner, { errorMessage } from './Banner'
import { PlayIcon, Spinner } from './icons'

// The query text survives app restarts (dev mode restarts on every Go change).
const STORAGE_KEY = 'spydb.query'
const loadSavedQuery = () => {
  try { return localStorage.getItem(STORAGE_KEY) ?? '' } catch { return '' }
}
const saveQuery = (sql: string) => {
  try { localStorage.setItem(STORAGE_KEY, sql) } catch { /* storage unavailable */ }
}

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

// A query to load into the editor and run, e.g. from a sidebar click. `seq`
// changes on every request, so clicking the same table twice runs it again.
export type QueryRequest = { sql: string; seq: number }

// CREATE / ALTER / DROP change what the sidebar should list. Checked on the SQL
// text, since CREATE TABLE AS and CREATE MATERIALIZED VIEW report "SELECT n".
// A false match (e.g. the word in a string) only costs an extra sidebar reload.
const changesSchema = (sql: string) => /\b(create|alter|drop)\b/i.test(sql)

type Props = {
  request: QueryRequest | null
  onSchemaChange: () => void
}

// Runs SQL on the connection opened by Connect (see DBService.RunQuery).
export default function QueryEditor({ request, onSchemaChange }: Props) {
  const [sql, setSql] = useState(loadSavedQuery)
  const [hasSelection, setHasSelection] = useState(false)
  const [outcome, setOutcome] = useState<Outcome>({ kind: 'idle' })
  const textarea = useRef<HTMLTextAreaElement>(null)
  const runningCall = useRef<{ cancel: () => void } | null>(null)
  // Each run gets a number; replies for an older run are ignored.
  const runSeq = useRef(0)
  const cancelledRun = useRef(0)

  useEffect(() => { textarea.current?.focus() }, [])

  const running = outcome.kind === 'running'

  // Runs `text`, which starts at `offset` within `fullSql` (for error positions).
  const execute = async (text: string, offset: number, fullSql: string) => {
    if (!text.trim()) return
    runningCall.current?.cancel() // a new run replaces one still running
    const seq = ++runSeq.current
    setOutcome({ kind: 'running' })
    const call = DBService.RunQuery(text)
    runningCall.current = call
    try {
      const result = await call
      if (seq !== runSeq.current) return
      setOutcome({ kind: 'done', result })
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

  // The Run button / ⌘↵: with text selected, run only the selection.
  const run = () => {
    const el = textarea.current
    if (!el || running) return
    const { selectionStart: start, selectionEnd: end } = el
    if (start !== end) execute(sql.slice(start, end), start, sql)
    else execute(sql, 0, sql)
  }

  // A sidebar click: replace the editor text and run it.
  useEffect(() => {
    if (!request) return
    setSql(request.sql)
    saveQuery(request.sql)
    execute(request.sql, 0, request.sql)
  }, [request?.seq])

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

  return (
    <section className="panel" aria-labelledby="editor-title">
      <header className="panel-header">
        <div>
          <h2 className="panel-title" id="editor-title">Query</h2>
          <p className="panel-subtitle">Every run uses the same connection, so <code>SET</code> and open transactions carry over.</p>
        </div>
        <div className="panel-actions">
          <span className="hint">{running ? <><kbd>esc</kbd> to cancel</> : <><kbd>⌘</kbd> <kbd>↵</kbd> to run</>}</span>
          {running ? (
            <button type="button" className="btn btn-danger btn-sm" onClick={cancel}><Spinner /> Cancel</button>
          ) : (
            <button type="button" className="btn btn-primary btn-sm" onClick={run} disabled={!sql.trim()}>
              <PlayIcon /> {hasSelection ? 'Run selection' : 'Run'}
            </button>
          )}
        </div>
      </header>

      <textarea
        ref={textarea}
        id="query"
        className="sql-input"
        aria-label="SQL query"
        value={sql}
        onChange={(e) => { setSql(e.target.value); saveQuery(e.target.value) }}
        onKeyDown={onKeyDown}
        onSelect={(e) => setHasSelection(e.currentTarget.selectionStart !== e.currentTarget.selectionEnd)}
        placeholder="SELECT * FROM pg_tables WHERE schemaname = 'public';"
        rows={6}
        spellCheck={false}
        autoCapitalize="off"
        autoCorrect="off"
        autoComplete="off"
      />

      <div className="query-output" aria-live="polite">
        <Output outcome={outcome} />
      </div>
    </section>
  )
}

function Output({ outcome }: { outcome: Outcome }) {
  switch (outcome.kind) {
    case 'idle':
      return null
    case 'running':
      return <p className="hint">Running…</p>
    case 'cancelled':
      return <Banner tone="info" title="Query cancelled" message="Postgres stopped the query. The connection is still open." />
    case 'error': {
      const { message, hint, detail, line, column } = outcome.error
      const where = line ? ` Line ${line}, column ${column}.` : ''
      return (
        <Banner tone="error" title="Query failed" message={`${message}${where}${hint ? ` Hint: ${hint}` : ''}`} detail={detail} />
      )
    }
    case 'done': {
      const results = outcome.result.results ?? []
      const { runId, pageSize, durationMs } = outcome.result
      if (results.length === 0) return <p className="hint">Nothing to run. · {durationMs} ms</p>
      // Keyed by run, so each new run starts back on page 1.
      if (results.length === 1) {
        return <StatementOutput key={runId} runId={runId} index={0} pageSize={pageSize} result={results[0]} durationMs={durationMs} />
      }
      return (
        <>
          {results.map((r, i) => (
            <StatementOutput key={`${runId}-${i}`} runId={runId} index={i} pageSize={pageSize} result={r} />
          ))}
          <p className="result-meta">{results.length} statements · {durationMs} ms</p>
        </>
      )
    }
  }
}

type StatementProps = {
  runId: number
  index: number // which statement of the run
  pageSize: number
  result: StatementResult
  durationMs?: number // passed when this is the run's only statement
}

function StatementOutput({ runId, index, pageSize, result, durationMs }: StatementProps) {
  const columns = result.columns ?? []
  const [page, setPage] = useState(0)
  const [rows, setRows] = useState(result.rows ?? [])
  const [loading, setLoading] = useState(false)
  const [pageError, setPageError] = useState<string | null>(null)
  const tableWrap = useRef<HTMLDivElement>(null)

  const took = durationMs !== undefined ? ` · ${durationMs} ms` : ''
  const pages = Math.ceil(result.storedRows / pageSize)
  const offset = page * pageSize

  // Later pages come from rows Go already holds (DBService.FetchRows).
  const goToPage = async (next: number) => {
    setLoading(true)
    setPageError(null)
    try {
      setRows((await DBService.FetchRows(runId, index, next * pageSize)) ?? [])
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
    return <p className="command-tag">✓ {result.command}<span className="result-meta">{took}</span></p>
  }

  return (
    <div className="statement">
      <div className="result-table-wrap" ref={tableWrap}>
        <table className="table result-table">
          <thead>
            <tr>
              <th className="rownum">#</th>
              {columns.map((c, i) => (
                <th key={i}>
                  {c.name}
                  {c.type && <span className="col-type">{c.type}</span>}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, i) => (
              <tr key={i}>
                <td className="rownum">{offset + i + 1}</td>
                {(row ?? []).map((value, j) => (
                  <td key={j} title={value ?? 'NULL'}>
                    {value === null ? <span className="null">NULL</span> : value}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="result-footer">
        <p className="result-meta">
          {pages > 1
            ? `Rows ${(offset + 1).toLocaleString()}–${(offset + rows.length).toLocaleString()} of ${result.rowCount.toLocaleString()}`
            : result.rowCount === 1 ? '1 row' : `${result.rowCount.toLocaleString()} rows`}
          {result.rowCount > result.storedRows && ` · only the first ${result.storedRows.toLocaleString()} can be paged`}
          {took}
          {pageError && <span className="page-error"> · {pageError}</span>}
        </p>
        {pages > 1 && (
          <div className="pager">
            {loading && <Spinner />}
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => goToPage(page - 1)}
                    disabled={loading || page === 0}>‹ Previous</button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => goToPage(page + 1)}
                    disabled={loading || page >= pages - 1}>Next ›</button>
          </div>
        )}
      </div>
    </div>
  )
}
