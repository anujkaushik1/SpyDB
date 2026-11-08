import assert from 'node:assert/strict'
import { test } from 'node:test'
import { formatRows, formatValue, rowsAsJson } from '../src/copyFormats'

const columns = [
  { name: 'id', category: 'number' },
  { name: 'name', category: 'text' },
  { name: 'amount', category: 'number' },
  { name: 'flag', category: 'bool' },
  { name: 'meta', category: 'json' },
  { name: 'note', category: 'text' },
]
const alice = ['1', 'alice', '1000.00', 't', '{"tags": [1, 2], "tier": "gold"}', 'plain']
const bob = ['2', 'bob, jr', '-42.50', 'f', null, 'say "hi"']
const carol = ['3', 'carol', null, null, '[]', 'line1\nline2']
const big = ['9007199254740993', 'tab\there', '0.10', 't', '{"n": 12345678901234567890}', 'a | pipe']

test('JSON: exact numbers, real booleans and nulls, json embedded', () => {
  assert.equal(formatRows('json', columns, [alice, carol]), [
    '[',
    '  {',
    '    "id": 1,',
    '    "name": "alice",',
    '    "amount": 1000.00,',
    '    "flag": true,',
    '    "meta": {"tags": [1, 2], "tier": "gold"},',
    '    "note": "plain"',
    '  },',
    '  {',
    '    "id": 3,',
    '    "name": "carol",',
    '    "amount": null,',
    '    "flag": null,',
    '    "meta": [],',
    '    "note": "line1\\nline2"',
    '  }',
    ']',
  ].join('\n'))
})

test('JSON: ids beyond 2^53 and big numbers inside json stay exact', () => {
  const text = formatRows('json', columns, [big])
  assert.match(text, /"id": 9007199254740993,/)
  assert.match(text, /"meta": \{"n": 12345678901234567890\}/)
})

test('JSON: no rows is an empty array; invalid json becomes a string', () => {
  assert.equal(formatRows('json', columns, []), '[]')
  const text = formatRows('json', [{ name: 'j', category: 'json' }], [['not json']])
  assert.match(text, /"j": "not json"/)
})

test('JSON: duplicate column names get _2, _3', () => {
  const dup = [{ name: 'id', category: 'number' }, { name: 'id', category: 'number' }, { name: 'id', category: 'text' }]
  assert.equal(formatRows('json', dup, [['1', '2', 'x']]), '[\n  {\n    "id": 1,\n    "id_2": 2,\n    "id_3": "x"\n  }\n]')
})

test('CSV: header, quoting, NULL as empty, booleans as shown', () => {
  assert.equal(formatRows('csv', columns, [bob, carol]), [
    'id,name,amount,flag,meta,note',
    '2,"bob, jr",-42.50,false,,"say ""hi"""',
    '3,carol,,,[],"line1\nline2"',
  ].join('\n'))
})

test('Text: tab-separated, quoting tabs and quotes', () => {
  assert.equal(formatRows('text', columns, [big]), [
    'id\tname\tamount\tflag\tmeta\tnote',
    '9007199254740993\t"tab\there"\t0.10\ttrue\t"{""n"": 12345678901234567890}"\ta | pipe',
  ].join('\n'))
})

test('Markdown: pipes escaped, line breaks as <br>, NULL shown', () => {
  assert.equal(formatRows('markdown', columns, [carol, big]), [
    '| id | name | amount | flag | meta | note |',
    '| --- | --- | --- | --- | --- | --- |',
    '| 3 | carol | NULL | NULL | [] | line1<br>line2 |',
    '| 9007199254740993 | tab\there | 0.10 | true | {"n": 12345678901234567890} | a \\| pipe |',
  ].join('\n'))
})

test('rowsAsJson: one row is an object, several are an array', () => {
  assert.equal(rowsAsJson(columns.slice(0, 2), [['1', 'alice']]), '{\n  "id": 1,\n  "name": "alice"\n}')
  assert.equal(rowsAsJson(columns.slice(0, 2), [['1', 'alice'], ['2', 'bob']]),
    '[\n  {\n    "id": 1,\n    "name": "alice"\n  },\n  {\n    "id": 2,\n    "name": "bob"\n  }\n]')
})

test('formatValue: what a single cell copies', () => {
  assert.equal(formatValue(null, 'text'), '')
  assert.equal(formatValue('t', 'bool'), 'true')
  assert.equal(formatValue('a,b', 'text'), 'a,b')
})
