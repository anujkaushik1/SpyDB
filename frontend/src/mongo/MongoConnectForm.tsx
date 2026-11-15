import { useEffect, useRef, useState, type ChangeEvent, type FormEvent, type ReactNode } from 'react'
import { Service as Mongo, type Config, type ConnectionInfo, type SavedConnection } from '../../bindings/github.com/anujkaushik1/SpyDB/mongodb'
import Banner, { errorDetail, errorMessage, type Notice } from '../Banner'
import { DatabaseIcon, EyeIcon, EyeOffIcon, Spinner } from '../icons'
import { isMac } from '../platform'
import { mongoTarget } from './mongoConnection'

type FieldName = 'uri' | 'host' | 'port' | 'user' | 'password' | 'database' | 'authSource'

// Port is kept as text while editing and converted when testing.
type FormValues = Record<FieldName, string> & { tls: boolean }

type Busy = 'testing' | 'connecting' | null

const initialValues: FormValues = { uri: '', host: 'localhost', port: '27017', user: '', password: '', database: '', authSource: '', tls: false }

const fromConfig = (c: Config): FormValues => ({
  uri: c.uri, host: c.host, port: String(c.port || 27017), user: c.user, password: c.password, database: c.database, authSource: c.authSource, tls: c.tls,
})

const toConfig = (v: FormValues, useUri: boolean): Config => useUri
  ? { ...fromValues(initialValues), uri: v.uri.trim(), database: v.database.trim() }
  : { ...fromValues(v), uri: '' }

const fromValues = (v: FormValues): Config => ({
  uri: v.uri.trim(), host: v.host.trim(), port: Number(v.port), user: v.user.trim(), password: v.password,
  database: v.database.trim(), authSource: v.authSource.trim(), tls: v.tls,
})

// A connection picked in the sidebar: load it into the form, and connect if
// asked (double-click). `seq` changes on every pick.
export type MongoFormRequest = { config: Config; savedId: string | null; name: string; connect: boolean; seq: number }

type Props = {
  connection: ConnectionInfo | null
  onConnectionChange: (connection: ConnectionInfo | null) => void
  request: MongoFormRequest | null
  onSaved: (connection: SavedConnection) => void // saved or updated; the sidebar reloads
  hidden?: boolean // another engine is shown: render nothing, keep the values
}

