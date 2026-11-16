// Postgres end to end: connect, edit a cell, the JSON view, delete (with its
// check of what else goes), a query tab and an edit in its results. Only
// the table spydb_ui_test, which run.mjs makes (and drops) in the test
// database.
import { CAPTURE_COPIES, check, MOD, SHIFT, sleep } from './cdp.mjs'

export default async function postgres(p, { shots, pg, sql }) {
const { evaluate, click, clickWith, dblclick, rightClick, key, type, waitFor, shot } = p
const helpers = `window.__t = {
  pane: () => [...document.querySelectorAll('.tab-panel')].find((e) => !e.hidden),
  grid: () => __t.pane().querySelector('.rows-pane:not([hidden]) .grid-wrap, .playground-results .grid-wrap, .results-pane:not([hidden]) .grid-wrap'),
  col: (name) => [...__t.grid().querySelectorAll('thead th')].findIndex((th) => th.querySelector('.grid-col-name')?.innerText === name),
  row: (id) => [...__t.grid().querySelectorAll('tbody tr')].find((tr) => tr.children[1]?.innerText === id),
  cell: (id, name) => __t.row(id)?.children[__t.col(name)],
  bar: () => __t.pane().querySelector('.changes-bar')?.innerText ?? '',
}`
const cell = (id, name) => `__t.cell('${id}', '${name}')`

await evaluate(helpers)
const setField = async (label, value) => evaluate(`(() => { const input = [...document.querySelectorAll('input')].find((i) => i.closest('label, .field')?.innerText.trim().startsWith('${label}')); if (!input) return false; const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set; set.call(input, '${value}'); input.dispatchEvent(new Event('input', { bubbles: true })); return true })()`)
// The form must be on screen before it's filled (a slow computer draws it after the page loads).
check('the connect form is there', await waitFor(`[...document.querySelectorAll('input')].some((i) => i.closest('label, .field')?.innerText.trim().startsWith('Database'))`, 20000), null)
const filled = [await setField('Host', 'localhost'), await setField('Port', '5432'), await setField('User', pg.user), await setField('Database', pg.db)]
check('host, port, user and database filled in', filled.every(Boolean), filled)
if (pg.password) await setField('Password', pg.password)
await click(`[...document.querySelectorAll('button')].find((b) => b.innerText.trim() === 'Connect')`)
check('connected', await waitFor(`[...document.querySelectorAll('.tree-item')].some((b) => b.innerText.includes('spydb_ui_test'))`, 15000), null)
await click(`[...document.querySelectorAll('.tree-item')].find((b) => b.innerText.includes('spydb_ui_test'))`)
// Right-click the table in the sidebar: Table details, with its columns and keys.
await rightClick(`[...document.querySelectorAll('.tree-item')].find((b) => b.innerText.includes('spydb_ui_test'))`)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Table details…')`)
check('Table details: the facts and the columns with types and the primary key', await waitFor(`(() => { const d = document.querySelector('.details-dialog'); if (!d) return false; const t = d.innerText.toLowerCase(); return t.includes('public.spydb_ui_test') && t.includes('rows (estimate)') && t.includes('size on disk') && t.includes('columns') && t.includes('integer') && t.includes('✓') && t.includes('jsonb') && t.includes('indexes') && t.includes('spydb_ui_test_pkey') })()`),
  await evaluate(`document.querySelector('.details-dialog')?.innerText`))
await key('Escape')
check('Esc closes it', await waitFor(`!document.querySelector('.details-dialog')`), null)
await evaluate(helpers)
check('table opens: 20 rows', await waitFor(`__t.grid()?.querySelectorAll('tbody tr').length === 20`), null)
// Every row as JSON by id, to see exactly which rows a save or delete changed.
const snapshot = () => Object.fromEntries(sql(`select id || E'\t' || row_to_json(t)::text from spydb_ui_test t order by id`).split('\n').filter(Boolean).map((l) => l.split('\t')))
const sameExcept = (before, ids, what) => {
  const after = snapshot()
  const diff = [...new Set([...Object.keys(before), ...Object.keys(after)])].filter((k) => before[k] !== after[k]).sort()
  check(`${what}: of the rows, only id ${ids.join(', ')} changed`, JSON.stringify(diff) === JSON.stringify(ids.map(String).sort()), diff)
}
let before = snapshot()
check('20 rows in Postgres before any edit', Object.keys(before).length === 20, Object.keys(before).length)

// ----- Filters: every shape, each checked against Postgres directly -----
const setValue = (js, value) => evaluate(`(() => { const el = ${js}; const set = Object.getOwnPropertyDescriptor(el.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype, 'value').set; set.call(el, ${JSON.stringify(value)}); el.dispatchEvent(new Event(el.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true })) })()`)
const filterRow = (i) => `__t.pane().querySelectorAll('.filter-bar .filter-row')[${i}]`
const setFilter = async (i, column, operator, value) => {
  await click(`${filterRow(i)}.querySelector('.field-picker-btn')`); await type(column); await key('Enter')
  await waitFor(`${filterRow(i)}.querySelector('.field-picker-btn').dataset.value === '${column}'`)
  await setValue(`${filterRow(i)}.querySelector('select[aria-label="Filter operator"]')`, operator)
  if (value !== undefined) await setValue(`${filterRow(i)}.querySelector('.filter-value')`, value)
}
const addFilterRow = () => click(`__t.pane().querySelector('.filter-bar [aria-label="Add filter"]')`)
const apply = async () => { await click(`[...__t.pane().querySelectorAll('.filter-bar .btn')].find((b) => b.innerText.includes('Apply'))`); await sleep(400) }
const shownIds = () => evaluate(`[...__t.grid().querySelectorAll('tbody tr')].map((tr) => tr.querySelector('td[data-col="0"]').innerText).sort((a, b) => a - b).join(',')`)
const trueIds = (where) => sql(`select coalesce(string_agg(id::text, ',' order by id), '') from spydb_ui_test where ${where}`)
const filterCase = async (what, where) => { check(`filter ${what}: exactly the rows Postgres says (${trueIds(where) || 'none'})`, await waitFor(`!__t.pane().querySelector('.grid-wrap.loading')`) && (await shownIds()) === trueIds(where), [await shownIds(), trueIds(where)]) }
await setFilter(0, 'name', 'contains', 'name 1'); await apply(); await filterCase('name contains "name 1"', `name like '%name 1%'`)
await setFilter(0, 'n', 'gte', '100'); await apply(); await filterCase('n ≥ 100', `n >= 100`)
await setFilter(0, 'flag', 'eq', 'true'); await apply(); await filterCase('flag = true', `flag`)
await setFilter(0, 'name', 'starts', 'name 2'); await apply(); await filterCase('name starts with "name 2"', `name like 'name 2%'`)
await setFilter(0, 'n', 'neq', '50'); await apply(); await filterCase('n ≠ 50', `n <> 50`)
await addFilterRow(); await setFilter(0, 'n', 'gt', '50'); await setFilter(1, 'flag', 'eq', 'true'); await apply(); await filterCase('n > 50 AND flag = true', `n > 50 and flag`)
await setFilter(1, 'parent_id', 'is_null'); await apply(); await filterCase('n > 50 AND parent_id is null', `n > 50 and parent_id is null`)
await setFilter(1, 'parent_id', 'is_not_null'); await setFilter(0, 'n', 'lt', '40'); await apply(); await filterCase('n < 40 AND parent_id is not null', `n < 40 and parent_id is not null`)
await click(`[...__t.pane().querySelectorAll('.filter-bar .btn')].find((b) => b.innerText === 'Clear')`)
check('Clear: all 20 rows again', await waitFor(`__t.grid()?.querySelectorAll('tbody tr').length === 20`), await evaluate(`__t.grid()?.querySelectorAll('tbody tr').length`))

// Pin the foreign-key column: it sticks next to the row numbers while the grid scrolls sideways, link button and all.
const headerOf = (name) => `[...__t.grid().querySelectorAll('thead th')].find((th) => th.querySelector('.grid-col-name')?.innerText === '${name}')`
await evaluate(`${headerOf('parent_id')}.querySelector('.grid-pin').click()`)
await waitFor(`__t.grid().querySelector('th.pinned .grid-col-name')?.innerText === 'parent_id'`)
const pinnedLeft = await evaluate(`__t.grid().querySelector('tbody tr td.pinned').getBoundingClientRect().left`)
await evaluate(`__t.grid().scrollLeft = 400`)
await sleep(300)
check('a pinned foreign-key column stays put while scrolling (its cells are sticky too)', await evaluate(`(() => { const td = __t.grid().querySelector('tbody tr td.pinned'); return getComputedStyle(td).position === 'sticky' && Math.abs(td.getBoundingClientRect().left - ${pinnedLeft}) < 1 && __t.grid().scrollLeft > 300 && td.innerText.trim().startsWith('1') })()`),
  await evaluate(`[getComputedStyle(__t.grid().querySelector('tbody tr td.pinned')).position, __t.grid().querySelector('tbody tr td.pinned').getBoundingClientRect().left, ${pinnedLeft}, __t.grid().scrollLeft]`))
await evaluate(`__t.grid().querySelector('th.pinned .grid-pin').click()`)
await waitFor(`!__t.grid().querySelector('th.pinned')`)
await evaluate(`__t.grid().scrollLeft = 0`)
await dblclick(cell('3', 'name')); await key('a', MOD); await type('renamed'); await key('Enter')
check('changes bar says rows', (await evaluate(`__t.bar()`)).includes('1 unsaved change in 1 row'), await evaluate(`__t.bar()`))
await key('s', MOD); await sleep(800)
check('saved: “Saved 1 row to public.spydb_ui_test”', await waitFor(`__t.bar().includes('Saved 1 row to public.spydb_ui_test')`), await evaluate(`__t.bar()`))
check('Postgres has it', sql(`select name from spydb_ui_test where id = 3`) === 'renamed', sql(`select name from spydb_ui_test where id = 3`))
sameExcept(before, [3], 'one cell saved')

await dblclick(`__t.row('4').children[0]`)
await waitFor(`document.querySelector('.json-modal .cm-content')`)
const row4 = JSON.parse(await evaluate(`document.querySelector('.json-modal .cm-content').innerText`))
check('row JSON as before: keys and types', row4.id === 4 && row4.n === 40 && row4.flag === true && row4.j?.k === 4, row4)
row4.n = 41
await click(`document.querySelector('.json-modal .cm-content')`); await key('a', MOD); await type(JSON.stringify(row4)); await sleep(500)
await click(`[...document.querySelectorAll('.json-modal .btn-primary')].find((b) => b.innerText === 'Apply')`)
await key('s', MOD); await sleep(800)
check('JSON edit saved', sql(`select n from spydb_ui_test where id = 4`) === '41', sql(`select n from spydb_ui_test where id = 4`))
await dblclick(`__t.row('5').children[0]`)
await waitFor(`document.querySelector('.json-modal .cm-content')`)
const row5 = JSON.parse(await evaluate(`document.querySelector('.json-modal .cm-content').innerText`))
row5.extra = 1
await click(`document.querySelector('.json-modal .cm-content')`); await key('a', MOD); await type(JSON.stringify(row5)); await sleep(500)
await click(`[...document.querySelectorAll('.json-modal .btn-primary')].find((b) => b.innerText === 'Apply')`)
check('Postgres JSON view still refuses new keys', await waitFor(`document.querySelector('.json-modal-status.error')?.innerText.includes('isn’t a column')`), await evaluate(`document.querySelector('.json-modal-status')?.innerText`))
await click(`[...document.querySelectorAll('.json-modal .btn')].find((b) => b.innerText === 'Cancel')`); await sleep(300)
await click(`[...document.querySelectorAll('.json-modal .btn')].find((b) => b.innerText === 'Discard')`).catch(() => {})

await rightClick(`__t.row('6').children[0]`)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete whole row…')`)
check('delete dialog checks effects (database + role shown)', await waitFor(`document.querySelector('.delete-rows')?.innerText.includes('as role') && document.querySelector('.delete-rows')?.innerText.includes('public.spydb_ui_test')`),
  await evaluate(`document.querySelector('.delete-rows')?.innerText`))
await waitFor(`!document.querySelector('.delete-rows .btn-danger')?.disabled`)
before = snapshot()
await click(`document.querySelector('.delete-rows .btn-danger')`)
check('deleted in Postgres', await waitFor(`!__t.row('6')`) && sql(`select count(*) from spydb_ui_test where id = 6`) === '0', null)
sameExcept(before, [6], 'one row deleted')

await key('t', MOD)
await evaluate(helpers)
check('⌘T: a query tab', await waitFor(`document.querySelector('.tab.active .tab-title')?.innerText.startsWith('Query')`), null)
await click(`__t.pane().querySelector('.cm-content')`); await type('SELECT * FROM spydb_ui_test ORDER BY id'); await key('Enter', MOD)
check('results: 19 rows, editable', await waitFor(`__t.grid()?.querySelectorAll('tbody tr').length === 19`), null)
before = snapshot()
await dblclick(cell('7', 'name')); await key('a', MOD); await type('from results'); await key('Enter'); await key('s', MOD); await sleep(800)
check('result edit saved', sql(`select name from spydb_ui_test where id = 7`) === 'from results', sql(`select name from spydb_ui_test where id = 7`))
sameExcept(before, [7], 'query result edit')
check('…and the result shows it without rerunning', (await evaluate(`${cell('7', 'name')}?.innerText`)) === 'from results', null)
await shot(`${shots}/p1-query.png`)
// ----- The JSON view's search, on a long document (every row as JSON) -----
await click(`[...document.querySelectorAll('.tree-item')].find((b) => b.innerText.includes('spydb_ui_test'))`)
await waitFor(`__t.grid()?.querySelectorAll('tbody tr').length === 19`)
const rowBy = (id) => `[...__t.grid().querySelectorAll('tbody tr')].find((tr) => tr.querySelector('td[data-col="0"]')?.innerText === '${id}')`
const numOf = (id) => `${rowBy(id)}.querySelector('td.grid-rownum')`
await click(numOf('1'))
await key('a', MOD)
check('⌘A selects every row', (await evaluate(`__t.grid().querySelectorAll('tbody tr.row-selected').length`)) === 19, await evaluate(`__t.grid().querySelectorAll('tbody tr.row-selected').length`))
await rightClick(numOf('1'))
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.startsWith('View') && b.innerText.includes('JSON'))`)
await waitFor(`document.querySelector('.json-modal .cm-content')`)
const searchState = () => evaluate(`(() => { const v = document.querySelector('.json-modal .cm-content').cmTile.view; const sel = v.state.selection.main; const sc = v.scrollDOM; const b = v.lineBlockAt(sel.from); return { count: document.querySelector('.json-modal .json-search-count')?.innerText ?? '', line: v.state.doc.lineAt(sel.from).number, text: v.state.doc.sliceString(sel.from, sel.to), visible: b.top >= sc.scrollTop && b.top + b.height <= sc.scrollTop + sc.clientHeight, lines: v.state.doc.lines } })()`)
const docLines = (await searchState()).lines
check('a long document: well over a screen of lines', docLines > 150, docLines)
await click(`document.querySelector('.json-modal .cm-content')`)
await key('f', MOD)
await waitFor(`document.querySelector('.json-modal .cm-search input[name="search"]')`)
// A new query: the field's text is selected (a synthetic ⌘A doesn't select in a plain input), typed over,
// then a keyup so the editor's own panel commits the query too, as it does for a person's typing.
const search = async (text) => { await evaluate(`(() => { const i = document.querySelector('.json-modal .cm-search input[name="search"]'); i.focus(); i.select() })()`); await type(text); await evaluate(`document.querySelector('.json-modal .cm-search input[name="search"]').dispatchEvent(new KeyboardEvent('keyup', { key: 'x', bubbles: true }))`); await sleep(250) }
await search('name 20')
let st = await searchState()
check('search: the one match near the end is selected and scrolled into view', st.count === '1 of 1' && st.text === 'name 20' && st.visible, st)
await search('name 1')
st = await searchState()
check('"name 1": 11 matches (name 1, name 10 … name 19), the first selected', st.count === '1 of 11' && st.text === 'name 1' && st.visible, st)
await key('Enter'); st = await searchState()
check('Enter: the second, in view', st.count === '2 of 11' && st.visible, st)
await key('Enter', SHIFT); st = await searchState()
check('Shift+Enter: back to the first', st.count === '1 of 11', st)
await key('Enter', SHIFT); st = await searchState()
check('Previous from the first wraps to the last, in view', st.count === '11 of 11' && st.visible, st)
await click(`[...document.querySelectorAll('.json-modal .cm-search button')].find((b) => b.name === 'next')`); st = await searchState()
check('Next from the last wraps to the first', st.count === '1 of 11', st)
await click(`document.querySelector('.json-modal .cm-search input[name="word"]')`); await sleep(250); st = await searchState()
check('by word: only "name 1" itself', st.count === '1 of 1' && st.text === 'name 1', st)
await click(`document.querySelector('.json-modal .cm-search input[name="word"]')`)
await search('NAME 1'); st = await searchState()
check('case doesn’t matter unless asked: still 11', st.count === '1 of 11', st)
await click(`document.querySelector('.json-modal .cm-search input[name="case"]')`); await sleep(250); st = await searchState()
check('match case: no match for NAME, and it says so', st.count === 'No matches', st)
await click(`document.querySelector('.json-modal .cm-search input[name="case"]')`)
await click(`document.querySelector('.json-modal .cm-search input[name="re"]')`)
await search('name 2\\d'); st = await searchState()
check('regexp: name 2\\d finds name 20 only', st.count === '1 of 1' && st.text === 'name 20', st)
await click(`document.querySelector('.json-modal .cm-search input[name="re"]')`)
await search('qqqq'); st = await searchState()
check('nothing: "No matches"', st.count === 'No matches', st)
await key('Escape')
await waitFor(`!document.querySelector('.json-modal .cm-search')`)
await key('Escape')
await waitFor(`!document.querySelector('.json-modal')`)

// ----- Several rows at once: exactly those change, nothing else, never the other table -----
const parentSnapshot = () => sql(`select id || E'\\t' || row_to_json(t)::text from spydb_ui_parent t order by id`)
const parentBefore = parentSnapshot()
const selectedCount = () => evaluate(`__t.grid().querySelectorAll('tbody tr.row-selected').length`)
const dialogText = () => evaluate(`document.querySelector('.delete-rows')?.innerText ?? ''`)
// A click, then Shift-click: a range of 4 (ids 9–12).
await click(numOf('9')); await clickWith(numOf('12'), SHIFT)
check('click + Shift-click selects the 4 rows between', (await selectedCount()) === 4, await selectedCount())
// Right-click on a row outside the selection: only that row is meant.
await rightClick(numOf('15'))
check('right-clicking an unselected row selects just it', (await selectedCount()) === 1 && (await evaluate(`${rowBy('15')}.classList.contains('row-selected')`)), await selectedCount())
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete whole row…')`)
check('…and the dialog names only id = 15', await waitFor(`document.querySelector('.delete-rows')?.innerText.includes('Delete the whole row') && document.querySelector('.delete-rows')?.innerText.includes('id = 15')`), await dialogText())
before = snapshot()
await click(`[...document.querySelectorAll('.delete-rows .btn')].find((b) => b.innerText === 'Cancel')`)
await waitFor(`!document.querySelector('.delete-rows')`)
sameExcept(before, [], 'Cancel: nothing deleted')
// The range again, right-click inside it: 4 whole rows, named, and exactly those go.
await click(numOf('9')); await clickWith(numOf('12'), SHIFT)
await rightClick(numOf('10'))
check('right-clicking inside the selection keeps all 4', (await selectedCount()) === 4, await selectedCount())
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete 4 whole rows…')`)
check('the dialog names the 4', await waitFor(`['id = 9', 'id = 10', 'id = 11', 'id = 12'].every((n) => document.querySelector('.delete-rows')?.innerText.includes(n))`), await dialogText())
await waitFor(`!document.querySelector('.delete-rows .btn-danger')?.disabled`)
before = snapshot()
await click(`document.querySelector('.delete-rows .btn-danger')`)
check('deleted 4', await waitFor(`__t.bar().includes('Deleted 4 rows') && __t.grid().querySelectorAll('tbody tr').length === 15`), await evaluate(`__t.bar()`))
sameExcept(before, [9, 10, 11, 12], 'four rows deleted together')
check('the other table is untouched', parentSnapshot() === parentBefore, null)
// ⌘-click: 3 rows apart (2, 7, 14), plus an edit in each and in 17: one save, exactly 4 rows.
await click(numOf('2')); await clickWith(numOf('7'), MOD); await clickWith(numOf('14'), MOD)
check('⌘-click adds rows one by one', (await selectedCount()) === 3, await selectedCount())
before = snapshot()
for (const id of ['2', '7', '14', '17']) { await dblclick(cell(id, 'name')); await key('a', MOD); await type(`edited ${id}`); await key('Enter') }
check('4 unsaved changes in 4 rows', (await evaluate(`__t.bar()`)).includes('4 unsaved changes in 4 rows'), await evaluate(`__t.bar()`))
await key('s', MOD); await sleep(800)
check('saved 4', await waitFor(`__t.bar().includes('Saved 4 rows')`), await evaluate(`__t.bar()`))
sameExcept(before, [2, 7, 14, 17], 'four cells saved together')
check('each got its own value', sql(`select string_agg(name, ',' order by id) from spydb_ui_test where id in (2,7,14,17)`) === 'edited 2,edited 7,edited 14,edited 17', null)
// Sorted the other way round: the row edited is the row meant.
const sortBy = (name) => `[...__t.grid().querySelectorAll('thead .grid-head')].find((b) => b.querySelector('.grid-col-name')?.innerText === '${name}')`
await click(sortBy('n')); await click(sortBy('n'))
check('sorted by n ↓: id 20 first', await waitFor(`__t.grid().querySelector('tbody tr td[data-col="0"]')?.innerText === '20'`), await evaluate(`__t.grid().querySelector('tbody tr td[data-col="0"]')?.innerText`))
before = snapshot()
await dblclick(`__t.grid().querySelector('tbody tr').children[__t.col('name')]`); await key('a', MOD); await type('top row'); await key('Enter')
await key('s', MOD); await sleep(800)
sameExcept(before, [20], 'edit while sorted')
check('…with the right value', sql(`select name from spydb_ui_test where id = 20`) === 'top row', null)
await click(sortBy('n'))
await waitFor(`__t.grid().querySelector('tbody tr td[data-col="0"]')?.innerText === '1'`)
// A pinned column, then a delete of 2 picked with ⌘: the pin changes nothing about which rows go.
await evaluate(`${sortBy('name').replace('.grid-head', 'th')}.querySelector('.grid-pin').click()`)
await waitFor(`__t.grid().querySelector('th.pinned')`)
await click(numOf('3')); await clickWith(numOf('18'), MOD)
await rightClick(numOf('18'))
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete 2 whole rows…')`)
await waitFor(`!document.querySelector('.delete-rows .btn-danger')?.disabled`)
before = snapshot()
await click(`document.querySelector('.delete-rows .btn-danger')`)
await waitFor(`__t.bar().includes('Deleted 2 rows')`)
sameExcept(before, [3, 18], 'two rows deleted with a column pinned')
await evaluate(`__t.grid().querySelector('th.pinned .grid-pin').click()`)
// Select all and delete: more than 5 rows must be typed as a number; a wrong number keeps the button off.
await click(numOf('1')); await key('a', MOD)
await rightClick(numOf('1'))
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.includes('whole rows…'))`)
await waitFor(`document.querySelector('.delete-rows .save-query-input')`)
check('deleting all 13 asks you to type the number', await evaluate(`document.querySelector('.delete-rows')?.innerText.includes('13')`), await dialogText())
await click(`document.querySelector('.delete-rows .save-query-input')`); await type('12')
check('a wrong number keeps Delete off', await waitFor(`document.querySelector('.delete-rows .btn-danger')?.disabled === true`), null)
before = snapshot()
await key('Escape')
await waitFor(`!document.querySelector('.delete-rows')`)
sameExcept(before, [], 'Esc: nothing deleted')
check('13 rows still there', sql(`select count(*) from spydb_ui_test`) === '13', null)
check('the other table is still untouched', parentSnapshot() === parentBefore, null)


// ===== More: paging, filtered edits, duplicates without a key, a two-column key, conflicts among several, the multi-row JSON view, foreign keys, query results =====
const tableSnap = (table, order) => Object.fromEntries(sql(`select ${order} || E'\t' || row_to_json(t)::text from ${table} t order by ${order}`).split('\n').filter(Boolean).map((l) => l.split('\t')))
const diffOf = (before, after) => [...new Set([...Object.keys(before), ...Object.keys(after)])].filter((k) => before[k] !== after[k]).sort((a, b) => a - b)
const onlyThese = (what, table, order, before, ids) => { const diff = diffOf(before, tableSnap(table, order)); check(`${what}: of ${table}, only ${ids.length ? ids.join(', ') : 'nothing'} changed`, JSON.stringify(diff) === JSON.stringify(ids.map(String).sort((a, b) => a - b)), diff) }
const openTable = async (name) => { await click(`[...document.querySelectorAll('.tree-item')].find((b) => b.innerText.includes('${name}'))`); await waitFor(`document.querySelector('.tab.active .tab-title')?.innerText === '${name}' && __t.grid()?.querySelectorAll('tbody tr').length > 0`); await evaluate(helpers) }
const idsShown = () => evaluate(`[...__t.grid().querySelectorAll('tbody tr')].map((tr) => tr.querySelector('td[data-col="0"]').innerText)`)
const deleteSelected = async (count) => { await rightClick(`__t.grid().querySelector('tbody tr.row-selected td.grid-rownum')`); await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.includes('Delete'))`); await waitFor(`!document.querySelector('.delete-rows .btn-danger')?.disabled`); await click(`document.querySelector('.delete-rows .btn-danger')`); await waitFor(`__t.bar().includes('Deleted ${count}')`) }
const saveAll = async () => { await key('s', MOD); await sleep(800) }
const editRow = async (id, column, value) => { await dblclick(`${rowBy(id)}.children[__t.col('${column}')]`); await key('a', MOD); await type(value); await key('Enter') }

