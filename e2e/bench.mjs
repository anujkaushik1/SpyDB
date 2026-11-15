// SpyDB's DynamoDB benchmark: the real app (its server build) in headless
// Chrome against DynamoDB Local, timed from the click to the screen showing
// the result, and checked against DynamoDB Local itself after every step
// (the rows shown are the items there; a save or delete changed exactly
// that item; a filter found every match). Prints a Markdown table.
//
//   node e2e/bench.mjs            (E2E_DDB: DynamoDB Local, default http://127.0.0.1:8000;
//                                  E2E_BENCH_ITEMS: table size, default 2000; E2E_BENCH_OUT: also write the table there)
//
// Needs the server build (go build -tags server -o bin/spydb-server .) and
// Chrome, like e2e/run.mjs. Only the table spydb_bench in DynamoDB Local is
// written; nothing here reaches AWS.
import { writeFileSync } from 'node:fs'
import { hostname, cpus, totalmem, release, platform } from 'node:os'
import { MOD, open, sleep } from './cdp.mjs'
import { ddb, getItem } from './dynamo-local.mjs'
import { checkServerBuild, debugPort, port, startChrome, startServer, waitForServer } from './harness.mjs'

const TABLE = 'spydb_bench'
const N = Number(process.env.E2E_BENCH_ITEMS ?? 2000)
const CITIES = ['Delhi', 'Mumbai', 'Pune', 'Agra', 'Goa']
const env = process.env

// ----- The table: N orders, userId (text) + createdAt (number) -------------------
async function seed() {
  try { await ddb('DeleteTable', { TableName: TABLE }) } catch { /* not there */ }
  await ddb('CreateTable', {
    TableName: TABLE, BillingMode: 'PAY_PER_REQUEST',
    AttributeDefinitions: [{ AttributeName: 'userId', AttributeType: 'S' }, { AttributeName: 'createdAt', AttributeType: 'N' }],
    KeySchema: [{ AttributeName: 'userId', KeyType: 'HASH' }, { AttributeName: 'createdAt', KeyType: 'RANGE' }],
  })
  const items = []
  for (let i = 0; i < N; i++) {
    items.push({
      userId: { S: `u${String(i).padStart(5, '0')}` }, createdAt: { N: String(1731400000 + i) },
      city: { S: CITIES[i % 5] }, amount: { N: `${100 + i}.50` }, paid: { BOOL: i % 2 === 0 },
      tags: { SS: ['new', `t${i % 3}`] }, address: { M: { pin: { N: String(110001 + i) }, lines: { L: [{ S: 'line 1' }] } } },
      ...(i % 10 === 0 ? { note: { S: `note ${i}` } } : {}),
    })
  }
  for (let i = 0; i < items.length; i += 25) {
    await ddb('BatchWriteItem', { RequestItems: { [TABLE]: items.slice(i, i + 25).map((Item) => ({ PutRequest: { Item } })) } })
  }
}
async function scanAll(extra = {}) {
  const out = []
  let start
  do {
    const page = await ddb('Scan', { TableName: TABLE, ConsistentRead: true, ...extra, ...(start && { ExclusiveStartKey: start }) })
    out.push(...page.Items)
    start = page.LastEvaluatedKey
  } while (start)
  return out
}
const item = (userId) => getItem(TABLE, { userId: { S: userId }, createdAt: { N: String(1731400000 + Number(userId.slice(1))) } })

// ----- The page --------------------------------------------------------------------
const helpers = `window.__t = {
  pane: () => [...document.querySelectorAll('.tab-panel')].find((e) => !e.hidden),
  grid: (where) => { const pane = __t.pane(); return where === 'results' ? pane.querySelector('.results-pane:not([hidden]) .grid-wrap, .playground-results .grid-wrap') : pane.querySelector('.rows-pane .grid-wrap') },
  col: (where, name) => [...__t.grid(where).querySelectorAll('thead th')].findIndex((th) => th.querySelector('.grid-col-name')?.innerText === name),
  row: (where, key) => [...__t.grid(where).querySelectorAll('tbody tr')].find((tr) => tr.children[1]?.innerText === key),
  rows: (where) => __t.grid(where)?.querySelectorAll('tbody tr').length ?? -1,
  keys: (where) => [...(__t.grid(where)?.querySelectorAll('tbody tr') ?? [])].map((tr) => tr.children[1]?.innerText),
  cell: (where, key, name) => __t.row(where, key)?.children[__t.col(where, name)],
  bar: () => __t.pane().querySelector('.changes-bar')?.innerText ?? '',
  status: () => __t.pane().querySelector('.grid-footer .grid-status')?.innerText ?? '',
  aws: () => { const m = /(\\d+) ms/.exec(__t.status()); return m ? Number(m[1]) : null },
  // arm(): the next click or key press stamps __t.t0, so a measure starts at the input itself.
  arm: () => { __t.t0 = null; const stamp = (e) => { if (__t.t0 === null) __t.t0 = e.timeStamp }; for (const ev of ['mousedown', 'keydown']) window.addEventListener(ev, stamp, { capture: true, once: true }) },
  setValue: (el, value) => { const set = Object.getOwnPropertyDescriptor(el.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype, 'value').set; set.call(el, value); el.dispatchEvent(new Event(el.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true })) },
}`

