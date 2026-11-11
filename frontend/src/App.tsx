import { useCallback, useEffect, useRef, useState } from 'react'
import { DBService, type ConnectionInfo, type ConnectionLists, type SavedConnection, type SavedQuery, type SchemaInfo, type TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import { errorMessage } from './Banner'
import ConnectionForm, { type FormRequest } from './ConnectionForm'
import ConnectionsSidebar from './ConnectionsSidebar'
import DynamoSignIn from './dynamo/DynamoSignIn'
import DatabasePicker from './DatabasePicker'
import ImportDialog from './ImportDialog'
import QueryTab from './QueryTab'
import SavedQueriesPanel from './SavedQueriesPanel'
import SchemaSidebar from './SchemaSidebar'
import ServerConnectionsPanel from './ServerConnectionsPanel'
import TabBar, { type Tab } from './TabBar'
import TablePalette from './TablePalette'
import TableTab, { type FollowTarget } from './TableTab'
import { targetKey } from './connectionSearch'
import { CodeIcon, TableIcon } from './icons'
import { upsertQuery } from './savedQueries'
import { completionSchema, type CompletionSchema } from './sqlCompletion'
import { SqlSchemaContext } from './sqlSchema'

function App() {
  // The open connection, or null when disconnected.
  const [connection, setConnection] = useState<ConnectionInfo | null>(null)
  const [disconnecting, setDisconnecting] = useState(false)
  // Schemas and tables, shared by the sidebar and the ⌘P / Ctrl+P switcher.
  const [schemas, setSchemas] = useState<SchemaInfo[] | null>(null)
  const [schemasLoading, setSchemasLoading] = useState(false)
  const [schemasError, setSchemasError] = useState<string | null>(null)
  // Tables and columns for the SQL editors' autocomplete; loads with the sidebar.
  const [completion, setCompletion] = useState<CompletionSchema | undefined>(undefined)
  // Open tabs of every connection used this session. Each connection (server,
  // login, database) is a workspace: switching away hides its tabs, switching
  // back shows them as they were (SQL, results, filters, unsaved edits).
  const [tabs, setTabs] = useState<Tab[]>([])
  const [activeTabs, setActiveTabs] = useState<Record<string, string>>({}) // workspace → tab id
  const [paletteOpen, setPaletteOpen] = useState(false)
  // Which database the connect screen is for.
  const [engine, setEngine] = useState<'postgres' | 'dynamo'>('postgres')
  // The Import a file as a new table dialog.
  const [importOpen, setImportOpen] = useState(false)
  // Tabs with unsaved cell edits.
  const [dirtyTabs, setDirtyTabs] = useState<Set<string>>(new Set())
  // Numbers for query tabs: Query 1, Query 2, … (not reused after closing).
  const queryNumber = useRef(0)
  // Disconnected screen: saved + recent connections, and what the form should load.
  const [lists, setLists] = useState<ConnectionLists | null>(null)
  const [listsError, setListsError] = useState<string | null>(null)
  const [selectedSaved, setSelectedSaved] = useState<string | null>(null)
  const [formRequest, setFormRequest] = useState<FormRequest | null>(null)
  // The left rail picks the sidebar's view. Saved queries are shared by every
  // connection (queries.json, see queries.go).
  const [sidebarView, setSidebarView] = useState<'tables' | 'queries'>('tables')
  const [savedQueries, setSavedQueries] = useState<SavedQuery[] | null>(null)
  const [queriesLoading, setQueriesLoading] = useState(false)
  const [queriesError, setQueriesError] = useState<string | null>(null)

  const loadSavedQueries = useCallback(async () => {
    setQueriesLoading(true)
    try {
      setSavedQueries((await DBService.SavedQueries()) ?? [])
      setQueriesError(null)
    } catch (err) {
      setQueriesError(errorMessage(err))
    } finally {
      setQueriesLoading(false)
    }
  }, [])

  useEffect(() => {
    loadSavedQueries()
  }, [loadSavedQueries])

  const loadLists = useCallback(async () => {
    try {
      setLists(await DBService.SavedConnections())
      setListsError(null)
    } catch (err) {
      setListsError(errorMessage(err))
    }
  }, [])

  // Reload on every return to the connect screen; the last connect is now in Recent.
  useEffect(() => {
    if (!connection) loadLists()
  }, [connection, loadLists])

  const loadSchemas = useCallback(async () => {
    setSchemasLoading(true)
    // Columns come separately; autocomplete without them still knows keywords.
    DBService.ListColumns()
      .then((tables) => setCompletion(completionSchema(tables ?? [])))
      .catch(() => setCompletion(undefined))
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

  // While connected: ⌘P / Ctrl+P opens the table switcher, ⌘T / Ctrl+T a new query tab.
  useEffect(() => {
    if (!connection) return
    const onKeyDown = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey)) return
      const key = e.key.toLowerCase()
      if (key === 'p') {
        e.preventDefault()
        setPaletteOpen(true)
      } else if (key === 't') {
        e.preventDefault()
        newQueryTab()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [connection])

  const workspace = connection ? targetKey(connection) : null
  const workspaceTabs = tabs.filter((t) => t.workspace === workspace)
  const activeTab = (workspace && activeTabs[workspace]) || ''
  const setActiveTab = (id: string) => {
    if (workspace) setActiveTabs((current) => ({ ...current, [workspace]: id }))
  }

  // Disconnecting keeps every workspace's tabs; reconnecting to the same
  // connection brings them back.
  const changeConnection = (next: ConnectionInfo | null) => {
    setConnection(next)
    if (!next) {
      setSchemas(null)
      setPaletteOpen(false)
    }
  }

  const pickConnection = (c: SavedConnection, saved: boolean, connect: boolean) => {
    setSelectedSaved(saved ? c.id : null)
    setFormRequest((r) => ({ config: c.config, savedId: saved ? c.id : null, name: saved ? c.name : '', connect, seq: (r?.seq ?? 0) + 1 }))
  }

  const newConnection = async () => {
    setSelectedSaved(null)
    let config = { host: 'localhost', port: 5432, user: '', password: '', database: '' }
    try {
      config = await DBService.DefaultConfig()
    } catch { /* keep the blank form */ }
    setFormRequest((r) => ({ config, savedId: null, name: '', connect: false, seq: (r?.seq ?? 0) + 1 }))
  }

  const deleteSaved = async (id: string) => {
    try {
      await DBService.DeleteConnection(id)
      if (id === selectedSaved) setSelectedSaved(null)
    } catch (err) {
      setListsError(errorMessage(err))
    }
    loadLists()
  }

  const clearRecent = async () => {
    try {
      await DBService.ClearRecent()
    } catch (err) {
      setListsError(errorMessage(err))
    }
    loadLists()
  }

  // After switching database or server. The old connection's tabs stay in
  // their workspace; this one's (if any) come back.
  const reconnected = (info: ConnectionInfo) => {
    setSchemas(null)
    setPaletteOpen(false)
    setConnection(info) // a new object, so the schema list reloads
  }

  // Reconnect to another database on the same server. Throws (and keeps
  // everything) if it fails.
  const switchDatabase = async (database: string) => {
    reconnected(await DBService.SwitchDatabase(database))
  }

  // Connect to a saved connection while connected: Connect opens the new one
  // before closing this one, so a failure keeps everything.
  const switchConnection = async (c: SavedConnection) => {
    reconnected(await DBService.Connect(c.config))
    // The connect screen shows this connection (with its password) after a disconnect.
    setSelectedSaved(c.id)
    setFormRequest((r) => ({ config: c.config, savedId: c.id, name: c.name, connect: false, seq: (r?.seq ?? 0) + 1 }))
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
  // Ids include the workspace, so the same table on two servers gets two tabs
  // (and two result stores on the Go side).
  const openTable = (table: TableInfo) => {
    if (workspace) openTab({ id: `${workspace}|table:${table.qualifiedName}`, workspace, kind: 'table', title: table.name, table })
  }
  // A foreign key value clicked in a grid: open the table it points at (or
  // switch to its tab) filtered to that row. A table the sidebar doesn't list
  // (a partition) opens all the same.
  const presetSeq = useRef(0)
  const followKey = ({ schema, table: name, qualifiedName, filters }: FollowTarget) => {
    if (!workspace) return
    const table = schemas?.find((s) => s.name === schema)?.tables?.find((t) => t.name === name)
      ?? { schema, name, kind: 'table', qualifiedName, estimatedRows: null }
    const id = `${workspace}|table:${table.qualifiedName}`
    const preset = { filters, seq: ++presetSeq.current }
    setTabs((current) => current.some((t) => t.id === id)
      ? current.map((t) => (t.id === id && t.kind === 'table' ? { ...t, preset } : t))
      : [...current, { id, workspace, kind: 'table', title: table.name, table, preset }])
    setActiveTab(id)
  }
  const openConnections = () => {
    if (!connection || !workspace) return
    const config = { host: connection.host, port: connection.port, user: connection.user, password: '', database: connection.database }
    openTab({ id: `${workspace}|connections`, workspace, kind: 'connections', title: 'Connections', config })
  }
  const newQueryTab = () => {
    if (!workspace) return
    queryNumber.current += 1
    openTab({ id: `query-${queryNumber.current}`, workspace, kind: 'query', title: `Query ${queryNumber.current}` })
  }

  // A saved query opens in a query tab, or switches to the one already showing it.
  const openSavedQuery = (q: SavedQuery) => {
    if (!workspace) return
    const open = workspaceTabs.find((t) => t.kind === 'query' && t.savedQueryId === q.id)
    if (open) return setActiveTab(open.id)
    queryNumber.current += 1
    openTab({ id: `query-${queryNumber.current}`, workspace, kind: 'query', title: q.name, savedQueryId: q.id, initialSql: q.sql })
  }

  // Save from a query tab: name null updates the saved query the tab belongs
  // to; a name creates a new one. Either way the tab takes its name.
  const saveQueryFromTab = async (tabId: string, sql: string, name: string | null) => {
    const tab = tabs.find((t) => t.id === tabId)
    if (!tab || tab.kind !== 'query') return
    const linked = name === null ? savedQueries?.find((q) => q.id === tab.savedQueryId) : undefined
    const saved = await DBService.SaveQuery({
      id: linked?.id ?? '',
      name: name ?? linked?.name ?? tab.title,
      sql,
      database: '',
      host: '',
      updatedAt: 0,
    })
    setSavedQueries((current) => upsertQuery(current ?? [], saved))
    setTabs((current) => current.map((t) => (t.id === tabId ? { ...t, title: saved.name, savedQueryId: saved.id } : t)))
  }

  const renameSavedQuery = async (id: string, name: string) => {
    const renamed = await DBService.RenameQuery(id, name)
    setSavedQueries((current) => upsertQuery(current ?? [], renamed))
    setTabs((current) => current.map((t) => (t.kind === 'query' && t.savedQueryId === id ? { ...t, title: renamed.name } : t)))
  }

  // Its open tabs stay, no longer linked: Save in them asks for a name again.
  const deleteSavedQuery = async (id: string) => {
    await DBService.DeleteQuery(id)
    setSavedQueries((current) => (current ?? []).filter((q) => q.id !== id))
    setTabs((current) => current.map((t) => (t.kind === 'query' && t.savedQueryId === id ? { ...t, savedQueryId: undefined } : t)))
  }

  const setTabDirty = (id: string, dirty: boolean) =>
    setDirtyTabs((current) => {
      if (current.has(id) === dirty) return current
      const next = new Set(current)
      if (dirty) next.add(id)
      else next.delete(id)
      return next
    })

  // Closing the active tab moves to the tab on its left (or the right, if it was first).
  const closeTab = (id: string) => {
    const index = workspaceTabs.findIndex((t) => t.id === id)
    const remaining = workspaceTabs.filter((t) => t.id !== id)
    setTabs((current) => current.filter((t) => t.id !== id))
    setTabDirty(id, false)
    if (id === activeTab) setActiveTab(remaining[Math.max(0, index - 1)]?.id ?? '')
  }

  const active = workspaceTabs.find((t) => t.id === activeTab)
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
        {!connection && (
          <ConnectionsSidebar
            lists={lists}
            error={listsError}
            selectedId={selectedSaved}
            onNew={newConnection}
            onSelect={(c, saved) => pickConnection(c, saved, false)}
            onOpen={(c) => pickConnection(c, (lists?.saved ?? []).some((s) => s.id === c.id), true)}
            onDelete={deleteSaved}
            onClearRecent={clearRecent}
          />
        )}
        {connection && (
          <nav className="rail" aria-label="Sidebar">
            <button type="button" className={sidebarView === 'tables' ? 'rail-btn active' : 'rail-btn'}
                    onClick={() => setSidebarView('tables')} aria-pressed={sidebarView === 'tables'} title="Tables & views">
              <TableIcon />
            </button>
            <button type="button" className={sidebarView === 'queries' ? 'rail-btn active' : 'rail-btn'}
                    onClick={() => setSidebarView('queries')} aria-pressed={sidebarView === 'queries'} title="Saved queries">
              <CodeIcon />
            </button>
          </nav>
        )}
        {connection && (
          <SchemaSidebar
            content={sidebarView === 'queries' ? (
              <SavedQueriesPanel
                queries={savedQueries}
                error={queriesError}
                loading={queriesLoading}
                currentDatabase={connection.database}
                onRefresh={loadSavedQueries}
                onOpen={openSavedQuery}
                onRename={renameSavedQuery}
                onDelete={deleteSavedQuery}
              />
            ) : undefined}
            databasePicker={<DatabasePicker connection={connection} onSwitchDatabase={switchDatabase} onSwitchConnection={switchConnection} />}
            schemas={schemas}
            loading={schemasLoading}
            error={schemasError}
            onRefresh={loadSchemas}
            onImportFile={() => setImportOpen(true)}
            activeTable={activeTable}
            onOpenTable={openTable}
            onOpenPalette={() => setPaletteOpen(true)}
            connection={connection}
            onShowConnections={openConnections}
            onDisconnect={disconnect}
            disconnecting={disconnecting}
          />
        )}
        <SqlSchemaContext.Provider value={completion}>
        <main className={connection ? 'main-area' : 'content'}>
          {!connection && (
            <div className="engine-switch" role="tablist" aria-label="Database">
              {([['postgres', 'PostgreSQL'], ['dynamo', 'DynamoDB']] as const).map(([value, label]) => (
                <button key={value} type="button" role="tab" aria-selected={engine === value}
                        className={engine === value ? 'engine-option on' : 'engine-option'} onClick={() => setEngine(value)}>
                  {label}
                </button>
              ))}
            </div>
          )}
          {/* Stays mounted (and renders nothing) while connected or on DynamoDB, so the form keeps its values. */}
          <ConnectionForm
            connection={connection}
            onConnectionChange={changeConnection}
            request={formRequest}
            onSaved={(c) => { setSelectedSaved(c.id); loadLists() }}
            hidden={engine !== 'postgres'}
          />
          {!connection && engine === 'dynamo' && <DynamoSignIn />}
          {connection && workspaceTabs.length === 0 && (
            <div className="workspace-empty">
              <span className="workspace-empty-icon"><TableIcon /></span>
              <p className="workspace-empty-title">Open a table</p>
              <p className="workspace-empty-text">
                Pick one from the sidebar, or <button type="button" className="link-btn" onClick={() => setPaletteOpen(true)}>search tables</button> with <span className="keys"><kbd>Ctrl</kbd> <kbd>P</kbd></span>.
                Press <span className="keys"><kbd>Ctrl</kbd> <kbd>T</kbd></span> for a new query, or click <kbd>&lt;/&gt;</kbd> in a table to write SQL.
              </p>
            </div>
          )}
          {connection && workspaceTabs.length > 0 && (
            <TabBar tabs={workspaceTabs} activeId={activeTab} onSelect={setActiveTab} onClose={closeTab} dirtyIds={dirtyTabs} />
          )}
          {/* Every open tab stays mounted, other connections' and while disconnected too (hidden),
              so coming back keeps its page, filters, SQL, results and unsaved edits. */}
          {tabs.map((tab) => {
            const shown = tab.workspace === workspace && tab.id === activeTab
            return (
              <div key={tab.id} role="tabpanel" className={`tab-panel tab-panel-${tab.kind}`} hidden={!shown}>
                {tab.kind === 'table' && (
                  <TableTab table={tab.table} tabId={tab.id} preset={tab.preset} onSchemaChange={loadSchemas}
                            onDirtyChange={(dirty) => setTabDirty(tab.id, dirty)} onFollow={followKey} />
                )}
                {tab.kind === 'query' && (
                  <QueryTab tabId={tab.id} title={tab.title} initialSql={tab.initialSql ?? ''} linked={!!tab.savedQueryId}
                            database={connection?.database ?? ''} active={shown} onSchemaChange={loadSchemas}
                            onDirtyChange={(dirty) => setTabDirty(tab.id, dirty)}
                            onSaveQuery={(sql, name) => saveQueryFromTab(tab.id, sql, name)} />
                )}
                {tab.kind === 'connections' && (
                  <ServerConnectionsPanel config={tab.config} temporary={false} />
                )}
              </div>
            )
          })}
        </main>
        </SqlSchemaContext.Provider>
      </div>

      {connection && importOpen && (
        <ImportDialog
          mode="new"
          schema={schemas?.some((s) => s.name === 'public') ? 'public' : schemas?.[0]?.name ?? 'public'}
          schemas={(schemas ?? []).map((s) => s.name)}
          onClose={() => setImportOpen(false)}
          onImported={(r) => {
            loadSchemas()
            const quote = (name: string) => (/^[a-z_][a-z0-9_]*$/.test(name) ? name : `"${name.replace(/"/g, '""')}"`)
            openTable({ schema: r.schema, name: r.table, kind: 'table', qualifiedName: `${quote(r.schema)}.${quote(r.table)}`, estimatedRows: r.rows })
          }}
        />
      )}
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
