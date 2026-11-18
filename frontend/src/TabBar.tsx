import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent, type MouseEvent } from 'react'
import type { DBConfig, TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import { errorMessage } from './Banner'
import { copyText } from './clipboard'
import { ActivityIcon, CodeIcon, TableIcon, XIcon } from './icons'
import { KindIcon, kindClass, tableTooltip } from './tableMeta'
import type { CloseCommand } from './tabActions'
import type { FilterPreset } from './TableTab'
import ContextMenu from './ContextMenu'

// workspace: the connection the tab belongs to (targetKey); other
// connections' tabs stay open but hidden.
export type Tab =
  | { id: string; workspace: string; kind: 'query'; title: string; savedQueryId?: string; initialSql?: string }
  | { id: string; workspace: string; kind: 'connections'; title: string; config: DBConfig }
  | { id: string; workspace: string; kind: 'table'; title: string; table: TableInfo; preset?: FilterPreset }
  | { id: string; workspace: string; kind: 'dynamo-table'; title: string; table: string } // table: its name
  | { id: string; workspace: string; kind: 'dynamo-query'; title: string; initialStatement?: string } // a PartiQL tab

type Props = {
  tabs: Tab[]
  activeId: string
  onSelect: (id: string) => void
  onClose: (id: string, command: CloseCommand) => void // close it, the others, those to its right, or all
  onDuplicate: (id: string) => void
  onReorder?: (id: string, beforeId: string | null) => void // a tab dragged: put it before beforeId (null: last)
  dirtyIds: Set<string> // tabs with unsaved cell edits, marked with a dot
}

// A tab being dragged: where it started (from, among rects, the tabs' places
// at the start), how far it has moved (dx), the slot it would land in (to).
type Drag = { id: string; from: number; startX: number; dx: number; to: number; moved: boolean; settling: boolean; rects: { id: string; left: number; width: number; right: number }[] }
const SETTLE_MS = 130 // the slide into the slot when a dragged tab is let go

// Open tables, query tabs and the Connections list; every tab can be closed.
// Right-click a tab for Close / Close Others / Close Tabs to Right / Close
// All, Duplicate, and (table tabs) Copy Name.
export default function TabBar({ tabs, activeId, onSelect, onClose, onDuplicate, onReorder, dirtyIds }: Props) {
  // Dragging a tab: it follows the mouse while the tabs it passes slide
  // aside; letting go slides it into its slot, and then the order changes
  // for real (in the same render as the slide's end, so nothing jumps).
  const [drag, setDrag] = useState<Drag | null>(null)
  const dragRef = useRef<Drag | null>(null)
  const reorderRef = useRef(onReorder)
  reorderRef.current = onReorder
  const settle = useRef<number | undefined>(undefined)
  useEffect(() => () => window.clearTimeout(settle.current), [])
  const startDrag = (e: MouseEvent<HTMLDivElement>, id: string) => {
    if (e.button !== 0 || !onReorder || (e.target as HTMLElement).closest('.tab-close')) return
    const bar = e.currentTarget.parentElement
    const rects = [...(bar?.querySelectorAll<HTMLElement>('.tab') ?? [])].map((t) => { const r = t.getBoundingClientRect(); return { id: t.dataset.tabId ?? '', left: r.left, width: r.width, right: r.right } })
    const from = rects.findIndex((r) => r.id === id)
    if (from < 0) return
    dragRef.current = { id, from, startX: e.clientX, dx: 0, to: from, moved: false, settling: false, rects }
    const onMove = (ev: globalThis.MouseEvent) => {
      const cur = dragRef.current
      if (!cur || cur.settling) return
      const dx = ev.clientX - cur.startX
      if (!cur.moved && Math.abs(dx) < 6) return // a click, so far
      const me = cur.rects[cur.from]
      const centre = me.left + me.width / 2 + dx
      // Its slot: after every other tab whose centre it has passed.
      const to = cur.rects.filter((r) => r.id !== cur.id && r.left + r.width / 2 < centre).length
      dragRef.current = { ...cur, dx, to, moved: true }
      setDrag(dragRef.current)
    }
    const onUp = () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
      const cur = dragRef.current
      if (!cur) return
      if (!cur.moved) { dragRef.current = null; return }
      const me = cur.rects[cur.from]
      const slotLeft = cur.to > cur.from ? cur.rects[cur.to].right - me.width : cur.rects[cur.to].left
      const settling: Drag = { ...cur, dx: slotLeft - me.left, settling: true }
      dragRef.current = settling
      setDrag(settling)
      settle.current = window.setTimeout(() => {
        if (dragRef.current !== settling) return
        dragRef.current = null
        setDrag(null)
        if (cur.to !== cur.from) {
          const others = cur.rects.filter((r) => r.id !== cur.id)
          reorderRef.current?.(cur.id, others[cur.to]?.id ?? null)
        }
      }, SETTLE_MS)
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }
  // Where a tab is while one is dragged: that one under the mouse, the ones it passed slid aside.
  const dragStyle = (id: string): CSSProperties | undefined => {
    if (!drag) return undefined
    if (id === drag.id) return { transform: `translateX(${drag.dx}px)`, transition: drag.settling ? `transform ${SETTLE_MS}ms ease` : 'none', zIndex: 2 }
    const i = drag.rects.findIndex((r) => r.id === id)
    if (i < 0) return undefined
    const j = i < drag.from ? i : i - 1 // its place among the others
    const gap = drag.rects.length > 1 ? drag.rects[1].left - drag.rects[0].right : 0
    const width = drag.rects[drag.from].width + gap
    const shift = j >= drag.to && j < drag.from ? width : j >= drag.from && j < drag.to ? -width : 0
    return { transform: `translateX(${shift}px)`, transition: 'transform 160ms ease' }
  }
  const [menu, setMenu] = useState<{ x: number; y: number; tab: Tab } | null>(null)
  // The active tab stays in view: one opened, or picked, past the bar's edge scrolls the bar to it.
  const bar = useRef<HTMLDivElement>(null)
  useEffect(() => {
    bar.current?.querySelector<HTMLElement>(`[data-tab-id="${CSS.escape(activeId)}"]`)?.scrollIntoView({ inline: 'nearest', block: 'nearest' })
  }, [activeId, tabs.length])
  const [copied, setCopied] = useState<{ text: string; error?: boolean } | null>(null)
  useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => setCopied(null), 2500)
    return () => clearTimeout(timer)
  }, [copied])
  // The menu's tab closed meanwhile (a click elsewhere, another command): drop the menu.
  useEffect(() => {
    if (menu && !tabs.some((t) => t.id === menu.tab.id)) setMenu(null)
  }, [tabs])

  // A table tab's table name: Postgres's without its schema, DynamoDB's as it is.
  const tableName = (tab: Tab) => (tab.kind === 'table' ? tab.table.name : tab.kind === 'dynamo-table' ? tab.table : null)
  const copyName = async (tab: Tab) => {
    const name = tableName(tab)
    if (name === null) return
    try {
      await copyText(name)
      setCopied({ text: `Copied ${name}` })
    } catch (err) {
      setCopied({ text: `Couldn’t copy: ${errorMessage(err)}`, error: true })
    }
  }

  return (
    <div className="tabbar" role="tablist" aria-label="Open tabs" ref={bar}>
      {tabs.map((tab) => {
        const active = tab.id === activeId
        const accent = tab.kind === 'table' ? kindClass(tab.table.kind) : tab.kind === 'dynamo-table' ? 'kind-table'
          : tab.kind === 'dynamo-query' ? 'kind-query' : `kind-${tab.kind}`

        const onKeyDown = (e: KeyboardEvent) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            onSelect(tab.id)
          }
        }
        // Middle-click closes, as in browsers.
        const onAuxClick = (e: MouseEvent) => {
          if (e.button === 1) onClose(tab.id, 'close')
        }
        const onContextMenu = (e: MouseEvent) => {
          e.preventDefault()
          setMenu({ x: e.clientX, y: e.clientY, tab })
        }

        return (
          <div
            key={tab.id}
            role="tab"
            tabIndex={0}
            aria-selected={active}
            data-tab-id={tab.id}
            className={`tab ${accent}${active ? ' active' : ''}${drag?.id === tab.id && drag.moved ? ' dragging' : ''}`}
            style={dragStyle(tab.id)}
            onMouseDown={(e) => startDrag(e, tab.id)}
            onClick={() => onSelect(tab.id)}
            onKeyDown={onKeyDown}
            onAuxClick={onAuxClick}
            onContextMenu={onContextMenu}
            title={tab.kind === 'table' ? tableTooltip(tab.table) : tab.title}
          >
            <span className="tab-icon">
              {(tab.kind === 'query' || tab.kind === 'dynamo-query') && <CodeIcon />}
              {tab.kind === 'connections' && <ActivityIcon />}
              {tab.kind === 'table' && <KindIcon kind={tab.table.kind} />}
              {tab.kind === 'dynamo-table' && <TableIcon />}
            </span>
            <span className="tab-title">{tab.title}</span>
            {dirtyIds.has(tab.id) && <span className="tab-dirty" title="Unsaved changes" aria-label="Unsaved changes" />}
            <button
              type="button"
              className="tab-close"
              aria-label={`Close ${tab.title}`}
              onClick={(e) => { e.stopPropagation(); onClose(tab.id, 'close') }}
            >
              <XIcon />
            </button>
          </div>
        )
      })}
      {menu && (() => {
        const at = tabs.findIndex((t) => t.id === menu.tab.id)
        const run = (action: () => void) => () => { setMenu(null); action() }
        const id = menu.tab.id
        return (
          <ContextMenu x={menu.x} y={menu.y} onClose={() => setMenu(null)} items={[
            { label: 'Close', onClick: run(() => onClose(id, 'close')) },
            { label: 'Close Others', onClick: run(() => onClose(id, 'others')), disabled: tabs.length < 2 },
            { label: 'Close Tabs to Right', onClick: run(() => onClose(id, 'right')), disabled: at === tabs.length - 1 },
            { label: 'Close All', onClick: run(() => onClose(id, 'all')) },
            'separator',
            ...(menu.tab.kind === 'connections' ? [] : [{ label: 'Duplicate', onClick: run(() => onDuplicate(id)) }]),
            ...(tableName(menu.tab) !== null ? [{ label: 'Copy Name', onClick: run(() => copyName(menu.tab)) }] : []),
          ]} />
        )
      })()}
      {copied && <div className={copied.error ? 'copy-toast error' : 'copy-toast'} role="status">{copied.text}</div>}
    </div>
  )
}