checkServerBuild()
console.log(`Seeding ${N} items into DynamoDB Local (${TABLE})…`)
await seed()
const app = startServer('bench')
const browser = startChrome('bench')
const rows = [] // the table printed at the end
let problems = 0
const median = (xs) => { const s = [...xs].sort((a, b) => a - b); return s[Math.floor(s.length / 2)] }
const fmt = (ms) => ms == null ? '—' : ms < 10 ? ms.toFixed(1) : String(Math.round(ms))
// A line of the table: what was timed, the time from the click to the screen
// (median of the runs), what DynamoDB Local itself took, and the check that passed.
function record(what, screenMs, awsMs, ok, checked) {
  if (!ok) problems++
  rows.push({ what, screen: fmt(median(screenMs)), aws: awsMs.length ? fmt(median(awsMs)) : '—', runs: screenMs.length, ok, checked })
  console.log(`${ok ? 'OK  ' : 'FAIL'}  ${what}: ${fmt(median(screenMs))} ms on screen${awsMs.length ? `, ${fmt(median(awsMs))} ms in DynamoDB Local` : ''} (${screenMs.length} run${screenMs.length === 1 ? '' : 's'}) · ${checked}`)
}

try {
  await waitForServer()
  const p = await open(debugPort, `http://localhost:${port}/`)
  const { send, evaluate, click, dblclick, rightClick, key, type, waitFor } = p
  // A watcher in the page that notes the time of the first frame in which
  // `js` is true (60 s at most); it starts now, so it misses nothing.
  const watch = (js) => send('Runtime.evaluate', { expression: `window.__watch = new Promise((done) => { const t0 = performance.now(); const tick = () => { let hit = false; try { hit = !!(${js}) } catch { /* not on screen yet */ } if (hit) done(performance.now()); else if (performance.now() - t0 > 60000) done(NaN); else requestAnimationFrame(tick) }; tick() })`, awaitPromise: false })
  const until = async (js) => { await watch(js); return evaluate(`__watch`) }
  // From the click or key press itself (stamped in the page by arm()) to the
  // frame in which the page shows `js`: the time a person waits, in ms.
  const measure = async (act, js) => {
    await evaluate(`__t.arm()`)
    await watch(js)
    await act()
    const end = await evaluate(`__watch`)
    const t0 = await evaluate(`__t.t0`)
    return t0 === null ? NaN : end - t0
  }

  await evaluate(helpers)
  // Sign in (the fake aws CLI's demo profile; the data is DynamoDB Local's).
  await click(`[...document.querySelectorAll('.rail-engine')].find((b) => b.innerText.trim() === 'Dy')`)
  await waitFor(`document.querySelector('.aws-card .card-footer .btn-primary')`)
  const signIn = await measure(() => click(`document.querySelector('.aws-card .card-footer .btn-primary')`), `document.querySelectorAll('.dynamo-sidebar .tree-item').length > 0`)
  record('Sign in and list the tables', [signIn], [], await evaluate(`[...document.querySelectorAll('.dynamo-sidebar .tree-item')].some((b) => b.innerText.includes('${TABLE}'))`), 'the table is listed')

  // 1. Open the table: a fresh tab each time, its first page of 50.
  const openTable = () => click(`[...document.querySelectorAll('.dynamo-sidebar .tree-item')].find((b) => b.innerText.includes('${TABLE}'))`)
  const opens = []
  const aws = []
  for (let i = 0; i < 5; i++) {
    opens.push(await measure(openTable, `__t.rows('items') === 50`))
    aws.push(await evaluate(`__t.aws()`))
    if (i < 4) { await click(`document.querySelector('.tab.active .tab-close')`); await until(`!document.querySelector('.tab')`) }
  }
  let keys = await evaluate(`__t.keys('items')`)
  // Each shown row is an item in the table, with the city it holds.
  const cities = await Promise.all(keys.map((k) => evaluate(`__t.cell('items', '${k}', 'city')?.innerText`)))
  const real = await Promise.all(keys.map(item))
  let ok = keys.length === 50 && real.every((it, i) => it && it.city.S === cities[i])
  record('Open a table: its first 50 items on screen', opens, aws, ok, '50 rows shown, each an item in the table with its city')

  // 2. Load more: the next 50, five times.
  const more = []
  for (let i = 1; i <= 5; i++) {
    more.push(await measure(() => click(`[...__t.pane().querySelectorAll('.grid-pager .btn')].find((b) => b.innerText.includes('Load more'))`), `__t.rows('items') === ${50 + 50 * i}`))
  }
  keys = await evaluate(`__t.keys('items')`)
  ok = keys.length === 300 && new Set(keys).size === 300 && (await Promise.all(keys.map(item))).every(Boolean)
  record('Load more: the next 50 items', more, [], ok, '300 rows, all different, all items in the table')

  // 3. Filter by the key: one item straight from its key (a Query, not a scan).
  const setFilter = async (field, value) => {
    await click(`__t.pane().querySelector('.filter-bar .field-picker-btn')`)
    await type(field)
    await key('Enter')
    await waitFor(`__t.pane().querySelector('.filter-bar .field-picker-btn').dataset.value === '${field}'`)
    await evaluate(`__t.setValue(__t.pane().querySelector('.filter-bar .filter-value'), '${value}')`)
    await click(`__t.pane().querySelector('.filter-bar .filter-value')`)
  }
  const targets = [0, 1, 2, 3, 4].map((i) => `u${String(Math.floor(N / 2) + i).padStart(5, '0')}`)
  const target = targets[0]
  const keyFilter = []
  const awsKey = []
  for (const t of targets) { // a different key each run, so the screen has to change
    await setFilter('userId', t)
    keyFilter.push(await measure(() => key('Enter'), `__t.rows('items') === 1 && __t.keys('items')[0] === '${t}'`))
    awsKey.push(await evaluate(`__t.aws()`))
  }
  await setFilter('userId', target)
  await key('Enter')
  await until(`__t.keys('items')[0] === '${target}'`)
  const found = await item(target)
  ok = found && (await evaluate(`__t.cell('items', '${target}', 'amount')?.innerText`)) === found.amount.N.replace(/0$/, '') && (await evaluate(`__t.status()`)).includes('key')
  record(`Filter by the key (userId = ${target})`, keyFilter, awsKey, !!ok, 'the one item, with its amount, read by its key')

  // 4. Filter on a plain field: a scan, 50 items checked per page, Load more until all are checked.
  await setFilter('city', 'Goa')
  const scanFirst = await measure(() => key('Enter'), `/found · 50 checked/.test(__t.status())`)
  let pages = 1
  const morePages = []
  while (!(await evaluate(`__t.status().includes('all of them')`))) {
    const checked = (pages + 1) * 50
    morePages.push(await measure(() => click(`[...__t.pane().querySelectorAll('.grid-pager .btn')].find((b) => b.innerText.includes('Load more'))`),
      `__t.status().includes((${checked}).toLocaleString() + ' checked') || __t.status().includes('all of them')`))
    pages++
    if (pages > N / 50 + 5) break
  }
  const goa = await scanAll({ FilterExpression: 'city = :c', ExpressionAttributeValues: { ':c': { S: 'Goa' } } })
  const shownGoa = await evaluate(`__t.keys('items')`)
  ok = shownGoa.length === goa.length && new Set(shownGoa).size === goa.length && goa.every((it) => shownGoa.includes(it.userId.S))
  record(`Filter on a field (city = Goa): first page of matches`, [scanFirst], [await evaluate(`__t.aws()`)], ok, `${shownGoa.length} found of ${N}, the same ${goa.length} a direct scan finds`)
  record(`…and Load more with the filter: the next 50 checked (${pages - 1} pages, until every item was)`, morePages, [], ok, 'every match shown once, none missing')
  await click(`[...__t.pane().querySelectorAll('.filter-bar .btn')].find((b) => b.innerText === 'Clear')`)
  await until(`__t.rows('items') === 50 && !__t.pane().querySelector('.grid-wrap.loading')`)
  keys = await evaluate(`__t.keys('items')`)

  // 5. Edit a cell and save (⌘S): one item changes, nothing else.
  const saves = []
  const touched = []
  for (let i = 0; i < 5; i++) {
    const k = keys[10 + i]
    await dblclick(`__t.cell('items', '${k}', 'city')`)
    await key('a', MOD)
    await type(`Bench${i}`)
    await key('Enter')
    saves.push(await measure(() => key('s', MOD), `__t.bar().includes('Saved 1 item')`))
    await until(`!__t.bar().includes('Saved')`)
    touched.push(k)
  }
  const all = await scanAll()
  ok = touched.every((k, i) => all.find((it) => it.userId.S === k)?.city.S === `Bench${i}`) && all.length === N
    && all.filter((it) => /^Bench\d$/.test(it.city.S)).length === 5
  record('Edit a cell and save', saves, [], ok, 'the 5 edited items hold the new value; the other items are untouched')

  // 6. Delete an item from the row menu.
  const deletes = []
  const gone = []
  for (let i = 0; i < 3; i++) {
    const k = keys[20 + i]
    await rightClick(`__t.row('items', '${k}').children[0]`)
    await click(`[...document.querySelectorAll('.row-menu button')].find((b) => b.innerText.trim() === 'Delete whole item…')`)
    await waitFor(`document.querySelector('.delete-rows .btn-danger')`)
    deletes.push(await measure(() => click(`document.querySelector('.delete-rows .btn-danger')`), `!__t.row('items', '${k}') && __t.bar().includes('Deleted 1 item')`))
    await until(`!__t.bar().includes('Deleted')`)
    gone.push(k)
  }
  const left = await scanAll()
  ok = left.length === N - 3 && gone.every((k) => !left.some((it) => it.userId.S === k))
  record('Delete an item', deletes, [], ok, `the 3 items are gone from the table, ${N - 3} remain`)

  // 7. PartiQL: a SELECT by key behind </>.
  await click(`__t.pane().querySelector('.filter-bar .sql-toggle')`)
  await waitFor(`__t.pane().querySelector('.sql-bar .cm-content')`)
  const partiql = []
  for (const t of targets) { // a different key each run
    await click(`__t.pane().querySelector('.sql-bar .cm-content')`)
    await key('a', MOD)
    await type(`SELECT * FROM "${TABLE}" WHERE "userId" = '${t}'`)
    partiql.push(await measure(() => key('Enter', MOD), `__t.rows('results') === 1 && __t.keys('results')[0] === '${t}'`))
  }
  await click(`__t.pane().querySelector('.sql-bar .cm-content')`)
  await key('a', MOD)
  await type(`SELECT * FROM "${TABLE}" WHERE "userId" = '${target}'`)
  await key('Enter', MOD)
  await until(`__t.keys('results')[0] === '${target}'`)
  record('PartiQL: SELECT one item by its key', partiql, [], (await evaluate(`__t.keys('results')[0]`)) === target, 'the one item comes back')

  // 8. The JSON view of an item.
  await click(`__t.pane().querySelector('.filter-bar .sql-toggle')`).catch(() => {})
  await evaluate(`__t.pane().querySelector('.sql-toggle.on')?.click()`)
  await until(`__t.rows('items') > 0 && !__t.pane().querySelector('.results-pane:not([hidden])')`)
  const json = []
  const k = (await evaluate(`__t.keys('items')`))[0]
  for (let i = 0; i < 5; i++) {
    json.push(await measure(() => dblclick(`__t.row('items', '${k}').children[0]`), `document.querySelector('.json-modal .cm-content')?.innerText.includes('"userId"')`))
    await key('Escape')
    await until(`!document.querySelector('.json-modal')`)
  }
  const shownJson = JSON.parse(await (async () => { await dblclick(`__t.row('items', '${k}').children[0]`); await until(`document.querySelector('.json-modal .cm-content')`); return evaluate(`document.querySelector('.json-modal .cm-content').innerText`) })())
  const realItem = await item(k)
  ok = shownJson.userId === k && String(shownJson.amount) === String(Number(realItem.amount.N)) && shownJson.address.pin === Number(realItem.address.M.pin.N)
  record('Open an item as JSON', json, [], ok, 'the JSON matches the item, numbers as numbers')
  await key('Escape')
  p.close()
} finally {
  app.kill()
  browser.kill()
  await sleep(500)
  try { await ddb('DeleteTable', { TableName: TABLE }) } catch { /* fine */ }
}

const machine = `${platform()} ${release()} · ${cpus()[0]?.model ?? ''} · ${Math.round(totalmem() / 2 ** 30)} GB`
const table = [
  `| What a person does | Screen (ms) | DynamoDB Local (ms) | Runs | Checked against DynamoDB Local |`,
  `|---|---:|---:|---:|---|`,
  ...rows.map((r) => `| ${r.what} | ${r.screen} | ${r.aws} | ${r.runs} | ${r.ok ? '✓' : '✗'} ${r.checked} |`),
].join('\n')
console.log(`\n${table}\n\nTable of ${N} items · ${machine} · ${new Date().toISOString().slice(0, 10)}`)
if (env.E2E_BENCH_OUT) writeFileSync(env.E2E_BENCH_OUT, `${table}\n\nTable of ${N} items · ${machine}\n`)
if (problems > 0) { console.log(`\n${problems} check${problems === 1 ? '' : 's'} FAILED`); process.exitCode = 1 }
