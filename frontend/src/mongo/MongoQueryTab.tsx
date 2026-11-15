import { modKey } from '../platform'
import Playground, { Shortcuts } from '../Playground'
import MongoQueryEditor from './MongoQueryEditor'

type Props = {
  tabId: string
  initialText: string // a new tab's example, or a duplicate's text
  active: boolean // this tab is showing (focus the editor)
  collections: string[]
  onDirtyChange: (dirty: boolean) => void
  onTextChange?: (text: string) => void // for Duplicate
  onRan: () => void // a write may have made or emptied collections
}

const SHORTCUTS: [string, string[]][] = [
  ['Run (the selection, or the command at the cursor)', [modKey(), '↵']],
  ['Run the command at the cursor', [modKey(), '⇧', '↵']],
  ['Suggestions', ['Ctrl', 'Space']],
  ['Find & replace', [modKey(), 'F']],
  ['New query tab', [modKey(), 'T']],
  ['Go to collection', [modKey(), 'P']],
]

// A Ctrl+T / ⌘T tab in MongoDB: the query editor on top, results below, like
// Postgres's SQL tabs. A find()'s documents can be edited in place.
export default function MongoQueryTab({ tabId, initialText, active, collections, onDirtyChange, onTextChange, onRan }: Props) {
  return (
    <MongoQueryEditor tabId={tabId} initialText={initialText} active={active} leading={null} layout="playground"
                      collections={collections} onDirtyChange={onDirtyChange} onTextChange={onTextChange}
                      onRan={(result) => { if (result.documents === null) onRan() }}>
      {(q) => (
        <Playground editor={q.editor} results={<>
          {q.notice}
          {q.results ?? (
            <>
              <p className="partiql-examples">
                For example: <code>db.orders.find({'{status: "PAID"}'}).sort({'{_id: -1}'}).limit(20)</code> ·{' '}
                <code>db.orders.aggregate([{'{$group: {_id: "$status", n: {$sum: 1}}}'}])</code> ·{' '}
                <code>db.orders.updateMany({'{status: "NEW"}, {$set: {status: "OPEN"}}'})</code>
              </p>
              <Shortcuts list={SHORTCUTS} />
            </>
          )}
        </>} />
      )}
    </MongoQueryEditor>
  )
}
