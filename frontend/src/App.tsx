import { useState } from 'react'
import type { ConnectionInfo } from '../bindings/github.com/anujkaushik1/SpyDB'
import ConnectionForm from './ConnectionForm'
import QueryEditor from './QueryEditor'

function App() {
  // The open connection, or null when disconnected.
  const [connection, setConnection] = useState<ConnectionInfo | null>(null)

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
      <main className="content">
        {/* Also renders the active connections panel below the form. */}
        <ConnectionForm connection={connection} onConnectionChange={setConnection} />
        {connection && <QueryEditor />}
      </main>
    </div>
  )
}

export default App
