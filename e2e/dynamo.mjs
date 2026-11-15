// DynamoDB end to end: sign in, edit cells, add fields, the JSON view,
// deletes, PartiQL (</> and ⌘T / Ctrl+T tabs), the tab menu, and switching to
// Postgres and back with both staying connected. DynamoDB Local's made-up
// tables (dynamo-local.mjs) and the test Postgres; run by run.mjs.
import { check, MOD, sleep } from './cdp.mjs'
import { ddb, getItem } from './dynamo-local.mjs'

export default async function dynamo(p, { shots, pg }) {
const { evaluate, click, dblclick, rightClick, key, type, waitFor, shot } = p

// Page helpers: the shown tab, a grid's cells by item key and column name.
const helpers = `window.__t = {
  pane: () => [...document.querySelectorAll('.tab-panel')].find((e) => !e.hidden),
  grid: (where) => { const pane = __t.pane(); return where === 'results' ? pane.querySelector('.results-pane:not([hidden]) .grid-wrap, .playground-results .grid-wrap') : pane.querySelector('.rows-pane .grid-wrap') },
  col: (where, name) => [...__t.grid(where).querySelectorAll('thead th')].findIndex((th) => th.querySelector('.grid-col-name')?.innerText === name),
  row: (where, key) => [...__t.grid(where).querySelectorAll('tbody tr')].find((tr) => tr.children[1]?.innerText === key),
  cell: (where, key, name) => __t.row(where, key)?.children[__t.col(where, name)],
  rows: (where) => __t.grid(where)?.querySelectorAll('tbody tr').length ?? -1,
  bar: () => __t.pane().querySelector('.changes-bar')?.innerText ?? '',
  error: () => __t.pane().querySelector('.changes-error')?.innerText ?? '',
}`
const cell = (key, name, where = 'items') => `__t.cell('${where}', '${key}', '${name}')`
let K = [] // the items on the first page, by userId
const o = (userId) => orders(userId, 1731400000 + Number(userId.slice(1)))
const text = (key, name, where = 'items') => evaluate(`${cell(key, name, where)}?.innerText`)
const orders = (userId, createdAt) => getItem('orders', { userId: { S: userId }, createdAt: { N: String(createdAt) } })
const editCell = async (key, name, value, where = 'items') => { await dblclick(cell(key, name, where)); await key2('a', MOD); await type(value); await key2('Enter') }
const key2 = key
const save = async () => { await key('s', MOD); await sleep(600) }

await evaluate(helpers)
// Sign in to DynamoDB (the fake aws CLI's "demo" profile; the data is DynamoDB Local's).
await click(`[...document.querySelectorAll('.engine-option')].find((b) => b.innerText.startsWith('DynamoDB'))`)
await waitFor(`document.querySelector('.aws-card .card-footer .btn-primary')`)
await click(`document.querySelector('.aws-card .card-footer .btn-primary')`)
check('signed in: the tables are listed', await waitFor(`document.querySelectorAll('.dynamo-sidebar .tree-item').length === 2`, 15000), null)
await click(`[...document.querySelectorAll('.dynamo-sidebar .tree-item')].find((b) => b.innerText === 'orders')`)
check('orders opens: 50 items', await waitFor(`__t.rows('items') === 50`), await evaluate(`__t.rows('items')`))
await evaluate(helpers)
K = await evaluate(`[...__t.grid('items').querySelectorAll('tbody tr')].map((tr) => tr.children[1].innerText).sort()`)
const noted = K.filter((k) => Number(k.slice(1)) % 5 === 0 && k !== K[13]) // items with a note (seed.mjs)
await shot(`${shots}/d1-orders.png`)

// 1. A text cell: type, Enter, ⌘S; saved as text, shown in place.
await editCell(K[1], 'city', 'Pune')
check('typed edit waits in the changes bar', (await evaluate(`__t.bar()`)).includes('1 unsaved change in 1 item'), await evaluate(`__t.bar()`))
check('the tab shows the unsaved dot', await evaluate(`!!document.querySelector('.tab.active .tab-dirty')`), null)
await save()
check('saved: “Saved 1 item to orders”', await waitFor(`__t.bar().includes('Saved 1 item to orders')`), await evaluate(`__t.bar()`))
check('DynamoDB has city = Pune, as text', JSON.stringify((await o(K[1])).city) === '{"S":"Pune"}', (await o(K[1])).city)
check('still 50 items (nothing reloaded to page 1)', (await evaluate(`__t.rows('items')`)) === 50, await evaluate(`__t.rows('items')`))

// 2. A number cell stays a number; text there is refused, nothing saved.
await editCell(K[2], 'amount', '999.25')
await save()
check('number edit saved as a number', JSON.stringify((await o(K[2])).amount) === '{"N":"999.25"}', (await o(K[2])).amount)
await editCell(K[3], 'amount', 'lots')
await save()
check('text in a number field is refused, with why', await waitFor(`__t.error().includes('holds numbers')`), await evaluate(`__t.error()`))
check('…and nothing was saved', JSON.stringify((await o(K[3])).amount) === `{"N":"${100 + Number(K[3].slice(1))}.5"}`, (await o(K[3])).amount)
await click(`[...__t.pane().querySelectorAll('.changes-bar .btn')].find((b) => b.innerText === 'Discard')`)

// 3. Booleans and NULL.
await editCell(K[4], 'paid', 'false')
await dblclick(cell(K[6], 'city'))
await click(`[...document.querySelectorAll('.cell-editor .cell-null-btn')].find((b) => b.innerText === 'NULL')`)
await save()
check('boolean saved', JSON.stringify((await o(K[4])).paid) === '{"BOOL":false}', (await o(K[4])).paid)
check('NULL saved', JSON.stringify((await o(K[6])).city) === '{"NULL":true}', (await o(K[6])).city)

// 4. The cell editor's ✕ removes a field.
await dblclick(cell(noted[0], 'note'))
await click(`[...document.querySelectorAll('.cell-editor .cell-null-btn')].find((b) => b.innerText === '✕')`)
await save()
check('✕ removed the field', !('note' in (await o(noted[0]))), Object.keys(await o(noted[0])))

// 5. Add field: a new column; values typed into it save with their items.
await click(`[...__t.pane().querySelectorAll('.grid-pager .btn')].find((b) => b.innerText.includes('Field'))`)
await waitFor(`document.querySelector('.add-field input')`)
await type('tier')
await evaluate(`(() => { const s = document.querySelector('.add-field select'); const set = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value').set; set.call(s, 'N'); s.dispatchEvent(new Event('change', { bubbles: true })) })()`)
await click(`[...document.querySelectorAll('.add-field .btn')].find((b) => b.innerText === 'Add')`)
check('the new column is there, typed number', await waitFor(`__t.col('items', 'tier') > 0`), null)
await shot(`${shots}/d2-new-field.png`)
await editCell(K[7], 'tier', '3')
await editCell(K[8], 'tier', '4')
await save()
check('new field saved on 2 items as numbers', JSON.stringify((await o(K[7])).tier) === '{"N":"3"}' && JSON.stringify((await o(K[8])).tier) === '{"N":"4"}',
  [(await o(K[7])).tier, (await o(K[8])).tier])
check('2 items saved together', await waitFor(`__t.bar().includes('Saved 2 items to orders')`), await evaluate(`__t.bar()`))

// 6. The JSON view: change a value, add fields (typed by the JSON), remove one; the key can't change.
await dblclick(`__t.row('items', '${noted[1]}').children[0]`)
await waitFor(`document.querySelector('.json-modal .cm-content')`)
const shown = await evaluate(`document.querySelector('.json-modal .cm-content').innerText`)
check('JSON view shows the item as typed JSON', shown.includes(`"amount": ${100 + Number(noted[1].slice(1))}.5,`) && shown.includes(`"paid": ${Number(noted[1].slice(1)) % 2 === 0}`) && shown.includes('"tags": ['), shown.slice(0, 300))
const item10 = JSON.parse(shown)
delete item10.note
item10.city = 'Goa'
item10.score = 7
item10.meta = { source: 'spydb', n: [1, 2] }
await click(`document.querySelector('.json-modal .cm-content')`)
await key('a', MOD)
await type(JSON.stringify(item10, null, 2))
await sleep(500)
await click(`[...document.querySelectorAll('.json-modal .btn-primary')].find((b) => b.innerText === 'Apply')`)
check('JSON changes wait as unsaved edits (4 changes)', await waitFor(`__t.bar().includes('4 unsaved changes in 1 item')`), await evaluate(`__t.bar()`))
check('new fields from the JSON got columns', await evaluate(`__t.col('items', 'score') > 0 && __t.col('items', 'meta') > 0`), null)
await save()
const saved10 = await o(noted[1])
check('JSON saved: city text, score a number, meta a map, note removed, tags still a set',
  saved10.city?.S === 'Goa' && saved10.score?.N === '7' && saved10.meta?.M?.n?.L?.length === 2 && !('note' in saved10) && Array.isArray(saved10.tags?.SS),
  saved10)
await dblclick(`__t.row('items', '${K[11]}').children[0]`)
await waitFor(`document.querySelector('.json-modal .cm-content')`)
const item11 = JSON.parse(await evaluate(`document.querySelector('.json-modal .cm-content').innerText`))
item11.createdAt = 5
await click(`document.querySelector('.json-modal .cm-content')`)
await key('a', MOD)
await type(JSON.stringify(item11, null, 2))
await sleep(500)
await click(`[...document.querySelectorAll('.json-modal .btn-primary')].find((b) => b.innerText === 'Apply')`)
check('changing the key in JSON is refused', await waitFor(`document.querySelector('.json-modal-status.error')?.innerText.includes('part of the key')`),
  await evaluate(`document.querySelector('.json-modal-status')?.innerText`))
await click(`[...document.querySelectorAll('.json-modal .btn')].find((b) => b.innerText === 'Cancel')`)
await sleep(300)
await click(`[...document.querySelectorAll('.json-modal .btn')].find((b) => b.innerText === 'Discard')`).catch(() => {})

// 7. Changed by someone else since loaded: refused.
await ddb('UpdateItem', { TableName: 'orders', Key: { userId: { S: K[12] }, createdAt: { N: String(1731400000 + Number(K[12].slice(1))) } }, UpdateExpression: 'SET city = :c', ExpressionAttributeValues: { ':c': { S: 'Elsewhere' } } })
await editCell(K[12], 'city', 'Mine')
await save()
check('a change made meanwhile stops the save', await waitFor(`__t.error().includes('changed since you loaded it')`), await evaluate(`__t.error()`))
check('…and theirs stays', (await o(K[12])).city?.S === 'Elsewhere', (await o(K[12])).city)
await click(`[...__t.pane().querySelectorAll('.changes-bar .btn')].find((b) => b.innerText === 'Discard')`)

// 8. Delete from the row menu, with the confirmation.
await rightClick(`__t.row('items', '${K[13]}').children[0]`)
const menu = await evaluate(`[...document.querySelectorAll('.row-menu button')].map((b) => b.innerText.trim())`)
check('row menu says items, offers Delete item…', menu.includes('Delete item…') && menu.some((m) => m.startsWith('View item as JSON')), menu)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete item…')`)
check('delete asks first, naming the item', await waitFor(`document.querySelector('.delete-rows')?.innerText.includes('userId = ${K[13]}, createdAt = ${1731400000 + Number(K[13].slice(1))}')`),
  await evaluate(`document.querySelector('.delete-rows')?.innerText`))
await shot(`${shots}/d3-delete.png`)
await click(`document.querySelector('.delete-rows .btn-danger')`)
check('deleted: gone from DynamoDB and the grid', await waitFor(`!__t.row('items', '${K[13]}')`) && !(await o(K[13])), await evaluate(`__t.rows('items')`))
check('“Deleted 1 item from orders”', await waitFor(`__t.bar().includes('Deleted 1 item from orders')`), await evaluate(`__t.bar()`))

// 9. Copy as JSON keeps types.
await click(`__t.row('items', '${K[14]}').children[0]`)
await rightClick(`__t.row('items', '${K[14]}').children[0]`)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'JSON')`)
check('copied 1 item as JSON', await waitFor(`document.querySelector('.copy-toast')?.innerText === 'Copied 1 item as JSON'`, 3000), await evaluate(`document.querySelector('.copy-toast')?.innerText`))

// 10. PartiQL behind </>: SELECT, edit a result, UPDATE.
await click(`__t.pane().querySelector('.filter-bar .sql-toggle')`)
check('</> shows the PartiQL editor with the table', await waitFor(`__t.pane().querySelector('.sql-bar .cm-content')?.innerText.includes('SELECT * FROM "orders"')`), null)
await click(`__t.pane().querySelector('.sql-bar .cm-content')`)
await key('Enter', MOD)
check('SELECT: 50 results, Load more', await waitFor(`__t.rows('results') === 50`), await evaluate(`__t.rows('results')`))
check('results say editable (orders)', (await evaluate(`__t.pane().querySelector('.results-pane .grid-status')?.innerText`))?.includes('editable (orders)'),
  await evaluate(`__t.pane().querySelector('.results-pane .grid-status')?.innerText`))
await shot(`${shots}/d4-partiql.png`)
await editCell(K[15], 'city', 'FromPartiQL', 'results')
await save()
check('a result edit saves to DynamoDB', (await o(K[15])).city?.S === 'FromPartiQL', (await o(K[15])).city)
await click(`[...__t.pane().querySelectorAll('.results-pane .grid-pager .btn')].find((b) => b.innerText.includes('Load more'))`)
check('Load more: 100 results', await waitFor(`__t.rows('results') === 100`), await evaluate(`__t.rows('results')`))
await click(`__t.pane().querySelector('.sql-bar .cm-content')`)
await key('a', MOD)
await type(`UPDATE "orders" SET "city" = 'ViaUpdate' WHERE "userId" = '${K[16]}' AND "createdAt" = ${1731400000 + Number(K[16].slice(1))}`)
await key('Enter', MOD)
check('UPDATE runs: ✓ UPDATE', await waitFor(`__t.pane().querySelector('.tab-notice.ok')?.innerText.includes('✓ UPDATE')`), await evaluate(`[__t.pane().querySelector('.tab-notice, .tab-notice-banner')?.innerText, __t.pane().querySelector('.sql-bar .cm-content')?.innerText]`))
check('…and changed the item', (await o(K[16])).city?.S === 'ViaUpdate', (await o(K[16])).city)
await key('a', MOD)
await type(`SELEC oops`)
await key('Enter', MOD)
check('a bad statement shows AWS’s error', await waitFor(`__t.pane().querySelector('.tab-notice-banner')?.innerText.includes('PartiQL failed')`), null)
await click(`__t.pane().querySelector('.sql-bar .sql-toggle')`)
check('</> again: back to the filters and items', await waitFor(`__t.pane().querySelector('.filter-bar') && __t.rows('items') > 0`), null)

// 11. ⌘T: a PartiQL tab; the tab menu: Duplicate, Close Others.
await key('t', MOD)
check('⌘T opens a PartiQL tab', await waitFor(`document.querySelector('.tab.active .tab-title')?.innerText.startsWith('PartiQL')`), await evaluate(`document.querySelector('.tab.active .tab-title')?.innerText`))
await evaluate(helpers)
await click(`__t.pane().querySelector('.sql-pane .cm-content')`)
await type(`SELECT * FROM "events"`)
await key('Enter', MOD)
check('it runs: 3 events', await waitFor(`__t.pane().querySelectorAll('.playground-results .grid-wrap tbody tr').length === 3`), null)
await shot(`${shots}/d5-partiql-tab.png`)
await rightClick(`document.querySelector('.tab.active')`)
const tabMenu = await evaluate(`[...document.querySelectorAll('.row-menu button')].map((b) => b.innerText.trim())`)
check('tab menu: Close, Close Others, Close Tabs to Right, Close All, Duplicate', ['Close', 'Close Others', 'Close Tabs to Right', 'Close All', 'Duplicate'].every((m) => tabMenu.includes(m)), tabMenu)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Duplicate')`)
check('Duplicate: a copy with the same statement', await waitFor(`document.querySelector('.tab.active .tab-title')?.innerText.endsWith('(copy)') && __t.pane().querySelector('.cm-content')?.innerText.includes('FROM "events"')`), null)
check('3 tabs now', (await evaluate(`document.querySelectorAll('.tab').length`)) === 3, await evaluate(`document.querySelectorAll('.tab').length`))
await rightClick(`[...document.querySelectorAll('.tab')].find((t) => t.innerText.includes('orders'))`)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Close Others')`)
check('Close Others leaves orders only', await waitFor(`document.querySelectorAll('.tab').length === 1`), await evaluate(`document.querySelectorAll('.tab').length`))

