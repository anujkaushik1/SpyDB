import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react'
import { autocompletion, closeBrackets, closeBracketsKeymap, completionKeymap, type Completion, type CompletionContext, type CompletionResult } from '@codemirror/autocomplete'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { PostgreSQL, schemaCompletionSource } from '@codemirror/lang-sql'
import { bracketMatching, HighlightStyle, indentOnInput, LanguageSupport, syntaxHighlighting } from '@codemirror/language'
import { highlightSelectionMatches, search, searchKeymap } from '@codemirror/search'
import { Compartment, EditorState, Prec } from '@codemirror/state'
import { drawSelection, EditorView, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers, placeholder } from '@codemirror/view'
import { tags as t } from '@lezer/highlight'
import { completionContext, keywordLabels, statementColumns, statementTables, type CompletionSchema } from './sqlCompletion'
import { statementAt } from './sqlStatements'

export type SqlCodeEditorHandle = {
  focus: (atEnd?: boolean) => void
  selection: () => { from: number; to: number }
  cursor: () => number
  select: (position: number) => void // puts the cursor there (e.g. where Postgres found an error)
}

type Props = {
  initialValue: string
  placeholderText: string
  schema?: CompletionSchema // tables and columns to autocomplete
  className?: string
  onChange: (value: string) => void
  onSelectionChange: (hasSelection: boolean) => void
  onRun: () => void // Mod-Enter (Mod is ⌘ on macOS, Ctrl elsewhere)
  onRunCurrent: () => void // Mod-Shift-Enter
  onSave?: () => void // Mod-s
  onEscape: () => boolean // Esc; true when it did something (cancelled a query)
}

// Colours for SQL, matching the app (and the JSON editor).
const highlight = HighlightStyle.define([
  { tag: [t.keyword, t.operatorKeyword, t.modifier], color: '#bb9af7' },
  { tag: [t.string, t.special(t.string)], color: '#9ece6a' },
  { tag: [t.number, t.bool, t.null], color: '#ff9e64' },
  { tag: t.comment, color: '#6b6b74', fontStyle: 'italic' },
  { tag: t.typeName, color: '#7dcfff' },
  { tag: t.special(t.name), color: '#7aa2f7' }, // "quoted identifiers"
  { tag: [t.operator, t.punctuation, t.bracket], color: '#a9b1d6' },
])

const theme = EditorView.theme({
  '&': { height: '100%', fontSize: '13px', color: 'var(--v-text)', backgroundColor: 'transparent' },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.65', overflow: 'auto' },
  '.cm-content': { padding: '8px 0', caretColor: 'var(--focus)' },
  '.cm-gutters': { backgroundColor: 'transparent', color: 'var(--text-subtle)', border: 'none' },
  '.cm-lineNumbers .cm-gutterElement': { padding: '0 10px 0 14px', minWidth: '24px' },
  '.cm-activeLine': { backgroundColor: 'rgba(255, 255, 255, 0.03)' },
  '.cm-activeLineGutter': { backgroundColor: 'transparent', color: 'var(--text)' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--focus)', borderLeftWidth: '2px' },
  '&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection':
    { backgroundColor: 'rgba(122, 162, 247, 0.28)' },
  '.cm-selectionMatch': { backgroundColor: 'rgba(122, 162, 247, 0.14)' },
  '&.cm-focused .cm-matchingBracket': { backgroundColor: 'rgba(122, 162, 247, 0.25)', outline: 'none' },
  '.cm-placeholder': { color: 'var(--text-subtle)' },
  '.cm-tooltip': { backgroundColor: 'var(--surface)', border: '1px solid var(--border-hover)', borderRadius: '8px', overflow: 'hidden' },
  '.cm-tooltip-autocomplete > ul': { fontFamily: 'var(--font-mono)', fontSize: '12.5px', maxHeight: '16em' },
  '.cm-tooltip-autocomplete > ul > li': { padding: '3px 10px', color: 'var(--text-muted)' },
  '.cm-tooltip-autocomplete > ul > li[aria-selected]': { backgroundColor: 'rgba(122, 162, 247, 0.22)', color: 'var(--text)' },
  '.cm-completionDetail': { color: 'var(--text-subtle)', fontStyle: 'normal', marginLeft: '10px' },
  // Small text tags instead of the default emoji icons.
  '.cm-completionIcon': { width: '2.6em', paddingRight: '6px', opacity: '1', fontSize: '10px', fontFamily: 'var(--font-mono)', textAlign: 'right' },
  '.cm-completionIcon-keyword::after': { content: "'kw'", color: '#bb9af7' },
  '.cm-completionIcon-type::after': { content: "'tbl'", color: 'var(--kind-table)' },
  '.cm-completionIcon-property::after': { content: "'col'", color: '#9ece6a' },
  '.cm-completionIcon-constant::after': { content: "'as'", color: '#ff9e64' },
  '.cm-completionMatchedText': { textDecoration: 'none', color: 'var(--focus)' },
  '.cm-panels': { backgroundColor: 'var(--grid-head)', color: 'var(--text)' },
  '.cm-panels-top': { borderBottom: '1px solid var(--border)' },
  '.cm-search': { fontFamily: 'var(--font-ui)', fontSize: '12px' },
  '.cm-textfield': { backgroundColor: 'var(--surface-sunken)', border: '1px solid var(--border-hover)', borderRadius: '5px', color: 'var(--text)' },
  '.cm-button': { backgroundImage: 'none', backgroundColor: 'var(--surface)', border: '1px solid var(--border-hover)', borderRadius: '5px', color: 'var(--text)' },
  '.cm-searchMatch': { backgroundColor: 'rgba(255, 158, 100, 0.22)' },
  '.cm-searchMatch-selected': { backgroundColor: 'rgba(255, 158, 100, 0.5)' },
}, { dark: true })

