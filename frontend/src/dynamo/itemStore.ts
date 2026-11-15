import { isLosslessNumber, LosslessNumber, parse, stringify } from 'lossless-json'
import type { FieldChange, ItemDelete, ItemEdit, Shown } from '../../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import type { CellValue, GridEdit, GridValues } from '../DataGrid'
import type { Item } from './itemGrid'

// What the grid's edits mean for DynamoDB: the JSON view of an item (its own
// fields, as their own types), edited JSON back into field changes, and the
// edits and deletes as dynamo.Service.SaveItems / DeleteItems take them.
// Kept free of React and Wails so tests can load it.

// keyNames: the table's key fields (partition, then sort). types: each
// field's one DynamoDB type among the items loaded (itemTypes), or a new
// field's chosen type; what a value typed into its column becomes.
export type ItemShape = { keyNames: string[]; types: Record<string, string> }

// One field's value with unsaved edits as JSON (lossless-json numbers), or
// undefined when the item hasn't got the field (or it was removed).
function fieldJson(shape: ItemShape, item: Item, field: string, edit: GridEdit | undefined): unknown {
  if (edit && field in edit.values) {
    const value = edit.values[field]
    if (value === undefined || value === null) return value
    const own = item[field]
    const type = edit.types?.[field] ?? (own && own.type !== 'NULL' ? own.type : shape.types[field] ?? 'S')
    return typedJson(value, type)
  }
  const own = item[field]
  if (!own) return undefined
  switch (own.type) {
    case 'NULL': return null
    case 'N': return new LosslessNumber(own.text)
    case 'BOOL': return own.text === 'true'
    case 'M': case 'L': case 'SS': case 'NS': case 'BS': return jsonOr(own.text)
  }
  return own.text // S, binary as base64
}

// Text as the JSON for a value of a DynamoDB type; what doesn't fit (a
// number column holding "abc" not yet saved) stays text.
function typedJson(text: string, type: string): unknown {
  switch (type) {
    case 'N': return /^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?$/.test(text.trim()) ? new LosslessNumber(text.trim()) : text
    case 'BOOL': {
      const t = text.trim().toLowerCase()
      return t === 'true' || t === 't' ? true : t === 'false' || t === 'f' ? false : text
    }
    case 'M': case 'L': case 'SS': case 'NS': case 'BS': case 'JSON': return jsonOr(text)
  }
  return text
}

function jsonOr(text: string): unknown {
  try {
    return parse(text)
  } catch {
    return text
  }
}

// The fields of one item with its edits: the key first, then A to Z.
function itemObject(shape: ItemShape, item: Item, edit: GridEdit | undefined): Record<string, unknown> {
  const names = new Set([...Object.keys(item), ...Object.keys(edit?.values ?? {})])
  const keys = shape.keyNames.filter((k) => names.has(k))
  const rest = [...names].filter((n) => !shape.keyNames.includes(n)).sort((a, b) => a.localeCompare(b))
  const object: Record<string, unknown> = {}
  for (const name of [...keys, ...rest]) {
    const value = fieldJson(shape, item, name, edit)
    if (value !== undefined) object[name] = value
  }
  return object
}

// Items (rows: indexes into items) as JSON: one object, or an array for
// several (or always, asArray), indented.
export function itemsJson(shape: ItemShape, items: Item[], rows: number[], edit: (row: number) => GridEdit | undefined, asArray: boolean): string {
  const objects = rows.map((r) => itemObject(shape, items[r] ?? {}, edit(r)))
  return stringify(objects.length === 1 && !asArray ? objects[0] : objects, null, 2) ?? ''
}

export type FieldEdit = { row: number; field: string; value: CellValue; type?: string } // row: index into the rows given

