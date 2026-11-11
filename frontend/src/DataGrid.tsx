import { lazy, Suspense, useEffect, useRef, useState, type KeyboardEvent, type MouseEvent, type PointerEvent, type ReactNode, type RefObject } from 'react'
import { DBService, type EditTarget, type ForeignKey, type RowEdit } from '../bindings/github.com/anujkaushik1/SpyDB'
import Banner, { errorDetail, errorMessage } from './Banner'
import { ArrowDownIcon, ArrowUpIcon, ArrowUpRightIcon, KeyIcon, LinkIcon, Spinner } from './icons'
import { copyText } from './clipboard'
import { formatRows, formatValue, rowsAsJson, type CopyFormat } from './copyFormats'
import { dragX } from './resize'
import RowMenu, { FORMAT_LABELS } from './RowMenu'
import { rowJsonEdits } from './rowJsonEdits'
import { cellClass, cellText } from './cellValues'
import { arrayElement, arrayToJson, jsonToArray } from './pgArray'
import { shortcut } from './platform'
import { useDismiss } from './useDismiss'

// The JSON editor is 1.2 MB; load it the first time a json / jsonb / array cell is opened.
const JsonEditorModal = lazy(() => import('./JsonEditorModal'))
const loadingJson = <div className="json-modal-backdrop"><div className="json-modal-loading">Loading JSON editor…</div></div>

export type GridColumn = {
  name: string
  type: string
  category: string // number, bool, time, json, text or other (for colouring)
  primaryKey: boolean
  source: string // the table column an edit saves to; '' = read-only
}

export type Sort = { column: string; desc: boolean } | null

type Cell = { row: number; col: number }

const MIN_COLUMN = 56 // px
const MAX_FIT = 2400 // double-click fits a column to its values, up to this (the cell limit in style.css)

type Values = Record<string, string | null> // column (or key name) → value; null is NULL

// Edited cells, keyed by the row's key as loaded (JSON), so an edit stays
// attached to its row across pages. `key` finds the row when saving, `old`
// holds each edited cell's value before the edit (the save checks nobody
// changed it meanwhile).
type Edits = Record<string, { key: Values; old: Values; values: Values }>
// Rows saved from query results, which aren't reloaded: their new key (a
// ctid changes on every save) and values as Postgres now shows them.
type Saved = Record<string, { key: Values; values: Values }>

type Props = {
  columns: GridColumn[]
  rows: (string | null)[][]
  firstRowNumber: number
  target: EditTarget | null // where edits save; null = read-only
  rowKeys: (string | null)[][] // per row, its key (target.key); from Go, never shown
  readOnlyReason?: string // why it can't be edited, e.g. "a join"
  keepSavedValues?: boolean // query results: show saved values (the rows aren't reloaded)
  onSaved?: () => void // after a successful save; table tabs reload their rows
  onDirtyChange?: (dirty: boolean) => void
  sort?: Sort
  onSort?: (column: string) => void // headers sort when given
  wrapClass: string
  wrapRef?: RefObject<HTMLDivElement>
  children?: ReactNode // shown under the table, e.g. "This table is empty."
  foreignKeys?: GridForeignKey[] // the table's foreign keys: their columns get a link mark
  onFollow?: (key: GridForeignKey, values: string[]) => void // open the row a value points at
}

// A foreign key (DBService.ForeignKeys), its column lists never null.
export type GridForeignKey = Omit<ForeignKey, 'columns' | 'refColumns'> & { columns: string[]; refColumns: string[] }

// A foreign key a cell can follow, with this row's values for its columns.
type Link = { key: GridForeignKey; values: string[] }

