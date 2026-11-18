import { lazy, Suspense, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent, type PointerEvent, type ReactNode, type RefObject, type ClipboardEvent } from 'react'
import { DBService, type EditTarget, type ForeignKey, type RowEdit } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import Banner, { errorDetail, errorMessage } from './Banner'
import { ArrowDownIcon, ArrowUpIcon, ArrowUpRightIcon, KeyIcon, LinkIcon, SearchIcon, Spinner, PinIcon, XIcon } from './icons'
import { copyText } from './clipboard'
import { formatRows, formatValue, rowsAsJson, type CopyFormat } from './copyFormats'
import { dragX } from './resize'
import DeleteRowsDialog from './DeleteRowsDialog'
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
const MAX_FIT = 2400 // double-click fits a column to its values, up to this (wider than the 400px cells start at in style.css)

// A cell: text (Postgres's text output, or a document's field as text), null
// for NULL, or undefined for a field a document (a DynamoDB item, a
// document) doesn't have, shown as an empty cell.
export type CellValue = string | null | undefined

export type GridValues = Record<string, CellValue> // column (or key name) → value

// Edited cells, keyed by the row's key as loaded (JSON), so an edit stays
// attached to its row across pages. `key` finds the row when saving, `old`
// holds each edited cell's value before the edit (the save checks nobody
// changed it meanwhile). In a document store's grid a value can be undefined
// (take the field off), and `types` says what a field's new value is when
// the JSON view decided it (5 a number, "5" text); see GridStore.
export type GridEdit = { key: GridValues; old: GridValues; values: GridValues; types?: Record<string, string> }
type Edits = Record<string, GridEdit>
// Rows saved from query results, which aren't reloaded: their new key (a
// ctid changes on every save) and values as Postgres now shows them.
type Saved = Record<string, { key: GridValues; values: GridValues }>

// Where edits go when it isn't a Postgres table (target): a DynamoDB table,
// a document store. Rows are found by keyNames (each row's values in
// rowKeys). Documents needn't share fields, so the JSON view and Copy as
// JSON are the store's (each row's own fields, as their own types), and in
// the JSON view fields can be added and removed.
export type GridStore = {
  where: string // what's saved to, for messages: "Saved 2 items to orders"
  noun: [string, string] // a row is an ['item', 'items'], or a ['document', 'documents']
  keyNames: string[]
  save: (edits: GridEdit[]) => Promise<number> // all of them or none; throws to keep them unsaved
  delete: (rows: { key: GridValues; old: GridValues }[]) => Promise<number> // likewise
  // The rows (indexes) as JSON, unsaved edits included: one object, or an array (asArray, or several rows).
  rowsJson: (rows: number[], edit: (row: number) => GridEdit | undefined, asArray: boolean) => string
  // Edited JSON back into field changes (row: index into rows), or what's wrong with it.
  applyRowsJson: (rows: number[], text: string, edit: (row: number) => GridEdit | undefined) =>
    { changes: { row: number; field: string; value: CellValue; type?: string }[] } | { error: string }
}

