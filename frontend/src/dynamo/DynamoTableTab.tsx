import { useEffect, useMemo, useRef, useState } from 'react'
import { Service as Dynamo, type Filter, type FindPlan, type TableDetails } from '../../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import Banner, { errorMessage } from '../Banner'
import DataGrid, { type Sort } from '../DataGrid'
import { CodeIcon, KeyIcon, PlusIcon, RefreshIcon, SearchIcon, Spinner, XIcon } from '../icons'
import { addSeen, fieldOptions, fieldType, formatSize, itemColumns, itemRows, sortItems, type Item, type NewField, type SeenFields } from './itemGrid'
import { itemKey } from './itemStore'
import PartiqlEditor from './PartiqlEditor'
import { useItemStore } from './useItemStore'

// Keys match the conditions in find.go (and Postgres's where they mean the same).
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

// How a search reads the table, for beside Apply and the footer.
const planText = (plan: FindPlan) =>
  !plan.query ? 'checks every item' : plan.index ? `uses ${plan.index}` : 'uses the table’s key'

// One DynamoDB table: its key and size along the top
// (dynamo.Service.DescribeTable), a filter bar like Postgres's, then the
// items that match a page at a time (FindItems): Load more adds the next
// page under the ones loaded. A filter with "equals" on a key field lets AWS
// go straight to those items (it says which index beside Apply); otherwise
// every item is checked, 50 a page. Items are edited as Postgres rows are
// (useItemStore): cells, Add field for a new column, the JSON view, Delete.
// </> swaps the filter bar for a PartiQL editor; its results take the
// items' place until "Show table items".
type Props = {
  table: string
  refreshSignal?: number // ⌘R / Ctrl+R: a change reloads from the first page, as ↻ does
  onDirtyChange?: (dirty: boolean) => void // unsaved edits in this tab
}

