// DynamoDB end to end: sign in, edit cells, add fields, the JSON view,
// deletes, PartiQL (</> and ⌘T / Ctrl+T tabs), the tab menu, and switching to
// Postgres and back with both staying connected. DynamoDB Local's made-up
// tables (dynamo-local.mjs) and the test Postgres; run by run.mjs.
import { CAPTURE_COPIES, check, MOD, SHIFT, sleep } from './cdp.mjs'
import { changedKeys, ddb, getItem, scanItems, snapshot } from './dynamo-local.mjs'

export default async function dynamo(p, { shots, pg }) {
const { evaluate, click, clickWith, dblclick, rightClick, key, type, waitFor, shot } = p

// Page helpers: the shown tab, a grid's cells by item key and column name.
const helpers = `window.__t = {
  pane: () => [...document.querySelectorAll('.tab-panel')].find((e) => !e.hidden),
  grid: (where) => { const pane = __t.pane(); return where === 'results' ? pane.querySelector('.results-pane:not([hidden]) .grid-wrap, .playground-results .grid-wrap') : pane.querySelector('.rows-pane .grid-wrap') },
  col: (where, name) => [...__t.grid(where).querySelectorAll('thead th')].findIndex((th) => th.querySelector('.grid-col-name')?.innerText === name),
  row: (where, key) => [...__t.grid(where).querySelectorAll('tbody tr')].find((tr) => tr.children[1]?.innerText === key),
  cell: (where, key, name) => __t.row(where, key)?.children[__t.col(where, name)],
  rows: (where) => __t.grid(where)?.querySelectorAll('tbody tr').length ?? -1,
  bar: () => __t.pane().querySelector('.changes-bar')?.innerText ?? '',
  status: () => __t.pane().querySelector('.grid-footer .grid-status')?.innerText ?? '',
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
await click(`[...document.querySelectorAll('.rail-engine')].find((b) => b.innerText.trim() === 'Dy')`)
// 0. Access keys typed in and saved: DynamoDB Local takes any keys. They land under DynamoDB in the sidebar.
await waitFor(`document.getElementById('aws-key-id')`)
const setInput = (id, value) => evaluate(`(() => { const i = document.getElementById(${JSON.stringify(id)}); const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set; set.call(i, ${JSON.stringify(value)}); i.dispatchEvent(new Event('input', { bubbles: true })); return true })()`)
await setInput('aws-key-id', 'AKIAE2ETESTKEY')
await setInput('aws-key-secret', 'e2e-secret')
await setInput('aws-key-name', 'E2E keys')
await click(`[...document.querySelectorAll('.aws-keys .btn-primary')].find((b) => b.innerText.includes('Sign in with keys'))`)
check('signed in with typed keys: the tables are listed', await waitFor(`document.querySelectorAll('.dynamo-sidebar .tree-item').length === 2`, 15000), null)
check('the sidebar footer names the keys', await evaluate(`document.querySelector('.dynamo-sidebar')?.innerText.includes('E2E keys')`), await evaluate(`document.querySelector('.dynamo-sidebar .sidebar-footer')?.innerText`))
await click(`document.querySelector('.dynamo-sidebar .sidebar-footer .danger, .dynamo-sidebar [title*="Sign out"], .dynamo-sidebar [aria-label*="Disconnect"]')`)
const dyGroup = `[...document.querySelectorAll('.conn-group')].find((g) => g.getAttribute('aria-label') === 'DynamoDB connections')`
check('signed out: the saved keys are listed under DynamoDB, secret not shown', await waitFor(`${dyGroup}?.innerText.includes('E2E keys') && ${dyGroup}.innerText.includes('AKIA…TKEY') && !${dyGroup}.innerText.includes('e2e-secret')`), await evaluate(`${dyGroup}?.innerText`))
await dblclick(`[...${dyGroup}.querySelectorAll('.conn-item')].find((e) => e.innerText.includes('E2E keys'))`)
check('double-clicking the saved keys signs in again', await waitFor(`document.querySelectorAll('.dynamo-sidebar .tree-item').length === 2`, 15000), null)
await click(`document.querySelector('.dynamo-sidebar .sidebar-footer .danger, .dynamo-sidebar [title*="Sign out"], .dynamo-sidebar [aria-label*="Disconnect"]')`)
await waitFor(`${dyGroup}?.innerText.includes('E2E keys')`)
await evaluate(`[...${dyGroup}.querySelectorAll('.conn-item')].find((e) => e.innerText.includes('E2E keys')).querySelector('.conn-delete').click()`)
await click(`[...${dyGroup}.querySelectorAll('.link-btn')].find((b) => b.innerText === 'Delete')`)
check('deleted: the saved keys are gone from the sidebar', await waitFor(`!${dyGroup}.innerText.includes('E2E keys')`), null)
// Then the profile sign-in, as before.
check('the sidebar lists the AWS profile under DynamoDB', await waitFor(`[...document.querySelectorAll('.conn-group')].some((g) => g.getAttribute('aria-label') === 'DynamoDB connections' && g.querySelector('.conn-item')?.innerText.includes('demo'))`), null)
await waitFor(`document.querySelector('.aws-card .card-footer .btn-primary')`)
await click(`document.querySelector('.aws-card .card-footer .btn-primary')`)
check('signed in: the tables are listed', await waitFor(`document.querySelectorAll('.dynamo-sidebar .tree-item').length === 2`, 15000), null)
await click(`[...document.querySelectorAll('.dynamo-sidebar .tree-item')].find((b) => b.innerText.startsWith('orders'))`)
check('the sidebar shows about how many items each table has', await waitFor(`[...document.querySelectorAll('.dynamo-sidebar .tree-item')].find((b) => b.innerText.includes('orders'))?.querySelector('.tree-rows')?.innerText === '~120'`),
  await evaluate(`[...document.querySelectorAll('.dynamo-sidebar .tree-item')].map((b) => b.innerText)`))
await rightClick(`[...document.querySelectorAll('.dynamo-sidebar .tree-item')].find((b) => b.innerText.includes('orders'))`)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Table details…')`)
check('Table details: key, items, size, capacity, key fields', await waitFor(`(() => { const t = (document.querySelector('.details-dialog')?.innerText ?? '').toLowerCase(); return t.includes('partition key') && t.includes('userid') && t.includes('sort key') && t.includes('createdat') && t.includes('~120') && t.includes('capacity') && t.includes('key fields') && t.includes('number') && t.includes('b per item') })()`),
  await evaluate(`document.querySelector('.details-dialog')?.innerText`))
await key('Escape')
await waitFor(`!document.querySelector('.details-dialog')`)
check('orders opens: 50 items', await waitFor(`__t.rows('items') === 50`), await evaluate(`__t.rows('items')`))
await evaluate(helpers)
K = await evaluate(`[...__t.grid('items').querySelectorAll('tbody tr')].map((tr) => tr.children[1].innerText).sort()`)
const noted = K.filter((k) => Number(k.slice(1)) % 5 === 0 && k !== K[13]) // items with a note (seed.mjs)
await shot(`${shots}/d1-orders.png`)

