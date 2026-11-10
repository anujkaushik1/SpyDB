// Postgres arrays as JSON, so text[] / int[] / … cells open in the JSON editor
// like jsonb: {savings,"hello world",NULL} shows as ["savings", "hello world",
// null] and goes back the same way. No React or Wails imports, so the unit
// tests can load it.
import { isLosslessNumber, parse, stringify } from 'lossless-json'

export type ArrayElement = 'number' | 'bool' | 'text'

const NUMBER_TYPES = new Set([
  'smallint', 'integer', 'bigint', 'numeric', 'real', 'double precision', 'oid',
  'int2', 'int4', 'int8', 'float4', 'float8',
])

// What an array column holds, from its type as the grid has it: "text[]",
// "numeric(12,2)[]" (table tabs, format_type) or "_int4" (query results,
// pgx's name). null when it isn't an array, or is one of box, whose values
// Postgres separates with ; instead of commas.
export function arrayElement(type: string): ArrayElement | null {
  let element: string
  if (type.endsWith('[]')) element = type.replace(/(\[\])+$/, '').replace(/\(.*\)$/, '')
  else if (type.startsWith('_')) element = type.slice(1)
  else return null
  if (element === 'box' || element === '') return null
  if (NUMBER_TYPES.has(element)) return 'number'
  if (element === 'boolean' || element === 'bool') return 'bool'
  return 'text'
}

type Item = { text: string; quoted: boolean } | Item[]

// Reads Postgres's text form of an array: {a,"b c",NULL,{1,2}}. null for
// anything else, including arrays that don't start at 1 ([0:1]={a,b}).
function parseArray(text: string): Item[] | null {
  const s = text.trim()
  let i = 0
  const space = () => { while (/\s/.test(s[i] ?? '')) i++ }
  const list = (): Item[] | null => {
    i++ // {
    const items: Item[] = []
    space()
    if (s[i] === '}') {
      i++
      return items
    }
    for (;;) {
      space()
      let item: Item | null
      if (s[i] === '{') {
        item = list()
      } else if (s[i] === '"') {
        i++
        let out = ''
        while (i < s.length && s[i] !== '"') {
          if (s[i] === '\\') i++
          out += s[i] ?? ''
          i++
        }
        if (s[i] !== '"') return null
        i++
        item = { text: out, quoted: true }
      } else {
        const start = i
        while (i < s.length && s[i] !== ',' && s[i] !== '}') i++
        const raw = s.slice(start, i).trim()
        item = raw === '' || /[{"\\]/.test(raw) ? null : { text: raw, quoted: false }
      }
      if (item === null) return null
      items.push(item)
      space()
      if (s[i] === ',') i++
      else if (s[i] === '}') {
        i++
        return items
      } else return null
    }
  }
  if (s[0] !== '{') return null
  const items = list()
  space()
  return items && i === s.length ? items : null
}

const JSON_NUMBER = /^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?$/

function itemJson(item: Item, element: ArrayElement): string {
  if (Array.isArray(item)) return `[${item.map((x) => itemJson(x, element)).join(', ')}]`
  if (!item.quoted && item.text.toUpperCase() === 'NULL') return 'null'
  if (element === 'number' && JSON_NUMBER.test(item.text)) return item.text // as written: 1.50 stays 1.50
  if (element === 'bool' && (item.text === 't' || item.text === 'f')) return item.text === 't' ? 'true' : 'false'
  return JSON.stringify(item.text) // NaN, Infinity and every other value stay text
}

// A Postgres array value as JSON text, or null when it can't be read as one
// (the cell then edits as plain text).
export function arrayToJson(value: string, element: ArrayElement): string | null {
  const items = parseArray(value)
  return items ? itemJson(items, element) : null
}

// Quoted the way Postgres prints array elements: only when needed.
function quote(text: string) {
  if (text !== '' && text.toUpperCase() !== 'NULL' && !/[{}",\\\s]/.test(text)) return text
  return `"${text.replace(/[\\"]/g, '\\$&')}"`
}

// JSON text from the editor back to a Postgres array value, or a message
// saying what's wrong with it. Nested arrays are dimensions, so they must
// all have the same length; objects (for json[] / jsonb[]) are stored as
// their JSON text.
export function jsonToArray(text: string, element: ArrayElement): { value: string } | { error: string } {
  let json: unknown
  try {
    json = parse(text)
  } catch (err) {
    return { error: `Not valid JSON: ${err instanceof Error ? err.message : String(err)}` }
  }
  if (!Array.isArray(json)) return { error: 'An array column needs a JSON array, like ["a", "b"].' }

  const shape = (value: unknown[]): string | null => {
    const nested = value.filter(Array.isArray)
    if (nested.length === 0) return null
    if (nested.length !== value.length) return 'Mix of arrays and single values: a nested array is a dimension, so every item at that level must be an array.'
    const length = nested[0].length
    if (nested.some((a) => a.length !== length)) return 'Nested arrays must all have the same length (Postgres arrays are rectangular).'
    for (const a of nested) {
      const problem = shape(a)
      if (problem) return problem
    }
    return null
  }
  const problem = shape(json)
  if (problem) return { error: problem }

  const literal = (value: unknown): string => {
    if (value === null) return 'NULL'
    if (Array.isArray(value)) return `{${value.map(literal).join(',')}}`
    if (isLosslessNumber(value)) return quote(value.value)
    if (typeof value === 'boolean') return element === 'bool' ? (value ? 't' : 'f') : String(value)
    if (typeof value === 'string') return quote(value)
    return quote(stringify(value) ?? '') // an object: its JSON, numbers as written
  }
  return { value: literal(json) }
}
