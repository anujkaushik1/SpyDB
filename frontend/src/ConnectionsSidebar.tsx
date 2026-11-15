import { useState, type ReactNode } from 'react'
import type { AWSProfile } from '../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import type { ConnectionLists, SavedConnection } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import { DynamoLogo, PlusIcon, PostgresLogo, SearchIcon, TrashIcon } from './icons'
import { SidebarResizer, useSidebarWidth } from './resize'
import { timeAgo } from './timeAgo'

export type Engine = 'postgres' | 'dynamo'

// The engines, in the order the rail and this sidebar show them.
export const ENGINES: { id: Engine; label: string; short: string; logo: () => ReactNode }[] = [
  { id: 'postgres', label: 'PostgreSQL', short: 'Pg', logo: PostgresLogo },
  { id: 'dynamo', label: 'DynamoDB', short: 'Dy', logo: DynamoLogo },
]

const target = (c: SavedConnection) => `${c.config.host}:${c.config.port}/${c.config.database || c.config.user}`

type Props = {
  engine: Engine // the one whose form is shown; New connection is for it
  // PostgreSQL's saved connections (connections.json), and the one open in the form.
  postgres: { lists: ConnectionLists | null; error: string | null; selectedId: string | null }
  // DynamoDB signs in with an AWS profile from ~/.aws: those are its "saved connections".
  dynamo: { profiles: AWSProfile[] | null; error: string | null; selected: string }
  onNew: () => void
  // A click fills that engine's form (showing it); a double-click or Enter connects.
  onPickPostgres: (connection: SavedConnection, connect: boolean) => void
  onPickDynamo: (profile: string, connect: boolean) => void
  onDeletePostgres: (id: string) => void
}

// The connect screen's sidebar: every saved connection, under its database's
// heading (PostgreSQL, DynamoDB). Click one to see it in its form,
// double-click to connect. New connection makes one for the database shown.
export default function ConnectionsSidebar({ engine, postgres, dynamo, onNew, onPickPostgres, onPickDynamo, onDeletePostgres }: Props) {
  const [filter, setFilter] = useState('')
  const [deleting, setDeleting] = useState<string | null>(null) // asking "delete?" for this id
  const [width, setWidth] = useSidebarWidth()
  const query = filter.trim().toLowerCase()
  const matches = (text: string) => !query || text.toLowerCase().includes(query)

  const pg = (postgres.lists?.saved ?? []).filter((c) => matches(`${c.name} ${c.config.host} ${c.config.database} ${c.config.user}`))
  const profiles = (dynamo.profiles ?? []).filter((p) => matches(`${p.name} ${p.region} ${p.kind}`))
  const loaded = { postgres: postgres.lists !== null, dynamo: dynamo.profiles !== null }
  const counts = { postgres: postgres.lists?.saved?.length ?? 0, dynamo: dynamo.profiles?.length ?? 0 }
  const shown = { postgres: pg.length, dynamo: profiles.length }
  const errors = { postgres: postgres.error, dynamo: dynamo.error }
  const current = ENGINES.find((e) => e.id === engine)!

  const item = (key: string, active: boolean, engineId: Engine, name: ReactNode, sub: string, title: string, pick: (connect: boolean) => void, onDelete?: () => void) => (
    <li key={key}>
      {onDelete && deleting === key ? (
        <div className="conn-item confirm">
          <span className="conn-confirm-text">Delete “{name}”?</span>
          <button type="button" className="link-btn danger-link" onClick={() => { onDelete(); setDeleting(null) }}>Delete</button>
          <button type="button" className="link-btn" onClick={() => setDeleting(null)}>Cancel</button>
        </div>
      ) : (
        <div role="button" tabIndex={0} className={active ? 'conn-item active' : 'conn-item'}
             onClick={() => pick(false)} onDoubleClick={() => pick(true)}
             onKeyDown={(e) => { if (e.key === 'Enter') pick(true) }} title={`${title} — double-click to connect`}>
          <span className={`conn-icon conn-icon-${engineId}`}>{ENGINES.find((e) => e.id === engineId)!.logo()}</span>
          <span className="conn-text">
            <span className="conn-name">{name}</span>
            <span className="conn-sub">{sub}</span>
          </span>
          {onDelete && (
            <button type="button" className="conn-delete" aria-label={`Delete ${name}`} title="Delete"
                    onClick={(e) => { e.stopPropagation(); setDeleting(key) }}>
              <TrashIcon />
            </button>
          )}
        </div>
      )}
    </li>
  )

  const section = (e: typeof ENGINES[number], list: ReactNode[], empty: string) => (
    <section key={e.id} className={engine === e.id ? 'conn-group current' : 'conn-group'} aria-label={`${e.label} connections`}>
      <div className="conn-section-title">
        <span className={`conn-group-logo conn-icon-${e.id}`}>{e.logo()}</span>
        {e.label}
        {loaded[e.id] && <span className="sidebar-count">{counts[e.id]}</span>}
      </div>
      {errors[e.id] && <p className="tree-message tree-error">{errors[e.id]}</p>}
      {loaded[e.id] && shown[e.id] === 0 && !errors[e.id] && (
        <p className="tree-message">{query ? `No ${e.label} connections match.` : empty}</p>
      )}
      <ul className="conn-list">{list}</ul>
    </section>
  )

  return (
    <aside className="sidebar" aria-label="Connections" style={{ flexBasis: width }}>
      <div className="sidebar-new">
        <button type="button" className="btn btn-secondary new-connection-btn" onClick={onNew} title={`A new ${current.label} connection`}>
          <PlusIcon /> New connection <span className={`new-connection-logo conn-icon-${engine}`}>{current.logo()}</span>
        </button>
      </div>

      <div className="sidebar-search">
        <SearchIcon />
        <input type="text" value={filter} onChange={(e) => setFilter(e.target.value)}
               onKeyDown={(e) => { if (e.key === 'Escape') setFilter('') }}
               placeholder="Filter connections" aria-label="Filter connections"
               spellCheck={false} autoCapitalize="off" autoCorrect="off" autoComplete="off" />
      </div>

      <div className="tree">
        {section(ENGINES[0], pg.map((c) =>
          item(`pg:${c.id}`, c.id === postgres.selectedId && engine === 'postgres', 'postgres', c.name, target(c),
            `${target(c)} · ${c.config.user} · last used ${timeAgo(c.lastUsed)}`, (connect) => onPickPostgres(c, connect), () => onDeletePostgres(c.id))),
          'None yet. Fill in the PostgreSQL form and click Save to keep a connection here.')}
        {section(ENGINES[1], profiles.map((p) =>
          item(`aws:${p.name}`, p.name === dynamo.selected && engine === 'dynamo', 'dynamo', p.name,
            `${p.region || 'no region set'} · ${PROFILE_KINDS[p.kind] ?? p.kind}`, `AWS profile ${p.name}`, (connect) => onPickDynamo(p.name, connect))),
          'No AWS profiles yet: sign in with your browser from the DynamoDB screen, or run aws configure.')}
      </div>
      <SidebarResizer width={width} onResize={setWidth} />
    </aside>
  )
}

// What each kind of AWS profile signs in with (AWSProfile.kind).
const PROFILE_KINDS: Record<string, string> = { keys: 'access keys', sso: 'AWS SSO', login: 'browser sign-in', role: 'role', process: 'credential program' }