// Paging: page 2 of 250 rows, delete 3 picked with ⌘; page 3, edit 2; exactly those, every time.
await openTable('spydb_ui_big')
check('250 rows: page 1 shows 100', (await idsShown()).length === 100 && (await evaluate(`__t.pane().querySelector('.grid-page-total')?.innerText`)) === 'of ~3', await evaluate(`__t.pane().querySelector('.grid-page-total')?.innerText`))
await click(`__t.pane().querySelector('.pager-btn[aria-label="Next page"]')`)
await waitFor(`__t.grid().querySelector('tbody tr td[data-col="0"]')?.innerText === '101'`)
let bigBefore = tableSnap('spydb_ui_big', 'id')
await click(numOf('105')); await clickWith(numOf('150'), MOD); await clickWith(numOf('199'), MOD)
await deleteSelected(3)
onlyThese('page 2, delete 3 apart', 'spydb_ui_big', 'id', bigBefore, [105, 150, 199])
check('…247 rows left', sql('select count(*) from spydb_ui_big') === '247', null)
await evaluate(`(() => { const i = __t.pane().querySelector('.page-input'); i.focus(); i.select() })()`); await type('3'); await key('Enter')
await waitFor(`Number(__t.grid().querySelector('tbody tr td[data-col="0"]')?.innerText) > 200`)
const page3 = await idsShown()
bigBefore = tableSnap('spydb_ui_big', 'id')
await editRow(page3[0], 'val', '-1'); await editRow(page3[page3.length - 1], 'val', '-2')
await saveAll()
onlyThese('page 3, edit the first and last rows', 'spydb_ui_big', 'id', bigBefore, [page3[0], page3[page3.length - 1]])
check('…with their own values', sql(`select string_agg(val::text, ',' order by id) from spydb_ui_big where id in (${page3[0]}, ${page3[page3.length - 1]})`) === '-1,-2', null)
// A filter, then edits and a delete inside it: only rows that match are touched.
await click(`__t.pane().querySelector('.pager-btn[aria-label="Previous page"]')`); await click(`__t.pane().querySelector('.pager-btn[aria-label="Previous page"]')`)
await waitFor(`__t.grid().querySelector('tbody tr td[data-col="0"]')?.innerText === '1'`)
await setFilter(0, 'grp', 'eq', 'B'); await apply()
await waitFor(`!__t.pane().querySelector('.grid-wrap.loading')`)
const bs = await idsShown()
check('filter grp = B: exactly the B rows', bs.join(',') === sql(`select string_agg(id::text, ',' order by id) from (select id from spydb_ui_big where grp = 'B' order by id limit 100) q`), null)
bigBefore = tableSnap('spydb_ui_big', 'id')
await editRow(bs[1], 'val', '777'); await editRow(bs[5], 'val', '888')
await saveAll()
onlyThese('edits inside a filter', 'spydb_ui_big', 'id', bigBefore, [bs[1], bs[5]])
bigBefore = tableSnap('spydb_ui_big', 'id')
await click(numOf(bs[2])); await clickWith(numOf(bs[3]), SHIFT)
await deleteSelected(2)
onlyThese('a delete inside a filter', 'spydb_ui_big', 'id', bigBefore, [bs[2], bs[3]])
await click(`[...__t.pane().querySelectorAll('.filter-bar .btn')].find((b) => b.innerText === 'Clear')`)
await waitFor(`__t.grid().querySelector('tbody tr td[data-col="0"]')?.innerText === '1'`)