export default function DynamoTableTab({ table, refreshSignal, onDirtyChange }: Props) {
  const [sqlMode, setSqlMode] = useState(false)
  const [itemsDirty, setItemsDirty] = useState(false)
  const [resultsDirty, setResultsDirty] = useState(false)
  useEffect(() => onDirtyChange?.(itemsDirty || resultsDirty), [itemsDirty || resultsDirty])
  const [extra, setExtra] = useState<NewField[]>([]) // fields added that no item has yet
  // A header click sorts the items loaded so far (DynamoDB itself can't sort a scan).
  const [sort, setSort] = useState<Sort>(null)
  const grid = useRef<HTMLDivElement>(null)
  const [details, setDetails] = useState<TableDetails | null>(null)
  const [detailsError, setDetailsError] = useState<string | null>(null)
  const [items, setItems] = useState<Item[]>([])
  const [next, setNext] = useState('') // where the next page starts; '' when there's none
  const [loaded, setLoaded] = useState(false) // the first page has come
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [durationMs, setDurationMs] = useState<number | null>(null)
  const [checked, setChecked] = useState(0) // items AWS read for the pages loaded
  const [readBy, setReadBy] = useState<FindPlan | null>(null) // how they were read
  const [seen, setSeen] = useState<SeenFields>({}) // every field this tab has seen, for the filter bar
  const nextKey = useRef(1)
  const emptyFilter = (): DraftFilter => ({ key: nextKey.current++, field: '', operator: 'eq', value: '' })
  const [draft, setDraft] = useState<DraftFilter[]>(() => [emptyFilter()])
  const [applied, setApplied] = useState<Filter[]>([])
  const [plan, setPlan] = useState<FindPlan | null>(null) // how the filters as typed would read it

  // from '' loads the first page again, replacing the items; otherwise the
  // page after, added below them.
  const load = async (from: string, filters: Filter[]) => {
    setLoading(true)
    try {
      const page = await Dynamo.FindItems(table, filters, from)
      const got = (page.items ?? []).filter((item): item is Item => item !== null)
      setItems((current) => (from === '' ? got : [...current, ...got]))
      setChecked((n) => (from === '' ? page.checked : n + page.checked))
      setReadBy(page.plan)
      setNext(page.next)
      setDurationMs(page.durationMs)
      setLoaded(true)
      setError(null)
    } catch (err) {
      setError(errorMessage(err)) // the items loaded so far stay
    } finally {
      setLoading(false)
    }
  }

  // Fields added but not typed into go; ones with unsaved values stay.
  const reload = () => {
    Dynamo.DescribeTable(table)
      .then((d) => { setDetails(d); setDetailsError(null) })
      .catch((err) => setDetailsError(errorMessage(err)))
    if (!itemsDirty) setExtra([])
    load('', applied)
  }

  useEffect(() => {
    reload()
  }, [table])
  // Not while a page is loading: ↻ is off then too.
  useEffect(() => { if (refreshSignal && !loading) reload() }, [refreshSignal])
  // Saved items can bring new fields too.
  useEffect(() => setSeen((s) => addSeen(s, items)), [items])

  const { store, readOnly } = useItemStore({
    table, details, items, setItems, extra,
    onNewFields: (fields) => setExtra((current) => [...current, ...fields]),
    onStale: reload,
  })
  const columns = useMemo(() => itemColumns(details, items, extra), [details, items, extra])
  // The loaded items in the sorted order (each row still carries its own item's key).
  const shownItems = useMemo(() => sortItems(items, sort), [items, sort])
  const rows = useMemo(() => itemRows(columns, shownItems), [columns, shownItems])
  const keyNames = store?.keyNames ?? []
  const rowKeys = useMemo(() => shownItems.map((item) => itemKey({ keyNames, types: {} }, item)), [shownItems, keyNames.join('\n')])
  const toggleSort = (column: string) => {
    setSort((s) => (s?.column !== column ? { column, desc: false } : !s.desc ? { column, desc: true } : null))
  }
  // A field just added: scroll right to its column.
  useEffect(() => {
    if (extra.length > 0) grid.current?.scrollTo({ left: grid.current.scrollWidth, behavior: 'smooth' })
  }, [extra.length])
  const toggle = (on: boolean) => (
    <button type="button" className={on ? 'sql-toggle on' : 'sql-toggle'} onClick={() => setSqlMode(!on)} aria-pressed={on}
            title={on ? 'Back to the filters' : 'Write PartiQL (DynamoDB’s SQL)'}>
      <CodeIcon />
    </button>
  )
  const fields = useMemo(() => fieldOptions(details, seen), [details, seen])
  const firstField = fields.keys[0]?.field ?? fields.others[0] ?? ''

  // The rows filled in, as Go takes them. Rows with no value are left out
  // (exists and doesn't exist need none). The type comes from the items seen.
  const ready: Filter[] = draft
    .map((f) => ({ field: f.field || firstField, operator: f.operator, value: NO_VALUE.has(f.operator) ? '' : f.value }))
    .filter((f) => f.field && (NO_VALUE.has(f.operator) || f.value !== ''))
    .map((f) => ({ ...f, type: fieldType(seen, f.field) }))
  const readyKey = JSON.stringify(ready)

  // As you type: will it use a key, or check every item?
  useEffect(() => {
    if (ready.length === 0) return setPlan(null)
    let current = true
    Dynamo.PlanFind(table, ready).then((p) => { if (current) setPlan(p) }).catch(() => { if (current) setPlan(null) })
    return () => { current = false }
  }, [table, readyKey, details])

  const updateDraft = (key: number, change: Partial<DraftFilter>) =>
    setDraft((d) => d.map((f) => (f.key === key ? { ...f, ...change } : f)))

  const applyFilters = () => {
    setApplied(ready)
    load('', ready)
  }
  const clearFilters = () => {
    setDraft([emptyFilter()])
    setApplied([])
    load('', [])
  }
  const filtered = applied.length > 0

  // Enter in any part of a filter (the lists too) applies them, as in Postgres.
  const filterBar = (
    <div className="filter-bar dynamo-filters" onKeyDown={(e) => {
      if (e.key === 'Enter' && (e.target as HTMLElement).matches('select, input')) {
        e.preventDefault()
        applyFilters()
      }
    }}>
      {draft.map((f, i) => (
        <div className="filter-row" key={f.key}>
          {i === 0 && toggle(false)}
          <select className="select select-column dynamo-field" aria-label="Filter field" value={f.field || firstField}
                  onChange={(e) => updateDraft(f.key, { field: e.target.value })}>
            {fields.keys.length > 0 && (
              <optgroup label="Keys: fast with “equals”">
                {fields.keys.map((k) => <option key={k.field} value={k.field}>{`🔑 ${k.field} · ${k.note}`}</option>)}
              </optgroup>
            )}
            <optgroup label="Other fields">
              {fields.others.map((name) => <option key={name} value={name}>{name}</option>)}
            </optgroup>
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
            <button type="button" className="sidebar-icon-btn filter-remove" aria-label="Remove filter" title="Remove filter"
                    onClick={() => setDraft((d) => d.filter((x) => x.key !== f.key))}>
              <XIcon />
            </button>
          )}
          {i === 0 && (
            <div className="filter-actions">
              <button type="button" className="sidebar-icon-btn" aria-label="Add filter" title="Add filter (combined with AND)"
                      onClick={() => setDraft((d) => [...d, emptyFilter()])}>
                <PlusIcon />
              </button>
              <button type="button" className="btn btn-primary btn-sm" onClick={applyFilters} disabled={loading}>
                <SearchIcon /> Apply
              </button>
              {filtered && <button type="button" className="btn btn-secondary btn-sm" onClick={clearFilters}>Clear</button>}
              {plan && (
                <span className={plan.query ? 'dynamo-plan fast' : 'dynamo-plan'}
                      title={`${planText(plan)}: ${plan.query ? 'AWS goes straight to the matching items' : 'no key fits these filters, so AWS reads every item, 50 a page, and keeps the ones that match'}`}>
                  {plan.query ? '⚡ ' : ''}{planText(plan)}
                </span>
              )}
            </div>
          )}
        </div>
      ))}
    </div>
  )

  return (
    <PartiqlEditor initialStatement={`SELECT * FROM "${table.replace(/"/g, '""')}"`} active={sqlMode} leading={toggle(true)}
                   onChanged={reload} onDirtyChange={setResultsDirty}>
      {(pq) => (
    <div className="table-view">
      <div className="dynamo-table-bar">
        {details && (
          <>
            <span className="dynamo-key" title="Every item has this field; it finds the item">
              <KeyIcon /> {details.partitionKey} <span className="dynamo-key-role">partition key</span>
            </span>
            {details.sortKey && (
              <span className="dynamo-key" title="Items with the same partition key are kept in this field’s order">
                <KeyIcon /> {details.sortKey} <span className="dynamo-key-role">sort key</span>
              </span>
            )}
            <span className="dynamo-table-size" title="AWS updates these about every 6 hours">
              ~{details.itemCount.toLocaleString()} items · {formatSize(details.sizeBytes)}
              {(details.indexes ?? []).length > 0 && ` · ${(details.indexes ?? []).length} index${(details.indexes ?? []).length === 1 ? '' : 'es'}`}
            </span>
          </>
        )}
        {!details && detailsError && <span className="dynamo-table-error">{detailsError}</span>}
        {!details && !detailsError && <span className="dynamo-table-size">Loading…</span>}
      </div>
      {sqlMode ? pq.editor : filterBar}
      {sqlMode && pq.notice}

      <div className="rows-pane" hidden={sqlMode && pq.results !== null}>
        {error && (
          <div className="table-error">
            <Banner tone="error" title="Couldn’t load items" message={error} />
          </div>
        )}

        <DataGrid
          columns={columns}
          rows={rows}
          firstRowNumber={1}
          target={null}
          store={store}
          rowKeys={store ? rowKeys : []}
          readOnlyReason={readOnly}
          canDelete
          sort={sort}
          onSort={toggleSort}
          onDirtyChange={setItemsDirty}
          wrapClass={loading && loaded ? 'grid-wrap loading' : 'grid-wrap'}
          wrapRef={grid}
        >
          {loaded && !loading && items.length === 0 && (
            <p className="grid-empty">
              {next ? (filtered ? `None of the ${checked.toLocaleString()} items checked so far match: load more to check the next ones.` : 'No items on this page: load more.')
                : filtered ? 'No items match these filters.' : 'This table is empty.'}
            </p>
          )}
          {!loaded && loading && <p className="grid-empty">Loading…</p>}
        </DataGrid>

        <footer className="grid-footer">
          <span className="grid-status">
            {[
              loaded && (filtered
                ? `${items.length.toLocaleString()} found · ${checked.toLocaleString()} checked${next ? '' : ' · all of them'}`
                : `${items.length.toLocaleString()} item${items.length === 1 ? '' : 's'} loaded${next ? '' : ' · all of them'}`),
              loaded && filtered && readBy && planText(readBy).replace('checks', 'checked'),
              sort && `sorted by ${sort.column} ${sort.desc ? '↓' : '↑'} (the items loaded)`,
              durationMs != null && `${durationMs} ms`,
              details && (store ? 'double-click a cell to edit' : `read-only: ${readOnly}`),
            ].filter(Boolean).join(' · ')}
          </span>
          <div className="grid-pager">
            <button type="button" className="sidebar-icon-btn" onClick={reload} disabled={loading}
                    aria-label="Reload items" title="Reload from the first page">
              {loading ? <Spinner /> : <RefreshIcon />}
            </button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => load(next, applied)} disabled={loading || !next}
                    title={filtered ? 'Check the next items (up to 50)' : 'Read the next page from AWS (up to 50 items)'}>
              Load more
            </button>
          </div>
        </footer>
      </div>
      {pq.results && <div className="results-pane" hidden={!sqlMode}>{pq.results}</div>}
    </div>
      )}
    </PartiqlEditor>
  )
}
