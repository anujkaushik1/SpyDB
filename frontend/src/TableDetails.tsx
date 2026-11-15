import { useEffect, useState } from 'react'
import { Service as Dynamo, type TableDetails as DynamoTable } from '../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import { DBService, type TableDescription, type TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import { errorMessage } from './Banner'
import { formatSize } from './dynamo/itemGrid'
import TableDetailsDialog, { type DetailsSection, type Fact } from './TableDetailsDialog'

export type DetailsTarget = { engine: 'postgres'; table: TableInfo } | { engine: 'dynamo'; table: string }

// Table details for a Postgres table (DBService.DescribeTable: facts,
// columns, indexes, foreign keys) or a DynamoDB table (Dynamo.DescribeTable:
// its key, counts, capacity, indexes), read when opened.
export default function TableDetails({ target, onClose }: { target: DetailsTarget; onClose: () => void }) {
  const [pg, setPg] = useState<TableDescription | null>(null)
  const [dy, setDy] = useState<DynamoTable | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    setPg(null); setDy(null); setError(null)
    const call = target.engine === 'postgres'
      ? DBService.DescribeTable(target.table.schema, target.table.name).then(setPg)
      : Dynamo.DescribeTable(target.table).then(setDy)
    call.catch((err) => setError(errorMessage(err)))
  }, [target])

  const yes = (b: boolean) => (b ? '✓' : '')
  const count = (n: number | null | undefined) => (n == null ? '—' : n.toLocaleString())
  let title = ''
  let subtitle: string | undefined
  let facts: Fact[] = []
  let sections: DetailsSection[] = []
  if (target.engine === 'postgres') {
    title = target.table.qualifiedName
    subtitle = `PostgreSQL ${pg?.kind ?? target.table.kind}`
    if (pg) {
      facts = [
        ['Rows (estimate)', pg.estimatedRows == null ? '— (a view)' : `~${count(pg.estimatedRows)} · from ANALYZE statistics, not a count`],
        ['Size on disk', pg.sizeBytes == null ? '—' : `${formatSize(pg.sizeBytes)} · table, indexes and TOAST`],
        ['Owner', pg.owner],
        ...(pg.comment ? [['Comment', pg.comment] as Fact] : []),
      ]
      sections = [
        { title: 'Columns', columns: ['Name', 'Type', 'Nullable', 'Default', 'Primary key', 'Comment'],
          rows: pg.columns.map((c) => [<code key="n">{c.name}</code>, c.type, yes(c.nullable), c.identity ? `identity (${c.identity})` : c.default ? <code key="d">{c.default}</code> : '', yes(c.primaryKey), c.comment]) },
        { title: 'Indexes', columns: ['Name', 'Columns', 'Unique', 'Method', 'Size'], empty: 'No indexes.',
          rows: pg.indexes.map((ix) => [<code key="n" title={ix.definition}>{ix.name}{ix.primary ? ' (primary key)' : ''}</code>, ix.columns.join(', '), yes(ix.unique), ix.method, formatSize(ix.sizeBytes)]) },
        { title: 'Foreign keys', columns: ['Name', 'Columns', 'Points to'], empty: 'No foreign keys.',
          rows: pg.foreignKeys.map((fk) => [<code key="n">{fk.name}</code>, fk.columns.join(', '), `${fk.refQualifiedName} (${fk.refColumns.join(', ')})`]) },
      ]
    }
  } else {
    title = target.table
    subtitle = 'DynamoDB table'
    if (dy) {
      const avg = dy.itemCount > 0 ? dy.sizeBytes / dy.itemCount : null
      facts = [
        ['Partition key', <code key="p">{dy.partitionKey}</code>],
        ['Sort key', dy.sortKey ? <code key="s">{dy.sortKey}</code> : '— (none)'],
        ['Items', `~${count(dy.itemCount)} · AWS’s count, updated about every 6 hours`],
        ['Size', `${formatSize(dy.sizeBytes)}${avg != null ? ` · about ${formatSize(Math.round(avg))} per item` : ''}`],
        ['Capacity', dy.billing === 'on-demand' ? 'On-demand (pay per request)' : 'Provisioned'],
        ['Status', dy.status],
        ...(dy.createdAt ? [['Created', new Date(dy.createdAt).toLocaleString()] as Fact] : []),
        ...(dy.arn ? [['ARN', <code key="a" className="details-wrap">{dy.arn}</code>] as Fact] : []),
      ]
      const typeName: Record<string, string> = { S: 'string', N: 'number', B: 'binary' }
      sections = [
        { title: 'Key fields', columns: ['Field', 'Type', 'Used by'],
          rows: Object.entries(dy.keyTypes ?? {}).map(([field, type]) => [<code key="f">{field}</code>, typeName[type ?? ''] ?? type ?? '',
            [field === dy.partitionKey || field === dy.sortKey ? 'the table' : '', ...(dy.indexes ?? []).filter((ix) => ix.partitionKey === field || ix.sortKey === field).map((ix) => ix.name)].filter(Boolean).join(', ')]) },
        { title: 'Indexes', columns: ['Name', 'Partition key', 'Sort key', 'Keeps', 'Items', 'Size'], empty: 'No secondary indexes: items can only be found by the table’s key.',
          rows: (dy.indexes ?? []).map((ix) => [<code key="n">{ix.name}</code>, <code key="p">{ix.partitionKey}</code>, ix.sortKey ? <code key="s">{ix.sortKey}</code> : '—', ix.allFields ? 'every field' : 'some fields', `~${count(ix.itemCount)}`, formatSize(ix.sizeBytes)]) },
      ]
    }
  }
  return <TableDetailsDialog title={title} subtitle={subtitle} facts={facts} sections={sections} loading={!pg && !dy} error={error} onClose={onClose} />
}