// 12. Engine switching keeps each one connected.
await click(`[...document.querySelectorAll('.rail-engine')].find((b) => b.innerText.startsWith('Pg'))`)
check('Pg: the Postgres connect screen; DynamoDB still marked connected', await waitFor(`document.querySelector('.engine-option.on')?.innerText.startsWith('PostgreSQL')`) &&
  await evaluate(`!![...document.querySelectorAll('.engine-option')].find((b) => b.innerText.startsWith('DynamoDB'))?.querySelector('.engine-connected')`), null)
const setField = async (label, value) => evaluate(`(() => { const input = [...document.querySelectorAll('input')].find((i) => i.closest('label, .field')?.innerText.trim().startsWith('${label}') || i.getAttribute('aria-label') === '${label}' || i.name === '${label.toLowerCase()}'); if (!input) return false; const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set; set.call(input, '${value}'); input.dispatchEvent(new Event('input', { bubbles: true })); return true })()`)
const filled = [await setField('Host', 'localhost'), await setField('Port', '5432'), await setField('User', pg.user), await setField('Database', pg.db)]
if (pg.password) await setField('Password', pg.password)
check('filled the Postgres form', filled.every(Boolean), filled)
await click(`[...document.querySelectorAll('button')].find((b) => b.innerText.trim() === 'Connect')`)
check('Postgres connects: its sidebar lists tables', await waitFor(`document.querySelector('.tree')?.innerText.length > 0 && !document.querySelector('.dynamo-sidebar')`, 15000), null)
await shot(`${shots}/d6-postgres.png`)
await click(`[...document.querySelectorAll('.rail-engine')].find((b) => b.innerText.startsWith('Dy'))`)
await evaluate(helpers)
check('Dy: DynamoDB still connected, the orders tab as it was', await waitFor(`document.querySelector('.dynamo-sidebar') && document.querySelector('.tab.active .tab-title')?.innerText === 'orders' && __t.rows('items') > 0`), null)
await click(`[...document.querySelectorAll('.rail-engine')].find((b) => b.innerText.startsWith('Pg'))`)
check('Pg again: still connected, no connect form', await waitFor(`!document.querySelector('.dynamo-sidebar') && document.querySelector('.rail-engine.active')?.innerText.startsWith('Pg') && !document.querySelector('.engine-switch')`), null)
check('both engines show a connected dot', (await evaluate(`document.querySelectorAll('.rail-engine-dot').length`)) === 2, await evaluate(`document.querySelectorAll('.rail-engine-dot').length`))

check('no page errors', p.errors.length === 0, p.errors)
}
