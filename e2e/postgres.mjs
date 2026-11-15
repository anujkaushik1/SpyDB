// Postgres end to end: connect, edit a cell, the JSON view, delete (with its
// check of what else goes), a query tab and an edit in its results. Only
// the table spydb_ui_test, which run.mjs makes (and drops) in the test
// database.
import { check, MOD, sleep } from './cdp.mjs'

export default async function postgres(p, { shots, pg, sql }) {
const { evaluate, click, dblclick, rightClick, key, type, waitFor, shot } = p
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
await setField('Host', 'localhost'); await setField('Port', '5432'); await setField('User', pg.user); await setField('Database', pg.db)
if (pg.password) await setField('Password', pg.password)
await click(`[...document.querySelectorAll('button')].find((b) => b.innerText.trim() === 'Connect')`)
check('connected', await waitFor(`[...document.querySelectorAll('.tree-item')].some((b) => b.innerText.includes('spydb_ui_test'))`, 15000), null)
await click(`[...document.querySelectorAll('.tree-item')].find((b) => b.innerText.includes('spydb_ui_test'))`)
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
await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete row…')`)
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
check('no page errors', p.errors.length === 0, p.errors)
}
