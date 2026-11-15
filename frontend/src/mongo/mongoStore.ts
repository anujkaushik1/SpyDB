import { Service as Mongo } from '../../bindings/github.com/anujkaushik1/SpyDB/mongodb'
import type { GridColumn, GridStore } from '../DataGrid'
import { applyDocsJson, columnHint, docsJson, documentDeletes, documentEdits, toDoc, type Doc, type NewField } from './docGrid'

type Options = {
  collection: string
  docs: Doc[] // the grid's rows, in order
  columns: GridColumn[]
  onSaved: (docs: Doc[]) => void // saved documents as they are now: put them in their rows
  onDeleted: (ids: string[]) => void // take these out of the rows
  onNewFields: (fields: NewField[]) => void // the JSON view added fields no column has yet
}

// The grid's GridStore for a MongoDB collection: edits save with
// mongodb.Service.SaveDocuments (by _id, each changed field checked first),
// deletes with DeleteDocuments, and the JSON view is the documents' own.
export function mongoStore({ collection, docs, columns, onSaved, onDeleted, onNewFields }: Options): GridStore {
  // The type each column's new values get: its one type (an added field's chosen one).
  const hints = Object.fromEntries(columns.map((c) => [c.name, columnHint(c)]))
  return {
    where: collection,
    noun: ['document', 'documents'],
    keyNames: ['_id'],
    save: async (edits) => {
      const saved = ((await Mongo.SaveDocuments(collection, documentEdits(edits, hints))) ?? []).map(toDoc)
      onSaved(saved)
      return saved.length
    },
    delete: async (rows) => {
      const deletes = documentDeletes(rows)
      const n = await Mongo.DeleteDocuments(collection, deletes)
      onDeleted(deletes.map((d) => d.id))
      return n
    },
    rowsJson: (rows, edit, asArray) => docsJson(docs, rows, edit, hints, asArray),
    applyRowsJson: (rows, text, edit) => {
      const result = applyDocsJson(docs, rows, text, edit, hints)
      if ('error' in result) return result
      const known = new Set(columns.map((c) => c.name))
      const added: NewField[] = []
      for (const c of result.changes) {
        if (known.has(c.field) || c.value === undefined) continue
        known.add(c.field)
        added.push({ name: c.field, type: c.type === 'number' ? (/[.eE]/.test(String(c.value)) ? 'double' : 'int') : c.type ?? 'string' })
      }
      if (added.length > 0) onNewFields(added)
      return result
    },
  }
}

// Saved documents in place of the ones with the same _id.
export const replaceDocs = (current: Doc[], saved: Doc[]) => {
  const byId = new Map(saved.map((d) => [d.id, d]))
  return current.map((d) => byId.get(d.id) ?? d)
}
