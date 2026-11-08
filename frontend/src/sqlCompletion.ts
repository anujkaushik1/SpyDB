// The SQL editor's autocomplete schema from DBService.ListColumns, in the
// shape @codemirror/lang-sql expects: { schema: { table: [columns] } }. With
// defaultSchema 'public', public tables also complete without "public.".
// No React or Wails imports, so the unit tests can load it.

type TableColumns = { schema: string; table: string; columns: string[] | null }
export type CompletionSchema = Record<string, Record<string, string[]>> // schema → table → columns

export function completionSchema(tables: TableColumns[]): CompletionSchema {
  const schema: CompletionSchema = {}
  for (const t of tables) (schema[t.schema] ??= {})[t.table] = t.columns ?? []
  return schema
}

// What the word at the cursor can be, from the SQL before it:
//   'qualified' after "name." (lang-sql completes that schema's tables or table's columns),
//   'table'     after FROM / JOIN / INTO / UPDATE / TABLE / TRUNCATE, or after a comma in a FROM list,
//   'general'   anywhere else.
export function completionContext(textBefore: string): 'qualified' | 'table' | 'general' {
  const before = textBefore.replace(/[\w$]*$/, '') // drop the word being typed
  if (/(?:^|[^\w$])(?:[A-Za-z_][\w$]*|"[^"]+")\s*\.\s*$/.test(before)) return 'qualified'
  if (/\b(from|join|into|update|table|truncate)\s+$/i.test(before)) return 'table'
  if (/\bfrom\s+(?:[\w$."]+(?:\s+(?:as\s+)?[\w$]+)?\s*,\s*)+$/i.test(before)) return 'table'
  return 'general'
}

// The SQL words worth suggesting: everyday statements, clauses, operators and
// functions, not Postgres's hundreds of keywords (those still get colours).
export const COMMON_KEYWORDS = [
  'select', 'from', 'where', 'and', 'or', 'not', 'null', 'is', 'in', 'like', 'ilike', 'between', 'exists', 'as',
  'distinct', 'join', 'left join', 'right join', 'inner join', 'full join', 'cross join', 'on', 'using',
  'group by', 'order by', 'having', 'limit', 'offset', 'asc', 'desc', 'nulls first', 'nulls last',
  'union', 'union all', 'intersect', 'except', 'case', 'when', 'then', 'else', 'end',
  'insert into', 'values', 'update', 'set', 'delete from', 'returning', 'on conflict', 'do nothing',
  'create table', 'create index', 'alter table', 'drop table', 'truncate', 'with', 'lateral', 'over', 'partition by',
  'count', 'sum', 'avg', 'min', 'max', 'coalesce', 'nullif', 'cast', 'lower', 'upper', 'length', 'trim',
  'now()', 'current_date', 'current_timestamp', 'interval', 'date_trunc', 'extract', 'to_char',
  'true', 'false', 'begin', 'commit', 'rollback', 'explain', 'explain analyze',
]

// Keywords in the case you're typing: "sel" → select, "SEL" → SELECT.
export function keywordLabels(typed: string) {
  const upper = /[A-Z]/.test(typed) && !/[a-z]/.test(typed)
  return COMMON_KEYWORDS.map((k) => (upper ? k.toUpperCase() : k))
}

const IDENT = String.raw`(?:"[^"]+"|[A-Za-z_][\w$]*)`
const REF = new RegExp(String.raw`^(${IDENT})(?:\s*\.\s*(${IDENT}))?(?:\s+(?:as\s+)?(${IDENT}))?`, 'i')
// Words that end a table reference rather than name an alias.
const NOT_ALIAS = new Set(['where', 'join', 'left', 'right', 'inner', 'full', 'cross', 'natural', 'lateral', 'on', 'using',
  'group', 'order', 'limit', 'offset', 'having', 'union', 'intersect', 'except', 'window', 'for', 'set', 'values',
  'select', 'returning', 'default', 'on'])
const unquote = (name: string) => (name.startsWith('"') ? name.slice(1, -1) : name.toLowerCase())

export type TableRef = { schema: string | null; table: string; alias: string | null }

// The tables a statement uses (after FROM, JOIN, UPDATE, INTO, and the rest
// of a FROM list), with their aliases. Unquoted names fold to lower case, as
// in Postgres. Subqueries are skipped.
export function statementTables(sql: string): TableRef[] {
  const refs: TableRef[] = []
  const starts = /\b(from|join|update|into)\s+/gi
  let start: RegExpExecArray | null
  while ((start = starts.exec(sql))) {
    let rest = sql.slice(starts.lastIndex)
    const list = start[1].toLowerCase() === 'from'
    for (;;) {
      const ref = REF.exec(rest)
      if (!ref) break
      const [whole, first, second, alias] = ref
      const isAlias = !!alias && !NOT_ALIAS.has(alias.toLowerCase())
      refs.push({ schema: second ? unquote(first) : null, table: unquote(second ?? first), alias: isAlias ? unquote(alias) : null })
      rest = rest.slice(isAlias || !alias ? whole.length : whole.length - alias.length)
      const comma = list ? /^\s*,\s*/.exec(rest) : null
      if (!comma) break
      rest = rest.slice(comma[0].length)
    }
  }
  return refs
}

// Columns of those tables, first table first, each name once; its table goes
// in `table` (shown next to the suggestion). Tables without a schema are
// looked up in public, then in any schema that has them.
export function statementColumns(schema: CompletionSchema, refs: TableRef[], defaultSchema = 'public') {
  const columns: { name: string; table: string }[] = []
  const seen = new Set<string>()
  for (const ref of refs) {
    const owner = ref.schema ?? (schema[defaultSchema]?.[ref.table] ? defaultSchema : Object.keys(schema).find((s) => schema[s][ref.table]))
    for (const name of (owner && schema[owner]?.[ref.table]) || []) {
      if (seen.has(name)) continue
      seen.add(name)
      columns.push({ name, table: ref.table })
    }
  }
  return columns
}
