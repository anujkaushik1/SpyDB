import { useEffect, useRef, useState, type ChangeEvent, type FormEvent, type ReactNode } from 'react'
import { DBService, type ConnectionInfo, type DBConfig, type SavedConnection } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import Banner, { errorDetail, errorMessage, type Notice } from './Banner'
import ServerConnectionsPanel from './ServerConnectionsPanel'
import { parseConnectionUrl } from './connectionUrl'
import { DatabaseIcon, EyeIcon, EyeOffIcon, Spinner } from './icons'
import { isMac } from './platform'

type FieldName = 'host' | 'port' | 'user' | 'password' | 'database'

// Port is kept as text while editing and converted when testing.
type FormValues = Record<FieldName, string>

type Busy = 'testing' | 'connecting' | null

const initialValues: FormValues = { host: 'localhost', port: '5432', user: '', password: '', database: '' }

function validate(v: FormValues): Partial<Record<FieldName, string>> {
  const errors: Partial<Record<FieldName, string>> = {}
  if (!v.host.trim()) errors.host = 'Host is required'
  const port = Number(v.port)
  if (!v.port || port < 1 || port > 65535) errors.port = 'Use 1–65535'
  return errors
}

const fromConfig = (c: DBConfig): FormValues => ({
  host: c.host, port: String(c.port), user: c.user, password: c.password, database: c.database,
})

const toConfig = (v: FormValues): DBConfig => ({
  host: v.host.trim(), port: Number(v.port), user: v.user.trim(), password: v.password, database: v.database.trim(),
})

const describe = (c: ConnectionInfo) => `${c.database} on ${c.host}:${c.port} as ${c.user}`

// A connection picked in the sidebar: load it into the form, and connect if asked
// (double-click). `seq` changes on every pick, so picking the same one again works.
export type FormRequest = { config: DBConfig; savedId: string | null; name: string; connect: boolean; seq: number }

type Props = {
  connection: ConnectionInfo | null
  onConnectionChange: (connection: ConnectionInfo | null) => void
  request: FormRequest | null
  onSaved: (connection: SavedConnection) => void // saved or updated; the sidebar reloads
}

