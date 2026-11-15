// MongoDB end to end: connect with a connection string, the collections, a
// collection's pages, sorting and filters, editing (a cell, Add field, the
// JSON view, removing a field), delete, the query editor behind </> and in
// a ⌘T tab, the tab menu, ⌘R, ⌘P, switching databases, saved connections,
// and Postgres, DynamoDB and MongoDB connected at once. Only the databases
// spydb_test_e2e (seeded by run.mjs with mongoseed) and spydb_test_e2e_other.
import { execFileSync } from 'node:child_process'
import { check, MOD, sleep } from './cdp.mjs'

export default async function mongo(p, { shots, pg, mongoUrl, root }) {
const { evaluate, click, dblclick, rightClick, key, type, waitFor, shot } = p
const DB = 'spydb_test_e2e'
const helpers = `window.__t = {
  pane: () => [...document.querySelectorAll('.tab-panel')].find((e) => !e.hidden),
  grid: (where) => { const pane = __t.pane(); return where === 'results' ? pane.querySelector('.results-pane:not([hidden]) .grid-wrap, .playground-results .grid-wrap') : pane.querySelector('.rows-pane .grid-wrap') },
  col: (where, name) => [...__t.grid(where).querySelectorAll('thead th')].findIndex((th) => th.querySelector('.grid-col-name')?.innerText === name),
  colType: (where, name) => [...__t.grid(where).querySelectorAll('thead th')].find((th) => th.querySelector('.grid-col-name')?.innerText === name)?.querySelector('.grid-col-type')?.innerText ?? null,
  row: (where, id) => [...__t.grid(where).querySelectorAll('tbody tr')].find((tr) => tr.children[1]?.innerText === id),
  cell: (where, id, name) => __t.row(where, id)?.children[__t.col(where, name)],
  rows: (where) => __t.grid(where)?.querySelectorAll('tbody tr').length ?? -1,
  ids: (where) => [...(__t.grid(where)?.querySelectorAll('tbody tr') ?? [])].map((tr) => tr.children[1]?.innerText),
  bar: (where) => (where === 'results' ? __t.pane().querySelector('.results-pane') : __t.pane()).querySelector('.changes-bar')?.innerText ?? '',
  error: () => __t.pane().querySelector('.changes-error')?.innerText ?? '',
  notice: () => __t.pane().querySelector('.tab-notice, .tab-notice-banner')?.innerText ?? '',
  status: () => __t.pane().querySelector('.rows-pane .grid-status')?.innerText ?? '',
  mongoForm: () => document.querySelector('form[aria-label="MongoDB connection"]'),
}`
const cell = (id, name, where = 'docs') => `__t.cell('${where}', '${id}', '${name}')`
const text = (id, name, where = 'docs') => evaluate(`${cell(id, name, where)}?.innerText`)
const editCell = async (id, name, value, where = 'docs') => { await dblclick(cell(id, name, where)); await key('a', MOD); await type(value); await key('Enter') }
const save = async () => { await key('s', MOD); await sleep(700) }
const setCode = async (selector, value) => { await click(selector); await key('a', MOD); await type(value); await sleep(200) }
const run = async () => { await key('Enter', MOD); await sleep(300) }
// A form field by id, set the way React notices.
const setInput = (id, value) => evaluate(`(() => { const input = document.getElementById(${JSON.stringify(id)}); if (!input) return false; const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set; set.call(input, ${JSON.stringify(value)}); input.dispatchEvent(new Event('input', { bubbles: true })); return true })()`)
const setLabelled = (label, value) => evaluate(`(() => { const input = [...document.querySelectorAll('input')].find((i) => i.closest('label, .field')?.innerText.trim().startsWith(${JSON.stringify(label)})); if (!input) return false; const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set; set.call(input, ${JSON.stringify(value)}); input.dispatchEvent(new Event('input', { bubbles: true })); return true })()`)
const choose = (selector, value) => evaluate(`(() => { const s = ${selector}; s.value = ${JSON.stringify(value)}; s.dispatchEvent(new Event('change', { bubbles: true })) })()`)
const formButton = (label) => `[...__t.mongoForm().querySelectorAll('.btn')].find((b) => b.innerText === '${label}')`
const railButton = (short) => `[...document.querySelectorAll('.rail-engine')].find((b) => b.innerText.trim() === '${short}')`
// Every document of a collection (mongoseed dump: _id → the document as
// canonical JSON, so types count), to see what a save or delete changed.
const snapshot = (collection) => JSON.parse(execFileSync('go', ['run', './e2e/mongoseed', mongoUrl, 'dump', DB, collection], { cwd: root, encoding: 'utf8' }))
const changedIds = (before, after) => [...new Set([...Object.keys(before), ...Object.keys(after)])].filter((k) => before[k] !== after[k]).sort()
const idOf = (n) => `{"_id":{"$numberInt":"${n}"}}`
const sameExcept = (before, ids, what) => {
  const diff = changedIds(before, snapshot('orders'))
  check(`${what}: of 120 orders, only _id ${ids.join(', ')} changed`, JSON.stringify(diff) === JSON.stringify(ids.map(idOf).sort()), diff)
}

await evaluate(helpers)

// 1. The rail has MongoDB; the connections sidebar groups every database's saved connections.
check('the rail: Pg, Dy, Mg', (await evaluate(`[...document.querySelectorAll('.rail-engine')].map((b) => b.innerText.trim()).join(',')`)) === 'Pg,Dy,Mg', null)
check('the sidebar has a PostgreSQL, DynamoDB and MongoDB group', (await evaluate(`[...document.querySelectorAll('.conn-group')].map((e) => e.getAttribute('aria-label')).join(',')`)) === 'PostgreSQL connections,DynamoDB connections,MongoDB connections',
  await evaluate(`[...document.querySelectorAll('.conn-group')].map((e) => e.getAttribute('aria-label'))`))
// A DynamoDB profile clicked from Postgres's screen: the rail goes to Dy and its sign-in shows that profile.
await click(`[...document.querySelectorAll('.conn-group')].find((g) => g.getAttribute('aria-label') === 'DynamoDB connections').querySelector('.conn-item')`)
check('clicking it shows DynamoDB’s sign-in with that profile', await waitFor(`document.querySelector('.rail-engine.active')?.innerText.trim() === 'Dy' && document.getElementById('aws-profile')?.value === 'demo' && !document.querySelector('.aws-card')?.closest('[hidden]')`),
  await evaluate(`[document.querySelector('.rail-engine.active')?.innerText, document.getElementById('aws-profile')?.value]`))
check('no Recent section', !(await evaluate(`document.querySelector('aside[aria-label="Connections"]').innerText.includes('RECENT')`)), null)
await click(railButton('Mg'))
check('Mg lit on the rail; MongoDB’s form; New MongoDB connection', await waitFor(`document.querySelector('.rail-engine.active')?.innerText.trim() === 'Mg' && __t.mongoForm() && document.querySelector('.new-connection-btn')?.innerText.includes('MongoDB')`), null)
await click(`document.querySelector('.rail-theme')`)
check('a theme switch on the rail: light mode applies and is remembered', await waitFor(`document.documentElement.dataset.theme === 'light' && localStorage.getItem('spydb-theme') === 'light' && getComputedStyle(document.body).backgroundColor === 'rgb(246, 246, 248)'`), await evaluate(`[document.documentElement.dataset.theme, getComputedStyle(document.body).backgroundColor]`))
await sleep(400)
await shot(`${shots}/m0-light.png`)
await click(`document.querySelector('.rail-theme')`)
check('…and back to dark', await waitFor(`document.documentElement.dataset.theme === 'dark'`), null)
await shot(`${shots}/m1-connect.png`)

// 2. A connection string: a non-MongoDB one is refused before anything is sent.
await click(`[...document.querySelectorAll('.import-url-btn')].find((b) => b.innerText.includes('connection string'))`)
await setInput('mongo-uri', 'postgres://nope')
await click(formButton('Test'))
check('a non-MongoDB string is refused', await waitFor(`__t.mongoForm().querySelector('.field-error')?.innerText.includes('mongodb://')`), null)
await setInput('mongo-uri', mongoUrl)
await setInput('mongo-database', DB)
await click(formButton('Test'))
check('Test: connection successful', await waitFor(`__t.mongoForm().querySelector('.banner-ok')?.innerText.includes('Connected to MongoDB')`, 15000), await evaluate(`__t.mongoForm().querySelector('.banner')?.innerText`))
await click(formButton('Connect'))
check('connected: the picker shows the database', await waitFor(`document.querySelector('.mongo-sidebar .db-picker-name')?.innerText === '${DB}'`, 15000), null)
check('collections with about how many documents', await waitFor(`[...document.querySelectorAll('.mongo-sidebar .tree-item')].map((b) => b.innerText.replace(/\\s+/g, ' ').trim()).join(',') === 'orders ~120,people ~2'`),
  await evaluate(`[...document.querySelectorAll('.mongo-sidebar .tree-item')].map((b) => b.innerText)`))

// 3. A collection: a page of 50, columns, types, paging, sorting, filters.
await click(`[...document.querySelectorAll('.mongo-sidebar .tree-item')].find((b) => b.innerText.includes('orders'))`)
await evaluate(helpers)
check('orders opens: 50 documents', await waitFor(`__t.rows('docs') === 50`), await evaluate(`__t.rows('docs')`))
check('columns: _id first, then in document order', (await evaluate(`[...__t.grid('docs').querySelectorAll('thead .grid-col-name')].map((e) => e.innerText).join(',')`)) === '_id,status,total,placed,address,note',
  await evaluate(`[...__t.grid('docs').querySelectorAll('thead .grid-col-name')].map((e) => e.innerText)`))
check('header types: int, long, date, object', (await evaluate(`['_id', 'total', 'placed', 'address'].map((c) => __t.colType('docs', c)).join(',')`)) === 'int,long,date,object',
  await evaluate(`['_id', 'total', 'placed', 'address'].map((c) => __t.colType('docs', c))`))
check('a field a document hasn’t got is an empty, hatched cell', await evaluate(`${cell('1', 'note')}.classList.contains('cell-absent')`), null)
check('a date shows as ISO 8601', (await text('1', 'placed')) === '2024-01-02T00:00:00.000Z', await text('1', 'placed'))
await shot(`${shots}/m2-collection.png`)
await click(`__t.pane().querySelector('.pager-btn[aria-label="Next page"]')`)
check('next page: 51–100', await waitFor(`__t.ids('docs')[0] === '51' && __t.rows('docs') === 50`), await evaluate(`__t.ids('docs').slice(0, 2)`))
await click(`__t.pane().querySelector('.pager-btn[aria-label="Next page"]')`)
check('last page: 20, and Next is off', await waitFor(`__t.rows('docs') === 20 && __t.pane().querySelector('.pager-btn[aria-label="Next page"]').disabled`), await evaluate(`__t.rows('docs')`))
const sortBy = `[...__t.grid('docs').querySelectorAll('thead .grid-head')].find((b) => b.querySelector('.grid-col-name')?.innerText === 'total')`
await click(sortBy)
await click(sortBy)
check('sorted by total ↓, back on page 1', await waitFor(`__t.ids('docs')[0] === '120' && __t.status().includes('sorted by total ↓')`), await evaluate(`[__t.ids('docs').slice(0, 3), __t.status()]`))
let before = snapshot('orders')
await editCell('116', 'status', 'SORTED')
await save()
check('an edit while sorted ↓ saves', await waitFor(`__t.bar().includes('Saved 1 document to orders')`), await evaluate(`[__t.bar(), __t.error()]`))
sameExcept(before, [116], 'edit while sorted')
await click(sortBy)
await choose(`__t.pane().querySelector('.filter-bar select[aria-label="Filter field"]')`, 'status')
await click(`__t.pane().querySelector('.filter-bar .filter-value')`)
await type('PAID')
await key('Enter')
check('filter status = PAID: 40', await waitFor(`__t.rows('docs') === 40`), await evaluate(`__t.rows('docs')`))
await choose(`__t.pane().querySelector('.filter-bar select[aria-label="Filter field"]')`, 'total')
await choose(`__t.pane().querySelector('.filter-bar select[aria-label="Filter condition"]')`, 'gte')
await evaluate(`(() => { const input = __t.pane().querySelector('.filter-bar .filter-value'); Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, '11000'); input.dispatchEvent(new Event('input', { bubbles: true })) })()`)
await click(`__t.pane().querySelector('.filter-bar .filter-value')`)
await key('Enter')
check('filter total ≥ 11000, typed as a number: 11', await waitFor(`__t.rows('docs') === 11`), await evaluate(`__t.rows('docs')`))
await click(`[...__t.pane().querySelectorAll('.filter-bar .btn')].find((b) => b.innerText === 'Clear')`)
check('Clear: page 1 again', await waitFor(`__t.rows('docs') === 50 && __t.ids('docs')[0] === '1'`), await evaluate(`__t.rows('docs')`))

// 4. Edit a cell: a long stays a long; ⌘S saves in place.
before = snapshot('orders')
check('120 orders before any edit', Object.keys(before).length === 120, Object.keys(before).length)
await editCell('1', 'total', '150')
check('the edit waits in the changes bar', (await evaluate(`__t.bar()`)).includes('1 unsaved change in 1 document'), await evaluate(`__t.bar()`))
check('the tab shows the unsaved dot', await evaluate(`!!document.querySelector('.tab.active .tab-dirty')`), null)
await save()
check('Saved 1 document to orders', await waitFor(`__t.bar().includes('Saved 1 document to orders')`), await evaluate(`[__t.bar(), __t.error()]`))
check('shown in place; total is still a long', (await text('1', 'total')) === '150' && (await evaluate(`__t.colType('docs', 'total')`)) === 'long',
  [await text('1', 'total'), await evaluate(`__t.colType('docs', 'total')`)])
sameExcept(before, [1], 'one cell saved')
await editCell('2', 'total', 'lots')
await save()
check('text in a whole-number field is refused, saying why', await waitFor(`__t.error().includes('isn’t a whole number')`), await evaluate(`__t.error()`))
await click(`[...__t.pane().querySelectorAll('.changes-bar .btn')].find((b) => b.innerText === 'Discard')`)

// 5. Two documents edited, saved together; no "+ Field" button (the JSON view adds fields).
check('no "+ Field" button', !(await evaluate(`[...__t.pane().querySelectorAll('.grid-pager .btn')].some((b) => b.innerText.includes('Field'))`)), null)
before = snapshot('orders')
await editCell('3', 'total', '301')
await editCell('4', 'total', '401')
await click(`[...__t.pane().querySelectorAll('.changes-bar .btn')].find((b) => b.innerText.includes('Save'))`)
check('Saved 2 documents', await waitFor(`__t.bar().includes('Saved 2 documents to orders')`), await evaluate(`[__t.bar(), __t.error()]`))
sameExcept(before, [3, 4], 'two documents saved together')

// 6. The JSON view: change, add (typed by the JSON), remove; _id can't change.
await dblclick(`__t.row('docs', '5').children[0]`)
await waitFor(`document.querySelector('.json-modal .cm-content')`)
const shown = await evaluate(`document.querySelector('.json-modal .cm-content').innerText`)
check('JSON view: each value as its type', shown.includes('"total": 500') && shown.includes('"$date": "2024-01-06T00:00:00.000Z"') && shown.includes('"_id": 5'), shown.slice(0, 300))
const doc5 = JSON.parse(shown)
delete doc5.address
doc5.status = 'GIFTED'
doc5.tags = ['a', 'b']
doc5.score = 7
await setCode(`document.querySelector('.json-modal .cm-content')`, JSON.stringify(doc5, null, 2))
await click(`[...document.querySelectorAll('.json-modal .btn-primary')].find((b) => b.innerText === 'Apply')`)
check('4 unsaved changes in 1 document', await waitFor(`__t.bar().includes('4 unsaved changes in 1 document')`), await evaluate(`__t.bar()`))
check('fields added in the JSON get columns', await evaluate(`__t.col('docs', 'tags') > 0 && __t.col('docs', 'score') > 0`), null)
await save()
check('JSON changes saved', await waitFor(`__t.bar().includes('Saved 1 document')`), await evaluate(`[__t.bar(), __t.error()]`))
check('address gone, status changed, score an int, tags an array', (await evaluate(`${cell('5', 'address')}.classList.contains('cell-absent')`)) && (await text('5', 'status')) === 'GIFTED'
  && (await evaluate(`__t.colType('docs', 'score')`)) === 'int' && (await evaluate(`__t.colType('docs', 'tags')`)) === 'array',
  [await text('5', 'status'), await evaluate(`__t.colType('docs', 'score')`), await evaluate(`__t.colType('docs', 'tags')`)])
await dblclick(`__t.row('docs', '6').children[0]`)
await waitFor(`document.querySelector('.json-modal .cm-content')`)
const doc6 = JSON.parse(await evaluate(`document.querySelector('.json-modal .cm-content').innerText`))
doc6._id = 600
await setCode(`document.querySelector('.json-modal .cm-content')`, JSON.stringify(doc6, null, 2))
await click(`[...document.querySelectorAll('.json-modal .btn-primary')].find((b) => b.innerText === 'Apply')`)
check('changing _id in the JSON is refused', await waitFor(`document.querySelector('.json-modal-status.error')?.innerText.includes('_id can’t be changed')`), await evaluate(`document.querySelector('.json-modal-status')?.innerText`))
await click(`[...document.querySelectorAll('.json-modal .btn')].find((b) => b.innerText === 'Cancel')`)
await sleep(300)
await click(`[...document.querySelectorAll('.json-modal .btn')].find((b) => b.innerText === 'Discard')`).catch(() => {})

// 7. The cell editor's ✕ takes a field off.
await dblclick(cell('11', 'status'))
await click(`[...__t.pane().querySelectorAll('.cell-editor .cell-null-btn')].find((b) => b.innerText === '✕')`)
await save()
check('✕ removed status from document 11', await waitFor(`${cell('11', 'status')}?.classList.contains('cell-absent') && __t.bar().includes('Saved 1 document')`), await evaluate(`[__t.bar(), __t.error()]`))

// 8. Delete from the row menu.
await rightClick(`__t.row('docs', '7').children[0]`)
const menu = await evaluate(`[...document.querySelectorAll('.row-menu button')].map((b) => b.innerText.trim())`)
check('row menu speaks of documents', menu[0] === 'View document as JSON' && menu.includes('Delete whole document…'), menu)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete whole document…')`)
check('delete asks first, naming the document', await waitFor(`document.querySelector('.delete-rows')?.innerText.includes('Delete the whole document from orders') && document.querySelector('.delete-rows')?.innerText.includes('_id = ')`),
  await evaluate(`document.querySelector('.delete-rows')?.innerText`))
before = snapshot('orders')
await click(`document.querySelector('.delete-rows .btn-danger')`)
check('Deleted 1 document; gone from the page', await waitFor(`__t.bar().includes('Deleted 1 document from orders') && !__t.row('docs', '7')`), await evaluate(`__t.bar()`))
sameExcept(before, [7], 'one document deleted')
check('119 orders remain', Object.keys(snapshot('orders')).length === 119, null)

// 9. The query editor behind </>: find (editable), aggregate, writes, a typo.
// (Of the 40 NEW orders, 7 was deleted above.)
await click(`__t.pane().querySelector('.filter-bar .sql-toggle')`)
check('</> shows the editor with db.orders.find({})', await waitFor(`__t.pane().querySelector('.sql-bar .cm-content')?.innerText.includes('db.orders.find({})')`), null)
await setCode(`__t.pane().querySelector('.sql-bar .cm-content')`, 'db.orders.find({status: "PAID"}).sort({total: -1})')
await run()
check('find: 40 results, editable', await waitFor(`__t.rows('results') === 40 && __t.pane().querySelector('.results-pane .grid-status')?.innerText.includes('editable (orders)')`),
  await evaluate(`[__t.rows('results'), __t.notice()]`))
before = snapshot('orders')
await editCell('120', 'status', 'PAID-X', 'results')
await save()
check('a result edit saves', await waitFor(`${cell('120', 'status', 'results')}?.innerText === 'PAID-X' && __t.bar('results').includes('Saved 1 document')`),
  await evaluate(`[__t.bar('results'), __t.pane().querySelector('.results-pane .changes-error')?.innerText]`))
sameExcept(before, [120], 'query result edit')
await setCode(`__t.pane().querySelector('.sql-bar .cm-content')`, 'db.orders.aggregate([{$group: {_id: "$status", n: {$sum: 1}}}, {$sort: {_id: 1}}])')
await run()
check('aggregate: groups, read-only', await waitFor(`__t.rows('results') >= 3 && __t.pane().querySelector('.results-pane .grid-status')?.innerText.includes('read-only')`), await evaluate(`[__t.rows('results'), __t.notice()]`))
await shot(`${shots}/m3-aggregate.png`)
await setCode(`__t.pane().querySelector('.sql-bar .cm-content')`, 'db.orders.find({status: })')
await run()
check('a typo says where: line 1, column 25', await waitFor(`__t.notice().includes('line 1, column 25')`), await evaluate(`__t.notice()`))
await setCode(`__t.pane().querySelector('.sql-bar .cm-content')`, 'db.orders.updateMany({status: "NEW"}, {$set: {status: "OPEN"}})')
await run()
check('updateMany: ✓ matched 39, changed 39', await waitFor(`__t.notice().includes('Matched 39 documents, changed 39')`), await evaluate(`__t.notice()`))
await setCode(`__t.pane().querySelector('.sql-bar .cm-content')`, 'db.orders.deleteMany({status: "OPEN"})')
await run()
check('deleteMany: ✓ deleted 39', await waitFor(`__t.notice().includes('Deleted 39 documents')`), await evaluate(`__t.notice()`))
await setCode(`__t.pane().querySelector('.sql-bar .cm-content')`, 'db.orders.insertOne({_id: 1000, status: "NEW", total: 5})')
await run()
check('insertOne: ✓ inserted (_id 1000)', await waitFor(`__t.notice().includes('Inserted 1 document (_id 1000)')`), await evaluate(`__t.notice()`))
await setCode(`__t.pane().querySelector('.sql-bar .cm-content')`, 'db.orders.countDocuments({})')
await run()
check('countDocuments: 81 documents', await waitFor(`__t.notice().includes('81 documents')`), await evaluate(`__t.notice()`))
await click(`__t.pane().querySelector('.sql-bar .sql-toggle')`)
check('back to the documents, reloaded: the NEW ones are gone', await waitFor(`__t.rows('docs') === 50 && !__t.ids('docs').includes('1')`), await evaluate(`__t.ids('docs').slice(0, 5)`))

// 10. ⌘T: a query tab; insertMany makes a collection (the sidebar reloads); ⌘R in the collection.
await key('t', MOD)
check('⌘T opens a query tab on orders', await waitFor(`document.querySelector('.tab.active .tab-title')?.innerText.startsWith('Query') && document.querySelector('.tab-panel:not([hidden]) .sql-pane .cm-content')?.innerText.includes('db.orders.find({})')`), null)
await evaluate(helpers)
await setCode(`__t.pane().querySelector('.sql-pane .cm-content')`, 'db.events.insertMany([{kind: "a"}, {kind: "b"}, {kind: "c", at: ISODate("2024-05-01T00:00:00Z")}])')
await run()
check('insertMany: ✓ inserted 3', await waitFor(`__t.notice().includes('Inserted 3 documents')`), await evaluate(`__t.notice()`))
check('the sidebar lists events', await waitFor(`[...document.querySelectorAll('.mongo-sidebar .tree-item')].some((b) => b.innerText.includes('events'))`), null)
await setCode(`__t.pane().querySelector('.sql-pane .cm-content')`, 'db.orders.updateOne({_id: 3}, {$set: {status: "VIA-OTHER-TAB"}})')
await run()
check('updateOne in the query tab', await waitFor(`__t.notice().includes('Matched 1 document, changed 1')`), await evaluate(`__t.notice()`))
await click(`[...document.querySelectorAll('.tab')].find((t) => t.innerText.includes('orders'))`)
await evaluate(helpers)
check('the collection tab still shows the old value', (await text('3', 'status')) === 'PAID', await text('3', 'status'))
await key('r', MOD)
check('⌘R reloads it', await waitFor(`${cell('3', 'status')}?.innerText === 'VIA-OTHER-TAB'`), await text('3', 'status'))

// 11. The tab menu: Duplicate keeps the filters; Close Others; Copy Name.
await choose(`__t.pane().querySelector('.filter-bar select[aria-label="Filter field"]')`, 'status')
await click(`__t.pane().querySelector('.filter-bar .filter-value')`)
await type('SHIPPED')
await key('Enter')
check('filter status = SHIPPED', await waitFor(`__t.rows('docs') > 0 && __t.status().startsWith('1 filter')`), await evaluate(`__t.status()`))
await rightClick(`document.querySelector('.tab.active')`)
const tabMenu = await evaluate(`[...document.querySelectorAll('.row-menu button')].map((b) => b.innerText.trim())`)
check('tab menu: Close … Close All, Duplicate, Copy Name', tabMenu.join(',') === 'Close,Close Others,Close Tabs to Right,Close All,Duplicate,Copy Name', tabMenu)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Duplicate')`)
await evaluate(helpers)
check('Duplicate: a second orders tab, same filter', await waitFor(`[...document.querySelectorAll('.tab')].filter((t) => t.innerText.includes('orders')).length === 2 && __t.status().startsWith('1 filter') && __t.rows('docs') > 0`), await evaluate(`__t.status()`))
await rightClick(`document.querySelector('.tab.active')`)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Close Others')`)
check('Close Others: one tab left', await waitFor(`document.querySelectorAll('.tab').length === 1`), await evaluate(`document.querySelectorAll('.tab').length`))
await rightClick(`document.querySelector('.tab.active')`)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Copy Name')`)
check('Copy Name: orders', await waitFor(`document.querySelector('.copy-toast')?.innerText.includes('orders')`), await evaluate(`document.querySelector('.copy-toast')?.innerText`))

// 12. ⌘P: go to a collection.
await key('p', MOD)
check('⌘P lists the collections', await waitFor(`[...document.querySelectorAll('.palette-item')].map((i) => i.innerText).join(' ').includes('people')`), null)
await type('peo')
await key('Enter')
check('…and opens one', await waitFor(`document.querySelector('.tab.active')?.innerText.includes('people')`), null)
// people's _ids are ObjectIds: an edit and a delete there touch only that document.
await evaluate(helpers)
check('people: 2 documents', await waitFor(`__t.rows('docs') === 2`), await evaluate(`__t.rows('docs')`))
const people = await evaluate(`__t.ids('docs')`)
const peopleBefore = snapshot('people')
await editCell(people[0], 'city', 'Goa')
await save()
check('an ObjectId document saves', await waitFor(`__t.bar().includes('Saved 1 document to people')`), await evaluate(`[__t.bar(), __t.error()]`))
let peopleDiff = changedIds(peopleBefore, snapshot('people'))
check('only that ObjectId document changed', peopleDiff.length === 1 && peopleDiff[0].includes(people[0]) && snapshot('people')[peopleDiff[0]].includes('"city":"Goa"'), peopleDiff)
await rightClick(`__t.row('docs', '${people[1]}').children[0]`)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete whole document…')`)
await waitFor(`document.querySelector('.delete-rows .btn-danger')`)
const peopleMid = snapshot('people')
await click(`document.querySelector('.delete-rows .btn-danger')`)
check('an ObjectId document deletes', await waitFor(`__t.bar().includes('Deleted 1 document from people') && __t.rows('docs') === 1`), await evaluate(`__t.bar()`))
peopleDiff = changedIds(peopleMid, snapshot('people'))
check('only that ObjectId document went', peopleDiff.length === 1 && peopleDiff[0].includes(people[1]) && Object.keys(snapshot('people')).length === 1, peopleDiff)

// 13. Another database on the server: its own (no) tabs; back again.
await click(`document.querySelector('.mongo-sidebar .db-picker-button')`)
check('the picker lists this server’s databases', await waitFor(`[...document.querySelectorAll('.db-menu-item')].some((b) => b.innerText.trim() === '${DB}')`), null)
await type('spydb_test_e2e_other')
await key('Enter')
check('a new database opens, with no tabs', await waitFor(`document.querySelector('.mongo-sidebar .db-picker-name')?.innerText === 'spydb_test_e2e_other' && document.querySelectorAll('.tab').length === 0`), null)
await click(`document.querySelector('.mongo-sidebar .db-picker-button')`)
await waitFor(`[...document.querySelectorAll('.db-menu-item')].some((b) => b.innerText.trim() === '${DB}')`)
await click(`[...document.querySelectorAll('.db-menu-item')].find((b) => b.innerText.trim() === '${DB}')`)
check('back: its tabs come back', await waitFor(`document.querySelectorAll('.tab').length === 2`), await evaluate(`document.querySelectorAll('.tab').length`))

// 14. Postgres, DynamoDB and MongoDB connected at once; switching keeps each.
await click(railButton('Pg'))
check('Pg: Postgres’s connect screen (Mongo stays connected)', await waitFor(`!document.querySelector('.mongo-sidebar') && document.querySelector('aside[aria-label="Connections"]') && document.querySelector('.rail-engine-mongo .rail-engine-dot')`), null)
await setLabelled('Host', 'localhost'); await setLabelled('Port', '5432'); await setLabelled('User', pg.user); await setLabelled('Database', pg.db)
if (pg.password) await setLabelled('Password', pg.password)
await click(`[...document.querySelectorAll('button')].find((b) => b.innerText.trim() === 'Connect')`)
check('Postgres connected too', await waitFor(`!!document.querySelector('.sidebar[aria-label="Tables and views"]')`, 15000), null)
await click(railButton('Dy'))
await waitFor(`document.querySelector('.aws-card .card-footer .btn-primary')`)
await click(`document.querySelector('.aws-card .card-footer .btn-primary')`)
check('DynamoDB signed in too', await waitFor(`!!document.querySelector('.dynamo-sidebar')`, 15000), null)
await click(railButton('Mg'))
check('Mg: still connected, its tabs as they were', await waitFor(`document.querySelector('.mongo-sidebar .db-picker-name')?.innerText === '${DB}' && document.querySelectorAll('.tab').length === 2`), null)
await click(railButton('Pg'))
check('Pg: still connected', await waitFor(`!!document.querySelector('.sidebar[aria-label="Tables and views"]') && !document.querySelector('.mongo-sidebar')`), null)
await click(railButton('Dy'))
check('Dy: still signed in', await waitFor(`!!document.querySelector('.dynamo-sidebar')`), null)
check('every engine shows connected on the rail', (await evaluate(`[...document.querySelectorAll('.rail-engine .rail-engine-dot')].length`)) === 3, await evaluate(`[...document.querySelectorAll('.rail-engine')].map((b) => b.title)`))
await click(railButton('Mg'))
await waitFor(`!!document.querySelector('.mongo-sidebar')`)

// 15. Disconnect: the form says so; Save puts it under MongoDB in the sidebar; double-click reconnects.
await click(`document.querySelector('.mongo-sidebar .sidebar-footer .danger')`)
check('disconnected: the form says so', await waitFor(`__t.mongoForm()?.querySelector('.banner-info')?.innerText.includes('Disconnected')`), null)
check('Postgres and DynamoDB still connected', (await evaluate(`[...document.querySelectorAll('.rail-engine .rail-engine-dot')].length`)) === 2, null)
await setInput('mongo-connection-name', 'E2E local')
await click(formButton('Save'))
const mongoGroup = `[...document.querySelectorAll('.conn-group')].find((g) => g.getAttribute('aria-label') === 'MongoDB connections')`
check('saved, under MongoDB in the sidebar', await waitFor(`[...${mongoGroup}.querySelectorAll('.conn-name')].some((e) => e.innerText === 'E2E local')`), null)
await click(`[...${mongoGroup}.querySelectorAll('.conn-item')].find((e) => e.innerText.includes('E2E local'))`)
check('clicking it shows MongoDB’s form with it', await waitFor(`__t.mongoForm() && document.getElementById('mongo-connection-name')?.value === 'E2E local'`), await evaluate(`document.getElementById('mongo-connection-name')?.value`))
await dblclick(`[...${mongoGroup}.querySelectorAll('.conn-item')].find((e) => e.innerText.includes('E2E local'))`)
check('double-clicking it reconnects, tabs back', await waitFor(`document.querySelectorAll('.tab').length === 2 && !!document.querySelector('.mongo-sidebar')`, 15000), null)
await shot(`${shots}/m4-final.png`)

check('no page errors', p.errors.length === 0, p.errors)
}