const KEYS = ['userId', 'createdAt']
const keyOf = (k) => `${k}|${1731400000 + Number(k.slice(1))}`
const sameExcept = async (before, keys, what) => {
  const diff = changedKeys(before, await snapshot('orders', KEYS))
  check(`${what}: of 120 items, only ${keys.join(', ')} changed`, JSON.stringify(diff) === JSON.stringify(keys.map(keyOf).sort()), diff)
}

// 0b. Filters: every shape a person can set, the plan SpyDB says it uses, and the items it
// finds, each checked against what DynamoDB Local holds (a direct read of every item).
const setValue = (js, value) => evaluate(`(() => { const el = ${js}; const set = Object.getOwnPropertyDescriptor(el.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype, 'value').set; set.call(el, ${JSON.stringify(value)}); el.dispatchEvent(new Event(el.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true })) })()`)
const filterRow = (i) => `__t.pane().querySelectorAll('.filter-bar .filter-row')[${i}]`
const setFilter = async (i, field, operator, value) => {
  await click(`${filterRow(i)}.querySelector('.field-picker-btn')`); await type(field); await key('Enter')
  await waitFor(`${filterRow(i)}.querySelector('.field-picker-btn').dataset.value === '${field}'`)
  await setValue(`${filterRow(i)}.querySelector('select[aria-label="Filter condition"]')`, operator)
  if (value !== undefined) await setValue(`${filterRow(i)}.querySelector('.filter-value')`, value)
}
const addFilter = () => click(`__t.pane().querySelector('.filter-bar [aria-label="Add filter"]')`)
const planText = () => evaluate(`__t.pane().querySelector('.dynamo-plan')?.innerText.replace('⚡ ', '') ?? ''`)
const applyAll = async () => { // Apply, then Load more until every item was checked
  // The plan badge appears beside Apply a few ms after a filter changes and reflows the bar: let it settle first.
  await waitFor(`__t.pane().querySelector('.dynamo-plan')`); await sleep(200)
  await click(`[...__t.pane().querySelectorAll('.filter-bar .btn')].find((b) => b.innerText.includes('Apply'))`)
  if (!(await waitFor(`__t.status().includes('found') && !__t.pane().querySelector('.grid-wrap.loading')`))) {
    throw new Error('Apply didn’t filter: ' + JSON.stringify(await evaluate(`(() => { const row = __t.pane().querySelector('.filter-bar .filter-row'); return { field: row.querySelector('.field-picker-btn').dataset.value, value: row.querySelector('.filter-value').value, status: __t.status(), plan: __t.pane().querySelector('.dynamo-plan')?.innerText, error: __t.pane().querySelector('.table-error')?.innerText, focused: document.activeElement?.className } })()`)))
  }
  for (let i = 0; i < 10 && !(await evaluate(`__t.status().includes('all of them')`)); i++) {
    await click(`[...__t.pane().querySelectorAll('.grid-pager .btn')].find((b) => b.innerText.includes('Load more'))`)
    await waitFor(`!__t.pane().querySelector('.grid-wrap.loading')`)
  }
  const status = await evaluate(`__t.status()`)
  const m = /(\d+) found · (\d+) checked/.exec(status.replace(/,/g, ''))
  return { keys: (await evaluate(`[...__t.grid('items').querySelectorAll('tbody tr')].map((tr) => tr.querySelector('td[data-col="0"]').innerText)`)).sort(), found: Number(m?.[1]), checked: Number(m?.[2]), status }
}
const items = await scanItems('orders')
const truth = (pick) => items.filter(pick).map((it) => it.userId.S).sort()
const num = (it, f) => Number(it[f].N)
const filterCase = async (what, plan, pick, checked) => {
  const got = await applyAll()
  const want = truth(pick)
  check(`filter ${what}: plan "${plan}", exactly the ${want.length} items DynamoDB holds`, (await planText()) === plan && JSON.stringify(got.keys) === JSON.stringify(want) && got.found === want.length,
    { plan: await planText(), found: got.found, want: want.length, extra: got.keys.filter((k) => !want.includes(k)), missing: want.filter((k) => !got.keys.includes(k)) })
  if (checked !== undefined) check(`…and read ${checked === want.length ? 'only those' : checked + ' items'} (${got.checked} checked)`, got.checked === checked, got.status)
}
const pune = items.filter((it) => it.city.S === 'Pune').length
await setFilter(0, 'city', 'eq', 'Pune'); await filterCase('city = Pune', 'uses cityIndex', (it) => it.city.S === 'Pune', pune)
await addFilter(); await setFilter(1, 'amount', 'gt', '150'); await filterCase('city = Pune AND amount > 150 (the index’s sort key: no filtering)', 'uses cityIndex', (it) => it.city.S === 'Pune' && num(it, 'amount') > 150, truth((it) => it.city.S === 'Pune' && num(it, 'amount') > 150).length)
await setFilter(1, 'amount', 'lte', '130'); await filterCase('city = Pune AND amount ≤ 130', 'uses cityIndex', (it) => it.city.S === 'Pune' && num(it, 'amount') <= 130, truth((it) => it.city.S === 'Pune' && num(it, 'amount') <= 130).length)
await setFilter(1, 'paid', 'eq', 'true'); await filterCase('city = Pune AND paid = true (a filter on the index’s items)', 'uses cityIndex', (it) => it.city.S === 'Pune' && it.paid.BOOL, pune)
await setFilter(1, 'amount', 'neq', '150.5'); await filterCase('city = Pune AND amount ≠ 150.5 (a sort key can’t do ≠: every item)', 'checks every item', (it) => it.city.S === 'Pune' && num(it, 'amount') !== 150.5, items.length)
await setFilter(0, 'paid', 'eq', 'true'); await setFilter(1, 'city', 'eq', 'Pune'); await filterCase('paid = true first, city = Pune second (the key row counts wherever it is)', 'uses cityIndex', (it) => it.paid.BOOL && it.city.S === 'Pune', pune)
await setFilter(0, 'userId', 'eq', 'u010'); await setFilter(1, 'city', 'eq', 'Pune'); await filterCase('userId = u010 AND city = Pune (the table’s key wins)', 'uses the table’s key', (it) => it.userId.S === 'u010' && it.city.S === 'Pune')
await setFilter(0, 'city', 'starts', 'P'); await setFilter(1, 'note', 'exists'); await filterCase('city starts with P AND note exists (no key fits)', 'checks every item', (it) => it.city.S.startsWith('P') && 'note' in it, items.length)
await setFilter(0, 'gone', 'not_exists'); await setFilter(1, 'amount', 'lt', '105'); await filterCase('gone doesn’t exist AND amount < 105', 'checks every item', (it) => !('gone' in it) && num(it, 'amount') < 105, items.length)
// Load all: one click reads page after page until every item was checked, the counts climbing meanwhile.
await click(`__t.pane().querySelectorAll('.filter-bar .filter-row')[1].querySelector('.filter-remove')`)
await setFilter(0, 'paid', 'eq', 'true')
await waitFor(`__t.pane().querySelector('.dynamo-plan')`); await sleep(200)
await click(`[...__t.pane().querySelectorAll('.filter-bar .btn')].find((b) => b.innerText.includes('Apply'))`)
await waitFor(`__t.status().includes('found') && !__t.pane().querySelector('.grid-wrap.loading')`)
check('after Apply: one page checked, more to read', (await evaluate(`__t.status()`)).includes('50 checked') && !(await evaluate(`__t.status()`)).includes('all of them'), await evaluate(`__t.status()`))
await click(`[...__t.pane().querySelectorAll('.grid-pager .btn')].find((b) => b.innerText === 'Load all')`)
check('Load all: every item checked, every match found, no more clicks', await waitFor(`__t.status().includes('all of them') && !__t.pane().querySelector('.grid-wrap.loading')`, 30000)
  && (await evaluate(`__t.status()`)).includes(`${items.length} checked`) && (await evaluate(`__t.rows('items')`)) === truth((it) => it.paid.BOOL).length, await evaluate(`__t.status()`))
