import assert from 'node:assert/strict'
import { test } from 'node:test'
import { formatSize, itemColumns, itemRows, type Item } from '../src/dynamo/itemGrid'

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
