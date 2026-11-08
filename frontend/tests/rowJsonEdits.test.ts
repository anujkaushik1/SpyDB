import assert from 'node:assert/strict'
import { test } from 'node:test'
import { LosslessNumber, parse, stringify } from 'lossless-json'
import { rowsAsJson } from '../src/copyFormats'
import { pretty } from '../src/jsonText'
import { rowJsonEdits } from '../src/rowJsonEdits'

// Like an accounts table tab: what DataGrid passes in.
const columns = [
  { name: 'id', type: 'int4', category: 'number', primaryKey: true },
  { name: 'owner', type: 'text', category: 'text', primaryKey: false },
  { name: 'balance', type: 'numeric(12,2)', category: 'number', primaryKey: false },
  { name: 'active', type: 'bool', category: 'bool', primaryKey: false },
  { name: 'updated_at', type: 'timestamptz', category: 'time', primaryKey: false },
  { name: 'tags', type: 'jsonb', category: 'json', primaryKey: false },
  { name: 'profile', type: 'jsonb', category: 'json', primaryKey: false },
  { name: 'labels', type: 'text[]', category: 'other', primaryKey: false },
]
const alice = ['1', 'alice', '1000.00', 't', '2026-10-31 02:39:10.038401+05:30', '["savings"]',
  '{"kyc": {"status": "ok"}, "n": 12345678901234567890}', '{a,"b c"}']
const bob = ['2', 'bob', '5.50', 'f', '2026-01-01 00:00:00+05:30', null, null, null]
const all = () => true

// What the modal shows: rowsAsJson, indented by pretty().
const shown = (rows: (string | null)[][], cols = columns) => pretty(rowsAsJson(cols, rows))
// Edit the shown JSON like a user would, then hand it back as text.
function edited(rows: (string | null)[][], change: (data: any) => void, cols = columns) {
  const data = parse(shown(rows, cols)) as any
  change(data)
  return stringify(data, null, 2) as string
}
const run = (rows: (string | null)[][], text: string, editable: (col: number) => boolean = all, cols = columns) =>
  rowJsonEdits(cols, rows, text, editable)

test('untouched JSON makes no edits (every type round-trips)', () => {
  assert.deepEqual(run([alice], shown([alice])), { edits: [] })
  assert.deepEqual(run([alice], rowsAsJson(columns, [alice])), { edits: [] }) // compact form too
  assert.deepEqual(run([alice, bob], shown([alice, bob])), { edits: [] })
})

test('ids beyond 2^53 and NULLs round-trip without false edits', () => {
  const big = ['9007199254740993', 'x', '0.10', null, null, 'null', '{"n": 123456789012345678901234567890}', '{}']
  assert.deepEqual(run([big], shown([big])), { edits: [] })
})

test('changing text, number, boolean and timestamp gives one edit each', () => {
  const text = edited([alice], (d) => {
    d.owner = 'alice b'
    d.balance = new LosslessNumber('12345678901.25')
    d.active = false
    d.updated_at = '2026-11-08 10:00:00+05:30'
  })
  assert.deepEqual(run([alice], text), {
    edits: [
      { row: 0, col: 1, value: 'alice b' },
      { row: 0, col: 2, value: '12345678901.25' },
      { row: 0, col: 3, value: 'false' },
      { row: 0, col: 4, value: '2026-11-08 10:00:00+05:30' },
    ],
  })
})

test('jsonb: a nested change saves the whole value, big numbers exact', () => {
  const text = edited([alice], (d) => { d.profile.kyc.status = 'done' })
  assert.deepEqual(run([alice], text), {
    edits: [{ row: 0, col: 6, value: '{"kyc":{"status":"done"},"n":12345678901234567890}' }],
  })
})

test('jsonb: replacing a NULL value and appending to an array', () => {
  const text = edited([bob], (d) => {
    d.tags = ['new']
    d.profile = { city: 'Pune' }
  })
  assert.deepEqual(run([bob], text), {
    edits: [{ row: 0, col: 5, value: '["new"]' }, { row: 0, col: 6, value: '{"city":"Pune"}' }],
  })
  const appended = edited([alice], (d) => { d.tags.push('nri') })
  assert.deepEqual(run([alice], appended), { edits: [{ row: 0, col: 5, value: '["savings","nri"]' }] })
})

test('null clears a value (SQL NULL), for json columns too', () => {
  const text = edited([alice], (d) => {
    d.owner = null
    d.profile = null
  })
  assert.deepEqual(run([alice], text), { edits: [{ row: 0, col: 1, value: null }, { row: 0, col: 6, value: null }] })
})

