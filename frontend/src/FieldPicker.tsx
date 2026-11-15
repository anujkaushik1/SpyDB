import { useEffect, useRef, useState, type ReactNode } from 'react'
import { fuzzyRank } from './fuzzy'
import { ChevronDownIcon, KeyIcon, SearchIcon } from './icons'
import { useDismiss } from './useDismiss'

export type FieldOption = {
  name: string
  note?: string // shown after the name, small: "table key", "string"
  key?: boolean // a table or index key: gets the key icon
  group?: string // options are listed under their group, in the order the groups first appear
}

type Props = {
  value: string
  options: FieldOption[]
  onChange: (name: string) => void
  label: string // for screen readers and tests: "Filter field"
  className?: string
  placeholder?: string
}

// A field (column) picker with a search box: a button showing the choice,
// and a dropdown that lists the fields, narrowed as you type (fuzzy: "crat"
// finds created_at). ↑ ↓ move, Enter picks, Esc closes. Used by the filter
// bars in place of a plain <select>, which can't search.
export default function FieldPicker({ value, options, onChange, label, className, placeholder = 'Search fields' }: Props) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [index, setIndex] = useState(0)
  const root = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLUListElement>(null)
  useDismiss(open, root, () => setOpen(false))

  const matches = fuzzyRank(query, options, (o) => o.name)
  const current = options.find((o) => o.name === value)

  useEffect(() => {
    if (!open) return
    setQuery('')
    setIndex(Math.max(0, options.findIndex((o) => o.name === value)))
    // After it's drawn: focus the search box, show the chosen field.
    const t = setTimeout(() => {
      input.current?.focus()
      list.current?.querySelector('.field-picker-item.active')?.scrollIntoView({ block: 'nearest' })
    }, 0)
    return () => clearTimeout(t)
  }, [open])
  useEffect(() => { setIndex(0) }, [query])
  useEffect(() => {
    list.current?.querySelector('.field-picker-item.active')?.scrollIntoView({ block: 'nearest' })
  }, [index])

  const pick = (name: string) => {
    onChange(name)
    setOpen(false)
  }
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setIndex((i) => Math.min(i + 1, matches.length - 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setIndex((i) => Math.max(i - 1, 0)) }
    else if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); if (matches[index]) pick(matches[index].item.name) }
    else if (e.key === 'Tab') setOpen(false)
  }

  // The list, under group headings when the options have groups.
  const items: ReactNode[] = []
  let lastGroup: string | undefined
  matches.forEach(({ item, match }, i) => {
    if (item.group && item.group !== lastGroup && !query) {
      items.push(<li key={`g:${item.group}`} className="field-picker-group" role="presentation">{item.group}</li>)
      lastGroup = item.group
    }
    items.push(
      <li key={item.name} role="option" aria-selected={i === index} data-name={item.name}
          className={`field-picker-item${i === index ? ' active' : ''}${item.name === value ? ' chosen' : ''}`}
          onMouseEnter={() => setIndex(i)} onMouseDown={(e) => e.preventDefault()} onClick={() => pick(item.name)}>
        <span className="field-picker-icon">{item.key && <KeyIcon />}</span>
        <span className="field-picker-name">{highlight(item.name, match.positions)}</span>
        {item.note && <span className="field-picker-note">{item.note}</span>}
      </li>,
    )
  })

  return (
    <div ref={root} className={`field-picker${className ? ` ${className}` : ''}`}>
      <button type="button" className="select field-picker-btn" aria-label={label} aria-haspopup="listbox" aria-expanded={open}
              data-value={value} title={current?.note ? `${value} · ${current.note}` : value}
              onClick={() => setOpen((o) => !o)}
              onKeyDown={(e) => { if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); setOpen(true) } }}>
        <span className="field-picker-icon">{current?.key && <KeyIcon />}</span>
        <span className="field-picker-name">{value || placeholder}</span>
        {current?.note && <span className="field-picker-note">{current.note}</span>}
        <ChevronDownIcon />
      </button>
      {open && (
        <div className="field-picker-menu" role="dialog" aria-label={`${label}: search`}>
          <div className="field-picker-search">
            <SearchIcon />
            <input ref={input} type="text" value={query} onChange={(e) => setQuery(e.target.value)} onKeyDown={onKeyDown}
                   placeholder={placeholder} aria-label={placeholder} spellCheck={false} autoCapitalize="off" autoCorrect="off" autoComplete="off" />
          </div>
          <ul ref={list} className="field-picker-list" role="listbox" aria-label={label}>
            {items}
            {matches.length === 0 && <li className="field-picker-empty">No field matches “{query.trim()}”</li>}
          </ul>
        </div>
      )}
    </div>
  )
}

// The name with its matched letters marked.
function highlight(name: string, positions: number[]): ReactNode {
  if (positions.length === 0) return name
  const set = new Set(positions)
  const parts: ReactNode[] = []
  let run = ''
  let marked = false
  for (let i = 0; i <= name.length; i++) {
    const m = i < name.length && set.has(i)
    if (i === name.length || m !== marked) {
      if (run) parts.push(marked ? <mark key={i}>{run}</mark> : run)
      run = ''
      marked = m
    }
    if (i < name.length) run += name[i]
  }
  return parts
}
