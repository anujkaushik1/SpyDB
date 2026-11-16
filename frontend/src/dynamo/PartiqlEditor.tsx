import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Service as Dynamo, type PartiQLResult, type TableDetails } from '../../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import Banner, { errorMessage } from '../Banner'
import DataGrid from '../DataGrid'
import { PlayIcon, Spinner } from '../icons'
import { shortcut } from '../platform'
import SqlCodeEditor, { type SqlCodeEditorHandle } from '../SqlCodeEditor'
import { splitStatements, statementAt } from '../sqlStatements'
import { itemColumns, itemRows, type Item, type NewField } from './itemGrid'
import { itemKey } from './itemStore'
import { useItemStore } from './useItemStore'

// What the table tab (or a PartiQL tab) places on screen, as with SqlEditor:
// the editor, a strip above the grid, and the results (null: none to show).
export type PartiqlParts = { editor: ReactNode; notice: ReactNode; results: ReactNode }

type Ran = { statement: string; result: PartiQLResult }

type Outcome =
  | { kind: 'idle' }
  | { kind: 'running' }
  | { kind: 'done'; runs: Ran[]; seq: number }
  | { kind: 'error'; message: string; runs: Ran[]; statement: string; seq: number }
  | { kind: 'cancelled' }

type Props = {
  initialStatement: string
  active: boolean // the editor is showing (focus it when it appears)
  leading: ReactNode // the </> toggle, at the start of the editor bar
  layout?: 'bar' | 'playground' // a table tab's bar, or a PartiQL tab's full pane
  onStatementChange?: (text: string) => void // for Duplicate
  onChanged?: () => void // a statement or a save may have changed items: the table tab reloads its own
  onDirtyChange?: (dirty: boolean) => void // unsaved edits in the results
  children: (parts: PartiqlParts) => ReactNode
}

