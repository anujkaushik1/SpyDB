import assert from 'node:assert/strict'
import { test } from 'node:test'
import { zoomAction } from '../src/zoomKeys'

const press = (key: string, code: string, mods: Partial<{ ctrlKey: boolean; metaKey: boolean; altKey: boolean }> = {}) =>
  zoomAction({ key, code, ctrlKey: true, metaKey: false, altKey: false, ...mods })

test('Ctrl with + or =, the keypad + too: zoom in', () => {
  assert.equal(press('=', 'Equal'), 'in')
  assert.equal(press('+', 'Equal'), 'in') // with Shift
  assert.equal(press('+', 'NumpadAdd'), 'in')
})

test('Ctrl with - or _, the keypad - too: zoom out', () => {
  assert.equal(press('-', 'Minus'), 'out')
  assert.equal(press('_', 'Minus'), 'out') // with Shift
  assert.equal(press('-', 'NumpadSubtract'), 'out')
})

test('Ctrl 0, the keypad 0 too: back to 100%', () => {
  assert.equal(press('0', 'Digit0'), 'reset')
  assert.equal(press('Insert', 'Numpad0'), 'reset') // keypad with Num Lock off
})

test('anything else is left alone', () => {
  assert.equal(press('=', 'Equal', { ctrlKey: false }), null, 'no Ctrl')
  assert.equal(press('=', 'Equal', { metaKey: true }), null, 'with ⌘ or the Windows key')
  assert.equal(press('=', 'Equal', { altKey: true }), null, 'with Alt')
  assert.equal(press('p', 'KeyP'), null, 'Ctrl+P is the table switcher')
  assert.equal(press('1', 'Digit1'), null)
})