// MongoDB's connect screen: a connection string (mongodb:// or
// mongodb+srv://, as Atlas gives it) or the fields, then Test and Connect,
// and Save to keep it in the sidebar, like Postgres's form.
export default function MongoConnectForm({ connection, onConnectionChange, request, onSaved, hidden }: Props) {
  const [values, setValues] = useState<FormValues>(initialValues)
  const [useUri, setUseUri] = useState(false)
  const [submitted, setSubmitted] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [busy, setBusy] = useState<Busy>(null)
  const [notice, setNotice] = useState<Notice | null>(null)
  const [savedId, setSavedId] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [saveState, setSaveState] = useState<{ kind: 'saving' } | { kind: 'saved' } | { kind: 'error'; message: string } | null>(null)
  const inputs = useRef<Partial<Record<FieldName, HTMLInputElement | null>>>({})

  // A window reload doesn't close Go's connection: pick it up.
  useEffect(() => {
    Mongo.ActiveConnection().then((active) => { if (active) onConnectionChange(active) }).catch(() => {})
  }, [])

  const errors: Partial<Record<FieldName, string>> = {}
  if (useUri) {
    if (!/^mongodb(\+srv)?:\/\//.test(values.uri.trim())) errors.uri = 'Starts with mongodb:// or mongodb+srv://'
  } else {
    if (!values.host.trim()) errors.host = 'Host is required'
    const port = Number(values.port)
    if (!values.port || port < 1 || port > 65535) errors.port = 'Use 1–65535'
  }
  if (/[/\\. "$]/.test(values.database)) errors.database = 'No spaces, dots, slashes, quotes or $'
  const visibleError = (f: FieldName) => (submitted ? errors[f] : undefined)

  const update = (field: FieldName) => (e: ChangeEvent<HTMLInputElement>) => {
    const value = field === 'port' ? e.target.value.replace(/\D/g, '') : e.target.value
    setValues((v) => ({ ...v, [field]: value }))
    setNotice(null)
  }

  const validConfig = (): Config | null => {
    setSubmitted(true)
    const firstInvalid = (Object.keys(errors) as FieldName[])[0]
    if (firstInvalid) {
      inputs.current[firstInvalid]?.focus()
      return null
    }
    return toConfig(values, useUri)
  }

  const test = async () => {
    const cfg = validConfig()
    if (!cfg) return
    setBusy('testing')
    setNotice(null)
    const started = performance.now()
    try {
      const message = await Mongo.TestConnection(cfg)
      setNotice({ tone: 'ok', title: 'Connection successful', message: `${message} · ${Math.round(performance.now() - started)} ms` })
    } catch (err) {
      setNotice({ tone: 'error', title: 'Connection failed', message: errorMessage(err), detail: errorDetail(err) })
    } finally {
      setBusy(null)
    }
  }

  const connect = (e: FormEvent) => {
    e.preventDefault()
    if (connection || busy) return
    const cfg = validConfig()
    if (cfg) connectWith(cfg)
  }

  const connectWith = async (cfg: Config) => {
    setBusy('connecting')
    setNotice(null)
    try {
      onConnectionChange(await Mongo.Connect(cfg))
    } catch (err) {
      setNotice({ tone: 'error', title: 'Couldn’t connect', message: errorMessage(err), detail: errorDetail(err) })
    } finally {
      setBusy(null)
    }
  }

  // Disconnecting happens in the sidebar; say so here when it does.
  const previous = useRef<ConnectionInfo | null>(null)
  useEffect(() => {
    if (previous.current && !connection) {
      setNotice({ tone: 'info', title: 'Disconnected', message: `Closed the connection to ${previous.current.host}.` })
    }
    previous.current = connection
  }, [connection])

  // A connection picked in the sidebar.
  useEffect(() => {
    if (!request) return
    setValues(fromConfig(request.config))
    setUseUri(request.config.uri.trim() !== '')
    setSavedId(request.savedId)
    setName(request.name)
    setSaveState(null)
    setNotice(null)
    setSubmitted(false)
    if (request.connect && !connection && !busy) connectWith(request.config)
  }, [request?.seq])

  const target = mongoTarget(toConfig(values, useUri))
  const defaultName = `${target.database || target.user || 'mongodb'} @ ${target.hosts.split(',')[0] || 'localhost'}`

  const save = async () => {
    const cfg = validConfig()
    if (!cfg) return
    setSaveState({ kind: 'saving' })
    try {
      const stored = await Mongo.SaveConnection({ id: savedId ?? '', name: name.trim() || defaultName, config: cfg, lastUsed: 0 })
      setSavedId(stored.id)
      setName(stored.name)
      setSaveState({ kind: 'saved' })
      onSaved(stored)
    } catch (err) {
      setSaveState({ kind: 'error', message: errorMessage(err) })
    }
  }

  const inputProps = (field: FieldName) => {
    const error = visibleError(field)
    return {
      id: `mongo-${field}`,
      name: field,
      className: 'input',
      value: values[field],
      onChange: update(field),
      ref: (el: HTMLInputElement | null) => { inputs.current[field] = el },
      'aria-invalid': error ? true : undefined,
      autoComplete: 'off',
      autoCapitalize: 'off',
      autoCorrect: 'off',
      spellCheck: false,
    }
  }

  const passwordToggle = (
    <button type="button" className="icon-btn" onClick={() => setShowPassword((s) => !s)}
            aria-label={showPassword ? 'Hide password' : 'Show password'} aria-pressed={showPassword}>
      {showPassword ? <EyeOffIcon /> : <EyeIcon />}
    </button>
  )

  // While connected the workspace takes over; another engine shows its own
  // screen. Rendering nothing, rather than unmounting, keeps the values.
  if (connection || hidden) return null

  return (
    <form className="card" onSubmit={connect} noValidate aria-label="MongoDB connection">
      <header className="card-header">
        <span className="db-badge db-badge-mongo"><DatabaseIcon /></span>
        <div className="card-heading">
          <h1 className="card-title">{savedId ? name || 'Saved connection' : 'New connection'}</h1>
          <p className="card-subtitle">MongoDB</p>
        </div>
        <button type="button" className="link-btn import-url-btn" onClick={() => { setUseUri((u) => !u); setSubmitted(false); setNotice(null) }}>
          {useUri ? 'Use host and port' : 'Use a connection string'}
        </button>
      </header>

      <fieldset className="fields" disabled={busy !== null}>
        {useUri ? (
          <Field id="uri" label="Connection string" error={visibleError('uri')} className="col-full">
            <div className="input-group">
              <input {...inputProps('uri')} type={showPassword ? 'text' : 'password'}
                     placeholder="mongodb+srv://user:password@cluster0.example.mongodb.net/" />
              {passwordToggle}
            </div>
          </Field>
        ) : (
          <>
            <Field id="host" label="Host" error={visibleError('host')} className="col-host">
              <input {...inputProps('host')} placeholder="localhost" />
            </Field>
            <Field id="port" label="Port" error={visibleError('port')} className="col-port">
              <input {...inputProps('port')} inputMode="numeric" maxLength={5} placeholder="27017" />
            </Field>
            <Field id="user" label="User">
              <input {...inputProps('user')} placeholder="None: no sign-in" />
            </Field>
            <Field id="password" label="Password">
              <div className="input-group">
                <input {...inputProps('password')} type={showPassword ? 'text' : 'password'} />
                {passwordToggle}
              </div>
            </Field>
            <Field id="authSource" label="Auth database">
              <input {...inputProps('authSource')} placeholder="admin" />
            </Field>
          </>
        )}
        <Field id="database" label="Database" error={visibleError('database')} className={useUri ? 'col-full' : undefined}>
          <input {...inputProps('database')} placeholder={useUri ? 'Default: the string’s, else the first one' : 'Default: the first one'} />
        </Field>
        {!useUri && (
          <label className="mongo-tls col-full">
            <input type="checkbox" checked={values.tls} onChange={(e) => { setValues((v) => ({ ...v, tls: e.target.checked })); setNotice(null) }} />
            Use TLS (hosted servers like Atlas need it)
          </label>
        )}
      </fieldset>

      <div className="result-slot" aria-live="polite">
        {notice && <Banner {...notice} />}
      </div>

      <footer className="card-footer">
        <span className="hint"><kbd>↵</kbd> to connect</span>
        <div className="actions">
          <button type="button" className="btn btn-secondary" onClick={test} disabled={busy !== null}>
            {busy === 'testing' ? <><Spinner /> Testing…</> : 'Test'}
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy !== null}>
            {busy === 'connecting' ? <><Spinner /> Connecting…</> : 'Connect'}
          </button>
        </div>
      </footer>

      <div className="save-connection">
        <label className="label" htmlFor="mongo-connection-name">{savedId ? 'Saved connection' : 'Save connection'}</label>
        <div className="save-connection-row">
          <input
            id="mongo-connection-name"
            className="input"
            value={name}
            placeholder={defaultName}
            onChange={(e) => { setName(e.target.value); setSaveState(null) }}
            onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); save() } }}
            spellCheck={false}
            autoComplete="off"
          />
          <button type="button" className="btn btn-secondary" onClick={save} disabled={saveState?.kind === 'saving'}>
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
  )
}

function Field({ id, label, error, className, children }: { id: FieldName; label: string; error?: string; className?: string; children: ReactNode }) {
  return (
    <div className={className ? `field ${className}` : 'field'}>
      <label className="label" htmlFor={`mongo-${id}`}>{label}</label>
      {children}
      {error && <p className="field-error">{error}</p>}
    </div>
  )
}