// PartiQL is DynamoDB's SQL: SELECT * FROM "orders" WHERE "userId" = 'u1',
// INSERT INTO "orders" VALUE {'userId': 'u2'}, UPDATE … SET … WHERE <key>,
// DELETE FROM … WHERE <key>. Run (⌘↵) runs the selection, or every statement
// in turn (stopping at the first that fails); ⌘⇧↵ the one at the cursor; Esc
// stops one running. A SELECT shows 50 items at a time (Load more for the
// next), editable like the table's when it read a table rather than an index.
export default function PartiqlEditor({ initialStatement, active, leading, layout = 'bar', onStatementChange, onChanged, onDirtyChange, children }: Props) {
  const [text, setText] = useState(initialStatement)
  const [hasSelection, setHasSelection] = useState(false)
  const [outcome, setOutcome] = useState<Outcome>({ kind: 'idle' })
  const code = useRef<SqlCodeEditorHandle>(null)
  const running = useRef<{ cancel: () => void } | null>(null)
  const runSeq = useRef(0)
  const cancelled = useRef(0)
  const dirty = useRef(new Set<number>())
  const setResultDirty = (index: number, isDirty: boolean) => {
    if (isDirty) dirty.current.add(index)
    else dirty.current.delete(index)
    onDirtyChange?.(dirty.current.size > 0)
  }

  useEffect(() => {
    if (active) code.current?.focus(true)
  }, [active])
  useEffect(() => () => running.current?.cancel(), [])

  const busy = outcome.kind === 'running'

  const execute = async (statements: string[]) => {
    if (statements.length === 0) return
    const seq = ++runSeq.current
    dirty.current.clear()
    onDirtyChange?.(false)
    setOutcome({ kind: 'running' })
    const runs: Ran[] = []
    let wrote = false
    for (const statement of statements) {
      const call = Dynamo.RunPartiQL(statement, '')
      running.current = call
      try {
        const result = await call
        if (seq !== runSeq.current) return
        runs.push({ statement, result })
        wrote ||= !result.select
      } catch (err) {
        if (seq !== runSeq.current) return
        if (wrote) onChanged?.()
        setOutcome(cancelled.current === seq ? { kind: 'cancelled' } : { kind: 'error', message: errorMessage(err), runs, statement, seq })
        return
      } finally {
        if (seq === runSeq.current) running.current = null
      }
    }
    if (wrote) onChanged?.()
    setOutcome({ kind: 'done', runs, seq })
  }

  const statementsOf = (sql: string) => splitStatements(sql).map((s) => s.text.replace(/;\s*$/, '')).filter((s) => s.trim())
  const run = () => {
    if (!code.current || busy) return
    const { from, to } = code.current.selection()
    execute(statementsOf(from !== to ? text.slice(from, to) : text))
  }
  const runCurrent = () => {
    if (!code.current || busy) return
    const statement = statementAt(text, code.current.cursor())
    if (statement) execute(statementsOf(statement.text))
  }
  const cancel = () => {
    cancelled.current = runSeq.current
    running.current?.cancel()
  }

  const codeEditor = (
    <SqlCodeEditor
      ref={code}
      className={layout === 'playground' ? 'sql-code-playground' : 'sql-code-bar'}
      initialValue={initialStatement}
      placeholderText={`Write PartiQL…   ${shortcut('↵')} to run`}
      onChange={(t) => { setText(t); onStatementChange?.(t) }}
      onSelectionChange={setHasSelection}
      onRun={run}
      onRunCurrent={runCurrent}
      onEscape={() => {
        if (!busy) return false
        cancel()
        return true
      }}
    />
  )
  const runButton = busy ? (
    <button type="button" className="btn btn-danger btn-sm" onClick={cancel}><Spinner /> Cancel</button>
  ) : (
    <button type="button" className="btn btn-primary btn-sm sql-run" onClick={run} disabled={!text.trim()}
            title={hasSelection ? `Run the selected text (${shortcut('↵')})` : `Run (${shortcut('↵')})`}>
      <PlayIcon /> {hasSelection ? 'Run selection' : 'Run'}
    </button>
  )
  const hint = busy ? 'Esc to cancel'
    : `${shortcut('↵')} to run · ${shortcut('⇧↵')} runs the statement at the cursor · a SELECT reads 50 items at a time · UPDATE and DELETE need the whole key in WHERE`
  const editor = layout === 'playground' ? (
    <div className="sql-pane">
      {codeEditor}
      <div className="sql-pane-bar">
        <p className="sql-pane-hint">{hint}</p>
        {runButton}
      </div>
    </div>
  ) : (
    <div className="sql-bar">
      {leading}
      <div className="sql-bar-main">
        {codeEditor}
        <p className="sql-bar-hint">{hint}</p>
      </div>
      {runButton}
    </div>
  )

  let notice: ReactNode = null
  let results: ReactNode = null
  const summary = (runs: Ran[]) => runs.map((r) => `✓ ${firstWord(r.statement)}${r.result.select ? ` (${r.result.items?.length ?? 0} items)` : ''}`).join('  ·  ')
  const meta = (runs: Ran[]) => {
    const capacity = runs.reduce((sum, r) => sum + r.result.capacity, 0)
    const ms = runs.reduce((sum, r) => sum + r.result.durationMs, 0)
    return <span className="tab-notice-meta"> · {ms} ms{capacity > 0 && ` · ${formatUnits(capacity)} capacity unit${capacity === 1 ? '' : 's'}`}</span>
  }
  const shown = (runs: Ran[], seq: number) => {
    const withItems = runs.map((r, i) => ({ ...r, i })).filter((r) => r.result.select || (r.result.items?.length ?? 0) > 0)
    if (withItems.length === 0) return null
    return withItems.length === 1 ? (
      <PartiqlResult key={`${seq}-${withItems[0].i}`} index={withItems[0].i} ran={withItems[0]} bleed onDirtyChange={setResultDirty} onChanged={onChanged} />
    ) : (
      <div className="sql-results-stack">
        {withItems.map((r) => <PartiqlResult key={`${seq}-${r.i}`} index={r.i} ran={r} onDirtyChange={setResultDirty} onChanged={onChanged} />)}
      </div>
    )
  }
  switch (outcome.kind) {
    case 'running':
      notice = <p className="tab-notice"><Spinner /> Running…</p>
      break
    case 'cancelled':
      notice = (
        <div className="tab-notice-banner">
          <Banner tone="info" title="Stopped" message="SpyDB stopped waiting for AWS. A write it had already sent may still have happened." />
        </div>
      )
      break
    case 'error':
      notice = (
        <div className="tab-notice-banner">
          <Banner tone="error" title="PartiQL failed"
                  message={`${outcome.runs.length > 0 ? `${summary(outcome.runs)}, then: ` : ''}${outcome.message}`}
                  detail={outcome.runs.length > 0 || statementsOf(text).length > 1 ? outcome.statement : undefined} />
        </div>
      )
      results = shown(outcome.runs, outcome.seq)
      break
    case 'done': {
      results = shown(outcome.runs, outcome.seq)
      notice = (
        <p className={results ? 'tab-notice' : 'tab-notice ok'}>
          {results ? `PartiQL results${outcome.runs.length > 1 ? ` · ${outcome.runs.length} statements` : ''}` : summary(outcome.runs)}
          {meta(outcome.runs)}
          {results && layout === 'bar' && (
            <button type="button" className="link-btn tab-notice-action" onClick={() => setOutcome({ kind: 'idle' })}>Show table items</button>
          )}
        </p>
      )
      break
    }
  }

  return <>{children({ editor, notice, results })}</>
}

const firstWord = (statement: string) => statement.replace(/^(\s|--[^\n]*\n|\/\*[\s\S]*?\*\/)*/, '').split(/\s/)[0].toUpperCase()

// 0.5 → "0.5", 12 → "12", 1.25 → "1.25"
const formatUnits = (n: number) => String(Math.round(n * 100) / 100)

