import SqlEditor from './SqlEditor'
import { CodeIcon } from './icons'
import { modKey } from './platform'

type Props = {
  tabId: string
  active: boolean // this tab is showing (focus the editor)
  onSchemaChange: () => void
  onDirtyChange: (dirty: boolean) => void
}

// A Ctrl+T / ⌘T tab: a SQL editor with its results below. Results that come
// straight from one table (with its primary key) can be edited in place.
export default function QueryTab({ tabId, active, onSchemaChange, onDirtyChange }: Props) {
  return (
    <SqlEditor
      tabId={tabId}
      initialSql=""
      active={active}
      leading={null}
      onSchemaChange={onSchemaChange}
      onRan={() => {}}
      onDirtyChange={onDirtyChange}
    >
      {(sql) => (
        <div className="table-view">
          {sql.editor}
          {sql.notice}
          {sql.results ?? (
            <div className="workspace-empty">
              <span className="workspace-empty-icon query"><CodeIcon /></span>
              <p className="workspace-empty-title">Write a query</p>
              <p className="workspace-empty-text">
                Press <span className="keys"><kbd>{modKey()}</kbd> <kbd>↵</kbd></span> to run it. If the result comes straight from one table and includes its
                primary key, you can edit its cells and save them.
              </p>
            </div>
          )}
        </div>
      )}
    </SqlEditor>
  )
}