check('…and the Load all button is back, off (nothing more to read)', await evaluate(`[...__t.pane().querySelectorAll('.grid-pager .btn')].find((b) => b.innerText === 'Load all')?.disabled === true`), null)
await click(`[...__t.pane().querySelectorAll('.filter-bar .btn')].find((b) => b.innerText === 'Clear')`)
check('Clear: the first page again, same 50 items', await waitFor(`__t.rows('items') === 50 && !__t.status().includes('found')`) && JSON.stringify(await evaluate(`[...__t.grid('items').querySelectorAll('tbody tr')].map((tr) => tr.children[1].innerText).sort()`)) === JSON.stringify(K), null)

// 1. A text cell: type, Enter, ⌘S; saved as text, shown in place.
let before = await snapshot('orders', KEYS)
check('the table holds 120 items before any edit', Object.keys(before).length === 120, Object.keys(before).length)
await editCell(K[1], 'city', 'Pune')
check('typed edit waits in the changes bar', (await evaluate(`__t.bar()`)).includes('1 unsaved change in 1 item'), await evaluate(`__t.bar()`))
check('the tab shows the unsaved dot', await evaluate(`!!document.querySelector('.tab.active .tab-dirty')`), null)
await save()
check('saved: “Saved 1 item to orders”', await waitFor(`__t.bar().includes('Saved 1 item to orders')`), await evaluate(`__t.bar()`))
check('DynamoDB has city = Pune, as text', JSON.stringify((await o(K[1])).city) === '{"S":"Pune"}', (await o(K[1])).city)
await sameExcept(before, [K[1]], 'one cell saved')
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
await dblclick(cell(K[6], 'paid'))
await click(`[...document.querySelectorAll('.cell-editor .cell-null-btn')].find((b) => b.innerText === 'NULL')`)
await save()
check('boolean saved', JSON.stringify((await o(K[4])).paid) === '{"BOOL":false}', (await o(K[4])).paid)
check('NULL saved', JSON.stringify((await o(K[6])).paid) === '{"NULL":true}', (await o(K[6])).paid)
// city is the key of cityIndex: NULL there is refused up front, with why, and nothing is saved.
before = await snapshot('orders', KEYS)
await editCell(K[9], 'paid', 'true')
await dblclick(cell(K[6], 'city'))
await click(`[...document.querySelectorAll('.cell-editor .cell-null-btn')].find((b) => b.innerText === 'NULL')`)
await save()
check('NULL on an index’s key is refused, saying so', await waitFor(`__t.error().includes('city is the key of an index (cityIndex)') && __t.error().includes('Nothing was saved')`), await evaluate(`__t.error()`))
await sameExcept(before, [], 'refused: nothing saved, not even the other item in the same save')
await click(`[...__t.pane().querySelectorAll('.changes-bar .btn')].find((b) => b.innerText === 'Discard')`)
await waitFor(`!__t.bar()`)

// 4. The cell editor's ✕ removes a field.
await dblclick(cell(noted[0], 'note'))
await click(`[...document.querySelectorAll('.cell-editor .cell-null-btn')].find((b) => b.innerText === '✕')`)
await save()
check('✕ removed the field', !('note' in (await o(noted[0]))), Object.keys(await o(noted[0])))

// 5. Two items edited, saved together; the footer has no "+ Field" (the JSON view adds fields).
check('no "+ Field" button', !(await evaluate(`[...__t.pane().querySelectorAll('.grid-pager .btn')].some((b) => b.innerText.includes('Field'))`)), null)
await shot(`${shots}/d2-table.png`)
before = await snapshot('orders', KEYS)
await editCell(K[21], 'amount', '3')
await editCell(K[22], 'amount', '4')
await save()
check('two number edits saved as numbers', JSON.stringify((await o(K[21])).amount) === '{"N":"3"}' && JSON.stringify((await o(K[22])).amount) === '{"N":"4"}',
  [(await o(K[21])).amount, (await o(K[22])).amount])
check('2 items saved together', await waitFor(`__t.bar().includes('Saved 2 items to orders')`), await evaluate(`__t.bar()`))
await sameExcept(before, [K[21], K[22]], 'two items saved together')

// 5a. Pin a column: it moves next to the row numbers and stays put while the grid scrolls sideways.
await evaluate(`__t.grid('items').scrollLeft = 0`)
const pinCol = 'city'
const headerOf = (name) => `[...__t.grid('items').querySelectorAll('thead th')].find((th) => th.querySelector('.grid-col-name')?.innerText === '${name}')`
await evaluate(`${headerOf(pinCol)}.querySelector('.grid-pin').click()`)
check('the pin lights up and the column is first after the row numbers', await waitFor(`__t.grid('items').querySelector('th.pinned .grid-col-name')?.innerText === '${pinCol}' && __t.grid('items').querySelector('thead th:nth-child(2) .grid-col-name')?.innerText === '${pinCol}' && ${headerOf(pinCol)}.querySelector('.grid-pin').classList.contains('on')`),
  await evaluate(`[...__t.grid('items').querySelectorAll('thead th .grid-col-name')].slice(0, 3).map((e) => e.innerText)`))
