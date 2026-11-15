import assert from 'node:assert/strict'
import { test } from 'node:test'
import { collectionRef, commandAt, commandToRun, splitCommands, suggestions } from '../src/mongo/shellText'

const text = `db.orders.find({status: "PAID"})
  .sort({total: -1})

// one more; db.not.this()
db.orders.aggregate([
  {$match: {note: 'a;b db.x'}},
]);  db.people.countDocuments({})
`

test('commands start with db on a line or after ;, and run over lines', () => {
  assert.deepEqual(splitCommands(text).map((c) => c.text), [
    'db.orders.find({status: "PAID"})\n  .sort({total: -1})\n\n// one more; db.not.this()',
    'db.orders.aggregate([\n  {$match: {note: \'a;b db.x\'}},\n])',
    'db.people.countDocuments({})',
  ])
})

test('the command at the cursor', () => {
  assert.equal(commandAt(text, 0)?.text.split('\n')[0], 'db.orders.find({status: "PAID"})')
  assert.equal(commandAt(text, text.indexOf('$match'))?.text.split('\n')[0], 'db.orders.aggregate([')
  assert.equal(commandAt(text, text.length)?.text, 'db.people.countDocuments({})')
  assert.equal(commandAt('', 0), null)
})

test('Run: the selection, the only command, or the one at the cursor', () => {
  assert.equal(commandToRun(text, 3, 9)?.text, 'orders')
  assert.equal(commandToRun('db.a.find()\n', 0, 0)?.text, 'db.a.find()\n')
  assert.equal(commandToRun(text, text.indexOf('countDocuments'), text.indexOf('countDocuments'))?.text, 'db.people.countDocuments({})')
  assert.equal(commandToRun('   ', 0, 0), null)
})

test('suggestions fit where the cursor is', () => {
  const names = (r: ReturnType<typeof suggestions>) => r?.options.map((o) => o.label) ?? null
  assert.deepEqual(names(suggestions('db.', ['orders', 'people'], [])), ['orders', 'people'])
  assert.deepEqual(suggestions('db.ord', ['orders'], [])?.from, 3)
  assert.ok(names(suggestions('db.orders.fi', [], []))!.includes('find'))
  assert.ok(names(suggestions('db.system.views.', [], []))!.includes('aggregate'))
  assert.ok(names(suggestions('db["a-b"].', [], []))!.includes('insertOne'))
  assert.deepEqual(names(suggestions('db.orders.find({}).', [], [])), ['sort', 'limit', 'skip', 'projection'])
  assert.ok(names(suggestions('db.orders.find({total: {$g', [], []))!.includes('$gte'))
  assert.ok(names(suggestions('db.orders.find({st', [], ['status']))!.includes('status'))
  assert.equal(suggestions('db.orders.find({', [], ['status']), null, 'nothing typed: nothing to suggest')
})

test('collections in commands', () => {
  assert.equal(collectionRef('orders'), 'db.orders')
  assert.equal(collectionRef('order-items'), 'db.getCollection("order-items")')
  assert.equal(collectionRef('system.views'), 'db.getCollection("system.views")')
})
