// SpyDB's end-to-end tests: the real app (its server build, which serves the
// same screens and Go code over HTTP) in headless Chrome, driven like a
// person would: clicks, typing, keys. The same on macOS, Ubuntu and Windows
// (.github/workflows/e2e.yml runs it on each).
//
//   node e2e/run.mjs [postgres] [dynamo] …   (default: all of them)
//
// Needs, already running: Postgres (E2E_PG_USER, E2E_PG_DB, E2E_PG_PASSWORD;
// psql on the PATH or E2E_PSQL) and DynamoDB Local (E2E_DDB, default
// http://127.0.0.1:8000). The server build: E2E_SERVER (default
// bin/spydb-server), built with `go build -tags server`. Chrome: E2E_CHROME.
// Screenshots go to E2E_SHOTS (default a temporary folder). Each test gets a
// fresh app with its own empty settings folder, and a fake AWS CLI (fakeaws)
// that signs in to DynamoDB Local with a made-up account: nothing here
// touches your saved connections, AWS, or any database but the test ones.
import { execFileSync, spawn } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, openSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { delimiter, join, resolve } from 'node:path'
import { failures, open, sleep } from './cdp.mjs'
import { seed } from './dynamo-local.mjs'

const root = resolve(import.meta.dirname, '..')
const windows = process.platform === 'win32'
const exe = windows ? '.exe' : ''
const env = process.env
const server = resolve(env.E2E_SERVER ?? join(root, 'bin', `spydb-server${exe}`))
const chrome = env.E2E_CHROME ?? {
  darwin: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  win32: 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
}[process.platform] ?? 'google-chrome'
const port = Number(env.E2E_PORT ?? 9241)
const debugPort = port + 1
const work = mkdtempSync(join(tmpdir(), 'spydb-e2e-'))
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
}
const picked = process.argv.slice(2).length > 0 ? process.argv.slice(2) : Object.keys(suites)

// The fake AWS CLI and the profile it signs in with.
const fakeBin = join(work, 'bin')
execFileSync('go', ['build', '-o', join(fakeBin, `aws${exe}`), './e2e/fakeaws'], { cwd: root, stdio: 'inherit' })
mkdirSync(join(work, 'aws'))
writeFileSync(join(work, 'aws', 'config'), '[profile demo]\nregion = ap-south-1\n')
writeFileSync(join(work, 'aws', 'credentials'), '[demo]\naws_access_key_id = AKIADEMOEXAMPLE\naws_secret_access_key = demo-secret\n')

function startServer(name) {
  const home = join(work, name, 'home')
  mkdirSync(home, { recursive: true })
  const appEnv = Object.fromEntries(Object.entries(env).filter(([k]) => !/^AWS_/.test(k)))
  Object.assign(appEnv, {
    PATH: fakeBin + delimiter + (env.PATH ?? env.Path ?? ''),
    HOME: home, USERPROFILE: home, XDG_CONFIG_HOME: join(home, '.config'), AppData: join(home, 'AppData'), // settings stay in here
    AWS_CONFIG_FILE: join(work, 'aws', 'config'), AWS_SHARED_CREDENTIALS_FILE: join(work, 'aws', 'credentials'),
    AWS_EC2_METADATA_DISABLED: 'true', AWS_ENDPOINT_URL_DYNAMODB: env.E2E_DDB ?? 'http://127.0.0.1:8000',
    WAILS_SERVER_PORT: String(port),
  })
  if (windows) delete appEnv.Path // one PATH
  const log = openSync(join(work, name, 'app.log'), 'w') // shown if the test fails
  return spawn(server, [], { cwd: root, env: appEnv, stdio: ['ignore', log, log] })
}

function startChrome(name) {
  const flags = ['--headless=new', `--remote-debugging-port=${debugPort}`, `--user-data-dir=${join(work, name, 'chrome')}`,
    '--no-first-run', '--no-default-browser-check', '--disable-extensions', '--window-size=1280,860', 'about:blank']
  if (process.platform === 'linux') flags.unshift('--no-sandbox')
  return spawn(chrome, flags, { stdio: 'ignore' })
}

async function waitForServer() {
  for (let i = 0; i < 120; i++) {
    try { if ((await fetch(`http://localhost:${port}/`)).ok) return } catch { /* not up yet */ }
    await sleep(250)
  }
  throw new Error(`the app didn't start on port ${port}`)
}

if (!existsSync(server)) throw new Error(`no server build at ${server}: go build -tags server -o ${server} .`)
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
      await test(page, { shots: join(shots, name), pg, sql, root })
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