// No primary key, identical rows: the one picked goes, the others stay; an edit changes one.
await openTable('spydb_ui_nokey')
const nokey = () => sql(`select string_agg(status || n, ',' order by status, n) from spydb_ui_nokey`)
check('3 identical a|1 rows to start', nokey() === 'a1,a1,a1,b2,b2,c3', nokey())
const nokeyRows = () => evaluate(`[...__t.grid().querySelectorAll('tbody tr')].map((tr) => tr.querySelector('td[data-col="0"]').innerText + tr.querySelector('td[data-col="1"]').innerText)`)
const aRows = (await nokeyRows()).map((t, i) => [t, i]).filter(([t]) => t === 'a1').map(([, i]) => i)
await click(`__t.grid().querySelectorAll('tbody tr')[${aRows[1]}].querySelector('td.grid-rownum')`)
await deleteSelected(1)
check('delete one of three identical rows: two remain', nokey() === 'a1,a1,b2,b2,c3', nokey())
const aRows2 = (await nokeyRows()).map((t, i) => [t, i]).filter(([t]) => t === 'a1').map(([, i]) => i)
await dblclick(`__t.grid().querySelectorAll('tbody tr')[${aRows2[0]}].children[__t.col('n')]`); await key('a', MOD); await type('9'); await key('Enter')
await saveAll()
check('edit one of two identical rows: one changes', nokey() === 'a1,a9,b2,b2,c3', nokey())

