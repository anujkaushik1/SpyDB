import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { CompletionContext, CompletionResult } from '@codemirror/autocomplete'
import { Service as Mongo, type QueryResult } from '../../bindings/github.com/anujkaushik1/SpyDB/mongodb'
import Banner, { errorMessage } from '../Banner'
import DataGrid from '../DataGrid'
import { PlayIcon, Spinner } from '../icons'
import { shortcut } from '../platform'
import SqlCodeEditor, { type SqlCodeEditorHandle } from '../SqlCodeEditor'
import { docColumns, docKeys, docRows, toDoc, type Doc, type NewField } from './docGrid'
import { mongoStore, replaceDocs } from './mongoStore'
import { commandAt, commandToRun, suggestions } from './shellText'

// Go's SyntaxError arrives as err.cause.
type SyntaxCause = Partial<{ message: string; line: number; column: number; offset: number }>

type Outcome =
  | { kind: 'idle' }
  | { kind: 'running' }
  | { kind: 'done'; result: QueryResult }
  | { kind: 'error'; message: string }
  | { kind: 'cancelled' }

// What a tab places on screen: the editor (in place of the filter bar, or a
// query tab's top half), a notice strip, and the results (null for none).
export type QueryParts = { editor: ReactNode; notice: ReactNode; results: ReactNode }

type Props = {
  tabId: string // keeps this tab's open result apart from other tabs' in Go
  initialText: string
  active: boolean // the editor is showing: focus it
  leading: ReactNode // the </> toggle, at the start of the bar
  layout?: 'bar' | 'playground'
  collections: string[] // for suggestions after "db."
  fields?: string[] // fields seen, for suggestions
  onRan?: (result: QueryResult) => void // after a command ran: a write may have changed the collection
  onDirtyChange?: (dirty: boolean) => void // unsaved edits in the results
  onTextChange?: (text: string) => void
  children: (parts: QueryParts) => ReactNode
}

// MongoDB's query editor: one shell-like command at a time
// (db.orders.find({…}).sort({…}), aggregate, countDocuments, insertOne,
// updateMany, deleteMany… see mongodb/shell.go), run by Go on the open
// database. Results show in the grid 50 at a time; a find()'s documents can
// be edited there, as a collection tab's can.
export default function MongoQueryEditor({ tabId, initialText, active, leading, layout = 'bar', collections, fields = [], onRan, onDirtyChange, onTextChange, children }: Props) {
  const [text, setText] = useState(initialText)
  const [hasSelection, setHasSelection] = useState(false)
  const [outcome, setOutcome] = useState<Outcome>({ kind: 'idle' })
  const code = useRef<SqlCodeEditorHandle>(null)
  const running = useRef<{ cancel: () => void } | null>(null)
  const runSeq = useRef(0)
  const cancelled = useRef(0)

  useEffect(() => {
    if (active) code.current?.focus(true)
  }, [active])

  // Closing the tab closes its open result in Go.
  useEffect(() => () => {
    running.current?.cancel()
    Mongo.ForgetRun(tabId)
  }, [tabId])

  const isRunning = outcome.kind === 'running'

  const execute = async (command: string, offset: number) => {
    if (!command.trim()) return
    const seq = ++runSeq.current
    onDirtyChange?.(false)
    setOutcome({ kind: 'running' })
    const call = Mongo.RunQuery(tabId, command)
    running.current = call
    try {
      const result = await call
      if (seq !== runSeq.current) return
      setOutcome({ kind: 'done', result })
      onRan?.(result)
    } catch (err) {
      if (seq !== runSeq.current) return
      if (cancelled.current === seq) return setOutcome({ kind: 'cancelled' })
      const cause: SyntaxCause = (err as { cause?: SyntaxCause }).cause ?? {}
      if (typeof cause.offset === 'number') code.current?.select(offset + cause.offset)
      setOutcome({ kind: 'error', message: errorMessage(err) })
    } finally {
      if (seq === runSeq.current) running.current = null
    }
  }

  const run = () => {
    if (!code.current || isRunning) return
    const { from, to } = code.current.selection()
    const command = commandToRun(text, from, to)
    if (command) execute(command.text, command.from)
  }
  const runCurrent = () => {
    if (!code.current || isRunning) return
    const command = commandAt(text, code.current.cursor())
    if (command) execute(command.text, command.from)
  }
  const cancel = () => {
    cancelled.current = runSeq.current
    running.current?.cancel()
  }

  // Suggestions from the open database's collections and the fields seen.
  const latest = useRef({ collections, fields })
  latest.current = { collections, fields }
  const completions = useCallback((context: CompletionContext): CompletionResult | null => {
    const before = context.state.sliceDoc(Math.max(0, context.pos - 300), context.pos)
    const found = suggestions(before, latest.current.collections, latest.current.fields)
    if (!found || (!context.explicit && found.from === context.pos && !/\.$/.test(before))) return null
    const kinds = { collection: 'type', method: 'keyword', operator: 'keyword', field: 'property', value: 'constant' } as const
    return {
      from: context.pos - (before.length - found.from),
      options: found.options.map((o) => ({ label: o.label, type: kinds[o.type] })),
      validFor: /^[\w$]*$/,
    }
  }, [])

  const codeEditor = (
    <SqlCodeEditor
      ref={code}
      className={layout === 'playground' ? 'sql-code-playground' : 'sql-code-bar'}
      initialValue={initialText}
      placeholderText={`db.collection.find({})   ${shortcut('↵')} to run`}
      completions={completions}
      label="MongoDB command"
      onChange={(t) => { setText(t); onTextChange?.(t) }}
      onSelectionChange={setHasSelection}
      onRun={run}
      onRunCurrent={runCurrent}
      onEscape={() => {
        if (!isRunning) return false
        cancel()
        return true
      }}
    />
  )
  const runButton = isRunning ? (
    <button type="button" className="btn btn-danger btn-sm" onClick={cancel}><Spinner /> Cancel</button>
  ) : (
    <button type="button" className="btn btn-primary btn-sm sql-run" onClick={run} disabled={!text.trim()}
            title={hasSelection ? `Run the selected text (${shortcut('↵')})` : `Run (${shortcut('↵')})`}>
      <PlayIcon /> {hasSelection ? 'Run selection' : 'Run'}
    </button>
  )
  const hint = isRunning ? 'Esc to cancel'
    : `${shortcut('↵')} runs the command (the one at the cursor when there are several) · find, aggregate, countDocuments, distinct, insertOne, updateMany, deleteMany…`

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
  switch (outcome.kind) {
    case 'running':
      notice = <p className="tab-notice"><Spinner /> Running…</p>
      break
    case 'cancelled':
      notice = <div className="tab-notice-banner"><Banner tone="info" title="Cancelled" message="The command was stopped. Anything it already wrote stays written." /></div>
      break
    case 'error':
      notice = <div className="tab-notice-banner"><Banner tone="error" title="Command failed" message={outcome.message} /></div>
      break
    case 'done': {
      const { result } = outcome
      const meta = <span className="tab-notice-meta"> · {result.durationMs} ms</span>
      if (result.documents === null) {
        notice = <p className="tab-notice ok">✓ {result.method}: {result.message}{meta}</p>
        break
      }
      notice = (
        <p className="tab-notice">
          {result.method} on {result.collection}{result.message ? ` · ${result.message}` : ''}{meta}
          {layout === 'bar' && <button type="button" className="link-btn tab-notice-action" onClick={() => setOutcome({ kind: 'idle' })}>Show the collection</button>}
        </p>
      )
      results = <QueryResults key={result.runId || `${result.method}-${result.durationMs}`} tabId={tabId} result={result} onDirtyChange={onDirtyChange} />
      break
    }
  }

  return <>{children({ editor, notice, results })}</>
}

