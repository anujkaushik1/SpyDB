import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type MouseEvent } from 'react'
import type { DBConfig, TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import { errorMessage } from './Banner'
import { copyText } from './clipboard'
import { ActivityIcon, CodeIcon, XIcon } from './icons'
import { KindIcon, kindClass, tableTooltip } from './tableMeta'
import type { CloseCommand } from './tabActions'
import type { FilterPreset } from './TableTab'
import { useDismiss } from './useDismiss'

// workspace: the connection the tab belongs to (targetKey); other
// connections' tabs stay open but hidden.
export type Tab =
  | { id: string; workspace: string; kind: 'query'; title: string; savedQueryId?: string; initialSql?: string }
  | { id: string; workspace: string; kind: 'connections'; title: string; config: DBConfig }
  | { id: string; workspace: string; kind: 'table'; title: string; table: TableInfo; preset?: FilterPreset }

type Props = {
  tabs: Tab[]
  activeId: string
  onSelect: (id: string) => void
  onClose: (id: string, command: CloseCommand) => void // close it, the others, those to its right, or all
  onDuplicate: (id: string) => void
  dirtyIds: Set<string> // tabs with unsaved cell edits, marked with a dot
}

// Open tables, query tabs and the Connections list; every tab can be closed.
// Right-click a tab for Close / Close Others / Close Tabs to Right / Close
// All, Duplicate, and (table tabs) Copy Name.
export default function TabBar({ tabs, activeId, onSelect, onClose, onDuplicate, dirtyIds }: Props) {
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

  const copyName = async (tab: Tab) => {
    if (tab.kind !== 'table') return
    try {
      await copyText(tab.table.name)
      setCopied({ text: `Copied ${tab.table.name}` })
    } catch (err) {
      setCopied({ text: `Couldn’t copy: ${errorMessage(err)}`, error: true })
    }
  }

  return (
    <div className="tabbar" role="tablist" aria-label="Open tabs">
      {tabs.map((tab) => {
        const active = tab.id === activeId
        const accent = tab.kind === 'table' ? kindClass(tab.table.kind) : `kind-${tab.kind}`

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
            className={`tab ${accent}${active ? ' active' : ''}`}
            onClick={() => onSelect(tab.id)}
            onKeyDown={onKeyDown}
            onAuxClick={onAuxClick}
            onContextMenu={onContextMenu}
            title={tab.kind === 'table' ? tableTooltip(tab.table) : tab.title}
          >
            <span className="tab-icon">
              {tab.kind === 'query' && <CodeIcon />}
              {tab.kind === 'connections' && <ActivityIcon />}
              {tab.kind === 'table' && <KindIcon kind={tab.table.kind} />}
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
          <TabMenu x={menu.x} y={menu.y} onClose={() => setMenu(null)} items={[
            { label: 'Close', onClick: run(() => onClose(id, 'close')) },
            { label: 'Close Others', onClick: run(() => onClose(id, 'others')), disabled: tabs.length < 2 },
            { label: 'Close Tabs to Right', onClick: run(() => onClose(id, 'right')), disabled: at === tabs.length - 1 },
            { label: 'Close All', onClick: run(() => onClose(id, 'all')) },
            'separator',
            ...(menu.tab.kind === 'connections' ? [] : [{ label: 'Duplicate', onClick: run(() => onDuplicate(id)) }]),
            ...(menu.tab.kind === 'table' ? [{ label: 'Copy Name', onClick: run(() => copyName(menu.tab)) }] : []),
          ]} />
        )
      })()}
      {copied && <div className={copied.error ? 'copy-toast error' : 'copy-toast'} role="status">{copied.text}</div>}
    </div>
  )
}

type MenuItem = { label: string; onClick: () => void; disabled?: boolean } | 'separator'

// A tab's right-click menu, kept on screen; ↑ / ↓ move, Enter picks, Esc closes.
function TabMenu({ x, y, items, onClose }: { x: number; y: number; items: MenuItem[]; onClose: () => void }) {
  const root = useRef<HTMLDivElement>(null)
  const [position, setPosition] = useState({ left: x, top: y })
  useDismiss(true, root, onClose)
  useLayoutEffect(() => {
    const el = root.current
    if (!el) return
    const { width, height } = el.getBoundingClientRect()
    setPosition({
      left: x + width > window.innerWidth - 8 ? Math.max(8, x - width) : x,
      top: y + height > window.innerHeight - 8 ? Math.max(8, y - height) : y,
    })
    el.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus()
  }, [x, y])
  useEffect(() => {
    window.addEventListener('resize', onClose)
    window.addEventListener('blur', onClose)
    return () => {
      window.removeEventListener('resize', onClose)
      window.removeEventListener('blur', onClose)
    }
  }, [onClose])
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
    e.preventDefault()
    const buttons = [...(root.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])]
    const i = buttons.indexOf(document.activeElement as HTMLButtonElement)
    buttons[e.key === 'ArrowDown' ? Math.min(i + 1, buttons.length - 1) : Math.max(i - 1, 0)]?.focus()
  }
  // A trailing separator (nothing after it) isn't drawn.
  const shown = items.filter((item, i) => item !== 'separator' || i < items.length - 1)
  return (
    <div className="row-menu" role="menu" ref={root} style={position} onKeyDown={onKeyDown}
         onContextMenu={(e) => e.preventDefault()} onClick={(e) => e.stopPropagation()}>
      {shown.map((item, i) => item === 'separator'
        ? <div key={i} className="row-menu-sep" role="separator" />
        : <button key={item.label} type="button" role="menuitem" className="row-menu-item" onClick={item.onClick} disabled={item.disabled}>
            {item.label}
          </button>)}
    </div>
  )
}