// The grid of rows. When `target` is set, every row comes with its key
// (rowKeys: primary key, or ctid + row hash; the key columns may not be
// shown) and cells can be edited: click to select, double-click / Enter /
// type to edit. Changes wait in a bar at the bottom until Save, which sends
// them as UPDATEs in one transaction (DBService.SaveRows).
export default function DataGrid(props: Props) {
  const { columns, rows, rowKeys, firstRowNumber, target, keepSavedValues, onSaved, onDirtyChange, sort, onSort } = props
  const [selected, setSelected] = useState<Cell | null>(null)
  const [editing, setEditing] = useState<{ cell: Cell; initial: string; typed: boolean } | null>(null)
  const [pending, setPending] = useState<Edits>({})
  // The target the unsaved edits were made against, saved to whatever the
  // rows show by then: if a reload brings another (the table dropped and
  // created again, or another connection, maybe another server through the
  // same tunnel), Go refuses rather than applying them there.
  const editedTarget = useRef<EditTarget | null>(null)
  const changePending = (next: Edits) => {
    if (Object.keys(next).length === 0) editedTarget.current = null
    else editedTarget.current ??= target
    setPending(next)
  }
  const [saved, setSaved] = useState<Saved>({})
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<{ message: string; detail?: string } | null>(null)
  const [savedMessage, setSavedMessage] = useState<string | null>(null)
  const [rowSelection, setRowSelection] = useState<Set<number>>(new Set()) // row indexes on this page
  const anchor = useRef<number | null>(null) // where a Shift range starts
  const lead = useRef<number | null>(null) // its moving end, for Shift+↑/↓
  const [menu, setMenu] = useState<{ x: number; y: number; cell: Cell | null } | null>(null)
  const [copied, setCopied] = useState<{ text: string; error?: boolean } | null>(null)
  const [jsonCell, setJsonCell] = useState<Cell | null>(null) // open in the JSON editor
  const [rowJson, setRowJson] = useState<number[] | null>(null) // rows shown with "View as JSON"
  const [linkMenu, setLinkMenu] = useState<{ x: number; y: number; links: Link[] } | null>(null) // a value that points at rows in several tables
  const ownRef = useRef<HTMLDivElement>(null)
  const wrap = props.wrapRef ?? ownRef
  const table = useRef<HTMLTableElement>(null)

  // Column widths in px ([row numbers, ...columns]) once one is resized; until
  // then the browser sizes columns to their content. They belong to these
  // column names, so another page keeps them and a different result starts fresh.
  const columnKey = columns.map((c) => c.name).join('\n')
  const [sizing, setSizing] = useState<{ key: string; widths: number[] } | null>(null)
  const [resizing, setResizing] = useState<number | null>(null) // column being dragged
  const widths = sizing?.key === columnKey ? sizing.widths : null

  // The widths as drawn now, so the first drag doesn't make anything jump.
  const currentWidths = () => widths
    ?? [...(table.current?.tHead?.rows[0]?.cells ?? [])].slice(0, columns.length + 1).map((c) => c.getBoundingClientRect().width)

  const setWidth = (base: number[], col: number, width: number) => {
    const next = base.slice()
    next[col + 1] = Math.round(width)
    setSizing({ key: columnKey, widths: next })
  }

  const startResize = (e: PointerEvent<HTMLSpanElement>, col: number) => {
    const base = currentWidths()
    setResizing(col)
    dragX(e, (dx) => setWidth(base, col, Math.max(MIN_COLUMN, base[col + 1] + dx)), () => setResizing(null))
  }

  // Double-click a column's edge: fit it to its header and the values on this page.
  const fitColumn = (col: number) => {
    let fit = MIN_COLUMN
    for (const row of table.current?.rows ?? []) {
      const cell = row.cells[col + 1]
      if (cell) fit = Math.max(fit, contentWidth(cell))
    }
    setWidth(currentWidths(), col, Math.min(fit, MAX_FIT))
  }

  const keyNames = target?.key ?? []
  const canEdit = target !== null && keyNames.length > 0 && rowKeys.length === rows.length
  const isEditable = (col: number) => canEdit && columns[col]?.source !== ''
  const isJson = (col: number) => columns[col]?.category === 'json'
  const arrayOf = (col: number) => arrayElement(columns[col]?.type ?? '')
  // json / jsonb, and arrays (text[], int[] …) shown as JSON arrays, open in
  // the JSON editor; an array Postgres printed some other way ([0:1]={a,b})
  // edits as text.
  const opensJson = (cell: Cell) => {
    if (isJson(cell.col)) return true
    const element = arrayOf(cell.col)
    const value = shownValue(cell.row, cell.col)
    return element !== null && (value === null || arrayToJson(value, element) !== null)
  }

  // Each column's foreign keys, one per table and columns they point at.
  const keysOf = (col: number) => {
    const seen = new Set<string>()
    return (props.foreignKeys ?? []).filter((k) => {
      const id = JSON.stringify([k.columns, k.refSchema, k.refTable, k.refColumns])
      if (!k.columns.includes(columns[col]?.name) || seen.has(id)) return false
      seen.add(id)
      return true
    })
  }
  const columnKeys = columns.map((_, j) => keysOf(j))
  // The keys this row's cell can follow: every column of the key has a value
  // (a NULL in any of them points nowhere).
  const linksOf = (row: number, col: number): Link[] => columnKeys[col].flatMap((key) => {
    const values = key.columns.map((name) => {
      const j = columns.findIndex((c) => c.name === name)
      return j < 0 ? null : shownValue(row, j)
    })
    return values.every((v): v is string => v !== null) ? [{ key, values }] : []
  })
  const follow = (e: MouseEvent<HTMLButtonElement>, links: Link[]) => {
    e.stopPropagation()
    if (links.length === 1) return props.onFollow?.(links[0].key, links[0].values)
    const box = e.currentTarget.getBoundingClientRect()
    setLinkMenu({ x: box.left, y: box.bottom + 4, links })
  }

  // Rows are told apart by their key as loaded; after a save, `saved` holds
  // the key that finds the row now.
  const rowId = (r: number) => JSON.stringify(rowKeys[r] ?? [])
  const currentKey = (r: number): Values =>
    saved[rowId(r)]?.key ?? Object.fromEntries(keyNames.map((name, j) => [name, rowKeys[r]?.[j] ?? null]))
  // The value without unsaved edits: saved (query results) or as loaded.
  const baseValue = (r: number, col: number) => {
    const values = saved[rowId(r)]?.values
    const source = columns[col].source
    return values && source && source in values ? values[source] : rows[r][col]
  }
  const shownValue = (r: number, col: number) => {
    const values = pending[rowId(r)]?.values
    const source = columns[col].source
    return values && source && source in values ? values[source] : baseValue(r, col)
  }
  const isChanged = (r: number, col: number) => {
    const values = pending[rowId(r)]?.values
    const source = columns[col].source
    return !!values && !!source && source in values
  }

  const changedRows = Object.keys(pending).length
  const changedCells = Object.values(pending).reduce((n, e) => n + Object.keys(e.values).length, 0)

  // New rows (another page, a reload): the selection no longer points at the same cell.
  useEffect(() => {
    setSelected(null)
    setEditing(null)
    setRowSelection(new Set())
    anchor.current = null
    lead.current = null
    setMenu(null)
    setJsonCell(null)
    setRowJson(null)
    setLinkMenu(null)
  }, [rows])

  useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => setCopied(null), 2500)
    return () => clearTimeout(timer)
  }, [copied])

  useEffect(() => onDirtyChange?.(changedRows > 0), [changedRows > 0])

  useEffect(() => {
    if (!savedMessage) return
    const timer = setTimeout(() => setSavedMessage(null), 3000)
    return () => clearTimeout(timer)
  }, [savedMessage])

  // Returns `edits` with one cell set; setting it back to its base value drops the edit.
  const withCell = (edits: Edits, cell: Cell, value: string | null): Edits => {
    const id = rowId(cell.row)
    const source = columns[cell.col].source
    const base = baseValue(cell.row, cell.col)
    const entry = edits[id] ?? { key: currentKey(cell.row), old: {}, values: {} }
    const values = { ...entry.values }
    const old = { ...entry.old }
    if (value === base) {
      delete values[source]
      delete old[source]
    } else {
      values[source] = value
      old[source] = base
    }
    const next = { ...edits }
    if (Object.keys(values).length === 0) delete next[id]
    else next[id] = { key: entry.key, old, values }
    return next
  }

  const save = async (edits: Edits = pending) => {
    const to = editedTarget.current ?? target
    if (!to || saving || Object.keys(edits).length === 0) return
    setSaving(true)
    setSaveError(null)
    try {
      const ids = Object.keys(edits)
      const rowEdits: RowEdit[] = ids.map((id) => edits[id])
      const savedRows = (await DBService.SaveRows(to, rowEdits)) ?? []
      const count = savedRows.length
      if (keepSavedValues) {
        // One saved row per edit, in order: keep its new key and values.
        setSaved((current) => {
          const next = { ...current }
          ids.forEach((id, k) => {
            const row = savedRows[k] as { key: Values | null; values: Values | null } | undefined
            next[id] = { key: row?.key ?? edits[id].key, values: { ...next[id]?.values, ...(row?.values ?? edits[id].values) } }
          })
          return next
        })
      }
      changePending({})
      setSavedMessage(`Saved ${count} row${count === 1 ? '' : 's'} to ${to.schema}.${to.table}`)
      onSaved?.()
    } catch (err) {
      changePending(edits) // keep them, so nothing typed is lost
      setSaveError({ message: errorMessage(err), detail: errorDetail(err) })
    } finally {
      setSaving(false)
    }
  }

  const discard = () => {
    changePending({})
    setSaveError(null)
  }

  const startEdit = (cell: Cell, typed?: string) => {
    // json / jsonb and array values open the JSON editor instead (read-only when they can't be saved).
    if (opensJson(cell)) return openJson(cell)
    if (!isEditable(cell.col)) return
    setSelected(cell)
    setEditing({ cell, initial: typed ?? shownValue(cell.row, cell.col) ?? '', typed: typed !== undefined })
  }

  // result: the new value, or null when the edit was cancelled or left unchanged.
  const finishEdit = (result: { value: string | null } | null, move?: 'down' | 'right' | 'left', saveAfter?: boolean) => {
    if (!editing) return
    const { cell } = editing
    const next = result ? withCell(pending, cell, result.value) : pending
    if (result) {
      changePending(next)
      setSavedMessage(null)
    }
    setEditing(null)
    const moved =
      move === 'down' ? { row: Math.min(cell.row + 1, rows.length - 1), col: cell.col }
      : move === 'right' ? { row: cell.row, col: Math.min(cell.col + 1, columns.length - 1) }
      : move === 'left' ? { row: cell.row, col: Math.max(cell.col - 1, 0) }
      : cell
    setSelected(moved)
    wrap.current?.focus({ preventScroll: true })
    if (saveAfter) save(next)
  }

  // A click on a row number, or a Shift / ⌘ click anywhere in a row: plain
  // picks just that row, ⌘ / Ctrl adds or removes it, Shift selects the range
  // from the row clicked before.
  const selectRange = (from: number, to: number) => {
    const [start, end] = from < to ? [from, to] : [to, from]
    setRowSelection(new Set(Array.from({ length: end - start + 1 }, (_, k) => start + k)))
  }
  const selectRow = (row: number, e: { shiftKey: boolean; metaKey: boolean; ctrlKey: boolean }) => {
    if (e.shiftKey && anchor.current !== null) {
      selectRange(anchor.current, row)
      lead.current = row
      return
    }
    if (e.metaKey || e.ctrlKey) {
      setRowSelection((current) => {
        const next = new Set(current)
        if (next.has(row)) next.delete(row)
        else next.add(row)
        return next
      })
    } else {
      setRowSelection(new Set([row]))
    }
    anchor.current = row
    lead.current = row
  }
  const allSelected = rows.length > 0 && rowSelection.size === rows.length
  const selectAll = () => setRowSelection(new Set(rows.map((_, i) => i)))

  const copy = async (text: string, message: string) => {
    try {
      await copyText(text)
      setCopied({ text: message })
    } catch (err) {
      setCopied({ text: `Couldn’t copy: ${errorMessage(err)}`, error: true })
    }
  }
  // Selected rows top to bottom, as shown (unsaved edits included).
  const copyRows = (format: CopyFormat) => {
    const picked = shownRows([...rowSelection].sort((a, b) => a - b))
    copy(formatRows(format, columns, picked), `Copied ${picked.length} row${picked.length === 1 ? '' : 's'} as ${FORMAT_LABELS[format]}`)
  }
  const copyCell = (cell: Cell) =>
    copy(formatValue(shownValue(cell.row, cell.col), columns[cell.col].category), 'Copied value')

  // Right-click: a row outside the selection becomes the selection, like Beekeeper.
  const openMenu = (e: MouseEvent, row: number, col: number | null) => {
    if ((e.target as HTMLElement).closest('input')) return // the cell editor keeps its own menu
    e.preventDefault()
    if (!rowSelection.has(row)) {
      setRowSelection(new Set([row]))
      anchor.current = row
      lead.current = row
    }
    if (col !== null) setSelected({ row, col })
    setMenu({ x: e.clientX, y: e.clientY, cell: col === null ? null : { row, col } })
  }
  const openJson = (cell: Cell) => {
    setSelected(cell)
    setJsonCell(cell)
  }
  const closeJson = () => {
    setJsonCell(null)
    wrap.current?.focus({ preventScroll: true })
  }
  // An array's JSON goes back to Postgres's form ({a,b}); a problem stays in the editor.
  const applyJson = (cell: Cell, value: string | null) => {
    const element = arrayOf(cell.col)
    if (element && value !== null) {
      const result = jsonToArray(value, element)
      if ('error' in result) return result.error
      value = result.value
    }
    changePending(withCell(pending, cell, value))
    setSavedMessage(null)
    closeJson()
  }

  const closeRowJson = () => {
    setRowJson(null)
    wrap.current?.focus({ preventScroll: true })
  }
  // Selected rows top to bottom, values as shown (unsaved edits included).
  const shownRows = (indexes: number[]) => indexes.map((i) => rows[i].map((_, j) => shownValue(i, j)))

  // Edited "View as JSON": changed fields become unsaved cell edits, like typed
  // ones. A rule broken (key removed, primary key changed…) comes back as a
  // message for the modal, which stays open.
  const applyRowJson = (indexes: number[], text: string | null) => {
    if (text === null) return 'A whole row can’t be set to NULL.'
    const result = rowJsonEdits(columns, shownRows(indexes), text, isEditable)
    if ('error' in result) return result.error
    let next = pending
    for (const e of result.edits) next = withCell(next, { row: indexes[e.row], col: e.col }, e.value)
    changePending(next)
    setSavedMessage(null)
    closeRowJson()
  }

  const closeMenu = () => {
    setMenu(null)
    wrap.current?.focus({ preventScroll: true }) // so ⌘C works next
  }

  // Keys on the grid itself; while a cell is being edited its input handles them.
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (editing) return
    const mod = e.metaKey || e.ctrlKey
    if (mod && e.key.toLowerCase() === 's') {
      e.preventDefault()
      save()
      return
    }
    if (mod && e.key.toLowerCase() === 'a') {
      e.preventDefault()
      selectAll()
      return
    }
    if (mod && e.key.toLowerCase() === 'c') {
      if (window.getSelection()?.toString()) return // text picked with the mouse: normal copy
      if (rowSelection.size > 0) {
        e.preventDefault()
        copyRows('text')
      } else if (selected) {
        e.preventDefault()
        copyCell(selected)
      }
      return
    }
    if (e.key === 'Escape' && rowSelection.size > 0) {
      setRowSelection(new Set())
      return
    }
    if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !mod && rows.length > 0) {
      const step = e.key === 'ArrowDown' ? 1 : -1
      const clamp = (row: number) => Math.max(0, Math.min(rows.length - 1, row))
      const scrollTo = (row: number) => wrap.current?.querySelectorAll('tbody tr')[row]?.scrollIntoView({ block: 'nearest' })
      if (e.shiftKey) {
        // Shift+↑/↓ grows or shrinks the row selection from the row it started
        // on: the selected cell's row, or the rows picked before.
        e.preventDefault()
        if (rowSelection.size === 0 || anchor.current === null || lead.current === null) {
          anchor.current = selected?.row ?? 0
          lead.current = anchor.current
        }
        const next = clamp(lead.current + step)
        lead.current = next
        selectRange(anchor.current, next)
        if (selected) setSelected({ row: next, col: selected.col })
        scrollTo(next)
        return
      }
      if (rowSelection.size > 0 && !selected) {
        // Rows picked by their numbers: ↑/↓ moves to the next row on its own.
        e.preventDefault()
        const next = clamp((lead.current ?? 0) + step)
        setRowSelection(new Set([next]))
        anchor.current = next
        lead.current = next
        scrollTo(next)
        return
      }
      if (rowSelection.size > 0) setRowSelection(new Set()) // the cell moves on its own, like a spreadsheet
    }
    if (!selected) return
    const step: Record<string, [number, number]> = { ArrowUp: [-1, 0], ArrowDown: [1, 0], ArrowLeft: [0, -1], ArrowRight: [0, 1] }
    if (e.key in step) {
      e.preventDefault()
      const [dr, dc] = step[e.key]
      const cell = {
        row: Math.max(0, Math.min(rows.length - 1, selected.row + dr)),
        col: Math.max(0, Math.min(columns.length - 1, selected.col + dc)),
      }
      setSelected(cell)
      wrap.current?.querySelector(`[data-cell="${cell.row}-${cell.col}"]`)?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
    } else if (e.key === 'Enter' || e.key === 'F2') {
      e.preventDefault()
      startEdit(selected)
    } else if (e.key === 'Backspace' || e.key === 'Delete') {
      e.preventDefault()
      startEdit(selected, '')
    } else if (e.key === 'Escape') {
      setSelected(null)
    } else if (e.key.length === 1 && !e.metaKey && !e.ctrlKey && !e.altKey) {
      e.preventDefault()
      startEdit(selected, e.key) // typing replaces the value, like a spreadsheet
    }
  }

  return (
    <>
      <div className={props.wrapClass} ref={wrap} tabIndex={0} onKeyDown={onKeyDown}>
        {columns.length > 0 && (
          <table className={widths ? 'grid sized' : 'grid'} ref={table}
                 style={widths ? { width: widths.reduce((sum, w) => sum + w, 0) } : undefined}>
            {widths && (
              <colgroup>
                {widths.map((w, k) => <col key={k} style={{ width: w }} />)}
                {/* the filler column takes any space left over */}
                <col />
              </colgroup>
            )}
            <thead>
              <tr>
                <th className="grid-rownum" aria-label="Select all rows" title={allSelected ? 'Clear selection' : `Select all rows (${shortcut('A')})`}
                    onClick={() => (allSelected ? setRowSelection(new Set()) : selectAll())} />
                {columns.map((c, i) => {
                  const sorted = sort?.column === c.name ? (sort.desc ? 'desc' : 'asc') : null
                  const keys = columnKeys[i]
                  const points = keys.map((k) => `${k.refTable}.${k.refColumns.join(', ')}`).join(', ')
                  const label = (
                    <>
                      {c.primaryKey && <span className="grid-key" aria-label="Primary key"><KeyIcon /></span>}
                      {keys.length > 0 && <span className="grid-fk" aria-label={`Foreign key to ${points}`}><LinkIcon /></span>}
                      <span className="grid-col-name">{c.name}</span>
                      {c.type && <span className="grid-col-type">{c.type}</span>}
                    </>
                  )
                  return (
                    <th key={i} aria-sort={sorted === 'asc' ? 'ascending' : sorted === 'desc' ? 'descending' : undefined}>
                      {onSort ? (
                        <button type="button" className="grid-head" onClick={() => onSort(c.name)}
                                title={`${c.name} · ${c.type}${c.primaryKey ? ' · primary key' : ''}${keys.length ? ` · foreign key → ${points}` : ''} — click to sort`}>
                          {label}
                          <span className={sorted ? 'grid-sort active' : 'grid-sort'}>
                            {sorted === 'desc' ? <ArrowDownIcon /> : <ArrowUpIcon />}
                          </span>
                        </button>
                      ) : (
                        <div className="grid-head static" title={keys.length ? `Foreign key → ${points}` : undefined}>{label}</div>
                      )}
                      <span className={resizing === i ? 'col-resizer dragging' : 'col-resizer'} aria-hidden="true"
                            title="Drag to resize · double-click to fit"
                            onPointerDown={(e) => startResize(e, i)} onDoubleClick={() => fitColumn(i)} />
                    </th>
                  )
                })}
                {widths && <th className="grid-filler" aria-hidden="true" />}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={i} className={rowSelection.has(i) ? 'row-selected' : undefined}
                    onMouseDown={(e) => {
                      // No text selection on Shift-click, except inside the cell editor.
                      if (e.shiftKey && !(e.target as HTMLElement).closest('.cell-editor')) e.preventDefault()
                    }}>
                  <td className="grid-rownum"
                      onClick={(e) => { selectRow(i, e); setSelected(null); wrap.current?.focus({ preventScroll: true }) }}
                      onDoubleClick={() => setRowJson([i])}
                      onContextMenu={(e) => openMenu(e, i, null)}
                      title="Click to select · double-click to view the row as JSON">
                    {firstRowNumber + i}
                  </td>
                  {row.map((_, j) => {
                    const value = shownValue(i, j)
                    const category = columns[j].category
                    const isSelected = selected?.row === i && selected?.col === j
                    const isEditing = editing?.cell.row === i && editing?.cell.col === j
                    const links = props.onFollow && !isEditing ? linksOf(i, j) : []
                    const classes = [
                      cellClass(value, category),
                      isChanged(i, j) && 'cell-changed',
                      isSelected && 'cell-selected',
                      isEditing && 'cell-editing',
                      columnKeys[j].length > 0 && 'cell-fk',
                    ].filter(Boolean).join(' ')
                    return (
                      <td
                        key={j}
                        data-cell={`${i}-${j}`}
                        className={classes}
                        title={isJson(j) || arrayOf(j) ? `${value ?? 'NULL'} — double-click to open the JSON editor`
                          : isEditable(j) ? `${value ?? 'NULL'} — double-click to edit` : value ?? 'NULL'}
                        onClick={(e) => {
                          // Clicks inside the cell editor place the cursor; moving focus would end the edit.
                          if (isEditing) return
                          if (e.shiftKey || e.metaKey || e.ctrlKey) selectRow(i, e)
                          else {
                            setRowSelection(new Set())
                            anchor.current = i
                            lead.current = i
                          }
                          setSelected({ row: i, col: j })
                          wrap.current?.focus({ preventScroll: true })
                        }}
                        onDoubleClick={() => { if (!isEditing) startEdit({ row: i, col: j }) }}
                        onContextMenu={(e) => openMenu(e, i, j)}
                      >
                        {isEditing && editing ? (
                          <CellEditor initial={editing.initial} typed={editing.typed} onDone={finishEdit} />
                        ) : value === null ? 'NULL' : cellText(value, category)}
                        {links.length > 0 && (
                          <button type="button" className="cell-link" tabIndex={-1}
                                  title={links.length === 1 ? linkTitle(links[0]) : `Open the row it points to (${links.length} tables)`}
                                  onMouseDown={(e) => e.preventDefault()} onClick={(e) => follow(e, links)}
                                  onDoubleClick={(e) => e.stopPropagation()}>
                            <ArrowUpRightIcon />
                          </button>
                        )}
                      </td>
                    )
                  })}
                  {widths && <td className="grid-filler" aria-hidden="true" />}
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {props.children}
      </div>

      {menu && (
        <RowMenu
          x={menu.x}
          y={menu.y}
          rowCount={rowSelection.size}
          onCopyValue={menu.cell ? () => { copyCell(menu.cell!); closeMenu() } : undefined}
          onOpenJson={menu.cell && opensJson(menu.cell) ? () => { closeMenu(); openJson(menu.cell!) } : undefined}
          onViewJson={() => { closeMenu(); setRowJson([...rowSelection].sort((a, b) => a - b)) }}
          onCopy={(format) => { copyRows(format); closeMenu() }}
          onClose={closeMenu}
        />
      )}
      {copied && <div className={copied.error ? 'copy-toast error' : 'copy-toast'} role="status">{copied.text}</div>}
      {linkMenu && (
        <LinkMenu x={linkMenu.x} y={linkMenu.y} links={linkMenu.links}
                  onPick={(link) => { setLinkMenu(null); props.onFollow?.(link.key, link.values) }}
                  onClose={() => setLinkMenu(null)} />
      )}
      {jsonCell && (
        <Suspense fallback={loadingJson}>
          <JsonEditorModal
            title={columns[jsonCell.col].name}
            tag={columns[jsonCell.col].type}
            note={`row ${(firstRowNumber + jsonCell.row).toLocaleString()}`}
            value={jsonValue(shownValue(jsonCell.row, jsonCell.col), arrayOf(jsonCell.col))}
            readOnly={!isEditable(jsonCell.col)}
            onApply={(value) => applyJson(jsonCell, value)}
            onClose={closeJson}
          />
        </Suspense>
      )}
      {rowJson && (
        <Suspense fallback={loadingJson}>
          <JsonEditorModal
            title={rowJson.length === 1 ? `Row ${(firstRowNumber + rowJson[0]).toLocaleString()}` : `${rowJson.length.toLocaleString()} rows`}
            tag="JSON"
            note={`${columns.length} column${columns.length === 1 ? '' : 's'}`}
            value={rowsAsJson(columns, shownRows(rowJson))}
            readOnly={!canEdit}
            readOnlyNote={`This result can’t be edited${props.readOnlyReason ? ` (${props.readOnlyReason})` : ''}. ${shortcut('F')} to search; Copy still works.`}
            hint={`Apply keeps changed fields as unsaved edits until you save (${shortcut('S')}). Keys and the primary key can’t change.`}
            allowNull={false}
            onApply={(text) => applyRowJson(rowJson, text)}
            onClose={closeRowJson}
          />
        </Suspense>
      )}

      {saveError && (
        <div className="changes-error">
          <Banner tone="error" title="Couldn’t save" message={saveError.message} detail={saveError.detail} />
        </div>
      )}
      {changedRows > 0 ? (
        <div className="changes-bar" role="status">
          <span className="changes-dot" aria-hidden="true" />
          <span className="changes-text">
            <strong>{changedCells}</strong> unsaved change{changedCells === 1 ? '' : 's'} in {changedRows} row{changedRows === 1 ? '' : 's'}
          </span>
          <span className="changes-hint">{shortcut('S')} to save</span>
          <button type="button" className="btn btn-secondary btn-sm" onClick={discard} disabled={saving}>Discard</button>
          <button type="button" className="btn btn-primary btn-sm" onClick={() => save()} disabled={saving}>
            {saving ? <><Spinner /> Saving…</> : 'Save changes'}
          </button>
        </div>
      ) : savedMessage && (
        <div className="changes-bar saved" role="status">✓ {savedMessage}</div>
      )}
    </>
  )
}

// The width a cell needs to show everything without cutting it off.
function contentWidth(cell: HTMLTableCellElement) {
  const style = getComputedStyle(cell)
  const edges = parseFloat(style.paddingLeft) + parseFloat(style.paddingRight) + parseFloat(style.borderRightWidth)
  const head = cell.tagName === 'TH' ? cell.querySelector<HTMLElement>('.grid-head') : null
  if (head) {
    // Header: its parts side by side (key, name, type, sort arrow) plus gaps and padding.
    const parts = [...head.children] as HTMLElement[]
    const headStyle = getComputedStyle(head)
    const gaps = parseFloat(headStyle.columnGap || '0') * Math.max(parts.length - 1, 0)
    return parts.reduce((sum, p) => sum + p.offsetWidth, 0) + gaps
      + parseFloat(headStyle.paddingLeft) + parseFloat(headStyle.paddingRight) + edges + 1
  }
  // Value: the text's laid-out width, which ignores the cell's clipping. A
  // foreign key's link button sits in the cell's right padding, so it's left out.
  const range = document.createRange()
  range.selectNodeContents(cell)
  const link = cell.querySelector('.cell-link')
  if (link) range.setEndBefore(link)
  return Math.ceil(range.getBoundingClientRect().width + edges) + 1
}

// What the JSON editor opens with: an array's value as a JSON array.
function jsonValue(value: string | null, element: ReturnType<typeof arrayElement>) {
  return value === null || element === null ? value : arrayToJson(value, element) ?? value
}

const linkTitle = ({ key, values }: Link) =>
  `Open ${key.refTable} where ${key.refColumns.map((c, k) => `${c} = ${values[k]}`).join(' and ')}`

// Picks the table to open when a value points at rows in more than one.
function LinkMenu({ x, y, links, onPick, onClose }: { x: number; y: number; links: Link[]; onPick: (link: Link) => void; onClose: () => void }) {
  const root = useRef<HTMLDivElement>(null)
  useDismiss(true, root, onClose)
  useEffect(() => root.current?.querySelector('button')?.focus(), [])
  return (
    <div className="row-menu link-menu" role="menu" ref={root} style={{ left: Math.min(x, window.innerWidth - 260), top: y }}>
      <p className="row-menu-title">Open the row it points to in</p>
      {links.map((link, k) => (
        <button key={k} type="button" role="menuitem" className="row-menu-item" onClick={() => onPick(link)}>
          {link.key.refSchema}.{link.key.refTable}
          <kbd>{link.key.refColumns.join(', ')}</kbd>
        </button>
      ))}
    </div>
  )
}

type EditorProps = {
  initial: string
  typed: boolean // editing began by typing a character, so the value already changed
  onDone: (result: { value: string | null } | null, move?: 'down' | 'right' | 'left', saveAfter?: boolean) => void
}

// The input inside a cell being edited. Enter saves and moves down, Tab moves
// sideways, Esc cancels, ⌘S saves everything; NULL sets the cell to NULL.
function CellEditor({ initial, typed, onDone }: EditorProps) {
  const [text, setText] = useState(initial)
  const input = useRef<HTMLInputElement>(null)
  const changed = useRef(typed)
  const done = useRef(false)

  useEffect(() => {
    const el = input.current
    if (!el) return
    el.focus()
    if (typed) el.setSelectionRange(el.value.length, el.value.length)
    else el.select()
  }, [])

  // Leaving a cell untouched is not an edit (so a NULL doesn't silently become '').
  const finish = (result: { value: string | null } | null, move?: 'down' | 'right' | 'left', saveAfter?: boolean) => {
    if (done.current) return
    done.current = true
    onDone(result, move, saveAfter)
  }
  const typedValue = () => (changed.current ? { value: text } : null)

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      finish(typedValue(), 'down')
    } else if (e.key === 'Tab') {
      e.preventDefault()
      finish(typedValue(), e.shiftKey ? 'left' : 'right')
    } else if (e.key === 'Escape') {
      e.preventDefault()
      finish(null)
    } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
      e.preventDefault()
      finish(typedValue(), undefined, true)
    }
  }

  return (
    <div className="cell-editor">
      <input
        ref={input}
        value={text}
        aria-label="Cell value"
        onChange={(e) => { changed.current = true; setText(e.target.value) }}
        onKeyDown={onKeyDown}
        onBlur={() => finish(typedValue())}
        spellCheck={false}
        autoCapitalize="off"
        autoCorrect="off"
        autoComplete="off"
      />
      <button type="button" className="cell-null-btn" title="Set to NULL"
              onMouseDown={(e) => e.preventDefault()} onClick={() => finish({ value: null })}>
        NULL
      </button>
    </div>
  )
}
