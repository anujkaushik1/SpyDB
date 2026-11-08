import { useEffect, useState } from 'react'
import Banner, { errorMessage } from './Banner'
import SaveQueryDialog from './SaveQueryDialog'
import SqlEditor from './SqlEditor'
import { CodeIcon } from './icons'
import { modKey } from './platform'
import { suggestName } from './savedQueries'

type Props = {
  tabId: string
  title: string // the tab's title; the Save dialog falls back to it for a name
  initialSql: string // '' for a new tab, the saved SQL when opened from Saved queries
  linked: boolean // the tab belongs to a saved query, so Save updates it
  database: string // the label a newly saved query gets
  active: boolean // this tab is showing (focus the editor)
  onSchemaChange: () => void
  onDirtyChange: (dirty: boolean) => void
  onSaveQuery: (sql: string, name: string | null) => Promise<void> // name null: update the linked query
}

// A Ctrl+T / ⌘T tab: a SQL editor with its results below. Results that come
// straight from one table (with its primary key) can be edited in place.
// Save keeps the SQL as a saved query: the first time it asks for a name.
export default function QueryTab({ tabId, title, initialSql, linked, database, active, onSchemaChange, onDirtyChange, onSaveQuery }: Props) {
  const [dialogSql, setDialogSql] = useState<string | null>(null) // asking for a name
  const [saveState, setSaveState] = useState<'idle' | 'saving' | 'saved'>('idle')
  const [saveError, setSaveError] = useState<string | null>(null)

  useEffect(() => {
    if (saveState !== 'saved') return
    const timer = setTimeout(() => setSaveState('idle'), 1500)
    return () => clearTimeout(timer)
  }, [saveState])

  const save = async (sql: string) => {
    setSaveError(null)
    if (!linked) {
      setDialogSql(sql)
      return
    }
    setSaveState('saving')
    try {
      await onSaveQuery(sql, null)
      setSaveState('saved')
    } catch (err) {
      setSaveState('idle')
      setSaveError(errorMessage(err))
    }
  }

  return (
    <>
      <SqlEditor
        tabId={tabId}
        initialSql={initialSql}
        active={active}
        leading={null}
        onSchemaChange={onSchemaChange}
        onRan={() => {}}
        onDirtyChange={onDirtyChange}
        onSave={save}
        saveState={saveState}
      >
        {(sql) => (
          <div className="table-view">
            {sql.editor}
            {saveError && <div className="save-query-banner"><Banner tone="error" title="Couldn’t save the query" message={saveError} /></div>}
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
      {dialogSql !== null && (
        <SaveQueryDialog
          initialName={suggestName(dialogSql, title)}
          database={database}
          onSave={async (name) => {
            await onSaveQuery(dialogSql, name)
            setDialogSql(null)
            setSaveState('saved')
          }}
          onClose={() => setDialogSql(null)}
        />
      )}
    </>
  )
}
