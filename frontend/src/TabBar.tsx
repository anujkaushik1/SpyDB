import type { KeyboardEvent, MouseEvent } from 'react'
import type { TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB'
import { ActivityIcon, XIcon } from './icons'
import { KindIcon, kindClass, tableTooltip } from './tableMeta'

export type Tab =
  | { id: string; kind: 'connections'; title: string }
  | { id: string; kind: 'table'; title: string; table: TableInfo }

type Props = {
  tabs: Tab[]
  activeId: string
  onSelect: (id: string) => void
  onClose: (id: string) => void
}

// Open tables (and the Connections list); every tab can be closed.
export default function TabBar({ tabs, activeId, onSelect, onClose }: Props) {
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
              {tab.kind === 'connections' && <ActivityIcon />}
              {tab.kind === 'table' && <KindIcon kind={tab.table.kind} />}
            </span>
            <span className="tab-title">{tab.title}</span>
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
