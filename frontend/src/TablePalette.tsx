import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import type { SchemaInfo, TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import { SearchIcon, XIcon } from './icons'
import { compactCount, Highlight, KindIcon, kindClass } from './tableMeta'

const MAX_RESULTS = 50

// Best matches first: exact name, then name prefix, then anywhere in the name,
// then anywhere in "schema.name" (so "public.emp" also works).
function rank(tables: TableInfo[], query: string) {
  if (!query) return tables
  const scored: { table: TableInfo; score: number }[] = []
  for (const table of tables) {
    const name = table.name.toLowerCase()
    const score = name === query ? 0
      : name.startsWith(query) ? 1
      : name.includes(query) ? 2
      : `${table.schema}.${name}`.toLowerCase().includes(query) ? 3
      : -1
    if (score >= 0) scored.push({ table, score })
  }
  return scored.sort((a, b) => a.score - b.score || a.table.name.localeCompare(b.table.name)).map((s) => s.table)
}

type Props = {
  schemas: SchemaInfo[]
  onOpen: (table: TableInfo) => void
  onClose: () => void
}

// ⌘P / Ctrl+P: type part of a table name, pick with ↑ ↓, open with Enter.
export default function TablePalette({ schemas, onOpen, onClose }: Props) {
  const [query, setQuery] = useState('')
  const [index, setIndex] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLUListElement>(null)

  const q = query.trim().toLowerCase()
  const matches = rank(schemas.flatMap((s) => s.tables ?? []), q).slice(0, MAX_RESULTS)

  useEffect(() => { input.current?.focus() }, [])
  useEffect(() => setIndex(0), [q])
  useEffect(() => {
    list.current?.children[index]?.scrollIntoView({ block: 'nearest' })
  }, [index])

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setIndex((i) => Math.min(i + 1, matches.length - 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setIndex((i) => Math.max(i - 1, 0))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (matches[index]) onOpen(matches[index])
    } else if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    }
  }

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="palette" role="dialog" aria-label="Go to table" onMouseDown={(e) => e.stopPropagation()}>
        <div className="palette-search">
          <SearchIcon />
          <input
            ref={input}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="Go to table…"
            aria-label="Table name"
            aria-controls="palette-results"
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            autoComplete="off"
          />
          {query && (
            <button type="button" className="sidebar-icon-btn" aria-label="Clear" onClick={() => { setQuery(''); input.current?.focus() }}>
              <XIcon />
            </button>
          )}
        </div>

        <ul className="palette-list" id="palette-results" role="listbox" ref={list}>
          {matches.map((t, i) => (
            <li
              key={t.qualifiedName}
              role="option"
              aria-selected={i === index}
              className={i === index ? 'palette-item active' : 'palette-item'}
              onMouseMove={() => setIndex(i)}
              onClick={() => onOpen(t)}
            >
              <span className={`tree-icon ${kindClass(t.kind)}`}><KindIcon kind={t.kind} /></span>
              <span className="palette-name">
                <span className="palette-schema">{t.schema}.</span>
                <Highlight text={t.name} query={q} />
              </span>
              {t.kind !== 'table' && <span className="palette-kind">{t.kind}</span>}
              {t.estimatedRows != null && <span className="tree-rows">~{compactCount(t.estimatedRows)}</span>}
            </li>
          ))}
          {matches.length === 0 && <li className="palette-empty">No tables match “{query.trim()}”</li>}
        </ul>

        <div className="palette-footer">
          <span><kbd>↑</kbd> <kbd>↓</kbd> move</span>
          <span><kbd>↵</kbd> open</span>
          <span><kbd>esc</kbd> close</span>
        </div>
      </div>
    </div>
  )
}