const pinnedLeft = await evaluate(`__t.grid('items').querySelector('tbody tr td.pinned').getBoundingClientRect().left`)
const otherLeft = await evaluate(`__t.grid('items').querySelector('tbody tr td:not(.pinned):not(.grid-rownum)').getBoundingClientRect().left`)
await evaluate(`__t.grid('items').scrollLeft = 400`)
await sleep(300)
check('scrolled sideways: the pinned column stays, the others move', await evaluate(`Math.abs(__t.grid('items').querySelector('tbody tr td.pinned').getBoundingClientRect().left - ${pinnedLeft}) < 1 && __t.grid('items').querySelector('tbody tr td:not(.pinned):not(.grid-rownum)').getBoundingClientRect().left < ${otherLeft} - 300`),
  await evaluate(`[__t.grid('items').querySelector('tbody tr td.pinned').getBoundingClientRect().left, ${pinnedLeft}, __t.grid('items').querySelector('tbody tr td:not(.pinned):not(.grid-rownum)').getBoundingClientRect().left, ${otherLeft}, __t.grid('items').scrollLeft]`))
const firstKey = await evaluate(`__t.grid('items').querySelector('tbody tr td[data-col="0"]').innerText`)
check('the pinned cell shows that row’s own value', (await evaluate(`__t.grid('items').querySelector('tbody tr td.pinned').innerText`)) === (await o(firstKey)).city?.S, [firstKey, (await o(firstKey)).city])
await shot(`${shots}/d2b-pinned.png`)
await evaluate(`__t.grid('items').querySelector('th.pinned .grid-pin').click()`)
check('unpinned: back in its place', await waitFor(`!__t.grid('items').querySelector('th.pinned') && __t.grid('items').querySelector('thead th:nth-child(2) .grid-col-name')?.innerText !== '${pinCol}'`), null)
await evaluate(`__t.grid('items').scrollLeft = 0`)

// 5b. Load more, then edit an item from the second page: only it changes.
await click(`[...__t.pane().querySelectorAll('.rows-pane .grid-pager .btn')].find((b) => b.innerText.includes('Load more'))`)
check('Load more: 100 items in the table', await waitFor(`__t.rows('items') === 100`), await evaluate(`__t.rows('items')`))
const page2 = await evaluate(`[...__t.grid('items').querySelectorAll('tbody tr')].slice(50).map((tr) => tr.children[1].innerText)`)
before = await snapshot('orders', KEYS)
await editCell(page2[7], 'city', 'Page2')
await save()
check('a second-page item saves', await waitFor(`__t.bar().includes('Saved 1 item to orders')`) && (await o(page2[7])).city?.S === 'Page2', (await o(page2[7])).city)
await sameExcept(before, [page2[7]], 'second-page item saved')

// 6. The JSON view: change a value, add fields (typed by the JSON), remove one; the key can't change.
await dblclick(`__t.row('items', '${noted[1]}').children[0]`)
await waitFor(`document.querySelector('.json-modal .cm-content')`)
const shown = await evaluate(`document.querySelector('.json-modal .cm-content').innerText`)
check('JSON view shows the item as typed JSON', shown.includes(`"amount": ${100 + Number(noted[1].slice(1))}.5,`) && shown.includes(`"paid": ${Number(noted[1].slice(1)) % 2 === 0}`) && shown.includes('"tags": ['), shown.slice(0, 300))
// ⌘F in the JSON view: as you type, it counts the matches and shows which one; Enter moves on.
await click(`document.querySelector('.json-modal .cm-content')`)
await key('f', MOD)
await waitFor(`document.querySelector('.json-modal .cm-search input[name="search"]')`)
await type('line')
check('search as you type: "1 of 2" for "line" (lines, line 1)', await waitFor(`document.querySelector('.json-modal .json-search-count')?.innerText === '1 of 2'`), await evaluate(`document.querySelector('.json-modal .json-search-count')?.innerText`))
check('…and the first match is selected and on screen', await evaluate(`(() => { const sel = document.querySelector('.json-modal .cm-selectionBackground, .json-modal .cm-searchMatch-selected'); if (!sel) return false; const r = sel.getBoundingClientRect(); const e = document.querySelector('.json-modal .cm-scroller').getBoundingClientRect(); return r.height > 0 && r.top >= e.top && r.bottom <= e.bottom })()`), null)
await key('Enter')
check('Enter: the next match, "2 of 2"', await waitFor(`document.querySelector('.json-modal .json-search-count')?.innerText === '2 of 2'`), await evaluate(`document.querySelector('.json-modal .json-search-count')?.innerText`))
await type('zzz')
check('no match: says so', await waitFor(`document.querySelector('.json-modal .json-search-count')?.innerText === 'No matches'`), null)
await key('Escape')
await waitFor(`!document.querySelector('.json-modal .cm-search')`)
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
check('row menu says items, offers Delete item…', menu.includes('Delete whole item…') && menu.some((m) => m.startsWith('View item as JSON')), menu)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete whole item…')`)
check('delete asks first, naming the item', await waitFor(`document.querySelector('.delete-rows')?.innerText.includes('userId = ${K[13]}, createdAt = ${1731400000 + Number(K[13].slice(1))}')`),
  await evaluate(`document.querySelector('.delete-rows')?.innerText`))
await shot(`${shots}/d3-delete.png`)
before = await snapshot('orders', KEYS)
await click(`document.querySelector('.delete-rows .btn-danger')`)
check('deleted: gone from DynamoDB and the grid', await waitFor(`!__t.row('items', '${K[13]}')`) && !(await o(K[13])), await evaluate(`__t.rows('items')`))
await sameExcept(before, [K[13]], 'one item deleted')
check('119 items remain', Object.keys(await snapshot('orders', KEYS)).length === 119, null)
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
before = await snapshot('orders', KEYS)
await editCell(K[15], 'city', 'FromPartiQL', 'results')
await save()
check('a result edit saves to DynamoDB', (await o(K[15])).city?.S === 'FromPartiQL', (await o(K[15])).city)
await sameExcept(before, [K[15]], 'PartiQL result edit')
before = await snapshot('orders', KEYS)
await click(`[...__t.pane().querySelectorAll('.results-pane .grid-pager .btn')].find((b) => b.innerText.includes('Load more'))`)
check('Load more: 100 results', await waitFor(`__t.rows('results') === 100`), await evaluate(`__t.rows('results')`))
await click(`__t.pane().querySelector('.sql-bar .cm-content')`)
await key('a', MOD)
await type(`UPDATE "orders" SET "city" = 'ViaUpdate' WHERE "userId" = '${K[16]}' AND "createdAt" = ${1731400000 + Number(K[16].slice(1))}`)
await key('Enter', MOD)
check('UPDATE runs: ✓ UPDATE', await waitFor(`__t.pane().querySelector('.tab-notice.ok')?.innerText.includes('✓ UPDATE')`), await evaluate(`[__t.pane().querySelector('.tab-notice, .tab-notice-banner')?.innerText, __t.pane().querySelector('.sql-bar .cm-content')?.innerText]`))
check('…and changed the item', (await o(K[16])).city?.S === 'ViaUpdate', (await o(K[16])).city)
await sameExcept(before, [K[16]], 'PartiQL UPDATE')
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
check('Pg: the Postgres connect screen; DynamoDB still marked connected', await waitFor(`document.querySelector('.rail-engine.active')?.innerText.trim() === 'Pg' && !!document.querySelector('.rail-engine-dynamo .rail-engine-dot')`), null)
const setField = async (label, value) => evaluate(`(() => { const input = [...document.querySelectorAll('input')].find((i) => i.closest('label, .field')?.innerText.trim().startsWith('${label}') || i.getAttribute('aria-label') === '${label}' || i.name === '${label.toLowerCase()}'); if (!input) return false; const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set; set.call(input, '${value}'); input.dispatchEvent(new Event('input', { bubbles: true })); return true })()`)
await waitFor(`[...document.querySelectorAll('input')].some((i) => i.closest('label, .field')?.innerText.trim().startsWith('Database'))`, 20000)
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
check('Pg again: still connected, no connect form', await waitFor(`!document.querySelector('.dynamo-sidebar') && document.querySelector('.rail-engine.active')?.innerText.trim() === 'Pg' && !document.querySelector('aside[aria-label="Connections"]')`), null)
check('both engines show a connected dot', (await evaluate(`document.querySelectorAll('.rail-engine-dot').length`)) === 2, await evaluate(`document.querySelectorAll('.rail-engine-dot').length`))

