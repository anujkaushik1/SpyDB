import assert from 'node:assert/strict'
import { test } from 'node:test'
import { filterQueries, suggestName, upsertQuery } from '../src/savedQueries'
import { timeAgo } from '../src/timeAgo'

const q = (id: string, name: string, sql: string, database: string, updatedAt: number) => ({ id, name, sql, database, updatedAt })
const phone = q('a', 'Telephone lookup', "SELECT * FROM arn WHERE data->>'Telephone (O)' LIKE '%5787'", 'cashier', 300)
const top = q('b', 'Top accounts', 'SELECT * FROM accounts ORDER BY balance DESC', 'anuj', 200)
const failed = q('c', 'Failed debits', "SELECT * FROM debit_requests WHERE status = 'FAILED'", 'cashier', 100)
const all = [phone, top, failed]
const ids = (xs: { id: string }[]) => xs.map((x) => x.id).join(',')

test('filterQueries matches name, SQL and database, ignoring case', () => {
  assert.equal(ids(filterQueries(all, '')), 'a,b,c')
  assert.equal(ids(filterQueries(all, '  ')), 'a,b,c')
  assert.equal(ids(filterQueries(all, 'TOP')), 'b')            // name
  assert.equal(ids(filterQueries(all, 'debit_requests')), 'c') // SQL
  assert.equal(ids(filterQueries(all, 'cashier')), 'a,c')      // database label
  assert.equal(ids(filterQueries(all, 'zzz')), '')
})

test('suggestName uses the first real line of SQL, shortened', () => {
  assert.equal(suggestName('SELECT * FROM accounts', 'Query 1'), 'SELECT * FROM accounts')
  assert.equal(suggestName('\n  -- find phones\nSELECT  *\n  FROM arn', 'Query 1'), 'SELECT *')
  assert.equal(suggestName('   \n\n', 'Query 3'), 'Query 3')
  const long = suggestName('SELECT id, owner, balance, updated_at, tags, profile FROM accounts WHERE id > 10', 'x')
  assert.equal(long.length, 48)
  assert.ok(long.endsWith('…'))
})

test('upsertQuery adds or replaces, newest first', () => {
  assert.equal(ids(upsertQuery([top, failed], phone)), 'a,b,c')                          // new, newest
  assert.equal(ids(upsertQuery(all, { ...failed, updatedAt: 400 })), 'c,a,b')           // re-saved goes to the top
  assert.equal(upsertQuery(all, { ...top, name: 'Richest' }).find((x) => x.id === 'b')?.name, 'Richest')
  assert.equal(upsertQuery(all, phone).length, 3)                                         // no duplicate
})

test('timeAgo', () => {
  const now = 10 * 365 * 24 * 3600 * 1000
  assert.equal(timeAgo(0, now), 'never')
  assert.equal(timeAgo(now - 40 * 1000, now), 'just now')
  assert.equal(timeAgo(now - 60 * 1000, now), '1 minute ago')
  assert.equal(timeAgo(now - 5 * 60 * 1000, now), '5 minutes ago')
  assert.equal(timeAgo(now - 3 * 3600 * 1000, now), '3 hours ago')
  assert.equal(timeAgo(now - 2 * 24 * 3600 * 1000, now), '2 days ago')
  assert.equal(timeAgo(now - 400 * 24 * 3600 * 1000, now), '1 year ago')
})
