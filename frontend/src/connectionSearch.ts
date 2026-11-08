// The top-left switcher's lists: which databases and saved connections
// match the search, and which connection is the current one.
// No React or Wails imports, so the unit tests can load it.

type Target = { host: string; port: number; user: string; database: string }
type Connection = { id: string; name: string; config: Target }
type Lists<C> = { saved: C[] | null }

// Same server, login and database; the password doesn't matter. Matches the
// Go side's sameTarget.
export function sameTarget(a: Target, b: Target) {
  return a.host === b.host && a.port === b.port && a.user === b.user && a.database === b.database
}

// Names a connection's workspace (its own set of tabs): one per server, login
// and database, e.g. "zf_admin@zf-cashier….amazonaws.com:5432/cashier".
export const targetKey = (t: Target) => `${t.user}@${t.host}:${t.port}/${t.database}`

// "Prod cashier", or "zf_admin@zf-cashier…" for a connection without a name.
export const connectionTitle = (c: Connection) => c.name.trim() || `${c.config.user}@${c.config.host}`

// "zf-cashier….amazonaws.com:5432 · cashier"
export const connectionDetail = (c: Connection) => `${c.config.host}:${c.config.port} · ${c.config.database}`

// Databases on this server and saved connections, each narrowed by the
// search (case-insensitive, anywhere in the name, host, host:port, database
// or user).
export function pickerSections<C extends Connection>(databases: string[], lists: Lists<C> | null, query: string) {
  const q = query.trim().toLowerCase()
  const hit = (...texts: string[]) => !q || texts.some((t) => t.toLowerCase().includes(q))
  const connectionHit = (c: C) =>
    hit(c.name, c.config.host, `${c.config.host}:${c.config.port}`, c.config.database, c.config.user)
  return {
    databases: databases.filter((d) => hit(d)),
    saved: (lists?.saved ?? []).filter(connectionHit),
  }
}