type Props = {
  columns: GridColumn[]
  rows: CellValue[][]
  firstRowNumber: number
  target: EditTarget | null // where edits save in Postgres; null = read-only, unless store is given
  store?: GridStore // where they save otherwise (DynamoDB)
  rowKeys: (string | null)[][] // per row, its key (target.key or store.keyNames); never shown
  readOnlyReason?: string // why it can't be edited, e.g. "a join"
  keepSavedValues?: boolean // query results: show saved values (the rows aren't reloaded)
  onSaved?: () => void // after a successful save or delete; table tabs reload their rows
  canDelete?: boolean // the right-click menu offers Delete: table tabs only, not query results
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
// them as UPDATEs in one transaction (DBService.SaveRows). With `store`
// instead, the same edits save to a DynamoDB table.
export default function DataGrid(props: Props) {
  const { columns, rows, rowKeys, firstRowNumber, target, store, keepSavedValues, onSaved, onDirtyChange, sort, onSort } = props
  const [one, many] = store?.noun ?? ['row', 'rows']
  const count = (n: number) => (n === 1 ? `1 ${one}` : `${n.toLocaleString()} ${many}`)
  const [selected, setSelected] = useState<Cell | null>(null)
  // A range of cells from the selected cell (its anchor) to `to`: dragged
  // over, Shift-clicked or Shift+arrowed. ⌘C copies it, a paste fills it,
  // Delete sets it to NULL.
  const [rangeTo, setRangeTo] = useState<Cell | null>(null)
  const dragFrom = useRef<Cell | null>(null) // the cell a mouse drag started on
  // The range's corners, kept within the rows and columns there are now (a reload may have fewer).
  const rangeBox = () => {
    if (!selected || !rangeTo || rows.length === 0 || columns.length === 0) return null
    const last = (n: number, max: number) => Math.max(0, Math.min(max - 1, n))
    return {
      r0: last(Math.min(selected.row, rangeTo.row), rows.length), r1: last(Math.max(selected.row, rangeTo.row), rows.length),
      c0: last(Math.min(selected.col, rangeTo.col), columns.length), c1: last(Math.max(selected.col, rangeTo.col), columns.length),
    }
  }
  const rangeCells = (): Cell[] => {
    const box = rangeBox()
    if (!box) return []
    const cells: Cell[] = []
    for (let r = box.r0; r <= box.r1; r++) for (let c = box.c0; c <= box.c1; c++) cells.push({ row: r, col: c })
    return cells
  }
  const inRange = (r: number, c: number) => { const b = rangeBox(); return !!b && r >= b.r0 && r <= b.r1 && c >= b.c0 && c <= b.c1 }
  useEffect(() => {
    const up = () => { dragFrom.current = null; wrap.current?.classList.remove('range-dragging') }
    window.addEventListener('mouseup', up)
    return () => window.removeEventListener('mouseup', up)
  }, [])
  useEffect(() => { if (!selected) setRangeTo(null) }, [selected])
  // Every cell of the range in an editable column set to one value (NULL, or
  // pasted text), as pending edits; how many that was.
  const fillRange = (cells: Cell[], valueAt: (i: number, cell: Cell) => CellValue): number => {
    let next = pending
    let n = 0
    cells.forEach((cell, i) => {
      if (!isEditable(cell.col)) return
      next = withCell(next, cell, valueAt(i, cell))
      n++
    })
    if (n > 0) changePending(next)
    return n
  }
  // Pasted text into the range (or down from the selected cell): one value
  // fills every cell; lines and tabs go cell by cell, like a spreadsheet.
  const onPaste = (e: ClipboardEvent<HTMLDivElement>) => {
    if (editing || !selected || !canEdit) return
    const target = e.target as HTMLElement
    if (target !== wrap.current && target.closest('input, textarea, [contenteditable="true"], .cm-editor')) return // a paste into some editor
    const text = e.clipboardData.getData('text/plain')
    if (!text) return
    e.preventDefault()
    const lines = text.replace(/\r/g, '').split('\n')
    if (lines.length > 1 && lines[lines.length - 1] === '') lines.pop()
    const grid = lines.map((l) => l.split('\t'))
    let cells = rangeCells()
    if (cells.length === 0) { // no range: as many cells down and across as the text has
      cells = []
      for (let r = 0; r < grid.length && selected.row + r < rows.length; r++)
        for (let c = 0; c < grid[r].length && selected.col + c < columns.length; c++) cells.push({ row: selected.row + r, col: selected.col + c })
    }
    const r0 = Math.min(...cells.map((c) => c.row)), c0 = Math.min(...cells.map((c) => c.col))
    const n = fillRange(cells, (_, cell) => {
      if (grid.length === 1 && grid[0].length === 1) return grid[0][0] // one value: every cell gets it
      const line = grid[(cell.row - r0) % grid.length]
      return line[(cell.col - c0) % line.length] ?? ''
    })
    setCopied({ text: n === 0 ? 'Nothing pasted: these columns can’t be edited' : `Pasted into ${n} cell${n === 1 ? '' : 's'} (unsaved)`, error: n === 0 })
  }
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
  // The find bar (⌘F / Ctrl+F): the text typed, and which match is current.
  const [find, setFind] = useState<{ query: string; at: number } | null>(null)
  const findInput = useRef<HTMLInputElement>(null)
  const [jsonCell, setJsonCell] = useState<Cell | null>(null) // open in the JSON editor
  const [rowJson, setRowJson] = useState<number[] | null>(null) // rows shown with "View as JSON"
  const [linkMenu, setLinkMenu] = useState<{ x: number; y: number; links: Link[] } | null>(null) // a value that points at rows in several tables
  // Rows asked to be deleted, as they were when Delete was clicked: what Go
  // deletes, even if the rows on screen change meanwhile.
  const [toDelete, setToDelete] = useState<{ target: EditTarget | null; rows: { key: GridValues; old: GridValues }[]; names: string[] } | null>(null)
  const deleting = useRef<{ cancel: () => void } | null>(null) // the delete running, for Stop
  const stopped = useRef(false)
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
  // Pinned columns (by name, in the order pinned) stay in view, first after
  // the row numbers, while the rest scroll sideways. They belong to these
  // column names too.
  const [pins, setPins] = useState<{ key: string; names: string[] } | null>(null)
  const pinnedNames = pins?.key === columnKey ? pins.names : []
  const pinnedIdx = pinnedNames.map((name) => columns.findIndex((c) => c.name === name)).filter((j) => j >= 0)
  // The columns in the order drawn: pinned first, then the rest as they are.
  const order = pinnedIdx.length > 0 ? [...pinnedIdx, ...columns.map((_, j) => j).filter((j) => !pinnedIdx.includes(j))] : columns.map((_, j) => j)
  const togglePin = (col: number) => {
    const name = columns[col].name
    // Pinned columns need known widths, for where each one sticks.
    if (!widths) setSizing({ key: columnKey, widths: currentWidths().map(Math.round) })
    setPins({ key: columnKey, names: pinnedNames.includes(name) ? pinnedNames.filter((n) => n !== name) : [...pinnedNames, name] })
  }
  // Where a pinned column sticks: after the row numbers and the pinned ones before it.
  const pinLeft = (col: number): number | undefined => {
    const at = pinnedIdx.indexOf(col)
    if (at < 0 || !widths) return undefined
    return widths[0] + pinnedIdx.slice(0, at).reduce((sum, j) => sum + widths[j + 1], 0)
  }
  const pinClass = (col: number) => {
    const at = pinnedIdx.indexOf(col)
    return at < 0 ? '' : at === pinnedIdx.length - 1 ? ' pinned pinned-last' : ' pinned'
  }

  // The widths as drawn now, so the first drag doesn't make anything jump.
  const currentWidths = () => widths
    ?? [table.current?.tHead?.rows[0]?.cells[0], ...columns.map((_, j) => table.current?.tHead?.querySelector(`th[data-col="${j}"]`))]
      .map((c) => c?.getBoundingClientRect().width ?? MIN_COLUMN)

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
      const cell = row.querySelector<HTMLTableCellElement>(`[data-col="${col}"]`)
      if (cell) fit = Math.max(fit, contentWidth(cell))
    }
    setWidth(currentWidths(), col, Math.min(fit, MAX_FIT))
  }

  const keyNames = store?.keyNames ?? target?.key ?? []
  const canEdit = (target !== null || !!store) && keyNames.length > 0 && rowKeys.length === rows.length
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
    return element !== null && value !== undefined && (value === null || arrayToJson(value, element) !== null)
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
  const currentKey = (r: number): GridValues =>
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

  // Find: cells whose shown text has the typed text, and columns whose name
  // has it (as their top cell, so a column's name finds the column). Column
  // matches first, then row by row; case doesn't matter.
  const findQuery = find?.query.trim().toLowerCase() ?? ''
  const findMatches = useMemo((): Cell[] => {
    if (!findQuery) return []
    const seen = new Set<string>()
    const out: Cell[] = []
    const add = (cell: Cell) => {
      const key = `${cell.row}-${cell.col}`
      if (!seen.has(key)) { seen.add(key); out.push(cell) }
    }
    columns.forEach((c, j) => { if (c.name.toLowerCase().includes(findQuery)) add({ row: rows.length > 0 ? 0 : -1, col: j }) })
    for (let i = 0; i < rows.length; i++) {
      for (let j = 0; j < columns.length; j++) {
        const v = shownValue(i, j)
        if (v != null && v.toLowerCase().includes(findQuery)) add({ row: i, col: j })
      }
    }
    return out
  }, [findQuery, rows, columns, pending, saved])
  const found = useMemo(() => new Set(findMatches.map((m) => `${m.row}-${m.col}`)), [findMatches])
  const findAt = find ? Math.min(find.at, Math.max(0, findMatches.length - 1)) : 0
  // Goes to a match: the cell is selected and scrolled to, out from under the
  // sticky header and a pinned column; a column with no rows just scrolls into view.
  const showMatch = (m: Cell) => {
    const w = wrap.current
    if (!w) return
    if (m.row < 0) {
      w.querySelector(`thead th[data-col="${m.col}"]`)?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
      return
    }
    setRangeTo(null)
    setSelected(m)
    const cell = w.querySelector<HTMLElement>(`[data-cell="${m.row}-${m.col}"]`)
    if (!cell) return
    cell.scrollIntoView({ block: 'nearest', inline: 'nearest' })
    const box = w.getBoundingClientRect(), r = cell.getBoundingClientRect()
    const head = w.querySelector('thead')?.getBoundingClientRect().height ?? 0
    const pinned = cell.classList.contains('pinned') ? 0
      : Math.max(0, ...[...w.querySelectorAll<HTMLElement>('thead th.pinned, thead th.grid-rownum')].map((th) => th.getBoundingClientRect().right - box.left))
    if (r.top < box.top + head) w.scrollTop -= box.top + head - r.top
    if (r.left < box.left + pinned) w.scrollLeft -= box.left + pinned - r.left
  }
  const stepFind = (by: number) => {
    if (!find || findMatches.length === 0) return
    const at = (findAt + by + findMatches.length) % findMatches.length
    setFind({ ...find, at })
    showMatch(findMatches[at])
  }
  const openFind = () => {
    setFind((f) => f ?? { query: '', at: 0 })
    requestAnimationFrame(() => { findInput.current?.focus(); findInput.current?.select() })
  }
  const closeFind = () => {
    setFind(null)
    wrap.current?.focus({ preventScroll: true })
  }
  // Typing goes to the first match straight away, like a browser's find.
  useEffect(() => {
    if (find && findMatches.length > 0 && find.at === 0) showMatch(findMatches[0])
  }, [findQuery])
  const onFindKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    e.stopPropagation() // the grid's own keys (type to edit, arrows…) aren't for the find box
    if (e.key === 'Enter') { e.preventDefault(); stepFind(e.shiftKey ? -1 : 1) }
    else if (e.key === 'Escape') { e.preventDefault(); closeFind() }
    else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'f') { e.preventDefault(); e.currentTarget.select() }
  }
  // ⌘F from anywhere in the tab (a filter box, a button) opens the find bar of
  // the grid on screen, unless an editor took it first (CodeMirror has its own).
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey || e.key.toLowerCase() !== 'f' || e.defaultPrevented) return
      const w = wrap.current
      if (!w || w.offsetParent === null) return
      e.preventDefault()
      openFind()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // New rows (another page, a reload): the selection no longer points at the
  // same cell. A layout effect, so it's cleared before the new rows are ever
  // shown: no click can pair the old rows' selection (or an open menu's
  // Delete) with the new rows. A JSON editor open on them (a sorted DynamoDB
  // tab re-sorts as a page lands) closes without applying, and says so.
  useLayoutEffect(() => {
    setSelected(null)
    setEditing(null)
    setRowSelection(new Set())
    anchor.current = null
    lead.current = null
    setMenu(null)
    setJsonCell(null)
    setRowJson(null)
    setLinkMenu(null)
    if (rowJson || jsonCell) setCopied({ text: `The ${many} changed (reloaded, or more loaded): the JSON editor closed without applying. Open it again.`, error: true })
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

  // Returns `edits` with row's field `source` (whose value without edits is
  // base) set; setting it back to base drops the edit. type: what the JSON
  // view made the value (a document store's); a typed value drops it.
  const withValue = (edits: Edits, row: number, source: string, base: CellValue, value: CellValue, type?: string): Edits => {
    const id = rowId(row)
    const entry = edits[id] ?? { key: currentKey(row), old: {}, values: {} }
    const values = { ...entry.values }
    const old = { ...entry.old }
    const types = { ...entry.types }
    if (value === base && type === undefined) {
      delete values[source]
      delete old[source]
    } else {
      values[source] = value
      old[source] = base
    }
    if (type === undefined || !(source in values)) delete types[source]
    else types[source] = type
    const next = { ...edits }
    if (Object.keys(values).length === 0) delete next[id]
    else next[id] = { key: entry.key, old, values, ...(Object.keys(types).length > 0 && { types }) }
    return next
  }
  const withCell = (edits: Edits, cell: Cell, value: CellValue): Edits =>
    withValue(edits, cell.row, columns[cell.col].source, baseValue(cell.row, cell.col), value)
  // A field by name, which may have no column yet (one the JSON view added).
  const withField = (edits: Edits, row: number, field: string, value: CellValue, type?: string): Edits => {
    const col = columns.findIndex((c) => c.source === field)
    return withValue(edits, row, field, col < 0 ? undefined : baseValue(row, col), value, type)
  }
  const editOf = (row: number): GridEdit | undefined => pending[rowId(row)]

  const save = async (edits: Edits = pending) => {
    const to = editedTarget.current ?? target
    if ((!to && !store) || saving || Object.keys(edits).length === 0) return
    setSaving(true)
    setSaveError(null)
    try {
      const ids = Object.keys(edits)
      if (store) {
        // The store puts the saved values in the rows it gives the grid.
        const n = await store.save(ids.map((id) => edits[id]))
        changePending({})
        setSavedMessage(`Saved ${count(n)} to ${store.where}`)
        onSaved?.()
        return
      }
      if (!to) return
      const rowEdits: RowEdit[] = ids.map((id) => edits[id])
      const savedRows = (await DBService.SaveRows(to, rowEdits)) ?? []
      const n = savedRows.length
      if (keepSavedValues) {
        // One saved row per edit, in order: keep its new key and values.
        setSaved((current) => {
          const next = { ...current }
          ids.forEach((id, k) => {
            const row = savedRows[k] as { key: GridValues | null; values: GridValues | null } | undefined
            next[id] = { key: row?.key ?? edits[id].key, values: { ...next[id]?.values, ...(row?.values ?? edits[id].values) } }
          })
          return next
        })
      }
      changePending({})
      setSavedMessage(`Saved ${count(n)} to ${to.schema}.${to.table}`)
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
    // While a save runs, new edits would be cleared when it finishes.
    if (saving) return
    // json / jsonb and array values open the JSON editor instead (read-only when they can't be saved).
    if (opensJson(cell)) return openJson(cell)
    if (!isEditable(cell.col)) return
    setSelected(cell)
    setEditing({ cell, initial: typed ?? shownValue(cell.row, cell.col) ?? '', typed: typed !== undefined })
  }

  // result: the new value, or null when the edit was cancelled or left unchanged.
  const finishEdit = (result: { value: CellValue } | null, move?: 'down' | 'right' | 'left', saveAfter?: boolean) => {
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
    const indexes = [...rowSelection].sort((a, b) => a - b)
    const text = store && format === 'json' ? store.rowsJson(indexes, editOf, true) : formatRows(format, columns, shownRows(indexes))
    copy(text, `Copied ${count(indexes.length)} as ${FORMAT_LABELS[format]}`)
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
  // message for the modal, which stays open. A store's rows can gain and
  // lose fields this way too.
  const applyRowJson = (indexes: number[], text: string | null) => {
    if (text === null) return `A whole ${one} can’t be set to NULL.`
    let next = pending
    if (store) {
      const result = store.applyRowsJson(indexes, text, editOf)
      if ('error' in result) return result.error
      for (const c of result.changes) next = withField(next, indexes[c.row], c.field, c.value, c.type)
    } else {
      const result = rowJsonEdits(columns, shownRows(indexes) as (string | null)[][], text, isEditable)
      if ('error' in result) return result.error
      for (const e of result.edits) next = withCell(next, { row: indexes[e.row], col: e.col }, e.value)
    }
    changePending(next)
    setSavedMessage(null)
    closeRowJson()
  }

  const closeMenu = () => {
    setMenu(null)
    wrap.current?.focus({ preventScroll: true }) // so ⌘C works next
  }

  // Selected rows top to bottom, or why they can't be deleted.
  const deletable = [...rowSelection].sort((a, b) => a - b)
  const deleteBlocked = !canEdit ? `Read-only${props.readOnlyReason ? `: ${props.readOnlyReason}` : ''}`
    : changedRows > 0 ? 'Save or discard your changes first'
    : undefined
  // Each row's key, and every value shown from the table (Go checks none
  // changed since), named for the confirmation: "id = 5", or "row 12".
  const askDelete = () => {
    if ((!target && !store) || deleteBlocked) return
    setToDelete({
      target: store ? null : target,
      rows: deletable.map((i) => ({
        key: currentKey(i),
        old: Object.fromEntries(columns.flatMap((c, j) => (c.source ? [[c.source, baseValue(i, j)]] : []))),
      })),
      names: deletable.map((i) => store || target?.kind === 'pk'
        ? keyNames.map((name) => `${name} = ${currentKey(i)[name] ?? 'NULL'}`).join(', ')
        : `row ${(firstRowNumber + i).toLocaleString()}: ${columns.slice(0, 3).map((c, j) => `${c.name} = ${baseValue(i, j) ?? 'NULL'}`).join(', ')}`),
    })
  }
  // Throws to keep the dialog open with the error: then nothing was deleted.
  const runDelete = async () => {
    if (!toDelete) return
    const { target: to, rows: picked } = toDelete
    if (store) {
      // Throws to keep the dialog open; the store takes the rows out of those it gives the grid.
      const n = await store.delete(picked)
      setToDelete(null)
      setRowSelection(new Set())
      setSavedMessage(`Deleted ${count(n)} from ${store.where}`)
      onSaved?.()
      wrap.current?.focus({ preventScroll: true })
      return
    }
    if (!to) return
    stopped.current = false
    const call = DBService.DeleteRows(to, picked)
    deleting.current = call
    let n: number
    try {
      n = await call
    } catch (err) {
      if (!stopped.current) throw err
      // Stopped: Go's transaction rolls back, unless it was already
      // committing. The rows reload to show which.
      onSaved?.()
      throw new Error('Stopped, so nothing was deleted — unless it had just finished. The rows behind this box now show what’s there.')
    } finally {
      deleting.current = null
    }
    setToDelete(null)
    setRowSelection(new Set())
    setSavedMessage(`Deleted ${count(n)} from ${to.schema}.${to.table}`)
    onSaved?.()
    wrap.current?.focus({ preventScroll: true })
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
    if (mod && e.key.toLowerCase() === 'f') {
      e.preventDefault()
      openFind()
      return
    }
    if (mod && e.key.toLowerCase() === 'a') {
      e.preventDefault()
      selectAll()
      return
    }
    if (mod && e.key.toLowerCase() === 'c') {
      if (window.getSelection()?.toString()) return // text picked with the mouse: normal copy
      const range = rangeCells()
      if (range.length > 1 && selected) {
        e.preventDefault()
        const { r0, r1, c0, c1 } = rangeBox()!
        const lines: string[] = []
        for (let r = r0; r <= r1; r++) {
          const vals: string[] = []
          for (let c = c0; c <= c1; c++) { const v = shownValue(r, c); vals.push(v == null ? '' : v) }
          lines.push(vals.join('\t'))
        }
        copy(lines.join('\n'), `Copied ${range.length} cells`)
        return
      }
      if (rowSelection.size > 0) {
        e.preventDefault()
        copyRows('text')
      } else if (selected) {
        e.preventDefault()
        copyCell(selected)
      }
      return
    }
    if (e.key === 'Escape' && rangeTo) {
      setRangeTo(null)
      return
    }
    if (e.key === 'Escape' && rowSelection.size > 0) {
      setRowSelection(new Set())
      return
    }
    // Shift+arrows from a selected cell grow the cell range (rows picked by number keep their own Shift+↑/↓ below).
    if (e.shiftKey && !mod && selected && rowSelection.size === 0 && (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
      e.preventDefault()
      const from = rangeTo ?? selected
      const d: Record<string, [number, number]> = { ArrowUp: [-1, 0], ArrowDown: [1, 0], ArrowLeft: [0, -1], ArrowRight: [0, 1] }
      const to = { row: Math.max(0, Math.min(rows.length - 1, from.row + d[e.key][0])), col: Math.max(0, Math.min(columns.length - 1, from.col + d[e.key][1])) }
      setRangeTo(to.row === selected.row && to.col === selected.col ? null : to)
      wrap.current?.querySelector(`[data-cell="${to.row}-${to.col}"]`)?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
      return
    }
    if ((e.key === 'Backspace' || e.key === 'Delete') && rangeCells().length > 1) {
      e.preventDefault()
      const n = fillRange(rangeCells(), () => null)
      setCopied({ text: n === 0 ? 'These columns can’t be edited' : `${n} cell${n === 1 ? '' : 's'} set to NULL (unsaved)`, error: n === 0 })
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
      setRangeTo(null)
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
      <div className={props.wrapClass} ref={wrap} tabIndex={0} onKeyDown={onKeyDown} onPaste={onPaste}>
        {find && (
          <div className="grid-find-holder">
            <div className="grid-find" role="search">
              <SearchIcon />
              <input ref={findInput} value={find.query} onChange={(e) => setFind({ query: e.target.value, at: 0 })} onKeyDown={onFindKeyDown}
                     placeholder={`Find in ${many}`} aria-label={`Find in ${many}`}
                     spellCheck={false} autoCapitalize="off" autoCorrect="off" autoComplete="off" />
              <span className="grid-find-count" aria-live="polite">
                {!findQuery ? '' : findMatches.length === 0 ? 'No matches' : `${findAt + 1} of ${findMatches.length.toLocaleString()}`}
              </span>
              <button type="button" className="grid-find-btn" onMouseDown={(e) => e.preventDefault()} onClick={() => stepFind(-1)}
                      disabled={findMatches.length === 0} aria-label="Previous match" title="Previous match (Shift+Enter)"><ArrowUpIcon /></button>
              <button type="button" className="grid-find-btn" onMouseDown={(e) => e.preventDefault()} onClick={() => stepFind(1)}
                      disabled={findMatches.length === 0} aria-label="Next match" title="Next match (Enter)"><ArrowDownIcon /></button>
              <button type="button" className="grid-find-btn" onClick={closeFind} aria-label="Close find" title="Close (Esc)"><XIcon /></button>
            </div>
          </div>
        )}
        {columns.length > 0 && (
          <table className={widths ? 'grid sized' : 'grid'} ref={table}
                 style={widths ? { width: widths.reduce((sum, w) => sum + w, 0) } : undefined}>
            {widths && (
              <colgroup>
                <col style={{ width: widths[0] }} />
                {order.map((j) => <col key={j} style={{ width: widths[j + 1] }} />)}
                {/* the filler column takes any space left over */}
                <col />
              </colgroup>
            )}
            <thead>
              <tr>
                <th className="grid-rownum" aria-label="Select all rows" title={allSelected ? 'Clear selection' : `Select all rows (${shortcut('A')})`}
                    onClick={() => (allSelected ? setRowSelection(new Set()) : selectAll())} />
                {order.map((i) => {
                  const c = columns[i]
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
                  const pinned = pinnedIdx.includes(i)
                  return (
                    <th key={i} data-col={i} className={pinClass(i) || undefined} style={{ left: pinLeft(i) }}
                        aria-sort={sorted === 'asc' ? 'ascending' : sorted === 'desc' ? 'descending' : undefined}>
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
                      <button type="button" className={pinned ? 'grid-pin on' : 'grid-pin'} aria-pressed={pinned}
                              aria-label={pinned ? `Unpin ${c.name}` : `Pin ${c.name}`}
                              title={pinned ? 'Unpin: scrolls with the others again' : 'Pin: keep this column in view while scrolling sideways'}
                              onClick={(e) => { e.stopPropagation(); togglePin(i) }}>
                        <PinIcon />
                      </button>
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
                  {order.map((j) => {
                    const value = shownValue(i, j)
                    const category = columns[j].category
                    const tip = value === undefined ? `no ${columns[j].name}` : value ?? 'NULL'
                    const isSelected = selected?.row === i && selected?.col === j
                    const isEditing = editing?.cell.row === i && editing?.cell.col === j
                    const links = props.onFollow && !isEditing ? linksOf(i, j) : []
                    const classes = [
                      cellClass(value, category),
                      isChanged(i, j) && 'cell-changed',
                      isSelected && 'cell-selected',
                      inRange(i, j) && !isSelected && 'cell-in-range',
                      isEditing && 'cell-editing',
                      columnKeys[j].length > 0 && 'cell-fk',
                      found.size > 0 && found.has(`${i}-${j}`) && 'cell-found',
                      pinClass(j).trim(),
                    ].filter(Boolean).join(' ')
                    return (
                      <td
                        key={j}
                        data-cell={`${i}-${j}`}
                        data-col={j}
                        className={classes}
                        style={{ left: pinLeft(j) }}
                        title={isJson(j) || arrayOf(j) ? `${tip} — double-click to open the JSON editor`
                          : isEditable(j) ? `${tip} — double-click to edit` : tip}
                        onMouseDown={(e) => {
                          // A drag from here over other cells selects the range between.
                          if (isEditing || e.button !== 0 || e.shiftKey || e.metaKey || e.ctrlKey) return
                          dragFrom.current = { row: i, col: j }
                          if (!editing) wrap.current?.focus({ preventScroll: true })
                        }}
                        onMouseEnter={() => {
                          const from = dragFrom.current
                          if (!from) return
                          if (from.row === i && from.col === j) { setRangeTo(null); return } // back on the cell it started from
                          if (!wrap.current?.classList.contains('range-dragging')) {
                            wrap.current?.classList.add('range-dragging')
                            window.getSelection()?.removeAllRanges() // the range, not the cells' text, is what's being picked
                          }
                          if (!selected || selected.row !== from.row || selected.col !== from.col) { setSelected(from); setRowSelection(new Set()) }
                          setRangeTo({ row: i, col: j })
                        }}
                        onClick={(e) => {
                          // Clicks inside the cell editor place the cursor; moving focus would end the edit.
                          if (isEditing) return
                          if (e.shiftKey && selected && rowSelection.size === 0) { // Shift-click: the range from the selected cell to here
                            setRangeTo(selected.row === i && selected.col === j ? null : { row: i, col: j })
                            wrap.current?.focus({ preventScroll: true })
                            return
                          }
                          if (e.shiftKey || e.metaKey || e.ctrlKey) selectRow(i, e)
                          else {
                            setRowSelection(new Set())
                            anchor.current = i
                            lead.current = i
                          }
                          if (!dragFrom.current || rangeTo === null || (rangeTo.row === i && rangeTo.col === j && selected?.row === i && selected?.col === j)) setRangeTo(null)
                          if (!rangeTo || !dragFrom.current) setSelected({ row: i, col: j })
                          wrap.current?.focus({ preventScroll: true })
                        }}
                        onDoubleClick={() => { if (!isEditing) startEdit({ row: i, col: j }) }}
                        onContextMenu={(e) => openMenu(e, i, j)}
                      >
                        {isEditing && editing ? (
                          <CellEditor initial={editing.initial} typed={editing.typed} onDone={finishEdit}
                                      removable={store && baseValue(i, j) !== undefined ? one : undefined} />
                        ) : value === null ? 'NULL' : value === undefined ? '' : cellText(value, category)}
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
          noun={[one, many]}
          onCopyValue={menu.cell ? () => { copyCell(menu.cell!); closeMenu() } : undefined}
          onOpenJson={menu.cell && opensJson(menu.cell) ? () => { closeMenu(); openJson(menu.cell!) } : undefined}
          onViewJson={() => { closeMenu(); setRowJson([...rowSelection].sort((a, b) => a - b)) }}
          onCopy={(format) => { copyRows(format); closeMenu() }}
          onDelete={props.canDelete && !deleteBlocked ? () => { closeMenu(); askDelete() } : undefined}
          deleteBlocked={props.canDelete ? deleteBlocked : undefined}
          onClose={closeMenu}
        />
      )}
      {toDelete && (
        <DeleteRowsDialog target={toDelete.target} where={store?.where} noun={[one, many]} rows={toDelete.names} onDelete={runDelete}
                          onStop={store ? undefined : () => { stopped.current = true; deleting.current?.cancel() }}
                          onClose={() => { setToDelete(null); wrap.current?.focus({ preventScroll: true }) }} />
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
            note={`${one} ${(firstRowNumber + jsonCell.row).toLocaleString()}`}
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
            title={rowJson.length === 1 ? `${one[0].toUpperCase()}${one.slice(1)} ${(firstRowNumber + rowJson[0]).toLocaleString()}` : count(rowJson.length)}
            tag="JSON"
            note={store ? store.where : `${columns.length} column${columns.length === 1 ? '' : 's'}`}
            value={store ? store.rowsJson(rowJson, editOf, false) : rowsAsJson(columns, shownRows(rowJson))}
            readOnly={!canEdit}
            readOnlyNote={`This result can’t be edited${props.readOnlyReason ? ` (${props.readOnlyReason})` : ''}. ${shortcut('F')} to search; Copy still works.`}
            hint={store
              ? `Apply keeps changes as unsaved edits until you save (${shortcut('S')}). Add a field to add it, delete one to remove it; the key can’t change.`
              : `Apply keeps changed fields as unsaved edits until you save (${shortcut('S')}). Keys and the primary key can’t change.`}
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
            <strong>{changedCells}</strong> unsaved change{changedCells === 1 ? '' : 's'} in {count(changedRows)}
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
function jsonValue(value: CellValue, element: ReturnType<typeof arrayElement>) {
  if (value === undefined) return null // a field the document hasn't got
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
  removable?: string // a document store's field: offer taking it off the row, named so (an item, a document)
  onDone: (result: { value: CellValue } | null, move?: 'down' | 'right' | 'left', saveAfter?: boolean) => void
}

// The input inside a cell being edited. Enter saves and moves down, Tab moves
// sideways, Esc cancels, ⌘S saves everything; NULL sets the cell to NULL, and
// ✕ (removable) takes the field off the document.
function CellEditor({ initial, typed, removable, onDone }: EditorProps) {
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
  const finish = (result: { value: CellValue } | null, move?: 'down' | 'right' | 'left', saveAfter?: boolean) => {
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
      {removable && (
        <button type="button" className="cell-null-btn" title={`Remove the field from this ${removable}`}
                onMouseDown={(e) => e.preventDefault()} onClick={() => finish({ value: undefined })}>
          ✕
        </button>
      )}
    </div>
  )
}
