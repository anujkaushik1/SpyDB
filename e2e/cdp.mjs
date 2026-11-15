// Drives SpyDB's page in headless Chrome over CDP (Chrome's remote-control
// protocol): clicks, typing and keys as real input events, plus checks.
// Shared by the end-to-end tests (see run.mjs).
import { writeFileSync } from 'node:fs'

export async function open(debugPort, appUrl, { width = 1280, height = 860 } = {}) {
  // Chrome can take a while to answer on a busy machine (a GitHub runner): up to 60 s.
  let targets
  for (let i = 0; i < 200 && !targets?.some((t) => t.type === 'page'); i++) {
    try { targets = await (await fetch(`http://127.0.0.1:${debugPort}/json/list`)).json() } catch { /* not up yet */ }
    if (!targets?.some((t) => t.type === 'page')) await sleep(300)
  }
  const page = targets?.find((t) => t.type === 'page')
  if (!page) throw new Error(`Chrome didn't answer on its debugging port ${debugPort} within 60 s`)
  const ws = new WebSocket(page.webSocketDebuggerUrl)
  await new Promise((r) => ws.addEventListener('open', r, { once: true }))
  let nextId = 0
  const pending = new Map()
  const errors = []
  ws.addEventListener('message', (ev) => {
    const m = JSON.parse(ev.data)
    if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id) }
    if (m.method === 'Runtime.exceptionThrown') errors.push(m.params.exceptionDetails.exception?.description ?? m.params.exceptionDetails.text)
    if (m.method === 'Runtime.consoleAPICalled' && m.params.type === 'error') errors.push('console.error: ' + m.params.args.map((a) => a.value ?? a.description).join(' '))
  })
  const send = (method, params = {}) => new Promise((res) => { const id = ++nextId; pending.set(id, res); ws.send(JSON.stringify({ id, method, params })) })
  const evaluate = async (expression) => {
    const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })
    if (r.result?.exceptionDetails) throw new Error(`${expression}: ${r.result.exceptionDetails.exception?.description}`)
    return r.result?.result?.value
  }
  const center = (js) => evaluate(`(() => { const el = (${js}); if (!el) return null; el.scrollIntoView({ block: 'center', inline: 'nearest' }); const r = el.getBoundingClientRect(); return { x: r.x + r.width / 2, y: r.y + r.height / 2 } })()`)
  const mouse = async (js, { button = 'left', count = 1, modifiers = 0 } = {}) => {
    const r = await center(js)
    if (!r) throw new Error('not found: ' + js)
    for (let c = 1; c <= count; c++) {
      for (const type of ['mousePressed', 'mouseReleased']) {
        await send('Input.dispatchMouseEvent', { type, x: r.x, y: r.y, button, buttons: button === 'right' ? 2 : 1, clickCount: c, modifiers })
      }
    }
    await sleep(250)
  }
  const KEYS = {
    Enter: ['Enter', 13, '\r'], Escape: ['Escape', 27], Tab: ['Tab', 9], Backspace: ['Backspace', 8], ArrowDown: ['ArrowDown', 40], ArrowUp: ['ArrowUp', 38],
  }
  // key('s', META) / key('Enter') …; modifiers: 1 Alt, 2 Ctrl, 4 Meta, 8 Shift.
  const key = async (k, modifiers = 0) => {
    const [code, vk, text] = KEYS[k] ?? [`Key${k.toUpperCase()}`, k.toUpperCase().charCodeAt(0)]
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key: k, code, windowsVirtualKeyCode: vk, modifiers, ...(text && !modifiers ? { text } : {}) })
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key: k, code, windowsVirtualKeyCode: vk, modifiers })
    await sleep(250)
  }
  const type = async (text) => { await send('Input.insertText', { text }); await sleep(150) }
  const waitFor = async (js, ms = 10000) => { for (let t = 0; t < ms; t += 150) { if (await evaluate(`!!(${js})`)) return true; await sleep(150) } return false }
  const shot = async (path) => { const r = await send('Page.captureScreenshot', { format: 'png' }); writeFileSync(path, Buffer.from(r.result.data, 'base64')) }
  await send('Page.enable'); await send('Runtime.enable')
  await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false })
  await send('Page.navigate', { url: appUrl }); await sleep(1500)
  // clickWith(js, SHIFT) / clickWith(js, MOD): a click holding a modifier, as for selecting several rows.
  return { send, evaluate, click: (js) => mouse(js), clickWith: (js, modifiers) => mouse(js, { modifiers }), dblclick: (js) => mouse(js, { count: 2 }), rightClick: (js) => mouse(js, { button: 'right' }), key, type, waitFor, shot, errors, close: () => ws.close() }
}

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
export const META = 4, CTRL = 2, SHIFT = 8
// ⌘ on a Mac, Ctrl elsewhere: what SpyDB's and the editors' shortcuts use.
export const MOD = process.platform === 'darwin' ? META : CTRL

let failed = 0
export function check(name, ok, got) {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${ok ? '' : ` — got: ${JSON.stringify(got)}`}`)
  if (!ok) failed++
  return ok
}
export const failures = () => failed
