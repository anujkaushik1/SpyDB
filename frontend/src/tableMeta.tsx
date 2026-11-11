import type { TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import { EyeIcon, LayersIcon, TableIcon } from './icons'

// Shared by the sidebar, the ⌘P / Ctrl+P switcher and the tab bar.

// 950 → "950", 1530 → "1.5k", 20003 → "20k", 1250000 → "1.3M"
export function compactCount(n: number) {
  const short = (value: number, unit: string) => `${value.toFixed(value < 10 ? 1 : 0).replace(/\.0$/, '')}${unit}`
  if (n < 1000) return String(n)
  if (n < 1_000_000) return short(n / 1000, 'k')
  return short(n / 1_000_000, 'M')
}

// "materialized view" → "kind-materialized-view", used for the icon colour.
export const kindClass = (kind: string) => `kind-${kind.replace(/ /g, '-')}`

export function KindIcon({ kind }: { kind: string }) {
  if (kind === 'view') return <EyeIcon />
  if (kind === 'materialized view') return <LayersIcon />
  return <TableIcon />
}

// Wraps the part of `text` that matches `query` (already lower-case) in <mark>.
export function Highlight({ text, query }: { text: string; query: string }) {
  const at = query ? text.toLowerCase().indexOf(query) : -1
  if (at < 0) return <>{text}</>
  return <>{text.slice(0, at)}<mark>{text.slice(at, at + query.length)}</mark>{text.slice(at + query.length)}</>
}

export const tableTooltip = (t: TableInfo) =>
  `${t.schema}.${t.name} · ${t.kind}${t.estimatedRows != null ? ` · about ${t.estimatedRows.toLocaleString()} rows` : ''}`

// Grid cell colouring, shared by table tabs and SQL results. `category` is
// number, bool, time, json, text or other (TableColumn.category in Go).
const NUMBER_TYPES = new Set(['int2', 'int4', 'int8', 'numeric', 'float4', 'float8', 'money', 'oid'])
const TIME_TYPES = new Set(['date', 'time', 'timetz', 'timestamp', 'timestamptz', 'interval'])

// SQL results only carry the type name (e.g. "int4"); map it to a category.
export function typeCategory(type: string) {
  if (NUMBER_TYPES.has(type)) return 'number'
  if (TIME_TYPES.has(type)) return 'time'
  if (type === 'bool') return 'bool'
  if (type === 'json' || type === 'jsonb') return 'json'
  return 'text'
}

// Kept in cellValues.ts (no React) so tests can load them; re-exported for existing imports.
export { cellClass, cellText } from './cellValues'
