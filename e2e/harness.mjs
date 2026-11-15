// Starts SpyDB's server build and headless Chrome for the end-to-end tests
// (run.mjs) and the benchmark (bench.mjs): a fresh app with an empty
// settings folder of its own, and a fake AWS CLI (fakeaws) that signs in to
// DynamoDB Local with a made-up account. Nothing here touches your saved
// connections, AWS, or any database but the test ones.
import { execFileSync, spawn } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, openSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { delimiter, join, resolve } from 'node:path'
import { sleep } from './cdp.mjs'

export const root = resolve(import.meta.dirname, '..')
export const windows = process.platform === 'win32'
export const exe = windows ? '.exe' : ''
const env = process.env
export const server = resolve(env.E2E_SERVER ?? join(root, 'bin', `spydb-server${exe}`))
export const chrome = env.E2E_CHROME ?? {
  darwin: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  win32: 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
}[process.platform] ?? 'google-chrome'
export const port = Number(env.E2E_PORT ?? 9241)
export const debugPort = port + 1
export const work = mkdtempSync(join(tmpdir(), 'spydb-e2e-'))

// The fake AWS CLI and the profile it signs in with.
export const fakeBin = join(work, 'bin')
execFileSync('go', ['build', '-o', join(fakeBin, `aws${exe}`), './e2e/fakeaws'], { cwd: root, stdio: 'inherit' })
mkdirSync(join(work, 'aws'))
writeFileSync(join(work, 'aws', 'config'), '[profile demo]\nregion = ap-south-1\n')
writeFileSync(join(work, 'aws', 'credentials'), '[demo]\naws_access_key_id = AKIADEMOEXAMPLE\naws_secret_access_key = demo-secret\n')

export function startServer(name) {
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

export function startChrome(name) {
  const flags = ['--headless=new', `--remote-debugging-port=${debugPort}`, `--user-data-dir=${join(work, name, 'chrome')}`,
    '--no-first-run', '--no-default-browser-check', '--disable-extensions', '--window-size=1280,860', 'about:blank']
  if (process.platform === 'linux') flags.unshift('--no-sandbox')
  return spawn(chrome, flags, { stdio: 'ignore' })
}

export async function waitForServer() {
  for (let i = 0; i < 120; i++) {
    try { if ((await fetch(`http://localhost:${port}/`)).ok) return } catch { /* not up yet */ }
    await sleep(250)
  }
  throw new Error(`the app didn't start on port ${port}`)
}


export function checkServerBuild() {
  if (!existsSync(server)) throw new Error(`no server build at ${server}: go build -tags server -o ${server} .`)
}