// Edited JSON of items back into field changes: a changed value, a field
// added (both take the JSON's type: 5 a number, "5" text, {…} a map), a
// field deleted (removed). The key can't change; the items can't be added
// or removed here.
export function itemsJsonEdits(shape: ItemShape, items: Item[], rows: number[], text: string, edit: (row: number) => GridEdit | undefined):
  { changes: FieldEdit[] } | { error: string } {
  let data: unknown
  try {
    data = parse(text)
  } catch (err) {
    return { error: `Not valid JSON: ${err instanceof Error ? err.message : String(err)}` }
  }
  let objects: unknown[]
  if (rows.length === 1) {
    if (!isObject(data)) return { error: 'Expected one JSON object for the item.' }
    objects = [data]
  } else {
    if (!Array.isArray(data)) return { error: `Expected an array of ${rows.length} item objects.` }
    if (data.length !== rows.length) {
      return { error: `Items can’t be added or removed here: expected ${rows.length}, found ${data.length}. (PartiQL can: INSERT, DELETE.)` }
    }
    objects = data
  }
  const changes: FieldEdit[] = []
  for (let i = 0; i < objects.length; i++) {
    const edited = objects[i]
    const where = rows.length > 1 ? `Item ${i + 1}: ` : ''
    if (!isObject(edited)) return { error: `${where}each item must be a JSON object.` }
    const shown = itemObject(shape, items[rows[i]] ?? {}, edit(rows[i]))
    for (const key of shape.keyNames) {
      if (!(key in edited) || stringify(edited[key]) !== stringify(shown[key])) {
        return { error: `${where}"${key}" is part of the key and can’t change here. (To make an item with another key, use PartiQL: INSERT.)` }
      }
    }
    for (const field of new Set([...Object.keys(shown), ...Object.keys(edited)])) {
      if (shape.keyNames.includes(field)) continue
      if (!(field in edited)) {
        changes.push({ row: i, field, value: undefined })
        continue
      }
      const value = edited[field]
      if (field in shown && stringify(value) === stringify(shown[field])) continue
      if (field === '') return { error: `${where}a field needs a name.` }
      changes.push({ row: i, field, ...cellOf(value) })
    }
  }
  return { changes }
}

// A JSON value as a grid cell and the type it is.
function cellOf(value: unknown): { value: CellValue; type?: string } {
  if (value === null) return { value: null }
  if (typeof value === 'string') return { value, type: 'S' }
  if (typeof value === 'boolean') return { value: value ? 'true' : 'false', type: 'BOOL' }
  if (isLosslessNumber(value)) return { value: value.toString(), type: 'N' }
  return { value: stringify(value) ?? '', type: 'JSON' }
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value) && !isLosslessNumber(value)
}

// A field as the grid showed it before the edit.
function shownOf(value: CellValue): Shown {
  if (value === undefined) return { has: false, null: false, text: '' }
  if (value === null) return { has: true, null: true, text: '' }
  return { has: true, null: false, text: value }
}

// The grid's edits as SaveItems takes them.
export function itemEdits(shape: ItemShape, edits: GridEdit[]): ItemEdit[] {
  return edits.map((e) => {
    const changes: { [field: string]: FieldChange } = {}
    for (const [field, value] of Object.entries(e.values)) {
      const chosen = e.types?.[field]
      changes[field] = value === undefined ? { remove: true, null: false, text: '', type: '', typed: false }
        : value === null ? { remove: false, null: true, text: '', type: '', typed: false }
        : { remove: false, null: false, text: value, type: chosen ?? shape.types[field] ?? '', typed: chosen !== undefined }
    }
    return { key: keyText(e.key), old: shownMap(e.old), changes }
  })
}

// The grid's rows to delete as DeleteItems takes them.
export function itemDeletes(rows: { key: GridValues; old: GridValues }[]): ItemDelete[] {
  return rows.map((r) => ({ key: keyText(r.key), old: shownMap(r.old) }))
}

function keyText(key: GridValues): { [field: string]: string } {
  return Object.fromEntries(Object.entries(key).map(([k, v]) => [k, v ?? '']))
}

function shownMap(values: GridValues): { [field: string]: Shown } {
  return Object.fromEntries(Object.entries(values).map(([k, v]) => [k, shownOf(v)]))
}

// An item's key as the grid's row key: its key fields' text, in order.
export const itemKey = (shape: ItemShape, item: Item) => shape.keyNames.map((k) => item[k]?.text ?? null)
