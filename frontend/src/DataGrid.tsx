import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from 'react'
import { DBService, type EditTarget, type RowEdit } from '../bindings/github.com/anujkaushik1/SpyDB'
import Banner, { errorDetail, errorMessage } from './Banner'
import { ArrowDownIcon, ArrowUpIcon, KeyIcon, Spinner } from './icons'
import { cellClass, cellText } from './tableMeta'

export type GridColumn = {
  name: string
  type: string
  category: string // number, bool, time, json, text or other (for colouring)
  primaryKey: boolean
  source: string // the table column an edit saves to; '' = read-only
}

export type Sort = { column: string; desc: boolean } | null

type Cell = { row: number; col: number }

// Edited cells, keyed by the row's original primary-key values (JSON), so an
// edit stays attached to its row across pages and reloads.
type Edits = Record<string, { key: Record<string, string | null>; values: Record<string, string | null> }>

type Props = {
  columns: GridColumn[]
  rows: (string | null)[][]
  firstRowNumber: number
  target: EditTarget | null // where edits save; null = read-only
  keepSavedValues?: boolean // query results: show saved values (the rows aren't reloaded)
  onSaved?: () => void // after a successful save; table tabs reload their rows
  onDirtyChange?: (dirty: boolean) => void
  sort?: Sort
  onSort?: (column: string) => void // headers sort when given
  wrapClass: string
  wrapRef?: RefObject<HTMLDivElement>
  children?: ReactNode // shown under the table, e.g. "This table is empty."
}