// A command's documents: the grid, with Load more while the server has
// more. A find()'s can be edited (saved by _id, like a collection tab's).
function QueryResults({ tabId, result, onDirtyChange }: { tabId: string; result: QueryResult; onDirtyChange?: (dirty: boolean) => void }) {
  const [docs, setDocs] = useState<Doc[]>(() => (result.documents ?? []).map(toDoc))
  const [more, setMore] = useState(result.more)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [added, setAdded] = useState<NewField[]>([])
  const editable = result.editable && docs.every((d) => d.id !== '')

  const columns = useMemo(() => docColumns(docs, added, editable), [docs, added, editable])
  const rows = useMemo(() => docRows(columns, docs), [columns, docs])
  const keys = useMemo(() => docKeys(docs), [docs])
  const store = useMemo(() => editable ? mongoStore({
    collection: result.collection, docs, columns,
    onSaved: (saved) => setDocs((current) => replaceDocs(current, saved)),
    onDeleted: (ids) => setDocs((current) => current.filter((d) => !ids.includes(d.id))),
    onNewFields: (fields) => setAdded((current) => [...current, ...fields]),
  }) : undefined, [editable, result.collection, docs, columns])

  const loadMore = async () => {
    setLoading(true)
    setError(null)
    try {
      const page = await Mongo.FetchMore(tabId, result.runId)
      setDocs((current) => [...current, ...(page.documents ?? []).map(toDoc)])
      setMore(page.more)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="statement bleed">
      <DataGrid
        columns={columns}
        rows={rows}
        firstRowNumber={1}
        target={null}
        store={store}
        rowKeys={keys}
        readOnlyReason={editable ? undefined : result.method === 'find' || result.method === 'findOne' ? 'documents without an _id' : `${result.method} results aren’t the collection’s own documents`}
        canDelete={editable}
        onDirtyChange={onDirtyChange}
        wrapClass="grid-wrap"
      >
        {docs.length === 0 && <p className="grid-empty">No documents.</p>}
      </DataGrid>
      <div className="grid-footer">
        <span className="grid-status">
          {docs.length.toLocaleString()} document{docs.length === 1 ? '' : 's'}{more ? ' so far' : ''}
          {editable ? ` · editable (${result.collection})` : ' · read-only'}
          {error && <span className="page-error"> · {error}</span>}
        </span>
        <div className="grid-pager">
          {loading && <Spinner />}
          <button type="button" className="btn btn-secondary btn-sm" onClick={loadMore} disabled={loading || !more}
                  title="Read the next 50 from the server">
            Load more
          </button>
        </div>
      </div>
    </div>
  )
}
