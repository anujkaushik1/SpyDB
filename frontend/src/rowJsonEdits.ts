import { isLosslessNumber, parse, stringify } from 'lossless-json'
import { jsonKeys, jsonLiteral } from './copyFormats'

type Column = { name: string; type: string; category: string; primaryKey: boolean }
type Row = (string | null)[]

export type RowJsonEdit = { row: number; col: number; value: string | null } // row: index into the rows given

// Turns the edited text of "View as JSON" back into cell edits: one per field
// whose value changed, converted to the text Postgres expects for that column
// (the same text a typed cell edit sends). Nothing is saved here; the grid
// keeps these as unsaved edits until Save.
//
// rows are the values the JSON was made from (rowsAsJson), so one object for
// a single row, an array for several. editable(col) says whether a column can
// be saved at all. Rules: keys can't be added or removed, the primary key
// can't change, and values must fit their column.
export function rowJsonEdits(columns: Column[], rows: Row[], text: string, editable: (col: number) => boolean):
  { edits: RowJsonEdit[] } | { error: string } {
  let data: unknown
  try {
    data = parse(text)
  } catch (err) {
    return { error: `Not valid JSON: ${err instanceof Error ? err.message : String(err)}` }
  }

  let objects: unknown[]
  if (rows.length === 1) {
    if (!isObject(data)) return { error: 'Expected one JSON object for the row.' }
    objects = [data]
  } else {
    if (!Array.isArray(data)) return { error: `Expected an array of ${rows.length} row objects.` }
    if (data.length !== rows.length) {
      return { error: `Rows can’t be added or removed here: expected ${rows.length}, found ${data.length}.` }
    }
    objects = data
  }

  const keys = jsonKeys(columns)
  const edits: RowJsonEdit[] = []
  for (let i = 0; i < objects.length; i++) {
    const object = objects[i]
    const where = rows.length > 1 ? `Row ${i + 1}: ` : ''
    if (!isObject(object)) return { error: `${where}each row must be a JSON object.` }

    const missing = keys.find((key) => !(key in object))
    if (missing !== undefined) {
      return { error: `${where}"${missing}" is missing. Keys can’t be removed; set it to null to clear the value.` }
    }
    const extra = Object.keys(object).find((key) => !keys.includes(key))
    if (extra !== undefined) return { error: `${where}"${extra}" isn’t a column here. Keys can’t be added.` }

    for (let col = 0; col < columns.length; col++) {
      const column = columns[col]
      const key = keys[col]
      const edited = object[key]
      if (stringify(edited) === stringify(parse(jsonLiteral(rows[i][col], column.category)))) continue

      if (column.primaryKey) return { error: `${where}"${key}" is the primary key and can’t be changed here.` }
      if (!editable(col)) return { error: `${where}"${key}" can’t be saved: it isn’t a column of the table being edited.` }
      try {
        edits.push({ row: i, col, value: columnText(edited, column) })
      } catch (err) {
        return { error: `${where}"${key}" ${err instanceof Error ? err.message : String(err)}` }
      }
    }
  }
  return { edits }
}

// A JSON value as the text Postgres reads for the column. null is SQL NULL
// (also for json / jsonb columns).
function columnText(value: unknown, column: Column): string | null {
  if (value === null) return null
  if (column.category === 'json') return stringify(value) ?? null
  if (Array.isArray(value)) {
    if (!isArrayType(column.type)) throw new Error(`is ${column.type}, so it can’t hold an array.`)
    return postgresArray(value)
  }
  if (isObject(value)) throw new Error(`is ${column.type}, so it can’t hold an object.`)
  return scalarText(value)
}

// Table tabs name array types "text[]", query results use the driver's "_text".
const isArrayType = (type: string) => type.endsWith('[]') || type.startsWith('_')

// ["a", "b c", null, [1, 2]] → {"a","b c",NULL,{"1","2"}}. Every element is
// quoted (Postgres accepts that for any element type) with " and \ escaped.
function postgresArray(items: unknown[]): string {
  const element = (item: unknown): string => {
    if (item === null) return 'NULL'
    if (Array.isArray(item)) return postgresArray(item)
    if (isObject(item)) throw new Error('can’t hold objects inside an array.')
    return `"${scalarText(item).replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`
  }
  return `{${items.map(element).join(',')}}`
}

// Numbers keep their exact digits; Postgres checks the value fits the column.
function scalarText(value: unknown): string {
  if (isLosslessNumber(value)) return value.toString()
  if (typeof value === 'boolean') return value ? 'true' : 'false'
  return String(value)
}

// A JSON object (lossless-json numbers are objects too, but not this kind).
function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value) && !isLosslessNumber(value)
}
