import { useEffect, useMemo, useRef, useState } from 'react'
import { Service as Dynamo, type Filter, type FindPlan, type TableDetails } from '../../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import Banner, { errorMessage } from '../Banner'
import DataGrid from '../DataGrid'
import { KeyIcon, PlusIcon, RefreshIcon, SearchIcon, Spinner, XIcon } from '../icons'
import { addSeen, fieldOptions, fieldType, formatSize, itemColumns, itemRows, type Item, type SeenFields } from './itemGrid'

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

// One DynamoDB table, read-only: its key and size along the top
// (dynamo.Service.DescribeTable), a filter bar like Postgres's, then the
// items that match a page at a time (FindItems): Load more adds the next
// page under the ones loaded. A filter with "equals" on a key field lets AWS
// go straight to those items (it says which index beside Apply); otherwise
// every item is checked, 50 a page.
export default function DynamoTableTab({ table }: { table: string }) {
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
      setSeen((s) => addSeen(s, got))
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

  const reload = () => {
    Dynamo.DescribeTable(table)
      .then((d) => { setDetails(d); setDetailsError(null) })
      .catch((err) => setDetailsError(errorMessage(err)))
    load('', applied)
  }

  useEffect(() => {
    reload()
  }, [table])

  const columns = useMemo(() => itemColumns(details, items), [details, items])
  const rows = useMemo(() => itemRows(columns, items), [columns, items])
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
      {filterBar}

      <div className="rows-pane">
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
          rowKeys={[]}
          readOnlyReason="DynamoDB tables are read-only for now"
          wrapClass={loading && loaded ? 'grid-wrap loading' : 'grid-wrap'}
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
              durationMs != null && `${durationMs} ms`,
              'read-only',
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
    </div>
  )
}
