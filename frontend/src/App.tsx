import { useCallback, useEffect, useState } from 'react'
import { DBService, type ConnectionInfo, type SchemaInfo, type TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB'
import { errorMessage } from './Banner'
import ConnectionForm from './ConnectionForm'
import SchemaSidebar from './SchemaSidebar'
import ServerConnectionsPanel from './ServerConnectionsPanel'
import TabBar, { type Tab } from './TabBar'
import TablePalette from './TablePalette'
import TableTab from './TableTab'
import { TableIcon } from './icons'

function App() {
  // The open connection, or null when disconnected.
  const [connection, setConnection] = useState<ConnectionInfo | null>(null)
  const [disconnecting, setDisconnecting] = useState(false)
  // Schemas and tables, shared by the sidebar and the ⌘P / Ctrl+P switcher.
  const [schemas, setSchemas] = useState<SchemaInfo[] | null>(null)
  const [schemasLoading, setSchemasLoading] = useState(false)
  const [schemasError, setSchemasError] = useState<string | null>(null)
  const [tabs, setTabs] = useState<Tab[]>([])
  const [activeTab, setActiveTab] = useState('')
  const [paletteOpen, setPaletteOpen] = useState(false)

  const loadSchemas = useCallback(async () => {
    setSchemasLoading(true)
    try {
      setSchemas((await DBService.ListTables()) ?? [])
      setSchemasError(null)
    } catch (err) {
      setSchemasError(errorMessage(err))
    } finally {
      setSchemasLoading(false)
    }
  }, [])

  useEffect(() => {
    if (connection) loadSchemas()
  }, [connection, loadSchemas])

  // ⌘P / Ctrl+P opens the table switcher while connected.
  useEffect(() => {
    if (!connection) return
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'p') {
        e.preventDefault()
        setPaletteOpen(true)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [connection])

  const changeConnection = (next: ConnectionInfo | null) => {
    setConnection(next)
    if (!next) {
      setTabs([])
      setActiveTab('')
      setSchemas(null)
      setPaletteOpen(false)
    }
  }

  const disconnect = async () => {
    setDisconnecting(true)
    try {
      await DBService.Disconnect()
      changeConnection(null)
    } finally {
      setDisconnecting(false)
    }
  }

  // Opens a tab, or switches to it if it's already open.
  const openTab = (tab: Tab) => {
    setTabs((current) => (current.some((t) => t.id === tab.id) ? current : [...current, tab]))
    setActiveTab(tab.id)
  }
  const openTable = (table: TableInfo) =>
    openTab({ id: `table:${table.qualifiedName}`, kind: 'table', title: table.name, table })
  const openConnections = () => openTab({ id: 'connections', kind: 'connections', title: 'Connections' })

  // Closing the active tab moves to the tab on its left (or the right, if it was first).
  const closeTab = (id: string) => {
    const index = tabs.findIndex((t) => t.id === id)
    const next = tabs.filter((t) => t.id !== id)
    setTabs(next)
    if (id === activeTab) setActiveTab(next[Math.max(0, index - 1)]?.id ?? '')
  }

  const active = tabs.find((t) => t.id === activeTab)
  const activeTable = active?.kind === 'table' ? active.table.qualifiedName : null

  return (
    <div className="app">
      {/* Sits under the macOS traffic lights; drags the window. */}
      <header className="titlebar">
        <span className="titlebar-title">SpyDB</span>
        {connection && (
          <span className="titlebar-status">
            <span className="status-dot" aria-hidden="true" />
            {connection.database}
          </span>
        )}
      </header>

      <div className="workspace">
        {connection && (
          <SchemaSidebar
            schemas={schemas}
            loading={schemasLoading}
            error={schemasError}
            onRefresh={loadSchemas}
            activeTable={activeTable}
            onOpenTable={openTable}
            onOpenPalette={() => setPaletteOpen(true)}
            connection={connection}
            onShowConnections={openConnections}
            onDisconnect={disconnect}
            disconnecting={disconnecting}
          />
        )}
        <main className={connection ? 'main-area' : 'content'}>
          {/* Stays mounted (and renders nothing) while connected, so the form keeps its values. */}
          <ConnectionForm connection={connection} onConnectionChange={changeConnection} />
          {connection && tabs.length === 0 && (
            <div className="workspace-empty">
              <span className="workspace-empty-icon"><TableIcon /></span>
              <p className="workspace-empty-title">Open a table</p>
              <p className="workspace-empty-text">
                Pick one from the sidebar, or <button type="button" className="link-btn" onClick={() => setPaletteOpen(true)}>search tables</button> with <kbd>Ctrl</kbd> <kbd>P</kbd>.
                Click <kbd>&lt;/&gt;</kbd> in a table to write SQL.
              </p>
            </div>
          )}
          {connection && tabs.length > 0 && (
            <>
              <TabBar tabs={tabs} activeId={activeTab} onSelect={setActiveTab} onClose={closeTab} />
              {/* Every open tab stays mounted, so switching keeps its page, filters, SQL and results. */}
              {tabs.map((tab) => (
                <div key={tab.id} role="tabpanel" className={`tab-panel tab-panel-${tab.kind}`} hidden={tab.id !== activeTab}>
                  {tab.kind === 'table' && <TableTab table={tab.table} tabId={tab.id} onSchemaChange={loadSchemas} />}
                  {tab.kind === 'connections' && (
                    <ServerConnectionsPanel
                      config={{ host: connection.host, port: connection.port, user: connection.user, password: '', database: connection.database }}
                      temporary={false}
                    />
                  )}
                </div>
              ))}
            </>
          )}
        </main>
      </div>

      {connection && paletteOpen && (
        <TablePalette
          schemas={schemas ?? []}
          onOpen={(table) => { openTable(table); setPaletteOpen(false) }}
          onClose={() => setPaletteOpen(false)}
        />
      )}
    </div>
  )
}

export default App
