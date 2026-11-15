import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Service as Mongo, type DocumentPage, type Filter } from '../../bindings/github.com/anujkaushik1/SpyDB/mongodb'
import Banner, { errorMessage } from '../Banner'
import DataGrid, { type Sort } from '../DataGrid'
import { ChevronIcon, ChevronLeftIcon, CodeIcon, PlusIcon, RefreshIcon, SearchIcon, Spinner, XIcon } from '../icons'
import { addSeen, docColumns, docKeys, docRows, fieldType, NEW_FIELD_TYPES, toDoc, type Doc, type NewField, type SeenFields } from './docGrid'
import MongoQueryEditor from './MongoQueryEditor'
import { mongoStore, replaceDocs } from './mongoStore'
import { collectionRef } from './shellText'

const PAGE_SIZE = 50

// Keys match filterCondition in find.go (and Postgres's, where they mean the same).
const OPERATORS = [
  { value: 'eq', label: 'equals' },
  { value: 'neq', label: 'not equal' },
  { value: 'contains', label: 'contains' },
  { value: 'starts', label: 'starts with' },
  { value: 'gt', label: '>' },
  { value: 'gte', label: '≥' },
  { value: 'lt', label: '<' },
  { value: 'lte', label: '≤' },
  { value: 'exists', label: 'exists' },
  { value: 'not_exists', label: 'doesn’t exist' },
]
const NO_VALUE = new Set(['exists', 'not_exists'])

type DraftFilter = { key: number; field: string; operator: string; value: string }

// Filters to show the tab with (Duplicate); a new seq applies them again.
export type MongoPreset = { filters: Filter[]; seq: number }

type Props = {
  collection: string
  tabId: string
  collections: string[] // the database's, for the query editor's suggestions
  preset?: MongoPreset
  onDirtyChange: (dirty: boolean) => void // unsaved edits in this tab
  onFiltersChange?: (filters: Filter[]) => void // the applied filters, for Duplicate
  onCollectionsChange: () => void // a command may have made or emptied collections: the sidebar reloads
  refreshSignal?: number // ⌘R / Ctrl+R: a change reloads the documents, as ↻ does
}

// One MongoDB collection: its documents a page at a time (FindDocuments),
// sorted by a header click and narrowed by a filter bar like Postgres's;
// cells, the JSON view and "Add field" edit them (saved by _id). </> swaps
// the filter bar for the query editor; the documents stay below until a
// command returns documents of its own.
export default function MongoCollectionTab({ collection, tabId, collections, preset, onDirtyChange, onFiltersChange, onCollectionsChange, refreshSignal }: Props) {
  const [queryMode, setQueryMode] = useState(false)
  const [rowsDirty, setRowsDirty] = useState(false)
  const [resultsDirty, setResultsDirty] = useState(false)
  useEffect(() => onDirtyChange(rowsDirty || resultsDirty), [rowsDirty || resultsDirty])
  const [reloadSignal, setReloadSignal] = useState(0)
  const reload = () => setReloadSignal((n) => n + 1)
  useEffect(() => { if (refreshSignal) reload() }, [refreshSignal])
  const [fields, setFields] = useState<string[]>([])

  const toggle = (on: boolean) => (
    <button type="button" className={on ? 'sql-toggle on' : 'sql-toggle'} onClick={() => { if (queryMode) reload(); setQueryMode((q) => !q) }}
            aria-pressed={on} title={on ? 'Back to the documents' : 'Write a query (find, aggregate, updateMany…)'}>
      <CodeIcon />
    </button>
  )

  return (
    <MongoQueryEditor
      tabId={tabId}
      initialText={`${collectionRef(collection)}.find({})`}
      active={queryMode}
      leading={toggle(true)}
      collections={collections}
      fields={fields}
      onRan={(result) => {
        // A write may have changed these documents, or made a collection.
        if (result.documents === null) {
          reload()
          onCollectionsChange()
        }
      }}
      onDirtyChange={setResultsDirty}
    >
      {(parts) => (
        <DocumentsView
          collection={collection}
          preset={preset}
          onFiltersChange={onFiltersChange}
          onFields={setFields}
          toggle={toggle(false)}
          reloadSignal={reloadSignal}
          top={queryMode ? parts.editor : null}
          notice={queryMode ? parts.notice : null}
          results={parts.results}
          showResults={queryMode && parts.results !== null}
          onDirtyChange={setRowsDirty}
        />
      )}
    </MongoQueryEditor>
  )
}

type ViewProps = {
  collection: string
  preset?: MongoPreset
  onFiltersChange?: (filters: Filter[]) => void
  onFields: (fields: string[]) => void
  toggle: ReactNode
  reloadSignal: number
  top: ReactNode // the query editor in place of the filter bar, or null
  notice: ReactNode
  results: ReactNode
  showResults: boolean // results take the documents' place (both stay mounted, so edits survive)
  onDirtyChange: (dirty: boolean) => void
}

