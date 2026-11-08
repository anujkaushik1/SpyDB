import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { parse, stringify } from 'lossless-json'
import { createJSONEditor, Mode, type JSONParser, type JsonEditor } from './vendor/vanilla-jsoneditor/standalone.js'
import './vendor/vanilla-jsoneditor/jse-theme-dark.css'
import { copyText } from './copyFormats'
import { XIcon } from './icons'

type Props = {
  column: string
  type: string // json or jsonb
  rowNumber: number // as shown in the grid
  value: string | null
  readOnly: boolean // query results that can't be saved back
  onApply: (value: string | null) => void // becomes an unsaved edit, like a typed one
  onClose: () => void
}

// lossless-json keeps numbers exactly as written: 12345678901234567890 stays
// that, where JSON.parse would round it to 12345678901234567000.
const parser = { parse, stringify } as JSONParser

// The editor passes typing on to get() / onChange 300 ms after the last
// keystroke; reading sooner would miss it.
const SETTLE_MS = 350

// Compact JSON text, numbers untouched. Throws if the text isn't valid JSON.
const normalize = (text: string) => stringify(parse(text)) ?? ''

// Postgres sends jsonb on one line; show it indented, numbers untouched.
function pretty(value: string | null) {
  if (value === null) return ''
  try {
    return stringify(parse(value), null, 2) ?? value
  } catch {
    return value
  }
}

