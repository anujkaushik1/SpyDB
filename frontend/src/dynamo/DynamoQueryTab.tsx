import { modKey } from '../platform'
import Playground, { Shortcuts } from '../Playground'
import PartiqlEditor from './PartiqlEditor'

type Props = {
  initialStatement: string // '' for a new tab; a duplicate's text
  active: boolean // this tab is showing (focus the editor)
  onDirtyChange: (dirty: boolean) => void
  onStatementChange?: (text: string) => void // for Duplicate
}

const SHORTCUTS: [string, string[]][] = [
  ['Run (the selection, if any)', [modKey(), '↵']],
  ['Run the statement at the cursor', [modKey(), '⇧', '↵']],
  ['Comment / uncomment lines', [modKey(), '/']],
  ['Find & replace', [modKey(), 'F']],
  ['New PartiQL tab', [modKey(), 'T']],
  ['Go to table', [modKey(), 'P']],
]

// A Ctrl+T / ⌘T tab in DynamoDB: a PartiQL playground, like Postgres's SQL
// tabs. SELECT results from one table can be edited in place.
export default function DynamoQueryTab({ initialStatement, active, onDirtyChange, onStatementChange }: Props) {
  return (
    <PartiqlEditor initialStatement={initialStatement} active={active} leading={null} layout="playground"
                   onDirtyChange={onDirtyChange} onStatementChange={onStatementChange}>
      {(pq) => (
        <Playground editor={pq.editor} results={<>
          {pq.notice}
          {pq.results ?? (
            <>
              <p className="partiql-examples">
                For example: <code>SELECT * FROM "orders" WHERE "userId" = 'u1'</code> ·{' '}
                <code>INSERT INTO "orders" VALUE {"{'userId': 'u2', 'total': 10}"}</code> ·{' '}
                <code>UPDATE "orders" SET "total" = 12 WHERE "userId" = 'u2'</code>
              </p>
              <Shortcuts list={SHORTCUTS} />
            </>
          )}
        </>} />
      )}
    </PartiqlEditor>
  )
}
