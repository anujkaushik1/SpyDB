import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import type { SavedQuery } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import { errorMessage } from './Banner'
import { CodeIcon, PencilIcon, RefreshIcon, SearchIcon, Spinner, TrashIcon } from './icons'
import { shortcut } from './platform'
import { filterQueries } from './savedQueries'
import { Highlight } from './tableMeta'
import { timeAgo } from './timeAgo'

type Props = {
  queries: SavedQuery[] | null // null until loaded
  error: string | null
  loading: boolean
  currentDatabase: string
  onRefresh: () => void
  onOpen: (query: SavedQuery) => void
  onRename: (id: string, name: string) => Promise<void> // throws to show the error
  onDelete: (id: string) => Promise<void>
}

// The sidebar's Saved queries view: every saved query, newest first, with
// its database as a label. Click (or Enter) opens it in a query tab.
export default function SavedQueriesPanel({ queries, error, loading, currentDatabase, onRefresh, onOpen, onRename, onDelete }: Props) {
  const [search, setSearch] = useState('')
  const [highlight, setHighlight] = useState(0)
  const [renaming, setRenaming] = useState<{ id: string; name: string } | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const list = useRef<HTMLUListElement>(null)

  const shown = filterQueries(queries ?? [], search)
  const query = search.trim().toLowerCase()

  useEffect(() => setHighlight(0), [search])
  useEffect(() => {
    list.current?.querySelector('.sq-item.highlighted')?.scrollIntoView({ block: 'nearest' })
  }, [highlight])

  const onSearchKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      const step = e.key === 'ArrowDown' ? 1 : -1
      setHighlight((h) => Math.min(Math.max(h + step, 0), Math.max(shown.length - 1, 0)))
    } else if (e.key === 'Enter' && shown[highlight]) {
      e.preventDefault()
      onOpen(shown[highlight])
    } else if (e.key === 'Escape') {
      setSearch('')
    }
  }

  const act = async (fn: () => Promise<void>) => {
    setActionError(null)
    try {
      await fn()
    } catch (err) {
      setActionError(errorMessage(err))
    }
  }

  const finishRename = () => {
    if (!renaming) return
    const { id, name } = renaming
    setRenaming(null)
    const before = queries?.find((q) => q.id === id)?.name
    if (name.trim() && name.trim() !== before) act(() => onRename(id, name.trim()))
  }

  return (
    <>
      <div className="sidebar-header">
        <span className="sidebar-title">
          Saved queries
          {queries && <span className="sidebar-count">{queries.length}</span>}
        </span>
        <button type="button" className="sidebar-icon-btn" onClick={onRefresh} disabled={loading}
                aria-label="Refresh saved queries" title="Refresh">
          {loading ? <Spinner /> : <RefreshIcon />}
        </button>
      </div>

      <div className="sidebar-search">
        <SearchIcon />
        <input type="text" value={search} onChange={(e) => setSearch(e.target.value)} onKeyDown={onSearchKeyDown}
               placeholder="Search saved queries" aria-label="Search saved queries"
               spellCheck={false} autoCapitalize="off" autoCorrect="off" autoComplete="off" />
      </div>

      <div className="tree">
        {error && <p className="tree-message tree-error">{error} <button type="button" className="link-btn" onClick={onRefresh}>Retry</button></p>}
        {actionError && <p className="tree-message tree-error">{actionError}</p>}
        {!error && !queries && <p className="tree-message">Loading…</p>}
        {queries && queries.length === 0 && (
          <p className="tree-message">
            No saved queries yet. Open a query tab (Ctrl T), write some SQL and click <strong>Save</strong> ({shortcut('S')}).
          </p>
        )}
        {queries && queries.length > 0 && shown.length === 0 && <p className="tree-message">No saved queries match “{search.trim()}”</p>}

        <ul className="sq-list" ref={list}>
          {shown.map((q, i) => {
            if (deleting === q.id) {
              return (
                <li key={q.id} className="sq-item confirm">
                  <span className="sq-confirm-text">Delete “{q.name}”?</span>
                  <button type="button" className="link-btn danger-link" onClick={() => { setDeleting(null); act(() => onDelete(q.id)) }}>Delete</button>
                  <button type="button" className="link-btn" onClick={() => setDeleting(null)}>Cancel</button>
                </li>
              )
            }
            if (renaming?.id === q.id) {
              return (
                <li key={q.id} className="sq-item renaming">
                  <input className="sq-rename" value={renaming.name} autoFocus aria-label="New name" spellCheck={false}
                         onChange={(e) => setRenaming({ id: q.id, name: e.target.value })}
                         onFocus={(e) => e.currentTarget.select()}
                         onBlur={finishRename}
                         onKeyDown={(e) => {
                           if (e.key === 'Enter') { e.preventDefault(); finishRename() }
                           else if (e.key === 'Escape') { e.preventDefault(); setRenaming(null) }
                         }} />
                </li>
              )
            }
            const here = q.database !== '' && q.database === currentDatabase
            return (
              <li key={q.id} className={i === highlight ? 'sq-item highlighted' : 'sq-item'}
                  onMouseMove={() => setHighlight(i)}>
                <button type="button" className="sq-open" onClick={() => onOpen(q)}
                        title={`${q.name}\n${q.database ? `${q.database} on ${q.host}` : 'no database'} · saved ${timeAgo(q.updatedAt)}\n\n${q.sql.slice(0, 400)}`}>
                  <span className="sq-icon"><CodeIcon /></span>
                  <span className="sq-text">
                    <span className="sq-name"><Highlight text={q.name} query={query} /></span>
                    <span className="sq-meta">
                      <span className={here ? 'sq-db here' : 'sq-db'}>{q.database || 'no database'}</span>
                      <span> · {timeAgo(q.updatedAt)}</span>
                    </span>
                  </span>
                </button>
                <button type="button" className="sq-action" aria-label={`Rename ${q.name}`} title="Rename"
                        onClick={() => setRenaming({ id: q.id, name: q.name })}><PencilIcon /></button>
                <button type="button" className="sq-action danger" aria-label={`Delete ${q.name}`} title="Delete"
                        onClick={() => setDeleting(q.id)}><TrashIcon /></button>
              </li>
            )
          })}
        </ul>
      </div>
    </>
  )
}
