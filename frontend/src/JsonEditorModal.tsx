import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import { parse, stringify } from 'lossless-json'
import { createJSONEditor, Mode, type JSONParser, type JsonEditor } from './vendor/vanilla-jsoneditor/standalone.js'
import './vendor/vanilla-jsoneditor/jse-theme-dark.css'
import { copyText } from './clipboard'
import { normalize, pretty } from './jsonText'
import { shortcut } from './platform'
import { XIcon } from './icons'

type Props = {
  title: string // the column name, or "Row 3"
  tag: string // small label after it: jsonb, json, JSON
  note: string // e.g. "row 3" or "6 columns"
  value: string | null
  readOnly: boolean // query results that can't be saved back, or a whole-row view
  readOnlyNote?: string // footer text when read-only
  hint?: string // footer text while editing
  allowNull?: boolean // offer Set NULL (a single value; not for a whole row)
  onApply?: (value: string | null) => string | void // becomes unsaved edits; a returned message is shown instead
  onClose: () => void
}

// lossless-json keeps numbers exactly as written: 12345678901234567890 stays
// that, where JSON.parse would round it to 12345678901234567000.
const parser = { parse, stringify } as JSONParser

// The editor passes typing on to get() / onChange 300 ms after the last
// keystroke; reading sooner would miss it.
const SETTLE_MS = 350


