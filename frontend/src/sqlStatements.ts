// Splits SQL into statements for "run the statement under the cursor"
// (⌘⇧↵ / Ctrl+Shift+Enter): on semicolons outside quoted strings and
// identifiers, dollar-quoted bodies ($$ … $$) and comments. No React or
// Wails imports, so the unit tests can load it.

export type Statement = { from: number; to: number; text: string } // [from, to) in the SQL; text includes its ;

export function splitStatements(sql: string): Statement[] {
  const statements: Statement[] = []
  const add = (start: number, end: number) => {
    const text = sql.slice(start, end)
    const lead = text.length - text.trimStart().length
    if (text.trim() !== '' && text.trim() !== ';') statements.push({ from: start + lead, to: end, text: text.trim() })
  }
  let start = 0
  let i = 0
  while (i < sql.length) {
    const c = sql[i]
    const next = sql[i + 1]
    if (c === "'" || c === '"') {
      // A quoted string or identifier; a doubled quote is an escaped one.
      i++
      while (i < sql.length) {
        if (sql[i] === c && sql[i + 1] === c) i += 2
        else if (sql[i] === c) { i++; break }
        else i++
      }
    } else if (c === '-' && next === '-') {
      while (i < sql.length && sql[i] !== '\n') i++
    } else if (c === '/' && next === '*') {
      // Block comments nest in Postgres.
      let depth = 1
      i += 2
      while (i < sql.length && depth > 0) {
        if (sql[i] === '/' && sql[i + 1] === '*') { depth++; i += 2 }
        else if (sql[i] === '*' && sql[i + 1] === '/') { depth--; i += 2 }
        else i++
      }
    } else if (c === '$' && /^\$([A-Za-z_][A-Za-z0-9_]*)?\$/.test(sql.slice(i, i + 64))) {
      const tag = /^\$([A-Za-z_][A-Za-z0-9_]*)?\$/.exec(sql.slice(i, i + 64))![0]
      const end = sql.indexOf(tag, i + tag.length)
      i = end < 0 ? sql.length : end + tag.length
    } else if (c === ';') {
      add(start, i + 1)
      start = i + 1
      i++
    } else {
      i++
    }
  }
  add(start, sql.length)
  return statements
}

// The statement the cursor is in or just after (e.g. right after its ;, or on
// the blank line below it); before the first one, the first. null if none.
export function statementAt(sql: string, cursor: number): Statement | null {
  const statements = splitStatements(sql)
  if (statements.length === 0) return null
  let pick = statements[0]
  for (const s of statements) {
    if (s.from <= cursor) pick = s
    else break
  }
  return pick
}
