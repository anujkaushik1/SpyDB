import { useEffect, useRef, useState, type ChangeEvent, type FormEvent, type ReactNode } from 'react'
import { DBService, type ConnectionInfo, type DBConfig } from '../bindings/github.com/anujkaushik1/SpyDB'
import Banner, { errorDetail, errorMessage, type Notice } from './Banner'
import ServerConnectionsPanel from './ServerConnectionsPanel'
import { DatabaseIcon, EyeIcon, EyeOffIcon, Spinner } from './icons'

type FieldName = 'host' | 'port' | 'user' | 'password' | 'database'

// Port is kept as text while editing and converted when testing.
type FormValues = Record<FieldName, string>

type Busy = 'testing' | 'connecting' | 'disconnecting' | null

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

type Props = {
  connection: ConnectionInfo | null
  onConnectionChange: (connection: ConnectionInfo | null) => void
}

export default function ConnectionForm({ connection, onConnectionChange }: Props) {
  const [values, setValues] = useState<FormValues>(initialValues)
  const [touched, setTouched] = useState<Partial<Record<FieldName, boolean>>>({})
  const [submitted, setSubmitted] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [busy, setBusy] = useState<Busy>(null)
  const [notice, setNotice] = useState<Notice | null>(null)
  // Settings the connections panel lists with; null means the panel is closed.
  const [panelConfig, setPanelConfig] = useState<DBConfig | null>(null)
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
  const connect = async (e: FormEvent) => {
    e.preventDefault()
    if (connection || busy) return
    const cfg = validConfig()
    if (!cfg) return
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

  const disconnect = async () => {
    if (!connection) return
    setBusy('disconnecting')
    try {
      await DBService.Disconnect()
      onConnectionChange(null)
      setPanelConfig(null)
      setNotice({ tone: 'info', title: 'Disconnected', message: `Closed the connection to ${describe(connection)}.` })
    } catch (err) {
      setNotice({ tone: 'error', title: 'Couldn’t disconnect', message: errorMessage(err) })
    } finally {
      setBusy(null)
    }
  }

  // Opens the panel with the form's settings (validated when not connected), or closes it.
  const toggleConnections = () => {
    if (panelConfig) {
      setPanelConfig(null)
      return
    }
    const cfg = connection ? toConfig(values) : validConfig()
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

  const banner: Notice | null = connection
    ? {
        tone: 'ok',
        title: 'Connected',
        message: connection.serverVersion
          ? `${describe(connection)} · PostgreSQL ${connection.serverVersion}`
          : describe(connection),
      }
    : notice

  const connectionsButton = (
    <button type="button" className="btn btn-secondary" onClick={toggleConnections} disabled={busy !== null}
            aria-expanded={panelConfig !== null} aria-controls="server-connections">
      {panelConfig ? 'Hide connections' : 'Active connections'}
    </button>
  )

  return (
    <>
      <form className="card" onSubmit={connect} noValidate>
        <header className="card-header">
          <span className="db-badge"><DatabaseIcon /></span>
          <div>
            <h1 className="card-title">New connection</h1>
            <p className="card-subtitle">PostgreSQL</p>
          </div>
        </header>

        {/* Locked while a request runs, and while connected (edits wouldn't
            change the open connection). */}
        <fieldset className="fields" disabled={busy !== null || connection !== null}>
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
          {banner && <Banner {...banner} />}
        </div>

        <footer className="card-footer">
          <span className="hint">
            {connection ? 'Disconnect to edit' : <><kbd>↵</kbd> to connect</>}
          </span>
          <div className="actions">
            {connectionsButton}
            {connection ? (
              <button type="button" className="btn btn-danger" onClick={disconnect} disabled={busy !== null}>
                {busy === 'disconnecting' ? <><Spinner /> Disconnecting…</> : 'Disconnect'}
              </button>
            ) : (
              <>
                <button type="button" className="btn btn-secondary" onClick={test} disabled={busy !== null}>
                  {busy === 'testing' ? <><Spinner /> Testing…</> : 'Test'}
                </button>
                <button type="submit" className="btn btn-primary" disabled={busy !== null}>
                  {busy === 'connecting' ? <><Spinner /> Connecting…</> : 'Connect'}
                </button>
              </>
            )}
          </div>
        </footer>
      </form>

      {panelConfig && <ServerConnectionsPanel config={panelConfig} temporary={connection === null} />}
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
