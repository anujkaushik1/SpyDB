import assert from 'node:assert/strict'
import { test } from 'node:test'
import { hidePassword, mongoLabel, mongoTarget, mongoWorkspace } from '../src/mongo/mongoConnection'

const base = { uri: '', host: 'localhost', port: 27017, user: '', database: 'shop' }

test('a connection’s target from its fields', () => {
  assert.deepEqual(mongoTarget(base), { hosts: 'localhost:27017', user: '', database: 'shop' })
  assert.equal(mongoLabel({ ...base, database: '' }), 'localhost:27017')
  assert.equal(mongoLabel({ ...base, user: 'ana' }), 'ana@localhost:27017/shop')
})

test('a connection’s target from its connection string, as Go reads it', () => {
  const srv = { ...base, uri: 'mongodb+srv://ana:p%40ss@cluster0.ab12.mongodb.net/sales?retryWrites=true', database: '' }
  assert.deepEqual(mongoTarget(srv), { hosts: 'cluster0.ab12.mongodb.net', user: 'ana', database: 'sales' })
  assert.equal(mongoTarget({ ...srv, database: 'other' }).database, 'other', 'the form’s Database wins')
  assert.deepEqual(mongoTarget({ ...base, uri: 'mongodb://u@h1:1,h2:2/?replicaSet=rs0', database: '' }), { hosts: 'h1:1,h2:2', user: 'u', database: '' })
  assert.equal(mongoLabel(srv), 'ana@cluster0.ab12.mongodb.net/sales')
})

test('workspaces and hidden passwords', () => {
  assert.equal(mongoWorkspace({ host: 'h:1', user: '', database: 'd' }), 'mongo|@h:1/d')
  assert.equal(hidePassword('mongodb://ana:secret@h/db'), 'mongodb://ana:•••@h/db')
  assert.equal(hidePassword('mongodb+srv://ana@h/db'), 'mongodb+srv://ana@h/db')
  assert.equal(hidePassword('mongodb://h/db'), 'mongodb://h/db')
})
