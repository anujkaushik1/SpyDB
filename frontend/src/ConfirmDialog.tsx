import type { ReactNode } from 'react'

type Props = {
  title: string
  message: ReactNode
  confirmLabel: string // the red button, e.g. "Close 2 tabs"
  onConfirm: () => void
  onCancel: () => void
}

// Asks before something that loses work. Cancel has the focus, so Enter
// and Esc both keep things as they are.
export default function ConfirmDialog({ title, message, confirmLabel, onConfirm, onCancel }: Props) {
  return (
    <div className="palette-backdrop centered" onMouseDown={onCancel}>
      <div className="save-query" role="alertdialog" aria-label={title}
           onMouseDown={(e) => e.stopPropagation()}
           onKeyDown={(e) => { if (e.key === 'Escape') { e.preventDefault(); onCancel() } }}>
        <p className="save-query-title">{title}</p>
        <p className="save-query-note">{message}</p>
        <div className="save-query-actions">
          <button type="button" className="btn btn-secondary btn-sm" onClick={onCancel} autoFocus>Cancel</button>
          <button type="button" className="btn btn-danger btn-sm" onClick={onConfirm}>{confirmLabel}</button>
        </div>
      </div>
    </div>
  )
}
