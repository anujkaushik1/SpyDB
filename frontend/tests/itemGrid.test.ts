import assert from 'node:assert/strict'
import { test } from 'node:test'
import { addSeen, fieldOptions, fieldType, formatSize, itemColumns, itemRows, type Item } from '../src/dynamo/itemGrid'

const details = { name: 'orders', partitionKey: 'userId', sortKey: 'createdAt', itemCount: 3, sizeBytes: 100 }
const v = (type: string, text = '') => ({ type, text })

const items: Item[] = [
  { userId: v('S', 'u1'), createdAt: v('N', '1731400000'), total: v('N', '1500'), paid: v('BOOL', 'true'), address: v('M', '{"city":"Delhi"}') },
  { userId: v('S', 'u2'), createdAt: v('N', '1731400001'), total: v('S', 'n/a'), paid: v('BOOL', 'false'), note: v('NULL') },
  { userId: v('S', 'u3'), createdAt: v('N', '1731400002'), Zone: v('S', 'north'), note: v('NULL') },
]

test('the key comes first, then every other field A to Z', () => {
  assert.deepEqual(itemColumns(details, items).map((c) => c.name), ['userId', 'createdAt', 'address', 'note', 'paid', 'total', 'Zone'])
})

test('a field of one type is coloured as it; a mix is plain text; NULL is no type', () => {
  const byName = Object.fromEntries(itemColumns(details, items).map((c) => [c.name, c]))
  assert.deepEqual([byName.userId.type, byName.userId.category, byName.userId.primaryKey], ['string', 'text', true])
  assert.deepEqual([byName.createdAt.type, byName.createdAt.category, byName.createdAt.primaryKey], ['number', 'number', true])
  assert.deepEqual([byName.total.type, byName.total.category], ['number / string', 'text'])
  assert.deepEqual([byName.paid.type, byName.paid.category], ['boolean', 'bool'])
  assert.deepEqual([byName.address.type, byName.address.category], ['map', 'json'])
  assert.deepEqual([byName.note.type, byName.note.category], ['', 'text'])
  assert.equal(byName.total.primaryKey, false)
  assert.ok(itemColumns(details, items).every((c) => c.source === ''), 'every column is read-only')
})

test('an empty table still shows its key columns', () => {
  assert.deepEqual(itemColumns(details, []).map((c) => c.name), ['userId', 'createdAt'])
  assert.deepEqual(itemColumns({ ...details, sortKey: '' }, []).map((c) => c.name), ['userId'])
  assert.deepEqual(itemColumns(null, []), [])
})

test('rows: missing fields are empty, NULL is null, booleans are t / f', () => {
  const columns = itemColumns(details, items)
  assert.deepEqual(itemRows(columns, items), [
    ['u1', '1731400000', '{"city":"Delhi"}', '', 't', '1500', ''],
    ['u2', '1731400001', '', null, 'f', 'n/a', ''],
    ['u3', '1731400002', '', null, '', '', 'north'],
  ])
})

test('a boolean in a mixed column stays true / false', () => {
  const mixed: Item[] = [{ flag: v('BOOL', 'true') }, { flag: v('S', 'yes') }]
  assert.deepEqual(itemRows(itemColumns(null, mixed), mixed), [['true'], ['yes']])
})

test('sizes', () => {
  assert.equal(formatSize(950), '950 B')
  assert.equal(formatSize(3_200_000), '3.2 MB')
  assert.equal(formatSize(45_000_000_000), '45 GB')
  assert.equal(formatSize(0), '0 B')
})

const tmOrders = {
  name: 'tm_orders', partitionKey: 'id', sortKey: '', itemCount: 0, sizeBytes: 0, keyTypes: {},
  indexes: [
    { name: 'sip_type_status', partitionKey: 'sip_type', sortKey: 'sip_status', allFields: true },
    { name: 'useridIndex', partitionKey: 'user_id', sortKey: '', allFields: true },
    { name: 'byUser', partitionKey: 'user_id', sortKey: 'createdAt', allFields: true },
    { name: 'keysOnly', partitionKey: 'pan', sortKey: '', allFields: false },
  ],
}

test('the field list: keys first with whose key, then the rest A to Z', () => {
  const seen = addSeen({}, [{ id: v('S', 'o1'), amount: v('N', '5'), sip_type: v('S', 'SIP'), pan: v('S', 'X'), Zone: v('S', 'n') }])
  const { keys, others } = fieldOptions(tmOrders, seen)
  assert.deepEqual(keys, [
    { field: 'id', note: 'table key' },
    { field: 'sip_type', note: 'sip_type_status' },
    { field: 'sip_status', note: 'sip_type_status, 2nd key' },
    { field: 'user_id', note: 'useridIndex; byUser' },
    { field: 'createdAt', note: 'byUser, 2nd key' },
  ])
  assert.deepEqual(others, ['amount', 'pan', 'Zone'], 'an index keeping only some fields is not offered as a key')
})

test('the field list without table details: just the fields seen', () => {
  assert.deepEqual(fieldOptions(null, addSeen({}, [{ b: v('S', 'x'), a: v('N', '1') }])), { keys: [], others: ['a', 'b'] })
})

test('fields seen only grow: a later page with fewer fields forgets none', () => {
  const first = addSeen({}, [{ note: v('S', 'x'), total: v('N', '1') }])
  const later = addSeen(first, [{ paid: v('BOOL', 'true') }])
  assert.deepEqual(Object.keys(later).sort(), ['note', 'paid', 'total'])
  assert.equal(addSeen(later, [{ paid: v('BOOL', 'false'), note: v('NULL') }]), later, 'nothing new: the same object')
})

test('a field’s type, from what it has held', () => {
  const seen = addSeen({}, [{ n: v('N', '1'), b: v('BOOL', 'true'), mixed: v('N', '1'), s: v('S', 'x') }, { n: v('NULL'), mixed: v('S', 'one'), onlyNull: v('NULL') }])
  assert.equal(fieldType(seen, 'n'), 'N', 'NULL doesn’t count')
  assert.equal(fieldType(seen, 'b'), 'BOOL')
  assert.equal(fieldType(seen, 'mixed'), 'S')
  assert.equal(fieldType(seen, 's'), 'S')
  assert.equal(fieldType(seen, 'onlyNull'), 'S')
  assert.equal(fieldType(seen, 'never seen'), 'S')
  assert.ok('onlyNull' in seen, 'a field only ever NULL is still listed')
})