// A json / jsonb value as indented JSON text, like DynamoDB's JSON view:
// vanilla-jsoneditor's code editor without its toolbar, so just line numbers,
// folding, colours, error marks, ⌘F search and ⌘Z undo. Loaded on first use,
// so the 1.2 MB editor doesn't slow down startup.
export default function JsonEditorModal({ title, tag, note, value, readOnly, readOnlyNote, hint, allowNull = true, onApply, onClose }: Props) {
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
  // Live search: the editor's ⌘F panel only highlights until you press Next.
  // As you type, this selects the first match (CodeMirror scrolls to it) and
  // shows "1 of 3"; Next, Previous and Enter keep the count. The bundled
  // editor doesn't hand out its CodeMirror view, but CodeMirror leaves it on
  // the content element (cmView.view), which is enough to move the cursor.
  useEffect(() => {
    const root = host.current
    if (!root) return
    type View = {
      state: { doc: { toString(): string }; selection: { main: { from: number; to: number } } }
      dispatch: (spec: object) => void
      lineBlockAt: (pos: number) => { top: number; height: number }
      scrollDOM: HTMLElement
    }
    // CodeMirror 6.38+ keeps it as cmTile.view; older builds as cmView.view.
    const view = (): View | null => {
      const content = root.querySelector('.cm-content') as unknown as { cmTile?: { view?: View }; cmView?: { view?: View } } | null
      return content?.cmTile?.view ?? content?.cmView?.view ?? null
    }
    const matches = (): { from: number; to: number }[] => {
      const panel = root.querySelector('.cm-search')
      const query = panel?.querySelector<HTMLInputElement>('input[name="search"]')?.value ?? ''
      const v = view()
      if (!panel || !query || !v) return []
      const text = v.state.doc.toString()
      const flag = (name: string) => panel.querySelector<HTMLInputElement>(`input[name="${name}"]`)?.checked ?? false
      let re: RegExp
      try {
        const source = flag('re') ? query : query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
        re = new RegExp(flag('word') ? `\\b(?:${source})\\b` : source, flag('case') ? 'g' : 'gi')
      } catch {
        return []
      }
      const at: { from: number; to: number }[] = []
      for (const m of text.matchAll(re)) {
        if (m[0] === '') break
        at.push({ from: m.index ?? 0, to: (m.index ?? 0) + m[0].length })
        if (at.length > 5000) break
      }
      return at
    }
    const showCount = (at: { from: number }[]) => {
      const panel = root.querySelector('.cm-search')
      if (!panel) return
      let badge = panel.querySelector<HTMLSpanElement>('.json-search-count')
      if (!badge) {
        badge = document.createElement('span')
        badge.className = 'json-search-count'
        panel.querySelector('input[name="search"]')?.insertAdjacentElement('afterend', badge)
      }
      const typed = panel.querySelector<HTMLInputElement>('input[name="search"]')?.value
      const here = view()?.state.selection.main.from
      const index = at.findIndex((m) => m.from === here)
      badge.textContent = at.length === 0 ? (typed ? 'No matches' : '') : `${index < 0 ? '–' : index + 1} of ${at.length}`
    }
    const reveal = (m: { from: number; to: number }) => {
      const v = view()
      if (!v) return
      v.dispatch({ selection: { anchor: m.from, head: m.to }, scrollIntoView: true })
      const block = v.lineBlockAt(m.from)
      const scroller = v.scrollDOM
      scroller.scrollTop = Math.max(0, block.top - (scroller.clientHeight - block.height) / 2)
    }
    const onInput = (e: Event) => {
      const target = e.target as HTMLInputElement
      if (!target.closest?.('.cm-search') || (target.name !== 'search' && target.type !== 'checkbox')) return
      const at = matches()
      // The first match from the start: selected (Next goes on from it) and scrolled
      // into the middle of the editor. The line's place comes from the editor's own
      // measurements (lineBlockAt), which it keeps for lines it hasn't drawn too.
      if (at.length > 0) reveal(at[0])
      showCount(at)
    }
    // Next, Previous, Enter and Shift+Enter move between the counted matches
    // (the panel's own Enter does nothing in this build, and its moves leave
    // the match at the edge): the match after or before the cursor, round the
    // ends. All, Replace and Replace All stay the panel's.
    const move = (step: 1 | -1) => {
      const at = matches()
      const v = view()
      if (at.length === 0 || !v) { showCount(at); return }
      const here = v.state.selection.main.from
      let index = at.findIndex((m) => m.from === here)
      if (index < 0) {
        index = step > 0 ? at.findIndex((m) => m.from > here) : -1
        if (step < 0) for (let k = at.length - 1; k >= 0; k--) if (at[k].from < here) { index = k; break }
      }
      else index += step
      if (index < 0 || index >= at.length) index = step > 0 ? 0 : at.length - 1
      reveal(at[index])
      showCount(at)
    }
    const onKey = (e: globalThis.KeyboardEvent) => {
      const target = e.target as HTMLInputElement
      if (target.name !== 'search' || !target.closest?.('.cm-search') || e.key !== 'Enter') return
      e.preventDefault()
      e.stopPropagation()
      move(e.shiftKey ? -1 : 1)
    }
    const onClick = (e: Event) => {
      const button = (e.target as HTMLElement).closest?.('.cm-search button') as HTMLButtonElement | null
      if (!button || (button.name !== 'next' && button.name !== 'prev')) return
      e.preventDefault()
      e.stopPropagation()
      move(button.name === 'next' ? 1 : -1)
    }
    root.addEventListener('input', onInput)
    root.addEventListener('click', onClick, true) // before the panel's own handlers
    root.addEventListener('keydown', onKey, true)
    return () => {
      root.removeEventListener('input', onInput)
      root.removeEventListener('click', onClick, true)
      root.removeEventListener('keydown', onKey, true)
    }
  }, [])

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
    const problem = onApply?.(next)
    if (problem) setError(problem)
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
      <div className="json-modal" role="dialog" aria-label={`JSON: ${title}`}
           onMouseDown={(e) => e.stopPropagation()} onKeyDownCapture={onKeyDownCapture}
           onInputCapture={noteEdit} onPasteCapture={noteEdit} onCutCapture={noteEdit} onDropCapture={noteEdit}>
        <div className="json-modal-header">
          <span className="json-modal-title">{title}</span>
          <span className="json-modal-type">{tag}</span>
          <span className="json-modal-row">{note}</span>
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
              <span className="json-modal-status">{readOnlyNote ?? `This result can’t be edited. ${shortcut('F')} to search; Copy and selecting text still work.`}</span>
              <button type="button" className="btn btn-secondary btn-sm" onClick={onClose}>Close</button>
            </>
          ) : (
            <>
              <span className={error || invalid ? 'json-modal-status error' : 'json-modal-status'}>
                {error ?? (invalid ? 'Fix the JSON before applying.' : hint ?? `${shortcut('F')} to search. Apply keeps the edit unsaved until you save (${shortcut('S')}).`)}
              </span>
              {allowNull && <button type="button" className="link-btn json-modal-null" onClick={() => onApply?.(null)}>Set NULL</button>}
              <button type="button" className="btn btn-secondary btn-sm" onClick={requestClose}>Cancel</button>
              <button type="button" className="btn btn-primary btn-sm" onClick={apply} disabled={invalid} title={`Apply (${shortcut('Enter')})`}>
                Apply
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
