import type { KeyboardEvent, MouseEvent } from 'react'
import type { DBConfig, TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB'
import { ActivityIcon, CodeIcon, XIcon } from './icons'
import { KindIcon, kindClass, tableTooltip } from './tableMeta'

// workspace: the connection the tab belongs to (targetKey); other
// connections' tabs stay open but hidden.
export type Tab =
  | { id: string; workspace: string; kind: 'query'; title: string; savedQueryId?: string; initialSql?: string }
  | { id: string; workspace: string; kind: 'connections'; title: string; config: DBConfig }
  | { id: string; workspace: string; kind: 'table'; title: string; table: TableInfo }

type Props = {
  tabs: Tab[]
  activeId: string
  onSelect: (id: string) => void
  onClose: (id: string) => void
  dirtyIds: Set<string> // tabs with unsaved cell edits, marked with a dot
}

// Open tables, query tabs and the Connections list; every tab can be closed.
export default function TabBar({ tabs, activeId, onSelect, onClose, dirtyIds }: Props) {
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
          if (e.button === 1) onClose(tab.id)
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
              onClick={(e) => { e.stopPropagation(); onClose(tab.id) }}
            >
              <XIcon />
            </button>
          </div>
        )
      })}
    </div>
  )
}