// A two-column key: the dialog names both parts; only that row goes or changes.
await openTable('spydb_ui_pair')
const pairs = () => sql(`select string_agg(a::text || b::text || ':' || note, ',' order by a, b) from spydb_ui_pair`)
const pairRow = (a, b) => `[...__t.grid().querySelectorAll('tbody tr')].find((tr) => tr.querySelector('td[data-col="0"]').innerText === '${a}' && tr.querySelector('td[data-col="1"]').innerText === '${b}')`
await rightClick(`${pairRow(1, 2)}.querySelector('td.grid-rownum')`)
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.includes('Delete'))`)
check('the dialog names a = 1, b = 2', await waitFor(`document.querySelector('.delete-rows')?.innerText.includes('a = 1, b = 2')`), await dialogText())
await waitFor(`!document.querySelector('.delete-rows .btn-danger')?.disabled`)
await click(`document.querySelector('.delete-rows .btn-danger')`); await waitFor(`__t.bar().includes('Deleted 1')`)
check('only (1,2) went; (1,1) and (1,3) with the same a stay', pairs() === '11:n11,13:n13,21:n21,22:n22,23:n23,31:n31,32:n32,33:n33', pairs())
await dblclick(`${pairRow(1, 3)}.children[__t.col('note')]`); await key('a', MOD); await type('changed'); await key('Enter')
await saveAll()
check('edit (1,3): only it', pairs() === '11:n11,13:changed,21:n21,22:n22,23:n23,31:n31,32:n32,33:n33', pairs())

// Conflicts among several: one row changed behind the grid's back refuses the whole save, and the whole delete.
await openTable('spydb_ui_test')
await editRow('1', 'name', 'mine 1'); await editRow('2', 'name', 'mine 2')
sql(`update spydb_ui_test set name = 'theirs' where id = 2`)
before = snapshot()
await saveAll()
check('a save of 2 where 1 changed meanwhile is refused, naming it', await waitFor(`__t.bar().includes('2 unsaved changes') && __t.pane().querySelector('.changes-error')?.innerText.includes('id = 2')`), await evaluate(`__t.pane().querySelector('.changes-error')?.innerText`))
sameExcept(before, [], 'refused save: nothing written, not even row 1')
await click(`[...__t.pane().querySelectorAll('.changes-bar .btn')].find((b) => b.innerText === 'Discard')`)
await click(`__t.pane().querySelector('.grid-pager [aria-label="Reload rows"], .grid-pager .sidebar-icon-btn')`); await sleep(600)
await click(numOf('4')); await clickWith(numOf('5'), MOD)
sql(`update spydb_ui_test set n = 999 where id = 5`)
await rightClick(numOf('5'))
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.includes('Delete 2'))`)
await waitFor(`!document.querySelector('.delete-rows .btn-danger')?.disabled`)
before = snapshot()
await click(`document.querySelector('.delete-rows .btn-danger')`)
check('a delete of 2 where 1 changed meanwhile is refused, naming the column', await waitFor(`document.querySelector('.delete-rows')?.innerText.includes('changed by someone else') && document.querySelector('.delete-rows')?.innerText.includes('(n)')`), await dialogText())
await key('Escape'); await waitFor(`!document.querySelector('.delete-rows')`)
sameExcept(before, [], 'refused delete: nothing deleted')
check('both rows still there', sql(`select count(*) from spydb_ui_test where id in (4, 5)`) === '2', null)
await click(`__t.pane().querySelector('.grid-pager .sidebar-icon-btn')`); await sleep(600)