test('Postgres arrays: a JSON array becomes an array literal with quoting', () => {
  const text = edited([alice], (d) => { d.labels = ['x', 'say "hi"', 'back\\slash', null, ['n', 'm'], new LosslessNumber('7')] })
  assert.deepEqual(run([alice], text), {
    edits: [{ row: 0, col: 7, value: '{"x","say \\"hi\\"","back\\\\slash",NULL,{"n","m"},"7"}' }],
  })
  // Query results name array types "_int4".
  const cols = [{ name: 'nums', type: '_int4', category: 'other', primaryKey: false }]
  const nums = edited([['{1,2}']], (d) => { d.nums = [new LosslessNumber('1'), new LosslessNumber('3')] }, cols)
  assert.deepEqual(run([['{1,2}']], nums, all, cols), { edits: [{ row: 0, col: 0, value: '{"1","3"}' }] })
  // Or keep editing the literal as a string.
  const asText = edited([alice], (d) => { d.labels = '{a,b}' })
  assert.deepEqual(run([alice], asText), { edits: [{ row: 0, col: 7, value: '{a,b}' }] })
})

test('a string for a number column is passed on for Postgres to check', () => {
  const text = edited([alice], (d) => { d.balance = '1500' })
  assert.deepEqual(run([alice], text), { edits: [{ row: 0, col: 2, value: '1500' }] })
})

test('several rows: edits point at the right row', () => {
  const text = edited([alice, bob], (d) => {
    d[1].owner = 'robert'
    d[0].active = false
  })
  assert.deepEqual(run([alice, bob], text), {
    edits: [{ row: 0, col: 3, value: 'false' }, { row: 1, col: 1, value: 'robert' }],
  })
})

test('duplicate column names map back through id, id_2', () => {
  const cols = [
    { name: 'id', type: 'int4', category: 'number', primaryKey: true },
    { name: 'id', type: 'int4', category: 'number', primaryKey: false },
  ]
  const text = edited([['1', '10']], (d) => { d.id_2 = new LosslessNumber('11') }, cols)
  assert.deepEqual(run([['1', '10']], text, all, cols), { edits: [{ row: 0, col: 1, value: '11' }] })
})

test('rule: keys can’t be removed', () => {
  const text = edited([alice], (d) => { delete d.owner })
  assert.deepEqual(run([alice], text), { error: '"owner" is missing. Keys can’t be removed; set it to null to clear the value.' })
})

test('rule: keys can’t be added', () => {
  const text = edited([alice], (d) => { d.city = 'Pune' })
  assert.deepEqual(run([alice], text), { error: '"city" isn’t a column here. Keys can’t be added.' })
})

test('rule: the primary key can’t change', () => {
  const text = edited([alice], (d) => { d.id = new LosslessNumber('99') })
  assert.deepEqual(run([alice], text), { error: '"id" is the primary key and can’t be changed here.' })
})

test('rule: columns that can’t be saved are refused, but may stay as they are', () => {
  const readOnlyProfile = (col: number) => col !== 6
  assert.deepEqual(run([alice], shown([alice]), readOnlyProfile), { edits: [] })
  const text = edited([alice], (d) => { d.profile.n = 1 })
  assert.deepEqual(run([alice], text, readOnlyProfile), {
    error: '"profile" can’t be saved: it isn’t a column of the table being edited.',
  })
})

test('rule: values must fit their column', () => {
  assert.deepEqual(run([alice], edited([alice], (d) => { d.owner = { first: 'a' } })), {
    error: '"owner" is text, so it can’t hold an object.',
  })
  assert.deepEqual(run([alice], edited([alice], (d) => { d.owner = ['a'] })), {
    error: '"owner" is text, so it can’t hold an array.',
  })
  assert.deepEqual(run([alice], edited([alice], (d) => { d.labels = [{ a: 1 }] })), {
    error: '"labels" can’t hold objects inside an array.',
  })
})

test('rule: the shape must match (one object, or one per row)', () => {
  assert.deepEqual(run([alice], '[]'), { error: 'Expected one JSON object for the row.' })
  assert.deepEqual(run([alice, bob], shown([alice])), { error: 'Expected an array of 2 row objects.' })
  const dropped = edited([alice, bob], (d) => { d.pop() })
  assert.deepEqual(run([alice, bob], dropped), { error: 'Rows can’t be added or removed here: expected 2, found 1.' })
  const notObject = edited([alice, bob], (d) => { d[1] = 5 })
  assert.deepEqual(run([alice, bob], notObject), { error: 'Row 2: each row must be a JSON object.' })
  const missing = edited([alice, bob], (d) => { delete d[1].active })
  assert.match((run([alice, bob], missing) as { error: string }).error, /^Row 2: "active" is missing/)
})

test('invalid JSON is reported, nothing else happens', () => {
  const result = run([alice], '{"id": 1,')
  assert.ok('error' in result && result.error.startsWith('Not valid JSON:'), JSON.stringify(result))
})
