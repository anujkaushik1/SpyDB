import type { TableDetails, Value } from '../../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import type { GridColumn } from '../DataGrid'

// Turns DynamoDB items (from dynamo.Service.ScanItems) into the columns and
// rows the shared DataGrid shows. Kept free of React so tests can load it.

export type Item = { [field: string]: Value | undefined }

// Each DynamoDB type's name in the column header, and the grid's colour
// category for it (number, bool, json, text or other; see cellValues.ts).
const TYPES: Record<string, { label: string; category: string }> = {
  S: { label: 'string', category: 'text' },
  N: { label: 'number', category: 'number' },
  BOOL: { label: 'boolean', category: 'bool' },
  B: { label: 'binary', category: 'other' },
  M: { label: 'map', category: 'json' },
  L: { label: 'list', category: 'json' },
  SS: { label: 'string set', category: 'json' },
  NS: { label: 'number set', category: 'json' },
  BS: { label: 'binary set', category: 'json' },
}

// Items needn't share fields, so the columns are every field in the items
// loaded so far: the table's key first (partition, then sort; there even
// before any item is), then the rest A to Z. A field that holds one type
// is coloured as that type; one holding several is plain text. NULL values
// don't count as a type.
export function itemColumns(details: TableDetails | null, items: Item[]): GridColumn[] {
  const seen = new Map<string, Set<string>>()
  for (const item of items) {
    for (const [name, value] of Object.entries(item)) {
      if (!value) continue
      if (!seen.has(name)) seen.set(name, new Set())
      if (value.type !== 'NULL') seen.get(name)!.add(value.type)
    }
  }
  const keys = [details?.partitionKey, details?.sortKey].filter((k): k is string => !!k)
  const rest = [...seen.keys()].filter((name) => !keys.includes(name)).sort((a, b) => a.localeCompare(b))
  return [...keys, ...rest].map((name) => {
    const types = [...(seen.get(name) ?? [])]
    return {
      name,
      type: types.length ? types.map((t) => TYPES[t]?.label ?? t).join(' / ') : '',
      category: types.length === 1 ? TYPES[types[0]]?.category ?? 'other' : 'text',
      primaryKey: keys.includes(name),
      source: '', // read-only
    }
  })
}

// One row per item, its fields under their columns. A field the item
// doesn't have is an empty cell; DynamoDB's NULL shows as NULL. In a
// boolean column, true / false go to the grid as t / f, as Postgres sends them.
export function itemRows(columns: GridColumn[], items: Item[]): (string | null)[][] {
  return items.map((item) =>
    columns.map((column) => {
      const value = item[column.name]
      if (!value) return ''
      if (value.type === 'NULL') return null
      if (value.type === 'BOOL' && column.category === 'bool') return value.text === 'true' ? 't' : 'f'
      return value.text
    }),
  )
}

// 950 → "950 B", 3200000 → "3.2 MB"
export function formatSize(bytes: number) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = bytes
  let unit = 0
  while (size >= 1000 && unit < units.length - 1) {
    size /= 1000
    unit++
  }
  return `${unit === 0 ? size : size.toFixed(size < 10 ? 1 : 0).replace(/\.0$/, '')} ${units[unit]}`
}