export default function ConnectionForm({ connection, onConnectionChange, request, onSaved }: Props) {
  const [values, setValues] = useState<FormValues>(initialValues)
  const [touched, setTouched] = useState<Partial<Record<FieldName, boolean>>>({})
  const [submitted, setSubmitted] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [busy, setBusy] = useState<Busy>(null)
  const [notice, setNotice] = useState<Notice | null>(null)
  // Settings the connections panel lists with; null means the panel is closed.
  const [panelConfig, setPanelConfig] = useState<DBConfig | null>(null)
  // The saved connection being edited (null for a new one), its name, and the Save outcome.
  const [savedId, setSavedId] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [saveState, setSaveState] = useState<{ kind: 'saving' } | { kind: 'saved' } | { kind: 'error'; message: string } | null>(null)
  // Import from URL: the pasted text while the box is open (null when closed).
  const [urlText, setUrlText] = useState<string | null>(null)
  const [urlError, setUrlError] = useState<string | null>(null)
  const inputs = useRef<Partial<Record<FieldName, HTMLInputElement | null>>>({})

  // On load, pick up a connection that is still open on the Go side (a page
  // reload doesn't close it). Otherwise pre-fill from db.config.json and focus
  // the first empty field.
  useEffect(() => {
    const load = async () => {
      const active = await DBService.ActiveConnection()
      if (active) {
        setValues({ ...initialValues, host: active.host, port: String(active.port), user: active.user, database: active.database })
        onConnectionChange(active)
        return
      }
      const next = fromConfig(await DBService.DefaultConfig())
      setValues(next)
      const firstEmpty = (['host', 'port', 'user', 'database'] as const).find((f) => !next[f])
      if (firstEmpty) requestAnimationFrame(() => inputs.current[firstEmpty]?.focus())
    }
    load().catch((err) => setNotice({ tone: 'error', title: 'Couldn’t load settings', message: errorMessage(err) }))
  }, [])

  const errors = validate(values)
  const visibleError = (f: FieldName) => (touched[f] || submitted ? errors[f] : undefined)

  const update = (field: FieldName) => (e: ChangeEvent<HTMLInputElement>) => {
    const value = field === 'port' ? e.target.value.replace(/\D/g, '') : e.target.value
    setValues((v) => ({ ...v, [field]: value }))
    // The last result and the connections list no longer match the form.
    setNotice(null)
    setPanelConfig(null)
  }

  // Shows field errors and focuses the first invalid field; returns null if invalid.
  const validConfig = (): DBConfig | null => {
    setSubmitted(true)
    const firstInvalid = (Object.keys(errors) as FieldName[])[0]
    if (firstInvalid) {
      inputs.current[firstInvalid]?.focus()
      return null
    }
    return toConfig(values)
  }

  const test = async () => {
    const cfg = validConfig()
    if (!cfg) return
    setBusy('testing')
    setNotice(null)
    const started = performance.now()
    try {
      const message = await DBService.TestConnection(cfg)
      setNotice({ tone: 'ok', title: 'Connection successful', message: `${message} · ${Math.round(performance.now() - started)} ms` })
    } catch (err) {
      setNotice({ tone: 'error', title: 'Connection failed', message: errorMessage(err), detail: errorDetail(err) })
    } finally {
      setBusy(null)
    }
  }

  // The form's submit (Connect button or Enter).
  // The form's submit (Connect button or Enter).
  const connect = (e: FormEvent) => {
    e.preventDefault()
    if (connection || busy) return
    const cfg = validConfig()
    if (cfg) connectWith(cfg)
  }

  const connectWith = async (cfg: DBConfig) => {
    setBusy('connecting')
    setNotice(null)
    try {
      onConnectionChange(await DBService.Connect(cfg))
      setPanelConfig(null)
    } catch (err) {
      setNotice({ tone: 'error', title: 'Couldn’t connect', message: errorMessage(err), detail: errorDetail(err) })
    } finally {
      setBusy(null)
    }
  }

  // Disconnecting happens in the sidebar; when it does, say so here. While
  // connected, keep the form in step with the connection (e.g. after switching
  // database), so it shows where you were when you come back.
  const previous = useRef<ConnectionInfo | null>(null)
  useEffect(() => {
    if (previous.current && !connection) {
      setNotice({ tone: 'info', title: 'Disconnected', message: `Closed the connection to ${describe(previous.current)}.` })
    }
    if (connection) {
      setValues((v) => ({ ...v, host: connection.host, port: String(connection.port), user: connection.user, database: connection.database }))
    }
    previous.current = connection
  }, [connection])

  // A connection picked in the sidebar.
  useEffect(() => {
    if (!request) return
    const next = fromConfig(request.config)
    setValues(next)
    setSavedId(request.savedId)
    setName(request.name)
    setSaveState(null)
    setNotice(null)
    setSubmitted(false)
    setTouched({})
    setPanelConfig(null)
    if (request.connect && !connection && !busy) connectWith(request.config)
  }, [request?.seq])

  // Save (or update) the form as a named connection; an empty name uses the suggestion.
  const save = async () => {
    const cfg = validConfig()
    if (!cfg) return
    const finalName = name.trim() || defaultName
    setSaveState({ kind: 'saving' })
    try {
      const stored = await DBService.SaveConnection({ id: savedId ?? '', name: finalName, config: cfg, lastUsed: 0 })
      setSavedId(stored.id)
      setName(stored.name)
      setSaveState({ kind: 'saved' })
      onSaved(stored)
    } catch (err) {
      setSaveState({ kind: 'error', message: errorMessage(err) })
    }
  }
  const defaultName = `${values.database || values.user || 'postgres'} @ ${values.host || 'localhost'}`

  // Fill the form from a postgres:// URL. Nothing connects or saves until you
  // click Connect or Save; the URL box is cleared so the password isn't left on screen.
  const importUrl = (text: string) => {
    try {
      const { config, ignored } = parseConnectionUrl(text)
      setValues(fromConfig(config))
      setSubmitted(false)
      setTouched({})
      setUrlText(null)
      setUrlError(null)
      setNotice({
        tone: 'info',
        title: 'Imported from URL',
        message: ignored.length
          ? `Filled in the form. Skipped settings that aren’t Postgres connection settings: ${ignored.join(', ')}.`
          : 'Filled in the form. Check it, then Connect or Save.',
      })
    } catch (err) {
      setUrlError(errorMessage(err))
    }
  }

  // Opens the panel with the form's settings (validated first), or closes it.
  const toggleConnections = () => {
    if (panelConfig) {
      setPanelConfig(null)
      return
    }
    const cfg = validConfig()
    if (cfg) setPanelConfig(cfg)
  }

  const inputProps = (field: FieldName) => {
    const error = visibleError(field)
    return {
      id: field,
      name: field,
      className: 'input',
      value: values[field],
      onChange: update(field),
      onBlur: () => setTouched((t) => ({ ...t, [field]: true })),
      ref: (el: HTMLInputElement | null) => { inputs.current[field] = el },
      'aria-invalid': error ? true : undefined,
      'aria-describedby': error ? `${field}-error` : undefined,
      // Hostnames and identifiers must not be autocorrected or capitalised.
      autoComplete: 'off',
      autoCapitalize: 'off',
      autoCorrect: 'off',
      spellCheck: false,
    }
  }

  const connectionsButton = (
    <button type="button" className="btn btn-secondary" onClick={toggleConnections} disabled={busy !== null}
            aria-expanded={panelConfig !== null} aria-controls="server-connections">
      {panelConfig ? 'Hide connections' : 'Active connections'}
    </button>
  )

  const panel = panelConfig && <ServerConnectionsPanel config={panelConfig} temporary />

  // While connected the workspace (sidebar + tabs) takes over. Rendering nothing,
  // rather than unmounting, keeps the form's values for when you disconnect.
  if (connection) return null

  return (
    <>
      <form className="card" onSubmit={connect} noValidate>
        <header className="card-header">
          <span className="db-badge"><DatabaseIcon /></span>
          <div className="card-heading">
            <h1 className="card-title">{savedId ? name || 'Saved connection' : 'New connection'}</h1>
            <p className="card-subtitle">PostgreSQL</p>
          </div>
          <button type="button" className="link-btn import-url-btn"
                  onClick={() => { setUrlText((t) => (t === null ? '' : null)); setUrlError(null) }}
                  aria-expanded={urlText !== null}>
            {urlText === null ? 'Import from URL' : 'Cancel'}
          </button>
        </header>

        {urlText !== null && (
          <div className="import-url">
            <div className="save-connection-row">
              <input
                className="input import-url-input"
                aria-label="Connection URL"
                value={urlText}
                placeholder="postgresql://user:password@host:5432/database"
                onChange={(e) => { setUrlText(e.target.value); setUrlError(null) }}
                onPaste={(e) => {
                  // Pasting a whole URL imports it straight away.
                  const pasted = e.clipboardData.getData('text')
                  if (/^\s*postgres(ql)?:\/\//i.test(pasted)) {
                    e.preventDefault()
                    importUrl(pasted)
                  }
                }}
                onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); importUrl(urlText) } }}
                autoFocus
                spellCheck={false}
                autoCapitalize="off"
                autoCorrect="off"
                autoComplete="off"
              />
              <button type="button" className="btn btn-secondary" onClick={() => importUrl(urlText)} disabled={!urlText.trim()}>
                Import
              </button>
            </div>
            {urlError && <p className="save-connection-note error">{urlError}</p>}
          </div>
        )}

        {/* Locked while a request runs. */}
        <fieldset className="fields" disabled={busy !== null}>
          <Field id="host" label="Host" error={visibleError('host')} className="col-host">
            <input {...inputProps('host')} placeholder="localhost" />
          </Field>
          <Field id="port" label="Port" error={visibleError('port')} className="col-port">
            <input {...inputProps('port')} inputMode="numeric" maxLength={5} placeholder="5432" />
          </Field>
          <Field id="user" label="User">
            <input {...inputProps('user')} placeholder="Default: OS user" />
          </Field>
          <Field id="password" label="Password">
            <div className="input-group">
              <input {...inputProps('password')} type={showPassword ? 'text' : 'password'} />
              <button type="button" className="icon-btn" onClick={() => setShowPassword((s) => !s)}
                      aria-label={showPassword ? 'Hide password' : 'Show password'} aria-pressed={showPassword}>
                {showPassword ? <EyeOffIcon /> : <EyeIcon />}
              </button>
            </div>
          </Field>
          <Field id="database" label="Database" className="col-full">
            <input {...inputProps('database')} placeholder="Default: same as user" />
          </Field>
        </fieldset>

        <div className="result-slot" aria-live="polite">
          {notice && <Banner {...notice} />}
        </div>

        <footer className="card-footer">
          <span className="hint"><kbd>↵</kbd> to connect</span>
          <div className="actions">
            {connectionsButton}
            <button type="button" className="btn btn-secondary" onClick={test} disabled={busy !== null}>
              {busy === 'testing' ? <><Spinner /> Testing…</> : 'Test'}
            </button>
            <button type="submit" className="btn btn-primary" disabled={busy !== null}>
              {busy === 'connecting' ? <><Spinner /> Connecting…</> : 'Connect'}
            </button>
          </div>
        </footer>

        <div className="save-connection">
          <label className="label" htmlFor="connection-name">{savedId ? 'Saved connection' : 'Save connection'}</label>
          <div className="save-connection-row">
            <input
              id="connection-name"
              className="input"
              value={name}
              placeholder={defaultName}
              onChange={(e) => { setName(e.target.value); setSaveState(null) }}
              onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); save() } }}
              spellCheck={false}
              autoComplete="off"
            />
            <button type="button" className="btn btn-secondary" onClick={save}
                    disabled={saveState?.kind === 'saving'}>
              {saveState?.kind === 'saving' ? <><Spinner /> Saving…</> : savedId ? 'Save changes' : 'Save'}
            </button>
          </div>
          <p className={saveState?.kind === 'error' ? 'save-connection-note error' : 'save-connection-note'}>
            {saveState?.kind === 'saved' ? '✓ Saved. It’s in the sidebar under Saved.'
              : saveState?.kind === 'error' ? saveState.message
              : `Saved connections, including the password, are kept on this ${isMac ? 'Mac' : 'computer'}, readable only by your user.`}
          </p>
        </div>
      </form>

      {panel}
    </>
  )
}

function Field({ id, label, error, className, children }: {
  id: FieldName
  label: string
  error?: string
  className?: string
  children: ReactNode
}) {
  return (
    <div className={className ? `field ${className}` : 'field'}>
      <label className="label" htmlFor={id}>{label}</label>
      {children}
      {error && <p className="field-error" id={`${id}-error`}>{error}</p>}
    </div>
  )
}
