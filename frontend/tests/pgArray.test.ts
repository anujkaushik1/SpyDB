import assert from 'node:assert/strict'
import { test } from 'node:test'
import { arrayElement, arrayToJson, jsonToArray } from '../src/pgArray'

test('array types from table tabs and from query results', () => {
  assert.equal(arrayElement('text[]'), 'text')
  assert.equal(arrayElement('character varying(255)[]'), 'text')
  assert.equal(arrayElement('numeric(12,2)[]'), 'number')
  assert.equal(arrayElement('double precision[]'), 'number')
  assert.equal(arrayElement('boolean[]'), 'bool')
  assert.equal(arrayElement('jsonb[]'), 'text')
  assert.equal(arrayElement('_int4'), 'number')
  assert.equal(arrayElement('_text'), 'text')
  assert.equal(arrayElement('_bool'), 'bool')
  assert.equal(arrayElement('text'), null)
  assert.equal(arrayElement('jsonb'), null)
  assert.equal(arrayElement('box[]'), null)
})

test('Postgres text arrays become JSON', () => {
  assert.equal(arrayToJson('{savings,ajs,hashjahsj}', 'text'), '["savings", "ajs", "hashjahsj"]')
  assert.equal(arrayToJson('{"hello world",NULL,"NULL","a\\"b","c\\\\d",""}', 'text'), '["hello world", null, "NULL", "a\\"b", "c\\\\d", ""]')
  assert.equal(arrayToJson('{}', 'text'), '[]')
  assert.equal(arrayToJson('{{a,b},{c,d}}', 'text'), '[["a", "b"], ["c", "d"]]')
})

test('numbers stay exactly as Postgres wrote them; booleans become true / false', () => {
  assert.equal(arrayToJson('{1,2.50,-3,12345678901234567890,NaN,1e+30}', 'number'), '[1, 2.50, -3, 12345678901234567890, "NaN", 1e+30]')
  assert.equal(arrayToJson('{t,f,NULL}', 'bool'), '[true, false, null]')
  // A text[] holding "1" keeps it a string.
  assert.equal(arrayToJson('{1,t}', 'text'), '["1", "t"]')
})

test('values that are not plain arrays are left for the text editor', () => {
  assert.equal(arrayToJson('[0:1]={a,b}', 'text'), null)
  assert.equal(arrayToJson('{a,b', 'text'), null)
  assert.equal(arrayToJson('{a,"b}', 'text'), null)
  assert.equal(arrayToJson('{a,}', 'text'), null)
  assert.equal(arrayToJson('not an array', 'text'), null)
})

test('JSON goes back to an array value, quoted only where Postgres needs it', () => {
  assert.deepEqual(jsonToArray('["savings", "ajs"]', 'text'), { value: '{savings,ajs}' })
  assert.deepEqual(jsonToArray('["hello world", null, "NULL", "", "a\\"b", "c\\\\d", "{x}", "1,2"]', 'text'),
    { value: '{"hello world",NULL,"NULL","","a\\"b","c\\\\d","{x}","1,2"}' })
  assert.deepEqual(jsonToArray('[]', 'text'), { value: '{}' })
  assert.deepEqual(jsonToArray('[[1, 2], [3, 4]]', 'number'), { value: '{{1,2},{3,4}}' })
  assert.deepEqual(jsonToArray('[12345678901234567890, 2.50]', 'number'), { value: '{12345678901234567890,2.50}' })
  assert.deepEqual(jsonToArray('[true, false]', 'bool'), { value: '{t,f}' })
  assert.deepEqual(jsonToArray('[true, 7]', 'text'), { value: '{true,7}' })
  assert.deepEqual(jsonToArray('[{"a": 12345678901234567890}]', 'text'), { value: '{"{\\"a\\":12345678901234567890}"}' })
})

test('a round trip gives back what Postgres printed', () => {
  for (const [value, element] of [
    ['{savings,ajs,"hello world",NULL,"NULL"}', 'text'],
    ['{1,2.50,-3}', 'number'],
    ['{t,f}', 'bool'],
    ['{{a,b},{c,d}}', 'text'],
    ['{"a\\"b","c\\\\d",""}', 'text'],
  ] as const) {
    assert.deepEqual(jsonToArray(arrayToJson(value, element)!, element), { value }, value)
  }
})

test('what the editor can not turn into an array is explained', () => {
  assert.match((jsonToArray('{"a": 1}', 'text') as { error: string }).error, /JSON array/)
  assert.match((jsonToArray('"text"', 'text') as { error: string }).error, /JSON array/)
  assert.match((jsonToArray('[1, [2]]', 'number') as { error: string }).error, /every item at that level/)
  assert.match((jsonToArray('[[1, 2], [3]]', 'number') as { error: string }).error, /same length/)
  assert.match((jsonToArray('[1,', 'number') as { error: string }).error, /Not valid JSON/)
})
