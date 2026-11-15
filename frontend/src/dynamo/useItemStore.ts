import { useMemo, type Dispatch, type SetStateAction } from 'react'
import { Service as Dynamo, type TableDetails } from '../../bindings/github.com/anujkaushik1/SpyDB/dynamo'
import type { GridStore } from '../DataGrid'
import { itemTypes, type Item, type NewField, type NewFieldType } from './itemGrid'
import { itemDeletes, itemEdits, itemKey, itemsJson, itemsJsonEdits, type ItemShape } from './itemStore'

type Options = {
  table: string // '' when the items aren't from one table (a PartiQL result from an index, say)
  details: TableDetails | null // the table's key; null until known (or if AWS won't say)
  items: Item[]
  setItems: Dispatch<SetStateAction<Item[]>>
  extra: NewField[] // fields added that no item has yet
  onNewFields: (fields: NewField[]) => void // the JSON view added some: give them columns
  onStale: () => void // a saved item couldn't be read back: load the items again
}

// The grid's store for a DynamoDB table's items (see GridStore in
// DataGrid.tsx): edits save with dynamo.Service.SaveItems and deletes go
// through DeleteItems; the saved items then take their old ones' places,
// deleted ones leave, and nothing reloads (which would lose the pages
// loaded with Load more). No store, and why, when they can't be edited.
export function useItemStore({ table, details, items, setItems, extra, onNewFields, onStale }: Options): { store?: GridStore; readOnly?: string } {
  return useMemo(() => {
    if (!table) return { readOnly: 'not from one table' }
    if (!details) return { readOnly: 'SpyDB couldn’t read the table’s key, which saving needs' }
    const keyNames = [details.partitionKey, details.sortKey].filter((k) => !!k)
    const missing = keyNames.find((k) => items.some((item) => !item[k]))
    if (missing) return { readOnly: `the items leave out ${missing}, part of the key` }
    const shape: ItemShape = {
      keyNames,
      types: { ...Object.fromEntries(extra.map((f) => [f.name, f.type])), ...itemTypes(items) },
    }
    const sameKey = (item: Item, key: (string | null)[]) => JSON.stringify(itemKey(shape, item)) === JSON.stringify(key)
    const store: GridStore = {
      where: table,
      noun: ['item', 'items'],
      keyNames,
      save: async (edits) => {
        const saved = (await Dynamo.SaveItems(table, itemEdits(shape, edits))) ?? []
        setItems((current) => {
          const next = current.slice()
          edits.forEach((e, i) => {
            const item = saved[i]
            const at = next.findIndex((it) => sameKey(it, keyNames.map((k) => e.key[k] ?? null)))
            if (item && at >= 0) next[at] = item
          })
          return next
        })
        if (saved.length < edits.length || saved.some((item) => !item)) onStale()
        return edits.length
      },
      delete: async (rows) => {
        const n = await Dynamo.DeleteItems(table, itemDeletes(rows))
        const gone = rows.map((r) => keyNames.map((k) => r.key[k] ?? null))
        setItems((current) => current.filter((item) => !gone.some((key) => sameKey(item, key))))
        return n
      },
      rowsJson: (rows, edit, asArray) => itemsJson(shape, items, rows, edit, asArray),
      applyRowsJson: (rows, text, edit) => {
        const result = itemsJsonEdits(shape, items, rows, text, edit)
        if ('changes' in result) {
          const known = new Set([...keyNames, ...extra.map((f) => f.name), ...items.flatMap((item) => Object.keys(item))])
          const added = new Map<string, NewFieldType>()
          for (const c of result.changes) {
            if (!known.has(c.field) && c.value !== undefined) added.set(c.field, (c.type as NewFieldType | undefined) ?? 'S')
          }
          if (added.size > 0) onNewFields([...added].map(([name, type]) => ({ name, type })))
        }
        return result
      },
    }
    return { store }
  }, [table, details, items, extra])
}
