// Matching a file's columns to a table's, for the import dialog. No React or
// Wails imports, so the unit tests can load it.

export type TargetColumn = { name: string; type: string; required: boolean; generated: boolean }

// The types a new table's columns can have; the same list as importTypes in Go.
export const IMPORT_TYPES = [
  'text', 'bigint', 'integer', 'numeric', 'double precision', 'boolean',
  'date', 'timestamp', 'timestamptz', 'time', 'jsonb', 'uuid',
]

// "Full Name", "full_name" and "FULLNAME" all compare as "fullname".
const squash = (s: string) => s.toLowerCase().replace(/[^\p{L}\p{N}]/gu, '')

// For each file column, the table column it most likely fills ('' for none):
// same name ignoring case, spaces and punctuation, each table column used
// once, never a column Postgres fills itself.
export function suggestTargets(headers: string[], columns: TargetColumn[]): string[] {
  const free = new Map<string, string>()
  for (const c of columns) {
    if (!c.generated && !free.has(squash(c.name))) free.set(squash(c.name), c.name)
  }
  return headers.map((h) => {
    const key = squash(h)
    const match = key ? free.get(key) : undefined
    if (!match) return ''
    free.delete(key)
    return match
  })
}

// Table columns every row needs (NOT NULL, no default) that no file column fills.
export function missingRequired(targets: string[], columns: TargetColumn[]): string[] {
  const filled = new Set(targets.filter(Boolean))
  return columns.filter((c) => c.required && !filled.has(c.name)).map((c) => c.name)
}

// Problems with a new table's column names, or null when they're fine.
export function nameProblem(names: string[]): string | null {
  const seen = new Set<string>()
  for (const name of names) {
    if (!name.trim()) return 'Every column you import needs a name.'
    if (seen.has(name)) return `Two columns are both named “${name}”.`
    seen.add(name)
  }
  return names.length === 0 ? 'Choose at least one column to import.' : null
}
