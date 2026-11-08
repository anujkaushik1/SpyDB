import assert from 'node:assert/strict'
import { test } from 'node:test'
import { normalize, pretty } from '../src/jsonText'

test('pretty indents Postgres one-line jsonb and keeps numbers exact', () => {
  assert.equal(
    pretty('{"a": 1, "price": 1000.00, "n": 12345678901234567890, "tags": ["x"]}'),
    '{\n  "a": 1,\n  "price": 1000.00,\n  "n": 12345678901234567890,\n  "tags": [\n    "x"\n  ]\n}',
  )
})

test('pretty: NULL is empty, invalid JSON comes back unchanged', () => {
  assert.equal(pretty(null), '')
  assert.equal(pretty('not json {'), 'not json {')
})

test('normalize compacts and keeps numbers exact', () => {
  assert.equal(normalize('{ "a" : [1, 2], "big": 12345678901234567890, "p": 1.50 }'), '{"a":[1,2],"big":12345678901234567890,"p":1.50}')
  assert.equal(normalize('"text"'), '"text"')
  assert.equal(normalize('null'), 'null')
})

test('normalize throws on invalid JSON', () => {
  assert.throws(() => normalize('{"a": }'))
  assert.throws(() => normalize(''))
})
