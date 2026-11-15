import assert from 'node:assert/strict'
import { test } from 'node:test'
import { addSeen, applyDocsJson, columnHint, docColumns, docKeys, docRows, docsJson, documentDeletes, documentEdits, fieldType, type Doc } from '../src/mongo/docGrid'
import type { GridEdit } from '../src/DataGrid'

const v = (type: string, text = '') => ({ type, text })
const doc = (id: string, fields: Record<string, { type: string; text: string }>): Doc => ({ id, fields, order: Object.keys(fields) })

const docs: Doc[] = [
  doc('{"$oid":"64f1c0ffee0000000000abcd"}', {
    _id: v('objectId', '64f1c0ffee0000000000abcd'), name: v('string', 'Asha'), total: v('long', '1500'),
    paid: v('bool', 'true'), placed: v('date', '2024-05-01T10:30:00.000Z'), address: v('object', '{"city":"Delhi","pin":110001}'),
  }),
  doc('{"$numberInt":"2"}', { total: v('string', 'n/a'), _id: v('int', '2'), paid: v('bool', 'false'), note: v('null'), price: v('decimal', '9.90') }),
]

test('columns: _id first, then fields in the order they first appear, then added ones', () => {
  const columns = docColumns(docs, [{ name: 'rating', type: 'double' }, { name: 'name', type: 'int' }])
  assert.deepEqual(columns.map((c) => c.name), ['_id', 'name', 'total', 'paid', 'placed', 'address', 'note', 'price', 'rating'])
  const byName = Object.fromEntries(columns.map((c) => [c.name, c]))
  assert.deepEqual([byName._id.type, byName._id.primaryKey, byName._id.source], ['objectId / int', true, ''])
  assert.deepEqual([byName.total.type, byName.total.category, byName.total.source], ['long / string', 'text', 'total'])
  assert.deepEqual([byName.paid.category, byName.placed.category, byName.address.category, byName.price.category], ['bool', 'time', 'json', 'number'])
  assert.deepEqual([byName.note.type, byName.rating.type, byName.name.type], ['', 'double', 'string'], 'an added field the documents already have keeps their type')
  assert.ok(docColumns(docs, [], false).every((c) => c.source === ''), 'read-only results')
})

test('rows: missing fields are undefined, null is null, booleans t / f', () => {
  const columns = docColumns(docs)
  assert.deepEqual(docRows(columns, docs), [
    ['64f1c0ffee0000000000abcd', 'Asha', '1500', 't', '2024-05-01T10:30:00.000Z', '{"city":"Delhi","pin":110001}', undefined, undefined],
    ['2', undefined, 'n/a', 'f', undefined, undefined, null, '9.90'],
  ])
  assert.deepEqual(docKeys(docs), [['{"$oid":"64f1c0ffee0000000000abcd"}'], ['{"$numberInt":"2"}']])
})

test('a new field’s values get its column’s one type', () => {
  const byName = Object.fromEntries(docColumns(docs, [{ name: 'tags', type: 'json' }]).map((c) => [c.name, c]))
  assert.equal(columnHint(byName.placed), 'date')
  assert.equal(columnHint(byName.total), '', 'two types: text')
  assert.equal(columnHint(byName.address), 'json')
  assert.equal(columnHint(byName.tags), 'json')
  assert.equal(columnHint(undefined), '')
})

test('filters type a value by what the field has held', () => {
  const seen = addSeen(addSeen({}, [docs[0]]), [docs[1]])
  assert.deepEqual(seen.total, ['long', 'string'])
  assert.deepEqual(seen.note, [], 'null is no type')
  assert.equal(fieldType(seen, 'paid'), 'bool')
  assert.equal(fieldType(seen, 'placed'), 'date')
  assert.equal(fieldType(seen, 'price'), 'number')
  assert.equal(fieldType(seen, 'total'), '')
  assert.equal(fieldType(addSeen({}, [doc('1', { n: v('int', '1') }), doc('2', { n: v('double', '1.5') })]), 'n'), 'number', 'every number type is a number')
  assert.equal(fieldType(seen, '_id'), '', 'an ObjectId and an int')
  assert.equal(fieldType(addSeen({}, [docs[0]]), '_id'), 'objectId')
})

const noEdits = () => undefined

