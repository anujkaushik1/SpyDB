// Names for MongoDB connections: what a connection reaches (from its fields
// or its connection string), and the workspace its tabs belong to. Mirrors
// Go's Config.target. No React or Wails imports, so the unit tests can load it.

type Config = { uri: string; host: string; port: number; user: string; database: string }
type Info = { host: string; user: string; database: string }

// A connection's hosts, user and database: "mongodb+srv://ana:pw@cluster0.ab12.mongodb.net/shop?…"
// gives cluster0.ab12.mongodb.net, ana, shop. The form's Database wins over the string's.
export function mongoTarget(c: Config): { hosts: string; user: string; database: string } {
  const uri = c.uri.trim()
  if (!uri) return { hosts: `${c.host.trim()}:${c.port}`, user: c.user, database: c.database }
  let rest = uri.replace(/^[a-z+]+:\/\//i, '').split('?')[0]
  let user = ''
  const at = rest.lastIndexOf('@')
  if (at >= 0) {
    user = decode(rest.slice(0, at).split(':')[0])
    rest = rest.slice(at + 1)
  }
  const slash = rest.indexOf('/')
  const hosts = slash < 0 ? rest : rest.slice(0, slash)
  const database = c.database || (slash < 0 ? '' : decode(rest.slice(slash + 1)))
  return { hosts, user, database }
}

const decode = (s: string) => {
  try {
    return decodeURIComponent(s)
  } catch {
    return s
  }
}

// "ana@cluster0.ab12.mongodb.net/shop", or "localhost:27017/shop" without a user.
export function mongoLabel(c: Config): string {
  const { hosts, user, database } = mongoTarget(c)
  return `${user ? `${user}@` : ''}${hosts}${database ? `/${database}` : ''}`
}

// The workspace of a MongoDB connection's tabs: one per server, user and database.
export const mongoWorkspace = (info: Info) => `mongo|${info.user}@${info.host}/${info.database}`

// A connection string with its password hidden, for showing.
export const hidePassword = (uri: string) => uri.replace(/^([a-z+]+:\/\/[^:@/]+):[^@/]*@/i, '$1:•••@')