type ResultProps = {
  index: number // which statement of the run
  ran: Ran
  bleed?: boolean // the only result: fill the grid area, as the table's items do
  onDirtyChange: (index: number, dirty: boolean) => void
  onChanged?: () => void
}

// One statement's items: a grid like the table tab's, Load more for the next
// 50, editable when they came from one table (Go says which) with its key.
function PartiqlResult({ index, ran, bleed, onDirtyChange, onChanged }: ResultProps) {
  const table = ran.result.table
  const [items, setItems] = useState<Item[]>(() => (ran.result.items ?? []).filter((i): i is Item => i !== null))
  const [next, setNext] = useState(ran.result.next)
  const [loading, setLoading] = useState(false)
  const [pageError, setPageError] = useState<string | null>(null)
  const [pagesRead, setPagesRead] = useState(1) // of 50 items each
  const [capacity, setCapacity] = useState(ran.result.capacity)
  const [loadingAll, setLoadingAll] = useState(false)
  const stopAll = useRef(false)
  const [details, setDetails] = useState<TableDetails | null>(null)
  const [extra, setExtra] = useState<NewField[]>([])

  useEffect(() => {
    if (!table) return
    let live = true
    Dynamo.DescribeTable(table).then((d) => { if (live) setDetails(d) }).catch(() => {})
    return () => { live = false }
  }, [table])

  const { store, readOnly } = useItemStore({
    table, details, items, setItems, extra,
    onNewFields: (fields) => setExtra((current) => [...current, ...fields]),
    onStale: () => onChanged?.(),
  })
  const columns = useMemo(() => itemColumns(details, items, extra), [details, items, extra])
  const rows = useMemo(() => itemRows(columns, items), [columns, items])
  const keyNames = store?.keyNames ?? []
  const rowKeys = useMemo(() => items.map((item) => itemKey({ keyNames, types: {} }, item)), [items, keyNames.join('\n')])

  // Reads the next page; returns where the one after starts ('' at the end; null after an error).
  const loadMore = async (from = next): Promise<string | null> => {
    setLoading(true)
    setPageError(null)
    try {
      const page = await Dynamo.RunPartiQL(ran.statement, from)
      setItems((current) => [...current, ...(page.items ?? []).filter((i): i is Item => i !== null)])
      setNext(page.next)
      setPagesRead((n) => n + 1)
      setCapacity((c) => c + page.capacity)
      return page.next
    } catch (err) {
      setPageError(errorMessage(err))
      return null
    } finally {
      setLoading(false)
    }
  }
  // Load all: page after page to the end, the count updating as it goes; Stop ends it after the page in flight.
  const loadAll = async () => {
    setLoadingAll(true)
    stopAll.current = false
    let from: string | null = next
    while (from && !stopAll.current) from = await loadMore(from)
    setLoadingAll(false)
  }

  return (
    <div className={bleed ? 'statement bleed' : 'statement'}>
      <DataGrid
        columns={columns}
        rows={rows}
        firstRowNumber={1}
        target={null}
        store={store}
        rowKeys={store ? rowKeys : []}
        readOnlyReason={readOnly}
        canDelete
        onSaved={onChanged}
        onDirtyChange={(d) => onDirtyChange(index, d)}
        wrapClass={bleed ? (loading ? 'grid-wrap loading' : 'grid-wrap') : 'result-box'}
      >
        {items.length === 0 && <p className="grid-empty">{next ? 'No items on this page: load more.' : 'The statement returned no items.'}</p>}
      </DataGrid>
      <div className={bleed ? 'grid-footer' : 'result-footer'}>
        <span className={bleed ? 'grid-status' : 'result-meta'}>
          {items.length === 1 ? '1 item' : `${items.length.toLocaleString()} items`}
          {next || pagesRead > 1 ? ` (of ${(pagesRead * 50).toLocaleString()} read${next ? ', more to read' : ''})` : ' · all of them'}
          {!next && pagesRead > 1 && ' · all of them'}
          {capacity > 0 && ` · ${formatUnits(capacity)} capacity unit${capacity === 1 ? '' : 's'}`}
          {store ? ` · editable (${table})` : ` · read-only${readOnly ? `: ${readOnly}` : ''}`}
          {pageError && <span className="page-error"> · {pageError}</span>}
        </span>
        <div className="grid-pager">
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => loadMore()} disabled={loading || !next}
                  title="Run the statement on from where this page ended (up to 50 items)">
            {loading && !loadingAll ? <><Spinner /> Loading…</> : 'Load more'}
          </button>
          {loadingAll ? (
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => { stopAll.current = true }} title="Stop after the page being read">
              <Spinner /> Stop
            </button>
          ) : (
            <button type="button" className="btn btn-secondary btn-sm" onClick={loadAll} disabled={loading || !next}
                    title="Run the statement on, page after page, until it has no more (Stop any time)">
              Load all
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
