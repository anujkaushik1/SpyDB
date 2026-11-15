import assert from 'node:assert/strict'
import { test } from 'node:test'
import { detectMac, detectWindows, fileManager, modKey, shortcut } from '../src/platform'

test('detectMac: macOS WebKit vs Ubuntu WebKitGTK', () => {
  assert.equal(detectMac('MacIntel'), true)
  assert.equal(detectMac('Linux x86_64'), false)
  assert.equal(detectMac('Linux aarch64'), false)
  assert.equal(detectMac('Win32'), false)
  assert.equal(detectMac('', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15'), true)
  assert.equal(detectMac('', 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/605.1.15 (KHTML, like Gecko)'), false)
})

test('detectWindows: WebView2 on Windows, not macOS or Ubuntu', () => {
  assert.equal(detectWindows('Win32'), true)
  assert.equal(detectWindows('MacIntel'), false)
  assert.equal(detectWindows('Linux x86_64'), false)
  assert.equal(detectWindows('', 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0'), true)
  assert.equal(detectWindows('', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15'), false)
})

test('labels: ⌘ and Finder on macOS, Ctrl and folder elsewhere', () => {
  assert.equal(shortcut('S', true), '⌘S')
  assert.equal(shortcut('S', false), 'Ctrl+S')
  assert.equal(shortcut('Enter', false), 'Ctrl+Enter')
  assert.equal(modKey(true), '⌘')
  assert.equal(modKey(false), 'Ctrl')
  assert.equal(fileManager(true), 'Finder')
  assert.equal(fileManager(false, false), 'folder')
  assert.equal(fileManager(false, true), 'File Explorer')
})
