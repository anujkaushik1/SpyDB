import { isLosslessNumber, parse, stringify } from 'lossless-json'
import type { Document, DocumentDelete, DocumentEdit, FieldChange, Shown, Value } from '../../bindings/github.com/anujkaushik1/SpyDB/mongodb'
import type { CellValue, GridColumn, GridEdit, GridValues } from '../DataGrid'

// Turns MongoDB documents (from mongodb.Service) into the shared DataGrid's
// columns and rows, the grid's edits into what Go saves, and documents into
// the JSON view and back. Kept free of React so tests can load it.

// A document as the grid holds it: fields never null.
export type Doc = { id: string; fields: Record<string, Value | undefined>; order: string[] }

export const toDoc = (d: Document): Doc => ({ id: d.id, fields: d.fields ?? {}, order: d.order ?? Object.keys(d.fields ?? {}) })

// A field added with "Add field", before any document has it: its name and
// the type its values get (string, int, double, bool, date, objectId, json).
export type NewField = { name: string; type: string }

// Each BSON type's colour category in the grid (see cellValues.ts).
const CATEGORIES: Record<string, string> = {
  string: 'text', int: 'number', long: 'number', double: 'number', decimal: 'number',
  bool: 'bool', date: 'time', objectId: 'other', object: 'json', array: 'json', json: 'json',
}

// The types "Add field" offers, as the column header names them.
export const NEW_FIELD_TYPES: [string, string][] = [
  ['string', 'text'], ['int', 'whole number'], ['double', 'number'], ['bool', 'true / false'],
  ['date', 'date'], ['objectId', 'ObjectId'], ['json', 'object or list (JSON)'],
]

// Documents needn't share fields, so the columns are every field of the
// documents loaded, _id first, then in the order they first appear (as
// mongosh and Compass show them), then fields added with "Add field". A
// field of one type is coloured as that type; one of several is plain text.
// _id is the key: never edited.
export function docColumns(docs: Doc[], added: NewField[] = [], editable = true): GridColumn[] {
  const types = new Map<string, Set<string>>()
  for (const doc of docs) {
    for (const name of doc.order) {
      const value = doc.fields[name]
      if (!value) continue
      if (!types.has(name)) types.set(name, new Set())
      if (value.type !== 'null') types.get(name)!.add(value.type)
    }
  }
  for (const f of added) {
    if (!types.has(f.name)) types.set(f.name, new Set([f.type]))
  }
  const names = [...types.keys()].sort((a, b) => (a === '_id' ? -1 : b === '_id' ? 1 : 0))
  return names.map((name) => {
    const seen = [...types.get(name)!]
    return {
      name,
      type: seen.join(' / '),
      category: seen.length === 1 ? CATEGORIES[seen[0]] ?? 'other' : 'text',
      primaryKey: name === '_id',
      source: name === '_id' || !editable ? '' : name,
    }
  })
}

// One row per document, its fields under their columns: a field it hasn't
// got is undefined (an empty cell), null is null; in a true / false column
// booleans are t / f, as Postgres sends them.
export function docRows(columns: GridColumn[], docs: Doc[]): CellValue[][] {
  return docs.map((doc) => columns.map((column) => {
    const value = doc.fields[column.name]
    if (!value) return undefined
    if (value.type === 'null') return null
    if (value.type === 'bool' && column.category === 'bool') return value.text === 'true' ? 't' : 'f'
    return value.text
  }))
}

// Each row's key: its _id, as Go wrote it (canonical Extended JSON).
export const docKeys = (docs: Doc[]) => docs.map((d) => [d.id])

// The type a field's typed values get when a document hasn't got it (or
// holds null): its column's one type, else text.
export function columnHint(column: GridColumn | undefined): string {
  if (!column || column.type.includes(' / ')) return ''
  return column.type === 'object' || column.type === 'array' ? 'json' : column.type
}

// Every field a tab has seen, with the types it held, for the filter bar.
// It only grows, as DynamoDB's does.
export type SeenFields = Record<string, string[]>

export function addSeen(seen: SeenFields, docs: Doc[]): SeenFields {
  let next = seen
  for (const doc of docs) {
    for (const name of doc.order) {
      const value = doc.fields[name]
      if (!value) continue
      const types = next[name] ?? []
      const type = value.type === 'null' ? null : value.type
      if (next[name] && (type === null || types.includes(type))) continue
      if (next === seen) next = { ...seen }
      next[name] = type === null ? types : [...types, type]
    }
  }
  return next
}

