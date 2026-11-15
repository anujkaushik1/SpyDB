import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { GridEdit } from '../src/DataGrid'
import type { Item } from '../src/dynamo/itemGrid'
import { itemDeletes, itemEdits, itemsJson, itemsJsonEdits } from '../src/dynamo/itemStore'

const v = (type: string, text = '') => ({ type, text })
const shape = { keyNames: ['id', 'n'], types: { amount: 'N', paid: 'BOOL', tier: 'N' } as Record<string, string> }
const items: Item[] = [
  { id: v('S', 'a'), n: v('N', '1'), amount: v('N', '12345678901234567890.50'), paid: v('BOOL', 'true'), city: v('S', 'Delhi'),
    address: v('M', '{"pin":110001,"lines":["x"]}'), note: v('NULL'), tags: v('SS', '["new","vip"]') },
  { id: v('S', 'b'), n: v('N', '2'), city: v('S', 'Pune') },
]
const none = () => undefined

test('an item as JSON: the key first, its own fields A to Z, as their types, numbers exact', () => {
  assert.equal(itemsJson(shape, items, [1], none, false), '{\n  "id": "b",\n  "n": 2,\n  "city": "Pune"\n}')
  const one = itemsJson(shape, items, [0], none, false)
  assert.match(one, /"amount": 12345678901234567890\.50,/)
  assert.match(one, /"address": \{\n {4}"pin": 110001,/)
  assert.match(one, /"note": null,\n {2}"paid": true,\n {2}"tags": \[/)
  assert.ok(itemsJson(shape, items, [0, 1], none, false).startsWith('[\n  {'))
  assert.ok(itemsJson(shape, items, [1], none, true).startsWith('[\n  {'), 'Copy as JSON: always an array')
})

test('unsaved edits show in the JSON: typed values as the field’s type, removed fields gone, new ones there', () => {
  const edit: GridEdit = {
    key: { id: 'a', n: '1' }, old: {},
    values: { amount: '99', paid: 'f', city: undefined, tier: '3', meta: '{"x":1}', label: '5' },
    types: { meta: 'JSON', label: 'S' },
  }
  const json = JSON.parse(itemsJson(shape, items, [0], (r) => (r === 0 ? edit : undefined), false))
  assert.equal(json.amount, 99)
  assert.equal(json.paid, false)
  assert.equal('city' in json, false)
  assert.equal(json.tier, 3, 'a new field typed into a number column')
  assert.deepEqual(json.meta, { x: 1 })
  assert.equal(json.label, '5', 'the JSON view chose text')
})

test('edited JSON back into changes: changed, added (typed by the JSON), removed', () => {
  const text = JSON.stringify({ id: 'b', n: 2, city: 'Agra', tier: 5, ok: true, meta: { a: [1, 2] }, empty: null, s: '5' })
  const result = itemsJsonEdits(shape, items, [1], text, none)
  assert.ok('changes' in result)
  assert.deepEqual(result.changes, [
    { row: 0, field: 'city', value: 'Agra', type: 'S' },
    { row: 0, field: 'tier', value: '5', type: 'N' },
    { row: 0, field: 'ok', value: 'true', type: 'BOOL' },
    { row: 0, field: 'meta', value: '{"a":[1,2]}', type: 'JSON' },
    { row: 0, field: 'empty', value: null },
    { row: 0, field: 's', value: '5', type: 'S' },
  ])
  const removed = itemsJsonEdits(shape, items, [1], JSON.stringify({ id: 'b', n: 2 }), none)
  assert.deepEqual(removed, { changes: [{ row: 0, field: 'city', value: undefined }] })
})

test('JSON left as it was changes nothing, exact big numbers included', () => {
  const same = itemsJson(shape, items, [0, 1], none, false)
  assert.deepEqual(itemsJsonEdits(shape, items, [0, 1], same, none), { changes: [] })
})

test('the key can’t change, nor items be added or removed', () => {
  const keyChanged = itemsJsonEdits(shape, items, [1], JSON.stringify({ id: 'z', n: 2, city: 'Pune' }), none)
  assert.ok('error' in keyChanged && keyChanged.error.includes('"id" is part of the key'))
  const keyGone = itemsJsonEdits(shape, items, [1], JSON.stringify({ id: 'b', city: 'Pune' }), none)
  assert.ok('error' in keyGone && keyGone.error.includes('"n" is part of the key'))
  const fewer = itemsJsonEdits(shape, items, [0, 1], JSON.stringify([{ id: 'a', n: 1 }]), none)
  assert.ok('error' in fewer && fewer.error.includes('expected 2, found 1'))
  const bad = itemsJsonEdits(shape, items, [1], '{oops', none)
  assert.ok('error' in bad && bad.error.startsWith('Not valid JSON'))
})

test('edits and deletes as Go takes them', () => {
  const edits = itemEdits(shape, [{
    key: { id: 'a', n: '1' },
    old: { city: 'Delhi', tier: undefined, note: null },
    values: { city: undefined, tier: '4', note: 'x', label: '5' },
    types: { label: 'N' },
  }])
  assert.deepEqual(edits, [{
    key: { id: 'a', n: '1' },
    old: { city: { has: true, null: false, text: 'Delhi' }, tier: { has: false, null: false, text: '' }, note: { has: true, null: true, text: '' } },
    changes: {
      city: { remove: true, null: false, text: '', type: '', typed: false },
      tier: { remove: false, null: false, text: '4', type: 'N', typed: false },
      note: { remove: false, null: false, text: 'x', type: '', typed: false },
      label: { remove: false, null: false, text: '5', type: 'N', typed: true },
    },
  }])
  assert.deepEqual(itemDeletes([{ key: { id: 'b', n: '2' }, old: { city: 'Pune', x: undefined } }]), [{
    key: { id: 'b', n: '2' }, old: { city: { has: true, null: false, text: 'Pune' }, x: { has: false, null: false, text: '' } },
  }])
})
