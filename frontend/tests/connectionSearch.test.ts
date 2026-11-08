import assert from 'node:assert/strict'
import { test } from 'node:test'
import { connectionDetail, connectionTitle, pickerSections, sameTarget, targetKey } from '../src/connectionSearch'

const conn = (id: string, name: string, host: string, database: string, user = 'anuj', port = 5432) =>
  ({ id, name, config: { host, port, user, database, password: 'secret' } })

const prod = conn('s1', 'Prod cashier', 'zf-cashier.rds.amazonaws.com', 'cashier', 'zf_admin')
const local = conn('s2', 'Local', 'localhost', 'anuj')
const recentProd = conn('r1', '', 'zf-cashier.rds.amazonaws.com', 'cashier', 'zf_admin') // same target as prod
const recentShop = conn('r2', '', 'localhost', 'shop')
const lists = { saved: [prod, local], recent: [recentProd, recentShop] }
const ids = (cs: { id: string }[]) => cs.map((c) => c.id)

test('sameTarget: host, port, user and database; not the password', () => {
  assert.ok(sameTarget(prod.config, { ...recentProd.config, password: 'other' } as typeof prod.config))
  assert.ok(!sameTarget(prod.config, { ...prod.config, port: 5433 }))
  assert.ok(!sameTarget(prod.config, { ...prod.config, database: 'postgres' }))
  assert.ok(!sameTarget(prod.config, { ...prod.config, user: 'reader' }))
  assert.ok(!sameTarget(local.config, { ...local.config, host: 'LOCALHOST' })) // exact, like the Go side
})

test('titles and details', () => {
  assert.equal(connectionTitle(prod), 'Prod cashier')
  assert.equal(connectionTitle(recentShop), 'anuj@localhost')
  assert.equal(connectionTitle(conn('x', '   ', 'h', 'd', 'u')), 'u@h')
  assert.equal(connectionDetail(prod), 'zf-cashier.rds.amazonaws.com:5432 · cashier')
})

test('no search: everything, with a recent duplicate of a saved one hidden', () => {
  const s = pickerSections(['anuj', 'postgres', 'shop'], lists, '')
  assert.deepEqual(s.databases, ['anuj', 'postgres', 'shop'])
  assert.deepEqual(ids(s.saved), ['s1', 's2'])
  assert.deepEqual(ids(s.recent), ['r2'])
})

test('search matches database names and any part of a connection', () => {
  assert.deepEqual(pickerSections(['anuj', 'postgres', 'shop'], lists, 'SHO').databases, ['shop'])
  assert.deepEqual(ids(pickerSections([], lists, 'shop').recent), ['r2'])         // database of a recent one
  assert.deepEqual(ids(pickerSections([], lists, 'prod').saved), ['s1'])          // name
  assert.deepEqual(ids(pickerSections([], lists, 'amazonaws').saved), ['s1'])     // host
  assert.deepEqual(ids(pickerSections([], lists, 'localhost:5432').saved), ['s2']) // host:port
  assert.deepEqual(ids(pickerSections([], lists, 'zf_admin').saved), ['s1'])      // user
  assert.deepEqual(ids(pickerSections([], lists, '  cashier ').saved), ['s1'])    // trimmed
})

test('nothing matches, and missing lists', () => {
  const s = pickerSections(['anuj'], lists, 'zzz')
  assert.deepEqual([s.databases, s.saved, s.recent], [[], [], []])
  const empty = pickerSections(['anuj'], null, '')
  assert.deepEqual([empty.databases, empty.saved, empty.recent], [['anuj'], [], []])
  const nulls = pickerSections([], { saved: null, recent: null }, '')
  assert.deepEqual([nulls.saved, nulls.recent], [[], []])
})

test('targetKey names one workspace per server, login and database', () => {
  assert.equal(targetKey(prod.config), 'zf_admin@zf-cashier.rds.amazonaws.com:5432/cashier')
  assert.equal(targetKey(prod.config), targetKey(recentProd.config)) // same target, same tabs
  assert.notEqual(targetKey(local.config), targetKey({ ...local.config, database: 'shop' }))
  assert.notEqual(targetKey(local.config), targetKey({ ...local.config, port: 5433 }))
  assert.notEqual(targetKey(local.config), targetKey({ ...local.config, user: 'reader' }))
})
