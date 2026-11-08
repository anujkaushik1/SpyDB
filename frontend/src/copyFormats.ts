import { cellText } from './cellValues'

export type CopyFormat = 'json' | 'csv' | 'text' | 'markdown'
type Column = { name: string; category: string }
type Row = (string | null)[]

// Rows as text in the chosen format. Values are Postgres text output; NULL is
// null in JSON, "NULL" in Markdown and an empty field in CSV / text.
export function formatRows(format: CopyFormat, columns: Column[], rows: Row[]): string {
  switch (format) {
    case 'json': return toJson(columns, rows)
    case 'csv': return delimited(',', columns, rows)
    case 'text': return delimited('\t', columns, rows)
    case 'markdown': return toMarkdown(columns, rows)
  }
}

// One cell as you'd paste it: what the grid shows, NULL as empty.
export function formatValue(value: string | null, category: string) {
  return value === null ? '' : cellText(value, category)
}

// Header row then data rows. A field with the delimiter, a quote or a line
// break is quoted, with quotes doubled; Excel and Sheets read both forms.
function delimited(delimiter: string, columns: Column[], rows: Row[]) {
  const field = (text: string) =>
    text.includes(delimiter) || /["\r\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text
  const lines = [
    columns.map((c) => field(c.name)),
    ...rows.map((row) => row.map((v, j) => field(formatValue(v, columns[j].category)))),
  ]
  return lines.map((cells) => cells.join(delimiter)).join('\n')
}

const JSON_NUMBER = /^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?$/

// An array of objects keyed by column name. Numbers keep their exact digits
// (1000.00 stays 1000.00, big ids don't lose precision), booleans become
// true/false and json / jsonb values are embedded as JSON.
function toJson(columns: Column[], rows: Row[]) {
  if (rows.length === 0) return '[]'
  return `[\n${jsonObjects(columns, rows, '  ').join(',\n')}\n]`
}

// One row as a JSON object, or several as an array: the grid's "View as JSON".
export function rowsAsJson(columns: Column[], rows: Row[]) {
  return rows.length === 1 ? jsonObjects(columns, rows, '')[0] : toJson(columns, rows)
}

// The JSON keys for these columns: their names, with duplicates numbered.
export const jsonKeys = (columns: Column[]) => uniqueNames(columns.map((c) => c.name))

function jsonObjects(columns: Column[], rows: Row[], indent: string) {
  const keys = jsonKeys(columns)
  return rows.map((row) => {
    const fields = row.map((v, j) => `${indent}  ${JSON.stringify(keys[j])}: ${jsonLiteral(v, columns[j].category)}`)
    return `${indent}{\n${fields.join(',\n')}\n${indent}}`
  })
}

// One Postgres text value as a JSON literal: number, true / false, null,
// embedded json, or a string.
export function jsonLiteral(value: string | null, category: string) {
  if (value === null) return 'null'
  if (category === 'number' && JSON_NUMBER.test(value)) return value
  if (category === 'bool' && (value === 't' || value === 'f')) return value === 't' ? 'true' : 'false'
  if (category === 'json') {
    try {
      JSON.parse(value)
      return value.trim()
    } catch {
      // not valid JSON after all: fall through to a string
    }
  }
  return JSON.stringify(value)
}

// A Markdown table; pipes are escaped and line breaks become <br>.
function toMarkdown(columns: Column[], rows: Row[]) {
  const cell = (text: string) => text.replace(/\|/g, '\\|').replace(/\r?\n/g, '<br>')
  const line = (cells: string[]) => `| ${cells.join(' | ')} |`
  return [
    line(columns.map((c) => cell(c.name))),
    line(columns.map(() => '---')),
    ...rows.map((row) => line(row.map((v, j) => (v === null ? 'NULL' : cell(cellText(v, columns[j].category)))))),
  ].join('\n')
}

// A query can return two columns with the same name (a.id, b.id); JSON keys
// must differ, so later ones get _2, _3, …
function uniqueNames(names: string[]) {
  const seen = new Map<string, number>()
  return names.map((name) => {
    const n = (seen.get(name) ?? 0) + 1
    seen.set(name, n)
    return n === 1 ? name : `${name}_${n}`
  })
}
