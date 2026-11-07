import { useState } from 'react'
import {DBService} from "../bindings/github.com/anujkaushik1/SpyDB";

type Status =
  | { kind: 'idle' }
  | { kind: 'testing' }
  | { kind: 'ok'; message: string }
  | { kind: 'error'; message: string };

function App() {
  const [status, setStatus] = useState<Status>({ kind: 'idle' });

  const testConnection = () => {
    setStatus({ kind: 'testing' });
    DBService.TestConnection()
      .then((message) => setStatus({ kind: 'ok', message }))
      .catch((err) => setStatus({ kind: 'error', message: err instanceof Error ? err.message : String(err) }));
  };

  return (
    <main className="container">
      <button className="btn" type="button" onClick={testConnection} disabled={status.kind === 'testing'}>
        {status.kind === 'testing' ? 'Testing…' : 'Test connection'}
      </button>
      {(status.kind === 'ok' || status.kind === 'error') && (
        <p className={`status status-${status.kind}`} role="status">{status.message}</p>
      )}
    </main>
  )
}

export default App