// The JSON view of 2 rows: change one value in each, Apply, save: exactly those 2.
await click(numOf('1')); await clickWith(numOf('7'), MOD)
await rightClick(numOf('7'))
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.startsWith('View 2 rows as JSON'))`)
await waitFor(`document.querySelector('.json-modal .cm-content')`)
const two = JSON.parse(await evaluate(`document.querySelector('.json-modal .cm-content').cmTile.view.state.doc.toString()`))
two[0].n = 1111; two[1].n = 7777
await evaluate(`(() => { const v = document.querySelector('.json-modal .cm-content').cmTile.view; v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: ${JSON.stringify(JSON.stringify(two, null, 2))} } }) })()`)
await sleep(600)
await click(`[...document.querySelectorAll('.json-modal .btn-primary')].find((b) => b.innerText === 'Apply')`)
await waitFor(`!document.querySelector('.json-modal')`)
check('the JSON view queued 2 changes in 2 rows', (await evaluate(`__t.bar()`)).includes('2 unsaved changes in 2 rows'), await evaluate(`__t.bar()`))
before = snapshot()
await saveAll()
sameExcept(before, [1, 7], 'JSON view of 2 rows saved')
check('…with the values typed', sql(`select string_agg(n::text, ',' order by id) from spydb_ui_test where id in (1, 7)`) === '1111,7777', null)

// Foreign keys: a parent still pointed at can't go (Postgres refuses; nothing changes); one no longer pointed at can.
await openTable('spydb_ui_parent')
const parentNow = () => tableSnap('spydb_ui_parent', 'id')
let parentSnap = parentNow()
await rightClick(numOf('1'))
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.includes('Delete'))`)
await waitFor(`!document.querySelector('.delete-rows .btn-danger')?.disabled`)
await click(`document.querySelector('.delete-rows .btn-danger')`)
check('deleting a parent that rows point at is refused by Postgres, shown in the dialog', await waitFor(`/foreign key|still referenced|violates/i.test(document.querySelector('.delete-rows')?.innerText ?? '')`), await dialogText())
await key('Escape'); await waitFor(`!document.querySelector('.delete-rows')`)
onlyThese('refused: the parent table is untouched', 'spydb_ui_parent', 'id', parentSnap, [])
parentSnap = parentNow()
await rightClick(numOf('6'))
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.includes('Delete'))`)
await waitFor(`!document.querySelector('.delete-rows .btn-danger')?.disabled`)
await click(`document.querySelector('.delete-rows .btn-danger')`); await waitFor(`__t.bar().includes('Deleted 1')`)
onlyThese('a parent nothing points at any more goes', 'spydb_ui_parent', 'id', parentSnap, [6])

// ===== Cell ranges and tab dragging: drag down a column (or Shift+↓); Backspace sets every cell to NULL, a paste fills them, ⌘C copies them; only those cells change. =====
await openTable('spydb_ui_test')
await click(`__t.pane().querySelector('.grid-pager [aria-label="Reload rows"]')`); await sleep(600)
await evaluate(CAPTURE_COPIES) // what ⌘C copies lands in window.__copied
const paste = async (text) => { await evaluate(`(() => { const dt = new DataTransfer(); dt.setData('text/plain', ${JSON.stringify(text)}); document.activeElement.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })) })()`); await sleep(300) }
const inRange = () => evaluate(`__t.grid().querySelectorAll('td.cell-in-range').length`)
const changed = () => evaluate(`__t.grid().querySelectorAll('td.cell-changed').length`)
const toast = () => evaluate(`document.querySelector('.copy-toast')?.innerText ?? ''`)
const cellAt = (row, name) => `__t.grid().querySelectorAll('tbody tr')[${row}].children[__t.col('${name}')]`
const [r1, r2, r3, r4] = await idsShown()
check('spydb_ui_test reloaded: 13 rows', (await idsShown()).length === 13, await idsShown())
await p.drag(cell(r1, 'n'), cell(r3, 'n'))
check('dragging down 3 cells of n: the first is selected, the 2 below are in the range', (await inRange()) === 2 && await evaluate(`${cell(r1, 'n')}.classList.contains('cell-selected') && ${cell(r2, 'n')}.classList.contains('cell-in-range') && ${cell(r3, 'n')}.classList.contains('cell-in-range')`), await inRange())
check('…and no text got selected on the way', (await evaluate(`window.getSelection().toString()`)) === '', await evaluate(`window.getSelection().toString()`))
before = snapshot()
await key('Backspace')
check('Backspace sets the 3 cells to NULL, unsaved', (await toast()) === '3 cells set to NULL (unsaved)' && (await changed()) === 3 && (await evaluate(`${cell(r2, 'n')}.innerText`)) === 'NULL' && (await evaluate(`__t.bar()`)).includes('3 unsaved'), [await toast(), await changed(), await evaluate(`__t.bar()`)])
sameExcept(before, [], 'nothing is written until saved')
await saveAll()
sameExcept(before, [r1, r2, r3], '3 cells NULL, saved')
check('…n is NULL in exactly those rows', trueIds('n is null') === [r1, r2, r3].join(','), trueIds('n is null'))
// A paste fills the range: one value into all 4 cells.
await p.drag(cell(r1, 'n'), cell(r4, 'n'))
await paste('42')
check('pasting 42 into a range of 4 fills all 4, unsaved', (await toast()) === 'Pasted into 4 cells (unsaved)' && (await changed()) === 4 && (await evaluate(`${cell(r4, 'n')}.innerText`)) === '42', [await toast(), await changed()])
before = snapshot()
await saveAll()
sameExcept(before, [r1, r2, r3, r4], 'pasted into 4 cells, saved')
check('…n = 42 in exactly those rows', trueIds('n = 42') === [r1, r2, r3, r4].join(','), trueIds('n = 42'))
// Shift+↓ grows the range from the keyboard; 3 pasted lines go cell by cell.
await click(cell(r1, 'name')); await key('ArrowDown', SHIFT); await key('ArrowDown', SHIFT)
check('Shift+↓ twice: a range of 3 cells', (await inRange()) === 2 && await evaluate(`${cell(r3, 'name')}.classList.contains('cell-in-range')`), await inRange())
await paste('first\nsecond\nthird\n')
const names3 = () => evaluate(`[${cell(r1, 'name')}, ${cell(r2, 'name')}, ${cell(r3, 'name')}].map((c) => c.innerText).join('|')`)
check('3 pasted lines land in the 3 cells, in order', (await changed()) === 3 && (await names3()) === 'first|second|third', [await changed(), await names3()])
before = snapshot()
await saveAll()
sameExcept(before, [r1, r2, r3], '3 pasted lines, saved')
check('…the names in Postgres', sql(`select string_agg(name, ',' order by id) from spydb_ui_test where id in (${r1}, ${r2}, ${r3})`) === 'first,second,third', null)
// ⌘C copies the range as lines of tab-separated values; Esc lets go of it.
await p.drag(cell(r1, 'name'), cell(r2, 'n'))
await key('c', MOD)
check('⌘C copies a 2×2 range as tab-separated lines', (await toast()) === 'Copied 4 cells' && (await evaluate(`window.__copied`)) === 'first\t42\nsecond\t42', [await toast(), await evaluate(`window.__copied`)])
await key('Escape')
check('Esc clears the range, the cell stays selected', (await inRange()) === 0 && (await evaluate(`__t.grid().querySelectorAll('td.cell-selected').length`)) === 1, null)
// NULL into the key column: Postgres refuses, and nothing at all is written.
await p.drag(cellAt(0, 'id'), cellAt(1, 'id')); await key('Backspace')
check('2 id cells set to NULL, unsaved', (await changed()) === 2 && (await evaluate(`${cellAt(0, 'id')}.innerText`)) === 'NULL', await changed())
before = snapshot()
await saveAll()
check('NULL in the primary key: refused by Postgres', await waitFor(`/null value|not-null|violates/i.test(__t.pane().querySelector('.changes-error')?.innerText ?? '')`), await evaluate(`__t.pane().querySelector('.changes-error')?.innerText`))
sameExcept(before, [], 'refused: nothing written')
await click(`[...__t.pane().querySelectorAll('.changes-bar .btn')].find((b) => b.innerText === 'Discard')`)
check('Discard: the ids are back', (await changed()) === 0 && (await evaluate(`${cellAt(0, 'id')}.innerText`)) === String(r1), await evaluate(`${cellAt(0, 'id')}.innerText`))

// Tabs drag into another order, which stays when switching tabs.
const tabTitles = () => evaluate(`[...document.querySelectorAll('.tabbar .tab .tab-title')].map((t) => t.innerText)`)
const tabOf = (title) => `[...document.querySelectorAll('.tabbar .tab')].find((t) => t.querySelector('.tab-title')?.innerText === '${title}')`
const titles = await tabTitles()
const others = titles.filter((t) => t !== 'spydb_ui_test')
check('several tabs are open', titles.length >= 4 && titles.includes('spydb_ui_test'), titles)
await p.drag(tabOf('spydb_ui_test'), `document.querySelectorAll('.tabbar .tab')[${titles.length - 1}]`, { dx: 20 })
check('dragging spydb_ui_test past the last tab puts it last', JSON.stringify(await tabTitles()) === JSON.stringify([...others, 'spydb_ui_test']), await tabTitles())
await click(tabOf(others[0])); await sleep(300)
await click(tabOf('spydb_ui_test')); await evaluate(helpers)
check('…its rows are still there after switching away and back', await waitFor(`document.querySelector('.tab.active .tab-title')?.innerText === 'spydb_ui_test'`) && (await idsShown()).length === 13, await idsShown())
check('the order stays after switching tabs', JSON.stringify(await tabTitles()) === JSON.stringify([...others, 'spydb_ui_test']), await tabTitles())
await p.drag(tabOf('spydb_ui_test'), `document.querySelector('.tabbar .tab')`, { dx: -20 })
check('dragged before the first tab: first', JSON.stringify(await tabTitles()) === JSON.stringify(['spydb_ui_test', ...others]), await tabTitles())
await shot(`${shots}/p9-range.png`)

// Query results: 3 rows edited in a SELECT's results save to exactly those rows; a JOIN's results are read-only.
await key('t', MOD)
await waitFor(`document.querySelector('.tab.active .tab-title')?.innerText.startsWith('Query')`)
await evaluate(helpers)
await click(`__t.pane().querySelector('.cm-content')`); await type('SELECT id, name, n FROM spydb_ui_test ORDER BY id'); await key('Enter', MOD)
await waitFor(`__t.grid()?.querySelectorAll('tbody tr').length === 13`)
before = snapshot()
for (const id of ['1', '8', '20']) { await dblclick(cell(id, 'name')); await key('a', MOD); await type(`result ${id}`); await key('Enter') }
await saveAll()
sameExcept(before, [1, 8, 20], 'three result rows saved')
check('…with their values', sql(`select string_agg(name, ',' order by id) from spydb_ui_test where id in (1, 8, 20)`) === 'result 1,result 8,result 20', null)
await click(`__t.pane().querySelector('.cm-content')`); await key('a', MOD); await type('SELECT t.id, t.name, p.label FROM spydb_ui_test t JOIN spydb_ui_parent p ON p.id = t.parent_id ORDER BY t.id'); await key('Enter', MOD)
await waitFor(`__t.grid()?.querySelectorAll('tbody tr').length === 13`)
check('a JOIN’s results are read-only, with the reason', await waitFor(`__t.pane().innerText.includes('read-only') || __t.pane().innerText.includes('Read-only')`), null)
await p.drag(cellAt(0, 'name'), cellAt(2, 'name'))
await key('Backspace')
check('Backspace on a range in read-only results changes nothing', (await toast()) === 'These columns can’t be edited' && (await changed()) === 0, [await toast(), await changed()])
check('the other tables ended as they should', sql('select count(*) from spydb_ui_big') === '245' && nokey() === 'a1,a9,b2,b2,c3', [sql('select count(*) from spydb_ui_big'), nokey()])

check('no page errors', p.errors.length === 0, p.errors)
}