// ----- Several items at once: exactly those change, nothing else, never the other table -----
await click(`[...document.querySelectorAll('.rail-engine')].find((b) => b.innerText.trim() === 'Dy')`)
await waitFor(`document.querySelector('.dynamo-sidebar') && __t.rows('items') >= 50`)
await evaluate(helpers)
const keyAt = (n) => evaluate(`__t.grid('items').querySelectorAll('tbody tr')[${n}].querySelector('td[data-col="0"]').innerText`)
const numAt = (n) => `__t.grid('items').querySelectorAll('tbody tr')[${n}].querySelector('td.grid-rownum')`
const selectedCount = () => evaluate(`__t.grid('items').querySelectorAll('tbody tr.row-selected').length`)
const dialogText = () => evaluate(`document.querySelector('.delete-rows')?.innerText ?? ''`)
const eventsBefore = JSON.stringify(await snapshot('events', ['id']))
// Rows 30–33: click + Shift-click, then right-click outside the selection.
const four = [await keyAt(30), await keyAt(31), await keyAt(32), await keyAt(33)]
await click(numAt(30)); await clickWith(numAt(33), SHIFT)
check('click + Shift-click selects 4 items', (await selectedCount()) === 4, await selectedCount())
const outside = await keyAt(40)
await rightClick(numAt(40))
check('right-clicking an unselected item selects just it', (await selectedCount()) === 1, await selectedCount())
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete whole item…')`)
check('…and the dialog names only that item', await waitFor(`document.querySelector('.delete-rows')?.innerText.includes('Delete the whole item') && document.querySelector('.delete-rows')?.innerText.includes('userId = ${outside}')`), await dialogText())
before = await snapshot('orders', KEYS)
await click(`[...document.querySelectorAll('.delete-rows .btn')].find((b) => b.innerText === 'Cancel')`)
await waitFor(`!document.querySelector('.delete-rows')`)
await sameExcept(before, [], 'Cancel: nothing deleted')
// The 4 again, right-click inside: 4 whole items, named, exactly those go.
await click(numAt(30)); await clickWith(numAt(33), SHIFT)
await rightClick(numAt(31))
check('right-clicking inside the selection keeps all 4', (await selectedCount()) === 4, await selectedCount())
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete 4 whole items…')`)
check('the dialog names the 4', await waitFor(`${JSON.stringify(four)}.every((k) => document.querySelector('.delete-rows')?.innerText.includes('userId = ' + k))`), await dialogText())
before = await snapshot('orders', KEYS)
await click(`document.querySelector('.delete-rows .btn-danger')`)
check('deleted 4', await waitFor(`__t.bar().includes('Deleted 4 items')`), await evaluate(`__t.bar()`))
await sameExcept(before, four, 'four items deleted together')
check('the events table is untouched', JSON.stringify(await snapshot('events', ['id'])) === eventsBefore, null)
// ⌘-click 3 apart, then an edit in 4 different rows: one save, exactly those 4.
await click(numAt(5)); await clickWith(numAt(20), MOD); await clickWith(numAt(40), MOD)
check('⌘-click adds items one by one', (await selectedCount()) === 3, await selectedCount())
const edited = [await keyAt(5), await keyAt(20), await keyAt(40), await keyAt(44)]
before = await snapshot('orders', KEYS)
for (const k of edited) { await editCell(k, 'city', `Edited ${k}`) }
check('4 unsaved changes in 4 items', (await evaluate(`__t.bar()`)).includes('4 unsaved changes in 4 items'), await evaluate(`__t.bar()`))
await save()
check('saved 4', await waitFor(`__t.bar().includes('Saved 4 items')`), await evaluate(`__t.bar()`))
await sameExcept(before, edited, 'four cells saved together')
check('each got its own value', (await Promise.all(edited.map(o))).every((it, i) => it.city?.S === `Edited ${edited[i]}`), await Promise.all(edited.map((k) => o(k).then((it) => it.city))))
// Sorted the other way round: the item edited is the item meant.
const sortBy = (name) => `[...__t.grid('items').querySelectorAll('thead .grid-head')].find((b) => b.querySelector('.grid-col-name')?.innerText === '${name}')`
await click(sortBy('amount')); await click(sortBy('amount'))
await waitFor(`__t.status().includes('sorted by amount ↓')`)
const top = await keyAt(0)
check('sorted by amount ↓: the biggest amount first', Number((await o(top)).amount.N) >= 999, (await o(top)).amount)
before = await snapshot('orders', KEYS)
await editCell(top, 'city', 'Top')
await save()
await sameExcept(before, [top], 'edit while sorted')
await click(sortBy('amount'))
await waitFor(`!__t.status().includes('sorted')`)
// A pinned column, then 2 picked with ⌘ and deleted: the pin changes nothing about which items go.
await evaluate(`${sortBy('city').replace('.grid-head', 'th')}.querySelector('.grid-pin').click()`)
await waitFor(`__t.grid('items').querySelector('th.pinned')`)
const two = [await keyAt(8), await keyAt(30)]
await click(numAt(8)); await clickWith(numAt(30), MOD)
await rightClick(numAt(30))
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete 2 whole items…')`)
await waitFor(`document.querySelector('.delete-rows .btn-danger')`)
before = await snapshot('orders', KEYS)
await click(`document.querySelector('.delete-rows .btn-danger')`)
await waitFor(`__t.bar().includes('Deleted 2 items')`)
await sameExcept(before, two, 'two items deleted with a column pinned')
await evaluate(`__t.grid('items').querySelector('th.pinned .grid-pin').click()`)
// Select all and delete: the number must be typed; a wrong number keeps the button off; Esc deletes nothing.
await click(numAt(0)); await key('a', MOD)
const all = await evaluate(`__t.rows('items')`)
await rightClick(numAt(0))
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.includes('whole items…'))`)
await waitFor(`document.querySelector('.delete-rows .save-query-input')`)
check(`deleting all ${all} asks you to type the number`, await evaluate(`document.querySelector('.delete-rows')?.innerText.includes('${all}')`), await dialogText())
await click(`document.querySelector('.delete-rows .save-query-input')`); await type(String(all - 1))
check('a wrong number keeps Delete off', await waitFor(`document.querySelector('.delete-rows .btn-danger')?.disabled === true`), null)
before = await snapshot('orders', KEYS)
await key('Escape')
await waitFor(`!document.querySelector('.delete-rows')`)
await sameExcept(before, [], 'Esc: nothing deleted')
check('the events table is still untouched', JSON.stringify(await snapshot('events', ['id'])) === eventsBefore, null)


// ===== More: items sharing a partition key, conflicts among several, the multi-item JSON view, edits and deletes inside a filter, PartiQL writes =====
const put = (userId, createdAt, city) => ddb('PutItem', { TableName: 'orders', Item: { userId: { S: userId }, createdAt: { N: String(createdAt) }, city: { S: city }, amount: { N: '1.00' }, paid: { BOOL: false } } })
const item = (userId, createdAt) => getItem('orders', { userId: { S: userId }, createdAt: { N: String(createdAt) } })
const keyRow = (userId, createdAt) => `[...__t.grid('items').querySelectorAll('tbody tr')].find((tr) => tr.querySelector('td[data-col="0"]').innerText === '${userId}' && tr.querySelector('td[data-col="1"]').innerText === '${createdAt}')`
const loadAll = async () => { for (let i = 0; i < 6 && !(await evaluate(`__t.status().includes('all of them')`)); i++) { await click(`[...__t.pane().querySelectorAll('.grid-pager .btn')].find((b) => b.innerText.includes('Load more'))`); await waitFor(`!__t.pane().querySelector('.grid-wrap.loading')`) } }
const reload = async () => { await click(`__t.pane().querySelector('.grid-pager [aria-label="Reload items"]')`); await waitFor(`!__t.pane().querySelector('.grid-wrap.loading') && __t.rows('items') > 0`); await loadAll() }
const deleteSelected = async (count) => { await rightClick(`__t.grid('items').querySelector('tbody tr.row-selected td.grid-rownum')`); await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.includes('Delete'))`); await waitFor(`document.querySelector('.delete-rows .btn-danger')`); await click(`document.querySelector('.delete-rows .btn-danger')`); await waitFor(`__t.bar().includes('Deleted ${count}')`) }
const snapAll = () => snapshot('orders', KEYS)
const onlyKeys = async (what, before, keys) => { const diff = changedKeys(before, await snapAll()); check(`${what}: only ${keys.length ? keys.join(', ') : 'nothing'} changed`, JSON.stringify(diff) === JSON.stringify([...keys].sort()), diff) }

