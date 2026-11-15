// SpyDB's end-to-end tests: the real app (its server build, which serves the
// same screens and Go code over HTTP) in headless Chrome, driven like a
// person would: clicks, typing, keys. The same on macOS, Ubuntu and Windows
// (.github/workflows/e2e.yml runs it on each).
//
//   node e2e/run.mjs [postgres] [dynamo] [mongo] …   (default: all of them)
//
// Needs, already running: Postgres (E2E_PG_USER, E2E_PG_DB, E2E_PG_PASSWORD;
// psql on the PATH or E2E_PSQL), DynamoDB Local (E2E_DDB, default
// http://127.0.0.1:8000) and MongoDB (E2E_MONGO_URL, default
// mongodb://127.0.0.1:27017; only spydb_test_* databases are written). The server build: E2E_SERVER (default
// bin/spydb-server), built with `go build -tags server`. Chrome: E2E_CHROME.
// Screenshots go to E2E_SHOTS (default a temporary folder). Each test gets a
// fresh app with its own empty settings folder, and a fake AWS CLI (fakeaws)
// that signs in to DynamoDB Local with a made-up account: nothing here
// touches your saved connections, AWS, or any database but the test ones.
import { execFileSync } from 'node:child_process'
import { mkdirSync, readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { failures, open, sleep } from './cdp.mjs'
import { seed } from './dynamo-local.mjs'
import { checkServerBuild, debugPort, port, root, startChrome, startServer, waitForServer, work } from './harness.mjs'

const env = process.env
const shots = env.E2E_SHOTS ? resolve(env.E2E_SHOTS) : join(work, 'shots')
mkdirSync(shots, { recursive: true })
const pg = { user: env.E2E_PG_USER ?? env.USER ?? 'postgres', db: env.E2E_PG_DB ?? 'postgres', password: env.E2E_PG_PASSWORD ?? '' }
const sql = (q) => execFileSync(env.E2E_PSQL ?? 'psql', ['-h', 'localhost', '-U', pg.user, '-d', pg.db, '-v', 'ON_ERROR_STOP=1', '-Atc', q],
  { encoding: 'utf8', env: { ...env, PGPASSWORD: pg.password } }).trim()

// What each test needs ready before the app opens, and puts back after.
const suites = {
  postgres: {
    before: () => sql(`DROP TABLE IF EXISTS spydb_ui_test; CREATE TABLE spydb_ui_test (id int PRIMARY KEY, name text, n int, flag boolean, j jsonb);
      INSERT INTO spydb_ui_test SELECT i, 'name ' || i, i * 10, i % 2 = 0, jsonb_build_object('k', i) FROM generate_series(1, 20) i`),
    after: () => sql('DROP TABLE IF EXISTS spydb_ui_test'),
  },
  dynamo: { before: seed, after: () => {} },
  mongo: {
    before: () => mongoseed('reset', 'spydb_test_e2e', 'spydb_test_e2e_other'),
    after: () => mongoseed('drop', 'spydb_test_e2e', 'spydb_test_e2e_other'),
  },
}
// MongoDB (E2E_MONGO_URL): mongoseed only ever touches spydb_test_* databases.
const mongoUrl = env.E2E_MONGO_URL ?? 'mongodb://127.0.0.1:27017'
function mongoseed(...args) {
  execFileSync('go', ['run', './e2e/mongoseed', mongoUrl, ...args], { cwd: root, stdio: 'inherit' })
}
const picked = process.argv.slice(2).length > 0 ? process.argv.slice(2) : Object.keys(suites)

checkServerBuild()
const results = []
for (const name of picked) {
  const suite = suites[name]
  if (!suite) throw new Error(`no test named ${name}; there are: ${Object.keys(suites).join(', ')}`)
  console.log(`\n=== ${name} (${process.platform})`)
  await suite.before()
  const app = startServer(name)
  const browser = startChrome(name)
  const before = failures()
  let stopped = false
  mkdirSync(join(shots, name), { recursive: true })
  try {
    await waitForServer()
    const page = await open(debugPort, `http://localhost:${port}/`)
    try {
      const test = (await import(`./${name}.mjs`)).default
      await test(page, { shots: join(shots, name), pg, sql, root, mongoUrl })
    } finally {
      page.close()
    }
  } catch (err) {
    console.log(`FAIL  ${name} stopped: ${err.stack ?? err}`)
    stopped = true
    process.exitCode = 1
  } finally {
    app.kill()
    browser.kill()
    await sleep(800)
    try { await suite.after() } catch (err) { console.log(`(couldn't clean up after ${name}: ${err.message})`) }
  }
  const failed = failures() - before
  if (stopped || failed > 0) {
    const log = readFileSync(join(work, name, 'app.log'), 'utf8').split('\n').filter((l) => !l.includes('AssetFileServerFS'))
    console.log(`--- the app's log (last 40 lines)\n${log.slice(-40).join('\n')}`)
  }
  results.push(`${name}: ${stopped ? 'stopped' : failed === 0 ? 'passed' : `${failed} failed`}`)
}
console.log(`\n${results.join(' · ')}\nScreenshots: ${shots}`)
if (failures() > 0) process.exitCode = 1
