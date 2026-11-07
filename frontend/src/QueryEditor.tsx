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

// Runs SQL on the connection opened by Connect (see DBService.RunQuery).
export default function QueryEditor() {
  const [sql, setSql] = useState(loadSavedQuery)
  const [hasSelection, setHasSelection] = useState(false)
  const [outcome, setOutcome] = useState<Outcome>({ kind: 'idle' })
  const textarea = useRef<HTMLTextAreaElement>(null)
  const runningCall = useRef<{ cancel: () => void } | null>(null)
  const cancelled = useRef(false)

  useEffect(() => { textarea.current?.focus() }, [])

  const running = outcome.kind === 'running'

  const run = async () => {
    const el = textarea.current
    if (!el || running) return
    // With text selected, run only the selection.
    const { selectionStart: start, selectionEnd: end } = el
    const offset = start !== end ? start : 0
    const text = start !== end ? sql.slice(start, end) : sql
    if (!text.trim()) return

    cancelled.current = false
    setOutcome({ kind: 'running' })
    const call = DBService.RunQuery(text)
    runningCall.current = call
    try {
      setOutcome({ kind: 'done', result: await call })
    } catch (err) {
      if (cancelled.current) {
        setOutcome({ kind: 'cancelled' })
        return
      }
      const cause: QueryErrorCause = (err as { cause?: QueryErrorCause }).cause ?? {}
      const failure: QueryFailure = { message: errorMessage(err), hint: cause.hint || undefined, detail: cause.detail || undefined }
      if (cause.position) {
        const position = offset + cause.position
        Object.assign(failure, lineAndColumn(sql, position))
        // Put the cursor where Postgres found the error.
        el.focus()
        el.setSelectionRange(position - 1, position - 1)
      }
      setOutcome({ kind: 'error', error: failure })
    } finally {
      runningCall.current = null
    }
  }

  const cancel = () => {
    cancelled.current = true
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
      const { durationMs } = outcome.result
      if (results.length === 0) return <p className="hint">Nothing to run. · {durationMs} ms</p>
      if (results.length === 1) return <StatementOutput result={results[0]} durationMs={durationMs} />
      return (
        <>
          {results.map((r, i) => <StatementOutput key={i} result={r} />)}
          <p className="result-meta">{results.length} statements · {durationMs} ms</p>
        </>
      )
    }
  }
}

// durationMs is passed when this is the run's only statement.
function StatementOutput({ result, durationMs }: { result: StatementResult; durationMs?: number }) {
  const columns = result.columns ?? []
  const rows = result.rows ?? []
  const took = durationMs !== undefined ? ` · ${durationMs} ms` : ''

  // INSERT, UPDATE, CREATE TABLE, ...: no rows, just the command tag.
  if (columns.length === 0) {
    return <p className="command-tag">✓ {result.command}<span className="result-meta">{took}</span></p>
  }

  return (
    <div className="statement">
      <div className="result-table-wrap">
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
                <td className="rownum">{i + 1}</td>
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
      <p className="result-meta">
        {result.rowCount === 1 ? '1 row' : `${result.rowCount.toLocaleString()} rows`}
        {result.rowCount > rows.length && ` · showing the first ${rows.length.toLocaleString()}`}
        {took}
      </p>
    </div>
  )
}
