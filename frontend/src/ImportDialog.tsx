import { useEffect, useMemo, useRef, useState } from 'react'
import { DBService, type ImportPreview, type ImportResult, type ImportTargetColumn } from '../bindings/github.com/anujkaushik1/SpyDB/postgres'
import { errorMessage } from './Banner'
import { Spinner, UploadIcon, XIcon } from './icons'
import { IMPORT_TYPES, missingRequired, nameProblem, suggestTargets } from './importMapping'

type Props = {
  mode: 'existing' | 'new'
  schema: string // existing: the table's schema; new: where to create the table
  table?: string // existing: the table to import into
  schemas: string[] // new: the schemas to choose from
  onClose: () => void
  onImported: (result: ImportResult) => void
}

// What happens to one file column: into which column (a table column, or a
// new table's column name) and, for a new table, with which type.
type Choice = { include: boolean; target: string; type: string }

const SEPARATORS: [string, string][] = [[',', 'Comma'], [';', 'Semicolon'], ['\t', 'Tab'], ['|', 'Pipe']]

// Imports a CSV or Excel file into an existing table, or as a new table.
// Go reads the file (DBService.PreviewImport) and suggests names and types;
// matching columns happens here; the import itself is one transaction
// (DBService.RunImport): every row goes in, or none does.
export default function ImportDialog({ mode, schema: initialSchema, table, schemas, onClose, onImported }: Props) {
  const isNew = mode === 'new'
  const [path, setPath] = useState('')
  const [emptyAsNull, setEmptyAsNull] = useState(true)
  const [preview, setPreview] = useState<ImportPreview | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [targets, setTargets] = useState<ImportTargetColumn[] | null>(null)
  const [choices, setChoices] = useState<Choice[]>([])
  const [schema, setSchema] = useState(initialSchema)
  const [tableName, setTableName] = useState('')
  const [keyMode, setKeyMode] = useState<'add' | 'column' | 'none'>('add')
  const [keyColumn, setKeyColumn] = useState(-1)
  const [importing, setImporting] = useState(false)
  const [result, setResult] = useState<ImportResult | null>(null)
  const pending = useRef<{ cancel: () => void } | null>(null)
  const cancelled = useRef(false)

  // An existing table's columns, to match the file's against.
  useEffect(() => {
    if (isNew || !table) return
    DBService.ImportTargetColumns(initialSchema, table)
      .then((columns) => setTargets(columns ?? []))
      .catch((err) => setError(errorMessage(err)))
  }, [isNew, initialSchema, table])

  // Reads the file again with these options (sheet, separator). Go works out
  // whether the first row is column names.
  const load = async (file: string, opts: { sheet?: string; delimiter?: string; fresh?: boolean } = {}) => {
    if (!file.trim()) return
    setLoading(true)
    setError(null)
    setResult(null)
    try {
      const p = await DBService.PreviewImport({
        path: file.trim(), sheet: opts.sheet ?? '', delimiter: opts.delimiter ?? '', hasHeader: false,
      })
      if (opts.fresh) setTableName(p.tableName)
      setPreview(p)
    } catch (err) {
      setPreview(null)
      setError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }

  const choose = async () => {
    try {
      const file = await DBService.ChooseImportFile()
      if (!file) return
      setPath(file)
      await load(file, { fresh: true })
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  // Fresh suggestions whenever the file (or the table's columns) arrive.
  useEffect(() => {
    if (!preview) return
    const columns = preview.columns ?? []
    if (isNew) {
      setChoices(columns.map((c) => ({ include: true, target: c.name, type: c.type })))
      setKeyMode(preview.keyColumn >= 0 ? 'column' : 'add')
      setKeyColumn(preview.keyColumn)
    } else if (targets) {
      const suggested = suggestTargets(columns.map((c) => c.header), targets)
      setChoices(columns.map((c, j) => ({ include: suggested[j] !== '', target: suggested[j], type: c.type })))
    }
  }, [preview, targets, isNew])

  const setChoice = (j: number, change: Partial<Choice>) =>
    setChoices((current) => current.map((c, i) => (i === j ? { ...c, ...change } : c)))

  const imported = choices.filter((c) => (isNew ? c.include : c.target !== ''))
  // An added id column can't clash with one from the file.
  const addKey = imported.some((c) => c.target === 'id') ? 'row_id' : 'id'
  const missing = !isNew && targets ? missingRequired(choices.map((c) => c.target), targets) : []

  const problem = useMemo(() => {
    if (!preview) return null
    if (isNew) {
      if (!tableName.trim()) return 'Give the new table a name.'
      if (keyMode === 'column' && !choices[keyColumn]?.include) return 'The primary key column isn’t being imported.'
      return nameProblem([...(keyMode === 'add' ? [addKey] : []), ...imported.map((c) => c.target.trim())])
    }
    if (imported.length === 0) return 'Choose at least one column to import.'
    const seen = new Set<string>()
    for (const c of imported) {
      if (seen.has(c.target)) return `Two file columns both go into “${c.target}”.`
      seen.add(c.target)
    }
    return null
  }, [preview, isNew, tableName, keyMode, keyColumn, choices, imported, addKey])

  const runImport = async () => {
    if (!preview || problem || importing) return
    setImporting(true)
    setError(null)
    cancelled.current = false
    const call = DBService.RunImport({
      path: path.trim(), sheet: preview.sheet, delimiter: preview.delimiter, hasHeader: preview.hasHeader, emptyAsNull,
      schema: isNew ? schema : initialSchema,
      table: isNew ? tableName.trim() : table ?? '',
      newTable: isNew,
      choices: choices.map((c) => ({ target: isNew && !c.include ? '' : c.target.trim(), type: c.type })),
      addKey: isNew && keyMode === 'add' ? addKey : '',
      keyColumn: isNew && keyMode === 'column' ? keyColumn : -1,
    })
    pending.current = call
    try {
      const r = await call
      setResult(r)
      onImported(r)
    } catch (err) {
      setError(cancelled.current ? 'Import cancelled. Nothing was imported.' : errorMessage(err))
    } finally {
      pending.current = null
      setImporting(false)
    }
  }

  const cancelImport = () => {
    cancelled.current = true
    pending.current?.cancel() // Wails cancels the Go context; COPY stops and the transaction rolls back
  }

  // Esc closes, wherever the focus is (a button disabled mid-import drops it to the page).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !importing) {
        e.preventDefault()
        onClose()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [importing, onClose])

  const columns = preview?.columns ?? []
  const sample = (j: number) => (preview?.rows ?? []).map((r) => r?.[j] ?? '').filter((v) => v !== '').slice(0, 3).join(', ')
  const close = () => { if (!importing) onClose() }

  return (
    <div className="palette-backdrop import-backdrop" onMouseDown={close}>
      <div className="import-dialog" role="dialog" aria-label={isNew ? 'Import a file as a new table' : `Import into ${table}`}
           onMouseDown={(e) => e.stopPropagation()}>
        <header className="import-header">
          <span className="import-title">
            {isNew ? 'Import a file as a new table' : <>Import into <code>{initialSchema}.{table}</code></>}
          </span>
          <button type="button" className="json-modal-close" onClick={close} disabled={importing} aria-label="Close" title="Close">
            <XIcon />
          </button>
        </header>

        <div className="import-body">
          <div className="import-file">
            <input id="import-path" className="input" placeholder="Choose a .csv or .xlsx file, or paste its path and press Enter"
                   value={path} spellCheck={false} autoComplete="off" disabled={importing}
                   onChange={(e) => setPath(e.target.value)}
                   onKeyDown={(e) => { if (e.key === 'Enter') load(path, { fresh: true }) }} />
            <button type="button" className="btn btn-secondary btn-sm" onClick={choose} disabled={loading || importing}>
              <UploadIcon /> Choose file…
            </button>
          </div>

          {loading && <p className="import-muted"><Spinner /> Reading the file…</p>}

          {preview && !loading && (
            <>
              <div className="import-options">
                {preview.format === 'xlsx' ? (
                  <label className="import-option">Sheet
                    <select className="select" value={preview.sheet} disabled={importing}
                            onChange={(e) => load(path, { sheet: e.target.value })}>
                      {(preview.sheets ?? []).map((s) => <option key={s} value={s}>{s}</option>)}
                    </select>
                  </label>
                ) : (
                  <label className="import-option">Separator
                    <select className="select" value={preview.delimiter} disabled={importing}
                            onChange={(e) => load(path, { delimiter: e.target.value })}>
                      {SEPARATORS.map(([value, label]) => <option key={label} value={value}>{label}</option>)}
                    </select>
                  </label>
                )}
                <label className="import-check">
                  <input type="checkbox" checked={emptyAsNull} disabled={importing} onChange={(e) => setEmptyAsNull(e.target.checked)} />
                  Empty cells become NULL
                </label>
                <span className="import-count">
                  {preview.rowCount.toLocaleString()} row{preview.rowCount === 1 ? '' : 's'} · {columns.length} column{columns.length === 1 ? '' : 's'}
                </span>
              </div>
              {!preview.hasHeader && (
                <p className="import-note">The first row looks like data, not column names, so it's imported too and the columns are numbered.</p>
              )}
              {(preview.notes ?? []).map((note) => <p key={note} className="import-note">{note}</p>)}

              {isNew && (
                <div className="import-new">
                  <label className="import-option">Schema
                    <select className="select" value={schema} disabled={importing} onChange={(e) => setSchema(e.target.value)}>
                      {schemas.map((s) => <option key={s} value={s}>{s}</option>)}
                    </select>
                  </label>
                  <label className="import-option import-grow">Table name
                    <input className="input" value={tableName} spellCheck={false} disabled={importing}
                           onChange={(e) => setTableName(e.target.value)} />
                  </label>
                  <label className="import-option">Primary key
                    <select className="select" disabled={importing}
                            value={keyMode === 'column' ? `col:${keyColumn}` : keyMode}
                            onChange={(e) => {
                              const v = e.target.value
                              if (v.startsWith('col:')) {
                                setKeyMode('column')
                                setKeyColumn(Number(v.slice(4)))
                              } else {
                                setKeyMode(v as 'add' | 'none')
                              }
                            }}>
                      <option value="add">Add an “{addKey}” column (1, 2, 3…)</option>
                      {columns.map((c, j) => (
                        <option key={j} value={`col:${j}`}>Use “{choices[j]?.target || c.name}”</option>
                      ))}
                      <option value="none">No primary key (rows matched by address)</option>
                    </select>
                  </label>
                </div>
              )}

              <div className="import-columns">
                <table className="import-table">
                  <thead>
                    <tr>
                      <th>File column</th>
                      <th>Sample values</th>
                      <th>{isNew ? 'New column' : 'Goes into'}</th>
                      {isNew && <th>Type</th>}
                    </tr>
                  </thead>
                  <tbody>
                    {columns.map((c, j) => {
                      const choice = choices[j]
                      if (!choice) return null
                      const skipped = isNew ? !choice.include : choice.target === ''
                      return (
                        <tr key={j} className={skipped ? 'skipped' : undefined}>
                          <td>
                            <label className="import-colname">
                              {isNew && (
                                <input type="checkbox" checked={choice.include} disabled={importing}
                                       onChange={(e) => setChoice(j, { include: e.target.checked })} />
                              )}
                              {c.header || `Column ${j + 1}`}
                            </label>
                          </td>
                          <td className="import-sample" title={sample(j)}>{sample(j) || <span className="import-muted">empty</span>}</td>
                          <td>
                            {isNew ? (
                              <input className="input" value={choice.target} spellCheck={false} disabled={skipped || importing}
                                     aria-label={`Name for ${c.header || `column ${j + 1}`}`}
                                     onChange={(e) => setChoice(j, { target: e.target.value })} />
                            ) : (
                              <select className="select" value={choice.target} disabled={importing}
                                      aria-label={`Table column for ${c.header || `column ${j + 1}`}`}
                                      onChange={(e) => setChoice(j, { target: e.target.value })}>
                                <option value="">Skip this column</option>
                                {(targets ?? []).map((t) => (
                                  <option key={t.name} value={t.name} disabled={t.generated}>
                                    {t.name} · {t.type}{t.generated ? ' (filled by Postgres)' : t.required ? ' · required' : ''}
                                  </option>
                                ))}
                              </select>
                            )}
                          </td>
                          {isNew && (
                            <td>
                              <select className="select" value={choice.type} disabled={skipped || importing}
                                      aria-label={`Type for ${c.header || `column ${j + 1}`}`}
                                      onChange={(e) => setChoice(j, { type: e.target.value })}>
                                {IMPORT_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
                              </select>
                            </td>
                          )}
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
              {missing.length > 0 && (
                <p className="import-warn">
                  Every row needs a value for {missing.map((m) => `“${m}”`).join(', ')}, but no file column goes there, so the import will fail.
                </p>
              )}
            </>
          )}

          {error && <p className="import-error">{error}</p>}
          {result && (
            <p className="import-done">
              Imported {result.rows.toLocaleString()} row{result.rows === 1 ? '' : 's'} into {result.schema}.{result.table}
              {result.created ? ' (new table)' : ''} in {result.durationMs.toLocaleString()} ms.
            </p>
          )}
        </div>

        <footer className="import-footer">
          <span className="import-muted">
            {result ? '' : importing ? <><Spinner /> Importing… everything goes in one transaction</>
              : preview ? problem ?? 'One transaction: every row goes in, or none does.' : ''}
          </span>
          {result ? (
            <button type="button" className="btn btn-primary btn-sm" onClick={onClose}>Done</button>
          ) : importing ? (
            <button type="button" className="btn btn-danger btn-sm" onClick={cancelImport}>Cancel import</button>
          ) : (
            <>
              <button type="button" className="btn btn-secondary btn-sm" onClick={onClose}>Cancel</button>
              <button type="button" className="btn btn-primary btn-sm" disabled={!preview || !!problem || loading} onClick={runImport}>
                Import {preview ? `${preview.rowCount.toLocaleString()} row${preview.rowCount === 1 ? '' : 's'}` : ''}
              </button>
            </>
          )}
        </footer>
      </div>
    </div>
  )
}
