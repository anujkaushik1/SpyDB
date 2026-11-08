import assert from 'node:assert/strict'
import { test } from 'node:test'
import { cellClass, cellText } from '../src/cellValues'

test('cellText shows Postgres booleans as true / false', () => {
  assert.equal(cellText('t', 'bool'), 'true')
  assert.equal(cellText('f', 'bool'), 'false')
  assert.equal(cellText('maybe', 'bool'), 'maybe')
  assert.equal(cellText('t', 'text'), 't')
})

test('cellClass picks the colour class', () => {
  assert.equal(cellClass(null, 'text'), 'cell-null')
  assert.equal(cellClass('t', 'bool'), 'cell-bool-true')
  assert.equal(cellClass('f', 'bool'), 'cell-bool-false')
  assert.equal(cellClass('5', 'number'), 'cell-number')
  assert.equal(cellClass('{}', 'json'), 'cell-json')
})
