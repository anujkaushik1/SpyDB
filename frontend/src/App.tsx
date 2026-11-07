import { useState } from 'react'
import type { ConnectionInfo, TableInfo } from '../bindings/github.com/anujkaushik1/SpyDB'
import ConnectionForm from './ConnectionForm'
import QueryEditor, { type QueryRequest } from './QueryEditor'
import SchemaSidebar from './SchemaSidebar'

function App() {
  // The open connection, or null when disconnected.
  const [connection, setConnection] = useState<ConnectionInfo | null>(null)
  // Sidebar → editor: the table last opened and the query to run for it.
  const [activeTable, setActiveTable] = useState<string | null>(null)
  const [queryRequest, setQueryRequest] = useState<QueryRequest | null>(null)
  // Bumped when the editor creates, alters or drops something, so the sidebar reloads.
  const [schemaVersion, setSchemaVersion] = useState(0)

  const changeConnection = (next: ConnectionInfo | null) => {
    setConnection(next)
    if (!next) {
      setActiveTable(null)
      setQueryRequest(null)
    }
  }

  const openTable = (table: TableInfo) => {
    setActiveTable(table.qualifiedName)
    setQueryRequest((r) => ({ sql: `SELECT * FROM ${table.qualifiedName};`, seq: (r?.seq ?? 0) + 1 }))
  }

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
      <div className={connection ? 'workspace has-sidebar' : 'workspace'}>
        {connection && (
          <SchemaSidebar activeTable={activeTable} refreshToken={schemaVersion} onOpenTable={openTable} />
        )}
        <main className="content">
          {/* Also renders the active connections panel below the form. */}
          <ConnectionForm connection={connection} onConnectionChange={changeConnection} />
          {connection && <QueryEditor request={queryRequest} onSchemaChange={() => setSchemaVersion((v) => v + 1)} />}
        </main>
      </div>
    </div>
  )
}

export default App