// Suggestions that fit where the cursor is:
//   after FROM / JOIN / INTO / UPDATE … : tables (and schemas) only;
//   after "name." : that schema's tables, or that table's (or alias's) columns;
//   anywhere else: columns of the tables this statement uses, then everyday
//   SQL words in the case you type, then other tables.
// Everything comes from `schema` (loaded once per connection): typing never
// queries the database.
function smartCompletions(schema?: CompletionSchema) {
  const fromSchema = schemaCompletionSource({ dialect: PostgreSQL, schema, defaultSchema: 'public' })
  return (context: CompletionContext): CompletionResult | null => {
    const before = context.state.sliceDoc(Math.max(0, context.pos - 500), context.pos)
    if (completionContext(before) !== 'general') return fromSchema(context) as CompletionResult | null
    const word = context.matchBefore(/[\w$]*/)
    if (!context.explicit && (!word || word.from === word.to)) return null
    const typed = word ? context.state.sliceDoc(word.from, word.to) : ''
    const statement = statementAt(context.state.doc.toString(), context.pos)
    const columns = schema && statement ? statementColumns(schema, statementTables(statement.text)) : []
    const tables = (fromSchema(context) as CompletionResult | null)?.options ?? []
    const options: Completion[] = [
      ...columns.map((c) => ({ label: c.name, type: 'property', detail: c.table, boost: 2 })),
      ...keywordLabels(typed).map((k) => ({ label: k, type: 'keyword' })),
      ...tables.map((o) => ({ ...o, boost: -1 })),
    ]
    return { from: word ? word.from : context.pos, options, validFor: /^[\w$]*$/ }
  }
}

// Postgres SQL (colours for every keyword) with the suggestions above.
const language = (schema?: CompletionSchema) =>
  new LanguageSupport(PostgreSQL.language, [PostgreSQL.language.data.of({ autocomplete: smartCompletions(schema) })])

// The SQL code editor (CodeMirror): line numbers, colours, autocomplete for
// SQL keywords and the connection's tables and columns, bracket matching,
// find & replace (Mod-F), comment toggling (Mod-/) and undo.
export default forwardRef<SqlCodeEditorHandle, Props>(function SqlCodeEditor(props, ref) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const languageSlot = useRef(new Compartment())
  // Key handlers run inside CodeMirror; read the newest props through this.
  const latest = useRef(props)
  latest.current = props

  useEffect(() => {
    const ownKeys = Prec.highest(keymap.of([
      { key: 'Mod-Enter', run: () => { latest.current.onRun(); return true } },
      { key: 'Mod-Shift-Enter', run: () => { latest.current.onRunCurrent(); return true } },
      { key: 'Mod-s', preventDefault: true, run: () => { latest.current.onSave?.(); return !!latest.current.onSave } },
      { key: 'Escape', run: () => latest.current.onEscape() },
    ]))
    const editor = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: props.initialValue,
        extensions: [
          ownKeys,
          lineNumbers(),
          highlightActiveLineGutter(),
          highlightActiveLine(),
          drawSelection(),
          history(),
          indentOnInput(),
          bracketMatching(),
          closeBrackets(),
          autocompletion({ filterStrict: true }), // a suggestion must start with what you typed (any case)
          search({ top: true }),
          highlightSelectionMatches(),
          languageSlot.current.of(language(props.schema)),
          syntaxHighlighting(highlight),
          theme,
          placeholder(props.placeholderText),
          keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...searchKeymap, ...historyKeymap, ...completionKeymap, indentWithTab]),
          EditorView.contentAttributes.of({ 'aria-label': 'SQL query', spellcheck: 'false', autocorrect: 'off', autocapitalize: 'off' }),
          EditorView.updateListener.of((update) => {
            if (update.docChanged) latest.current.onChange(update.state.doc.toString())
            if (update.docChanged || update.selectionSet) latest.current.onSelectionChange(!update.state.selection.main.empty)
          }),
        ],
      }),
    })
    view.current = editor
    return () => {
      editor.destroy()
      view.current = null
    }
  }, [])

  // Another connection, or a CREATE / ALTER / DROP: autocomplete follows.
  useEffect(() => {
    view.current?.dispatch({ effects: languageSlot.current.reconfigure(language(props.schema)) })
  }, [props.schema])

  useImperativeHandle(ref, () => ({
    focus: (atEnd) => {
      const editor = view.current
      if (!editor) return
      if (atEnd) editor.dispatch({ selection: { anchor: editor.state.doc.length } })
      editor.focus()
    },
    selection: () => {
      const main = view.current?.state.selection.main
      return { from: main?.from ?? 0, to: main?.to ?? 0 }
    },
    cursor: () => view.current?.state.selection.main.head ?? 0,
    select: (position) => {
      const editor = view.current
      if (!editor) return
      editor.dispatch({ selection: { anchor: Math.max(0, Math.min(position, editor.state.doc.length)) }, scrollIntoView: true })
      editor.focus()
    },
  }), [])

  return <div className={['sql-code', props.className].filter(Boolean).join(' ')} ref={host} />
})