// What a filter's typed value means for a field, from what it has held:
// numbers when only numbers, and likewise true / false, dates and ObjectIds;
// else text.
export function fieldType(seen: SeenFields, field: string): string {
  const types = new Set((seen[field] ?? []).map((t) => (['int', 'long', 'double', 'decimal'].includes(t) ? 'number' : t)))
  if (types.size !== 1) return ''
  const [type] = types
  return ['number', 'bool', 'date', 'objectId'].includes(type) ? type : ''
}

// --- The JSON view ---------------------------------------------------------

// A field's value as JSON (relaxed Extended JSON, as mongosh's EJSON shows
// it): {"$oid": …} for an ObjectId, {"$date": …} for a date,
// {"$numberDecimal": …} for an exact decimal; objects and arrays as Go sent them.
export function valueJson(value: Value): string {
  switch (value.type) {
    case 'string': return JSON.stringify(value.text)
    case 'int': case 'long': return value.text
    case 'double': return /^-?(Infinity|NaN)$/.test(value.text) ? `{"$numberDouble":"${value.text}"}` : value.text
    case 'decimal': return `{"$numberDecimal":${JSON.stringify(value.text)}}`
    case 'bool': return value.text === 'true' ? 'true' : 'false'
    case 'null': return 'null'
    case 'objectId': return `{"$oid":${JSON.stringify(value.text)}}`
    case 'date': return `{"$date":${JSON.stringify(value.text)}}`
  }
  return value.text // object, array and the rarer types: already Extended JSON
}

const NUMBER = /^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?$/
const HEX24 = /^[0-9a-fA-F]{24}$/

// A grid value (as typed, or set by the JSON view with `type`) as JSON,
// for a field whose type is `kind` (its own, or its column's for a new one).
// Text that doesn't fit the type shows as a string: Go refuses it on save.
function editedJson(value: string | null, kind: string, type?: string): string {
  if (value === null) return 'null'
  const as = type ?? kind
  switch (as) {
    case 'number': case 'int': case 'long': case 'double':
      return NUMBER.test(value.trim()) ? value.trim() : JSON.stringify(value)
    case 'decimal':
      return NUMBER.test(value.trim()) ? `{"$numberDecimal":${JSON.stringify(value.trim())}}` : JSON.stringify(value)
    case 'bool': {
      const v = value.trim().toLowerCase()
      return v === 't' || v === 'true' ? 'true' : v === 'f' || v === 'false' ? 'false' : JSON.stringify(value)
    }
    case 'objectId':
      return HEX24.test(value.trim()) ? `{"$oid":${JSON.stringify(value.trim())}}` : JSON.stringify(value)
    case 'date':
      return /^\d{4}-\d\d-\d\d/.test(value.trim()) ? `{"$date":${JSON.stringify(value.trim())}}` : JSON.stringify(value)
    case 'json': case 'object': case 'array':
      try {
        parse(value)
        return value
      } catch {
        return JSON.stringify(value)
      }
  }
  return JSON.stringify(value)
}

// One document as a JSON object's text, unsaved edits included: fields in
// the document's order, then fields the edits added.
function docJson(doc: Doc, edit: GridEdit | undefined, hints: Record<string, string>): string {
  const names = [...doc.order, ...Object.keys(edit?.values ?? {}).filter((n) => !doc.order.includes(n))]
  const parts: string[] = []
  for (const name of names) {
    const values = edit?.values ?? {}
    if (name in values) {
      const v = values[name]
      if (v === undefined) continue // removed
      const current = doc.fields[name]
      const kind = current && current.type !== 'null' ? current.type : hints[name] ?? ''
      parts.push(`${JSON.stringify(name)}:${editedJson(v, kind, edit?.types?.[name])}`)
    } else if (doc.fields[name]) {
      parts.push(`${JSON.stringify(name)}:${valueJson(doc.fields[name]!)}`)
    }
  }
  return `{${parts.join(',')}}`
}

