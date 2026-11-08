import type { DBConfig } from '../bindings/github.com/anujkaushik1/SpyDB'

// Query parameters that set connection fields (libpq allows these in the URL).
const FIELD_PARAMS: Record<string, keyof DBConfig> = {
  host: 'host',
  port: 'port',
  user: 'user',
  password: 'password',
  dbname: 'database',
}

export type ParsedUrl = { config: DBConfig; ignored: string[] }

// Decodes %xx escapes; a stray % (an unescaped password) is kept as typed.
function decode(part: string) {
  try {
    return decodeURIComponent(part)
  } catch {
    return part
  }
}

// Parses postgres://user:password@host:port/database?params into the form's
// fields. Parsed by hand rather than with new URL(), because pasted URLs often
// carry unescaped passwords: the last "@" ends the login, so a password may
// contain "@", ":" or "/". Unknown parameters (pool_size, TrustServerCertificate,
// sslmode, …) are listed in `ignored`. Throws with a readable message.
export function parseConnectionUrl(text: string): ParsedUrl {
  const trimmed = text.trim()
  const scheme = /^(postgres|postgresql):\/\//i.exec(trimmed)
  if (!scheme) throw new Error('Paste a URL that starts with postgres:// or postgresql://')

  let rest = trimmed.slice(scheme[0].length)
  let query = ''
  const q = rest.indexOf('?')
  if (q >= 0) {
    query = rest.slice(q + 1)
    rest = rest.slice(0, q)
  }

  const config: DBConfig = { host: '', port: 5432, user: '', password: '', database: '' }

  const at = rest.lastIndexOf('@')
  if (at >= 0) {
    const login = rest.slice(0, at)
    const colon = login.indexOf(':')
    config.user = decode(colon >= 0 ? login.slice(0, colon) : login)
    config.password = colon >= 0 ? decode(login.slice(colon + 1)) : ''
    rest = rest.slice(at + 1)
  }

  const slash = rest.indexOf('/')
  const address = slash >= 0 ? rest.slice(0, slash) : rest
  if (slash >= 0) config.database = decode(rest.slice(slash + 1))

  // host, host:port, [ipv6] or [ipv6]:port
  const bracket = /^\[([^\]]+)\](?::(\d+))?$/.exec(address)
  const plain = /^([^:]*)(?::(\d*))?$/.exec(address)
  const match = bracket ?? plain
  if (!match) throw new Error(`Couldn’t read the host from “${address}”.`)
  config.host = decode(match[1])
  if (match[2]) config.port = Number(match[2])

  const ignored: string[] = []
  for (const pair of query.split('&').filter(Boolean)) {
    const eq = pair.indexOf('=')
    const key = decode(eq >= 0 ? pair.slice(0, eq) : pair)
    const value = decode(eq >= 0 ? pair.slice(eq + 1) : '')
    const field = FIELD_PARAMS[key.toLowerCase()]
    if (!field) ignored.push(key)
    else if (field === 'port') config.port = Number(value)
    else config[field] = value
  }

  if (!config.host) throw new Error('The URL has no host.')
  if (!Number.isInteger(config.port) || config.port < 1 || config.port > 65535) {
    throw new Error('The URL’s port must be a number from 1 to 65535.')
  }
  return { config, ignored }
}