// Three items of one user (the same partition key, different sort keys): the middle one goes alone; an edit lands on one.
await put('u900', 1, 'One'); await put('u900', 2, 'Two'); await put('u900', 3, 'Three')
await reload()
check('the three u900 items are shown', await evaluate(`!!(${keyRow('u900', 1)} && ${keyRow('u900', 2)} && ${keyRow('u900', 3)})`), null)
await rightClick(`${keyRow('u900', 2)}.querySelector('td.grid-rownum')`)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.includes('Delete'))`)
check('the dialog names both key parts', await waitFor(`document.querySelector('.delete-rows')?.innerText.includes('userId = u900, createdAt = 2')`), await dialogText())
let before2 = await snapAll()
await click(`document.querySelector('.delete-rows .btn-danger')`); await waitFor(`__t.bar().includes('Deleted 1')`)
await onlyKeys('delete u900/2', before2, ['u900|2'])
check('u900/1 and u900/3 stay', !!(await item('u900', 1)) && !!(await item('u900', 3)) && !(await item('u900', 2)), null)
before2 = await snapAll()
await dblclick(`${keyRow('u900', 3)}.children[__t.col('items', 'city')]`); await key('a', MOD); await type('Three!'); await key('Enter'); await save()
await onlyKeys('edit u900/3', before2, ['u900|3'])
check('…u900/1 kept its city', (await item('u900', 1)).city.S === 'One', null)

// Conflicts among several: one item changed behind the grid's back refuses the whole save; the whole delete too.
const c3 = [await keyAt(2), await keyAt(3), await keyAt(4)]
for (const k of c3) await editCell(k, 'city', `Mine ${k}`)
await ddb('UpdateItem', { TableName: 'orders', Key: { userId: { S: c3[1] }, createdAt: { N: String(1731400000 + Number(c3[1].slice(1))) } }, UpdateExpression: 'SET city = :c', ExpressionAttributeValues: { ':c': { S: 'Theirs' } } })
before2 = await snapAll()
await save()
check('a save of 3 where 1 changed meanwhile is refused, naming it', await waitFor(`__t.error().includes('${c3[1]}') && __t.error().includes('changed since you loaded it')`), await evaluate(`__t.error()`))
await onlyKeys('refused save: nothing written, not even the other 2', before2, [])
await click(`[...__t.pane().querySelectorAll('.changes-bar .btn')].find((b) => b.innerText === 'Discard')`); await waitFor(`!__t.bar()`)
await click(numAt(6)); await clickWith(numAt(7), SHIFT); await clickWith(numAt(8), SHIFT)
const d3 = [await keyAt(6), await keyAt(7), await keyAt(8)]
await ddb('UpdateItem', { TableName: 'orders', Key: { userId: { S: d3[2] }, createdAt: { N: String(1731400000 + Number(d3[2].slice(1))) } }, UpdateExpression: 'SET amount = :a', ExpressionAttributeValues: { ':a': { N: '0.5' } } })
await rightClick(numAt(7))
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.includes('Delete 3'))`)
await waitFor(`document.querySelector('.delete-rows .btn-danger')`)
before2 = await snapAll()
await click(`document.querySelector('.delete-rows .btn-danger')`)
check('a delete of 3 where 1 changed meanwhile is refused, naming it', await waitFor(`document.querySelector('.delete-rows')?.innerText.includes('${d3[2]}') && document.querySelector('.delete-rows')?.innerText.includes('changed since you loaded it')`), await dialogText())
await key('Escape'); await waitFor(`!document.querySelector('.delete-rows')`)
await onlyKeys('refused delete: nothing deleted', before2, [])
await reload()