// A json / jsonb value as indented JSON text, like DynamoDB's JSON view:
// vanilla-jsoneditor's code editor without its toolbar, so just line numbers,
// folding, colours, error marks, ⌘F search and ⌘Z undo. Loaded on first use,
// so the 1.2 MB editor doesn't slow down startup.
export default function JsonEditorModal({ column, type, rowNumber, value, readOnly, onApply, onClose }: Props) {
  const host = useRef<HTMLDivElement>(null)
  const editor = useRef<JsonEditor | null>(null)
  const [invalid, setInvalid] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [confirmDiscard, setConfirmDiscard] = useState(false)
  const [copied, setCopied] = useState(false)
  const discardButton = useRef<HTMLButtonElement>(null)
  const initialText = useRef(pretty(value)) // what the editor opened with
  const lastEdit = useRef(0) // when a key, input (dictation, emoji…), paste, cut or drop last reached the editor

  useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => setCopied(false), 2000)
    return () => clearTimeout(timer)
  }, [copied])

  useEffect(() => {
    if (!host.current) return
    const instance = createJSONEditor({
      target: host.current,
      props: {
        content: { text: initialText.current },
        mode: Mode.text,
        parser,
        readOnly,
        askToFormat: false,
        mainMenuBar: false,
        navigationBar: false,
        onChange: (_content: unknown, _previous: unknown, status: { contentErrors?: unknown }) => {
          setInvalid(status.contentErrors !== undefined)
          setError(null)
          setConfirmDiscard(false)
        },
      },
    })
    editor.current = instance
    instance.focus()
    return () => {
      instance.destroy()
      editor.current = null
    }
  }, [])

  // The content including the last keystrokes: waits out the editor's delay
  // when something was typed in the last moment.
  const latest = async () => {
    const wait = lastEdit.current + SETTLE_MS - Date.now()
    if (wait > 0) await new Promise((resolve) => setTimeout(resolve, wait))
    return editor.current?.get() as { json: unknown } | { text: string } | undefined
  }
  const noteEdit = (e: { target: EventTarget }) => {
    const target = e.target as HTMLElement
    if (host.current?.contains(target) && !target.closest('.cm-search')) lastEdit.current = Date.now()
  }

  const apply = async () => {
    const content = await latest()
    if (!content) return
    let next: string
    try {
      if ('json' in content) next = stringify(content.json) ?? ''
      else if (content.text.trim() === '') next = ''
      else next = normalize(content.text)
    } catch (err) {
      setError(`Not valid JSON: ${err instanceof Error ? err.message : String(err)}`)
      return
    }
    if (next === '') {
      setError('The value is empty. Use Set NULL to clear it.')
      return
    }
    // Nothing really changed (or changed back): close without an edit.
    if (value !== null) {
      try {
        if (normalize(value) === next) return onClose()
      } catch {
        // The original wasn't valid JSON, so any valid value is a change.
      }
    }
    onApply(next)
  }

  const copy = async () => {
    const content = await latest()
    if (!content) return
    try {
      await copyText('json' in content ? stringify(content.json, null, 2) ?? '' : content.text)
      setCopied(true)
    } catch (err) {
      setError(`Couldn’t copy: ${err instanceof Error ? err.message : String(err)}`)
    }
  }

  // Compared with the opening text rather than tracked through onChange,
  // which the editor reports a moment late: typing then Esc must still ask.
  const hasChanges = async () => {
    const content = await latest()
    if (!content) return false
    const text = 'json' in content ? stringify(content.json, null, 2) ?? '' : content.text
    return text !== initialText.current
  }

  const requestClose = async () => {
    if (!readOnly && (await hasChanges())) setConfirmDiscard(true)
    else onClose()
  }
  const keepEditing = () => {
    setConfirmDiscard(false)
    editor.current?.focus()
  }

  // Asking "discard?": Enter discards, Esc goes back to editing.
  useEffect(() => {
    if (confirmDiscard) discardButton.current?.focus()
  }, [confirmDiscard])

  // Runs before the editor sees the key, since it would swallow both:
  // ⌘Enter applies (instead of inserting a line), Esc closes (or asks to
  // discard), except while the editor's search bar or suggestions are open,
  // which Esc closes first.
  const onKeyDownCapture = (e: KeyboardEvent<HTMLDivElement>) => {
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter' && !readOnly) {
      e.preventDefault()
      e.stopPropagation()
      apply()
    } else if (e.key === 'Escape' && !host.current?.querySelector('.cm-search, .cm-tooltip-autocomplete')) {
      e.preventDefault()
      e.stopPropagation()
      if (confirmDiscard) keepEditing()
      else requestClose()
    } else {
      noteEdit(e)
    }
  }

  return (
    <div className="json-modal-backdrop" onMouseDown={requestClose}>
      <div className="json-modal" role="dialog" aria-label={`JSON editor: ${column}`}
           onMouseDown={(e) => e.stopPropagation()} onKeyDownCapture={onKeyDownCapture}
           onInputCapture={noteEdit} onPasteCapture={noteEdit} onCutCapture={noteEdit} onDropCapture={noteEdit}>
        <div className="json-modal-header">
          <span className="json-modal-title">{column}</span>
          <span className="json-modal-type">{type}</span>
          <span className="json-modal-row">row {rowNumber.toLocaleString()}</span>
          {readOnly && <span className="json-modal-badge">read-only</span>}
          <button type="button" className="btn btn-secondary btn-sm json-modal-copy" onClick={copy}>
            {copied ? 'Copied' : 'Copy'}
          </button>
          <button type="button" className="json-modal-close" onClick={requestClose} aria-label="Close" title="Close">
            <XIcon />
          </button>
        </div>

        <div className="json-modal-editor jse-theme-dark" ref={host} />

        <div className="json-modal-footer">
          {confirmDiscard ? (
            <>
              <span className="json-modal-status">Discard your changes to this value?</span>
              <button type="button" className="btn btn-secondary btn-sm" onClick={keepEditing}>Keep editing</button>
              <button type="button" className="btn btn-danger btn-sm" onClick={onClose} ref={discardButton}>Discard</button>
            </>
          ) : readOnly ? (
            <>
              <span className="json-modal-status">This result can’t be edited. ⌘F to search; Copy and selecting text still work.</span>
              <button type="button" className="btn btn-secondary btn-sm" onClick={onClose}>Close</button>
            </>
          ) : (
            <>
              <span className={error || invalid ? 'json-modal-status error' : 'json-modal-status'}>
                {error ?? (invalid ? 'Fix the JSON before applying.' : '⌘F to search. Apply keeps the edit unsaved until you save (⌘S).')}
              </span>
              <button type="button" className="link-btn json-modal-null" onClick={() => onApply(null)}>Set NULL</button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={requestClose}>Cancel</button>
              <button type="button" className="btn btn-primary btn-sm" onClick={apply} disabled={invalid} title="Apply (⌘Enter)">
                Apply
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