// The grid of rows. When `target` is set and the primary key is among the
// columns, cells can be edited: click to select, double-click / Enter / type
// to edit. Changes wait in a bar at the bottom until Save, which sends them
// as UPDATEs in one transaction (DBService.SaveRows).
export default function DataGrid(props: Props) {
  const { columns, rows, firstRowNumber, target, keepSavedValues, onSaved, onDirtyChange, sort, onSort } = props
  const [selected, setSelected] = useState<Cell | null>(null)
  const [editing, setEditing] = useState<{ cell: Cell; initial: string; typed: boolean } | null>(null)
  const [pending, setPending] = useState<Edits>({})
  const [saved, setSaved] = useState<Edits>({})
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<{ message: string; detail?: string } | null>(null)
  const [savedMessage, setSavedMessage] = useState<string | null>(null)
  const ownRef = useRef<HTMLDivElement>(null)
  const wrap = props.wrapRef ?? ownRef

  const keyIndexes = columns.flatMap((c, i) => (c.primaryKey && c.source ? [i] : []))
  const canEdit = target !== null && keyIndexes.length > 0
  const isEditable = (col: number) => canEdit && columns[col]?.source !== ''

  const rowId = (row: (string | null)[]) => JSON.stringify(keyIndexes.map((i) => row[i]))
  // The value without unsaved edits: saved (query results) or as loaded.
  const baseValue = (row: (string | null)[], col: number) => {
    const values = saved[rowId(row)]?.values
    const source = columns[col].source
    return values && source in values ? values[source] : row[col]
  }
  const shownValue = (row: (string | null)[], col: number) => {
    const values = pending[rowId(row)]?.values
    const source = columns[col].source
    return values && source && source in values ? values[source] : baseValue(row, col)
  }
  const isChanged = (row: (string | null)[], col: number) => {
    const values = pending[rowId(row)]?.values
    return !!values && columns[col].source in values
  }

  const changedRows = Object.keys(pending).length
  const changedCells = Object.values(pending).reduce((n, e) => n + Object.keys(e.values).length, 0)

  // New rows (another page, a reload): the selection no longer points at the same cell.
  useEffect(() => {
    setSelected(null)
    setEditing(null)
  }, [rows])

  useEffect(() => onDirtyChange?.(changedRows > 0), [changedRows > 0])

  useEffect(() => {
    if (!savedMessage) return
    const timer = setTimeout(() => setSavedMessage(null), 3000)
    return () => clearTimeout(timer)
  }, [savedMessage])

  // Returns `edits` with one cell set; setting it back to its base value drops the edit.
  const withCell = (edits: Edits, cell: Cell, value: string | null): Edits => {
    const row = rows[cell.row]
    const id = rowId(row)
    const source = columns[cell.col].source
    const entry = edits[id] ?? {
      key: Object.fromEntries(keyIndexes.map((i) => [columns[i].source, baseValue(row, i)])),
      values: {},
    }
    const values = { ...entry.values }
    if (value === baseValue(row, cell.col)) delete values[source]
    else values[source] = value
    const next = { ...edits }
    if (Object.keys(values).length === 0) delete next[id]
    else next[id] = { key: entry.key, values }
    return next
  }

  const save = async (edits: Edits = pending) => {
    if (!target || saving || Object.keys(edits).length === 0) return
    setSaving(true)
    setSaveError(null)
    try {
      const rowEdits: RowEdit[] = Object.values(edits).map((e) => ({ key: e.key, values: e.values }))
      const count = await DBService.SaveRows(target, rowEdits)
      if (keepSavedValues) {
        setSaved((current) => {
          const next = { ...current }
          for (const [id, e] of Object.entries(edits)) next[id] = { key: e.key, values: { ...next[id]?.values, ...e.values } }
          return next
        })
      }
      setPending({})
      setSavedMessage(`Saved ${count} row${count === 1 ? '' : 's'} to ${target.schema}.${target.table}`)
      onSaved?.()
    } catch (err) {
      setPending(edits) // keep them, so nothing typed is lost
      setSaveError({ message: errorMessage(err), detail: errorDetail(err) })
    } finally {
      setSaving(false)
    }
  }

  const discard = () => {
    setPending({})
    setSaveError(null)
  }

  const startEdit = (cell: Cell, typed?: string) => {
    if (!isEditable(cell.col)) return
    setSelected(cell)
    setEditing({ cell, initial: typed ?? shownValue(rows[cell.row], cell.col) ?? '', typed: typed !== undefined })
  }

  // result: the new value, or null when the edit was cancelled or left unchanged.
  const finishEdit = (result: { value: string | null } | null, move?: 'down' | 'right' | 'left', saveAfter?: boolean) => {
    if (!editing) return
    const { cell } = editing
    const next = result ? withCell(pending, cell, result.value) : pending
    if (result) {
      setPending(next)
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

  // Keys on the grid itself; while a cell is being edited its input handles them.
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (editing) return
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
      e.preventDefault()
      save()
      return
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
          <table className="grid">
            <thead>
              <tr>
                <th className="grid-rownum" aria-label="Row number" />
                {columns.map((c, i) => {
                  const sorted = sort?.column === c.name ? (sort.desc ? 'desc' : 'asc') : null
                  const label = (
                    <>
                      {c.primaryKey && <span className="grid-key" aria-label="Primary key"><KeyIcon /></span>}
                      <span className="grid-col-name">{c.name}</span>
                      {c.type && <span className="grid-col-type">{c.type}</span>}
                    </>
                  )
                  return (
                    <th key={i} aria-sort={sorted === 'asc' ? 'ascending' : sorted === 'desc' ? 'descending' : undefined}>
                      {onSort ? (
                        <button type="button" className="grid-head" onClick={() => onSort(c.name)}
                                title={`${c.name} · ${c.type}${c.primaryKey ? ' · primary key' : ''} — click to sort`}>
                          {label}
                          <span className={sorted ? 'grid-sort active' : 'grid-sort'}>
                            {sorted === 'desc' ? <ArrowDownIcon /> : <ArrowUpIcon />}
                          </span>
                        </button>
                      ) : (
                        <div className="grid-head static">{label}</div>
                      )}
                    </th>
                  )
                })}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={i}>
                  <td className="grid-rownum">{firstRowNumber + i}</td>
                  {row.map((_, j) => {
                    const value = shownValue(row, j)
                    const category = columns[j].category
                    const isSelected = selected?.row === i && selected?.col === j
                    const isEditing = editing?.cell.row === i && editing?.cell.col === j
                    const classes = [
                      cellClass(value, category),
                      isChanged(row, j) && 'cell-changed',
                      isSelected && 'cell-selected',
                      isEditing && 'cell-editing',
                    ].filter(Boolean).join(' ')
                    return (
                      <td
                        key={j}
                        data-cell={`${i}-${j}`}
                        className={classes}
                        title={isEditable(j) ? `${value ?? 'NULL'} — double-click to edit` : value ?? 'NULL'}
                        onClick={() => { setSelected({ row: i, col: j }); wrap.current?.focus({ preventScroll: true }) }}
                        onDoubleClick={() => startEdit({ row: i, col: j })}
                      >
                        {isEditing && editing ? (
                          <CellEditor initial={editing.initial} typed={editing.typed} onDone={finishEdit} />
                        ) : value === null ? 'NULL' : cellText(value, category)}
                      </td>
                    )
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {props.children}
      </div>

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
          <span className="changes-hint">⌘S to save</span>
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
