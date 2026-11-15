import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fuzzyMatch, fuzzyRank } from '../src/fuzzy'

test('fuzzyMatch: exact, prefix, word start, anywhere, letters in order', () => {
  assert.equal(fuzzyMatch('id', 'id')?.score, 0)
  assert.equal(fuzzyMatch('cre', 'createdAt')?.score, 1)
  assert.equal(fuzzyMatch('at', 'createdAt')?.score, 2) // the capital A starts a word
  assert.equal(fuzzyMatch('ted', 'createdAt')?.score, 3)
  assert.deepEqual(fuzzyMatch('crat', 'created_at')?.positions, [0, 1, 8, 9]) // cr, then the word "at"
  assert.equal(fuzzyMatch('xyz', 'created_at'), null)
  assert.equal(fuzzyMatch('', 'anything')?.score, 0)
  assert.equal(fuzzyMatch('UID', 'user_id')?.positions.join(','), '0,5,6', 'case doesn’t matter')
})

test('fuzzyRank: best first, non-matches left out, ties in the given order', () => {
  const names = ['amount', 'status', 'created_at', 'customer_status', 'city', 'subsequent_sips']
  assert.deepEqual(fuzzyRank('status', names, (n) => n).map((m) => m.item), ['status', 'customer_status'])
  assert.deepEqual(fuzzyRank('sts', names, (n) => n).map((m) => m.item)[0], 'status')
  assert.deepEqual(fuzzyRank('c', names, (n) => n).map((m) => m.item), ['created_at', 'customer_status', 'city'])
  assert.deepEqual(fuzzyRank('', names, (n) => n).map((m) => m.item), names)
})
