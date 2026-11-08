import assert from 'node:assert/strict'
import { test } from 'node:test'
import { completionSchema } from '../src/sqlCompletion'
import { splitStatements, statementAt } from '../src/sqlStatements'

const texts = (sql: string) => splitStatements(sql).map((s) => s.text)

test('splitStatements on semicolons', () => {
  assert.deepEqual(texts('select 1; select 2;'), ['select 1;', 'select 2;'])
  assert.deepEqual(texts('select 1;\n\nselect 2'), ['select 1;', 'select 2'])  // last one without ;
  assert.deepEqual(texts('  \n ;; \n'), [])
  assert.deepEqual(texts(''), [])
})

test('splitStatements ignores semicolons in strings, identifiers, comments and $$ bodies', () => {
  assert.deepEqual(texts("select 'a;b', 'it''s; fine'; select 2"), ["select 'a;b', 'it''s; fine';", 'select 2'])
  assert.deepEqual(texts('select "odd;name" from t; select 2'), ['select "odd;name" from t;', 'select 2'])
  assert.deepEqual(texts('select 1 -- not; here\n; select 2'), ['select 1 -- not; here\n;', 'select 2'])
  assert.deepEqual(texts('select /* a; /* nested; */ b; */ 1; select 2'), ['select /* a; /* nested; */ b; */ 1;', 'select 2'])
  assert.deepEqual(texts('create function f() returns int as $$ select 1; $$ language sql; select 2'),
    ['create function f() returns int as $$ select 1; $$ language sql;', 'select 2'])
  assert.deepEqual(texts('do $body$ begin perform 1; end $body$; select $1'), ['do $body$ begin perform 1; end $body$;', 'select $1'])
})

test('positions point at each statement in the original SQL', () => {
  const sql = 'select 1;\n  select 2;'
  const [a, b] = splitStatements(sql)
  assert.equal(sql.slice(a.from, a.to), 'select 1;')
  assert.equal(b.from, sql.indexOf('select 2'))
  assert.equal(sql.slice(b.from, b.to), 'select 2;')
})

test('statementAt picks the statement under (or just before) the cursor', () => {
  const sql = 'select 1;\n\nselect 2;\nselect 3'
  const at = (pos: number) => statementAt(sql, pos)?.text
  assert.equal(at(0), 'select 1;')                           // start
  assert.equal(at(4), 'select 1;')                           // inside
  assert.equal(at(9), 'select 1;')                           // right after its ;
  assert.equal(at(10), 'select 1;')                          // blank line below it
  assert.equal(at(sql.indexOf('select 2') + 3), 'select 2;')
  assert.equal(at(sql.length), 'select 3')                    // end, no ;
  assert.equal(statementAt('  \n', 1), null)
  assert.equal(statementAt('\n\n  select 9', 0)?.text, 'select 9') // before the first: the first
})

test('completionSchema groups columns by schema and table', () => {
  assert.deepEqual(completionSchema([
    { schema: 'public', table: 'accounts', columns: ['id', 'owner'] },
    { schema: 'public', table: 'users', columns: ['id'] },
    { schema: 'zarena', table: 'mandates', columns: null },
  ]), {
    public: { accounts: ['id', 'owner'], users: ['id'] },
    zarena: { mandates: [] },
  })
  assert.deepEqual(completionSchema([]), {})
})

import { completionContext, keywordLabels, statementColumns, statementTables } from '../src/sqlCompletion'

test('completionContext: tables after FROM / JOIN / INTO / UPDATE …', () => {
  assert.equal(completionContext('select * from se'), 'table')
  assert.equal(completionContext('SELECT * FROM '), 'table')
  assert.equal(completionContext('select * from accounts a left join us'), 'table')
  assert.equal(completionContext('insert into acc'), 'table')
  assert.equal(completionContext('update acc'), 'table')
  assert.equal(completionContext('truncate '), 'table')
  assert.equal(completionContext('select * from accounts a, us'), 'table')     // FROM list
  assert.equal(completionContext('select * from accounts as a, users u, em'), 'table')
})

test('completionContext: "name." and everything else', () => {
  assert.equal(completionContext('select accounts.'), 'qualified')
  assert.equal(completionContext('select * from public.acc'), 'qualified')
  assert.equal(completionContext('select a.ow'), 'qualified')
  assert.equal(completionContext('sel'), 'general')
  assert.equal(completionContext('select co'), 'general')
  assert.equal(completionContext('select * from accounts where ow'), 'general')
  assert.equal(completionContext('select * from accounts order by '), 'general')
  assert.equal(completionContext('select 1.5'), 'general')                      // a number, not name.
  assert.equal(completionContext(''), 'general')
})

test('keywordLabels follow the case you type', () => {
  assert.ok(keywordLabels('sel').includes('select'))
  assert.ok(keywordLabels('SEL').includes('SELECT'))
  assert.ok(keywordLabels('Sel').includes('select'))
  assert.ok(keywordLabels('').includes('group by'))
  assert.ok(!keywordLabels('se').includes('security'))                          // no long tail of rare keywords
})

test('statementTables finds tables and aliases', () => {
  assert.deepEqual(statementTables('select * from accounts a join public.users u on u.id = a.id where a.id > 1'), [
    { schema: null, table: 'accounts', alias: 'a' },
    { schema: 'public', table: 'users', alias: 'u' },
  ])
  assert.deepEqual(statementTables('SELECT * FROM Accounts AS acc, zarena.mandates m, "Odd Name"'), [
    { schema: null, table: 'accounts', alias: 'acc' },
    { schema: 'zarena', table: 'mandates', alias: 'm' },
    { schema: null, table: 'Odd Name', alias: null },
  ])
  assert.deepEqual(statementTables('update accounts set owner = 1'), [{ schema: null, table: 'accounts', alias: null }])
  assert.deepEqual(statementTables('insert into accounts (id) values (1)'), [{ schema: null, table: 'accounts', alias: null }])
  assert.deepEqual(statementTables('select * from accounts where id in (select id from users)'),
    [{ schema: null, table: 'accounts', alias: null }, { schema: null, table: 'users', alias: null }])
  assert.deepEqual(statementTables('select * from (select 1) s'), [])
  assert.deepEqual(statementTables('select 1'), [])
})

test('statementColumns: columns of the tables in the query, first table first, no repeats', () => {
  const schema = { public: { accounts: ['id', 'owner', 'balance'], users: ['id', 'email'] }, zarena: { mandates: ['id', 'status'] } }
  assert.deepEqual(statementColumns(schema, statementTables('select * from accounts join users on true')).map((c) => `${c.table}.${c.name}`),
    ['accounts.id', 'accounts.owner', 'accounts.balance', 'users.email'])
  assert.deepEqual(statementColumns(schema, statementTables('select * from mandates')).map((c) => c.name), ['id', 'status']) // found outside public
  assert.deepEqual(statementColumns(schema, statementTables('select * from nope')), [])
})
