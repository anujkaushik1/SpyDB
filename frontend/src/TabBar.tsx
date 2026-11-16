import { useEffect, useState, type KeyboardEvent, type MouseEvent, useRef } from 'react'
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

// Open tables, query tabs and the Connections list; every tab can be closed.
// Right-click a tab for Close / Close Others / Close Tabs to Right / Close
// All, Duplicate, and (table tabs) Copy Name.
export default function TabBar({ tabs, activeId, onSelect, onClose, onDuplicate, onReorder, dirtyIds }: Props) {
  // Dragging a tab (the mouse held and moved sideways) moves it live past its
  // neighbours; letting go leaves it there.
  const drag = useRef<{ id: string; startX: number; moved: boolean } | null>(null)
  useEffect(() => {
    const onMove = (e: globalThis.MouseEvent) => {
      const d = drag.current
      if (!d || !onReorder) return
      if (!d.moved && Math.abs(e.clientX - d.startX) < 6) return
      d.moved = true
      document.querySelector(`.tab[data-tab-id="${d.id}"]`)?.classList.add('dragging')
      const over = (document.elementFromPoint(e.clientX, e.clientY) as HTMLElement | null)?.closest('.tab') as HTMLElement | null
      if (!over || over.dataset.tabId === d.id) return
      const box = over.getBoundingClientRect()
      const ids = [...document.querySelectorAll<HTMLElement>('.tabbar .tab')].map((t) => t.dataset.tabId ?? '')
      const from = ids.indexOf(d.id), at = ids.indexOf(over.dataset.tabId ?? '')
      const before = e.clientX < box.left + box.width / 2
      const target = before ? at : at + 1 // where it would go, among the tabs as they are
      if (target === from || target === from + 1) return
      onReorder(d.id, ids[target] ?? null)
    }
    const onUp = () => {
      const d = drag.current
      if (!d) return
      document.querySelector(`.tab[data-tab-id="${d.id}"]`)?.classList.remove('dragging')
      drag.current = null
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    return () => { window.removeEventListener('mousemove', onMove); window.removeEventListener('mouseup', onUp) }
  }, [onReorder])
  const [menu, setMenu] = useState<{ x: number; y: number; tab: Tab } | null>(null)
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
    <div className="tabbar" role="tablist" aria-label="Open tabs">
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
            className={`tab ${accent}${active ? ' active' : ''}`}
            onMouseDown={(e) => { if (e.button === 0 && !(e.target as HTMLElement).closest('.tab-close')) drag.current = { id: tab.id, startX: e.clientX, moved: false } }}
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
