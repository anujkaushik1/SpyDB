import { useState } from 'react'
import type { ConnectionLists, SavedConnection } from '../bindings/github.com/anujkaushik1/SpyDB'
import { DatabaseIcon, PlusIcon, SearchIcon, TrashIcon } from './icons'
import { SidebarResizer, useSidebarWidth } from './resize'
import { timeAgo } from './timeAgo'

const target = (c: SavedConnection) => `${c.config.host}:${c.config.port}/${c.config.database || c.config.user}`

type Props = {
  lists: ConnectionLists | null // null until loaded
  error: string | null
  selectedId: string | null // the saved connection open in the form
  onNew: () => void
  onSelect: (connection: SavedConnection, saved: boolean) => void // fill the form
  onOpen: (connection: SavedConnection) => void // double-click: connect
  onDelete: (id: string) => void
  onClearRecent: () => void
}

// The disconnected screen's sidebar: saved and recent connections, read from
// ~/Library/Application Support/SpyDB/connections.json.
export default function ConnectionsSidebar({ lists, error, selectedId, onNew, onSelect, onOpen, onDelete, onClearRecent }: Props) {
  const [filter, setFilter] = useState('')
  const [deleting, setDeleting] = useState<string | null>(null) // asking "delete?" for this id
  const [width, setWidth] = useSidebarWidth()

  const query = filter.trim().toLowerCase()
  const matches = (c: SavedConnection) =>
    !query || `${c.name} ${c.config.host} ${c.config.database} ${c.config.user}`.toLowerCase().includes(query)
  const allSaved = lists?.saved ?? []
  const allRecent = lists?.recent ?? []
  const saved = allSaved.filter(matches)
  const recent = allRecent.filter(matches)

  return (
    <aside className="sidebar" aria-label="Connections" style={{ flexBasis: width }}>
      <div className="sidebar-new">
        <button type="button" className="btn btn-secondary new-connection-btn" onClick={onNew}>
          <PlusIcon /> New connection
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
        {error && <p className="tree-message tree-error">{error}</p>}

        <div className="conn-section-title">
          Saved {lists && <span className="sidebar-count">{allSaved.length}</span>}
        </div>
        {lists && saved.length === 0 && (
          <p className="tree-message">
            {query ? 'No saved connections match.' : 'None yet. Fill in the form and click Save to keep a connection here.'}
          </p>
        )}
        <ul className="conn-list">
          {saved.map((c) => (
            <li key={c.id}>
              {deleting === c.id ? (
                <div className="conn-item confirm">
                  <span className="conn-confirm-text">Delete “{c.name}”?</span>
                  <button type="button" className="link-btn danger-link" onClick={() => { onDelete(c.id); setDeleting(null) }}>Delete</button>
                  <button type="button" className="link-btn" onClick={() => setDeleting(null)}>Cancel</button>
                </div>
              ) : (
                <div
                  role="button"
                  tabIndex={0}
                  className={c.id === selectedId ? 'conn-item active' : 'conn-item'}
                  onClick={() => onSelect(c, true)}
                  onDoubleClick={() => onOpen(c)}
                  onKeyDown={(e) => { if (e.key === 'Enter') onOpen(c) }}
                  title={`${target(c)} · ${c.config.user} · last used ${timeAgo(c.lastUsed)} — double-click to connect`}
                >
                  <span className="conn-icon"><DatabaseIcon /></span>
                  <span className="conn-text">
                    <span className="conn-name">{c.name}</span>
                    <span className="conn-sub">{target(c)}</span>
                  </span>
                  <button type="button" className="conn-delete" aria-label={`Delete ${c.name}`} title="Delete"
                          onClick={(e) => { e.stopPropagation(); setDeleting(c.id) }}>
                    <TrashIcon />
                  </button>
                </div>
              )}
            </li>
          ))}
        </ul>

        <div className="conn-section-title">
          Recent {lists && <span className="sidebar-count">{allRecent.length}</span>}
          {allRecent.length > 0 && (
            <button type="button" className="link-btn conn-clear" onClick={onClearRecent}>Clear</button>
          )}
        </div>
        {lists && recent.length === 0 && (
          <p className="tree-message">{query ? 'No recent connections match.' : 'Connections you make appear here.'}</p>
        )}
        <ul className="conn-list">
          {recent.map((c) => (
            <li key={c.id}>
              <div
                role="button"
                tabIndex={0}
                className="conn-item"
                onClick={() => onSelect(c, false)}
                onDoubleClick={() => onOpen(c)}
                onKeyDown={(e) => { if (e.key === 'Enter') onOpen(c) }}
                title={`${target(c)} · ${c.config.user} — double-click to connect`}
              >
                <span className="conn-icon recent"><DatabaseIcon /></span>
                <span className="conn-text">
                  <span className="conn-name">{c.config.database || c.config.user} <span className="conn-at">@ {c.config.host}</span></span>
                  <span className="conn-sub">{c.config.user} · {timeAgo(c.lastUsed)}</span>
                </span>
              </div>
            </li>
          ))}
        </ul>
      </div>
      <SidebarResizer width={width} onResize={setWidth} />
    </aside>
  )
}
