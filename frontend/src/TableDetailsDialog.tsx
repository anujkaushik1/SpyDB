import type { ReactNode } from 'react'
import { XIcon } from './icons'

export type Fact = [label: string, value: ReactNode]
export type DetailsSection = { title: string; columns: string[]; rows: ReactNode[][]; empty?: string }

type Props = {
  title: string
  subtitle?: string
  facts: Fact[]
  sections: DetailsSection[]
  loading?: boolean
  error?: string | null
  onClose: () => void
}

// "Table details": the facts the database keeps about a table (its key,
// how many rows, how big…), then its columns or fields, indexes and keys as
// small tables. The same dialog for every database; each fills it in.
export default function TableDetailsDialog({ title, subtitle, facts, sections, loading, error, onClose }: Props) {
  return (
    <div className="palette-backdrop centered" onMouseDown={onClose}>
      <div className="details-dialog" role="dialog" aria-label={`${title} details`} onMouseDown={(e) => e.stopPropagation()}
           onKeyDown={(e) => { if (e.key === 'Escape') { e.preventDefault(); onClose() } }} tabIndex={-1} ref={(el) => el?.focus()}>
        <header className="details-head">
          <div>
            <p className="details-title">{title}</p>
            {subtitle && <p className="details-subtitle">{subtitle}</p>}
          </div>
          <button type="button" className="sidebar-icon-btn" onClick={onClose} aria-label="Close" title="Close (Esc)"><XIcon /></button>
        </header>
        <div className="details-body">
          {error && <p className="details-error">{error}</p>}
          {loading && !error && <p className="details-note">Reading…</p>}
          {facts.length > 0 && (
            <dl className="details-facts">
              {facts.map(([label, value]) => (
                <div key={label} className="details-fact">
                  <dt>{label}</dt>
                  <dd>{value}</dd>
                </div>
              ))}
            </dl>
          )}
          {sections.map((s) => (
            <section key={s.title} className="details-section" aria-label={s.title}>
              <p className="details-section-title">{s.title} <span className="sidebar-count">{s.rows.length}</span></p>
              {s.rows.length === 0 ? <p className="details-note">{s.empty ?? 'None.'}</p> : (
                <table className="details-table">
                  <thead><tr>{s.columns.map((c) => <th key={c}>{c}</th>)}</tr></thead>
                  <tbody>{s.rows.map((r, i) => <tr key={i}>{r.map((cell, j) => <td key={j}>{cell}</td>)}</tr>)}</tbody>
                </table>
              )}
            </section>
          ))}
        </div>
      </div>
    </div>
  )
}