// The JSON view of 3 items: change 2 of them, Apply, save: exactly those 2.
const j3 = [await keyAt(10), await keyAt(11), await keyAt(12)]
await click(numAt(10)); await clickWith(numAt(12), SHIFT)
await rightClick(numAt(11))
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.startsWith('View 3 items as JSON'))`)
await waitFor(`document.querySelector('.json-modal .cm-content')`)
const three = JSON.parse(await evaluate(`document.querySelector('.json-modal .cm-content').cmTile.view.state.doc.toString()`))
three[0].city = 'Json A'; three[2].city = 'Json C'
await evaluate(`(() => { const v = document.querySelector('.json-modal .cm-content').cmTile.view; v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: ${JSON.stringify(JSON.stringify(three, null, 2))} } }) })()`)
await sleep(600)
await click(`[...document.querySelectorAll('.json-modal .btn-primary')].find((b) => b.innerText === 'Apply')`)
await waitFor(`!document.querySelector('.json-modal')`)
check('the JSON view queued 2 changes in 2 items', (await evaluate(`__t.bar()`)).includes('2 unsaved changes in 2 items'), await evaluate(`__t.bar()`))
before2 = await snapAll()
await save()
await onlyKeys('JSON view of 3 items, 2 changed', before2, [j3[0], j3[2]].map(keyOf))
check('…with the values typed; the middle one untouched', (await o(j3[0])).city.S === 'Json A' && (await o(j3[2])).city.S === 'Json C' && (await o(j3[1])).city.S !== 'Json B', null)

// Inside a filter: edits and a delete touch only matching items; Clear shows the rest unchanged.
await setFilter(0, 'city', 'eq', 'Agra'); await applyAll()
const agra = await evaluate(`[...__t.grid('items').querySelectorAll('tbody tr')].map((tr) => tr.querySelector('td[data-col="0"]').innerText)`)
check('filter city = Agra for the edits', agra.length > 3 && agra.every((k) => true), agra.length)
before2 = await snapAll()
await editCell(agra[0], 'paid', 'true'); await editCell(agra[2], 'amount', '42')
await save()
await onlyKeys('edits inside a filter', before2, [agra[0], agra[2]].map(keyOf))
before2 = await snapAll()
await click(`${keyRow(agra[1], 1731400000 + Number(agra[1].slice(1)))}.querySelector('td.grid-rownum')`); await clickWith(`${keyRow(agra[3], 1731400000 + Number(agra[3].slice(1)))}.querySelector('td.grid-rownum')`, MOD)
await deleteSelected(2)
await onlyKeys('a delete inside a filter', before2, [agra[1], agra[3]].map(keyOf))
await click(`[...__t.pane().querySelectorAll('.filter-bar .btn')].find((b) => b.innerText === 'Clear')`)
await waitFor(`__t.rows('items') === 50 && !__t.status().includes('found')`)

// PartiQL writes: UPDATE and DELETE by key touch one item; INSERT adds one; edits in SELECT results save to exactly those.
const pql = async (statement) => { await click(`__t.pane().querySelector('.sql-bar .cm-content')`); await key('a', MOD); await type(statement); await key('Enter', MOD); await sleep(500) }
await click(`__t.pane().querySelector('.filter-bar .sql-toggle')`)
await waitFor(`__t.pane().querySelector('.sql-bar .cm-content')`)
const pk = await keyAt(20)
const pkAt = 1731400000 + Number(pk.slice(1))
before2 = await snapAll()
await pql(`UPDATE "orders" SET city = 'ViaPartiQL' WHERE userId = '${pk}' AND createdAt = ${pkAt}`)
await waitFor(`__t.pane().querySelector('.tab-notice.ok')?.innerText.includes('UPDATE')`)
await onlyKeys('PartiQL UPDATE by key', before2, [keyOf(pk)])
before2 = await snapAll()
await pql(`INSERT INTO "orders" VALUE {'userId': 'u901', 'createdAt': 1, 'city': 'Inserted'}`)
await waitFor(`__t.pane().querySelector('.tab-notice.ok')?.innerText.includes('INSERT')`)
await onlyKeys('PartiQL INSERT', before2, ['u901|1'])
before2 = await snapAll()
await pql(`DELETE FROM "orders" WHERE userId = 'u901' AND createdAt = 1`)
await waitFor(`__t.pane().querySelector('.tab-notice.ok')?.innerText.includes('DELETE')`)
await onlyKeys('PartiQL DELETE by key', before2, ['u901|1'])
await pql(`SELECT * FROM "orders"`)
await waitFor(`__t.rows('results') === 50`)
const rk = await evaluate(`[...__t.grid('results').querySelectorAll('tbody tr')].slice(0, 3).map((tr) => tr.querySelector('td[data-col="0"]').innerText)`)
before2 = await snapAll()
for (const k of rk) await editCell(k, 'city', `Result ${k}`, 'results')
await save()
await onlyKeys('3 PartiQL result rows saved', before2, rk.map(keyOf))
// A SELECT that filters: the footer says how many were read for the items shown; Load all reads to the end.
await pql(`SELECT * FROM "orders" WHERE city = 'Agra'`)
await waitFor(`__t.rows('results') > 0 && __t.pane().querySelector('.results-pane .grid-status')?.innerText.includes('of 50 read')`)
check('a filtering SELECT says "N items (of 50 read, more to read)"', /\d+ items \(of 50 read, more to read\)/.test(await evaluate(`__t.pane().querySelector('.results-pane .grid-status').innerText`)), await evaluate(`__t.pane().querySelector('.results-pane .grid-status').innerText`))
await click(`[...__t.pane().querySelectorAll('.results-pane .grid-pager .btn')].find((b) => b.innerText === 'Load all')`)
const agraNow = (await scanItems('orders')).filter((it) => it.city?.S === 'Agra').length
check('Load all on a SELECT: every page read, exactly the matching items (DynamoDB Local reports no capacity; AWS would show it)', await waitFor(`__t.pane().querySelector('.results-pane .grid-status')?.innerText.includes('all of them')`, 30000)
  && (await evaluate(`__t.rows('results')`)) === agraNow && /of \d+ read\) · all of them/.test(await evaluate(`__t.pane().querySelector('.results-pane .grid-status').innerText`)), [await evaluate(`__t.rows('results')`), agraNow, await evaluate(`__t.pane().querySelector('.results-pane .grid-status').innerText`)])
await click(`__t.pane().querySelector('.sql-toggle.on')`)
await waitFor(`__t.pane().querySelector('.filter-bar') && __t.rows('items') > 0`)

// ===== Cell ranges: drag down a column, Backspace → NULL, a paste into every cell, ⌘C; NULL in an index key is refused whole. Tabs drag into another order. =====
await click(`__t.pane().querySelector('.grid-pager [aria-label="Reload items"]')`); await waitFor(`__t.rows('items') === 50 && !__t.pane().querySelector('.grid-wrap.loading')`); await sleep(300)
await evaluate(CAPTURE_COPIES) // what ⌘C copies lands in window.__copied
const paste = async (text) => { await evaluate(`(() => { const dt = new DataTransfer(); dt.setData('text/plain', ${JSON.stringify(text)}); document.activeElement.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })) })()`); await sleep(300) }
const inRange = () => evaluate(`__t.grid('items').querySelectorAll('td.cell-in-range').length`)
const changed = () => evaluate(`__t.grid('items').querySelectorAll('td.cell-changed').length`)
const toast = () => evaluate(`document.querySelector('.copy-toast')?.innerText ?? ''`)
const [k1, k2, k3] = [await keyAt(0), await keyAt(1), await keyAt(2)]
await p.drag(cell(k1, 'paid'), cell(k3, 'paid'))
check('dragging down 3 paid cells: the first selected, 2 in the range', (await inRange()) === 2 && await evaluate(`${cell(k1, 'paid')}.classList.contains('cell-selected') && ${cell(k3, 'paid')}.classList.contains('cell-in-range')`), await inRange())
before2 = await snapAll()
await key('Backspace')
check('Backspace: 3 cells to NULL, unsaved', (await toast()) === '3 cells set to NULL (unsaved)' && (await changed()) === 3 && (await text(k2, 'paid')) === 'NULL', [await toast(), await changed(), await text(k2, 'paid')])
await onlyKeys('nothing is written until saved', before2, [])
await save()
await onlyKeys('3 paid cells NULL, saved', before2, [k1, k2, k3].map(keyOf))
check('…paid is NULL in DynamoDB for those', (await o(k1)).paid?.NULL === true && (await o(k2)).paid?.NULL === true && (await o(k3)).paid?.NULL === true, [(await o(k1)).paid, (await o(k3)).paid])
// city is cityIndex's key: NULL there is refused before anything is sent; nothing changes.
await p.drag(cell(k1, 'city'), cell(k3, 'city')); await key('Backspace')
check('3 city cells to NULL, unsaved', (await changed()) === 3, await changed())
before2 = await snapAll()
await save()
check('NULL in an index key: the save is refused, naming the index', await waitFor(`__t.error().includes('key of an index') && __t.error().includes('cityIndex')`), await evaluate(`__t.error()`))
await onlyKeys('refused: nothing written', before2, [])
await click(`[...__t.pane().querySelectorAll('.changes-bar .btn')].find((b) => b.innerText === 'Discard')`)
check('Discard: the cities are back', (await changed()) === 0 && (await text(k1, 'city')) !== 'NULL', await text(k1, 'city'))
// A paste: one city into 3 cells (text, which the index takes).
await p.drag(cell(k1, 'city'), cell(k3, 'city'))
await paste('Pasted City')
check('pasting one value fills the 3 city cells, unsaved', (await toast()) === 'Pasted into 3 cells (unsaved)' && (await changed()) === 3 && (await text(k3, 'city')) === 'Pasted City', [await toast(), await changed()])
before2 = await snapAll()
await save()
await onlyKeys('pasted into 3 cells, saved', before2, [k1, k2, k3].map(keyOf))
check('…city = Pasted City in DynamoDB for exactly those', (await scanItems('orders')).filter((it) => it.city?.S === 'Pasted City').map((it) => it.userId.S).sort().join() === [k1, k2, k3].sort().join(), null)
// Shift+↓ grows the range; 3 pasted lines go cell by cell into note (a field most items don't have yet).
await click(cell(k1, 'note')); await key('ArrowDown', SHIFT); await key('ArrowDown', SHIFT)
check('Shift+↓ twice: a range of 3', (await inRange()) === 2, await inRange())
await paste('n1\nn2\nn3')
check('3 pasted lines, cell by cell', (await changed()) === 3 && (await text(k2, 'note')) === 'n2' && (await text(k3, 'note')) === 'n3', [await changed(), await text(k2, 'note')])
before2 = await snapAll()
await save()
await onlyKeys('3 notes pasted, saved', before2, [k1, k2, k3].map(keyOf))
check('…as strings', (await o(k1)).note?.S === 'n1' && (await o(k2)).note?.S === 'n2' && (await o(k3)).note?.S === 'n3', [(await o(k1)).note, (await o(k3)).note])
// ⌘C copies the range; Esc lets go of it.
await p.drag(cell(k1, 'city'), cell(k3, 'city')); await key('c', MOD)
check('⌘C copies 3 cells as lines', (await toast()) === 'Copied 3 cells' && (await evaluate(`window.__copied`)) === 'Pasted City\nPasted City\nPasted City', await evaluate(`window.__copied`))
await key('Escape')
check('Esc clears the range', (await inRange()) === 0, await inRange())

// Tabs drag into another order.
await click(`[...document.querySelectorAll('.dynamo-sidebar .tree-item')].find((b) => b.innerText.startsWith('events'))`)
await waitFor(`document.querySelector('.tab.active .tab-title')?.innerText === 'events'`)
const tabTitles = () => evaluate(`[...document.querySelectorAll('.tabbar .tab .tab-title')].map((t) => t.innerText)`)
const tabOrder = async (expected) => { await waitFor(`JSON.stringify([...document.querySelectorAll('.tabbar .tab .tab-title')].map((t) => t.innerText)) === ${JSON.stringify(JSON.stringify(expected))}`, 3000); return JSON.stringify(await tabTitles()) === JSON.stringify(expected) }
const tabOf = (title) => `[...document.querySelectorAll('.tabbar .tab')].find((t) => t.querySelector('.tab-title')?.innerText === '${title}')`
const titles = await tabTitles()
const others = titles.filter((t) => t !== 'orders')
check('orders and events tabs are open', titles.includes('orders') && titles.includes('events'), titles)
await p.drag(tabOf('orders'), `document.querySelectorAll('.tabbar .tab')[${titles.length - 1}]`, { dx: 20 })
check('dragging orders past the last tab puts it last', await tabOrder([...others, 'orders']), await tabTitles())
await click(tabOf('orders')); await evaluate(helpers)
check('…and it still shows its 50 items', await waitFor(`document.querySelector('.tab.active .tab-title')?.innerText === 'orders' && __t.rows('items') === 50`), await evaluate(`__t.rows('items')`))
await p.drag(tabOf('orders'), `document.querySelector('.tabbar .tab')`, { dx: -20 })
check('dragged before the first tab: first', await tabOrder(['orders', ...others]), await tabTitles())
await shot(`${shots}/d9-range.png`)

check('the events table never moved', JSON.stringify(await snapshot('events', ['id'])) === eventsBefore, null)

check('no page errors', p.errors.length === 0, p.errors)
}
