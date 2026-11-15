import type { TableDetails, Value } from '../../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import type { CellValue, GridColumn } from '../DataGrid'

// Turns DynamoDB items (from dynamo.Service.FindItems) into the columns and
// rows the shared DataGrid shows. Kept free of React so tests can load it.

export type Item = { [field: string]: Value | undefined }

// A field added with "Add field" (or in the JSON view) that no item loaded
// has yet: a column to type its values into. type: what they are.
export type NewField = { name: string; type: NewFieldType }
export type NewFieldType = 'S' | 'N' | 'BOOL' | 'JSON'
export const NEW_FIELD_TYPES: { type: NewFieldType; label: string; category: string }[] = [
  { type: 'S', label: 'string', category: 'text' },
  { type: 'N', label: 'number', category: 'number' },
  { type: 'BOOL', label: 'boolean', category: 'bool' },
  { type: 'JSON', label: 'map / list (JSON)', category: 'json' },
]

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
// before any item is), then the rest A to Z, then fields just added (extra).
// A field that holds one type is coloured as that type; one holding several
// is plain text. NULL values don't count as a type. Key fields can't be
// edited (source ''); the others save to the field of their name.
export function itemColumns(details: TableDetails | null, items: Item[], extra: NewField[] = []): GridColumn[] {
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
  const columns = [...keys, ...rest].map((name) => {
    const types = [...(seen.get(name) ?? [])]
    return {
      name,
      type: types.length ? types.map((t) => TYPES[t]?.label ?? t).join(' / ') : '',
      category: types.length === 1 ? TYPES[types[0]]?.category ?? 'other' : 'text',
      primaryKey: keys.includes(name),
      source: keys.includes(name) ? '' : name,
    }
  })
  for (const field of extra) {
    if (seen.has(field.name) || keys.includes(field.name)) continue
    const t = NEW_FIELD_TYPES.find((x) => x.type === field.type)
    columns.push({ name: field.name, type: t?.label ?? '', category: t?.category ?? 'text', primaryKey: false, source: field.name })
  }
  return columns
}

// Each field's DynamoDB type when every item loaded that has it (not as
// NULL) holds the same one: S, N, BOOL, M, SS…; fields of mixed types are
// left out. New values typed into a column take this type.
export function itemTypes(items: Item[]): Record<string, string> {
  const types: Record<string, string | null> = {}
  for (const item of items) {
    for (const [name, value] of Object.entries(item)) {
      if (!value || value.type === 'NULL') continue
      types[name] = name in types && types[name] !== value.type ? null : value.type
    }
  }
  return Object.fromEntries(Object.entries(types).filter((e): e is [string, string] => e[1] !== null))
}

// One row per item, its fields under their columns. A field the item
// doesn't have is undefined (an empty cell); DynamoDB's NULL shows as NULL.
// In a boolean column, true / false go to the grid as t / f, as Postgres
// sends them.
export function itemRows(columns: GridColumn[], items: Item[]): CellValue[][] {
  return items.map((item) =>
    columns.map((column) => {
      const value = item[column.name]
      if (!value) return undefined
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

// Every field a table tab has seen, with the types it held. It only grows:
// a search showing fewer items mustn't drop fields from the filter bar or
// forget what they hold.
export type SeenFields = Record<string, string[]>

export function addSeen(seen: SeenFields, items: Item[]): SeenFields {
  let next = seen
  for (const item of items) {
    for (const [field, value] of Object.entries(item)) {
      if (!value) continue
      const types = next[field] ?? []
      const type = value.type === 'NULL' ? null : value.type
      if (next[field] && (type === null || types.includes(type))) continue
      if (next === seen) next = { ...seen }
      next[field] = type === null ? types : [...types, type]
    }
  }
  return next
}

// The filter bar's field list: fields some key can find items by come
// first, each with whose key it is (the table's or an index's; a sort key
// says it's the 2nd key), then every other field seen, A to Z. Only
// indexes that keep every field count: SpyDB doesn't search the others.
export type KeyField = { field: string; note: string }

export function fieldOptions(details: TableDetails | null, seen: SeenFields): { keys: KeyField[]; others: string[] } {
  const notes = new Map<string, string[]>()
  const add = (field: string, note: string) => {
    if (!field) return
    if (!notes.has(field)) notes.set(field, [])
    notes.get(field)!.push(note)
  }
  if (details) {
    add(details.partitionKey, 'table key')
    add(details.sortKey, 'table, 2nd key')
    for (const ix of details.indexes ?? []) {
      if (!ix.allFields) continue
      add(ix.partitionKey, ix.name)
      add(ix.sortKey, `${ix.name}, 2nd key`)
    }
  }
  const keys = [...notes].map(([field, list]) => ({ field, note: list.join('; ') }))
  const others = Object.keys(seen).filter((name) => !notes.has(name)).sort((a, b) => a.localeCompare(b))
  return { keys, others }
}

// What a typed value means for a field, from what it has held: numbers
// when only numbers, true / false when only booleans, else text. (For a
// key field Go uses the table's own definition instead.)
export function fieldType(seen: SeenFields, field: string): string {
  const types = seen[field] ?? []
  if (types.length === 1 && (types[0] === 'N' || types[0] === 'BOOL')) return types[0]
  return 'S'
}

// The loaded items sorted by a column: numbers as numbers, the rest as text,
// items without the field last. No sort: as AWS sent them.
export function sortItems(items: Item[], sort: { column: string; desc: boolean } | null): Item[] {
  if (!sort) return items
  const { column, desc } = sort
  const numeric = items.every((item) => !item[column] || item[column].type === 'N' || item[column].type === 'NULL')
  const compare = (a: Item, b: Item) => {
    const va = a[column], vb = b[column]
    const ha = !!va && va.type !== 'NULL', hb = !!vb && vb.type !== 'NULL'
    if (ha !== hb) return ha ? -1 : 1 // missing and NULL go last, whichever way
    if (!ha) return 0
    const c = numeric ? Number(va!.text) - Number(vb!.text) : va!.text.localeCompare(vb!.text, undefined, { numeric: true })
    return desc ? -c : c
  }
  return items.map((item, i) => [item, i] as const).sort(([a, i], [b, j]) => compare(a, b) || i - j).map(([item]) => item)
}