test('the JSON view shows each field as its own type', () => {
  assert.equal(docsJson(docs, [0], noEdits, {}, false), `{
  "_id": {
    "$oid": "64f1c0ffee0000000000abcd"
  },
  "name": "Asha",
  "total": 1500,
  "paid": true,
  "placed": {
    "$date": "2024-05-01T10:30:00.000Z"
  },
  "address": {
    "city": "Delhi",
    "pin": 110001
  }
}`)
  const both = JSON.parse(docsJson(docs, [0, 1], noEdits, {}, false))
  assert.deepEqual(both[1], { total: 'n/a', _id: 2, paid: false, note: null, price: { $numberDecimal: '9.90' } })
  assert.ok(docsJson(docs, [1], noEdits, {}, true).startsWith('['), 'Copy as JSON is always a list')
})

test('the JSON view includes unsaved edits: changed, added and removed fields', () => {
  const edit: GridEdit = { key: { _id: docs[0].id }, old: {}, values: { total: '1600', name: undefined, rating: '4.5', tags: '["a"]', when: 'soon' }, types: { tags: 'json' } }
  const shown = JSON.parse(docsJson(docs, [0], () => edit, { rating: 'double', when: 'date' }, false))
  assert.equal(shown.total, 1600, 'a long stays a number')
  assert.ok(!('name' in shown), 'removed')
  assert.equal(shown.rating, 4.5, 'a new number column')
  assert.deepEqual(shown.tags, ['a'])
  assert.equal(shown.when, 'soon', 'text that isn’t a date shows as text (Go refuses it on save)')
})

test('edited JSON becomes field changes with their JSON types', () => {
  const text = `{"_id": {"$oid": "64f1c0ffee0000000000abcd"}, "name": "Asha", "total": 1500.50, "paid": false,
    "placed": {"$date": "2024-05-01T10:30:00.000Z"}, "address": {"city": "Pune", "pin": 110001}, "rating": 12345678901234567890, "nick": null}`
  const result = applyDocsJson(docs, [0], text, noEdits, {})
  assert.ok('changes' in result)
  assert.deepEqual(result.changes, [
    { row: 0, field: 'total', value: '1500.50', type: 'number' },
    { row: 0, field: 'paid', value: 'f', type: 'bool' },
    { row: 0, field: 'address', value: '{"city":"Pune","pin":110001}', type: 'json' },
    { row: 0, field: 'rating', value: '12345678901234567890', type: 'number' },
    { row: 0, field: 'nick', value: null },
  ])
  const removed = applyDocsJson(docs, [0], `{"_id": {"$oid": "64f1c0ffee0000000000abcd"}, "name": "5"}`, noEdits, {})
  assert.ok('changes' in removed)
  assert.deepEqual(removed.changes.map((c) => [c.field, c.value, c.type]), [
    ['name', '5', 'string'], ['total', undefined, undefined], ['paid', undefined, undefined], ['placed', undefined, undefined], ['address', undefined, undefined],
  ])
})

test('the JSON view refuses a changed _id, bad JSON, and adding documents', () => {
  const err = (r: ReturnType<typeof applyDocsJson>) => ('error' in r ? r.error : '')
  assert.match(err(applyDocsJson(docs, [0], '{"_id": 5}', noEdits, {})), /_id can’t be changed/)
  assert.match(err(applyDocsJson(docs, [0], '{"name": "x"}', noEdits, {})), /_id can’t be changed or removed/)
  assert.match(err(applyDocsJson(docs, [0], '{"name": ', noEdits, {})), /Not valid JSON/)
  assert.match(err(applyDocsJson(docs, [0, 1], '[{}]', noEdits, {})), /expected 2, found 1/)
})

test('edits and deletes as Go takes them', () => {
  const edits: GridEdit[] = [{
    key: { _id: docs[0].id },
    old: { total: '1500', paid: 't', rating: undefined, note: null },
    values: { total: '1600', paid: undefined, rating: '4.5', note: '{"a":1}' },
    types: { note: 'json' },
  }]
  assert.deepEqual(documentEdits(edits, { rating: 'double' }), [{
    id: docs[0].id,
    old: {
      total: { has: true, null: false, text: '1500' },
      paid: { has: true, null: false, text: 't' },
      rating: { has: false, null: false, text: '' },
      note: { has: true, null: true, text: '' },
    },
    changes: {
      total: { remove: false, null: false, text: '1600', type: '', typed: false },
      paid: { remove: true, null: false, text: '', type: '', typed: false },
      rating: { remove: false, null: false, text: '4.5', type: 'double', typed: false },
      note: { remove: false, null: false, text: '{"a":1}', type: 'json', typed: true },
    },
  }])
  assert.deepEqual(documentDeletes([{ key: { _id: '{"$numberInt":"2"}' }, old: { total: 'n/a', name: undefined } }]), [
    { id: '{"$numberInt":"2"}', old: { total: { has: true, null: false, text: 'n/a' }, name: { has: false, null: false, text: '' } } },
  ])
})