// The rows' documents as indented JSON: one object, or an array (asArray, or several).
export function docsJson(docs: Doc[], rows: number[], edit: (row: number) => GridEdit | undefined, hints: Record<string, string>, asArray: boolean): string {
  const objects = rows.map((r) => docJson(docs[r], edit(r), hints))
  const text = rows.length === 1 && !asArray ? objects[0] : `[${objects.join(',')}]`
  try {
    return stringify(parse(text), null, 2) ?? text
  } catch {
    return text
  }
}

export type FieldEditFromJson = { row: number; field: string; value: CellValue; type?: string }

// The edited JSON view back into field changes: a changed value, an added
// key (a new field) or a removed key (the field goes). _id can't change.
// The type of each new value is its JSON type; Go keeps a field's own
// number type where the number fits it.
export function applyDocsJson(docs: Doc[], rows: number[], text: string, edit: (row: number) => GridEdit | undefined, hints: Record<string, string>):
  { changes: FieldEditFromJson[] } | { error: string } {
  let data: unknown
  try {
    data = parse(text)
  } catch (err) {
    return { error: `Not valid JSON: ${err instanceof Error ? err.message : String(err)}` }
  }
  let objects: unknown[]
  if (rows.length === 1 && !Array.isArray(data)) {
    objects = [data]
  } else {
    if (!Array.isArray(data)) return { error: `Expected an array of ${rows.length} documents.` }
    if (data.length !== rows.length) return { error: `Documents can’t be added or removed here: expected ${rows.length}, found ${data.length}. Use insertOne in the query editor.` }
    objects = data
  }
  const changes: FieldEditFromJson[] = []
  for (let i = 0; i < objects.length; i++) {
    const edited = objects[i]
    const where = rows.length > 1 ? `Document ${i + 1}: ` : ''
    if (!isObject(edited)) return { error: `${where}a document must be a JSON object.` }
    const shown = parse(docJson(docs[rows[i]], edit(rows[i]), hints)) as Record<string, unknown>
    if (stringify(edited._id) !== stringify(shown._id)) return { error: `${where}_id can’t be changed or removed (MongoDB never changes a document’s _id).` }
    for (const [field, value] of Object.entries(edited)) {
      if (field in shown && stringify(value) === stringify(shown[field])) continue
      if (field === '') return { error: `${where}a field needs a name.` }
      changes.push({ row: i, field, ...cellFromJson(value) })
    }
    for (const field of Object.keys(shown)) {
      if (!(field in edited)) changes.push({ row: i, field, value: undefined })
    }
  }
  return { changes }
}

// A JSON value as the grid keeps it: its text and its JSON type.
function cellFromJson(value: unknown): { value: CellValue; type?: string } {
  if (value === null) return { value: null }
  if (typeof value === 'string') return { value, type: 'string' }
  if (typeof value === 'boolean') return { value: value ? 't' : 'f', type: 'bool' }
  if (isLosslessNumber(value)) return { value: value.toString(), type: 'number' }
  return { value: stringify(value) ?? '', type: 'json' }
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value) && !isLosslessNumber(value)
}

// --- What Go saves ---------------------------------------------------------

// A grid value as loaded, as Go compares it with the document now.
const shownOf = (value: CellValue): Shown =>
  value === undefined ? { has: false, null: false, text: '' } : value === null ? { has: true, null: true, text: '' } : { has: true, null: false, text: value }

// The grid's edits as Go's DocumentEdits: each changed field's value as
// loaded (Go checks it's still that) and its new value, with the type a new
// field's values get (hints: column → type).
export function documentEdits(edits: GridEdit[], hints: Record<string, string>): DocumentEdit[] {
  return edits.map((e) => {
    const old: Record<string, Shown> = {}
    const changes: Record<string, FieldChange> = {}
    for (const [field, value] of Object.entries(e.values)) {
      old[field] = shownOf(e.old[field])
      const typed = e.types?.[field]
      changes[field] = {
        remove: value === undefined,
        null: value === null,
        text: value ?? '',
        type: typed ?? hints[field] ?? '',
        typed: typed !== undefined,
      }
    }
    return { id: String(e.key._id ?? ''), old, changes }
  })
}

// Rows to delete as Go's DocumentDeletes: every field as loaded.
export function documentDeletes(rows: { key: GridValues; old: GridValues }[]): DocumentDelete[] {
  return rows.map((r) => ({
    id: String(r.key._id ?? ''),
    old: Object.fromEntries(Object.entries(r.old).map(([field, value]) => [field, shownOf(value)])),
  }))
}