function DocumentsView({ collection, preset, onFiltersChange, onFields, toggle, reloadSignal, top, notice, results, showResults, onDirtyChange }: ViewProps) {
  const [page, setPage] = useState(0)
  const [sort, setSort] = useState<Sort>(null)
  const nextKey = useRef(1)
  const draftOf = (filters: Filter[]): DraftFilter[] =>
    filters.length > 0 ? filters.map((f) => ({ key: nextKey.current++, field: f.field, operator: f.operator, value: f.value })) : [{ key: nextKey.current++, field: '', operator: 'eq', value: '' }]
  const [draft, setDraft] = useState<DraftFilter[]>(() => draftOf(preset?.filters ?? []))
  const [applied, setApplied] = useState<Filter[]>(preset?.filters ?? [])
  useEffect(() => onFiltersChange?.(applied), [applied])
  const [reloadToken, setReloadToken] = useState(0)
  const [data, setData] = useState<DocumentPage | null>(null)
  const [docs, setDocs] = useState<Doc[]>([])
  const [seen, setSeen] = useState<SeenFields>({})
  const [added, setAdded] = useState<NewField[]>([]) // "Add field" columns no document has yet
  const [adding, setAdding] = useState<{ name: string; type: string; error?: string } | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [pageInput, setPageInput] = useState('1')
  const grid = useRef<HTMLDivElement>(null)

  const presetSeq = useRef(preset?.seq)
  useEffect(() => {
    if (!preset || preset.seq === presetSeq.current) return
    presetSeq.current = preset.seq
    setDraft(draftOf(preset.filters))
    setApplied(preset.filters)
    setPage(0)
  }, [preset])

  // The page, sort or filters changed, or a reload: read the page again.
  useEffect(() => {
    let current = true
    setLoading(true)
    const call = Mongo.FindDocuments({ collection, filters: applied, sortBy: sort?.column ?? '', sortDesc: sort?.desc ?? false, page, pageSize: PAGE_SIZE })
    call
      .then((result) => {
        if (!current) return
        const got = (result.documents ?? []).map(toDoc)
        setData(result)
        setDocs(got)
        setSeen((s) => addSeen(s, got))
        setError(null)
        grid.current?.scrollTo({ top: 0 })
      })
      .catch((err) => { if (current) setError(errorMessage(err)) })
      .finally(() => { if (current) setLoading(false) })
    return () => {
      current = false
      call.cancel()
    }
  }, [collection, page, sort, applied, reloadToken, reloadSignal])

  useEffect(() => setPageInput(String(page + 1)), [page])
  useEffect(() => onFields(Object.keys(seen)), [seen])

  const columns = useMemo(() => docColumns(docs, added), [docs, added])
  const rows = useMemo(() => docRows(columns, docs), [columns, docs])
  const keys = useMemo(() => docKeys(docs), [docs])
  const store = useMemo(() => mongoStore({
    collection, docs, columns,
    onSaved: (saved) => {
      setDocs((current) => replaceDocs(current, saved))
      setSeen((s) => addSeen(s, saved))
    },
    onDeleted: (ids) => setDocs((current) => current.filter((d) => !ids.includes(d.id))),
    onNewFields: (fields) => setAdded((current) => [...current, ...fields]),
  }), [collection, docs, columns])

  const filtered = applied.length > 0
  const fieldNames = useMemo(() => {
    const names = Object.keys(seen)
    return names.includes('_id') ? ['_id', ...names.filter((n) => n !== '_id')] : names
  }, [seen])
  const firstField = fieldNames[0] ?? '_id'

  const toggleSort = (column: string) => {
    setSort((s) => (s?.column !== column ? { column, desc: false } : !s.desc ? { column, desc: true } : null))
    setPage(0)
  }
  const updateDraft = (key: number, change: Partial<DraftFilter>) => setDraft((d) => d.map((f) => (f.key === key ? { ...f, ...change } : f)))
  // Rows with no value (other than exists / doesn't exist) are left out. The
  // type comes from what the field has held.
  const applyFilters = () => {
    const ready = draft
      .map((f) => ({ field: f.field || firstField, operator: f.operator, value: NO_VALUE.has(f.operator) ? '' : f.value }))
      .filter((f) => f.field && (NO_VALUE.has(f.operator) || f.value !== ''))
      .map((f) => ({ ...f, type: fieldType(seen, f.field) }))
    setApplied(ready)
    setPage(0)
  }
  const clearFilters = () => {
    setDraft(draftOf([]))
    setApplied([])
    setPage(0)
  }
  const goToPage = () => {
    const n = Number.parseInt(pageInput, 10)
    if (Number.isFinite(n) && n >= 1) setPage(n - 1)
    else setPageInput(String(page + 1))
  }

  // "Add field": a new column; typing in its cells adds the field to those documents.
  const addField = () => {
    if (!adding) return
    const name = adding.name.trim()
    const problem = !name ? 'Give it a name.'
      : name === '_id' || columns.some((c) => c.name === name) ? `There’s a ${name} column already.`
      : /^\$|\./.test(name) ? 'A field name can’t start with $ or have a dot.'
      : null
    if (problem) return setAdding({ ...adding, error: problem })
    setAdded((current) => [...current, { name, type: adding.type }])
    setAdding(null)
  }

  const first = page * PAGE_SIZE + 1
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
          <select className="select select-column" aria-label="Filter field" value={f.field || firstField}
                  onChange={(e) => updateDraft(f.key, { field: e.target.value })}>
            {(fieldNames.length ? fieldNames : ['_id']).map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
          <select className="select select-operator" aria-label="Filter condition" value={f.operator}
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
                      onClick={() => setDraft((d) => [...d, { key: nextKey.current++, field: '', operator: 'eq', value: '' }])}>
                <PlusIcon />
              </button>
              <button type="button" className="btn btn-primary btn-sm" onClick={applyFilters}>
                <SearchIcon /> Apply
              </button>
              {filtered && <button type="button" className="btn btn-secondary btn-sm" onClick={clearFilters}>Clear</button>}
            </>
          )}
        </div>
      ))}
    </div>
  )

  const docsView = (
    <div className="rows-pane" hidden={showResults}>
      {error && (
        <div className="table-error">
          <Banner tone="error" title="Couldn’t load documents" message={error} />
        </div>
      )}
      <DataGrid
        columns={columns}
        rows={rows}
        firstRowNumber={first}
        target={null}
        store={store}
        rowKeys={keys}
        canDelete
        onDirtyChange={onDirtyChange}
        sort={sort}
        onSort={toggleSort}
        wrapClass={loading && data ? 'grid-wrap loading' : 'grid-wrap'}
        wrapRef={grid}
      >
        {!loading && data && docs.length === 0 && (
          <p className="grid-empty">{page > 0 ? `No documents on page ${page + 1}.` : filtered ? 'No documents match these filters.' : 'This collection is empty.'}</p>
        )}
        {loading && !data && <p className="grid-empty">Loading…</p>}
      </DataGrid>

      <footer className="grid-footer">
        <span className="grid-status">
          {filtered ? `${applied.length} filter${applied.length > 1 ? 's' : ''}` : collection}
          {docs.length > 0 && ` · documents ${first.toLocaleString()}–${(first + docs.length - 1).toLocaleString()}`}
          {sort && ` · sorted by ${sort.column} ${sort.desc ? '↓' : '↑'}`}
          {data && ` · ${data.durationMs} ms`}
        </span>
        <div className="grid-pager">
          {adding ? (
            <form className="mongo-add-field" onSubmit={(e) => { e.preventDefault(); addField() }}>
              <input className="input" value={adding.name} autoFocus placeholder="Field name" aria-label="New field’s name"
                     onChange={(e) => setAdding({ ...adding, name: e.target.value, error: undefined })}
                     onKeyDown={(e) => { if (e.key === 'Escape') setAdding(null) }}
                     spellCheck={false} autoCapitalize="off" autoCorrect="off" autoComplete="off" />
              <select className="select" value={adding.type} aria-label="New field’s type" onChange={(e) => setAdding({ ...adding, type: e.target.value })}>
                {NEW_FIELD_TYPES.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
              </select>
              <button type="submit" className="btn btn-primary btn-sm">Add</button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setAdding(null)}>Cancel</button>
              {adding.error && <span className="page-error">{adding.error}</span>}
            </form>
          ) : (
            <button type="button" className="btn btn-secondary btn-sm export-btn" onClick={() => setAdding({ name: '', type: 'string' })}
                    title="Add a column; type in its cells to add the field to those documents, then save">
              <PlusIcon /> Field
            </button>
          )}
          <button type="button" className="sidebar-icon-btn" onClick={() => setReloadToken((t) => t + 1)} disabled={loading}
                  aria-label="Reload documents" title="Reload">
            {loading ? <Spinner /> : <RefreshIcon />}
          </button>
          <button type="button" className="pager-btn" onClick={() => setPage((p) => p - 1)} disabled={loading || page === 0} aria-label="Previous page"><ChevronLeftIcon /></button>
          <input className="page-input" aria-label="Page" value={pageInput} inputMode="numeric"
                 onChange={(e) => setPageInput(e.target.value.replace(/\D/g, ''))}
                 onKeyDown={(e) => { if (e.key === 'Enter') goToPage() }} onBlur={goToPage} />
          <button type="button" className="pager-btn" onClick={() => setPage((p) => p + 1)} disabled={loading || !data?.hasMore} aria-label="Next page"><ChevronIcon /></button>
        </div>
      </footer>
    </div>
  )

  return (
    <div className="table-view">
      {top ?? filterBar}
      {notice}
      {docsView}
      {results && <div className="results-pane" hidden={!showResults}>{results}</div>}
    </div>
  )
}
