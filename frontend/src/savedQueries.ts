// Saved-query list helpers: search, a suggested name, newest-first order.
// No React or Wails imports, so the unit tests can load it.

type Query = { id: string; name: string; sql: string; database: string; updatedAt: number }

// Case-insensitive match anywhere in the name, the SQL or the database label.
export function filterQueries<Q extends Query>(queries: Q[], search: string): Q[] {
  const q = search.trim().toLowerCase()
  if (!q) return queries
  return queries.filter((x) => [x.name, x.sql, x.database].some((t) => t.toLowerCase().includes(q)))
}

// The name the Save dialog starts with: the SQL's first line, squeezed and
// cut to 48 characters ("SELECT * FROM arn WHERE …"), or the fallback.
export function suggestName(sql: string, fallback: string) {
  const line = sql.split('\n').map((l) => l.replace(/\s+/g, ' ').trim()).find((l) => l && !l.startsWith('--'))
  if (!line) return fallback
  return line.length > 48 ? `${line.slice(0, 47).trimEnd()}…` : line
}

// The list with q added or replaced, newest first (as Go stores it).
export function upsertQuery<Q extends Query>(queries: Q[], q: Q): Q[] {
  return [q, ...queries.filter((x) => x.id !== q.id)].sort((a, b) => b.updatedAt - a.updatedAt)
}
