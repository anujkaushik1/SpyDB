import assert from 'node:assert/strict'
import { test } from 'node:test'
import { missingRequired, nameProblem, suggestTargets, type TargetColumn } from '../src/importMapping'

const columns: TargetColumn[] = [
  { name: 'id', type: 'integer', required: false, generated: true },
  { name: 'full_name', type: 'text', required: true, generated: false },
  { name: 'balance', type: 'numeric', required: false, generated: false },
  { name: 'joined_on', type: 'date', required: true, generated: false },
]

test('file columns match table columns ignoring case, spaces and punctuation', () => {
  assert.deepEqual(suggestTargets(['Full Name', 'BALANCE', 'notes', 'Joined-On'], columns), ['full_name', 'balance', '', 'joined_on'])
})

test('a generated column is never suggested, and each column is used once', () => {
  assert.deepEqual(suggestTargets(['ID', 'balance', 'Balance'], columns), ['', 'balance', ''])
})

test('no header: nothing is guessed', () => {
  assert.deepEqual(suggestTargets(['', ''], columns), ['', ''])
})

test('required columns nobody fills are listed', () => {
  assert.deepEqual(missingRequired(['full_name', '', 'balance'], columns), ['joined_on'])
  assert.deepEqual(missingRequired(['full_name', 'joined_on'], columns), [])
})

test('new table names must be filled in and different', () => {
  assert.equal(nameProblem(['a', 'b']), null)
  assert.match(nameProblem(['a', ' ']) ?? '', /needs a name/)
  assert.match(nameProblem(['a', 'a']) ?? '', /both named/)
  assert.match(nameProblem([]) ?? '', /at least one/)
})
