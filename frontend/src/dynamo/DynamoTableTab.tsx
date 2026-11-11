import { useEffect, useMemo, useState } from 'react'
import { Service as Dynamo, type TableDetails } from '../../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import Banner, { errorMessage } from '../Banner'
import DataGrid from '../DataGrid'
import { KeyIcon, RefreshIcon, Spinner } from '../icons'
import { formatSize, itemColumns, itemRows, type Item } from './itemGrid'

// One DynamoDB table, read-only: its key and size along the top
// (dynamo.Service.DescribeTable), then its items a page at a time
// (ScanItems): Load more adds the next page under the ones loaded.
export default function DynamoTableTab({ table }: { table: string }) {
  const [details, setDetails] = useState<TableDetails | null>(null)
  const [detailsError, setDetailsError] = useState<string | null>(null)
  const [items, setItems] = useState<Item[]>([])
  const [next, setNext] = useState('') // where the next page starts; '' when there's none
  const [loaded, setLoaded] = useState(false) // the first page has come
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [durationMs, setDurationMs] = useState<number | null>(null)

  // from '' loads the first page again, replacing the items; otherwise the
  // page after, added below them.
  const load = async (from: string) => {
    setLoading(true)
    try {
      const page = await Dynamo.ScanItems(table, from)
      const got = (page.items ?? []).filter((item): item is Item => item !== null)
      setItems((current) => (from === '' ? got : [...current, ...got]))
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
    load('')
  }

  useEffect(() => {
    reload()
  }, [table])

  const columns = useMemo(() => itemColumns(details, items), [details, items])
  const rows = useMemo(() => itemRows(columns, items), [columns, items])

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
            </span>
          </>
        )}
        {!details && detailsError && <span className="dynamo-table-error">{detailsError}</span>}
        {!details && !detailsError && <span className="dynamo-table-size">Loading…</span>}
      </div>

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
            <p className="grid-empty">{next ? 'No items on this page: load more.' : 'This table is empty.'}</p>
          )}
          {!loaded && loading && <p className="grid-empty">Loading…</p>}
        </DataGrid>

        <footer className="grid-footer">
          <span className="grid-status">
            {[
              loaded && `${items.length.toLocaleString()} item${items.length === 1 ? '' : 's'} loaded${next ? '' : ' · all of them'}`,
              durationMs != null && `${durationMs} ms`,
              'read-only',
            ].filter(Boolean).join(' · ')}
          </span>
          <div className="grid-pager">
            <button type="button" className="sidebar-icon-btn" onClick={reload} disabled={loading}
                    aria-label="Reload items" title="Reload from the first page">
              {loading ? <Spinner /> : <RefreshIcon />}
            </button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => load(next)} disabled={loading || !next}
                    title="Read the next page from AWS (up to 50 items)">
              Load more
            </button>
          </div>
        </footer>
      </div>
    </div>
  )
}
