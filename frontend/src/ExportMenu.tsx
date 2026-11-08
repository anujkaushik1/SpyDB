import { useEffect, useRef, useState } from 'react'
import { DBService } from '../bindings/github.com/anujkaushik1/SpyDB'
import { errorMessage } from './Banner'
import { DownloadIcon, Spinner } from './icons'
import { fileManager } from './platform'
import { useDismiss } from './useDismiss'

export type ExportFormat = 'csv' | 'xlsx'

// One group in the menu, e.g. "These 125 rows" or "Full table", offered as CSV and Excel.
export type ExportChoice = {
  label: string
  note?: string // small print under the label
  fileName: string // suggested file name, without extension or date
  run: (path: string, format: ExportFormat) => Promise<number> // writes the file, returns rows written
}

// 2026-11-08 05:30 → "2026-11-08_0530", appended to suggested file names.
function stamp(date = new Date()) {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}_${pad(date.getHours())}${pad(date.getMinutes())}`
}

const baseName = (path: string) => path.split('/').pop() ?? path

// The Export button in a grid's footer: pick what to export and the format,
// choose where to save (DBService.ChooseExportFile), then Go writes the file.
export default function ExportMenu({ choices }: { choices: ExportChoice[] }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<{ text: string; path?: string; error?: boolean } | null>(null)
  const root = useRef<HTMLDivElement>(null)
  useDismiss(open, root, () => setOpen(false))

  // A success message fades after a while; errors stay until the next export.
  useEffect(() => {
    if (!result || result.error) return
    const timer = setTimeout(() => setResult(null), 10000)
    return () => clearTimeout(timer)
  }, [result])

  const exportAs = async (choice: ExportChoice, format: ExportFormat) => {
    setOpen(false)
    setResult(null)
    try {
      const path = await DBService.ChooseExportFile(`${choice.fileName}_${stamp()}`, format)
      if (!path) return // cancelled
      setBusy(true)
      const rows = await choice.run(path, format)
      setResult({ text: `Exported ${rows.toLocaleString()} row${rows === 1 ? '' : 's'} to ${baseName(path)}`, path })
    } catch (err) {
      setResult({ text: `Export failed: ${errorMessage(err)}`, error: true })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="export-menu" ref={root}>
      {result && (
        <span className={result.error ? 'export-status error' : 'export-status'} title={result.text}>
          {result.text}
          {result.path && (
            <button type="button" className="link-btn" onClick={() => DBService.RevealFile(result.path!)}>Show in {fileManager()}</button>
          )}
        </span>
      )}
      <button type="button" className="btn btn-secondary btn-sm export-btn" onClick={() => setOpen((o) => !o)}
              disabled={busy} aria-haspopup="menu" aria-expanded={open}>
        {busy ? <><Spinner /> Exporting…</> : <><DownloadIcon /> Export</>}
      </button>

      {open && (
        <div className="export-dropdown" role="menu">
          {choices.map((choice) => (
            <div className="export-group" key={choice.label}>
              <p className="export-group-title">{choice.label}</p>
              {choice.note && <p className="export-group-note">{choice.note}</p>}
              <div className="export-formats">
                <button type="button" role="menuitem" className="export-item" onClick={() => exportAs(choice, 'csv')}>
                  CSV <span>.csv</span>
                </button>
                <button type="button" role="menuitem" className="export-item" onClick={() => exportAs(choice, 'xlsx')}>
                  Excel <span>.xlsx</span>
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
